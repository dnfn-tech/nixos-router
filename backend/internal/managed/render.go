package managed

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"net"
	"path/filepath"
	"reflect"
	"regexp"
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
	if e := c.Validate(); e != nil {
		return e
	}
	if c.WAN.Mode != "dhcp" && c.WAN.Mode != "static" {
		return errors.New("managed mode does not support PPPoE")
	}
	if c.QoS.Enable || c.DDNS.Enable || c.Parental.Enable || c.IPv6.Enable || c.IPv6.LANRA || c.IPv6.LANPD || c.Firewall.UPnPEnable {
		return errors.New("managed mode does not support QoS/DDNS/parental/IPv6/UPnP")
	}
	for _, p := range c.Plugins {
		if p.Enable {
			return errors.New("managed mode does not support plugins")
		}
	}
	if m.good != nil && (c.LAN.BridgeName != m.good.LAN.BridgeName || c.WAN.Interface != m.good.WAN.Interface) {
		return errors.New("changing the owned bridge or WAN interface requires explicit offline migration")
	}
	if m.good != nil && !reflect.DeepEqual(c.SSH, m.good.SSH) {
		return errors.New("SSH settings are controlled by NixOS; runtime changes unsupported")
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
	if c.LAN.DHCP.Enable && !c.DNS.EnableDNSMasq && len(configuredUpstreams(c)) == 0 {
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
		if m.opts.WiFiInterface == "" || !c.WiFi.BridgeToLan {
			return errors.New("WiFi requires allowlisted interface and LAN bridging")
		}
		count := 0
		for _, a := range c.WiFi.APs {
			if !a.Enable {
				continue
			}
			count++
			if a.Guest || a.Band == "6g" {
				return errors.New("guest/6GHz AP unsupported")
			}
			if len(a.SSID) < 1 || len(a.SSID) > 32 || !clean(a.SSID) || !clean(a.PSK) || len(a.PSK) < 8 || len(a.PSK) > 63 {
				return errors.New("invalid SSID or WPA2 passphrase")
			}
			for _, r := range a.PSK {
				if r < 32 || r > 126 {
					return errors.New("WPA2 passphrase must be printable ASCII")
				}
			}
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
func (m *Manager) render(c config.Config) map[string]string {
	lan, _, _ := net.ParseCIDR(c.LAN.IPv4CIDR)
	dns := fmt.Sprintf("interface=%s\nbind-dynamic\nexcept-interface=lo\ndhcp-authoritative\ndhcp-leasefile=%s\n", c.LAN.BridgeName, filepath.Join(m.opts.StateDir, "dnsmasq.leases"))
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
	host := ""
	if c.WiFi.Enable {
		for _, a := range c.WiFi.APs {
			if !a.Enable {
				continue
			}
			mode := "g"
			if a.Band == "5g" {
				mode = "a"
			}
			isolate := 0
			if a.Isolate {
				isolate = 1
			}
			host = "ctrl_interface=" + filepath.Join(m.base(), "hostapd-control") + "\n" + fmt.Sprintf("interface=%s\nbridge=%s\ndriver=nl80211\ncountry_code=%s\nssid2=%s\nhw_mode=%s\nchannel=%d\nwpa=2\nwpa_key_mgmt=WPA-PSK\nrsn_pairwise=CCMP\nauth_algs=1\nwpa_passphrase=%s\nap_isolate=%d\n", m.opts.WiFiInterface, c.LAN.BridgeName, m.opts.CountryCode, hex.EncodeToString([]byte(a.SSID)), mode, a.Channel, a.PSK, isolate)
		}
	}
	wan := ""
	if c.WAN.Mode == "dhcp" {
		wan = "WAN_INTERFACE='" + c.WAN.Interface + "'\n"
	}
	// add+flush+redefine runs as a single nft transaction and never touches foreign tables.
	nft := "add table inet nixos_router\nflush table inet nixos_router\ntable inet nixos_router {\n"
	managementPorts := []string{fmt.Sprint(m.opts.ManagementPort)}
	for _, port := range m.opts.SSHPorts {
		if port != m.opts.ManagementPort {
			managementPorts = append(managementPorts, fmt.Sprint(port))
		}
	}
	nft += fmt.Sprintf(" chain input { type filter hook input priority 0; policy accept; iifname != { \"lo\", \"%s\" } tcp dport { %s } drop;", c.LAN.BridgeName, strings.Join(managementPorts, ", "))
	if c.Firewall.Enable {
		nft += fmt.Sprintf(" iifname \"%s\" ct state established,related accept; iifname \"%s\" udp sport 67 udp dport 68 accept; iifname \"%s\" drop;", c.WAN.Interface, c.WAN.Interface, c.WAN.Interface)
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
	nft += fmt.Sprintf(" ct state established,related accept\n iifname \"%s\" oifname \"%s\" accept\n", c.LAN.BridgeName, c.WAN.Interface)
	for _, p := range c.Firewall.PortForwards {
		nft += fmt.Sprintf(" iifname \"%s\" oifname \"%s\" ct status dnat ip daddr %s %s dport %d accept\n", c.WAN.Interface, c.LAN.BridgeName, p.DestIP, p.Protocol, p.DestPort)
	}
	nft += " }\n chain prerouting { type nat hook prerouting priority dstnat; policy accept;\n"
	for _, p := range c.Firewall.PortForwards {
		ports := fmt.Sprint(p.ExternalPort)
		if p.ExternalPortEnd > p.ExternalPort {
			ports += fmt.Sprintf("-%d", p.ExternalPortEnd)
		}
		nft += fmt.Sprintf(" iifname \"%s\" %s dport %s dnat ip to %s:%d\n", c.WAN.Interface, p.Protocol, ports, p.DestIP, p.DestPort)
	}
	nft += " }\n chain postrouting { type nat hook postrouting priority srcnat; policy accept;\n"
	if c.Firewall.NATEnabled {
		_, subnet, _ := net.ParseCIDR(c.LAN.IPv4CIDR)
		nft += fmt.Sprintf(" ip saddr %s oifname \"%s\" masquerade\n", subnet, c.WAN.Interface)
	}
	nft += " }\n}\n"
	return map[string]string{"dnsmasq.conf": dns, "hostapd.conf": host, "wan.env": wan, "firewall.nft": nft}
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
