package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	srv.Handler().ServeHTTP(w, withConfigRevision(srv, req))
	if w.Code != http.StatusOK {
		t.Fatalf("patch hostname failed: %d %s", w.Code, w.Body.String())
	}
	// Block client
	body2 := map[string]interface{}{"blocked": true}
	bs2, _ := json.Marshal(body2)
	req2 := httptest.NewRequest(http.MethodPatch, "/api/v1/clients/AA%3ABB%3ACC%3ADD%3AEE%3A01", bytes.NewReader(bs2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, withConfigRevision(srv, req2))
	if w2.Code != http.StatusOK {
		t.Fatalf("patch block failed: %d %s", w2.Code, w2.Body.String())
	}
	// GET clients should show blocked and hostname
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	w3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w3, withConfigRevision(srv, req3))
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

func TestPatchIndependentWiFiClient(t *testing.T) {
	for _, body := range []string{`{"hostname":"wifi-new"}`, `{"blocked":true}`, `{"hostname":"wifi-new","blocked":true}`} {
		t.Run(body, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.WiFi.Enable = true
			cfg.WiFi.BridgeToLan = false
			cfg.WiFi.APs = []config.WiFiAP{{SSID: "test", PSK: "password123", Enable: true, Band: "2g", Channel: 1}}
			cfg.WiFi.Network = &config.WiFiNetwork{IPv4CIDR: "192.168.2.1/24", StaticLeases: []config.StaticLease{{MAC: "02:00:00:00:00:01", IP: "192.168.2.10", Hostname: "wifi-old"}}}
			cfg.LAN.StaticLeases = []config.StaticLease{{MAC: "02:00:00:00:00:02", IP: "192.168.1.10", Hostname: "wired"}}
			dir := t.TempDir()
			srv := New(Options{Config: cfg, DevMode: true, StateDir: dir, ConfigPath: filepath.Join(dir, "config.json")})
			if err := config.SaveToFile(srv.cfgPath, cfg); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPatch, "/api/v1/clients/02:00:00:00:00:01", strings.NewReader(body))
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, withConfigRevision(srv, r))
			if w.Code != http.StatusOK {
				t.Fatalf("WiFi client patch: %d %s", w.Code, w.Body.String())
			}
			saved, err := config.LoadFromFile(srv.cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(saved.LAN.StaticLeases, cfg.LAN.StaticLeases) || saved.WiFi.Network.StaticLeases[0].IP != "192.168.2.10" {
				t.Fatal("WiFi patch changed wired leases or the WiFi address")
			}
			if strings.Contains(body, "hostname") && saved.WiFi.Network.StaticLeases[0].Hostname != "wifi-new" {
				t.Fatal("WiFi hostname was not persisted")
			}
			if strings.Contains(body, "blocked") && !isMacBlocked(saved, "02:00:00:00:00:01") {
				t.Fatal("WiFi block was not persisted")
			}
			// With no live observer, the client list must still include WiFi static leases.
			w = httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil))
			var list struct {
				Clients []struct {
					IP, Hostname string
					Blocked      bool
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, client := range list.Clients {
				if client.IP == "192.168.2.10" {
					found = true
					if client.Hostname != saved.WiFi.Network.StaticLeases[0].Hostname || client.Blocked != isMacBlocked(saved, "02:00:00:00:00:01") {
						t.Fatal("WiFi client list disagrees with the saved patch")
					}
				}
			}
			if !found {
				t.Fatal("WiFi client missing from static fallback")
			}
		})
	}
}

func TestPatchBlockWithoutStaticLease(t *testing.T) {
	cfg := config.DefaultConfig()
	dir := t.TempDir()
	srv := New(Options{Config: cfg, DevMode: true, StateDir: dir, ConfigPath: filepath.Join(dir, "config.json")})
	if err := config.SaveToFile(srv.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"blocked":true}`, `{"blocked":false}`} {
		r := httptest.NewRequest(http.MethodPatch, "/api/v1/clients/02:00:00:00:00:03", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, withConfigRevision(srv, r))
		if w.Code != http.StatusOK {
			t.Fatalf("block-only patch: %d %s", w.Code, w.Body.String())
		}
		saved, err := config.LoadFromFile(srv.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(saved.LAN.StaticLeases) != 0 || isMacBlocked(saved, "02:00:00:00:00:03") != strings.Contains(body, "true") {
			t.Fatal("block-only patch created a lease or lost the block state")
		}
	}
	before, err := os.ReadFile(srv.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/clients/02:00:00:00:00:03", strings.NewReader(`{"hostname":"unknown"}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, withConfigRevision(srv, r))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("rename without a lease: %d", w.Code)
	}
	after, err := os.ReadFile(srv.cfgPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected rename changed saved configuration")
	}
}
