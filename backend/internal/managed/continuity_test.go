package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type continuityRunner struct {
	recorder
	c     config.Config
	drift bool
	mtu   int
	hook  func(string, []string)
}

func (r *continuityRunner) Run(ctx context.Context, n string, a ...string) (string, error) {
	out, err := r.recorder.Run(ctx, n, a...)
	if r.hook != nil {
		r.hook(n, a)
	}
	if err != nil {
		return out, err
	}
	if n != "ip" || len(a) == 0 || a[0] != "-j" {
		return out, nil
	}
	if strings.Join(a, " ") == "-j -4 addr show" {
		prefix := netip.MustParsePrefix(r.c.LAN.IPv4CIDR)
		v := []map[string]any{{"ifname": r.c.LAN.BridgeName, "addr_info": []map[string]any{{"local": prefix.Addr().String(), "prefixlen": prefix.Bits(), "scope": "global"}}}, {"ifname": wanEgress(r.c), "addr_info": []map[string]any{{"local": "203.0.113.8", "prefixlen": 32, "scope": "global"}}}}
		b, _ := json.Marshal(v)
		return string(b), nil
	}
	if a[1] == "link" || a[1] == "-d" {
		master := r.c.LAN.BridgeName
		if r.drift {
			master = ""
		}
		v := []map[string]any{
			{"ifname": r.c.LAN.BridgeName, "ifalias": "nixos-router-managed", "flags": []string{"UP"}, "linkinfo": map[string]string{"info_kind": "bridge"}},
			{"ifname": r.c.WAN.Interface, "flags": []string{"UP", "LOWER_UP"}},
			{"ifname": "lan0", "master": master, "flags": []string{"UP"}},
			{"ifname": "lan1", "flags": []string{"UP"}},
		}
		if r.c.WAN.Mode == "pppoe" {
			v = append(v, map[string]any{"ifname": managedPPPInterface, "ifalias": "nixos-router-managed-ppp", "mtu": r.mtu, "flags": []string{"UP"}})
		}
		if r.c.WiFi.Enable {
			v = append(v, map[string]any{"ifname": "wlan0", "master": r.c.LAN.BridgeName, "flags": []string{"UP"}})
		}
		b, _ := json.Marshal(v)
		return string(b), nil
	}
	return out, nil
}

func continuityFixture(t *testing.T, mode string) (*Manager, *continuityRunner, config.Config) {
	t.Helper()
	m, _, c := fixture(t)
	c.WAN.Mode = mode
	if mode == "pppoe" {
		c.WAN.PPPoE = &config.PPPoEConfig{Username: "test", Password: "secret"}
	}
	if err := m.Restore(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	r := &continuityRunner{c: clone(c), mtu: 1460}
	m.opts.Runner = r
	t.Cleanup(m.Close)
	return m, r, c
}

func assertNoLinkDisruption(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		for _, forbidden := range []string{"ip link set", "ip link add", "ip link delete", "ip -4 addr flush", "ip -4 route flush", "systemctl stop nixos-router-wan", "systemctl stop nixos-router-pppoe", "systemctl restart nixos-router-wan", "systemctl restart nixos-router-pppoe"} {
			if strings.HasPrefix(call, forbidden) {
				t.Fatalf("unchanged links/session disrupted: %s", call)
			}
		}
	}
}

func TestUnchangedApplyPreservesRuntimeGeneration(t *testing.T) {
	for _, mode := range []string{"dhcp", "pppoe"} {
		t.Run(mode, func(t *testing.T) {
			m, r, c := continuityFixture(t, mode)
			before, _ := os.Readlink(filepath.Join(m.base(), "current"))
			if _, err := m.Apply(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			assertNoLinkDisruption(t, r.calls)
			after, _ := os.Readlink(filepath.Join(m.base(), "current"))
			if after != before {
				t.Fatal("live protocol generation replaced")
			}
			for _, call := range r.calls {
				if strings.HasPrefix(call, "systemctl restart") || strings.HasPrefix(call, "systemctl stop") {
					t.Fatal("unchanged service touched", call)
				}
			}
		})
	}
}

func TestDNSChangeAndExpiryPreserveWANAndCallbacks(t *testing.T) {
	m, r, c := continuityFixture(t, "pppoe")
	ctx := context.Background()
	gen, _ := os.Readlink(filepath.Join(m.base(), "current"))
	options, _ := os.ReadFile(filepath.Join(m.base(), "current", "pppoe.options"))
	if err := m.HandleIPv4DNS(ctx, gen, managedPPPInterface, "bound", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	m.opts.Clock = func() time.Time { return now }
	m.opts.ConfirmationTimeout = time.Minute
	c.DNS.Upstreams = []string{"8.8.8.8"}
	if _, err := m.Apply(ctx, c); err != nil {
		t.Fatal(err)
	}
	assertNoLinkDisruption(t, r.calls)
	if !strings.Contains(strings.Join(r.calls, "\n"), "systemctl restart nixos-router-dnsmasq.service") {
		t.Fatal("DNS change was not applied")
	}
	got, _ := os.ReadFile(filepath.Join(m.base(), "wan-resolv.conf"))
	if string(got) != "nameserver 1.1.1.1\n" {
		t.Fatal("protocol DNS lost", string(got))
	}
	if err := m.HandleIPv4DNS(ctx, gen, managedPPPInterface, "bound", "9.9.9.9"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(m.base(), "wan-resolv.conf"))
	if string(got) != "nameserver 9.9.9.9\n" {
		t.Fatal("retained session callback rejected")
	}
	now = now.Add(2 * time.Minute)
	r.calls = nil
	if err := m.ExpireCandidate(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoLinkDisruption(t, r.calls)
	after, _ := os.ReadFile(filepath.Join(m.base(), "current", "pppoe.options"))
	if string(after) != string(options) {
		t.Fatal("PPP generation/options changed")
	}
	candidate, err := m.Candidate(ctx)
	if err != nil || candidate.State != "rolled-back" {
		t.Fatal(candidate, err)
	}
}

func TestFirewallUpdatePreservesNegotiatedMSS(t *testing.T) {
	m, r, c := continuityFixture(t, "pppoe")
	c.Firewall.BlockedMACs = []string{"02:00:00:00:00:01"}
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	assertNoLinkDisruption(t, r.calls)
	b, _ := os.ReadFile(filepath.Join(m.base(), "current", "firewall.nft"))
	for _, want := range []string{"02:00:00:00:00:01", "maxseg size set 1420", "maxseg size set 1400"} {
		if !strings.Contains(string(b), want) {
			t.Fatal("missing live policy", want)
		}
	}
}

func TestContinuityFallsBackForTopologyChangeOrDrift(t *testing.T) {
	for _, tc := range []string{"drift", "port-change", "cold-restore"} {
		t.Run(tc, func(t *testing.T) {
			m, r, c := continuityFixture(t, "dhcp")
			switch tc {
			case "drift":
				r.drift = true
			case "port-change":
				c.LAN.Ports = []string{"lan1"}
			case "cold-restore":
				m.publish(nil)
			}
			if _, err := m.Apply(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(r.calls, "\n"), "systemctl stop nixos-router-wan.service") {
				t.Fatal("unsafe continuity shortcut")
			}
		})
	}
}

func TestContinuityDNSFailureRestoresPreviousFiles(t *testing.T) {
	m, r, c := continuityFixture(t, "pppoe")
	before, err := os.ReadFile(filepath.Join(m.base(), "current", "dnsmasq.conf"))
	if err != nil {
		t.Fatal(err)
	}
	r.fail = "systemctl restart nixos-router-dnsmasq.service"
	c.DNS.Upstreams = []string{"8.8.8.8"}
	res, err := m.Apply(context.Background(), c)
	if err == nil || res.RollbackStatus != "succeeded" {
		t.Fatal(res, err)
	}
	after, err := os.ReadFile(filepath.Join(m.base(), "current", "dnsmasq.conf"))
	if err != nil || string(after) != string(before) {
		t.Fatal("previous DNS file not restored", err)
	}
	assertNoLinkDisruption(t, r.calls)
}

func TestContinuitySSIDChangeRestartsOnlyAP(t *testing.T) {
	m, r, c := continuityFixture(t, "dhcp")
	c.WiFi.Enable = true
	c.WiFi.BridgeToLan = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "before", PSK: "password123", Band: "2g", Channel: 6}}
	r.c = clone(c)
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	c.WiFi.APs[0].SSID = "after"
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	assertNoLinkDisruption(t, r.calls)
	joined := strings.Join(r.calls, "\n")
	if !strings.Contains(joined, "systemctl restart nixos-router-hostapd.service") {
		t.Fatal("AP change not activated")
	}
	if strings.Contains(joined, "systemctl restart nixos-router-dnsmasq.service") {
		t.Fatal("unchanged DNS restarted")
	}
}

func TestContinuityRejectsGenerationDirectorySymlink(t *testing.T) {
	m, _, c := continuityFixture(t, "dhcp")
	gen, _ := os.Readlink(filepath.Join(m.base(), "current"))
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Rename(filepath.Join(m.base(), gen), outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(m.base(), gen)); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(outside, "dnsmasq.conf"))
	c.DNS.Upstreams = []string{"8.8.8.8"}
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(outside, "dnsmasq.conf"))
	if string(after) != string(before) {
		t.Fatal("continuity followed a generation directory symlink")
	}
}

func TestContinuityAlwaysRestoresFirewall(t *testing.T) {
	m, r, c := continuityFixture(t, "dhcp")
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, call := range r.calls {
		if strings.HasPrefix(call, "nft -f ") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("unchanged policy was not restored to kernel: %d", count)
	}
	assertNoLinkDisruption(t, r.calls)
}

func TestContinuityFirewallWriteFailureRestoresKernel(t *testing.T) {
	m, r, c := continuityFixture(t, "dhcp")
	path := filepath.Join(m.base(), "current", "firewall.nft")
	previous, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var loaded []string
	injected := false
	blocked := false
	r.hook = func(n string, a []string) {
		if n == "ip" && blocked {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, previous, 0600); err != nil {
				t.Fatal(err)
			}
			blocked = false
		}
		if n == "nft" && len(a) == 2 && a[0] == "-f" {
			rules, err := os.ReadFile(a[1])
			if err != nil {
				t.Fatal(err)
			}
			loaded = append(loaded, string(rules))
			if !injected {
				// Force atomicFile's rename to fail after the candidate reached nft.
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				injected = true
				blocked = true
			}
		}
	}
	c.Firewall.BlockedMACs = []string{"02:00:00:00:00:01"}
	result, err := m.Apply(context.Background(), c)
	if err == nil || result.RollbackStatus != "succeeded" {
		t.Fatal(result, err)
	}
	if len(loaded) != 2 || !strings.Contains(loaded[0], "02:00:00:00:00:01") || loaded[1] != string(previous) {
		t.Fatal("rollback did not restore live firewall after file write failed", len(loaded))
	}
	assertNoLinkDisruption(t, r.calls)
}

func TestContinuitySupportsLargeGeneratedFiles(t *testing.T) {
	m, r, c := continuityFixture(t, "dhcp")
	// Real generated rules, rather than an artificial oversized context file.
	for i := 0; i < 1500; i++ {
		c.Firewall.BlockedMACs = append(c.Firewall.BlockedMACs, fmt.Sprintf("02:00:00:%02x:%02x:%02x", i>>16, (i>>8)&255, i&255))
	}
	for i := 0; i < 2; i++ {
		if _, err := m.Apply(context.Background(), c); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(m.base(), "current", "firewall.nft"))
	if err != nil || len(b) <= 16384 {
		t.Fatal("fixture did not exceed old 16KB limit", len(b), err)
	}
	assertNoLinkDisruption(t, r.calls)
}

func TestContinuityResamplesMTUAfterProtocolLock(t *testing.T) {
	m, r, c := continuityFixture(t, "pppoe")
	f, err := openIPv6PrivateFile(filepath.Join(m.base(), ".ipv6.lock"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	sampled := make(chan struct{})
	release := make(chan struct{})
	once := false
	r.hook = func(n string, a []string) {
		if !once && n == "systemctl" && strings.Join(a, " ") == "is-active --quiet nixos-router-dnsmasq.service" {
			once = true
			close(sampled)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() { _, err := m.Apply(context.Background(), c); done <- err }()
	select {
	case <-sampled:
	case <-time.After(3 * time.Second):
		t.Fatal("did not reach topology check")
	}
	// Simulate pre-up negotiating a smaller MTU while it owns the same lock.
	r.mtu = 1400
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Apply blocked")
	}
	rules, err := os.ReadFile(filepath.Join(m.base(), "current", "firewall.nft"))
	if err != nil || !strings.Contains(string(rules), "maxseg size set 1360") || !strings.Contains(string(rules), "maxseg size set 1340") {
		t.Fatal("Apply restored stale pre-reconnect MSS", err)
	}
	assertNoLinkDisruption(t, r.calls)
}
