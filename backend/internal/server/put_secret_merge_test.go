package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
)

// Test that PUT with redacted secrets preserves existing PPPoE/WiFi/DDNS secrets
func TestPutConfigPreservesSecrets(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	old := config.DefaultConfig()
	old.WAN.Mode = "pppoe"
	old.WAN.PPPoE = &config.PPPoEConfig{Username: "u", Password: "pppoe-secret"}
	old.WiFi.Enable = true
	old.WiFi.APs = []config.WiFiAP{{SSID: "main", Enable: true, PSK: "wifi-secret"}}
	old.DDNS.Enable = true
	old.DDNS.Provider = "duckdns"
	old.DDNS.DuckDNS = &config.DuckDNSConfig{Token: "duck-token", Domain: "example"}
	if err := config.SaveToFile(cfgPath, old); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	srv := New(Options{
		Config:     old,
		ConfigPath: cfgPath,
		DB:         database,
		DevMode:    true, // bypass auth for test convenience
	})

	// Incoming replaces values but uses redacted placeholders
	inc := old.RedactedCopy()
	inc.System.Hostname = "changed"
	buf, _ := json.Marshal(map[string]any{"config": inc})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("put failed: %d %s", w.Code, w.Body.String())
	}
	// Load file and verify secrets unchanged
	loaded, err := config.LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.WAN.PPPoE == nil || loaded.WAN.PPPoE.Password != "pppoe-secret" {
		t.Fatalf("pppoe password lost")
	}
	if loaded.WiFi.APs[0].PSK != "wifi-secret" {
		t.Fatalf("wifi psk lost: %q", loaded.WiFi.APs[0].PSK)
	}
	if loaded.DDNS.DuckDNS == nil || loaded.DDNS.DuckDNS.Token != "duck-token" {
		t.Fatalf("ddns token lost")
	}
}

