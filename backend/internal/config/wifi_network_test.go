package config

import (
	"strings"
	"testing"
)

func routedWiFiConfig() Config {
	c := DefaultConfig()
	c.WiFi.Enable = true
	c.WiFi.BridgeToLan = false
	c.WiFi.Network = &WiFiNetwork{IPv4CIDR: "192.168.77.1/24", DHCP: DHCPv4Config{Enable: true, RangeStart: "192.168.77.100", RangeEnd: "192.168.77.110", LeaseMins: 30}}
	return c
}
func TestRoutedWiFiValidation(t *testing.T) {
	c := routedWiFiConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.WiFi.Network.AllowManagement || c.WiFi.Network.AllowWiredAccess {
		t.Fatal("access defaults must be explicit")
	}
	for _, tc := range []struct {
		name, path string
		change     func(*Config)
	}{
		{"missing", "wifi.network", func(c *Config) { c.WiFi.Network = nil }},
		{"v6", "wifi.network.ipv4Cidr", func(c *Config) { c.WiFi.Network.IPv4CIDR = "2001:db8::1/64" }},
		{"network address", "wifi.network.ipv4Cidr", func(c *Config) { c.WiFi.Network.IPv4CIDR = "192.168.77.0/24" }},
		{"broadcast", "wifi.network.ipv4Cidr", func(c *Config) { c.WiFi.Network.IPv4CIDR = "192.168.77.255/24" }},
		{"too small", "wifi.network.ipv4Cidr", func(c *Config) { c.WiFi.Network.IPv4CIDR = "192.168.77.1/31" }},
		{"wired overlap", "wifi.network.ipv4Cidr", func(c *Config) { c.WiFi.Network.IPv4CIDR = c.LAN.IPv4CIDR }},
		{"supernet overlap", "wifi.network.ipv4Cidr", func(c *Config) { c.WiFi.Network.IPv4CIDR = "192.168.0.1/16" }},
		{"WAN overlap", "wifi.network.ipv4Cidr", func(c *Config) { c.WAN.Mode = "static"; c.WAN.Static = &StaticIP{AddressCIDR: "192.168.77.254/24"} }},
		{"pool out", "wifi.network.dhcp.rangeStart", func(c *Config) { c.WiFi.Network.DHCP.RangeStart = "192.168.78.2" }},
		{"pool gateway", "wifi.network.dhcp.rangeStart", func(c *Config) { c.WiFi.Network.DHCP.RangeStart = "192.168.77.1" }},
		{"pool broadcast", "wifi.network.dhcp.rangeEnd", func(c *Config) { c.WiFi.Network.DHCP.RangeEnd = "192.168.77.255" }},
		{"pool reversed", "wifi.network.dhcp.rangeEnd", func(c *Config) { c.WiFi.Network.DHCP.RangeEnd = "192.168.77.99" }},
		{"lease zero", "wifi.network.dhcp.leaseMins", func(c *Config) { c.WiFi.Network.DHCP.LeaseMins = 0 }},
		{"static foreign", "wifi.network.staticLeases.0.ip", func(c *Config) {
			c.WiFi.Network.StaticLeases = []StaticLease{{MAC: "02:00:00:00:00:01", IP: "10.0.0.2"}}
		}},
		{"static bad mac", "wifi.network.staticLeases.0.mac", func(c *Config) { c.WiFi.Network.StaticLeases = []StaticLease{{MAC: "bad", IP: "192.168.77.2"}} }},

		{"static multicast", "wifi.network.staticLeases.0.mac", func(c *Config) {
			c.WiFi.Network.StaticLeases = []StaticLease{{MAC: "01:00:00:00:00:01", IP: "192.168.77.2"}}
		}},
		{"duplicate MAC", "wifi.network.staticLeases.1.mac", func(c *Config) {
			c.WiFi.Network.StaticLeases = []StaticLease{{MAC: "02:00:00:00:00:01", IP: "192.168.77.2"}, {MAC: "02:00:00:00:00:01", IP: "192.168.77.3"}}
		}},
		{"duplicate IP", "wifi.network.staticLeases.1.ip", func(c *Config) {
			c.WiFi.Network.StaticLeases = []StaticLease{{MAC: "02:00:00:00:00:01", IP: "192.168.77.2"}, {MAC: "02:00:00:00:00:02", IP: "192.168.77.2"}}
		}},
		{"injected hostname", "wifi.network.staticLeases.0.hostname", func(c *Config) {
			c.WiFi.Network.StaticLeases = []StaticLease{{MAC: "02:00:00:00:00:01", IP: "192.168.77.2", Hostname: "host\ninterface=foreign"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := routedWiFiConfig()
			tc.change(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("want %s, got %v", tc.path, err)
			}
		})
	}
	c.WiFi.Enable = false
	c.WiFi.Network.IPv4CIDR = "draft"
	if err := c.Validate(); err != nil {
		t.Fatal("disabled draft", err)
	}
	c.WiFi.Enable = true
	c.WiFi.BridgeToLan = true
	if err := c.Validate(); err != nil {
		t.Fatal("inactive network draft", err)
	}
}
