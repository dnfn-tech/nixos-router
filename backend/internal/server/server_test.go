package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"path/filepath"
	"os"
)

func TestHealth(t *testing.T) {
	srv := New(Options{
		Config:  config.DefaultConfig(),
		Version: "test",
		DevMode: true,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("expected ok=true")
	}
	if v, _ := body["version"].(string); v != "test" {
		t.Fatalf("version mismatch, got %v", v)
	}
}

func TestClientsEndpoint(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.LAN.StaticLeases = []config.StaticLease{{MAC: "AA:BB:CC:DD:EE:FF", IP: "192.168.1.50", Hostname: "pc"}}
	srv := New(Options{Config: cfg, DevMode: true})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if _, ok := body["clients"]; !ok {
		t.Fatalf("missing clients")
	}
}

func TestClientsFromLeases(t *testing.T) {
	cfg := config.DefaultConfig()
	srv := New(Options{Config: cfg, DevMode: true, StateDir: t.TempDir()})
	// Create a leases file and point env to it
	leases := "1999999999 aa:bb:cc:dd:ee:ff 192.168.1.101 host-a *\n1999999999 11:22:33:44:55:66 192.168.1.102 host-b *\n"
	path := filepath.Join(t.TempDir(), "dnsmasq.leases")
	if err := os.WriteFile(path, []byte(leases), 0o644); err != nil {
		t.Fatalf("write leases: %v", err)
	}
	t.Setenv("NIXOS_ROUTER_DNSMASQ_LEASES", path)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	clients, _ := body["clients"].([]interface{})
	if len(clients) != 2 {
		t.Fatalf("expected 2 clients from leases, got %d", len(clients))
	}
}

