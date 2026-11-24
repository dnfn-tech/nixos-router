package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type countedRoutes struct {
	defaultRunner
	calls atomic.Int32
}

func (*countedRoutes) LookPath(string) error { return nil }
func (r *countedRoutes) Output(string, ...string) (string, error) {
	r.calls.Add(1)
	return "", errors.New("route unavailable")
}

func TestNetworkLiveSharesFailedCollection(t *testing.T) {
	s := New(Options{Config: config.DefaultConfig(), DevMode: true})
	runner := &countedRoutes{}
	s.runner = runner
	handler := s.Handler()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/network/live", nil))
			if w.Code != http.StatusOK {
				t.Errorf("unexpected HTTP %d", w.Code)
			}
		})
	}
	wg.Wait()
	if calls := runner.calls.Load(); calls != 1 {
		t.Fatalf("expected one route probe shared by all requests, got %d", calls)
	}
}

func TestMonitoringCPUFromProcStat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat")
	cache := monitoringCache{}
	step := func(contents string, want *float64) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		got := cache.sampleCPU(path)
		if (want == nil && got != nil) || (want != nil && (got == nil || *got != *want)) {
			t.Fatalf("unexpected CPU sample for %q: %v, want %v", contents, got, want)
		}
	}
	zero, half := 0.0, 50.0
	step("cpu 100 0 0 100 0 0 0 0 50 0", nil)
	step("cpu 120 0 0 110 10 0 0 0 60 0", &half)
	step("cpu 120 0 0 120 10 0 0 0 60 0", &zero)
	step("cpu 120 0 0 120 10 0 0 0 60 0", nil)
	step("cpu 10 0 0 130 10 0 0 0 60 0", nil)
	step("cpu broken", nil)
	step("cpu 20 0 0 140 10 0 0 0 60 0", nil)
	cache.cpu.at = time.Now().Add(-maxSampleGap - time.Second)
	step("cpu 30 0 0 150 10 0 0 0", nil)
	step("cpu 40 0 0 160 10 0 0 0", &half)
}

func TestMonitoringMemoryRequiresMemAvailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	for _, tc := range []struct {
		text string
		want *memoryUsage
	}{
		{"MemTotal: 10 kB\nMemFree: 9 kB\nMemAvailable: 4 kB\n", &memoryUsage{10240, 6144}},
		{"MemTotal: 10 kB\nMemAvailable: 10 kB\n", &memoryUsage{10240, 0}},
		{"MemTotal: 10 kB\nMemAvailable: 0 kB\n", &memoryUsage{10240, 10240}},
		{"MemTotal: 10 kB\nMemFree: 8 kB\n", nil},
		{"MemTotal: 10 kB\nMemAvailable: 11 kB\n", nil},
		{"MemTotal: 10 kB\nMemAvailable: 1 MB\n", nil},
	} {
		if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
			t.Fatal(err)
		}
		got := readMemoryUsage(path)
		if (tc.want == nil && got != nil) || (tc.want != nil && (got == nil || *got != *tc.want)) {
			t.Fatalf("unexpected meminfo result for %q: %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestMonitoringRefreshesOnRevisionAndExpiry(t *testing.T) {
	cfg := config.DefaultConfig()
	s := New(Options{Config: cfg, DevMode: true})
	runner := &countedRoutes{}
	s.runner = runner
	sample := func() monitoringSnapshot {
		t.Helper()
		value, ok := s.monitoringSnapshot(context.Background())
		if !ok {
			t.Fatal("snapshot unavailable")
		}
		return value
	}
	first, cached := sample(), sample()
	if first.SampleMs != cached.SampleMs || runner.calls.Load() != 1 {
		t.Fatal("cached response changed acquisition time or probed again")
	}
	s.cfgMu.Lock()
	s.cfg.Revision = "updated"
	s.cfg.WAN.ContractDownMbps = 500
	s.cfgMu.Unlock()
	if got := sample(); got.ContractDownMbps != 500 || runner.calls.Load() != 2 {
		t.Fatalf("config revision did not refresh snapshot: %+v, calls %d", got, runner.calls.Load())
	}
	s.monitor.mu.Lock()
	s.monitor.expires = time.Time{}
	s.monitor.mu.Unlock()
	if got := sample(); got.WAN.Link != "unknown" || runner.calls.Load() != 3 {
		t.Fatalf("failed route probe was not retried after expiry: %+v, calls %d", got, runner.calls.Load())
	}
}

func TestMonitoringPortNegotiationRefreshesOnCarrierChangeAndExpiry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NIXOS_ROUTER_SYS_CLASS_NET", root)
	writeIfaceSysfs(t, root, "wan0", true, 1000, "full", 0, 0)
	path := filepath.Join(root, "wan0")
	if err := os.Mkdir(filepath.Join(path, "device"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(field, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(path, field), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("type", "1")
	write("ifindex", "11")
	write("carrier_changes", "1")
	s := New(Options{Config: config.DefaultConfig()})
	check := func(speed int, link string) {
		t.Helper()
		ports := s.portObservations("wan0", nil)
		if len(ports) != 1 || ports[0].SpeedMbps != speed || ports[0].Link != link {
			t.Fatalf("unexpected port observations: %+v", ports)
		}
	}
	check(1000, "up")
	write("speed", "2500")
	check(1000, "up")
	write("carrier_changes", "3")
	check(2500, "up")
	write("operstate", "down")
	check(0, "down")
	write("operstate", "up")
	check(2500, "up")
	write("speed", "unavailable")
	s.monitor.portDue = time.Time{}
	check(0, "up")
}

type gatedRoutes struct {
	countedRoutes
	started chan struct{}
	release chan struct{}
}

func (r *gatedRoutes) Output(string, ...string) (string, error) {
	if r.calls.Add(1) == 1 {
		close(r.started)
		<-r.release
	}
	return "", errors.New("route unavailable")
}

func TestMonitoringDiscardsInFlightOldRevision(t *testing.T) {
	s := New(Options{Config: config.DefaultConfig()})
	runner := &gatedRoutes{started: make(chan struct{}), release: make(chan struct{})}
	s.runner = runner
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan monitoringSnapshot, 1)
	go func() {
		value, _ := s.monitoringSnapshot(ctx)
		result <- value
	}()
	select {
	case <-runner.started:
	case <-ctx.Done():
		close(runner.release)
		t.Fatal("route collection did not start")
	}
	s.cfgMu.Lock()
	s.cfg.Revision = "new-draft"
	s.cfg.WAN.ContractUpMbps = 100
	s.cfgMu.Unlock()
	releasedAt := time.Since(networkSampleClock).Milliseconds()
	close(runner.release)
	select {
	case value := <-result:
		if value.ContractUpMbps != 100 || value.SampleMs < releasedAt || runner.calls.Load() != 2 {
			t.Fatalf("expected fresh configuration and acquisition time, got %+v and %d probes", value, runner.calls.Load())
		}
	case <-ctx.Done():
		t.Fatal("updated sample did not arrive")
	}
}
