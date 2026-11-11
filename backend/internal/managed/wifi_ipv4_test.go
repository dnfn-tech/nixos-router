package managed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type wifiIPv4Fake struct {
	t         *testing.T
	journal   string
	present   bool
	addresses []string
	mutations []string
	failAdd   bool
}

func (r *wifiIPv4Fake) Run(_ context.Context, name string, args ...string) (string, error) {
	command := name + " " + strings.Join(args, " ")
	if command == "ip -j addr show" {
		if !r.present {
			return "[]", nil
		}
		info := []map[string]any{}
		for _, cidr := range r.addresses {
			p := strings.Split(cidr, "/")
			prefix, err := strconv.Atoi(p[1])
			if err != nil {
				r.t.Fatal(err)
			}
			info = append(info, map[string]any{"family": "inet", "local": p[0], "prefixlen": prefix, "scope": "global"})
		}
		raw, _ := json.Marshal([]map[string]any{{"ifname": "wlan0", "addr_info": info}, {"ifname": "foreign0", "addr_info": []map[string]any{{"family": "inet", "local": "198.51.100.1", "prefixlen": 24, "scope": "global"}}}})
		return string(raw), nil
	}
	if name != "ip" || len(args) != 6 || args[0] != "-4" || args[1] != "addr" || args[4] != "dev" || args[5] != "wlan0" || (args[2] != "add" && args[2] != "del") {
		r.t.Fatalf("unexpected/unscoped operation: %s", command)
	}
	raw, err := os.ReadFile(r.journal)
	if err != nil {
		r.t.Fatal("missing write-ahead ownership", err)
	}
	var journal wifiIPv4Journal
	if json.Unmarshal(raw, &journal) != nil || !slices.Contains(journal.Addresses, args[3]) {
		r.t.Fatal("unrecorded mutation", string(raw), command)
	}
	r.mutations = append(r.mutations, command)
	if args[2] == "add" {
		if r.failAdd {
			r.failAdd = false
			return "", errors.New("injected address failure")
		}
		if slices.Contains(r.addresses, args[3]) {
			return "", errors.New("address already present")
		}
		r.addresses = append(r.addresses, args[3])
	} else {
		index := slices.Index(r.addresses, args[3])
		if index < 0 {
			return "", errors.New("address absent")
		}
		r.addresses = slices.Delete(r.addresses, index, index+1)
	}
	return "", nil
}

func wifiIPv4Fixture(t *testing.T) (*Manager, *wifiIPv4Fake, config.Config) {
	m, _, c := fixture(t)
	r := &wifiIPv4Fake{t: t, journal: filepath.Join(m.base(), "wifi-ipv4.json"), present: true}
	m.opts.Runner = r
	c.WiFi.Enable = true
	c.WiFi.BridgeToLan = false
	c.WiFi.Network = &config.WiFiNetwork{IPv4CIDR: "192.168.77.1/24", DHCP: config.DHCPv4Config{Enable: true, RangeStart: "192.168.77.100", RangeEnd: "192.168.77.110", LeaseMins: 30}}
	return m, r, c
}
func TestWiFiIPv4JournalLifecycleAndCrashRetry(t *testing.T) {
	m, r, c := wifiIPv4Fixture(t)
	ctx := context.Background()
	if err := m.preflightWiFiIPv4(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := m.reconcileWiFiIPv4(ctx, c); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.addresses, []string{c.WiFi.Network.IPv4CIDR}) {
		t.Fatal(r.addresses)
	}
	info, err := os.Stat(r.journal)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	restarted, err := New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.preflightWiFiIPv4(ctx, c); err != nil {
		t.Fatal("owned address rejected after restart", err)
	}
	if err = restarted.reconcileWiFiIPv4(ctx, c); err != nil || len(r.mutations) != 1 {
		t.Fatal("non-idempotent restore", r.mutations, err)
	}
	old := c.WiFi.Network.IPv4CIDR
	c.WiFi.Network.IPv4CIDR = "192.168.78.1/24"
	r.failAdd = true
	if err = restarted.reconcileWiFiIPv4(ctx, c); err == nil {
		t.Fatal("address failure hidden")
	}
	restarted, err = New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.reconcileWiFiIPv4(ctx, c); err != nil {
		t.Fatal("crash retry failed", err)
	}
	if slices.Contains(r.addresses, old) || !slices.Contains(r.addresses, c.WiFi.Network.IPv4CIDR) {
		t.Fatal(r.addresses)
	}
	// An address added by somebody else must survive our disable cleanup.
	r.addresses = append(r.addresses, "203.0.113.7/32")
	c.WiFi.Enable = false
	if err = restarted.reconcileWiFiIPv4(ctx, c); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.addresses, []string{"203.0.113.7/32"}) {
		t.Fatal("foreign address lost", r.addresses)
	}
	if _, err = os.Stat(r.journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal not cleared", err)
	}
}
func TestWiFiIPv4RejectsForeignAndHandlesVanishedLink(t *testing.T) {
	m, r, c := wifiIPv4Fixture(t)
	ctx := context.Background()
	r.addresses = []string{c.WiFi.Network.IPv4CIDR}
	if err := m.preflightWiFiIPv4(ctx, c); err == nil {
		t.Fatal("adopted foreign matching address")
	}
	if err := m.reconcileWiFiIPv4(ctx, c); err == nil || len(r.mutations) != 0 {
		t.Fatal("foreign address changed", r.mutations, err)
	}
	r.addresses = nil
	if err := m.reconcileWiFiIPv4(ctx, c); err != nil {
		t.Fatal(err)
	}
	r.present = false
	c.WiFi.BridgeToLan = true
	if err := m.reconcileWiFiIPv4(ctx, c); err != nil {
		t.Fatal("vanished interface cleanup", err)
	}
	if len(r.mutations) != 1 {
		t.Fatal("mutated absent interface", r.mutations)
	}
}

func TestWiFiIPv4RejectsInvalidPrivateJournal(t *testing.T) {
	for _, raw := range []string{
		`{"Version":2,"Interface":"wlan0","Addresses":[]}`,
		`{"Version":1,"Interface":"foreign0","Addresses":["192.168.77.1/24"]}`,
		`{"Version":1,"Interface":"wlan0","Addresses":["192.168.77.0/24"]}`,
		`{"Version":1,"Interface":"wlan0","Addresses":["192.168.77.255/24"]}`,
		`{"Version":1,"Interface":"wlan0","Addresses":["192.168.77.1/24","192.168.77.1/24"]}`,
		`{"Version":1,"Interface":"wlan0","Addresses":["2001:db8::1/64"]}`,
		`{"Version":1,"Interface":"wlan0","Addresses":[],"command":"arbitrary"}`,
		`{"Version":1,"Interface":"wlan0","Addresses":[]} {}`,
	} {
		m, r, c := wifiIPv4Fixture(t)
		if err := os.WriteFile(r.journal, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := m.preflightWiFiIPv4(context.Background(), c); err == nil {
			t.Fatal("invalid journal accepted", raw)
		}
		if len(r.mutations) != 0 {
			t.Fatal(r.mutations)
		}
	}
	t.Run("symlink", func(t *testing.T) {
		m, r, c := wifiIPv4Fixture(t)
		target := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(target, []byte(`{"Version":1,"Interface":"wlan0","Addresses":[]}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, r.journal); err != nil {
			t.Fatal(err)
		}
		if err := m.reconcileWiFiIPv4(context.Background(), c); err == nil {
			t.Fatal("followed journal symlink")
		}
	})
}

type wifiApplyRunner struct {
	base recorder
	wifi *wifiIPv4Fake
}

func (r *wifiApplyRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	command := name + " " + strings.Join(args, " ")
	if command == "ip -j addr show" || (name == "ip" && len(args) == 6 && args[0] == "-4" && args[1] == "addr" && args[5] == "wlan0") {
		return r.wifi.Run(ctx, name, args...)
	}
	return r.base.Run(ctx, name, args...)
}
func TestRoutedWiFiApplyTransitionsAndFailureRecovery(t *testing.T) {
	m, r, c := wifiIPv4Fixture(t)
	runner := &wifiApplyRunner{wifi: r}
	m.opts.Runner = runner
	c.WiFi.APs = wifiTestAP()
	ctx := context.Background()
	if _, err := m.Apply(ctx, c); err != nil {
		t.Fatal("routed apply", err)
	}
	changed := clone(c)
	changed.WiFi.Network.IPv4CIDR = "192.168.78.1/24"
	changed.WiFi.Network.DHCP.RangeStart = "192.168.78.100"
	changed.WiFi.Network.DHCP.RangeEnd = "192.168.78.110"
	runner.base.fail = "nft -f"
	res, err := m.Apply(ctx, changed)
	if err == nil || res.RollbackStatus != "succeeded" || !slices.Equal(r.addresses, []string{"192.168.77.1/24"}) {
		t.Fatal("failed candidate did not restore owned address", res, r.addresses, err)
	}
	bridged := clone(c)
	bridged.WiFi.BridgeToLan = true
	if _, err = m.Apply(ctx, bridged); err != nil || len(r.addresses) != 0 {
		t.Fatal("routed to bridge cleanup", r.addresses, err)
	}
	if _, err = m.Apply(ctx, c); err != nil {
		t.Fatal("bridge to routed", err)
	}
	disabled := clone(c)
	disabled.WiFi.Enable = false
	if _, err = m.Apply(ctx, disabled); err != nil || len(r.addresses) != 0 {
		t.Fatal("disable cleanup", r.addresses, err)
	}
}
