package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type recorder struct {
	calls  []string
	fail   string
	failed bool
}

func (r *recorder) Run(ctx context.Context, name string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		return "", errors.New("missing deadline")
	}
	s := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, s)
	if r.fail != "" && strings.Contains(s, r.fail) && !r.failed {
		r.failed = true
		return "", errors.New("injected failure")
	}
	if name == "systemctl" && len(args) > 0 && args[0] == "show" {
		return "inactive\n", nil
	}
	if name == "hostapd_cli" {
		return "state=ENABLED\n", nil
	}
	return "[]", nil
}
func fixture(t *testing.T) (*Manager, *recorder, config.Config) {
	t.Helper()
	r := &recorder{}
	m, e := New(Options{StateDir: t.TempDir(), Interfaces: []string{"wan0", "lan0", "lan1"}, WiFiInterface: "wlan0", CountryCode: "US", ManagementPort: 8080, Runner: r})
	if e != nil {
		t.Fatal(e)
	}
	c := config.DefaultConfig()
	c.WAN.Interface = "wan0"
	c.LAN.Ports = []string{"lan0"}
	return m, r, c
}
func TestRejectBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*config.Config)
	}{
		{"foreign", func(c *config.Config) { c.LAN.Ports = []string{"eth-host"} }},
		{"wan bridge collision", func(c *config.Config) { c.LAN.Ports = []string{"wan0"} }},
		{"bridge physical collision", func(c *config.Config) { c.LAN.BridgeName = "lan1" }},
		{"injected domain", func(c *config.Config) { c.DNS.Domain = "lan\ninterface=eth-host" }},
		{"unsupported qos", func(c *config.Config) { c.QoS.Enable = true }},
		{"unsupported ipv6", func(c *config.Config) { c.IPv6.Enable = true }},
		{"pool outside lan", func(c *config.Config) { c.LAN.DHCP.RangeStart = "10.0.0.2" }},
		{"foreign forward", func(c *config.Config) {
			c.Firewall.PortForwards = []config.PortForward{{Protocol: "tcp", ExternalPort: 80, DestPort: 80, DestIP: "10.0.0.1"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, r, c := fixture(t)
			tc.change(&c)
			if _, e := m.Apply(context.Background(), c); e == nil {
				t.Fatal("accepted invalid config")
			}
			if len(r.calls) != 0 {
				t.Fatal(r.calls)
			}
		})
	}
}
func TestApplyFilesOwnershipAndDisable(t *testing.T) {
	m, r, c := fixture(t)
	c.WiFi.Enable = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "a#b", PSK: "password123", Band: "2g", Channel: 6}}
	c.Firewall.BlockedMACs = []string{"02:00:00:00:00:01"}
	c.Firewall.PortForwards = []config.PortForward{{Protocol: "tcp", ExternalPort: 8081, DestPort: 80, DestIP: "192.168.1.2"}}
	res, e := m.Apply(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	if res.Stage != "awaiting-wan-lease" {
		t.Fatal(res)
	}
	for _, f := range []string{"hostapd.conf", "dnsmasq.conf", "wan.env", "firewall.nft"} {
		p := filepath.Join(m.opts.StateDir, "managed/current", f)
		s, e := os.Stat(p)
		if e != nil {
			t.Fatal(e)
		}
		if s.Mode().Perm() != 0600 {
			t.Fatalf("%s mode %o", f, s.Mode().Perm())
		}
	}
	b, _ := os.ReadFile(filepath.Join(m.opts.StateDir, "managed/current/firewall.nft"))
	s := string(b)
	for _, want := range []string{"table inet nixos_router", "masquerade", "dnat ip to 192.168.1.2:80", "ether saddr 02:00:00:00:00:01 drop", "iifname \"wan0\""} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q: %s", want, s)
		}
	}
	if strings.Contains(s, "flush ruleset") {
		t.Fatal("global ownership")
	}
	c.WiFi.Enable = false
	c.DNS.EnableDNSMasq = false
	c.LAN.DHCP.Enable = false
	c.Firewall.NATEnabled = false
	c.Firewall.PortForwards = nil
	c.Firewall.BlockedMACs = nil
	c.LAN.Ports = []string{"lan1"}
	r.calls = nil
	if _, e = m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	calls := strings.Join(r.calls, "\n")
	for _, want := range []string{"systemctl stop nixos-router-hostapd.service", "ip link set dev lan0 nomaster", "systemctl stop nixos-router-dnsmasq.service"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %s: %s", want, calls)
		}
	}
	b, _ = os.ReadFile(filepath.Join(m.opts.StateDir, "managed/current/firewall.nft"))
	if strings.Contains(string(b), "masquerade") {
		t.Fatal("stale NAT")
	}
}
func TestFailureRollbackAndLastGoodRestore(t *testing.T) {
	m, r, c := fixture(t)
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	changed := c
	changed.LAN.IPv4CIDR = "192.168.1.254/24"
	r.fail = "systemctl restart nixos-router-dnsmasq"
	res, e := m.Apply(context.Background(), changed)
	if e == nil || res.RollbackStatus != "succeeded" {
		t.Fatalf("%+v %v", res, e)
	}
	r.calls = nil
	if e = m.Restore(context.Background(), changed); e != nil {
		t.Fatal(e)
	}
	s := strings.Join(r.calls, "\n")
	if !strings.Contains(s, "192.168.1.1/24") {
		t.Fatal("draft restored", s)
	}
	if strings.Contains(s, "192.168.1.254/24") {
		t.Fatal("draft restored", s)
	}
}

type replyRunner struct {
	recorder
	links      string
	active     string
	alwaysFail string
}

func (r *replyRunner) Run(ctx context.Context, n string, a ...string) (string, error) {
	s := n + " " + strings.Join(a, " ")
	if r.alwaysFail != "" && strings.Contains(s, r.alwaysFail) {
		return "", errors.New("permanent failure")
	}
	out, e := r.recorder.Run(ctx, n, a...)
	if n == "ip" && len(a) > 1 && a[0] == "-j" && r.links != "" {
		return r.links, e
	}
	if n == "systemctl" && len(a) > 1 && a[0] == "show" && a[len(a)-1] == r.active {
		return "active\n", nil
	}
	return out, e
}
func TestForeignBridgeAndManagerConflict(t *testing.T) {
	for _, tc := range []struct{ name, links, active string }{{"foreign bridge", `[{"ifname":"br-lan","linkinfo":{"info_kind":"bridge"}}]`, ""}, {"foreign master", `[{"ifname":"lan0","master":"foreign"}]`, ""}, {"network manager", "[]", "NetworkManager.service"}} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, c := fixture(t)
			r := &replyRunner{links: tc.links, active: tc.active}
			m.opts.Runner = r
			if _, e := m.Apply(context.Background(), c); e == nil {
				t.Fatal("accepted conflicting owner")
			}
			for _, s := range r.calls {
				if strings.Contains(s, "link set") || strings.Contains(s, "systemctl stop") {
					t.Fatal("mutated before rejection", s)
				}
			}
		})
	}
}
func TestRollbackFailureIsExplicit(t *testing.T) {
	m, _, c := fixture(t)
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	m.opts.Runner = &replyRunner{alwaysFail: "systemctl restart nixos-router-dnsmasq"}
	res, e := m.Apply(context.Background(), c)
	if e == nil || res.RollbackStatus != "failed" || !strings.Contains(e.Error(), "rollback failed") {
		t.Fatalf("%+v %v", res, e)
	}
}
func TestCorruptLastGoodNeverFallsBack(t *testing.T) {
	m, _, _ := fixture(t)
	if e := os.WriteFile(m.lastGood(), []byte("bad-json"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := New(m.opts); e == nil {
		t.Fatal("corrupt last-good accepted")
	}
}
func TestWANGenerationAndAtomicLastGood(t *testing.T) {
	m, _, c := fixture(t)
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(m.base(), "current/wan.env")
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "WAN_GENERATION='generation-") {
		t.Fatal("generation missing", string(b))
	}
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	next, _ := os.ReadFile(p)
	if string(b) == string(next) {
		t.Fatal("generation reused")
	}
	st, _ := os.Stat(m.lastGood())
	if st.Mode().Perm() != 0600 {
		t.Fatal("last-good secrets exposed")
	}
}
func TestFirstFailureStopsServices(t *testing.T) {
	m, r, c := fixture(t)
	r.fail = "systemctl restart nixos-router-dnsmasq"
	res, e := m.Apply(context.Background(), c)
	if e == nil || res.RollbackStatus != "succeeded" {
		t.Fatalf("%+v %v", res, e)
	}
	if _, e = os.Stat(m.lastGood()); !os.IsNotExist(e) {
		t.Fatal("failed config persisted")
	}
}
func TestRenderProtocolContent(t *testing.T) {
	m, _, c := fixture(t)
	c.LAN.StaticLeases = []config.StaticLease{{MAC: "02:00:00:00:00:01", IP: "192.168.1.20", Hostname: "printer"}}
	c.WiFi.Enable = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "a=b", PSK: "password123", Channel: 6}}
	f := m.render(c)
	for _, s := range []string{"dhcp-range=192.168.1.100,192.168.1.200,1440m", "dhcp-option=option:router,192.168.1.1", "dhcp-host=02:00:00:00:00:01,192.168.1.20,printer", "server=1.1.1.1"} {
		if !strings.Contains(f["dnsmasq.conf"], s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"ssid2=613d62", "bridge=br-lan", "wpa=2", "wpa_passphrase=password123"} {
		if !strings.Contains(f["hostapd.conf"], s) {
			t.Fatal(s)
		}
	}
}
func TestWANMayNotAlreadyBeBridged(t *testing.T) {
	m, _, c := fixture(t)
	m.opts.Runner = &replyRunner{links: `[{"ifname":"wan0","master":"br-lan"},{"ifname":"br-lan","ifalias":"nixos-router-managed","linkinfo":{"info_kind":"bridge"}}]`}
	if _, e := m.Apply(context.Background(), c); e == nil {
		t.Fatal("WAN bridge membership accepted")
	}
}
func TestRejectUnsafeWirelessAndUnsupported(t *testing.T) {
	for _, change := range []func(*config.Config){func(c *config.Config) { c.WiFi.APs[0].PSK = "password\ninterface=eth0" }, func(c *config.Config) { c.WiFi.APs[0].Guest = true }, func(c *config.Config) { c.WiFi.APs[0].Band = "6g" }, func(c *config.Config) { c.WiFi.BridgeToLan = false }, func(c *config.Config) { c.Firewall.UPnPEnable = true }, func(c *config.Config) { c.Plugins = map[string]config.PluginConfig{"foo": {Enable: true}} }} {
		m, r, c := fixture(t)
		c.WiFi.Enable = true
		c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "test", PSK: "password123", Channel: 6}}
		change(&c)
		if _, e := m.Apply(context.Background(), c); e == nil {
			t.Fatal("unsafe/unsupported accepted")
		}
		if len(r.calls) > 0 {
			t.Fatal("mutated")
		}
	}
}
func TestStaticWANAndSSHChanges(t *testing.T) {
	m, r, c := fixture(t)
	c.WAN.Mode = "static"
	c.WAN.Static = &config.StaticIP{AddressCIDR: "10.0.0.2/24", Gateway: "10.0.0.1"}
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	calls := strings.Join(r.calls, "\n")
	if !strings.Contains(calls, "ip route replace default via 10.0.0.1 dev wan0") {
		t.Fatal(calls)
	}
	if strings.Contains(calls, "restart nixos-router-wan") {
		t.Fatal("DHCP started for static WAN")
	}
	c.SSH.Port = 2222
	r.calls = nil
	if _, e := m.Apply(context.Background(), c); e == nil {
		t.Fatal("SSH silently accepted")
	}
	if len(r.calls) > 0 {
		t.Fatal("mutated")
	}
}
func TestCrossProcessTransactionLock(t *testing.T) {
	m, _, c := fixture(t)
	other, e := New(m.opts)
	if e != nil {
		t.Fatal(e)
	}
	lock, e := m.transactionLock()
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	res, e := other.Apply(context.Background(), c)
	if e == nil || res.Stage != "lock" {
		t.Fatalf("second manager entered transaction: %+v %v", res, e)
	}
}

type persistFailureRunner struct {
	recorder
	target string
}

func (r *persistFailureRunner) Run(ctx context.Context, n string, a ...string) (string, error) {
	out, e := r.recorder.Run(ctx, n, a...)
	if n == "systemctl" && strings.Join(a, " ") == "is-active --quiet nixos-router-dnsmasq.service" {
		if err := os.Mkdir(r.target, 0700); err != nil {
			return "", err
		}
	}
	return out, e
}
func TestPersistenceFailureDoesNotReportSuccess(t *testing.T) {
	m, _, c := fixture(t)
	m.opts.Runner = &persistFailureRunner{target: m.lastGood()}
	res, e := m.Apply(context.Background(), c)
	if e == nil || res.Stage != "persist" {
		t.Fatalf("%+v %v", res, e)
	}
	if _, e = os.Stat(m.lastGood()); !os.IsNotExist(e) {
		t.Fatal("failed commit left invalid last-good artifact")
	}
}
func TestAppliedConfigIsImmutableSnapshot(t *testing.T) {
	m, _, c := fixture(t)
	if _, ok := m.AppliedConfig(); ok {
		t.Fatal("unapplied bootstrap reported active")
	}
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	got, ok := m.AppliedConfig()
	if !ok {
		t.Fatal("missing applied config")
	}
	got.LAN.Ports[0] = "host-interface"
	again, _ := m.AppliedConfig()
	if again.LAN.Ports[0] != "lan0" {
		t.Fatal("snapshot was mutable")
	}
}

func TestManagementPortsProtectedWithFirewallDisabled(t *testing.T) {
	m, _, c := fixture(t)
	c.Firewall.Enable = false
	for _, port := range []int{22, 2222} {
		m.opts.SSHPorts = []int{port}
		c.SSH.Port = port
		rules := m.render(c)["firewall.nft"]
		want := fmt.Sprintf(`iifname != { "lo", "br-lan" } tcp dport { 8080, %d } drop`, port)
		if !strings.Contains(rules, want) {
			t.Fatalf("missing SSH/API LAN-only guard %s:\n%s", want, rules)
		}
	}
}
func TestPortForwardRangeMapsEveryExternalPortToOneDestination(t *testing.T) {
	m, _, c := fixture(t)
	c.Firewall.PortForwards = []config.PortForward{{Protocol: "tcp", ExternalPort: 8000, ExternalPortEnd: 8010, DestIP: "192.168.1.20", DestPort: 80}}
	if e := m.validate(c); e != nil {
		t.Fatal(e)
	}
	rules := m.render(c)["firewall.nft"]
	if !strings.Contains(rules, `tcp dport 8000-8010 dnat ip to 192.168.1.20:80`) {
		t.Fatal(rules)
	}
}

type wanAddressRunner struct {
	recorder
	addresses    string
	addressError error
}

func (r *wanAddressRunner) Run(ctx context.Context, n string, a ...string) (string, error) {
	out, e := r.recorder.Run(ctx, n, a...)
	if n == "ip" && strings.Join(a, " ") == "-j -4 addr show dev wan0" {
		return r.addresses, r.addressError
	}
	return out, e
}
func TestDHCPLeaseObservationIsSeparateFromActivation(t *testing.T) {
	for _, tc := range []struct {
		name, addresses, stage, warning string
		err                             error
	}{
		{name: "lease pending", addresses: "[]", stage: "awaiting-wan-lease", warning: "awaiting an IPv4 lease"},
		{name: "lease ready", addresses: `[{"addr_info":[{"family":"inet","local":"10.0.0.20","scope":"global"}]}]`, stage: "complete"},
		{name: "link local only", addresses: `[{"addr_info":[{"family":"inet","local":"169.254.1.2","scope":"link"}]}]`, stage: "awaiting-wan-lease", warning: "awaiting an IPv4 lease"},
		{name: "read failure", stage: "wan-status-unknown", warning: "WAN address observation failed", err: errors.New("netlink unavailable")},
		{name: "malformed read", addresses: "invalid json", stage: "wan-status-unknown", warning: "WAN address observation failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, c := fixture(t)
			m.opts.Runner = &wanAddressRunner{addresses: tc.addresses, addressError: tc.err}
			res, e := m.Apply(context.Background(), c)
			if e != nil {
				t.Fatal("valid config should stay applied", e)
			}
			if res.Stage != tc.stage || !strings.Contains(strings.Join(res.Warnings, "\n"), tc.warning) {
				t.Fatalf("%+v", res)
			}
			if _, ok := m.AppliedConfig(); !ok {
				t.Fatal("activation not published")
			}
		})
	}
}

func TestChangingOwnedBridgeOrWANRequiresExplicitMigration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*config.Config)
	}{
		{"bridge", func(c *config.Config) { c.LAN.BridgeName = "br-new" }},
		{"wan", func(c *config.Config) { c.WAN.Interface = "lan1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, r, c := fixture(t)
			if _, e := m.Apply(context.Background(), c); e != nil {
				t.Fatal(e)
			}
			r.calls = nil
			tc.change(&c)
			res, e := m.Apply(context.Background(), c)
			if e == nil || res.Stage != "validate" {
				t.Fatalf("ownership migration accepted: %+v %v", res, e)
			}
			if len(r.calls) > 0 {
				t.Fatalf("ownership change executed commands: %v", r.calls)
			}
		})
	}
}
func TestConnManConflictBeforeMutation(t *testing.T) {
	m, _, c := fixture(t)
	r := &replyRunner{active: "connman.service"}
	m.opts.Runner = r
	if _, e := m.Apply(context.Background(), c); e == nil {
		t.Fatal("ConnMan conflict accepted")
	}
	for _, s := range r.calls {
		if strings.Contains(s, "systemctl stop") || strings.Contains(s, "link set") {
			t.Fatal("mutated before conflict rejection", s)
		}
	}
}

func TestDHCPWithoutLocalDNSRequiresExplicitUsableServers(t *testing.T) {
	m, r, c := fixture(t)
	c.DNS.EnableDNSMasq = false
	c.DNS.Upstreams = nil
	if _, e := m.Apply(context.Background(), c); e == nil {
		t.Fatal("dead router DNS advertised")
	}
	if len(r.calls) > 0 {
		t.Fatal("commands executed before invalid DNS rejection")
	}
	c.WAN.Mode = "static"
	c.WAN.Static = &config.StaticIP{AddressCIDR: "10.0.0.2/24", DNS: []string{"10.0.0.1"}}
	if e := m.validate(c); e != nil {
		t.Fatal(e)
	}
	dns := m.render(c)["dnsmasq.conf"]
	if !strings.Contains(dns, "dhcp-option=option:dns-server,10.0.0.1\n") {
		t.Fatal(dns)
	}
}

type apPendingRunner struct{ recorder }

func (r *apPendingRunner) Run(ctx context.Context, n string, a ...string) (string, error) {
	out, e := r.recorder.Run(ctx, n, a...)
	if n == "hostapd_cli" {
		return "state=DISABLED\n", nil
	}
	return out, e
}
func TestHostapdMustReportEnabledBeforeCommit(t *testing.T) {
	m, _, c := fixture(t)
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	m.opts.Runner = &apPendingRunner{}
	c.WiFi.Enable = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "test", PSK: "password123", Channel: 6}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, e := m.Apply(ctx, c)
	if e == nil || res.Stage != "activate" || res.RollbackStatus != "succeeded" {
		t.Fatalf("unready AP committed: %+v %v", res, e)
	}
	active, _ := m.AppliedConfig()
	if active.WiFi.Enable {
		t.Fatal("unready AP published")
	}
}

// The subprocess helper performs no network operations. Its sole purpose is to
// reproduce a child holding inherited stdout open after the command exits.
func TestCommandRunnerHelper(t *testing.T) {
	switch os.Args[len(os.Args)-1] {
	case "managed-runner-hold-pipe":
		time.Sleep(3 * time.Second)
		os.Exit(0)
	case "managed-runner-parent":
		child := exec.Command(os.Args[0], "-test.run=TestCommandRunnerHelper", "--", "managed-runner-hold-pipe")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if e := child.Start(); e != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}
func TestCommandRunnerBoundsInheritedPipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, e := (commandRunner{}).Run(ctx, os.Args[0], "-test.run=TestCommandRunnerHelper", "--", "managed-runner-parent")
	if !errors.Is(e, exec.ErrWaitDelay) {
		t.Fatalf("inherited pipe was not bounded by WaitDelay: %v", e)
	}
}

// Real isolated-netns reproduction: Linux bridge creation ignored IFLA_IFALIAS
// supplied on RTM_NEWLINK; a subsequent link set is required to establish it.
type kernelBridgeRunner struct {
	recorder
	exists, tagged, failTag bool
}

func (r *kernelBridgeRunner) Run(ctx context.Context, n string, a ...string) (string, error) {
	out, e := r.recorder.Run(ctx, n, a...)
	if e != nil {
		return out, e
	}
	joined := strings.Join(a, " ")
	if n == "ip" && strings.HasPrefix(joined, "link add name br-lan ") {
		r.exists = true
	}
	if n == "ip" && joined == "link set dev br-lan alias nixos-router-managed" {
		if r.failTag {
			return "", errors.New("tagging failed")
		}
		r.tagged = true
	}
	if n == "ip" && joined == "link delete dev br-lan" {
		r.exists = false
		r.tagged = false
	}
	if n == "ip" && (joined == "-j -d link show" || joined == "-j link show") {
		if !r.exists {
			return "[]", nil
		}
		alias := ""
		if r.tagged {
			alias = "nixos-router-managed"
		}
		return fmt.Sprintf(`[{"ifname":"br-lan","ifalias":%q,"linkinfo":{"info_kind":"bridge"}}]`, alias), nil
	}
	return out, nil
}
func TestBridgeOwnershipSurvivesRepeatedApplyWithRealKernelAliasSemantics(t *testing.T) {
	m, _, c := fixture(t)
	r := &kernelBridgeRunner{}
	m.opts.Runner = r
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal("second apply rejected its own bridge", e)
	}
	if !r.tagged {
		t.Fatal("bridge was never tagged")
	}
}
func TestFailedBridgeTaggingRemovesOnlyJustCreatedBridge(t *testing.T) {
	m, _, c := fixture(t)
	r := &kernelBridgeRunner{failTag: true}
	m.opts.Runner = r
	res, e := m.Apply(context.Background(), c)
	if e == nil || res.RollbackStatus != "succeeded" || r.exists {
		t.Fatalf("tagging failure left unowned bridge: %+v %v exists=%v", res, e, r.exists)
	}
}

func TestFiveGHzRejectsDFSAndInvalidChannelsBeforeCommands(t *testing.T) {
	for _, channel := range []int{0, 37, 52, 56, 60, 64, 100, 116, 132, 144, 166} {
		t.Run(fmt.Sprint(channel), func(t *testing.T) {
			m, r, c := fixture(t)
			c.WiFi.Enable = true
			c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "test", PSK: "password123", Band: "5g", Channel: channel}}
			res, e := m.Apply(context.Background(), c)
			if e == nil || res.Stage != "validate" {
				t.Fatalf("unsupported 5GHz channel accepted: %+v %v", res, e)
			}
			if len(r.calls) > 0 {
				t.Fatalf("unsupported channel executed commands: %v", r.calls)
			}
		})
	}
	for _, channel := range []int{36, 40, 44, 48, 149, 153, 157, 161, 165} {
		m, _, c := fixture(t)
		c.WiFi.Enable = true
		c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "test", PSK: "password123", Band: "5g", Channel: channel}}
		if e := m.validate(c); e != nil {
			t.Fatalf("non-DFS channel %d rejected: %v", channel, e)
		}
	}
}

type topologyRunner struct {
	recorder
	masters map[string]string
}

func (r *topologyRunner) Run(ctx context.Context, n string, a ...string) (string, error) {
	out, e := r.recorder.Run(ctx, n, a...)
	if e != nil {
		return out, e
	}
	if n == "ip" && len(a) > 2 && a[0] == "-j" && (a[1] == "link" || a[1] == "-d") {
		links := []map[string]any{{"ifname": "br-lan", "ifalias": "nixos-router-managed", "linkinfo": map[string]string{"info_kind": "bridge"}}}
		for name, master := range r.masters {
			links = append(links, map[string]any{"ifname": name, "master": master})
		}
		b, _ := json.Marshal(links)
		return string(b), nil
	}
	if n == "ip" && len(a) >= 5 && a[0] == "link" && a[1] == "set" && a[2] == "dev" {
		if a[4] == "nomaster" {
			r.masters[a[3]] = ""
		}
		if a[4] == "master" && len(a) == 6 {
			r.masters[a[3]] = a[5]
		}
	}
	return out, nil
}
func TestRestoreRemovesCrashLeftoverAuthorizedBridgeMembers(t *testing.T) {
	m, _, c := fixture(t)
	r := &topologyRunner{masters: map[string]string{"wan0": "", "lan0": "br-lan", "lan1": "", "foreign0": "br-lan"}}
	m.opts.Runner = r
	if _, e := m.Apply(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	r.masters["lan1"] = "br-lan"  // candidate added it, then crashed before last-good commit
	r.masters["wlan0"] = "br-lan" // separately owned AP also enabled by interrupted candidate
	restored, e := New(m.opts)
	if e != nil {
		t.Fatal(e)
	}
	if e = restored.Restore(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	if r.masters["lan1"] != "" {
		t.Fatal("crashed candidate membership survived restore")
	}
	if r.masters["wlan0"] != "" {
		t.Fatal("disabled WiFi candidate membership survived restore")
	}
	if r.masters["lan0"] != "br-lan" || r.masters["foreign0"] != "br-lan" {
		t.Fatal("target or unowned link changed", r.masters)
	}
}

func TestManagementGuardUsesHostSSHPortsNotDraft(t *testing.T) {
	m, _, c := fixture(t)
	opts := m.opts
	opts.SSHPorts = []int{22, 2200, 22}
	m, e := New(opts)
	if e != nil {
		t.Fatal(e)
	}
	c.SSH.Port = 2222
	c.Firewall.Enable = false
	rules := m.render(c)["firewall.nft"]
	if !strings.Contains(rules, `tcp dport { 8080, 22, 2200 } drop`) || strings.Contains(rules, "2222") {
		t.Fatal(rules)
	}
	if len(m.opts.SSHPorts) != 2 {
		t.Fatal("SSH ports not deduplicated")
	}
	for _, bad := range []int{0, -1, 65536} {
		opts.SSHPorts = []int{bad}
		if _, e = New(opts); e == nil {
			t.Fatalf("accepted invalid host SSH port %d", bad)
		}
	}
}

func generationCount(t *testing.T, m *Manager) int {
	t.Helper()
	entries, e := os.ReadDir(m.base())
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "generation-") {
			count++
		}
	}
	return count
}
func TestGenerationHistoryBoundedAndCurrentPreserved(t *testing.T) {
	m, _, c := fixture(t)
	for i := 0; i < 7; i++ {
		if _, e := m.Apply(context.Background(), c); e != nil {
			t.Fatal(e)
		}
	}
	if n := generationCount(t, m); n > 3 {
		t.Fatalf("generation secrets accumulated: %d", n)
	}
	current, e := os.Readlink(filepath.Join(m.base(), "current"))
	if e != nil {
		t.Fatal(e)
	}
	currentPath := filepath.Join(m.base(), current)
	old := time.Now().Add(-24 * time.Hour)
	if e = os.Chtimes(currentPath, old, old); e != nil {
		t.Fatal(e)
	}
	r := &recorder{fail: "nft --check"}
	m.opts.Runner = r
	if _, e = m.Apply(context.Background(), c); e == nil {
		t.Fatal("preflight fault missing")
	}
	after, e := os.Readlink(filepath.Join(m.base(), "current"))
	if e != nil || after != current {
		t.Fatal("current pointer changed on preflight failure", after, e)
	}
	if _, e = os.ReadFile(filepath.Join(m.base(), "current/dnsmasq.conf")); e != nil {
		t.Fatal("current files removed", e)
	}
	if n := generationCount(t, m); n > 3 {
		t.Fatalf("preflight candidate leaked: %d", n)
	}
}
func TestPreflightFailureDeletesItsSecretCandidate(t *testing.T) {
	m, r, c := fixture(t)
	r.fail = "nft --check"
	if _, e := m.Apply(context.Background(), c); e == nil {
		t.Fatal("preflight fault missing")
	}
	if n := generationCount(t, m); n != 0 {
		t.Fatalf("failed preflight retained %d candidate dirs", n)
	}
}

type ownershipRunner struct {
	recorder
	state string
	err   error
}

func (r *ownershipRunner) Run(ctx context.Context, n string, a ...string) (string, error) {
	if n == "systemctl" && len(a) > 0 && a[0] == "show" {
		r.calls = append(r.calls, n+" "+strings.Join(a, " "))
		return r.state, r.err
	}
	return r.recorder.Run(ctx, n, a...)
}
func TestOwnershipQueryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		err         error
	}{
		{"bus", "Failed to connect to bus", errors.New("bus unavailable")},
		{"command timeout", "", context.DeadlineExceeded},
		{"inactive with error", "inactive", errors.New("query failed")},
		{"empty", "", nil}, {"unknown", "unknown", nil},
		{"deactivating", "deactivating", nil}, {"failed", "failed", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, c := fixture(t)
			r := &ownershipRunner{state: tc.state, err: tc.err}
			m.opts.Runner = r
			result, err := m.Apply(context.Background(), c)
			if err == nil || result.Stage != "preflight" {
				t.Fatalf("accepted uncertain ownership: %+v %v", result, err)
			}
			for _, call := range r.calls {
				if !strings.HasPrefix(call, "systemctl show ") {
					t.Fatalf("command after failed ownership: %s", call)
				}
			}
		})
	}
}
func TestOwnershipInactivePermitsApply(t *testing.T) {
	m, _, c := fixture(t)
	m.opts.Runner = &ownershipRunner{state: "inactive\n"}
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}
