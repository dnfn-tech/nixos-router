package managed

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIPv6WANProtocolFirewall(t *testing.T) {
	for _, mode := range []string{"disabled", "dhcpv6", "slaac", "pppoe6"} {
		t.Run(mode, func(t *testing.T) {
			m, _, c := fixture(t)
			c.IPv6.Enable = mode != "disabled"
			c.IPv6.WANMode = mode
			if mode == "unfiltered" {
				c.IPv6.WANMode = "dhcpv6"
				c.Firewall.Enable = false
			}
			if mode == "pppoe6" {
				c.WAN.Mode = "pppoe"
			}
			rules := m.render(c)["firewall.nft"]
			egress := WANEgress(c)
			guard := strings.Index(rules, `tcp dport { 8080, 22 } drop`)
			drop := strings.Index(rules, `iifname "`+egress+`" drop`)
			ra := strings.Index(rules, `iifname "`+egress+`" ip6 saddr fe80::/10 ip6 hoplimit 255 icmpv6 type nd-router-advert accept`)
			if c.IPv6.Enable && (ra < 0 || guard < 0 || ra < guard || ra > drop) {
				t.Fatal("RA cannot pass while management remains guarded", rules)
			}
			if !c.IPv6.Enable && strings.Contains(rules, "nd-router-advert") {
				t.Fatal("inactive IPv6 draft allowed RA")
			}
			if c.IPv6.Enable && !strings.Contains(rules, `ip6 hoplimit 255 icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert } accept`) {
				t.Fatal("missing ND", rules)
			}
			if c.IPv6.Enable && !strings.Contains(rules, `icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem } accept`) {
				t.Fatal("missing IPv6 control errors", rules)
			}
			dhcp := strings.Index(rules, `iifname "`+egress+`" meta nfproto ipv6 udp sport 547 udp dport 546 accept`)
			if (dhcp >= 0) != (mode == "dhcpv6" || mode == "pppoe6") {
				t.Fatal("wrong DHCPv6 allowance", rules)
			}
			if dhcp >= 0 && (dhcp > drop || dhcp < guard) {
				t.Fatal("DHCPv6 rule ordering", rules)
			}
			for _, line := range strings.Split(rules, "\n") {
				if strings.Contains(line, "masquerade") && (!strings.Contains(line, "ip saddr") || strings.Contains(line, "ip6")) {
					t.Fatal("NAT not restricted to IPv4", line)
				}
			}

		})
	}
}

func TestIPv6FirewallKernel(t *testing.T) {
	nft := os.Getenv("ROUTERD_TEST_NFT_BINARY")
	if nft == "" {
		t.Skip("set ROUTERD_TEST_NFT_BINARY for isolated nft kernel validation")
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	if parent := os.Getenv("ROUTERD_TEST_NFT_PARENT_NS"); parent == "" {
		bin, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--net", bin, "-test.run=^TestIPv6FirewallKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "ROUTERD_TEST_NFT_PARENT_NS="+ns)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated nft test: %v\n%s", err, out)
		}
		return
	} else if parent == ns {
		t.Fatal("refusing host namespace nft changes")
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(nft, args...).CombinedOutput(); err != nil {
			t.Fatalf("nft %v: %v\n%s", args, err, out)
		}
	}
	run("add", "table", "inet", "foreign_sentinel")
	for _, mode := range []string{"dhcpv6", "slaac", "pppoe6", "host-service", "pppoe-services", "disabled", "unfiltered"} {
		m, _, c := fixture(t)
		c.IPv6.Enable = mode != "disabled"
		c.IPv6.WANMode = mode
		if mode == "unfiltered" {
			c.IPv6.WANMode = "dhcpv6"
			c.Firewall.Enable = false
		}
		if mode == "pppoe6" {
			c.WAN.Mode = "pppoe"
		}
		if mode == "host-service" || mode == "pppoe-services" {
			m.opts.WANUDPPorts = []int{9993}
			c.IPv6.WANMode = "slaac"
			if mode == "pppoe-services" {
				c.WAN.Mode, c.IPv6.WANMode = "pppoe", "pppoe6"
			}
		}
		file := filepath.Join(t.TempDir(), "rules.nft")
		if err := os.WriteFile(file, []byte(m.render(c)["firewall.nft"]), 0600); err != nil {
			t.Fatal(err)
		}
		run("--check", "-f", file)
		run("-f", file)
		run("list", "table", "inet", "foreign_sentinel")
		probe := exec.Command("python", filepath.Join("..", "..", "..", "tests", "ipv6-firewall-packets.py"), mode, WANEgress(c))
		if out, err := probe.CombinedOutput(); err != nil {
			t.Fatalf("%s packet verdicts: %v\n%s", mode, err, out)
		}
		if mode == "pppoe-services" {
			physical := exec.Command("python", filepath.Join("..", "..", "..", "tests", "ipv6-firewall-packets.py"), "disabled", c.WAN.Interface)
			if out, err := physical.CombinedOutput(); err != nil {
				t.Fatalf("PPP physical isolation: %v\n%s", err, out)
			}
		}

	}
}
