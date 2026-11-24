package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedDDNSControlPersistsWithoutNetworkApply(t *testing.T) {
	s, dir, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	s.managedRuntime = &runtimeStub{}
	s.stateDir = dir
	request := func(handler http.Handler, method, body string, auth bool, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/ddns", strings.NewReader(body))
		if auth {
			for _, cookie := range cookies {
				r.AddCookie(cookie)
			}
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	expect := func(handler http.Handler, method, body string, auth bool, origin string, code int) *httptest.ResponseRecorder {
		t.Helper()
		w := request(handler, method, body, auth, origin)
		if w.Code != code {
			t.Fatalf("%s got %d, want %d: %s", method, w.Code, code, w.Body)
		}
		return w
	}
	expect(s.Handler(), "GET", "", true, "", http.StatusNotFound)
	s.managedDDNSConfig = ManagedDDNSConfig{Provider: "Example DNS", Record: "s01.example.com",
		RecordType: "AAAA", Interface: "ppp-test", IntervalMinutes: 10}
	h := s.Handler()
	expect(h, "PUT", `{"enabled":false}`, false, "", http.StatusUnauthorized)
	expect(h, "PUT", `{"enabled":false}`, true, "https://elsewhere.example", http.StatusForbidden)
	expect(h, "PUT", `{"enabled":false,"token":"not-accepted"}`, true, "", http.StatusBadRequest)
	expect(h, "PUT", `{}`, true, "", http.StatusBadRequest)
	check := func(w *httptest.ResponseRecorder, enabled bool) {
		t.Helper()
		var result struct {
			Enabled bool `json:"enabled"`
			ManagedDDNSConfig
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Enabled != enabled || result.ManagedDDNSConfig != s.managedDDNSConfig {
			t.Fatalf("unexpected state: %s, %v", w.Body, err)
		}
	}
	check(expect(h, "GET", "", true, "", http.StatusOK), true)
	for i := 0; i < 2; i++ {
		check(expect(h, "PUT", `{"enabled":false}`, true, "", http.StatusOK), false)
	}
	reloaded := New(Options{ManagedRuntime: &runtimeStub{}, StateDir: dir, DB: s.db, ManagedDDNSConfig: s.managedDDNSConfig})
	check(expect(reloaded.Handler(), "GET", "", true, "", http.StatusOK), false)
	check(expect(h, "PUT", `{"enabled":true}`, true, "", http.StatusOK), true)
	check(expect(reloaded.Handler(), "GET", "", true, "", http.StatusOK), true)
}

func TestManagedDDNSRuntimeSettings(t *testing.T) {
	s, dir, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	s.managedRuntime, s.stateDir = &runtimeStub{}, dir
	settings, err := json.Marshal(map[string]any{
		"provider": "Cloudflare", "record": "initial.example.net", "settingsEditable": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(settings, &s.managedDDNSConfig); err != nil {
		t.Fatal(err)
	}
	const token = "cf-mock-sensitive-token"
	const updated = `{"record":"edge.example.net","zoneId":"0123456789abcdef0123456789abcdef","apiToken":"` + token + `"}`
	h := s.Handler()
	request := func(method, body, addr string, authenticated bool, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/ddns", strings.NewReader(body))
		r.RemoteAddr = addr
		if authenticated {
			for _, cookie := range cookies {
				r.AddCookie(cookie)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want || strings.Contains(w.Body.String(), token) {
			t.Fatalf("%s got status %d, want %d, response contains secret: %v", method, w.Code, want, strings.Contains(w.Body.String(), token))
		}
		return w.Body.String()
	}
	request("PATCH", updated, "127.0.0.1:9999", false, 401)
	request("PATCH", updated, "192.0.2.3:9999", true, 403)
	request("PATCH", `{"record":"bad..example.net"}`, "127.0.0.1:9999", true, 400)
	request("PATCH", updated, "127.0.0.1:9999", true, 200)
	h = New(Options{ManagedRuntime: &runtimeStub{}, StateDir: dir, DB: s.db, ManagedDDNSConfig: s.managedDDNSConfig}).Handler()
	body := request("GET", "", "127.0.0.1:9999", true, 200)
	if !strings.Contains(body, `"record":"edge.example.net"`) || !strings.Contains(body, `"tokenConfigured":true`) {
		t.Fatal("saved settings missing after restart")
	}
	request("PATCH", `{"record":"new.example.net"}`, "127.0.0.1:9999", true, 200)
	path := filepath.Join(dir, "ddns-settings.json")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), token) || !strings.Contains(string(data), "new.example.net") {
		t.Fatal("domain update failed to preserve credential")
	}
	if meta, err := os.Stat(path); err != nil || meta.Mode().Perm() != 0600 {
		t.Fatal("settings file should be private")
	}
}
