package server

import (
	"encoding/json"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPluginManifestReportsCapabilityNotJustIntent(t *testing.T) {
	s, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var body struct {
		Plugins []struct {
			ID               string `json:"id"`
			Ownership        string `json:"ownership"`
			Available        *bool  `json:"available"`
			RuntimeSupported bool   `json:"runtimeSupported"`
			Status           string `json:"status"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range body.Plugins {
		if item.ID == "adblock" {
			found = true
			if item.Available == nil || !*item.Available || item.RuntimeSupported || item.Status != "not-applied" {
				t.Fatalf("incorrect generated-only capability: %+v", item)
			}
		}
		if item.ID == "tailscale" && (item.Available == nil || *item.Available || item.Ownership != "system") {
			t.Fatalf("system module incorrectly available: %+v", item)
		}
	}
	if !found {
		t.Fatal("missing module")
	}
}

func TestConfigWriteRejectsRetiredSSHRegardlessOfVersion(t *testing.T) {
	for _, raw := range []string{`{"ssh":{"enable":true}}`, `{"config":{"schemaVersion":1,"ssh":{"enable":true}},"clearSecrets":[]}`} {
		if _, _, err := decodeConfigWrite([]byte(raw)); err == nil {
			t.Fatal("retired field accepted")
		}
	}
}

func TestAdBlockManifestIncludesSchema(t *testing.T) {
	s, _, _, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	for _, m := range s.moduleManifest() {
		if m["id"] == "adblock" {
			if m["configSchema"] == nil {
				t.Fatal("module schema missing")
			}
			return
		}
	}
	t.Fatal("module missing")
}

func TestDisabledModuleEditsStillValidateNewData(t *testing.T) {
	s, _, _, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	for _, id := range []string{"../new", "adblock"} {
		candidate := s.configSnapshot()
		candidate.Plugins = map[string]config.PluginConfig{id: {Config: map[string]interface{}{"entries": "invalid-shape"}}}
		w := httptest.NewRecorder()
		if err := s.saveConfigLocked(w, &candidate); err == nil {
			t.Fatalf("new invalid disabled module accepted: %s", id)
		}
	}
	historical := s.configSnapshot()
	historical.Plugins = map[string]config.PluginConfig{"adblock": {Config: map[string]interface{}{"lists": []interface{}{"https://historical.example/list"}}}}
	s.cfg = historical
	candidate := cloneConfig(historical)
	if err := s.saveConfigLocked(httptest.NewRecorder(), &candidate); err != nil {
		t.Fatal("unchanged historical disabled data rejected", err)
	}
}
