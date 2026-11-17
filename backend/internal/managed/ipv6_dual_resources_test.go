package managed

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dualIPv6Fixture(t *testing.T) (*Manager, *ipv6NetworkFake, string) {
	m, r, generation := resourceFixture(t)
	path := filepath.Join(m.base(), generation, "ipv6-context.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c ipv6RuntimeConfig
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	c.WiFiLAN = "wlan0"
	raw, _ = json.Marshal(c)
	if err = atomicFile(path, raw); err != nil {
		t.Fatal(err)
	}
	return m, r, generation
}
func TestIPv6DualLANResourceLifecycle(t *testing.T) {
	m, r, gen := dualIPv6Fixture(t)
	ctx := context.Background()
	event := leaseEvent(t, "bound")
	if err := m.reconcileIPv6Event(ctx, gen, "wan0", event, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	var wired, wifi netip.Prefix
	for _, resource := range r.live {
		if resource.Kind != "address" {
			continue
		}
		switch resource.Interface {
		case "br-lan":
			wired = resource.Prefix.Masked()
		case "wlan0":
			wifi = resource.Prefix.Masked()
		}
	}
	if !wired.IsValid() || !wifi.IsValid() || wired.Overlaps(wifi) || wired.String() != "2001:db8:100::/64" || wifi.String() != "2001:db8:100:1::/64" {
		t.Fatal(wired, wifi)
	}
	event.State = "updated"
	event.Prefixes = []ipv6Delegation{{Prefix: netip.MustParsePrefix("2001:db8:200::/60"), Preferred: 30, Valid: 60}}
	if err := m.reconcileIPv6Event(ctx, gen, "wan0", event, time.Unix(1001, 0)); err != nil {
		t.Fatal(err)
	}
	for _, resource := range r.live {
		if resource.Interface != "wan0" && resource.Prefix.Overlaps(netip.MustParsePrefix("2001:db8:100::/60")) {
			t.Fatal("old LAN prefix survived renumber", resource)
		}
	}
	// A /64 cannot serve two networks: remove both old LAN assignments while
	// preserving independently valid WAN resources and report the capacity issue.
	event.Prefixes = []ipv6Delegation{{Prefix: netip.MustParsePrefix("2001:db8:300::/64"), Preferred: 30, Valid: 60}}
	if err := m.reconcileIPv6Event(ctx, gen, "wan0", event, time.Unix(1002, 0)); err == nil {
		t.Fatal("insufficient delegation not reported")
	}
	if len(r.live) == 0 {
		t.Fatal("WAN resources lost with LAN capacity")
	}
	for _, resource := range r.live {
		if resource.Interface != "wan0" {
			t.Fatal("partial dual LAN left active", resource)
		}
	}
	if err := m.reconcileIPv6Event(ctx, gen, "wan0", ipv6Event{State: "stopped"}, time.Unix(1003, 0)); err != nil {
		t.Fatal(err)
	}
	if len(r.live) != 0 {
		t.Fatal("owned dual resources leaked", r.live)
	}
}
func TestIPv6DualLANContextAuthorization(t *testing.T) {
	m, _, gen := dualIPv6Fixture(t)
	path := filepath.Join(m.base(), gen, "ipv6-context.json")
	for _, iface := range []string{"foreign0", "wan0", "br-lan", "lo", "all"} {
		c := ipv6RuntimeConfig{Enabled: true, Mode: "dhcpv6", WAN: "wan0", PhysicalWAN: "wan0", LAN: "br-lan", WiFiLAN: iface, PD: true, RA: true}
		raw, _ := json.Marshal(c)
		if err := atomicFile(path, raw); err != nil {
			t.Fatal(err)
		}
		called := false
		if err := m.withIPv6Generation(context.Background(), gen, "wan0", func(ipv6RuntimeConfig) error { called = true; return nil }); err == nil || called {
			t.Fatal("unauthorized second LAN accepted", iface)
		}
	}
}

func TestIPv6DualLANGenerationAndServices(t *testing.T) {
	m, r, c := wifiIPv4Fixture(t)
	runner := &wifiApplyRunner{wifi: r}
	m.opts.Runner = runner
	c.WiFi.APs = wifiTestAP()
	c.IPv6.Enable = true
	c.IPv6.WANMode = "dhcpv6"
	c.IPv6.LANPD = true
	c.IPv6.LANRA = true
	ctx := context.Background()
	dir, err := m.prepare(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "ipv6-context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var runtime ipv6RuntimeConfig
	if json.Unmarshal(raw, &runtime) != nil || runtime.WiFiLAN != "wlan0" {
		t.Fatal("missing trusted WiFi runtime scope", string(raw))
	}
	dns, err := os.ReadFile(filepath.Join(dir, "dnsmasq.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dns), "dhcp-range=::,constructor:wlan0,ra-stateless,64\n") {
		t.Fatal("WiFi RA not configured", string(dns))
	}
	if err = m.activate(ctx, c, nil, dir); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(runner.base.calls, "\n")
	at := strings.Index(calls, "net/ipv6/conf/wlan0/force_forwarding=1")
	if at < 0 || at < strings.Index(calls, "nft -f ") {
		t.Fatal("WiFi forwarding missing or enabled ahead of firewall", calls)
	}
	runner.base.calls = nil
	if err = m.cleanup(ctx, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(runner.base.calls, "\n"), "net/ipv6/conf/wlan0/force_forwarding=0") {
		t.Fatal("WiFi forwarding left active after cleanup")
	}
}

func TestIPv6RoutedWiFiPublicApplyAndRestore(t *testing.T) {
	m, r, c := wifiIPv4Fixture(t)
	m.opts.Runner = &wifiApplyRunner{wifi: r}
	c.WiFi.APs = wifiTestAP()
	c.IPv6.Enable = true
	c.IPv6.WANMode = "dhcpv6"
	c.IPv6.LANPD = true
	c.IPv6.LANRA = true
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal("dual-stack public apply rejected", err)
	}
	restarted, err := New(m.opts)
	if err != nil {
		t.Fatal("dual-stack last-good cannot reload", err)
	}
	if err = restarted.Restore(context.Background(), c); err != nil {
		t.Fatal("dual-stack last-good cannot restore", err)
	}
}
