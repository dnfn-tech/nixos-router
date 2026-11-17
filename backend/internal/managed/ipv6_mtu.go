package managed

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Previous and Target retain both sides of an interrupted sysctl write. The
// baseline is never resampled from an MTU that this generation already changed.
type ipv6MTUResource struct {
	Original, Previous, Target uint32
}

type ipv6MTUChange struct {
	state           *ipv6MTUResource
	current, target uint32
	clear           bool
}

func validKernelIPv6MTU(n uint32) bool { return n >= 1280 && n <= 1<<31-1 }
func validateIPv6MTUResource(r *ipv6MTUResource) error {
	if r != nil && (!validKernelIPv6MTU(r.Original) || !validKernelIPv6MTU(r.Previous) || !validKernelIPv6MTU(r.Target)) {
		return errors.New("invalid IPv6 MTU journal")
	}
	return nil
}

func (m *Manager) planIPv6MTU(ctx context.Context, c ipv6RuntimeConfig, advertised uint32, old *ipv6MTUResource) (ipv6MTUChange, error) {
	fail := func(message string) (ipv6MTUChange, error) { return ipv6MTUChange{}, errors.New(message) }
	if !ifname.MatchString(c.WAN) || c.WAN == "lo" || c.WAN == "all" || c.WAN == "default" {
		return fail("invalid IPv6 MTU interface")
	}
	if advertised != 0 && (advertised < 1280 || advertised > 65535) {
		return fail("invalid advertised IPv6 MTU")
	}
	if err := validateIPv6MTUResource(old); err != nil {
		return ipv6MTUChange{}, err
	}
	if old == nil && advertised == 0 {
		return ipv6MTUChange{}, nil
	}
	raw, err := m.run(ctx, "ip", "-j", "link", "show")
	if err != nil {
		return ipv6MTUChange{}, err
	}
	var links []struct {
		Name string `json:"ifname"`
		MTU  uint32 `json:"mtu"`
	}
	if json.Unmarshal([]byte(raw), &links) != nil {
		return fail("invalid IPv6 MTU link observation")
	}
	var link uint32
	for _, item := range links {
		if item.Name == c.WAN {
			if link != 0 {
				return fail("ambiguous IPv6 MTU link")
			}
			link = item.MTU
		}
	}
	if link == 0 {
		// PPP may disappear before its final callback. An absent interface no
		// longer owns a sysctl value that needs restoring.
		if advertised == 0 {
			return ipv6MTUChange{clear: true}, nil
		}
		return fail("IPv6 MTU interface unavailable")
	}
	if !validKernelIPv6MTU(link) {
		return fail("invalid IPv6 link MTU")
	}
	key := "net/ipv6/conf/" + c.WAN + "/mtu"
	raw, err = m.run(ctx, "sysctl", "-n", key)
	if err != nil {
		return ipv6MTUChange{}, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	current := uint32(n)
	if err != nil || !validKernelIPv6MTU(current) || current > link {
		return fail("invalid IPv6 MTU observation")
	}
	original := current
	if old != nil {
		if current != min(old.Original, link) && current != min(old.Previous, link) && current != min(old.Target, link) {
			return fail("refusing to replace an unowned IPv6 MTU")
		}
		original = old.Original
	}
	target := min(original, link)
	if advertised != 0 {
		if advertised <= link {
			target = advertised
		} else if old != nil {
			target = min(old.Target, link)
		} else {
			return ipv6MTUChange{}, nil
		}
	}
	if old == nil && current == target {
		return ipv6MTUChange{}, nil
	}
	return ipv6MTUChange{state: &ipv6MTUResource{Original: original, Previous: current, Target: target}, current: current, target: target, clear: advertised == 0}, nil
}

func (m *Manager) applyIPv6MTU(ctx context.Context, iface string, change ipv6MTUChange) error {
	if change.state == nil || change.current == change.target {
		return nil
	}
	return m.cmd(ctx, "sysctl", "-qw", "net/ipv6/conf/"+iface+"/mtu="+strconv.FormatUint(uint64(change.target), 10))
}
