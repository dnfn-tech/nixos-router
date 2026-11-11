package managed

import (
	"context"
	"strings"
	"testing"
)

func TestIPv6ForwardingActivationAndCleanup(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		m, r, c := fixture(t)
		c.IPv6.Enable = enabled
		c.IPv6.WANMode = "dhcpv6"
		c.IPv6.LANPD = true
		dir, err := m.prepare(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		r.calls = nil
		if err = m.activate(context.Background(), c, nil, dir); err != nil {
			t.Fatal(err)
		}
		calls := strings.Join(r.calls, "\n")
		value := "0"
		if enabled {
			value = "1"
		}
		for _, iface := range []string{c.LAN.BridgeName, c.WAN.Interface} {
			at := strings.Index(calls, "net/ipv6/conf/"+iface+"/force_forwarding="+value)
			if at < 0 {
				t.Fatal("missing per-link forwarding policy", calls)
			}
			if enabled && at < strings.Index(calls, "nft -f ") {
				t.Fatal("forwarding enabled before firewall")
			}
		}
		r.calls = nil
		if err = m.cleanup(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		calls = strings.Join(r.calls, "\n")
		for _, iface := range []string{c.WAN.Interface} {
			if !strings.Contains(calls, "net/ipv6/conf/"+iface+"/force_forwarding=0") {
				t.Fatal("cleanup left forwarding enabled", calls)
			}
		}
	}
}
