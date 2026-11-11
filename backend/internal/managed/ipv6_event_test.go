package managed

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestIPv6EventRetainsProtocolSourcesAndWithdrawal(t *testing.T) {
	e, err := parseIPv6Event("ra-updated", map[string]string{
		"ADDRESSES":    "2001:db8::123/128,300,600,100,200",
		"RA_ADDRESSES": "2001:db8::123/64,0,900",
		"PREFIXES":     "2001:db8:100::/56,0,0,100,200",
		"RA_ROUTES":    "::/0,fe80::1,0,512 2001:db8::/64,,600,256",
		"RDNSS":        "2001:db8::53 fe80::53",
		"RA_DNS":       "2001:db8::53",
		"RA_MTU":       "1492",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.State != "ra-updated" || e.MTU != 1492 || len(e.DHCPAddresses) != 1 || len(e.RAAddresses) != 1 {
		t.Fatal(e)
	}
	if e.DHCPAddresses[0].Prefix.String() != "2001:db8::123/128" || e.RAAddresses[0].Prefix.String() != "2001:db8::123/64" || e.RAAddresses[0].Preferred != 0 {
		t.Fatal("host bits or deprecation lost", e)
	}
	if len(e.Prefixes) != 1 || e.Prefixes[0].Valid != 0 || len(e.Routes) != 2 || e.Routes[0].Valid != 0 {
		t.Fatal("withdrawal lost", e)
	}
	if e.Routes[0].Gateway != netip.MustParseAddr("fe80::1") || e.Routes[1].Gateway.IsValid() {
		t.Fatal(e.Routes)
	}
	if len(e.DHCPDNS) != 2 || len(e.RADNS) != 1 {
		t.Fatal("sources incorrectly merged", e)
	}
}

func TestIPv6EventStatesAndBounds(t *testing.T) {
	for _, state := range []string{"started", "bound", "informed", "updated", "ra-updated", "rebound", "unbound", "stopped"} {
		if _, err := parseIPv6Event(state, nil); err != nil {
			t.Fatal(state, err)
		}
	}
	for _, tc := range []struct{ state, key, value string }{
		{"unknown-secret", "", ""},
		{"bound", "ADDRESSES", "2001:db8::1/64,1,2;secret"},
		{"bound", "ADDRESSES", "2001:db8::1/0,1,2"},
		{"bound", "RA_ADDRESSES", "8000::1/1,1,2"},
		{"bound", "ADDRESSES", "fe80::1/64,1,2"},
		{"bound", "ADDRESSES", "::ffff:192.0.2.1/128,1,2"},
		{"bound", "ADDRESSES", "::1/128,1,2"},
		{"bound", "ADDRESSES", "2001:db8::1/64,3,2"},
		{"bound", "RA_ADDRESSES", "2001:db8::1/64,1,2,3"},
		{"bound", "RA_ADDRESSES", "2001:db8::1/64,1,2,3,no"},
		{"bound", "RA_ROUTES", "::/0,fe80::1%secret,30,1"},
		{"bound", "RA_ROUTES", "::/0,2001:db8::1,30,1"},
		{"bound", "RA_ROUTES", "ff00::/8,fe80::1,30,1"},
		{"bound", "RA_ROUTES", "2001:db8::1/64,,30,1"},
		{"bound", "RA_ROUTES", "::/0,fe80::1,-1,1"},
		{"bound", "RA_ROUTES", "::/0,fe80::1,30,4294967296"},
		{"bound", "RDNSS", "127.0.0.1"},
		{"bound", "RDNSS", "::1"},
		{"bound", "RA_DNS", "fe80::1%secret"},
		{"bound", "RA_DNS", "ff02::1"},
		{"bound", "RA_DNS", "::"},
		{"bound", "RA_MTU", "1279"},
		{"bound", "RA_MTU", "65536"},
		{"bound", "RA_MTU", "4294967295"},
		{"bound", "RA_MTU", "secret"},
		{"bound", "RA_MTU", "4294967296"},
		{"bound", "PREFIXES", "2001:db8::/56,1,secret"},
		{"bound", "ADDRESSES", strings.Repeat("2001:db8::1/128,1,2 ", 129)},
		{"bound", "RA_DNS", strings.Repeat("2001:db8::1 ", 129)},
		{"bound", "RA_ROUTES", strings.Repeat("::/0,fe80::1,30,1 ", 129)},
		{"bound", "RDNSS", strings.Repeat(" ", 65537)},
	} {
		e, err := parseIPv6Event(tc.state, map[string]string{tc.key: tc.value})
		if err == nil || !reflect.DeepEqual(e, ipv6Event{}) {
			t.Fatalf("accepted invalid %s: %+v %v", tc.key, e, err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("echoed network input")
		}
	}
	// The size limit covers the whole supported event, not just each variable.
	if _, err := parseIPv6Event("bound", map[string]string{"RDNSS": strings.Repeat(" ", 40000), "RA_DNS": strings.Repeat(" ", 40000)}); err == nil {
		t.Fatal("unbounded event")
	}
}

func TestIPv6EventDuplicateIdentity(t *testing.T) {
	base := map[string]string{"ADDRESSES": "2001:db8::1/128,1,2 2001:0db8::1/128,1,2", "RA_ROUTES": "::/0,fe80::1,30,1 ::/0,fe80::1,30,1 ::/0,fe80::2,30,1", "RDNSS": "2001:db8::53 2001:0db8::53"}
	e, err := parseIPv6Event("bound", base)
	if err != nil || len(e.DHCPAddresses) != 1 || len(e.Routes) != 2 || len(e.DHCPDNS) != 1 {
		t.Fatal(e, err)
	}
	for key, value := range map[string]string{
		"ADDRESSES": "2001:db8::1/128,1,2 2001:db8::1/128,0,0",
		"RA_ROUTES": "::/0,fe80::1,30,1 ::/0,fe80::1,0,1",
	} {
		if _, err := parseIPv6Event("bound", map[string]string{key: value}); err == nil {
			t.Fatal("conflicting duplicate", key)
		}
	}
}

func TestIPv6EventMTUBoundaries(t *testing.T) {
	for raw, want := range map[string]uint32{"": 0, "0": 0, "1280": 1280, "65535": 65535} {
		event, err := parseIPv6Event("ra-updated", map[string]string{"RA_MTU": raw})
		if err != nil || event.MTU != want {
			t.Fatalf("MTU %q: %+v %v", raw, event, err)
		}
	}
}
