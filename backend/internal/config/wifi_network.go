package config

import (
	"bytes"
	"fmt"
	"net"
	"strings"
)

// WiFiNetwork describes the independent network on the allowlisted radio.
// It is inactive while WiFi is disabled or bridged to the wired LAN.
type WiFiNetwork struct {
	IPv4CIDR         string        `json:"ipv4Cidr"`
	DHCP             DHCPv4Config  `json:"dhcp"`
	StaticLeases     []StaticLease `json:"staticLeases,omitempty"`
	AllowManagement  bool          `json:"allowManagement"`
	AllowWiredAccess bool          `json:"allowWiredAccess"`
}

func (c *Config) validateWiFiNetwork() error {
	if !c.WiFi.Enable || c.WiFi.BridgeToLan {
		return nil
	}
	w := c.WiFi.Network
	if w == nil {
		return invalidField("wifi.network", "required for independent WiFi")
	}
	ip, n, err := net.ParseCIDR(w.IPv4CIDR)
	if err != nil || ip.To4() == nil || strings.Contains(w.IPv4CIDR, ":") {
		return invalidField("wifi.network.ipv4Cidr", "must be an IPv4 host CIDR")
	}
	ones, _ := n.Mask.Size()
	broadcast := append(net.IP(nil), n.IP.To4()...)
	for i := range broadcast {
		broadcast[i] |= ^n.Mask[i]
	}
	if ones < 1 || ones > 30 || ip.Equal(n.IP) || ip.Equal(broadcast) {
		return invalidField("wifi.network.ipv4Cidr", "must be a usable gateway in a subnet of at least four addresses")
	}
	overlaps := func(cidr string) bool {
		_, other, err := net.ParseCIDR(cidr)
		return err == nil && other.IP.To4() != nil && (other.Contains(n.IP) || n.Contains(other.IP))
	}
	if overlaps(c.LAN.IPv4CIDR) || (c.WAN.Mode == "static" && c.WAN.Static != nil && overlaps(c.WAN.Static.AddressCIDR)) {
		return invalidField("wifi.network.ipv4Cidr", "must not overlap wired LAN or static WAN")
	}
	usable := func(value string) bool {
		p := net.ParseIP(value)
		return p != nil && p.To4() != nil && !strings.Contains(value, ":") && n.Contains(p) && !p.Equal(ip) && !p.Equal(n.IP) && !p.Equal(broadcast)
	}
	if w.DHCP.Enable {
		if !usable(w.DHCP.RangeStart) {
			return invalidField("wifi.network.dhcp.rangeStart", "must be a usable client address in the WiFi subnet")
		}
		if !usable(w.DHCP.RangeEnd) || bytes.Compare(net.ParseIP(w.DHCP.RangeStart).To4(), net.ParseIP(w.DHCP.RangeEnd).To4()) > 0 {
			return invalidField("wifi.network.dhcp.rangeEnd", "must end an ordered usable client range in the WiFi subnet")
		}
		if w.DHCP.LeaseMins < 1 {
			return invalidField("wifi.network.dhcp.leaseMins", "must be at least 1")
		}
	}
	macs, ips := map[string]bool{}, map[string]bool{}
	for i, lease := range w.StaticLeases {
		path := fmt.Sprintf("wifi.network.staticLeases.%d.", i)
		mac, err := net.ParseMAC(lease.MAC)
		if err != nil || len(mac) != 6 || mac[0]&1 != 0 || macs[mac.String()] {
			return invalidField(path+"mac", "must be a unique unicast Ethernet MAC")
		}
		macs[mac.String()] = true
		if !usable(lease.IP) || ips[net.ParseIP(lease.IP).String()] {
			return invalidField(path+"ip", "must be a unique usable address in the WiFi subnet")
		}
		ips[net.ParseIP(lease.IP).String()] = true
		if lease.Hostname != "" && !validWiFiLeaseHostname(lease.Hostname) {
			return invalidField(path+"hostname", "must be a valid hostname")
		}
	}
	return nil
}

func validWiFiLeaseHostname(name string) bool {
	if len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
