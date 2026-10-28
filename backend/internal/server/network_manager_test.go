package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type managerRunner struct {
	fakeRunner
	unit, state string
	err         error
	queries     int
}

func (f *managerRunner) Output(name string, args ...string) (string, error) {
	f.queries++
	if name != "systemctl" || len(args) != 4 || args[0] != "show" || args[1] != "--property=ActiveState" || args[2] != "--value" {
		return "", errors.New("unexpected query")
	}
	if args[3] == f.unit {
		return f.state, f.err
	}
	return "inactive", nil
}
func TestApplyNetworkManagerConflicts(t *testing.T) {
	for _, unit := range []string{"NetworkManager.service", "systemd-networkd.service", "dhcpcd.service", "connman.service"} {
		for _, state := range []string{"active", "activating", "deactivating", "reloading", "failed", ""} {
			t.Run(unit+"/"+state, func(t *testing.T) {
				srv, dir, cookies, cleanup := setupApplyEnv(t, true)
				defer cleanup()
				srv.applyReload = true
				runner := &managerRunner{unit: unit, state: state}
				srv.runner = runner
				gen := filepath.Join(dir, "generated")
				if err := os.MkdirAll(gen, 0755); err != nil {
					t.Fatal(err)
				}
				sentinel := filepath.Join(gen, "dnsmasq.conf.fragment")
				if err := os.WriteFile(sentinel, []byte("previous config"), 0600); err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", nil)
				for _, cookie := range cookies {
					req.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				srv.Handler().ServeHTTP(w, req)
				if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), unit) {
					t.Fatalf("want conflict naming unit, got %d %s", w.Code, w.Body.String())
				}
				if len(runner.cmds) != 0 {
					t.Fatalf("mutated runtime despite conflict: %v", runner.cmds)
				}
				got, err := os.ReadFile(sentinel)
				if err != nil || string(got) != "previous config" {
					t.Fatalf("generated config overwritten: %q %v", got, err)
				}
			})
		}
	}
}
func TestApplyNetworkManagerUnavailable(t *testing.T) {
	srv, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	srv.applyReload = true
	runner := &managerRunner{unit: "NetworkManager.service", err: errors.New("bus unavailable")}
	srv.runner = runner
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "network_manager_check_failed") || len(runner.cmds) != 0 {
		t.Fatalf("unknown ownership must refuse apply: %d %s commands=%v", w.Code, w.Body.String(), runner.cmds)
	}
}
func TestApplyNetworkManagerGenerateOnly(t *testing.T) {
	srv, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	runner := &managerRunner{unit: "NetworkManager.service", state: "active"}
	srv.runner = runner
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || runner.queries != 0 || len(runner.cmds) != 0 {
		t.Fatalf("preview must work without querying host managers: %d %s", w.Code, w.Body.String())
	}
}
