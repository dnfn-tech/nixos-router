package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

type concurrentRunner struct {
	entered, release chan struct{}
	once             sync.Once
}

func (*concurrentRunner) LookPath(string) error       { return nil }
func (*concurrentRunner) Run(string, ...string) error { return nil }
func (f *concurrentRunner) Output(_ string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "show" {
		if f.entered != nil {
			f.once.Do(func() { close(f.entered); <-f.release })
		}
		return "inactive", nil
	}
	return "", errors.New("mock unit unavailable")
}
func concurrentServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.LAN.StaticLeases = []config.StaticLease{{MAC: "AA:BB:CC:DD:EE:01", IP: "192.168.1.10", Hostname: "client"}}
	database, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateUser("admin", string(hash)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := config.SaveToFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Config: cfg, ConfigPath: path, StateDir: dir, DB: database, DevMode: true})
	s.runner = &concurrentRunner{}
	s.sysClassNet = filepath.Join(dir, "sys")
	for _, name := range []string{cfg.WAN.Interface, cfg.LAN.BridgeName} {
		p := filepath.Join(s.sysClassNet, name, "statistics")
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		for _, counter := range []string{"rx_bytes", "tx_bytes"} {
			if err := os.WriteFile(filepath.Join(p, counter), []byte("42"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s
}
func TestConcurrentManagementRequests(t *testing.T) {
	s := concurrentServer(t)
	h := s.Handler()
	s.cfg.WAN.Mode = "pppoe"
	s.cfg.WAN.PPPoE = &config.PPPoEConfig{Username: "isp", Password: "preserved-secret"}
	if err := config.SaveToFile(s.cfgPath, s.cfg); err != nil {
		t.Fatal(err)
	}
	redacted := s.cfg.RedactedCopy()
	bs, _ := json.Marshal(redacted)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, route := range []struct {
		method, path, body string
		code               int
	}{
		{"POST", "/api/v1/session", `{"username":"admin","password":"secret"}`, 201}, {"POST", "/api/v1/session", `{"username":"admin","password":"secret"}`, 201},
		{"PUT", "/api/v1/config", string(bs), 200}, {"POST", "/api/v1/backup/restore", string(bs), 200}, {"PATCH", "/api/v1/clients/AA:BB:CC:DD:EE:01", `{"hostname":"updated","blocked":true}`, 200}, {"GET", "/api/v1/config", "", 200}, {"GET", "/api/v1/status", "", 200}, {"GET", "/api/v1/status", "", 200}, {"GET", "/api/v1/clients", "", 200}, {"GET", "/api/v1/backup", "", 200}, {"POST", "/api/v1/session", `{"username":"missing","password":"invalid"}`, 401}, {"POST", "/api/v1/session", `{"username":"missing","password":"invalid"}`, 401}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 12; i++ {
				r := httptest.NewRequest(route.method, route.path, bytes.NewBufferString(route.body))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != route.code && !(route.path == "/api/v1/session" && w.Code == 429) {
					t.Errorf("%s %s: %d %s", route.method, route.path, w.Code, w.Body.String())
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	cfg, err := config.LoadFromFile(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.WAN.PPPoE.Password != "preserved-secret" {
		t.Fatal("concurrent restore lost secret")
	}
	if !reflect.DeepEqual(cfg, s.configSnapshot()) {
		t.Fatal("disk and published config differ")
	}
}
func TestConcurrentApplyBusyAndReads(t *testing.T) {
	s := concurrentServer(t)
	s.applyReload = true
	f := &concurrentRunner{entered: make(chan struct{}), release: make(chan struct{})}
	s.runner = f
	h := s.Handler()
	first := make(chan struct{})
	go func() {
		defer close(first)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v1/apply", nil))
	}()
	select {
	case <-f.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("apply did not start")
	}
	defer func() { close(f.release); <-first }()
	for _, route := range []struct {
		method, path string
		code         int
	}{{"POST", "/api/v1/apply", 409}, {"GET", "/api/v1/status", 200}, {"GET", "/api/v1/jobs", 200}} {
		done := make(chan int, 1)
		go func() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
			done <- w.Code
		}()
		select {
		case code := <-done:
			if code != route.code {
				t.Errorf("%s: got %d want %d", route.path, code, route.code)
			}
		case <-time.After(time.Second):
			t.Errorf("%s blocked behind apply", route.path)
		}
	}
}

func TestClientPatchFailureDoesNotPublish(t *testing.T) {
	s := concurrentServer(t)
	before, _ := json.Marshal(s.cfg)
	// A directory at the save destination forces persistence to fail, even as root.
	s.cfgPath = t.TempDir()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("PATCH", "/api/v1/clients/AA:BB:CC:DD:EE:01", bytes.NewBufferString(`{"hostname":"not-saved","blocked":true}`)))
	if w.Code != 500 {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	after, _ := json.Marshal(s.configSnapshot())
	if !bytes.Equal(before, after) {
		t.Fatal("failed mutation changed published configuration")
	}
}
