package config

import (
	"errors"
	"strings"
	"testing"
)

func TestValidationErrorPaths(t *testing.T) {
	const secret = "private-input-value"
	cases := []struct {
		name, path string
		edit       func(*Config)
	}{
		{"schema version", "schemaVersion", func(c *Config) { c.SchemaVersion = 99 }},
		{"revision", "revision", func(c *Config) { c.SchemaVersion = 1; c.Revision = secret }},
		{"hostname", "system.hostname", func(c *Config) { c.System.Hostname = "" }},
		{"static wan", "wan.static.addressCidr", func(c *Config) { c.WAN.Mode = "static"; c.WAN.Static = &StaticIP{AddressCIDR: secret} }},
		{"second lease", "lan.staticLeases.1.ip", func(c *Config) {
			c.LAN.StaticLeases = []StaticLease{{MAC: "00:11:22:33:44:55", IP: "192.168.1.2"}, {MAC: "00:11:22:33:44:66", IP: secret}}
		}},
		{"second ap", "wifi.aps.1.psk", func(c *Config) {
			c.WiFi.APs = []WiFiAP{{SSID: "first", PSK: "valid-password"}, {SSID: "second", PSK: "short"}}
		}},
		{"forward port", "firewall.portForwards.0.externalPort", func(c *Config) {
			c.Firewall.PortForwards = []PortForward{{Protocol: "tcp", ExternalPort: 70000, DestIP: "192.168.1.2", DestPort: 80}}
		}},
		{"ddns secret", "ddns.cloudflare.apiToken", func(c *Config) {
			c.DDNS = DDNSConfig{Enable: true, Provider: "cloudflare", Cloudflare: &CloudflareDDNS{Zone: "zone", Record: "record"}}
		}},
		{"dns", "dns.upstreams.1", func(c *Config) { c.DNS.Upstreams = []string{"1.1.1.1", secret} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.edit(&c)
			err := c.Validate()
			var issue *ValidationError
			if !errors.As(err, &issue) {
				t.Fatalf("expected typed validation error, got %T: %v", err, err)
			}
			if issue.Path != tc.path || issue.Code != "invalid_field" || issue.Message == "" {
				t.Fatalf("invalid issue: %+v", issue)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "short") {
				t.Fatalf("input leaked: %s", err)
			}
		})
	}
}
