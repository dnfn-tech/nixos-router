package privsep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
)

type protocolRunner struct {
	calls      atomic.Int32
	failStarts atomic.Int32
	entered    chan struct{}
	release    chan struct{}
}

func (r *protocolRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	n := r.calls.Add(1)
	if n == 1 && r.entered != nil {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if name == "systemctl" && len(args) > 1 && args[0] == "restart" && args[1] == "nixos-router-dnsmasq.service" && r.failStarts.Load() > 0 {
		r.failStarts.Add(-1)
		return "", errors.New("injected dnsmasq activation failure")
	}
	if name == "systemctl" && len(args) > 0 && args[0] == "show" {
		return "inactive\n", nil
	}
	return "[]", nil
}
func protocolFixture(t *testing.T, r *protocolRunner) (*managed.Manager, config.Config, string) {
	t.Helper()
	state := filepath.Dir(socketPath(t))
	m, e := managed.New(managed.Options{StateDir: state, Interfaces: []string{"wan0", "lan0"}, ManagementPort: 8080, Runner: r})
	if e != nil {
		t.Fatal(e)
	}
	c := config.DefaultConfig()
	c.WAN.Interface = "wan0"
	c.LAN.Ports = []string{"lan0"}
	return m, c, state
}
func protocolService(t *testing.T, m *managed.Manager, state string) (*Executor, *Client, func()) {
	t.Helper()
	e, err := OpenExecutor(state, m)
	if err != nil {
		t.Fatal(err)
	}
	path := socketPath(t)
	l, err := Listen(path, uint32(os.Geteuid()))
	if err != nil {
		e.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.Serve(l) }()
	c := NewClient(path, uint32(os.Geteuid()))
	stop := func() { c.Close(); e.Close(); l.Close(); <-done }
	return e, c, stop
}
func awaitJob(t *testing.T, c *Client, id string) Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, e := c.Job(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if j.Status != "running" {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not complete")
	return Job{}
}
func TestRPCDurableApplyAndIdempotency(t *testing.T) {
	r := &protocolRunner{entered: make(chan struct{}), release: make(chan struct{})}
	m, cfg, state := protocolFixture(t, r)
	_, c, stop := protocolService(t, m, state)
	stopped := false
	defer func() {
		if !stopped {
			select {
			case <-r.release:
			default:
				close(r.release)
			}
			stop()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	j, e := c.Submit(ctx, "job-1", cfg)
	if e != nil || j.Status != "running" {
		t.Fatalf("submit: %+v %v", j, e)
	}
	cancel() // disconnect/cancel the API caller, not the accepted transaction.
	<-r.entered
	same, e := c.Submit(context.Background(), "job-1", cfg)
	if e != nil || same.ID != "job-1" {
		t.Fatalf("retry: %+v %v", same, e)
	}
	changed := cfg
	changed.DNS.Domain = "changed.lan"
	if _, e = c.Submit(context.Background(), "job-1", changed); e == nil {
		t.Fatal("ID reused for another candidate")
	}
	if _, e = c.Submit(context.Background(), "job-2", cfg); e == nil {
		t.Fatal("concurrent apply accepted")
	}
	close(r.release)
	j = awaitJob(t, c, "job-1")
	if j.Status != "success" || j.Result.Stage != "awaiting-wan-lease" {
		t.Fatalf("terminal job: %+v", j)
	}
	b, e := os.ReadFile(filepath.Join(state, "managed", "last-good.json"))
	if e != nil {
		t.Fatal(e)
	}
	var saved config.Config
	if json.Unmarshal(b, &saved) != nil || saved.WAN.Interface != "wan0" {
		t.Fatal("real manager did not persist candidate")
	}
	count := r.calls.Load()
	if _, e = c.Submit(context.Background(), "job-1", cfg); e != nil {
		t.Fatal(e)
	}
	if r.calls.Load() != count {
		t.Fatal("duplicate caused network execution")
	}
	stop()
	stopped = true
	_, c2, stop2 := protocolService(t, m, state)
	defer stop2()
	j, e = c2.Job(context.Background(), "job-1")
	if e != nil || j.Status != "success" {
		t.Fatalf("lost result after reopen: %+v %v", j, e)
	}
	if _, e = c2.Submit(context.Background(), "job-1", cfg); e != nil {
		t.Fatal(e)
	}
	if r.calls.Load() != count {
		t.Fatal("restart replayed completed job")
	}
	applied, ok, e := c2.Applied(context.Background())
	if e != nil || !ok || applied.WAN.Interface != "wan0" {
		t.Fatalf("applied: %v %v", ok, e)
	}
}
func TestRPCRejectForeignInterfaceBeforeCommands(t *testing.T) {
	r := &protocolRunner{}
	m, cfg, state := protocolFixture(t, r)
	_, c, stop := protocolService(t, m, state)
	defer stop()
	cfg.LAN.Ports = []string{"host-management"}
	if _, e := c.Submit(context.Background(), "foreign", cfg); e != nil {
		t.Fatal(e)
	}
	j := awaitJob(t, c, "foreign")
	if j.Status != "failed" || j.Result.Stage != "validate" || r.calls.Load() != 0 {
		t.Fatalf("unsafe apply: %+v calls=%d", j, r.calls.Load())
	}
}
func TestRPCStrictRequestBoundary(t *testing.T) {
	r := &protocolRunner{}
	m, cfg, state := protocolFixture(t, r)
	e, err := OpenExecutor(state, m)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	path := socketPath(t)
	l, err := Listen(path, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go e.Serve(l)
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return DialContext(ctx, path, uint32(os.Geteuid()))
	}}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: time.Second}
	raw, _ := json.Marshal(cfg)
	valid := `{"id":"bad","config":` + string(raw) + `}`
	for _, tc := range []struct {
		name, path, body string
		want             int
	}{
		{"unknown action", "/v1/exec", `{"command":"id"}`, 404},
		{"unknown envelope field", "/v1/jobs", strings.TrimSuffix(valid, "}") + `,"command":"id"}`, 400},
		{"unknown nested field", "/v1/jobs", strings.Replace(valid, `"wan":{`, `"wan":{"command":"id",`, 1), 400},
		{"null config", "/v1/jobs", `{"id":"bad","config":null}`, 400},
		{"trailing value", "/v1/jobs", valid + ` {}`, 400},
		{"invalid id", "/v1/jobs", strings.Replace(valid, `"bad"`, `"../outside"`, 1), 400},
		{"invalid config", "/v1/jobs", `{"id":"bad","config":{}}`, 400},
		{"oversize", "/v1/jobs", strings.Repeat(" ", 2<<20) + valid, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := hc.Post("http://executor"+tc.path, "application/json", bytes.NewBufferString(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status=%d want=%d", resp.StatusCode, tc.want)
			}
		})
	}
	if r.calls.Load() != 0 {
		t.Fatal("malformed requests executed network commands")
	}
}

func TestJournalExclusiveOwnershipAndInterruptedRecovery(t *testing.T) {
	r := &protocolRunner{}
	m, _, state := protocolFixture(t, r)
	e, err := OpenExecutor(state, m)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := OpenExecutor(state, m); err == nil {
		other.Close()
		e.Close()
		t.Fatal("second executor opened same journal")
	}
	// A durable running row is what remains after process death. Reopening must
	// preserve identity and uncertainty, never fabricate success or replay it.
	_, err = e.db.Exec(`INSERT INTO executor_jobs VALUES('interrupted-job','digest','running','{"stage":"activate","rollbackStatus":"not-needed"}','')`)
	if err != nil {
		e.Close()
		t.Fatal(err)
	}
	e.Close()
	_, c, stop := protocolService(t, m, state)
	defer stop()
	j, err := c.Job(context.Background(), "interrupted-job")
	if err != nil || j.Status != "interrupted" || j.Result.RollbackStatus != "unknown" {
		t.Fatalf("recovery: %+v %v", j, err)
	}
	if r.calls.Load() != 0 {
		t.Fatal("replayed interrupted job")
	}
}

func TestJournalRejectSymlink(t *testing.T) {
	r := &protocolRunner{}
	m, _, state := protocolFixture(t, r)
	target := filepath.Join(state, "preserve")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(state, "jobs.sqlite")); err != nil {
		t.Fatal(err)
	}
	e, err := OpenExecutor(state, m)
	if err == nil {
		e.Close()
		t.Fatal("journal symlink accepted")
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "preserve" {
		t.Fatal("symlink target altered")
	}
}

func TestJournalFailureDoesNotRunCandidateOrClaimSuccess(t *testing.T) {
	t.Run("before acceptance", func(t *testing.T) {
		r := &protocolRunner{}
		m, cfg, state := protocolFixture(t, r)
		e, c, stop := protocolService(t, m, state)
		defer stop()
		e.db.Close()
		if _, err := c.Submit(context.Background(), "not-durable", cfg); err == nil {
			t.Fatal("accepted without durable journal")
		}
		if r.calls.Load() != 0 {
			t.Fatal("executed without durable acceptance")
		}
	})
	t.Run("terminal write", func(t *testing.T) {
		r := &protocolRunner{entered: make(chan struct{}), release: make(chan struct{})}
		m, cfg, state := protocolFixture(t, r)
		e, c, stop := protocolService(t, m, state)
		defer func() {
			select {
			case <-r.release:
			default:
				close(r.release)
			}
			stop()
		}()
		if _, err := c.Submit(context.Background(), "uncertain", cfg); err != nil {
			t.Fatal(err)
		}
		<-r.entered
		e.db.Close()
		close(r.release)
		e.workers.Wait()
		if j, err := c.Job(context.Background(), "uncertain"); err == nil {
			t.Fatalf("claimed persisted result: %+v", j)
		}
		n := r.calls.Load()
		if _, err := c.Submit(context.Background(), "next", cfg); err == nil {
			t.Fatal("accepted while journal faulted")
		}
		if r.calls.Load() != n {
			t.Fatal("mutated after journal failure")
		}
	})
}

func TestRPCActivationRecoveryResultPersists(t *testing.T) {
	for _, tc := range []struct {
		failures int32
		rollback string
	}{{1, "succeeded"}, {2, "failed"}} {
		t.Run(tc.rollback, func(t *testing.T) {
			r := &protocolRunner{}
			m, cfg, state := protocolFixture(t, r)
			_, c, stop := protocolService(t, m, state)
			stopped := false
			defer func() {
				if !stopped {
					stop()
				}
			}()
			if _, err := c.Submit(context.Background(), "good", cfg); err != nil {
				t.Fatal(err)
			}
			if j := awaitJob(t, c, "good"); j.Status != "success" {
				t.Fatal(j)
			}
			r.failStarts.Store(tc.failures)
			cfg.DNS.Domain = "new.lan"
			if _, err := c.Submit(context.Background(), "failed", cfg); err != nil {
				t.Fatal(err)
			}
			j := awaitJob(t, c, "failed")
			if j.Status != "failed" || j.Result.RollbackStatus != tc.rollback || j.Error == "" {
				t.Fatalf("lost recovery result: %+v", j)
			}
			stop()
			stopped = true
			fresh, err := managed.New(managed.Options{StateDir: state, Interfaces: []string{"wan0", "lan0"}, ManagementPort: 8080, Runner: r})
			if err != nil {
				t.Fatal(err)
			}
			_, c2, stop2 := protocolService(t, fresh, state)
			defer stop2()
			persisted, err := c2.Job(context.Background(), "failed")
			if err != nil || persisted.Status != "failed" || persisted.Result.RollbackStatus != tc.rollback {
				t.Fatalf("reopened job: %+v %v", persisted, err)
			}
		})
	}
}
func TestRPCLostSubmitResponseCanBeQueriedAndRetried(t *testing.T) {
	r := &protocolRunner{entered: make(chan struct{}), release: make(chan struct{})}
	m, cfg, state := protocolFixture(t, r)
	_, c, stop := protocolService(t, m, state)
	defer func() {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
		stop()
	}()
	raw, err := c.transport.DialContext(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	body, _ := json.Marshal(applyRequest{ID: "lost-response", Config: &cfg})
	_, err = fmt.Fprintf(raw, "POST /v1/jobs HTTP/1.1\r\nHost: executor\r\nContent-Length: %d\r\nContent-Type: application/json\r\n\r\n%s", len(body), body)
	if err != nil {
		t.Fatal(err)
	}
	<-r.entered // journal committed; deliberately discard every response byte.
	raw.Close()
	j, err := c.Job(context.Background(), "lost-response")
	if err != nil || j.Status != "running" {
		t.Fatalf("query: %+v %v", j, err)
	}
	if _, err = c.Submit(context.Background(), "lost-response", cfg); err != nil {
		t.Fatal(err)
	}
	close(r.release)
	if j = awaitJob(t, c, "lost-response"); j.Status != "success" {
		t.Fatal(j)
	}
	count := r.calls.Load()
	if _, err = c.Submit(context.Background(), "lost-response", cfg); err != nil {
		t.Fatal(err)
	}
	if count != r.calls.Load() {
		t.Fatal("lost response retry executed twice")
	}
}

func TestRPCLeaseSnapshot(t *testing.T) {
	r := &protocolRunner{}
	m, _, state := protocolFixture(t, r)
	_, c, stop := protocolService(t, m, state)
	defer stop()
	if _, err := c.Leases(context.Background()); err == nil {
		t.Fatal("missing lease file treated as empty")
	}
	if err := os.WriteFile(filepath.Join(state, "dnsmasq.leases"), []byte("0 aa:bb:cc:dd:ee:ff 192.168.1.2 laptop *\n"), 0600); err != nil {
		t.Fatal(err)
	}
	leases, err := c.Leases(context.Background())
	if err != nil || len(leases) != 1 || leases[0].IP != "192.168.1.2" {
		t.Fatalf("lease RPC: %+v %v", leases, err)
	}
	var body map[string]any
	if err := c.call(context.Background(), "GET", "/v1/leases?path=/etc/passwd", nil, &body); err == nil {
		t.Fatal("caller-selected path accepted")
	}
	if r.calls.Load() != 0 {
		t.Fatal("lease query executed commands")
	}
}

func TestExecutorExcludesLegacyDaemon(t *testing.T) {
	m, _, state := protocolFixture(t, &protocolRunner{})
	f, err := lockStateFile(filepath.Join(state, "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	e, err := OpenExecutor(state, m)
	if err == nil {
		e.Close()
		t.Fatal("executor shared state with legacy daemon")
	}
}
