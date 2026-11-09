package server

import (
	"bytes"
	"encoding/json"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
)

func TestConfigRevisionRejectsConcurrentAndUnconditionalWrites(t *testing.T) {
	s := concurrentServer(t)
	body, _ := json.Marshal(s.configSnapshot())
	get := httptest.NewRecorder()
	s.Handler().ServeHTTP(get, httptest.NewRequest("GET", "/api/v1/config", nil))
	etag := get.Header().Get("ETag")
	if etag == "" {
		t.Fatal("config has no revision validator")
	}
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(body))
			r.Header.Set("If-Match", etag)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	seen := map[int]int{}
	for c := range codes {
		seen[c]++
	}
	if seen[200] != 1 || seen[409] != 1 {
		t.Fatalf("concurrent writes: %v", seen)
	}
	for _, endpoint := range []struct{ method, path, body string }{
		{"PUT", "/api/v1/config", string(body)},
		{"PUT", "/api/v1/plugins/custom", `{"enabled":true}`},
		{"PATCH", "/api/v1/clients/AA:BB:CC:DD:EE:01", `{"blocked":true}`},
		{"POST", "/api/v1/backup/restore", string(body)},
	} {
		for _, validator := range []string{"", etag} {
			before, _ := os.ReadFile(s.cfgPath)
			r := httptest.NewRequest(endpoint.method, endpoint.path, bytes.NewBufferString(endpoint.body))
			if validator != "" {
				r.Header.Set("If-Match", validator)
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			want := 409
			if validator == "" {
				want = 428
			}
			if w.Code != want {
				t.Fatalf("%s validator %q: %d %s", endpoint.path, validator, w.Code, w.Body.String())
			}
			after, _ := os.ReadFile(s.cfgPath)
			if !bytes.Equal(before, after) {
				t.Fatal("conflict changed disk")
			}
		}
	}
	current := s.configSnapshot()
	r := httptest.NewRequest("POST", "/api/v1/backup/restore", bytes.NewReader(body))
	r.Header.Set("If-Match", strconv.Quote(current.Revision))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || s.configSnapshot().Revision == current.Revision {
		t.Fatalf("restore reused revision: %d", w.Code)
	}
}

// Existing handler tests opt in explicitly; conflict tests above supply their
// own captured validators and never use this helper to refresh stale state.
func withConfigRevision(s *Server, r *http.Request) *http.Request {
	if r.Method != "GET" {
		r.Header.Set("If-Match", strconv.Quote(s.configSnapshot().Revision))
	}
	return r
}

func TestConfigurationEnvelopesRejectAmbiguity(t *testing.T) {
	s := concurrentServer(t)
	cfg, _ := json.Marshal(s.configSnapshot())
	for _, endpoint := range []struct{ method, path string }{{"PUT", "/api/v1/config"}, {"POST", "/api/v1/backup/restore"}} {
		for _, raw := range []string{`{"config":` + string(cfg) + `,"config":` + string(cfg) + `}`, `{"config":` + string(cfg) + `,"unknown":"private-secret"}`} {
			r := httptest.NewRequest(endpoint.method, endpoint.path, bytes.NewBufferString(raw))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, withConfigRevision(s, r))
			if w.Code != 400 {
				t.Errorf("ambiguous envelope accepted: %s %d", endpoint.path, w.Code)
			}
		}
	}
}

func TestPartialConfigWritesRejectUnknownAndDuplicateFields(t *testing.T) {
	s := concurrentServer(t)
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/v1/plugins/custom", `{"enabled":true,"enabled":false}`},
		{"PUT", "/api/v1/plugins/custom", `{"enabled":true,"unknown":1}`},
		{"PATCH", "/api/v1/clients/AA:BB:CC:DD:EE:01", `{"hostname":"one","hostname":"two"}`},
	} {
		r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, withConfigRevision(s, r))
		if w.Code != 400 {
			t.Errorf("invalid partial write accepted: %s %d", tc.path, w.Code)
		}
	}
}

func TestApplyKeepsCapturedRevisionDuringNewSave(t *testing.T) {
	s := concurrentServer(t)
	runtime := &runtimeStub{start: make(chan config.Config, 1), release: make(chan struct{}, 1)}
	defer close(runtime.release)
	s.managedRuntime = runtime
	save := func(hostname string) {
		c := cloneConfig(s.configSnapshot())
		c.System.Hostname = hostname
		body, _ := json.Marshal(c)
		r := httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(body))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, withConfigRevision(s, r))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	save("first-revision")
	captured := s.configSnapshot().Revision
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/apply", nil))
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var response struct {
		JobID    string `json:"jobId"`
		Revision string `json:"configRevision"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	execution := <-runtime.start
	save("second-revision")
	if execution.Revision != captured || response.Revision != captured || s.configSnapshot().Revision == captured {
		t.Fatal("apply revision was not immutable")
	}
	runtime.release <- struct{}{}
	s.applyMu.Lock()
	s.applyMu.Unlock()
	job, _, err := s.db.GetJob(response.JobID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Revision string `json:"configRevision"`
	}
	if err := json.Unmarshal([]byte(job.Payload), &result); err != nil || result.Revision != captured {
		t.Fatalf("job revision lost: %+v %v", result, err)
	}
}
