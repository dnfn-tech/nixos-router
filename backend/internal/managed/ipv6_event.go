package managed

import (
	"errors"
	"net/netip"
	"strings"
)

type ipv6Address struct {
	Prefix           netip.Prefix // Host bits are meaningful here, unlike a delegation.
	Preferred, Valid uint32
}

type ipv6Route struct {
	Prefix        netip.Prefix
	Gateway       netip.Addr // Invalid means an on-link route, not an unspecified IP.
	Valid, Metric uint32
}

// Keep DHCP and RA provenance separate: losing DHCP must not implicitly erase
// still-valid RA information. All lifetimes are remaining seconds, including
// zero for withdrawals and uint32 max for infinity.
type ipv6Event struct {
	State                      string
	DHCPAddresses, RAAddresses []ipv6Address
	Prefixes                   []ipv6Delegation
	Routes                     []ipv6Route
	DHCPDNS, RADNS             []netip.Addr
	MTU                        uint32 // Zero means no advertised MTU.
}

var errIPv6Event = errors.New("invalid IPv6 protocol event")

func parseIPv6Event(state string, env map[string]string) (ipv6Event, error) {
	fail := func() (ipv6Event, error) { return ipv6Event{}, errIPv6Event }
	switch state {
	case "started", "bound", "informed", "updated", "ra-updated", "rebound", "unbound", "stopped":
	default:
		return fail()
	}
	remaining := 65536
	for _, key := range []string{"ADDRESSES", "RA_ADDRESSES", "PREFIXES", "RA_ROUTES", "RDNSS", "RA_DNS", "RA_MTU"} {
		if len(env[key]) > remaining {
			return fail()
		}
		remaining -= len(env[key])
	}
	e := ipv6Event{State: state}
	var err error
	if e.DHCPAddresses, err = parseIPv6Addresses(env["ADDRESSES"]); err != nil {
		return fail()
	}
	if e.RAAddresses, err = parseIPv6Addresses(env["RA_ADDRESSES"]); err != nil {
		return fail()
	}
	if e.Prefixes, err = parseDelegatedPrefixes(env["PREFIXES"]); err != nil {
		return fail()
	}
	if e.Routes, err = parseIPv6Routes(env["RA_ROUTES"]); err != nil {
		return fail()
	}
	if e.DHCPDNS, err = parseIPv6DNS(env["RDNSS"]); err != nil {
		return fail()
	}
	if e.RADNS, err = parseIPv6DNS(env["RA_DNS"]); err != nil {
		return fail()
	}
	if value := env["RA_MTU"]; value != "" {
		var ok bool
		e.MTU, ok = delegationNumber(value, 10)
		if !ok || (e.MTU != 0 && (e.MTU < 1280 || e.MTU > 65535)) {
			return fail()
		}
	}
	return e, nil
}

func parseIPv6Addresses(value string) ([]ipv6Address, error) {
	entries := strings.Fields(value)
	if len(entries) > 128 {
		return nil, errIPv6Event
	}
	var result []ipv6Address
	seen := make(map[netip.Addr]ipv6Address)
	for _, entry := range entries {
		parts := strings.Split(entry, ",")
		if len(parts) != 3 && len(parts) != 5 {
			return nil, errIPv6Event
		}
		p, err := netip.ParsePrefix(parts[0])
		if err != nil || !protocolIPv6Address(p.Addr(), false) {
			return nil, errIPv6Event
		}
		// Assigning an address also installs a connected subnet route. The
		// host may be global while an overbroad mask covers reserved space.
		if _, ok := delegationNetwork(p.Masked().String()); !ok {
			return nil, errIPv6Event
		}
		preferred, ok := delegationNumber(parts[1], 10)
		if !ok {
			return nil, errIPv6Event
		}
		valid, ok := delegationNumber(parts[2], 10)
		if !ok || preferred > valid {
			return nil, errIPv6Event
		}
		for _, timer := range parts[3:] {
			if _, ok := delegationNumber(timer, 10); !ok {
				return nil, errIPv6Event
			}
		}
		a := ipv6Address{Prefix: p, Preferred: preferred, Valid: valid}
		if prior, exists := seen[p.Addr()]; exists {
			if prior != a {
				return nil, errIPv6Event
			}
			continue
		}
		seen[p.Addr()] = a
		result = append(result, a)
	}
	return result, nil
}

func parseIPv6Routes(value string) ([]ipv6Route, error) {
	entries := strings.Fields(value)
	if len(entries) > 128 {
		return nil, errIPv6Event
	}
	type identity struct {
		prefix  netip.Prefix
		gateway netip.Addr
		metric  uint32
	}
	seen := make(map[identity]ipv6Route)
	var result []ipv6Route
	for _, entry := range entries {
		parts := strings.Split(entry, ",")
		if len(parts) != 4 {
			return nil, errIPv6Event
		}
		p, err := netip.ParsePrefix(parts[0])
		if err != nil {
			return nil, errIPv6Event
		}
		if p != netip.MustParsePrefix("::/0") {
			if _, ok := delegationNetwork(parts[0]); !ok {
				return nil, errIPv6Event
			}
		}
		var gateway netip.Addr
		if parts[1] != "" {
			gateway, err = netip.ParseAddr(parts[1])
			if err != nil || !gateway.Is6() || !gateway.IsLinkLocalUnicast() || gateway.Zone() != "" {
				return nil, errIPv6Event
			}
		} else if p.Bits() == 0 {
			return nil, errIPv6Event
		}
		valid, ok := delegationNumber(parts[2], 10)
		if !ok {
			return nil, errIPv6Event
		}
		metric, ok := delegationNumber(parts[3], 10)
		if !ok {
			return nil, errIPv6Event
		}
		r := ipv6Route{Prefix: p, Gateway: gateway, Valid: valid, Metric: metric}
		key := identity{p, gateway, metric}
		if prior, exists := seen[key]; exists {
			if prior != r {
				return nil, errIPv6Event
			}
			continue
		}
		seen[key] = r
		result = append(result, r)
	}
	return result, nil
}

func parseIPv6DNS(value string) ([]netip.Addr, error) {
	entries := strings.Fields(value)
	if len(entries) > 128 {
		return nil, errIPv6Event
	}
	var result []netip.Addr
	seen := make(map[netip.Addr]bool)
	for _, entry := range entries {
		a, err := netip.ParseAddr(entry)
		if err != nil || !protocolIPv6Address(a, true) {
			return nil, errIPv6Event
		}
		if !seen[a] {
			result = append(result, a)
			seen[a] = true
		}
	}
	return result, nil
}

func protocolIPv6Address(a netip.Addr, allowLinkLocal bool) bool {
	return a.Is6() && !a.Is4In6() && a.Zone() == "" &&
		(a.IsGlobalUnicast() || (allowLinkLocal && a.IsLinkLocalUnicast()))
}
