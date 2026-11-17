package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"github.com/dnfn-tech/nixos-router/backend/internal/privsep"
)

type remoteTestRunner struct{}

func (remoteTestRunner) Run(context.Context, string, ...string) (string, error) { return "[]", nil }
func remoteClient(t *testing.T, leaseData ...string) *privsep.Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "router-remote-")
	if err != nil {
		t.Fatal(err)
	}
	if len(leaseData) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "dnsmasq.leases"), []byte(leaseData[0]), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := managed.New(managed.Options{StateDir: dir, Interfaces: []string{"wan0", "lan0"}, ManagementPort: 8080, Runner: remoteTestRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := privsep.OpenExecutor(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "executor.sock")
	l, err := privsep.Listen(path, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.Serve(l) }()
	c := privsep.NewClient(path, uint32(os.Geteuid()))
	t.Cleanup(func() { c.Close(); e.Close(); l.Close(); <-done; os.RemoveAll(dir) })
	return c
}
func TestRemoteManagedApplySharesAPIJobID(t *testing.T) {
	s, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	c := remoteClient(t)
	s.managedRuntime = c
	// Deliberately foreign default interface: real manager rejects without commands.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/apply", nil)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	s.Handler().ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var response struct {
		JobID string `json:"jobId"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	s.applyMu.Lock()
	s.applyMu.Unlock()
	j, err := c.Job(context.Background(), response.JobID)
	if err != nil || j.Status != "failed" {
		t.Fatalf("API/executor ID diverged: %+v %v", j, err)
	}
}
func TestRemoteJobReconcilesAfterAPIRecreation(t *testing.T) {
	s, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	c := remoteClient(t)
	cfg := config.DefaultConfig() // manager refuses unowned WAN, no network command.
	if _, err := c.Submit(context.Background(), "resume-job", cfg); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); ; {
		j, err := c.Job(context.Background(), "resume-job")
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job pending")
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.db.CreateJob("resume-job", "apply", `{"mode":"managed-runtime","executor":true,"stage":"queued"}`); err != nil {
		t.Fatal(err)
	}
	// New API server has no goroutine from the original submission.
	fresh := New(Options{Config: s.cfg, ConfigPath: s.cfgPath, StateDir: s.stateDir, DB: s.db, ManagedRuntime: c})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/jobs/resume-job", nil)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	fresh.Handler().ServeHTTP(w, r)
	var j struct {
		Status  string `json:"status"`
		Payload string `json:"payload"`
	}
	json.Unmarshal(w.Body.Bytes(), &j)
	if w.Code != 200 || j.Status != "failed" {
		t.Fatalf("stale API job: %d %s", w.Code, w.Body.String())
	}
	var result map[string]any
	json.Unmarshal([]byte(j.Payload), &result)
	if result["stage"] != "validate" || result["appliedRuntime"] != false {
		t.Fatalf("lost executor result: %v", result)
	}
	stored, _, err := s.db.GetJob("resume-job")
	if err != nil || stored.Status != "failed" {
		t.Fatal("reconciled result not persisted")
	}
}
func TestRemoteJobUnavailableIsNotTerminal(t *testing.T) {
	s, dir, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	c := privsep.NewClient(filepath.Join(dir, "missing.sock"), uint32(os.Geteuid()))
	defer c.Close()
	s.managedRuntime = c
	if err := s.db.CreateJob("unknown-job", "apply", `{"mode":"managed-runtime","executor":true}`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/jobs/unknown-job", nil)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	s.Handler().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("missing executor treated as known result: %d %s", w.Code, w.Body.String())
	}
	j, _, _ := s.db.GetJob("unknown-job")
	if j.Status != "running" {
		t.Fatalf("unknown outcome became %s", j.Status)
	}
}

// A crash after the API journal insert but before RPC submission must remain
// observable without replaying the configuration or hiding the job history.
func TestRemoteUnacceptedJobRemainsObservable(t *testing.T) {
	s, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	c := remoteClient(t)
	s.managedRuntime = c
	if err := s.db.CreateJob("orphan", "apply", `{"executor":true}`); err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := get("/api/v1/jobs/orphan")
	var item struct {
		Status  string `json:"status"`
		Payload string `json:"payload"`
	}
	json.Unmarshal(w.Body.Bytes(), &item)
	if w.Code != 200 || item.Status != "unknown" {
		t.Fatalf("unaccepted job hidden: %d %s", w.Code, w.Body.String())
	}
	var payload map[string]any
	json.Unmarshal([]byte(item.Payload), &payload)
	if payload["executorStatus"] != "not-found" || payload["stage"] != "outcome-unknown" {
		t.Fatalf("missing uncertainty: %v", payload)
	}
	stored, _, _ := s.db.GetJob("orphan")
	if stored.Status != "running" {
		t.Fatal("observation overwrote durable job state")
	}
	if w = get("/api/v1/jobs"); w.Code != 200 {
		t.Fatalf("history hidden: %s", w.Body.String())
	}
	// A late submission with this same ID can still be reconciled normally.
	if _, err := c.Submit(context.Background(), "orphan", config.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); ; {
		w = get("/api/v1/jobs/orphan")
		json.Unmarshal(w.Body.Bytes(), &item)
		if item.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("late result not reconciled: %s", w.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRemoteOutageDoesNotHideHistory(t *testing.T) {
	s, dir, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	c := privsep.NewClient(filepath.Join(dir, "missing.sock"), uint32(os.Geteuid()))
	defer c.Close()
	s.managedRuntime = c
	if err := s.db.CreateJob("unknown", "apply", `{"executor":true}`); err != nil {
		t.Fatal(err)
	}
	if err := s.db.CreateJob("completed", "apply", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := s.db.FinishJob("completed", "success", `{}`, ""); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/jobs", nil)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	s.Handler().ServeHTTP(w, r)
	var result struct {
		Jobs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"jobs"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Jobs) != 2 {
		t.Fatalf("history hidden: %d %s", w.Code, w.Body.String())
	}
	for _, j := range result.Jobs {
		if j.ID == "completed" && j.Status != "success" {
			t.Fatal("completed job lost its result")
		}
		if j.ID == "unknown" && j.Status != "unknown" {
			t.Fatal("outage presented as known state")
		}
	}
}

func TestRemoteAPIRejectsDirectRebootEvenWithEnvironmentEnabled(t *testing.T) {
	t.Setenv("NIXOS_ROUTER_ALLOW_REBOOT", "1")
	s, dir, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	c := privsep.NewClient(filepath.Join(dir, "unused.sock"), uint32(os.Geteuid()))
	defer c.Close()
	s.managedRuntime = c
	for _, path := range []string{"/api/v1/health", "/api/v1/system/reboot"} {
		method := "GET"
		if path != "/api/v1/health" {
			method = "POST"
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, nil)
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		s.Handler().ServeHTTP(w, r)
		if method == "POST" {
			if w.Code != 501 {
				t.Fatalf("remote reboot accepted: %d %s", w.Code, w.Body.String())
			}
			continue
		}
		var health struct {
			Deployment struct {
				RebootAllowed bool `json:"rebootAllowed"`
			} `json:"deployment"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
			t.Fatal(err)
		}
		if health.Deployment.RebootAllowed {
			t.Fatal("remote API advertises direct reboot")
		}
	}
}

func TestRemoteClientsUseExecutorLeases(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []string
		status int
		count  int
	}{
		{"missing", nil, 503, 0}, {"empty", []string{""}, 200, 0},
		{"live", []string{"0 aa:bb:cc:dd:ee:ff 192.168.1.2 laptop *\n"}, 200, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, cookies, cleanup := setupApplyEnv(t, true)
			defer cleanup()
			s.managedRuntime = remoteClient(t, tc.data...)
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/api/v1/clients", nil)
			for _, cookie := range cookies {
				r.AddCookie(cookie)
			}
			s.Handler().ServeHTTP(w, r)
			var body struct {
				Source  string `json:"source"`
				Clients []struct {
					IP string `json:"ip"`
				} `json:"clients"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || len(body.Clients) != tc.count {
				t.Fatalf("leases lost: %d %s", w.Code, w.Body.String())
			}
			if tc.status == 200 && body.Source != "leases" {
				t.Fatalf("real leases mislabeled: %s", w.Body.String())
			}
			if tc.count == 1 && body.Clients[0].IP != "192.168.1.2" {
				t.Fatal("wrong live lease")
			}
		})
	}
}

func TestRemoteStatusUsesLeaseSnapshotAndReconcilesApply(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []string
		source string
	}{
		{"missing", nil, "unavailable"}, {"empty", []string{""}, "leases"},
		{"live", []string{"0 aa:bb:cc:dd:ee:ff 192.168.1.2 laptop *\n"}, "leases"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, cookies, cleanup := setupApplyEnv(t, true)
			defer cleanup()
			s.managedRuntime = remoteClient(t, tc.data...)
			if err := s.db.CreateJob("unaccepted", "apply", `{"executor":true}`); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/api/v1/status", nil)
			for _, cookie := range cookies {
				r.AddCookie(cookie)
			}
			s.Handler().ServeHTTP(w, r)
			var body struct {
				Sources   map[string]string `json:"sources"`
				LastApply struct {
					Status string `json:"status"`
				} `json:"lastApply"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || body.Sources["clients"] != tc.source {
				t.Fatalf("stale local lease summary: %d %s", w.Code, w.Body.String())
			}
			if body.LastApply.Status != "unknown" {
				t.Fatalf("unreconciled apply summary: %s", w.Body.String())
			}
		})
	}
}
