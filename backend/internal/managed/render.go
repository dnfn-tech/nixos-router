package managed

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/modules"
	"net"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var ifname = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}$`)
var domain = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

func ipv4(s string) bool {
	p := net.ParseIP(s)
	return p != nil && p.To4() != nil && !strings.Contains(s, ":")
}
func clean(s string) bool { return !strings.ContainsAny(s, "\r\n\x00") }
func (m *Manager) validate(c config.Config) error {
	if err := m.validateManagementConfig(c); err != nil {
		return err
	}
	if e := c.Validate(); e != nil {
		return e
	}
	if _, err := renderPPPoE(c); err != nil {
		return err
	}
	if c.LAN.BridgeName == managedPPPInterface || m.allowed[managedPPPInterface] || m.opts.WiFiInterface == managedPPPInterface {
		return errors.New("PPP interface name is reserved")
	}
	if c.QoS.Enable || c.DDNS.Enable || c.Parental.Enable || c.Firewall.UPnPEnable {
		return errors.New("managed mode does not support QoS/DDNS/parental/UPnP")
	}
	for id, p := range c.Plugins {
		if p.Enable && !slices.Contains(m.opts.Modules, id) {
			return &config.ValidationError{Path: "plugins." + id + ".enable", Code: "module_unavailable", Message: "module is not allowed by deployment"}
		}
	}
	if m.good != nil && (c.LAN.BridgeName != m.good.LAN.BridgeName || c.WAN.Interface != m.good.WAN.Interface) {
		return errors.New("changing the owned bridge or WAN interface requires explicit offline migration")
	}
	if !m.allowed[c.WAN.Interface] || c.WAN.Interface == m.opts.WiFiInterface {
		return errors.New("WAN interface not in wired allowlist")
	}
	if !ifname.MatchString(c.LAN.BridgeName) || m.allowed[c.LAN.BridgeName] || c.LAN.BridgeName == m.opts.WiFiInterface || c.LAN.BridgeName == "lo" {
		return errors.New("invalid or conflicting LAN bridge")
	}
	seen := map[string]bool{c.WAN.Interface: true}
	for _, p := range c.LAN.Ports {
		if !m.allowed[p] || seen[p] || p == m.opts.WiFiInterface {
			return errors.New("LAN port is unowned or conflicts")
		}
		seen[p] = true
	}
	ip, n, e := net.ParseCIDR(c.LAN.IPv4CIDR)
	if e != nil || ip.To4() == nil || strings.Contains(c.LAN.IPv4CIDR, ":") {
		return errors.New("LAN requires IPv4 CIDR")
	}
	ones, _ := n.Mask.Size()
	if ones > 30 {
		return errors.New("LAN subnet too small")
	}
	usable := func(s string) bool {
		p := net.ParseIP(s)
		if !ipv4(s) || !n.Contains(p) || p.Equal(ip) || p.Equal(n.IP) {
			return false
		}
		broadcast := append(net.IP(nil), n.IP.To4()...)
		for i := range broadcast {
			broadcast[i] |= ^n.Mask[i]
		}
		return !p.Equal(broadcast)
	}
	if c.LAN.DHCP.Enable {
		if !usable(c.LAN.DHCP.RangeStart) || !usable(c.LAN.DHCP.RangeEnd) || bytes.Compare(net.ParseIP(c.LAN.DHCP.RangeStart).To4(), net.ParseIP(c.LAN.DHCP.RangeEnd).To4()) > 0 {
			return errors.New("invalid LAN DHCP range")
		}
	}
	for _, s := range c.LAN.StaticLeases {
		mac, e := net.ParseMAC(s.MAC)
		if e != nil || len(mac) != 6 || !usable(s.IP) || (s.Hostname != "" && !domain.MatchString(s.Hostname)) {
			return errors.New("invalid static lease")
		}
	}
	if (c.LAN.DHCP.Enable || (routedWiFi(c) && c.WiFi.Network.DHCP.Enable)) && !c.DNS.EnableDNSMasq && len(configuredUpstreams(c)) == 0 {
		return errors.New("DHCP without local DNS requires explicit DNS upstreams or static WAN DNS")
	}
	if c.DNS.Domain != "" && !domain.MatchString(c.DNS.Domain) {
		return errors.New("invalid DNS domain")
	}
	for _, s := range c.DNS.Upstreams {
		if !ipv4(s) {
			return errors.New("upstream must be IPv4")
		}
	}
	if c.WAN.Mode == "static" {
		w, wn, e := net.ParseCIDR(c.WAN.Static.AddressCIDR)
		if e != nil || w.To4() == nil || strings.Contains(c.WAN.Static.AddressCIDR, ":") || n.Contains(w) || wn.Contains(ip) {
			return errors.New("invalid or overlapping WAN subnet")
		}
		if c.WAN.Static.Gateway != "" && (!ipv4(c.WAN.Static.Gateway) || !wn.Contains(net.ParseIP(c.WAN.Static.Gateway))) {
			return errors.New("invalid WAN gateway")
		}
		for _, s := range c.WAN.Static.DNS {
			if !ipv4(s) {
				return errors.New("WAN DNS must be IPv4")
			}
		}
	}
	for _, p := range c.Firewall.PortForwards {
		if !usable(p.DestIP) {
			return errors.New("forward destination must be LAN host")
		}
	}
	for _, s := range c.Firewall.BlockedMACs {
		mac, e := net.ParseMAC(s)
		if e != nil || len(mac) != 6 {
			return errors.New("invalid Ethernet MAC")
		}
	}
	if c.WiFi.Enable {
		if m.opts.WiFiInterface == "" {
			return errors.New("WiFi requires an allowlisted interface")
		}
		count := 0
		for _, a := range c.WiFi.APs {
			if !a.Enable {
				continue
			}
			count++
			switch a.Security {
			case "", "wpa2-psk", "wpa3-sae":
			default:
				return errors.New("unsupported WiFi security")
			}
			if a.Guest || a.Band == "6g" {
				return errors.New("guest/6GHz AP unsupported")
			}
			if len(a.SSID) < 1 || len(a.SSID) > 32 || !clean(a.SSID) || !clean(a.PSK) || len(a.PSK) < 8 || len(a.PSK) > 63 {
				return errors.New("invalid SSID or WiFi passphrase")
			}
			for _, r := range a.PSK {
				if r < 32 || r > 126 {
					return errors.New("WiFi passphrase must be printable ASCII")
				}
			}
			if a.Channel == 0 {
				continue
			} // actual channel is selected in preflight
			if a.Band == "5g" {
				// DFS CAC may legitimately exceed the bounded AP-ready wait.
				// This initial profile supports only explicit non-DFS channels;
				// hostapd still enforces the configured country and driver rules.
				switch a.Channel {
				case 36, 40, 44, 48, 149, 153, 157, 161, 165:
				default:
					return errors.New("5GHz DFS unsupported; use non-DFS channel 36,40,44,48,149,153,157,161,165 subject to country and driver support")
				}
			} else if a.Channel < 1 || a.Channel > 11 {
				return errors.New("2GHz requires channel 1-11")
			}
		}
		if count != 1 {
			return errors.New("exactly one enabled AP required")
		}
	}
	return nil
}
func lanIPv6Advertisement(c config.Config) bool {
	return c.IPv6.Enable && c.IPv6.LANPD && c.IPv6.LANRA
}

func needsLANService(c config.Config) bool {
	return c.DNS.EnableDNSMasq || c.LAN.DHCP.Enable || (routedWiFi(c) && c.WiFi.Network.DHCP.Enable) || lanIPv6Advertisement(c)
}

func (m *Manager) render(c config.Config) map[string]string {
	lan, _, _ := net.ParseCIDR(c.LAN.IPv4CIDR)
	dns := fmt.Sprintf("interface=%s\nbind-dynamic\nexcept-interface=lo\ndhcp-authoritative\ndhcp-leasefile=%s\n", c.LAN.BridgeName, filepath.Join(m.opts.StateDir, "dnsmasq.leases"))
	if routedWiFi(c) {
		dns += "interface=" + m.opts.WiFiInterface + "\n"
	}
	if !c.DNS.EnableDNSMasq {
		dns += "port=0\n"
	} else {
		dns += "no-resolv\ndomain-needed\nbogus-priv\n"
		ups := configuredUpstreams(c)
		if len(ups) == 0 {
			dns += "resolv-file=" + filepath.Join(m.opts.StateDir, "managed/wan-resolv.conf") + "\n"
			dns = strings.ReplaceAll(dns, "no-resolv\n", "")
		}
		for _, s := range ups {
			dns += "server=" + s + "\n"
		}
		if c.DNS.Domain != "" {
			dns += "domain=" + c.DNS.Domain + "\nlocal=/" + c.DNS.Domain + "/\n"
		}
	}
	for _, d := range modules.All() {
		if pc := c.Plugins[d.ID]; pc.Enable {
			rules, _ := modules.Render(d.ID, pc.Config) // validate() ran before render.
			dns += rules
		}
	}
	if routedWiFi(c) {
		dns += renderDHCPv4Network(c, "wired", c.LAN.IPv4CIDR, c.LAN.DHCP, c.LAN.StaticLeases)
		dns += renderDHCPv4Network(c, "wifi", c.WiFi.Network.IPv4CIDR, c.WiFi.Network.DHCP, c.WiFi.Network.StaticLeases)
	} else {
		if c.LAN.DHCP.Enable {
			dns += fmt.Sprintf("dhcp-range=%s,%s,%dm\ndhcp-option=option:router,%s\n", c.LAN.DHCP.RangeStart, c.LAN.DHCP.RangeEnd, c.LAN.DHCP.LeaseMins, lan)
			if c.DNS.EnableDNSMasq {
				dns += "dhcp-option=option:dns-server," + lan.String() + "\n"
			} else if len(configuredUpstreams(c)) > 0 {
				dns += "dhcp-option=option:dns-server," + strings.Join(configuredUpstreams(c), ",") + "\n"
			}
			for _, s := range c.LAN.StaticLeases {
				dns += "dhcp-host=" + s.MAC + "," + s.IP
				if s.Hostname != "" {
					dns += "," + s.Hostname
				}
				dns += "\n"
			}
		}
	}
	if !c.DNS.EnableDNSMasq && c.DNS.Domain != "" {
		dns += "domain=" + c.DNS.Domain + "\n"
	}

	if lanIPv6Advertisement(c) {
		// The owned bridge address carries the delegated prefix and its real
		// preferred/valid lifetimes; never bake a sampled lease into this file.
		mode := "ra-stateless"
		if !c.DNS.EnableDNSMasq {
			mode = "ra-only"
			dns += "dhcp-option=option6:dns-server\n"
		}
		dns += fmt.Sprintf("enable-ra\ndhcp-range=::,constructor:%s,%s,64\n", c.LAN.BridgeName, mode)
		if routedWiFi(c) {
			dns += fmt.Sprintf("dhcp-range=::,constructor:%s,%s,64\n", m.opts.WiFiInterface, mode)
		}
	}
	host := ""
	if c.WiFi.Enable {
		for _, a := range c.WiFi.APs {
			if !a.Enable {
				continue
			}
			mode := "g"
			if a.Channel == 0 {
				a.Channel = m.wifiSelected
				a.ChannelMode = "fixed"
			}
			if a.ChannelWidthMHz == config.WiFiAutoWidthMHz {
				a.ChannelWidthMHz = m.wifiSelectedWidth
			}
			if a.Band == "5g" {
				mode = "a"
			}
			isolate := 0
			if a.Isolate {
				isolate = 1
			}
			security := "wpa_key_mgmt=WPA-PSK\nwpa_passphrase=" + a.PSK + "\n"
			if a.Security == "wpa3-sae" {
				// SAE uses the shared literal passphrase when no SAE password list is
				// configured. Avoid sae_password list syntax interpreting |id=/|mac=.
				security = "wpa_key_mgmt=SAE\nieee80211w=2\nwpa_passphrase=" + a.PSK + "\n"
			}
			bridge := ""
			if c.WiFi.BridgeToLan {
				bridge = "bridge=" + c.LAN.BridgeName + "\n"
			}
			host = "ctrl_interface=" + filepath.Join(m.base(), "hostapd-control") + "\n" + fmt.Sprintf("interface=%s\n%sdriver=nl80211\ncountry_code=%s\nssid2=%s\nhw_mode=%s\nchannel=%d\nwpa=2\n%srsn_pairwise=CCMP\nauth_algs=1\nap_isolate=%d\n", m.opts.WiFiInterface, bridge, m.opts.CountryCode, hex.EncodeToString([]byte(a.SSID)), mode, a.Channel, security, isolate) + renderWiFiRadio(a)
		}
	}
	wan := ""
	if c.WAN.Mode == "dhcp" {
		wan = "WAN_INTERFACE='" + c.WAN.Interface + "'\n"
	}
	egress := WANEgress(c)
	ppp, _ := renderPPPoE(c) // validated before generation
	// add+flush+redefine runs as a single nft transaction and never touches foreign tables.
	nft := "add table inet nixos_router\nflush table inet nixos_router\ntable inet nixos_router {\n"
	managementPorts := []string{fmt.Sprint(m.opts.ManagementPort)}
	for _, port := range m.opts.SSHPorts {
		if port != m.opts.ManagementPort {
			managementPorts = append(managementPorts, fmt.Sprint(port))
		}
	}
	managementInterfaces := []string{`"lo"`, fmt.Sprintf("%q", c.LAN.BridgeName)}
	if routedWiFi(c) && c.WiFi.Network.AllowManagement {
		managementInterfaces = append(managementInterfaces, fmt.Sprintf("%q", m.opts.WiFiInterface))
	}
	nft += " chain input { type filter hook input priority 0; policy accept;"
	// Management gating precedes established-flow acceptance and optional firewall.
	nft += fmt.Sprintf(" iifname { %s } tcp dport { %s } accept;", strings.Join(managementInterfaces, ", "), strings.Join(managementPorts, ", "))
	for _, grant := range m.opts.ManagementAccess {
		for _, prefix := range grant.Sources {
			family := "ip"
			if strings.Contains(prefix, ":") {
				family = "ip6"
			}
			nft += fmt.Sprintf(" iifname %q %s saddr %s tcp dport { %s } accept;", grant.Interface, family, prefix, strings.Join(managementPorts, ", "))
		}
	}
	nft += fmt.Sprintf(" tcp dport { %s } drop;", strings.Join(managementPorts, ", "))
	if routedWiFi(c) {
		wifi := fmt.Sprintf(" iifname %q ", m.opts.WiFiInterface)
		nft += wifi + "ct state established,related accept;"
		if c.WiFi.Network.DHCP.Enable {
			nft += wifi + "meta nfproto ipv4 udp sport 68 udp dport 67 accept;"
		}
		if c.DNS.EnableDNSMasq {
			nft += wifi + "udp dport 53 accept;" + wifi + "tcp dport 53 accept;"
		}
		nft += wifi + "meta nfproto ipv4 icmp type { echo-request, echo-reply, destination-unreachable, time-exceeded, parameter-problem } accept;"
		if c.IPv6.Enable {
			nft += wifi + "ip6 hoplimit 255 icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-solicit } accept;"
			nft += wifi + "icmpv6 type { echo-request, echo-reply, destination-unreachable, packet-too-big, time-exceeded, parameter-problem } accept;"
			if lanIPv6Advertisement(c) && c.DNS.EnableDNSMasq {
				nft += wifi + "meta nfproto ipv6 udp sport 546 udp dport 547 accept;"
			}
		}
		if c.WiFi.Network.AllowManagement {
			nft += wifi + "tcp dport { " + strings.Join(managementPorts, ", ") + " } accept;"
		}
		nft += wifi + "drop;"
	}
	if c.Firewall.Enable {
		interfaces := []string{c.WAN.Interface}
		if egress != c.WAN.Interface {
			interfaces = append(interfaces, egress)
		}
		for _, iface := range interfaces {
			nft += fmt.Sprintf(" iifname %q ct state established,related accept;", iface)
			if iface == c.WAN.Interface {
				nft += fmt.Sprintf(" iifname %q meta nfproto ipv4 udp sport 67 udp dport 68 accept;", iface)
			}
			if c.IPv6.Enable && iface == egress {
				// odhcp6c receives RA directly; kernel autoconfiguration is off.
				// ND requires on-link hop limit, including DAD's unspecified source.
				nft += fmt.Sprintf(" iifname %q ip6 saddr fe80::/10 ip6 hoplimit 255 icmpv6 type nd-router-advert accept;", iface)
				nft += fmt.Sprintf(" iifname %q ip6 hoplimit 255 icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert } accept;", iface)
				nft += fmt.Sprintf(" iifname %q icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem } accept;", iface)
				if c.IPv6.WANMode == "dhcpv6" || c.IPv6.WANMode == "pppoe6" {
					nft += fmt.Sprintf(" iifname %q meta nfproto ipv6 udp sport 547 udp dport 546 accept;", iface)
				}
			}
			if iface == egress && len(m.opts.WANUDPPorts) > 0 {
				ports := make([]string, 0, len(m.opts.WANUDPPorts))
				for _, port := range m.opts.WANUDPPorts {
					ports = append(ports, fmt.Sprint(port))
				}
				nft += fmt.Sprintf(" iifname %q udp dport { %s } accept;", iface, strings.Join(ports, ", "))
			}
			nft += fmt.Sprintf(" iifname %q drop;", iface)
		}
	}
	nft += " }\n"
	policy := "accept"
	if c.Firewall.Enable {
		policy = "drop"
	}
	nft += " chain forward { type filter hook forward priority 0; policy " + policy + ";\n"
	for _, s := range c.Firewall.BlockedMACs {
		nft += " ether saddr " + s + " drop\n"
	}
	if routedWiFi(c) {
		wifi := m.opts.WiFiInterface
		if !c.WiFi.Network.AllowWiredAccess {
			nft += fmt.Sprintf(" iifname %q oifname %q drop\n iifname %q oifname %q drop\n", wifi, c.LAN.BridgeName, c.LAN.BridgeName, wifi)
		}
		peers := []string{fmt.Sprintf("%q", egress)}
		if c.WiFi.Network.AllowWiredAccess {
			peers = append(peers, fmt.Sprintf("%q", c.LAN.BridgeName))
		}
		// Preserve the independent zone even when the optional WAN filter is off.
		nft += fmt.Sprintf(" iifname %q oifname != { %s } drop\n oifname %q iifname != { %s } drop\n", wifi, strings.Join(peers, ", "), wifi, strings.Join(peers, ", "))
	}
	nft += fmt.Sprintf(" ct state established,related accept\n iifname \"%s\" oifname \"%s\" accept\n", c.LAN.BridgeName, egress)
	if routedWiFi(c) {
		nft += fmt.Sprintf(" iifname %q oifname %q accept\n", m.opts.WiFiInterface, egress)
		if c.WiFi.Network.AllowWiredAccess {
			nft += fmt.Sprintf(" iifname %q oifname %q accept\n iifname %q oifname %q accept\n", m.opts.WiFiInterface, c.LAN.BridgeName, c.LAN.BridgeName, m.opts.WiFiInterface)
		}
		nft += fmt.Sprintf(" oifname %q drop\n", m.opts.WiFiInterface)
	}
	for _, p := range c.Firewall.PortForwards {
		nft += fmt.Sprintf(" iifname \"%s\" oifname \"%s\" ct status dnat ip daddr %s %s dport %d accept\n", egress, c.LAN.BridgeName, p.DestIP, p.Protocol, p.DestPort)
	}
	nft += " }\n chain prerouting { type nat hook prerouting priority dstnat; policy accept;\n"
	for _, p := range c.Firewall.PortForwards {
		ports := fmt.Sprint(p.ExternalPort)
		if p.ExternalPortEnd > p.ExternalPort {
			ports += fmt.Sprintf("-%d", p.ExternalPortEnd)
		}
		nft += fmt.Sprintf(" iifname \"%s\" %s dport %s dnat ip to %s:%d\n", egress, p.Protocol, ports, p.DestIP, p.DestPort)
	}
	nft += " }\n chain postrouting { type nat hook postrouting priority srcnat; policy accept;\n"
	if c.Firewall.NATEnabled {
		_, subnet, _ := net.ParseCIDR(c.LAN.IPv4CIDR)
		nft += fmt.Sprintf(" ip saddr %s oifname \"%s\" masquerade\n", subnet, egress)
		if routedWiFi(c) {
			_, wifiSubnet, _ := net.ParseCIDR(c.WiFi.Network.IPv4CIDR)
			nft += fmt.Sprintf(" ip saddr %s oifname %q masquerade\n", wifiSubnet, egress)
		}
	}
	nft += " }\n"
	if c.WAN.Mode == "pppoe" {
		// Initial ceilings before a session exists. The synchronous PPP
		// net-pre-up hook replaces both families before either NCP starts.
		nft += " chain ppp_mss { type filter hook forward priority mangle; policy accept;\n"
		for _, direction := range []string{"iifname", "oifname"} {
			nft += fmt.Sprintf(" meta nfproto ipv4 %s \"%s\" tcp flags & (syn | rst) == syn tcp option maxseg size > 1452 tcp option maxseg size set 1452\n", direction, egress)
			nft += fmt.Sprintf(" meta nfproto ipv6 %s \"%s\" tcp flags & (syn | rst) == syn tcp option maxseg size > 1432 tcp option maxseg size set 1432\n", direction, egress)
		}
		nft += " }\n"
	}
	nft += "}\n"
	return map[string]string{"dnsmasq.conf": dns, "hostapd.conf": host, "wan.env": wan, "firewall.nft": nft, "pppoe.options": ppp}
}

func configuredUpstreams(c config.Config) []string {
	if len(c.DNS.Upstreams) > 0 {
		return c.DNS.Upstreams
	}
	if c.WAN.Mode == "static" && c.WAN.Static != nil {
		return c.WAN.Static.DNS
	}
	return nil
}

func renderDHCPv4Network(c config.Config, tag, cidr string, dhcp config.DHCPv4Config, leases []config.StaticLease) string {
	if !dhcp.Enable {
		return ""
	}
	gateway, _, _ := net.ParseCIDR(cidr)
	result := fmt.Sprintf("dhcp-range=set:%s,%s,%s,%dm\ndhcp-option=tag:%s,option:router,%s\n", tag, dhcp.RangeStart, dhcp.RangeEnd, dhcp.LeaseMins, tag, gateway)
	dns := configuredUpstreams(c)
	if c.DNS.EnableDNSMasq {
		dns = []string{gateway.String()}
	}
	if len(dns) > 0 {
		result += fmt.Sprintf("dhcp-option=tag:%s,option:dns-server,%s\n", tag, strings.Join(dns, ","))
	}
	for _, lease := range leases {
		result += "dhcp-host=" + lease.MAC + "," + lease.IP
		if lease.Hostname != "" {
			result += "," + lease.Hostname
		}
		result += "\n"
	}
	return result
}
