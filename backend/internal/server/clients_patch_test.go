package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestPatchClientHostnameAndBlock(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.LAN.StaticLeases = []config.StaticLease{{MAC: "AA:BB:CC:DD:EE:01", IP: "192.168.1.10"}}
	srv := New(Options{Config: cfg, DevMode: true, StateDir: t.TempDir(), ConfigPath: filepath.Join(t.TempDir(), "config.json")})
	_ = config.SaveToFile(srv.cfgPath, cfg)

	// Set hostname
	body := map[string]interface{}{"hostname": "pc-01"}
	bs, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/clients/AA%3ABB%3ACC%3ADD%3AEE%3A01", bytes.NewReader(bs))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch hostname failed: %d %s", w.Code, w.Body.String())
	}
	// Block client
	body2 := map[string]interface{}{"blocked": true}
	bs2, _ := json.Marshal(body2)
	req2 := httptest.NewRequest(http.MethodPatch, "/api/v1/clients/AA%3ABB%3ACC%3ADD%3AEE%3A01", bytes.NewReader(bs2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("patch block failed: %d %s", w2.Code, w2.Body.String())
	}
	// GET clients should show blocked and hostname
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	w3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("get clients failed: %d", w3.Code)
	}
	var body3 map[string]interface{}
	_ = json.Unmarshal(w3.Body.Bytes(), &body3)
	arr := body3["clients"].([]interface{})
	found := false
	for _, it := range arr {
		m := it.(map[string]interface{})
		if m["mac"] == "AA:BB:CC:DD:EE:01" {
			found = true
			if m["hostname"] != "pc-01" {
				t.Fatalf("hostname not updated: %v", m["hostname"])
			}
			if blk, _ := m["blocked"].(bool); !blk {
				t.Fatalf("blocked flag not set")
			}
		}
	}
	if !found {
		t.Fatalf("client not found after patch")
	}
}

