package managed

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strconv"
	"strings"
)

// ipv6Delegation retains zero lifetimes: expired delegations are withdrawal
// events, not absent input. Consumers must remove their previous assignment.
type ipv6Delegation struct {
	Prefix, Excluded netip.Prefix
	Preferred, Valid uint32
	Class            uint32
}

var errIPv6Delegation = errors.New("invalid IPv6 delegation event")

// parseDelegatedPrefixes accepts the documented odhcp6c three-field format
// and the newer five-field format containing T1/T2. Input is protocol data,
// never a command or a path. A malformed entry rejects the entire snapshot.
func parseDelegatedPrefixes(value string) ([]ipv6Delegation, error) {
	if len(value) > 65536 {
		return nil, errIPv6Delegation
	}
	entries := strings.Fields(value)
	if len(entries) > 128 {
		return nil, errIPv6Delegation
	}
	var result []ipv6Delegation
	seen := make(map[netip.Prefix]ipv6Delegation)
	for _, entry := range entries {
		parts := strings.Split(entry, ",")
		if len(parts) < 3 || len(parts) > 7 {
			return nil, errIPv6Delegation
		}
		p, ok := delegationNetwork(parts[0])
		if !ok {
			return nil, errIPv6Delegation
		}
		preferred, ok := delegationNumber(parts[1], 10)
		if !ok {
			return nil, errIPv6Delegation
		}
		valid, ok := delegationNumber(parts[2], 10)
		if !ok || preferred > valid {
			return nil, errIPv6Delegation
		}
		d := ipv6Delegation{Prefix: p, Preferred: preferred, Valid: valid}
		extra := parts[3:]
		if len(extra) > 0 && !strings.Contains(extra[0], "=") {
			if len(extra) < 2 {
				return nil, errIPv6Delegation
			}
			for _, timer := range extra[:2] {
				if _, ok := delegationNumber(timer, 10); !ok {
					return nil, errIPv6Delegation
				}
			}
			// odhcp6c owns renewal/rebind timers. Do not confuse these with
			// preferred/valid lifetimes or reject a withdrawal retaining T1/T2.
			extra = extra[2:]
		}
		keys := make(map[string]bool)
		for _, option := range extra {
			key, val, found := strings.Cut(option, "=")
			if !found || keys[key] {
				return nil, errIPv6Delegation
			}
			keys[key] = true
			switch key {
			case "class":
				d.Class, ok = delegationNumber(val, 16)
			case "excluded":
				d.Excluded, ok = delegationNetwork(val)
				ok = ok && d.Excluded.Bits() > p.Bits() && p.Contains(d.Excluded.Addr())
			default:
				ok = false
			}
			if !ok {
				return nil, errIPv6Delegation
			}
		}
		if previous, exists := seen[p]; exists {
			if previous != d {
				return nil, errIPv6Delegation
			}
			continue
		}
		seen[p] = d
		result = append(result, d)
	}
	return result, nil
}

func delegationNumber(value string, base int) (uint32, bool) {
	// ParseUint accepts a leading +; protocol numbers do not need that syntax.
	if value == "" || value[0] == '+' || value[0] == '-' {
		return 0, false
	}
	n, err := strconv.ParseUint(value, base, 32)
	return uint32(n), err == nil
}

func delegationNetwork(value string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(value)
	if err != nil || !p.Addr().Is6() || p.Addr().Is4In6() || !p.Addr().IsGlobalUnicast() || p != p.Masked() {
		return netip.Prefix{}, false
	}
	// Broad inputs must not extend into multicast or link-local space even
	// when their first address happens to be global unicast.
	for _, reserved := range []string{"ff00::/8", "fe80::/10"} {
		if p.Overlaps(netip.MustParsePrefix(reserved)) {
			return netip.Prefix{}, false
		}
	}
	return p, true
}

// lanPrefix chooses the first wholly available /64 without iterating through
// a potentially enormous delegation. A sub-/64 exclusion reserves its whole
// enclosing /64, because advertising that /64 would include excluded space.
func (d ipv6Delegation) lanPrefix() (netip.Prefix, error) {
	if !d.Prefix.IsValid() || !d.Prefix.Addr().Is6() || d.Prefix.Bits() > 64 || d.Valid == 0 {
		return netip.Prefix{}, errors.New("IPv6 delegation has no usable LAN /64")
	}
	candidate := netip.PrefixFrom(d.Prefix.Addr(), 64).Masked()
	if d.Excluded.IsValid() && candidate.Overlaps(d.Excluded) {
		address := d.Excluded.Masked().Addr().As16()
		upper := binary.BigEndian.Uint64(address[:8])
		if d.Excluded.Bits() < 64 {
			upper |= (uint64(1) << (64 - d.Excluded.Bits())) - 1
		}
		if upper == ^uint64(0) {
			return netip.Prefix{}, errors.New("IPv6 delegation has no usable LAN /64")
		}
		binary.BigEndian.PutUint64(address[:8], upper+1)
		clear(address[8:])
		candidate = netip.PrefixFrom(netip.AddrFrom16(address), 64)
	}
	if !d.Prefix.Contains(candidate.Addr()) {
		return netip.Prefix{}, errors.New("IPv6 delegation has no usable LAN /64")
	}
	return candidate, nil
}
