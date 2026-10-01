package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
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

