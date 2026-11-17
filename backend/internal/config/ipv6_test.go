package config

import (
	"errors"
	"testing"
)

func TestIPv6ConfigurationCombinations(t *testing.T) {
	for _, tc := range []struct {
		name, wan, mode string
		pd, ra          bool
		path            string
	}{
		{"dhcp6 WAN only", "dhcp", "dhcpv6", false, false, ""},
		{"routed LAN", "dhcp", "dhcpv6", true, true, ""},
		{"manual LAN clients", "dhcp", "dhcpv6", true, false, ""},
		{"slaac WAN only", "dhcp", "slaac", false, false, ""},
		{"PPP routed LAN", "pppoe", "pppoe6", true, true, ""},
		{"disabled WAN with IPv6 on", "dhcp", "disabled", false, false, "ipv6.wanMode"},
		{"missing WAN mode", "dhcp", "", false, false, "ipv6.wanMode"},
		{"PPP mode without PPP", "dhcp", "pppoe6", false, false, "ipv6.wanMode"},
		{"DHCP6 on PPP carrier", "pppoe", "dhcpv6", false, false, "ipv6.wanMode"},
		{"SLAAC on PPP carrier", "pppoe", "slaac", false, false, "ipv6.wanMode"},
		{"SLAAC cannot delegate", "dhcp", "slaac", true, false, "ipv6.lanPrefixDelegation"},
		{"RA requires routed prefix", "dhcp", "dhcpv6", false, true, "ipv6.lanRouterAdvertisement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.WAN.Mode = tc.wan
			c.WAN.PPPoE = &PPPoEConfig{Username: "test", Password: "test-password"}
			c.IPv6 = IPv6Config{Enable: true, WANMode: tc.mode, LANPD: tc.pd, LANRA: tc.ra}
			err := c.Validate()
			if tc.path == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var issue *ValidationError
			if !errors.As(err, &issue) || issue.Path != tc.path {
				t.Fatalf("want %s, got %v", tc.path, err)
			}
			// Disabling IPv6 preserves the draft instead of forcing a rewrite
			// of inactive dependent settings or blocking an IPv4 WAN change.
			c.IPv6.Enable = false
			before := c.IPv6
			if err := c.Validate(); err != nil || c.IPv6 != before {
				t.Fatal("disabled draft lost or rejected", err)
			}
		})
	}
}
