package managed

import (
	"reflect"
	"testing"
	"time"
)

func leaseEvent(t *testing.T, state string) ipv6Event {
	t.Helper()
	e, err := parseIPv6Event(state, map[string]string{
		"ADDRESSES":    "2001:db8::2/128,30,60",
		"PREFIXES":     "2001:db8:100::/56,30,60",
		"RA_ADDRESSES": "2001:db8::3/64,40,80",
		"RA_ROUTES":    "::/0,fe80::1,80,512",
		"RDNSS":        "2001:db8::53", "RA_DNS": "fe80::53", "RA_MTU": "1500",
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestIPv6LeaseUnboundAndStopped(t *testing.T) {
	now := time.Unix(1000, 0)
	bound, err := nextIPv6Lease(ipv6Lease{}, leaseEvent(t, "bound"), now)
	if err != nil || !bound.DHCPActive {
		t.Fatal(bound, err)
	}
	unbound, err := nextIPv6Lease(bound, leaseEvent(t, "unbound"), now.Add(time.Second))
	if err != nil || unbound.DHCPActive || len(unbound.Event.DHCPDNS) != 0 || len(unbound.Event.Prefixes) != 0 || len(unbound.Event.DHCPAddresses) != 0 {
		t.Fatal("retained DHCP data after loss", unbound, err)
	}
	if len(unbound.Event.RADNS) != 1 || len(unbound.Event.RAAddresses) != 1 || len(unbound.Event.Routes) != 1 {
		t.Fatal("lost RA on DHCP loss", unbound)
	}
	ra := leaseEvent(t, "ra-updated")
	ra.DHCPAddresses, ra.Prefixes = nil, nil
	// odhcp6c may still export old RDNSS after becoming unbound.
	after, err := nextIPv6Lease(unbound, ra, now.Add(2*time.Second))
	if err != nil || after.DHCPActive || len(after.Event.DHCPDNS) != 0 {
		t.Fatal("resurrected stale DHCP DNS", after, err)
	}
	for _, state := range []string{"stopped", "started"} {
		cleared, err := nextIPv6Lease(bound, leaseEvent(t, state), now)
		if err != nil || cleared.DHCPActive || !reflect.DeepEqual(cleared.Event, ipv6Event{State: state}) {
			t.Fatal(state, cleared, err)
		}
	}
}

func TestIPv6LeaseStatelessAndCoalescedEvents(t *testing.T) {
	now := time.Unix(1000, 0)
	informed := leaseEvent(t, "informed")
	informed.DHCPAddresses, informed.Prefixes = nil, nil
	s, err := nextIPv6Lease(ipv6Lease{}, informed, now)
	if err != nil || !s.DHCPActive || len(s.Event.DHCPDNS) != 1 {
		t.Fatal(s, err)
	}
	informed.State = "ra-updated"
	s, err = nextIPv6Lease(s, informed, now)
	if err != nil || len(s.Event.DHCPDNS) != 1 {
		t.Fatal("lost stateless DNS", s, err)
	}
	// A coalesced RA callback can contain the full DHCP lease even if the
	// preceding bound notification did not get delivered to our callback.
	s, err = nextIPv6Lease(ipv6Lease{}, leaseEvent(t, "ra-updated"), now)
	if err != nil || !s.DHCPActive || len(s.Event.Prefixes) != 1 {
		t.Fatal(s, err)
	}
}

func TestIPv6LeaseAgingDoesNotExtendOrMutate(t *testing.T) {
	now := time.Unix(1000, 0)
	e := leaseEvent(t, "bound")
	s, err := nextIPv6Lease(ipv6Lease{}, e, now)
	if err != nil {
		t.Fatal(err)
	}
	// Input and result must not share backing slices across callbacks.
	e.DHCPAddresses[0].Valid = 999
	aged, err := s.at(now.Add(30*time.Second + time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if aged.Event.DHCPAddresses[0].Preferred != 0 || aged.Event.DHCPAddresses[0].Valid != 29 || aged.Event.Prefixes[0].Valid != 29 || aged.Event.Routes[0].Valid != 49 {
		t.Fatal(aged)
	}
	if s.Event.DHCPAddresses[0].Valid != 60 || s.Event.DHCPAddresses[0].Preferred != 30 {
		t.Fatal("aging mutated original", s)
	}
	expired, err := s.at(now.Add(80 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if expired.Event.DHCPAddresses[0].Valid != 0 || expired.Event.Prefixes[0].Valid != 0 || expired.Event.Routes[0].Valid != 0 {
		t.Fatal("expired records revived", expired)
	}
	if len(expired.Event.DHCPDNS) != 0 || len(expired.Event.RADNS) != 0 {
		t.Fatal("unproven DNS lifetime survived replay", expired)
	}
	if _, err := s.at(now.Add(-time.Second)); err == nil {
		t.Fatal("backward clock extended lease")
	}
	if _, err := nextIPv6Lease(s, leaseEvent(t, "bound"), now.Add(-time.Second)); err == nil {
		t.Fatal("older callback overwrote newer observation")
	}
}

func TestIPv6LeaseInfiniteAndInvalidState(t *testing.T) {
	now := time.Unix(1000, 0)
	e := leaseEvent(t, "bound")
	e.DHCPAddresses[0].Preferred, e.DHCPAddresses[0].Valid = ^uint32(0), ^uint32(0)
	s, err := nextIPv6Lease(ipv6Lease{}, e, now)
	if err != nil {
		t.Fatal(err)
	}
	aged, err := s.at(now.Add(100 * 365 * 24 * time.Hour))
	if err != nil || aged.Event.DHCPAddresses[0].Valid != ^uint32(0) || aged.Event.DHCPAddresses[0].Preferred != ^uint32(0) {
		t.Fatal(aged, err)
	}
	e.State = "unexpected"
	if _, err := nextIPv6Lease(s, e, now); err == nil {
		t.Fatal("unknown transition accepted")
	}
}
