package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestAPIErrorContract(t *testing.T) {
	s := concurrentServer(t)
	c := s.configSnapshot()
	c.System.Hostname = ""
	invalid, _ := json.Marshal(c)
	for _, tc := range []struct {
		method, path, body, origin string
		status                     int
		field                      string
	}{
		{"PUT", "/api/v1/config", string(invalid), "", 400, "system.hostname"},
		{"POST", "/api/v1/backup/restore", string(invalid), "", 400, "system.hostname"},
		{"PUT", "/api/v1/config", "{", "", 400, ""},
		{"DELETE", "/api/v1/config", "", "", 405, ""},
		{"PUT", "/api/v1/config", "{}", "https://untrusted.example", 403, ""},
		{"GET", "/api/v1/not-found", "", "", 404, ""},
		{"PUT", "/api/v1/config", strings.Repeat("x", (16<<20)+1), "", 413, ""},
	} {
		t.Run(tc.method+tc.path+tc.field+strconv.Itoa(tc.status), func(t *testing.T) {
			before, _ := os.ReadFile(s.cfgPath)
			revision := s.configSnapshot().Revision
			r := withConfigRevision(s, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			var body struct {
				Code, Message string
				FieldErrors   map[string]string
			}
			err := json.Unmarshal(w.Body.Bytes(), &body)
			if w.Code != tc.status || err != nil || body.Code == "" || body.Message == "" || body.FieldErrors == nil || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("response %d: %.500s", w.Code, w.Body.String())
			}
			if tc.field != "" && body.FieldErrors[tc.field] == "" {
				t.Fatalf("missing field %s: %+v", tc.field, body)
			}
			after, _ := os.ReadFile(s.cfgPath)
			if !bytes.Equal(before, after) || revision != s.configSnapshot().Revision {
				t.Fatal("failed request mutated config")
			}
		})
	}
}

func TestAPIErrorBoundaries(t *testing.T) {
	s := concurrentServer(t)
	assertError := func(w *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		var response struct {
			Code, Message string
			FieldErrors   map[string]string
		}
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || w.Code != status || response.Code != code || response.Message == "" || response.FieldErrors == nil {
			t.Fatalf("invalid error %d: %.500s", w.Code, w.Body.String())
		}
	}
	t.Run("authentication precedes body allocation", func(t *testing.T) {
		s.requireAuth = true
		defer func() { s.requireAuth = false }()
		r := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader("{}"))
		r.ContentLength = 17 << 20
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		assertError(w, 401, "unauthorized")
	})
	t.Run("chunked oversized body", func(t *testing.T) {
		r := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(strings.Repeat("x", (16<<20)+1)))
		r.ContentLength = -1
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		assertError(w, 413, "request_entity_too_large")
	})
	t.Run("storage failure", func(t *testing.T) {
		old := s.cfgPath
		s.cfgPath = t.TempDir()
		defer func() { s.cfgPath = old }()
		c := s.configSnapshot()
		body, _ := json.Marshal(c)
		r := withConfigRevision(s, httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(body)))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		assertError(w, 500, "internal_server_error")
		if strings.Contains(w.Body.String(), s.cfgPath) || s.configSnapshot().Revision != c.Revision {
			t.Fatal("storage failure exposed path or mutated revision")
		}
	})
	t.Run("legacy response metadata", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeJSON(w, 409, map[string]interface{}{"error": "apply_busy", "jobId": "existing-job"})
		assertError(w, 409, "apply_busy")
		var body map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &body)
		if body["error"] != "apply_busy" || body["jobId"] != "existing-job" {
			t.Fatal("legacy metadata lost")
		}
	})
}

func TestManagedValidationKeepsLegacyErrorCode(t *testing.T) {
	s := concurrentServer(t)
	s.managedRuntime = &runtimeStub{}
	s.cfg.System.Hostname = ""
	data, _ := json.Marshal(s.cfg)
	if err := os.WriteFile(s.cfgPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/apply", nil))
	var body struct {
		Code, Error string
		FieldErrors map[string]string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 400 || body.Code != "invalid_config" || body.Error != "invalid_config" || body.FieldErrors["system.hostname"] == "" {
		t.Fatalf("legacy error contract changed: %d %s", w.Code, w.Body.String())
	}
}
