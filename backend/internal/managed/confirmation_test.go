package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCandidateDoesNotReplaceLastGood(t *testing.T) {
	m, _, c := fixture(t)
	if err := m.Restore(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.lastGood())
	if err != nil {
		t.Fatal(err)
	}
	m.opts.ConfirmationTimeout = time.Minute
	t.Cleanup(m.Close)
	c.System.Hostname = "candidate"
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(m.lastGood())
	if string(before) != string(after) {
		t.Fatal("unconfirmed candidate replaced last-good")
	}
}

func guardedFixture(t *testing.T) (*Manager, *recorder, config.Config, *time.Time) {
	m, r, c := fixture(t)
	now := time.Now()
	m.opts.Clock = func() time.Time { return now }
	if err := m.Restore(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	m.opts.ConfirmationTimeout = time.Minute
	t.Cleanup(func() { m.Close() })
	return m, r, c, &now
}
func TestCandidateConfirmAndSuperseded(t *testing.T) {
	m, _, c, _ := guardedFixture(t)
	ctx := context.Background()
	c.System.Hostname = "confirmed"
	result, err := m.Apply(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	id := result.Confirmation.ID
	if _, err = m.Apply(ctx, c); err == nil {
		t.Fatal("second candidate accepted")
	}
	if _, err = m.Confirm(ctx, "wrong"); err == nil {
		t.Fatal("wrong ID accepted")
	}
	for i := 0; i < 2; i++ {
		s, e := m.Confirm(ctx, id)
		if e != nil || s.State != "confirmed" {
			t.Fatal(s, e)
		}
	}
	b, _ := os.ReadFile(m.lastGood())
	if !strings.Contains(string(b), "confirmed") {
		t.Fatal(string(b))
	}
	if _, err = m.Apply(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Confirm(ctx, id); err == nil {
		t.Fatal("superseded confirmation accepted")
	}
}
func TestCandidateExpiryAndCrashRecovery(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprint(crash), func(t *testing.T) {
			m, _, c, now := guardedFixture(t)
			ctx := context.Background()
			before, _ := os.ReadFile(m.lastGood())
			c.System.Hostname = "unreachable"
			result, err := m.Apply(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if crash {
				m.Close()
				fresh, e := New(m.opts)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(fresh.Close)
				if e = fresh.Restore(ctx, c); e != nil {
					t.Fatal(e)
				}
				m = fresh
			} else {
				*now = now.Add(61 * time.Second)
				if _, err = m.Confirm(ctx, result.Confirmation.ID); err == nil {
					t.Fatal("expired confirmation accepted")
				}
			}
			after, _ := os.ReadFile(m.lastGood())
			if string(before) != string(after) {
				t.Fatal("baseline replaced")
			}
			applied, ok := m.AppliedConfig()
			if !ok || applied.System.Hostname == "unreachable" {
				t.Fatal(applied, ok)
			}
			status, err := m.Candidate(ctx)
			if err != nil || status.State != "rolled-back" {
				t.Fatal(status, err)
			}
		})
	}
}
func TestCandidateRecoveryFailureBlocksApply(t *testing.T) {
	m, r, c, now := guardedFixture(t)
	ctx := context.Background()
	if _, err := m.Apply(ctx, c); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	r.fail = "nft --check"
	r.failed = false
	if err := m.ExpireCandidate(ctx); err == nil {
		t.Fatal("failed recovery reported successful")
	}
	if _, ok := m.AppliedConfig(); ok {
		t.Fatal("failed recovery published as available")
	}
	if _, err := m.Apply(ctx, c); err == nil {
		t.Fatal("apply accepted with failed recovery")
	}
	status, _ := m.Candidate(ctx)
	if status.State != "rollback-failed" {
		t.Fatal(status)
	}
	m.Close()
	fresh, err := New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.Restore(ctx, c); err != nil {
		t.Fatal(err)
	}
	status, _ = fresh.Candidate(ctx)
	if status.State != "rolled-back" {
		t.Fatal(status)
	}
}
func TestManagementGrantBounds(t *testing.T) {
	m, _, c := fixture(t)
	for _, g := range []ManagementAccess{
		{Interface: "wan0", Sources: []string{"10.2.0.0/24"}},
		{Interface: "zt*", Sources: []string{"10.2.0.0/24"}},
		{Interface: "zt0", Sources: []string{"0.0.0.0/0"}},
		{Interface: "zt0", Sources: []string{"::/0"}},
		{Interface: "zt0", Sources: []string{"10.2.0.3/24"}},
		{Interface: "zt0"},
	} {
		m.opts.ManagementAccess = []ManagementAccess{g}
		if m.validateManagementAccess() == nil {
			t.Fatalf("accepted %+v", g)
		}
	}
	m.opts.ManagementAccess = []ManagementAccess{{Interface: "zt0", Sources: []string{"10.2.0.0/24", "fd00:2::/64"}}}
	if err := m.validateManagementAccess(); err != nil {
		t.Fatal(err)
	}
	rules := m.render(c)["firewall.nft"]
	for _, rule := range []string{`iifname "zt0" ip saddr 10.2.0.0/24 tcp dport { 8080, 22 } accept;`, `iifname "zt0" ip6 saddr fd00:2::/64 tcp dport { 8080, 22 } accept;`, `tcp dport { 8080, 22 } drop;`} {
		if !strings.Contains(rules, rule) {
			t.Fatalf("missing %s", rule)
		}
	}
	c.LAN.BridgeName = "zt0"
	if m.validate(c) == nil {
		t.Fatal("grant adopted as owned bridge")
	}
}

func TestCandidateWatchdogIndependentOfCaller(t *testing.T) {
	m, _, c, _ := guardedFixture(t)
	m.opts.Clock = time.Now
	m.opts.ConfirmationTimeout = 30 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := m.Apply(ctx, c); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := m.Candidate(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.State == "rolled-back" {
			return
		}
		time.Sleep(time.Millisecond * 10)
	}
	t.Fatal("watchdog did not restore after disconnected caller")
}
func TestCandidateCrashDuringConfirmRestoresExactBaseline(t *testing.T) {
	m, _, c, _ := guardedFixture(t)
	ctx := context.Background()
	// A valid imported baseline need not use the engine's pretty-print layout.
	raw, _ := os.ReadFile(m.lastGood())
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
	compact.WriteByte('\n')
	before := compact.Bytes()
	if err := os.WriteFile(m.lastGood(), before, 0600); err != nil {
		t.Fatal(err)
	}
	c.System.Hostname = "confirmation-interrupted"
	if _, err := m.Apply(ctx, c); err != nil {
		t.Fatal(err)
	}
	r, err := m.readCandidate()
	if err != nil {
		t.Fatal(err)
	}
	r.State = "confirming"
	if err = m.writeCandidate(r); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	if err = atomicFile(m.lastGood(), b); err != nil {
		t.Fatal(err)
	}
	m.Close()
	fresh, err := New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.Restore(ctx, c); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(m.lastGood())
	if string(before) != string(after) {
		t.Fatal("original document not restored exactly")
	}
}

func TestManagementFilterInstalledBeforeManagedLinksUp(t *testing.T) {
	m, r, c := fixture(t)
	if err := m.Restore(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	filter := -1
	for i, call := range r.calls {
		if strings.HasPrefix(call, "nft -f ") {
			filter = i
		}
		if strings.HasPrefix(call, "ip link set dev ") && strings.HasSuffix(call, " up") {
			if filter < 0 || filter > i {
				t.Fatal("managed link enabled before management filter", r.calls)
			}
		}
	}
}

func TestFailedBootstrapRetainsManagementGuard(t *testing.T) {
	m, r, c := fixture(t)
	r.fail = "systemctl restart nixos-router-dnsmasq.service"
	if _, err := m.Apply(context.Background(), c); err == nil {
		t.Fatal("injected bootstrap failure accepted")
	}
	found := false
	for _, call := range r.calls {
		if strings.HasPrefix(call, "nft delete table") {
			t.Fatal("management guard removed", r.calls)
		}
		if strings.HasPrefix(call, "nft -f ") && strings.HasSuffix(call, "management-guard.nft") {
			found = true
		}
	}
	if !found {
		t.Fatal("management guard not retained", r.calls)
	}
	guard, err := os.ReadFile(filepath.Join(m.base(), "management-guard.nft"))
	if err != nil || !strings.Contains(string(guard), `iifname != "lo" tcp dport { 8080, 22 } drop`) {
		t.Fatal(string(guard), err)
	}
}
