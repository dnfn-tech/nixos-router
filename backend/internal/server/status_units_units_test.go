package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// fakeRunnerStatus supports Output for `systemctl is-active <unit>`
type fakeRunnerStatus struct {
	missingSystemctl bool
	byUnit           map[string]string // unit -> is-active stdout
}

func (f *fakeRunnerStatus) LookPath(name string) error {
	if name == "systemctl" && f.missingSystemctl {
		return assertErr
	}
	return nil
}
func (f *fakeRunnerStatus) Run(name string, args ...string) error { return nil }
func (f *fakeRunnerStatus) Output(name string, args ...string) (string, error) {
	if name != "systemctl" || len(args) < 2 || args[0] != "is-active" {
		return "", nil
	}
	u := args[1]
	out := f.byUnit[u]
	if out == "" {
		// Simulate not-found
		return "", assertErr
	}
	// When explicitly set to "not-found", also simulate error
	if strings.EqualFold(out, "not-found") {
		return out, assertErr
	}
	return out, nil
}

// assertErr is a sentinel error used to simulate failures/not-found.
type sentinelErr struct{}

func (sentinelErr) Error() string { return "sentinel" }

var assertErr error = sentinelErr{}

func TestStatusUnits_SystemctlMissing(t *testing.T) {
	cfg := config.DefaultConfig()
	srv := New(Options{Config: cfg, DevMode: true})
	srv.runner = &fakeRunnerStatus{missingSystemctl: true}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	srcs, _ := body["sources"].(map[string]interface{})
	if srcs == nil || srcs["units"] != "stub" {
		t.Fatalf("expected sources.units=stub, got %v", srcs)
	}
	units, _ := body["units"].([]interface{})
	if len(units) == 0 {
		t.Fatalf("expected units list present even when stub")
	}
	// Check one core entry exists
	foundDNS := false
	for _, it := range units {
		m, _ := it.(map[string]interface{})
		if m["id"] == "dnsmasq" {
			foundDNS = true
			if st, _ := m["state"].(string); st == "" {
				t.Fatalf("expected state for dnsmasq")
			}
		}
	}
	if !foundDNS {
		t.Fatalf("dnsmasq not found in units")
	}
}

func TestStatusUnits_StateMapping(t *testing.T) {
	cfg := config.DefaultConfig()
	// Make tailscale headscale relevant
	cfg.Plugins = map[string]config.PluginConfig{
		"tailscale": {Enable: true, Config: map[string]interface{}{"controlPlane": "headscale"}},
	}
	srv := New(Options{Config: cfg, DevMode: true})
	srv.runner = &fakeRunnerStatus{
		byUnit: map[string]string{
			"dnsmasq.service":      "active",
			"nftables.service":     "failed",
			"hostapd.service":      "inactive",
			"tailscaled.service":   "active",
			"headscale.service":    "not-found", // simulate explicit not-found
			"zerotier-one.service": "activating",
			// mihomo not provided -> treated as missing
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	srcs, _ := body["sources"].(map[string]interface{})
	if srcs == nil || srcs["units"] != "systemctl" {
		t.Fatalf("expected sources.units=systemctl, got %v", srcs)
	}
	// Build map id->state
	items, _ := body["units"].([]interface{})
	m := map[string]string{}
	for _, it := range items {
		row, _ := it.(map[string]interface{})
		if row == nil {
			continue
		}
		id, _ := row["id"].(string)
		st, _ := row["state"].(string)
		if id != "" {
			m[id] = st
		}
	}
	want := map[string]string{
		"dnsmasq":      "active",
		"nftables":     "failed",
		"hostapd":      "inactive",
		"mihomo":       "missing",
		"tailscaled":   "active",
		"headscale":    "missing",
		"zerotier-one": "unknown", // activating -> unknown
	}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("unit %s: want %s, got %s", k, v, m[k])
		}
	}
}

