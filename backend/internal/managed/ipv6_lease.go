package managed

import (
	"errors"
	"net/netip"
	"time"
)

// ipv6Lease is the observed protocol snapshot, not proof of installed network
// state. A resource journal must separately record successful/pending commands.
type ipv6Lease struct {
	ObservedAt time.Time
	DHCPActive bool
	Event      ipv6Event
}

func nextIPv6Lease(previous ipv6Lease, event ipv6Event, observedAt time.Time) (ipv6Lease, error) {
	if observedAt.IsZero() {
		return ipv6Lease{}, errors.New("IPv6 observation time required")
	}
	if !previous.ObservedAt.IsZero() && observedAt.Before(previous.ObservedAt) {
		return ipv6Lease{}, errors.New("outdated IPv6 observation")
	}
	s := ipv6Lease{ObservedAt: observedAt, DHCPActive: previous.DHCPActive, Event: cloneIPv6Event(event)}
	switch event.State {
	case "started", "stopped":
		s.DHCPActive = false
		s.Event = ipv6Event{State: event.State}
	case "unbound":
		s.DHCPActive = false
	case "bound", "rebound", "updated", "informed":
		s.DHCPActive = true
	case "ra-updated":
		// odhcp6c can coalesce script notifications. Full lease entries prove
		// a binding; RDNSS alone does not, because it can outlive unbound.
		if len(event.DHCPAddresses) != 0 || len(event.Prefixes) != 0 {
			s.DHCPActive = true
		}
	default:
		return ipv6Lease{}, errors.New("invalid IPv6 state transition")
	}
	if !s.DHCPActive {
		s.Event.DHCPAddresses, s.Event.Prefixes, s.Event.DHCPDNS = nil, nil, nil
	}
	return s, nil
}

// at produces a conservative replay view. The timestamp is advanced with the
// aged lifetimes, so repeated replay cannot restart a lease's original timer.
// Bare DNS addresses carry no TTL in odhcp6c's script interface: a later replay
// must await a fresh callback for DNS rather than inventing a lifetime.
func (s ipv6Lease) at(now time.Time) (ipv6Lease, error) {
	if s.ObservedAt.IsZero() || now.Before(s.ObservedAt) {
		return ipv6Lease{}, errors.New("IPv6 lease observation time is unavailable or in the future")
	}
	elapsed := now.Sub(s.ObservedAt)
	seconds := uint64(elapsed / time.Second)
	if elapsed%time.Second != 0 {
		seconds++
	}
	age := func(n uint32) uint32 {
		if n == ^uint32(0) {
			return n
		}
		if uint64(n) <= seconds {
			return 0
		}
		return n - uint32(seconds)
	}
	s.Event = cloneIPv6Event(s.Event)
	for _, addresses := range [][]ipv6Address{s.Event.DHCPAddresses, s.Event.RAAddresses} {
		for i := range addresses {
			addresses[i].Preferred = age(addresses[i].Preferred)
			addresses[i].Valid = age(addresses[i].Valid)
		}
	}
	for i := range s.Event.Prefixes {
		s.Event.Prefixes[i].Preferred = age(s.Event.Prefixes[i].Preferred)
		s.Event.Prefixes[i].Valid = age(s.Event.Prefixes[i].Valid)
	}
	for i := range s.Event.Routes {
		s.Event.Routes[i].Valid = age(s.Event.Routes[i].Valid)
	}
	if seconds > 0 {
		s.Event.DHCPDNS, s.Event.RADNS = nil, nil
	}
	s.ObservedAt = now
	return s, nil
}

func cloneIPv6Event(e ipv6Event) ipv6Event {
	e.DHCPAddresses = append([]ipv6Address(nil), e.DHCPAddresses...)
	e.RAAddresses = append([]ipv6Address(nil), e.RAAddresses...)
	e.Prefixes = append([]ipv6Delegation(nil), e.Prefixes...)
	e.Routes = append([]ipv6Route(nil), e.Routes...)
	e.DHCPDNS = append([]netip.Addr(nil), e.DHCPDNS...)
	e.RADNS = append([]netip.Addr(nil), e.RADNS...)
	return e
}
