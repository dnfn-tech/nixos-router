package managed

import (
	"strings"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestPPPoEOptionsAndEgress(t *testing.T) {
	c := config.DefaultConfig()
	c.WAN.Interface = "wan0"
	for _, mode := range []string{"dhcp", "static"} {
		c.WAN.Mode = mode
		if got := wanEgress(c); got != "wan0" {
			t.Fatal(got)
		}
		if got, err := renderPPPoE(c); err != nil || got != "" {
			t.Fatalf("inactive PPP options: %q %v", got, err)
		}
	}
	c.WAN.Mode = "pppoe"
	c.WAN.PPPoE = &config.PPPoEConfig{Username: `name with # and "quote"`, Password: `secret\with "quotes" # spaces`}
	got, err := renderPPPoE(c)
	if err != nil {
		t.Fatal(err)
	}
	if wanEgress(c) != "nr-ppp0" {
		t.Fatal("physical WAN used for PPP traffic")
	}
	for _, line := range []string{`plugin pppoe.so`, `nic-wan0`, `ifname nr-ppp0`, `user "name with # and \"quote\""`, `password "secret\\with \"quotes\" # spaces"`, `mtu 1492`, `mru 1492`, `nodetach`, `persist`, `maxfail 0`, `holdoff 5`, `lcp-echo-interval 10`, `lcp-echo-failure 3`, `hide-password`} {
		if !strings.Contains("\n"+got, "\n"+line+"\n") {
			t.Fatalf("missing %q", line)
		}
	}
	if strings.Contains(got, "debug\n") || strings.Contains(got, "dump\n") {
		t.Fatal("unsafe credential logging")
	}
}

func TestPPPoERejectsUnsafeOptions(t *testing.T) {
	for _, value := range []string{"", "x\nplugin evil.so", "x\rsecret", "x\x00", "x\tvalue", strings.Repeat("a", 256)} {
		for _, field := range []string{"username", "password"} {
			c := config.DefaultConfig()
			c.WAN.Mode = "pppoe"
			c.WAN.Interface = "wan0"
			c.WAN.PPPoE = &config.PPPoEConfig{Username: "user", Password: "pass"}
			if field == "username" {
				c.WAN.PPPoE.Username = value
			} else {
				c.WAN.PPPoE.Password = value
			}
			got, err := renderPPPoE(c)
			if err == nil || got != "" {
				t.Fatalf("accepted unsafe %s", field)
			}
			if value != "" && strings.Contains(err.Error(), value) {
				t.Fatal("error exposed input")
			}
		}
	}
	c := config.DefaultConfig()
	c.WAN.Mode = "pppoe"
	if _, err := renderPPPoE(c); err == nil {
		t.Fatal("missing credentials accepted")
	}
	c.WAN.PPPoE = &config.PPPoEConfig{Username: "u", Password: "p"}
	for _, iface := range []string{"wan0\nfile /tmp/evil", "nr-ppp0", "lo", ""} {
		c.WAN.Interface = iface
		if _, err := renderPPPoE(c); err == nil {
			t.Fatal("invalid physical WAN accepted")
		}
	}
}
