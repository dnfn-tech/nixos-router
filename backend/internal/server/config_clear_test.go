package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestExplicitSecretClearingIsAtomicAndConditional(t *testing.T) {
	s := concurrentServer(t)
	c := s.configSnapshot()
	c.WAN.PPPoE = &config.PPPoEConfig{Username: "user", Password: "wan-secret"}
	c.Plugins = map[string]config.PluginConfig{"custom.with/slash": {Config: map[string]interface{}{"api.token": "plugin-secret", "label": "keep"}}}
	put := func(body interface{}, stale bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := withConfigRevision(s, httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(raw)))
		if stale {
			r.Header.Set("If-Match", `"outdated"`)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := put(c, false); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// Empty fields retain; clearing is a separate request instruction.
	candidate := redactedSnapshot(s)
	candidate.WAN.PPPoE.Password = ""
	if w := put(candidate, false); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if s.configSnapshot().WAN.PPPoE.Password != "wan-secret" {
		t.Fatal("blank cleared password")
	}
	for _, tc := range []struct {
		paths  [][]string
		stale  bool
		status int
	}{
		{[][]string{{"wan", "pppoe", "password"}}, true, 409},
		{[][]string{{"wan", "pppoe", "password"}, {"system", "hostname"}}, false, 400},
		{[][]string{{"plugins", "custom.with/slash", "config", "label"}}, false, 400},
		{[][]string{{"wifi", "aps", "99", "psk"}}, false, 400},
		{[][]string{{"wan", "pppoe", "password"}, {"wan", "pppoe", "password"}}, false, 400},
	} {
		before, _ := os.ReadFile(s.cfgPath)
		revision := s.configSnapshot().Revision
		w := put(map[string]interface{}{"config": redactedSnapshot(s), "clearSecrets": tc.paths}, tc.stale)
		if w.Code != tc.status {
			t.Fatalf("clear rejection: %d want %d", w.Code, tc.status)
		}
		after, _ := os.ReadFile(s.cfgPath)
		if !bytes.Equal(before, after) || s.configSnapshot().Revision != revision {
			t.Fatal("rejected clear modified draft")
		}
	}
	paths := [][]string{{"wan", "pppoe", "password"}, {"plugins", "custom.with/slash", "config", "api.token"}}
	if w := put(map[string]interface{}{"config": redactedSnapshot(s), "clearSecrets": paths}, false); w.Code != 200 {
		t.Fatalf("explicit clear rejected: %d %s", w.Code, w.Body.String())
	}
	actual, err := config.LoadFromFile(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if actual.WAN.PPPoE.Password != "" || actual.Plugins["custom.with/slash"].Config["api.token"] != "" {
		t.Fatal("explicit clear not persisted")
	}
}

func TestClearingRequiredSecretFailsValidation(t *testing.T) {
	s := concurrentServer(t)
	c := s.configSnapshot()
	c.WAN.Mode = "pppoe"
	c.WAN.PPPoE = &config.PPPoEConfig{Username: "user", Password: "keep-secret"}
	raw, _ := json.Marshal(c)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, withConfigRevision(s, httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(raw))))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	before, _ := os.ReadFile(s.cfgPath)
	raw, _ = json.Marshal(map[string]interface{}{"config": redactedSnapshot(s), "clearSecrets": [][]string{{"wan", "pppoe", "password"}}})
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, withConfigRevision(s, httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(raw))))
	var response struct{ FieldErrors map[string]string }
	json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != 400 || response.FieldErrors["wan.pppoe.password"] == "" {
		t.Fatalf("required secret not validated: %d %s", w.Code, w.Body.String())
	}
	after, _ := os.ReadFile(s.cfgPath)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid clear changed disk")
	}
}

func redactedSnapshot(s *Server) config.Config { c := s.configSnapshot(); return c.RedactedCopy() }
