package privsep

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
)

// Leave room for the fixed RPC envelope and bounded job ID around a full draft.
const maxRequestBytes = config.MaxDocumentBytes + 1024

var jobID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

type Runtime interface {
	Apply(context.Context, config.Config) (managed.Result, error)
	AppliedConfig() (config.Config, bool)
}

// Job describes executor state, not Internet reachability. An interrupted job
// is never replayed: the daemon restores last-good before accepting new jobs.
type Job struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Result managed.Result `json:"result"`
	Error  string         `json:"error,omitempty"`
}
type applyRequest struct {
	ID     string         `json:"id"`
	Config *config.Config `json:"config"`
}
type appliedResponse struct {
	Config    config.Config `json:"config"`
	Available bool          `json:"available"`
}
type Executor struct {
	runtime    Runtime
	db         *sql.DB
	lock       *os.File
	legacyLock *os.File
	mu         sync.Mutex
	busy       bool
	closing    bool
	fault      error
	workers    sync.WaitGroup
	http       *http.Server
	once       sync.Once
	closeErr   error
}

// OpenExecutor opens a private journal under an already provisioned executor
// directory. Only this process may own it. It must not be API-writable.
func OpenExecutor(dir string, runtime Runtime) (*Executor, error) {
	if runtime == nil {
		return nil, errors.New("runtime required")
	}
	path := filepath.Join(dir, "jobs.sqlite")
	if err := checkParent(path, uint32(os.Geteuid())); err != nil {
		return nil, err
	}
	legacyLock, err := lockStateFile(filepath.Join(dir, "daemon.lock"))
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			legacyLock.Close()
		}
	}()
	lock, err := os.OpenFile(filepath.Join(dir, "executor.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("executor already owns journal: %w", err)
	}
	db, err := openJournal(path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	success = true
	return &Executor{runtime: runtime, db: db, lock: lock, legacyLock: legacyLock}, nil
}

// Serve accepts only a listener created by Listen, preventing accidental TCP
// exposure of the privileged protocol without peer authentication.
func (e *Executor) Serve(l net.Listener) error {
	if _, ok := l.(*authenticatedListener); !ok {
		return errors.New("authenticated Unix listener required")
	}
	e.mu.Lock()
	if e.closing || e.http != nil {
		e.mu.Unlock()
		return errors.New("executor already serving or closed")
	}
	srv := &http.Server{Handler: http.HandlerFunc(e.handle), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	e.http = srv
	e.mu.Unlock()
	return srv.Serve(l)
}
func (e *Executor) Close() error {
	e.once.Do(func() {
		e.mu.Lock()
		e.closing = true
		srv := e.http
		e.mu.Unlock()
		if srv != nil {
			srv.Close()
		}
		// A disconnected HTTP peer cannot cancel an accepted network transaction.
		e.workers.Wait()
		e.closeErr = e.db.Close()
		e.lock.Close()
		e.legacyLock.Close()
	})
	return e.closeErr
}
func rpcJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func rpcError(w http.ResponseWriter, status int, code string) {
	rpcJSON(w, status, map[string]string{"error": code})
}
func (e *Executor) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		rpcError(w, 400, "query_not_supported")
		return
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/jobs":
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req applyRequest
		err := dec.Decode(&req)
		if err == nil {
			var extra any
			err = dec.Decode(&extra)
			if err == io.EOF {
				err = nil
			} else if err == nil {
				err = errors.New("trailing JSON")
			}
		}
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				rpcError(w, 413, "request_too_large")
			} else {
				rpcError(w, 400, "invalid_request")
			}
			return
		}
		if !jobID.MatchString(req.ID) || req.Config == nil {
			rpcError(w, 400, "invalid_request")
			return
		}
		if err = req.Config.Validate(); err != nil {
			rpcError(w, 400, "invalid_config")
			return
		}
		e.submit(w, req)
	case r.Method == "GET" && len(r.URL.Path) > len("/v1/jobs/") && r.URL.Path[:len("/v1/jobs/")] == "/v1/jobs/":
		id := r.URL.Path[len("/v1/jobs/"):]
		if !jobID.MatchString(id) {
			rpcError(w, 400, "invalid_job_id")
			return
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.fault != nil || e.closing {
			rpcError(w, 503, "executor_unavailable")
			return
		}
		j, _, err := e.readJob(id)
		if errors.Is(err, sql.ErrNoRows) {
			rpcError(w, 404, "job_not_found")
		} else if err != nil {
			rpcError(w, 503, "journal_unavailable")
		} else {
			rpcJSON(w, 200, j)
		}

	case r.Method == "GET" && r.URL.Path == "/v1/leases":
		observer, ok := e.runtime.(interface {
			Leases(context.Context) ([]managed.Lease, error)
		})
		if !ok {
			rpcError(w, 503, "leases_unavailable")
			return
		}
		leases, err := observer.Leases(r.Context())
		if err != nil {
			rpcError(w, 503, "leases_unavailable")
			return
		}
		rpcJSON(w, 200, leases)
	case r.Method == "GET" && r.URL.Path == "/v1/applied":
		c, ok := e.runtime.AppliedConfig()
		rpcJSON(w, 200, appliedResponse{Config: c, Available: ok})
	default:
		rpcError(w, 404, "unknown_operation")
	}
}
func (e *Executor) submit(w http.ResponseWriter, req applyRequest) {
	b, _ := json.Marshal(req.Config)
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fault != nil || e.closing {
		rpcError(w, 503, "executor_unavailable")
		return
	}
	old, hash, err := e.readJob(req.ID)
	if err == nil {
		if hash != digest {
			rpcError(w, 409, "job_id_conflict")
		} else {
			rpcJSON(w, 200, old)
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		rpcError(w, 503, "journal_unavailable")
		return
	}
	if e.busy {
		rpcError(w, 409, "apply_busy")
		return
	}
	j := Job{ID: req.ID, Status: "running", Result: managed.Result{Stage: "queued", RollbackStatus: "not-needed"}}
	result, _ := json.Marshal(j.Result)
	if _, err = e.db.Exec(`INSERT INTO executor_jobs(id,digest,status,result,error) VALUES(?,?,?,?,?)`, j.ID, digest, j.Status, string(result), ""); err != nil {
		rpcError(w, 503, "journal_unavailable")
		return
	}
	e.busy = true
	e.workers.Add(1)
	go e.run(j, *req.Config)
	rpcJSON(w, 202, j)
}
func (e *Executor) run(j Job, c config.Config) {
	defer e.workers.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := e.runtime.Apply(ctx, c)
	j.Result = result
	j.Status = "success"
	if err != nil {
		j.Status = "failed"
		j.Error = err.Error()
	}
	b, _ := json.Marshal(j.Result)
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err = e.db.Exec(`UPDATE executor_jobs SET status=?,result=?,error=? WHERE id=?`, j.Status, string(b), j.Error, j.ID)
	if err != nil {
		e.fault = err
	} // Never acknowledge success that was not persisted.
	e.busy = false
}
func (e *Executor) readJob(id string) (Job, string, error) {
	var j Job
	var digest, body string
	err := e.db.QueryRow(`SELECT id,digest,status,result,error FROM executor_jobs WHERE id=?`, id).Scan(&j.ID, &digest, &j.Status, &body, &j.Error)
	if err == nil {
		err = json.Unmarshal([]byte(body), &j.Result)
	}
	return j, digest, err
}
