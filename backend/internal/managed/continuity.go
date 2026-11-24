package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// A verified, live topology can keep its protocol generation. Changing that
// generation without restarting DHCP/PPP would silently reject their callbacks.
// Cold restore, changed topology and observed drift still use full activation.
func sameRuntimeTopology(a, b config.Config) bool {
	portsA, portsB := slices.Clone(a.LAN.Ports), slices.Clone(b.LAN.Ports)
	slices.Sort(portsA)
	slices.Sort(portsB)
	if !reflect.DeepEqual(a.WAN, b.WAN) || !reflect.DeepEqual(a.IPv6, b.IPv6) ||
		a.LAN.BridgeName != b.LAN.BridgeName || a.LAN.IPv4CIDR != b.LAN.IPv4CIDR || !slices.Equal(portsA, portsB) ||
		a.WiFi.Enable != b.WiFi.Enable || a.WiFi.BridgeToLan != b.WiFi.BridgeToLan {
		return false
	}
	if routedWiFi(a) {
		return a.WiFi.Network != nil && b.WiFi.Network != nil && a.WiFi.Network.IPv4CIDR == b.WiFi.Network.IPv4CIDR
	}
	return true
}

type continuityLink struct {
	Name   string   `json:"ifname"`
	Alias  string   `json:"ifalias"`
	Master string   `json:"master"`
	Flags  []string `json:"flags"`
	MTU    int      `json:"mtu"`
	Info   struct {
		Kind string `json:"info_kind"`
	} `json:"linkinfo"`
}

func (m *Manager) liveTopology(ctx context.Context, c config.Config) (bool, int) {
	raw, err := m.run(ctx, "ip", "-j", "-d", "link", "show")
	if err != nil {
		return false, 0
	}
	var links []continuityLink
	if json.Unmarshal([]byte(raw), &links) != nil {
		return false, 0
	}
	byName := map[string]continuityLink{}
	for _, link := range links {
		byName[link.Name] = link
	}
	bridge := byName[c.LAN.BridgeName]
	if bridge.Alias != "nixos-router-managed" || bridge.Info.Kind != "bridge" || !slices.Contains(bridge.Flags, "UP") {
		return false, 0
	}
	wan := byName[c.WAN.Interface]
	if wan.Name == "" || wan.Master != "" || !slices.Contains(wan.Flags, "UP") || !slices.Contains(wan.Flags, "LOWER_UP") {
		return false, 0
	}
	for port := range m.allowed {
		link := byName[port]
		if slices.Contains(c.LAN.Ports, port) {
			if link.Master != c.LAN.BridgeName || !slices.Contains(link.Flags, "UP") {
				return false, 0
			}
		} else if link.Master == c.LAN.BridgeName {
			return false, 0
		}
	}
	if c.WiFi.Enable {
		wifi := byName[m.opts.WiFiInterface]
		master := ""
		if c.WiFi.BridgeToLan {
			master = c.LAN.BridgeName
		}
		if wifi.Name == "" || wifi.Master != master || !slices.Contains(wifi.Flags, "UP") {
			return false, 0
		}
	} else if byName[m.opts.WiFiInterface].Master == c.LAN.BridgeName {
		return false, 0
	}
	mtu := 0
	if c.WAN.Mode == "pppoe" {
		ppp := byName[managedPPPInterface]
		if ppp.Alias != "nixos-router-managed-ppp" || !slices.Contains(ppp.Flags, "UP") || ppp.MTU < 128 || ppp.MTU > 1492 {
			return false, 0
		}
		mtu = ppp.MTU
	}
	raw, err = m.run(ctx, "ip", "-j", "-4", "addr", "show")
	if err != nil {
		return false, 0
	}
	var addresses []struct {
		Name string `json:"ifname"`
		Info []struct {
			Local  string `json:"local"`
			Prefix int    `json:"prefixlen"`
			Scope  string `json:"scope"`
		} `json:"addr_info"`
	}
	if json.Unmarshal([]byte(raw), &addresses) != nil {
		return false, 0
	}
	wanted := map[string]string{c.LAN.BridgeName: c.LAN.IPv4CIDR}
	if routedWiFi(c) {
		wanted[m.opts.WiFiInterface] = c.WiFi.Network.IPv4CIDR
	}
	if c.WAN.Mode == "static" {
		wanted[c.WAN.Interface] = c.WAN.Static.AddressCIDR
	}
	expected := make(map[string]string, len(wanted))
	for name, cidr := range wanted {
		expected[name] = cidr
	}
	counts := map[string]int{}
	wanReady := false
	for _, link := range addresses {
		for _, addr := range link.Info {
			if addr.Scope != "global" {
				continue
			}
			if link.Name == wanEgress(c) && ipv4(addr.Local) {
				wanReady = true
			}
			if cidr, ok := expected[link.Name]; ok {
				counts[link.Name]++
				prefix, err := netip.ParsePrefix(cidr)
				if err != nil || prefix.Addr().String() != addr.Local || prefix.Bits() != addr.Prefix || counts[link.Name] > 1 {
					return false, 0
				}
			}
			if cidr, ok := wanted[link.Name]; ok {
				prefix, err := netip.ParsePrefix(cidr)
				if err == nil && prefix.Addr().String() == addr.Local && prefix.Bits() == addr.Prefix {
					delete(wanted, link.Name)
				}
			}
		}
	}
	if len(wanted) != 0 || !wanReady {
		return false, 0
	}
	if c.WAN.Mode == "static" && c.WAN.Static.Gateway != "" {
		raw, err = m.run(ctx, "ip", "-j", "-4", "route", "show", "default")
		if err != nil {
			return false, 0
		}
		var routes []struct {
			Dev     string `json:"dev"`
			Gateway string `json:"gateway"`
		}
		if json.Unmarshal([]byte(raw), &routes) != nil {
			return false, 0
		}
		found := false
		for _, route := range routes {
			if route.Dev == c.WAN.Interface && route.Gateway == c.WAN.Static.Gateway {
				found = true
			}
		}
		if !found {
			return false, 0
		}
	}
	units := []string{}
	if c.WAN.Mode == "dhcp" {
		units = append(units, "wan")
	}
	if c.WAN.Mode == "pppoe" {
		units = append(units, "pppoe")
	}
	if c.IPv6.Enable {
		units = append(units, "wan6")
	}
	if needsLANService(c) {
		units = append(units, "dnsmasq")
	}
	if c.WiFi.Enable {
		units = append(units, "hostapd")
	}
	for _, unit := range units {
		if m.cmd(ctx, "systemctl", "is-active", "--quiet", "nixos-router-"+unit+".service") != nil {
			return false, 0
		}
	}
	return true, mtu
}

func readContinuityFile(path string) ([]byte, error) {
	f, err := openIPv6PrivateFileLimit(path, false, config.MaxDocumentBytes)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, config.MaxDocumentBytes+1))
}

func (m *Manager) activatePreservingRuntime(ctx context.Context, c config.Config, old *config.Config, prepared string) (bool, error) {
	published := m.published.Load()
	if old == nil || published == nil || !sameRuntimeTopology(c, *old) || !sameRuntimeTopology(*published, *old) {
		return false, nil
	}
	live, _ := m.liveTopology(ctx, *old)
	if !live {
		return false, nil
	}
	generation, err := os.Readlink(filepath.Join(m.base(), "current"))
	if err != nil || !ipv6GenerationName.MatchString(generation) {
		return false, nil
	}
	current := filepath.Join(m.base(), generation)
	info, err := os.Lstat(current)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return false, nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false, nil
	}
	// Verify callback contexts and session options still describe this runtime.
	protocol := m.render(*old)
	if old.WAN.Mode == "dhcp" {
		protocol["wan.env"] += "WAN_GENERATION='" + generation + "'\n"
	}
	if old.WAN.Mode == "pppoe" {
		protocol["pppoe.options"] += "ipparam " + generation + "\n"
	}
	checks := map[string][]byte{"wan.env": []byte(protocol["wan.env"]), "pppoe.options": []byte(protocol["pppoe.options"])}
	checks["dns-context.json"], _ = json.Marshal(dnsContextFor(*old))
	ipv6 := ipv6ContextFor(*old)
	if routedWiFi(*old) {
		ipv6.WiFiLAN = m.opts.WiFiInterface
	}
	checks["ipv6-context.json"], _ = json.Marshal(ipv6)
	env, err := ipv6ServiceEnvironment(*old, generation)
	if err != nil {
		return false, err
	}
	checks["wan6.env"] = []byte(env)
	for name, want := range checks {
		got, err := readContinuityFile(filepath.Join(current, name))
		if err != nil || !bytes.Equal(got, want) {
			return false, nil
		}
	}
	changed := map[string][]byte{}
	if err := m.withIPv6StateLock(ctx, func() error {
		mtu := 0
		if c.WAN.Mode == "pppoe" {
			raw, err := m.run(ctx, "ip", "-j", "-d", "link", "show", "dev", managedPPPInterface)
			var links []continuityLink
			if err != nil || json.Unmarshal([]byte(raw), &links) != nil {
				return fmt.Errorf("cannot verify live PPP MTU: %v", err)
			}
			for _, link := range links {
				if link.Name == managedPPPInterface && link.Alias == "nixos-router-managed-ppp" && link.MTU >= 128 && link.MTU <= 1492 {
					mtu = link.MTU
				}
			}
			if mtu == 0 {
				return fmt.Errorf("live PPP MTU unavailable")
			}
		}
		for _, name := range []string{"firewall.nft", "dnsmasq.conf", "hostapd.conf"} {
			data, err := os.ReadFile(filepath.Join(prepared, name))
			if err != nil {
				return err
			}
			if name == "firewall.nft" && mtu != 0 {
				// A firewall update must not restore the default MSS above the live
				// LCP-negotiated ceiling while the PPP session stays running.
				rules := strings.ReplaceAll(string(data), "maxseg size > 1452 tcp option maxseg size set 1452", fmt.Sprintf("maxseg size > %d tcp option maxseg size set %d", mtu-40, mtu-40))
				rules = strings.ReplaceAll(rules, "maxseg size > 1432 tcp option maxseg size set 1432", fmt.Sprintf("maxseg size > %d tcp option maxseg size set %d", mtu-60, mtu-60))
				data = []byte(rules)
				if err := atomicFile(filepath.Join(prepared, name), data); err != nil {
					return err
				}
				if err := m.cmd(ctx, "nft", "--check", "-f", filepath.Join(prepared, name)); err != nil {
					return err
				}
			}
			before, err := readContinuityFile(filepath.Join(current, name))
			if err != nil {
				return err
			}
			if !bytes.Equal(before, data) {
				changed[name] = data
			}
		}
		// File equality cannot prove kernel rule equality. Always restore the
		// intended policy, including rollback after a previous file-write error.
		if err := m.cmd(ctx, "nft", "-f", filepath.Join(prepared, "firewall.nft")); err != nil {
			return err
		}
		for _, name := range []string{"firewall.nft", "dnsmasq.conf", "hostapd.conf"} {
			if data, ok := changed[name]; ok {
				if err := atomicFile(filepath.Join(current, name), data); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return true, err
	}
	if _, ok := changed["dnsmasq.conf"]; ok {
		action := "stop"
		if needsLANService(c) {
			action = "restart"
		}
		if err := m.cmd(ctx, "systemctl", action, "nixos-router-dnsmasq.service"); err != nil {
			return true, err
		}
		if needsLANService(c) {
			if err := m.cmd(ctx, "systemctl", "is-active", "--quiet", "nixos-router-dnsmasq.service"); err != nil {
				return true, err
			}
		}
	}
	if _, ok := changed["hostapd.conf"]; ok && c.WiFi.Enable {
		if err := m.cmd(ctx, "systemctl", "restart", "nixos-router-hostapd.service"); err != nil {
			return true, err
		}
		if err := m.cmd(ctx, "systemctl", "is-active", "--quiet", "nixos-router-hostapd.service"); err != nil {
			return true, err
		}
		if err := m.waitAPReady(ctx); err != nil {
			return true, err
		}
	}
	return true, nil
}
