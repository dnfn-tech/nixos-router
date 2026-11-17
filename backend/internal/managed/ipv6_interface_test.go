package managed

import (
	"context"
	"strings"
	"testing"
)

func TestIPv6AutoconfigurationOwnership(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		m, r, c := fixture(t)
		c.IPv6.Enable = enabled
		c.IPv6.WANMode = "dhcpv6"
		dir, err := m.prepare(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		r.calls = nil
		if err = m.activate(context.Background(), c, nil, dir); err != nil {
			t.Fatal(err)
		}
		calls := strings.Join(r.calls, "\n")
		for _, iface := range []string{c.WAN.Interface, c.LAN.BridgeName, c.LAN.Ports[0]} {
			key := "net/ipv6/conf/" + iface + "/accept_ra=0"
			at := strings.Index(calls, key)
			if (at >= 0) != enabled {
				t.Fatal("unexpected kernel RA ownership", calls)
			}
			if enabled && at > strings.Index(calls, "ip link set dev "+iface+" up") {
				t.Fatal("autoconfiguration disabled after link up", calls)
			}
		}
		if strings.Contains(calls, "net/ipv6/conf/all/") || strings.Contains(calls, "net/ipv6/conf/default/") {
			t.Fatal("global sysctl mutation", calls)
		}
	}
}

func TestIPv6AutoconfigurationFailureStopsActivation(t *testing.T) {
	m, r, c := fixture(t)
	c.IPv6.Enable, c.IPv6.WANMode = true, "dhcpv6"
	dir, err := m.prepare(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	r.fail = "/accept_ra=0"
	if err = m.activate(context.Background(), c, nil, dir); err == nil {
		t.Fatal("ignored kernel configuration failure")
	}
	if strings.Contains(strings.Join(r.calls, "\n"), "restart nixos-router-wan6.service") {
		t.Fatal("client started after failed ownership setup")
	}
}
