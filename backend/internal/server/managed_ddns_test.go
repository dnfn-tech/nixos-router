package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	s.managedDDNSRecord = "s01.example.com"
	h := s.Handler()
	expect(h, "PUT", `{"enabled":false}`, false, "", http.StatusUnauthorized)
	expect(h, "PUT", `{"enabled":false}`, true, "https://elsewhere.example", http.StatusForbidden)
	expect(h, "PUT", `{"enabled":false,"token":"not-accepted"}`, true, "", http.StatusBadRequest)
	expect(h, "PUT", `{}`, true, "", http.StatusBadRequest)
	check := func(w *httptest.ResponseRecorder, enabled bool) {
		t.Helper()
		var result struct {
			Enabled bool   `json:"enabled"`
			Record  string `json:"record"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Enabled != enabled || result.Record != "s01.example.com" {
			t.Fatalf("unexpected state: %s, %v", w.Body, err)
		}
	}
	check(expect(h, "GET", "", true, "", http.StatusOK), true)
	for i := 0; i < 2; i++ {
		check(expect(h, "PUT", `{"enabled":false}`, true, "", http.StatusOK), false)
	}
	reloaded := New(Options{ManagedRuntime: &runtimeStub{}, StateDir: dir, DB: s.db, ManagedDDNSRecord: s.managedDDNSRecord})
	check(expect(reloaded.Handler(), "GET", "", true, "", http.StatusOK), false)
	check(expect(h, "PUT", `{"enabled":true}`, true, "", http.StatusOK), true)
	check(expect(reloaded.Handler(), "GET", "", true, "", http.StatusOK), true)
}
