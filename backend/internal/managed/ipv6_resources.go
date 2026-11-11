package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const ipv6ResourceProtocol = "242"

type ipv6Resource struct {
	Kind, Interface          string
	Prefix                   netip.Prefix
	Gateway                  netip.Addr
	Metric, Preferred, Valid uint32
}

// key is the kernel replacement slot, not the complete identity. A different
// gateway or address mask in the same slot must not bypass collision checks.
func (r ipv6Resource) key() string {
	if r.Kind == "address" {
		return "address|" + r.Interface + "|" + r.Prefix.Addr().String()
	}
	return "route|" + r.Prefix.String() + "|" + strconv.FormatUint(uint64(r.Metric), 10)
}
func (r ipv6Resource) identity() ipv6Resource { r.Preferred, r.Valid = 0, 0; return r }

type ipv6ResourceJournal struct {
	Version    int
	Generation string
	Context    ipv6RuntimeConfig
	Lease      ipv6Lease
	Pending    bool
	Resources  []ipv6Resource
	MTU        *ipv6MTUResource `json:",omitempty"`
}

func ipv6DesiredResources(c ipv6RuntimeConfig, e ipv6Event) ([]ipv6Resource, error) {
	byKey := map[string]ipv6Resource{}
	put := func(r ipv6Resource) error {
		if r.Valid == 0 {
			return nil
		}
		if old, ok := byKey[r.key()]; ok {
			if old.identity() != r.identity() {
				if r.Kind != "route" || old.Interface != r.Interface {
					return errors.New("conflicting IPv6 resource assignment")
				}
				// Equal-priority routers: choose a deterministic gateway.
				if !r.Gateway.Less(old.Gateway) {
					return nil
				}
			} else {
				r.Preferred = max(r.Preferred, old.Preferred)
				r.Valid = max(r.Valid, old.Valid)
			}
		}
		byKey[r.key()] = r
		return nil
	}
	for _, list := range [][]ipv6Address{e.DHCPAddresses, e.RAAddresses} {
		for _, a := range list {
			// On-link reachability comes from explicit RA routes. Avoid implicit
			// connected routes whose ownership and lifetime cannot be journaled.
			if err := put(ipv6Resource{Kind: "address", Interface: c.WAN, Prefix: netip.PrefixFrom(a.Prefix.Addr(), 128), Preferred: a.Preferred, Valid: a.Valid}); err != nil {
				return nil, err
			}
		}
	}
	for _, r := range e.Routes {
		metric := r.Metric
		if metric == 0 {
			metric = 1024
		}
		if err := put(ipv6Resource{Kind: "route", Interface: c.WAN, Prefix: r.Prefix, Gateway: r.Gateway, Metric: metric, Valid: r.Valid}); err != nil {
			return nil, err
		}
	}
	if c.PD {
		lans := []string{c.LAN}
		if c.WiFiLAN != "" {
			lans = append(lans, c.WiFiLAN)
		}
		for _, d := range e.Prefixes {
			if d.Valid == 0 {
				continue
			}
			prefixes, err := d.lanPrefixes(len(lans))
			if err != nil {
				continue
			} // Withdraw old LANs, retain valid independent WAN state.
			for i, iface := range lans {
				p := prefixes[i]
				if err = put(ipv6Resource{Kind: "address", Interface: iface, Prefix: netip.PrefixFrom(p.Addr().Next(), 64), Preferred: d.Preferred, Valid: d.Valid}); err != nil {
					return nil, err
				}
				if err = put(ipv6Resource{Kind: "route", Interface: iface, Prefix: p, Metric: 256, Valid: d.Valid}); err != nil {
					return nil, err
				}
			}
		}
	}

	var result []ipv6Resource
	for _, r := range byKey {
		if err := validateIPv6Resource(c, r); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key() < result[j].key() })
	return result, nil
}

func validateIPv6Resource(c ipv6RuntimeConfig, r ipv6Resource) error {
	bad := errors.New("invalid owned IPv6 resource")
	if r.Interface != c.WAN && !c.isLAN(r.Interface) {
		return bad
	}
	if r.Kind == "address" {
		if !protocolIPv6Address(r.Prefix.Addr(), false) || r.Gateway.IsValid() || r.Metric != 0 || r.Preferred > r.Valid {
			return bad
		}
		if _, ok := delegationNetwork(r.Prefix.Masked().String()); !ok {
			return bad
		}
		if (r.Interface == c.WAN && r.Prefix.Bits() != 128) || (c.isLAN(r.Interface) && r.Prefix.Bits() != 64) {
			return bad
		}
	} else if r.Kind == "route" {
		if r.Prefix != netip.MustParsePrefix("::/0") {
			if _, ok := delegationNetwork(r.Prefix.String()); !ok {
				return bad
			}
		}
		if r.Metric == 0 || r.Preferred != 0 {
			return bad
		}
		if r.Gateway.IsValid() && (!r.Gateway.Is6() || !r.Gateway.IsLinkLocalUnicast() || r.Gateway.Zone() != "" || r.Interface != c.WAN) {
			return bad
		}
		if r.Prefix.Bits() == 0 && !r.Gateway.IsValid() {
			return bad
		}
	} else {
		return bad
	}
	return nil
}

func (m *Manager) ipv6JournalPath() string { return filepath.Join(m.base(), "ipv6-resources.json") }
func (m *Manager) readIPv6Journal() (*ipv6ResourceJournal, error) {
	f, err := openIPv6PrivateFileLimit(m.ipv6JournalPath(), false, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, (1<<20)+1))
	decoder.DisallowUnknownFields()
	var j ipv6ResourceJournal
	if err = decoder.Decode(&j); err != nil {
		return nil, errors.New("invalid IPv6 resource journal")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || j.Version != 1 || !ipv6GenerationName.MatchString(j.Generation) || len(j.Resources) > 2048 {
		return nil, errors.New("invalid IPv6 resource journal")
	}
	c := j.Context
	if !m.allowed[c.PhysicalWAN] || c.PhysicalWAN == m.opts.WiFiInterface || !ifname.MatchString(c.LAN) || m.allowed[c.LAN] || c.LAN == "lo" || c.LAN == managedPPPInterface || c.LAN == m.opts.WiFiInterface {
		return nil, errors.New("unauthorized IPv6 journal interfaces")
	}
	if !m.validIPv6WiFiLAN(c) {
		return nil, errors.New("unauthorized IPv6 WiFi journal interface")
	}
	wan := c.PhysicalWAN
	if c.Mode == "pppoe6" {
		wan = managedPPPInterface
	} else if c.Mode != "dhcpv6" && c.Mode != "slaac" {
		return nil, errors.New("invalid IPv6 journal mode")
	}
	if c.WAN != wan || !c.Enabled {
		return nil, errors.New("invalid IPv6 journal context")
	}
	if err := validateIPv6MTUResource(j.MTU); err != nil {
		return nil, err
	}
	for _, r := range j.Resources {
		if err = validateIPv6Resource(c, r); err != nil {
			return nil, err
		}
	}
	return &j, nil
}
func (m *Manager) writeIPv6Journal(j ipv6ResourceJournal) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return errors.New("IPv6 journal limit exceeded")
	}
	return atomicFile(m.ipv6JournalPath(), b)
}

func (m *Manager) reconcileIPv6Event(ctx context.Context, generation, iface string, event ipv6Event, observedAt time.Time) error {
	return m.withIPv6Generation(ctx, generation, iface, func(c ipv6RuntimeConfig) error {
		old, err := m.readIPv6Journal()
		if err != nil {
			return err
		}
		var previous ipv6Lease
		var mtu *ipv6MTUResource
		var owned []ipv6Resource
		if old != nil {
			if old.Generation != generation {
				if len(old.Resources) != 0 || old.MTU != nil {
					return errors.New("previous IPv6 resources require cleanup")
				}
			} else {
				if old.Context != c {
					return errors.New("IPv6 journal context changed")
				}
				previous = old.Lease
				owned = old.Resources
				mtu = old.MTU
			}
		}
		lease, err := nextIPv6Lease(previous, event, observedAt)
		if err != nil {
			return err
		}
		desired, err := ipv6DesiredResources(c, lease.Event)
		if err != nil {
			return err
		}
		if err = m.applyIPv6Resources(ctx, ipv6ResourceJournal{Version: 1, Generation: generation, Context: c, Lease: lease, MTU: mtu}, owned, desired); err != nil {
			return err
		}
		if c.PD && c.WiFiLAN != "" {
			live, usable := false, false
			for _, d := range lease.Event.Prefixes {
				if d.Valid == 0 {
					continue
				}
				live = true
				if _, err := d.lanPrefixes(2); err == nil {
					usable = true
				}
			}
			if live && !usable {
				return errors.New("IPv6 delegation cannot provide two distinct LAN /64s; LAN prefixes withdrawn, WAN retained")
			}
		}
		return nil
	})
}

type ipv6ObservedResource struct {
	Resource         ipv6Resource
	Tagged, Conflict bool
}

func ipv6ProtocolTag(raw json.RawMessage) bool {
	// iproute2 uses hexadecimal names for unknown address protocols and
	// decimal names for routes. Accept only our exact reserved tag.
	return string(raw) == ipv6ResourceProtocol || string(raw) == `"242"` || string(raw) == `"0xf2"`
}

func (m *Manager) observeIPv6Resources(ctx context.Context, c ipv6RuntimeConfig) (map[string]ipv6ObservedResource, error) {
	result := map[string]ipv6ObservedResource{}
	put := func(r ipv6Resource, tagged, conflict bool) {
		key := r.key()
		if _, ok := result[key]; ok {
			conflict = true
		}
		result[key] = ipv6ObservedResource{r, tagged, conflict}
	}
	s, err := m.run(ctx, "ip", "-j", "-6", "address", "show")
	if err != nil {
		return nil, err
	}
	var links []struct {
		IfName    string `json:"ifname"`
		Addresses []struct {
			Family, Local string
			Bits          int `json:"prefixlen"`
			Protocol      json.RawMessage
		} `json:"addr_info"`
	}
	if err = json.Unmarshal([]byte(s), &links); err != nil {
		return nil, errors.New("invalid IPv6 address observation")
	}
	for _, link := range links {
		if link.IfName != c.WAN && !c.isLAN(link.IfName) {
			continue
		}
		for _, a := range link.Addresses {
			if a.Family != "inet6" {
				continue
			}
			addr, err := netip.ParseAddr(a.Local)
			if err != nil || a.Bits < 0 || a.Bits > 128 {
				return nil, errors.New("invalid IPv6 address observation")
			}
			put(ipv6Resource{Kind: "address", Interface: link.IfName, Prefix: netip.PrefixFrom(addr, a.Bits)}, ipv6ProtocolTag(a.Protocol), false)
		}
	}
	s, err = m.run(ctx, "ip", "-j", "-6", "route", "show", "table", "main")
	if err != nil {
		return nil, err
	}
	var routes []struct {
		Dst, Dev, Gateway, Type, From string
		Metric                        uint32
		Protocol, Multipath           json.RawMessage
	}
	if err = json.Unmarshal([]byte(s), &routes); err != nil {
		return nil, errors.New("invalid IPv6 route observation")
	}
	for _, r := range routes {
		dst := r.Dst
		if dst == "default" || dst == "" {
			dst = "::/0"
		}
		p, err := netip.ParsePrefix(dst)
		if err != nil && !strings.Contains(dst, "/") {
			// iproute2 omits /128 for host routes, including unrelated
			// kernel-connected routes present in the same routing table.
			if addr, parseErr := netip.ParseAddr(dst); parseErr == nil && addr.Is6() && !addr.Is4In6() && addr.Zone() == "" {
				p, err = netip.PrefixFrom(addr, 128), nil
			}
		}
		if err != nil {
			return nil, errors.New("invalid IPv6 route observation")
		}
		var gw netip.Addr
		if r.Gateway != "" {
			gw, err = netip.ParseAddr(r.Gateway)
			if err != nil {
				return nil, errors.New("invalid IPv6 route observation")
			}
		}
		metric := r.Metric
		if metric == 0 {
			metric = 1024
		}
		conflict := (r.Type != "" && r.Type != "unicast") || (r.From != "" && r.From != "all") || len(r.Multipath) > 0
		put(ipv6Resource{Kind: "route", Interface: r.Dev, Prefix: p, Gateway: gw, Metric: metric}, ipv6ProtocolTag(r.Protocol), conflict)
	}
	return result, nil
}

func (m *Manager) applyIPv6Resources(ctx context.Context, j ipv6ResourceJournal, owned, desired []ipv6Resource) error {
	if len(desired) > 0 && (j.Lease.ObservedAt.IsZero() || m.opts.Clock().Before(j.Lease.ObservedAt)) {
		return errors.New("IPv6 resource observation time is unavailable or in the future")
	}
	live, err := m.observeIPv6Resources(ctx, j.Context)
	if err != nil {
		return err
	}
	identities := map[ipv6Resource]bool{}
	for _, r := range owned {
		identities[r.identity()] = true
	}
	for _, r := range append(append([]ipv6Resource(nil), owned...), desired...) {
		if actual, exists := live[r.key()]; exists && (!actual.Tagged || actual.Conflict || !identities[actual.Resource.identity()]) {
			return errors.New("refusing to replace an unowned IPv6 resource")
		}
	}
	union := append([]ipv6Resource(nil), owned...)
	for _, r := range desired {
		if !identities[r.identity()] {
			union = append(union, r)
			identities[r.identity()] = true
		}
	}
	if len(union) > 2048 {
		return errors.New("IPv6 resource limit exceeded")
	}
	mtu, err := m.planIPv6MTU(ctx, j.Context, j.Lease.Event.MTU, j.MTU)
	if err != nil {
		return err
	}
	j.MTU = mtu.state
	j.Pending = true
	j.Resources = union
	if err = m.writeIPv6Journal(j); err != nil {
		return err
	}
	if err = m.applyIPv6MTU(ctx, j.Context.WAN, mtu); err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, r := range desired {
		wanted[r.key()] = true
	}
	// Delete only observed, recorded resources; an already expired lease is
	// absent, not a fatal deletion error. Routes go before addresses.
	deleted := map[string]bool{}
	for _, kind := range []string{"route", "address"} {
		for _, r := range owned {
			if r.Kind != kind || wanted[r.key()] || deleted[r.key()] {
				continue
			}
			actual, exists := live[r.key()]
			if !exists {
				continue
			}
			if err = m.runIPv6Resource(ctx, "del", actual.Resource); err != nil {
				return err
			}
			deleted[r.key()] = true
		}
	}
	var installed []ipv6Resource
	for _, r := range desired {
		now := m.opts.Clock()
		if now.Before(j.Lease.ObservedAt) {
			return errors.New("IPv6 resource clock moved backwards")
		}
		elapsed := now.Sub(j.Lease.ObservedAt)
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
		r.Preferred, r.Valid = age(r.Preferred), age(r.Valid)
		if r.Valid == 0 {
			// An old assignment in the same kernel slot must also disappear;
			// merely skipping replacement would retain the previous lease.
			if actual, exists := live[r.key()]; exists {
				if err = m.runIPv6Resource(ctx, "del", actual.Resource); err != nil {
					return err
				}
			}
			continue
		}
		if err = m.runIPv6Resource(ctx, "replace", r); err != nil {
			return err
		}
		installed = append(installed, r)
	}
	var dns strings.Builder
	seen := map[netip.Addr]bool{}
	for _, list := range [][]netip.Addr{j.Lease.Event.DHCPDNS, j.Lease.Event.RADNS} {
		for _, a := range list {
			if seen[a] {
				continue
			}
			seen[a] = true
			value := a.String()
			if a.IsLinkLocalUnicast() {
				value += "%" + j.Context.WAN
			}
			fmt.Fprintf(&dns, "nameserver %s\n", value)
		}
	}
	if err = m.publishDNSSource("wan6", dns.String()); err != nil {
		return err
	}
	if mtu.state == nil || mtu.clear {
		j.MTU = nil
	} else {
		j.MTU = &ipv6MTUResource{Original: mtu.state.Original, Previous: mtu.target, Target: mtu.target}
	}
	j.Pending = false
	j.Resources = installed
	return m.writeIPv6Journal(j)
}

func (m *Manager) runIPv6Resource(ctx context.Context, operation string, r ipv6Resource) error {
	args := []string{"-6", r.Kind, operation, r.Prefix.String()}
	if r.Kind == "route" && r.Gateway.IsValid() {
		args = append(args, "via", r.Gateway.String())
	}
	args = append(args, "dev", r.Interface)
	if r.Kind == "route" {
		args = append(args, "proto", ipv6ResourceProtocol, "metric", strconv.FormatUint(uint64(r.Metric), 10))
	}
	if operation == "replace" {
		life := func(n uint32) string {
			if n == ^uint32(0) {
				return "forever"
			}
			return strconv.FormatUint(uint64(n), 10)
		}
		if r.Kind == "address" {
			args = append(args, "proto", ipv6ResourceProtocol, "noprefixroute", "preferred_lft", life(r.Preferred), "valid_lft", life(r.Valid))
		} else if r.Valid != ^uint32(0) {
			args = append(args, "expires", life(r.Valid))
		}
	}
	return m.cmd(ctx, "ip", args...)
}

// Called under the shared generation lock, after protocol services stop.
func (m *Manager) cleanupIPv6Resources(ctx context.Context) error {
	j, err := m.readIPv6Journal()
	if err != nil || j == nil {
		return err
	}
	current, err := os.Readlink(filepath.Join(m.base(), "current"))
	if err != nil {
		return err
	}
	if j.Generation != current && (len(j.Resources) > 0 || j.MTU != nil) {
		return errors.New("IPv6 resource generation does not match current")
	}
	j.Lease = ipv6Lease{}
	return m.applyIPv6Resources(ctx, *j, j.Resources, nil)
}
