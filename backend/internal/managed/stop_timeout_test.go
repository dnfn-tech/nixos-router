package managed

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

type slowProtocolStop struct{ started chan struct{} }

func (r slowProtocolStop) Run(ctx context.Context, _ string, _ ...string) (string, error) {
	close(r.started)
	timer := time.NewTimer(21 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return "", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Opt-in elapsed-time regression: a live callback can legitimately outlast
// the old 20-second generic command limit. No systemd or network is invoked.
func TestManagedStopWaitsForProtocolCleanup(t *testing.T) {
	if os.Getenv("ROUTERD_TEST_SLOW_STOP") != "1" {
		t.Skip("set ROUTERD_TEST_SLOW_STOP=1")
	}
	m, _, _ := fixture(t)
	m.opts.Runner = slowProtocolStop{make(chan struct{})}
	if err := m.cmd(context.Background(), "systemctl", "stop", "nixos-router-wan6.service"); err != nil {
		t.Fatal("abandoned live protocol cleanup", err)
	}
}

func TestManagedStopHonorsCallerCancellation(t *testing.T) {
	m, _, _ := fixture(t)
	started := make(chan struct{})
	m.opts.Runner = slowProtocolStop{started}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- m.cmd(ctx, "systemctl", "stop", "nixos-router-wan6.service") }()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop ignored caller cancellation")
	}
}

type deadlineProbe struct{ remaining time.Duration }

func (r *deadlineProbe) Run(ctx context.Context, _ string, _ ...string) (string, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return "", errors.New("missing deadline")
	}
	r.remaining = time.Until(deadline)
	return "", nil
}
func TestExtendedStopBudgetIsScoped(t *testing.T) {
	for _, args := range [][]string{{"stop", "foreign.service"}, {"is-active", "nixos-router-wan6.service"}, {"stop", "nixos-router-wan6.service", "foreign.service"}} {
		m, _, _ := fixture(t)
		r := &deadlineProbe{}
		m.opts.Runner = r
		if err := m.cmd(context.Background(), "systemctl", args...); err != nil {
			t.Fatal(err)
		}
		if r.remaining > 20*time.Second || r.remaining < 19*time.Second {
			t.Fatal("ordinary command budget changed", args, r.remaining)
		}
	}
	m, _, _ := fixture(t)
	r := &deadlineProbe{}
	m.opts.Runner = r
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.cmd(ctx, "systemctl", "stop", "nixos-router-wan6.service"); err != nil {
		t.Fatal(err)
	}
	if r.remaining > time.Second {
		t.Fatal("extended caller deadline", r.remaining)
	}
}

type callbackDuringStop struct {
	m        *Manager
	fallback *recorder
	seen     bool
}

func (r *callbackDuringStop) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name == "systemctl" && len(args) == 2 && args[0] == "stop" && args[1] == "nixos-router-wan6.service" {
		callbackCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := r.m.withIPv6StateLock(callbackCtx, func() error { return r.m.publishDNSSource("wan6", "") }); err != nil {
			return "", err
		}
		r.seen = true
	}
	return r.fallback.Run(ctx, name, args...)
}
func TestStopCallbackCanFinishBeforeGenerationSwitch(t *testing.T) {
	m, r, c := fixture(t)
	dir, err := m.prepare(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.switchFiles(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	next, err := m.prepare(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	runner := &callbackDuringStop{m: m, fallback: r}
	m.opts.Runner = runner
	if err = m.activate(context.Background(), c, nil, next); err != nil {
		t.Fatal("callback could not complete during stop", err)
	}
	if !runner.seen {
		t.Fatal("stop callback not exercised")
	}
}
