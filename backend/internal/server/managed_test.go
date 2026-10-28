package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type runtimeStub struct {
	start   chan config.Config
	release chan struct{}
	fail    bool
}

func (m *runtimeStub) Apply(ctx context.Context, c config.Config) (managed.Result, error) {
	m.start <- c
	select {
	case <-m.release:
	case <-ctx.Done():
		return managed.Result{}, ctx.Err()
	}
	if m.fail {
		return managed.Result{Stage: "activate", RollbackStatus: "succeeded"}, errors.New("injected activation failure")
	}
	return managed.Result{Stage: "complete", RollbackStatus: "not-needed"}, nil
}
func TestManagedApplyJobRemainsReadableAndSerial(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			s, _, cookies, cleanup := setupApplyEnv(t, true)
			defer cleanup()
			runtime := &runtimeStub{start: make(chan config.Config, 1), release: make(chan struct{}), fail: fail}
			s.managedRuntime = runtime
			request := func(method, path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, nil)
				for _, c := range cookies {
					r.AddCookie(c)
				}
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				return w
			}
			response := request("POST", "/api/v1/apply")
			if response.Code != http.StatusAccepted {
				t.Fatalf("not async: %d %s", response.Code, response.Body.String())
			}
			var submitted struct {
				JobID string `json:"jobId"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &submitted); err != nil || submitted.JobID == "" {
				t.Fatal(response.Body.String())
			}
			select {
			case <-runtime.start:
			case <-time.After(time.Second):
				t.Fatal("worker did not start")
			}
			if w := request("POST", "/api/v1/apply"); w.Code != 409 {
				t.Fatalf("parallel apply allowed: %d", w.Code)
			}
			if w := request("GET", "/api/v1/jobs/"+submitted.JobID); w.Code != 200 {
				t.Fatalf("running job unreadable: %s", w.Body.String())
			}
			close(runtime.release)
			deadline := time.Now().Add(2 * time.Second)
			for {
				job, _, err := s.db.GetJob(submitted.JobID)
				if err != nil {
					t.Fatal(err)
				}
				if job.Status != "running" {
					var result map[string]interface{}
					if err = json.Unmarshal([]byte(job.Payload), &result); err != nil {
						t.Fatal(err)
					}
					if result["appliedRuntime"] != !fail {
						t.Fatalf("wrong final result: %v", result)
					}
					if fail && result["rollbackStatus"] != "succeeded" {
						t.Fatalf("rollback result lost: %v", result)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("job did not finish")
				}
				time.Sleep(time.Millisecond * 10)
			}
		})
	}
}

type flushCheckedRuntime struct {
	flushed <-chan struct{}
	result  chan bool
}

func (m *flushCheckedRuntime) Apply(ctx context.Context, c config.Config) (managed.Result, error) {
	select {
	case <-m.flushed:
		m.result <- true
	default:
		m.result <- false
	}
	return managed.Result{Stage: "complete"}, nil
}

type signalingWriter struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func (w *signalingWriter) Flush() { w.ResponseRecorder.Flush(); close(w.flushed) }
func TestManagedApplyFlushesJobBeforeNetworkMutation(t *testing.T) {
	s, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	w := &signalingWriter{httptest.NewRecorder(), make(chan struct{})}
	m := &flushCheckedRuntime{w.flushed, make(chan bool, 1)}
	s.managedRuntime = m
	r := httptest.NewRequest("POST", "/api/v1/apply", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	s.Handler().ServeHTTP(w, r)
	select {
	case ok := <-m.result:
		if !ok {
			t.Fatal("network changed before response flushed")
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not run")
	}
	s.applyMu.Lock()
	s.applyMu.Unlock()
}
