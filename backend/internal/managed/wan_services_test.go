package managed

import (
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"strings"
	"testing"
)

func TestWANUDPServicesValidateAndCopyTrustedPolicy(t *testing.T) {
	m, _, _ := fixture(t)
	opts := m.opts
	ports := []int{9993, 51820, 9993}
	opts.WANUDPPorts = ports
	trusted, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ports[0] = 9
	if len(trusted.opts.WANUDPPorts) != 2 || trusted.opts.WANUDPPorts[0] != 9993 {
		t.Fatal("trusted policy aliased or not deduplicated", trusted.opts.WANUDPPorts)
	}
	excessive := make([]int, 65)
	for i := range excessive {
		excessive[i] = 9993
	}
	for _, bad := range [][]int{{0}, {-1}, {65536}, excessive} {
		opts.WANUDPPorts = bad
		if _, err := New(opts); err == nil {
			t.Fatal("invalid service ports accepted", bad)
		}
	}
}

func TestWANUDPServicesFollowActualEgress(t *testing.T) {
	for _, mode := range []string{"dhcp", "static", "pppoe"} {
		m, _, c := fixture(t)
		c.WAN.Mode = mode
		c.WAN.Static = &config.StaticIP{AddressCIDR: "192.0.2.2/24"}
		m.opts.WANUDPPorts = []int{9993}
		rules := m.render(c)["firewall.nft"]
		want := `iifname "` + WANEgress(c) + `" udp dport { 9993 } accept`
		if !strings.Contains(rules, want) {
			t.Fatal("missing explicit host service policy", rules)
		}
		if mode == "pppoe" && strings.Contains(rules, `iifname "wan0" udp dport { 9993 } accept`) {
			t.Fatal("physical PPP port exposed")
		}
		m.opts.WANUDPPorts = nil
		if strings.Contains(m.render(c)["firewall.nft"], "9993") {
			t.Fatal("removed host service remains open")
		}
	}
}

func TestWANTCPServicesUseOnlyEgressAndProtectManagement(t *testing.T) {
	m, _, c := fixture(t)
	c.WAN.Mode = "pppoe"
	opts := m.opts
	opts.WANTCPPorts = []int{24443, 24443}
	m, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	rules := m.render(c)["firewall.nft"]
	allow := strings.Index(rules, `iifname "nr-ppp0" tcp dport { 24443 } accept`)
	drop := strings.Index(rules, `iifname "nr-ppp0" drop`)
	if allow < 0 || drop < allow || strings.Contains(rules, `iifname "wan0" tcp dport { 24443 }`) ||
		strings.Contains(rules, "udp dport { 24443 }") {
		t.Fatal("unexpected managed WAN listener policy")
	}
	for _, port := range []int{0, 65536, opts.ManagementPort, 22} {
		opts.WANTCPPorts = []int{port}
		if _, err := New(opts); err == nil {
			t.Fatalf("invalid or protected port %d allowed", port)
		}
	}
}
