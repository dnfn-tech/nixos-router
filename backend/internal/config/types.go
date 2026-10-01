package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultStateDir  = "/var/lib/nixos-router"
	DefaultConfigBasename = "config.json"
)

// Root configuration describing the intended network state.
type Config struct {
	System   SystemConfig            `json:"system"`
	WAN      WANConfig               `json:"wan"`
	LAN      LANConfig               `json:"lan"`
	DNS      DNSConfig               `json:"dns"`
	WiFi     WiFiConfig              `json:"wifi"`
	Firewall FirewallConfig          `json:"firewall"`
	SSH      SSHConfig               `json:"ssh"`
	Plugins  map[string]PluginConfig `json:"plugins,omitempty"`
}

type SystemConfig struct {
	Hostname string `json:"hostname"`
	Timezone string `json:"timezone,omitempty"`
}

// WAN: support dhcp|static|pppoe (stubs for now).
type WANConfig struct {
	Interface string       `json:"interface,omitempty"`
	Mode      string       `json:"mode"` // "dhcp" | "static" | "pppoe"
	Static    *StaticIP    `json:"static,omitempty"`
	PPPoE     *PPPoEConfig `json:"pppoe,omitempty"`
}

type StaticIP struct {
	AddressCIDR string   `json:"addressCidr"` // e.g. "203.0.113.10/24"
	Gateway     string   `json:"gateway,omitempty"`
	DNS         []string `json:"dns,omitempty"`
}

type PPPoEConfig struct {
	Username string `json:"username"`
	Password string `json:"password"` // redacted in API responses
}

// LAN: br-lan bridge with IPv4 CIDR and DHCP pool (stub).
type LANConfig struct {
	BridgeName string       `json:"bridgeName"` // e.g. "br-lan"
	IPv4CIDR   string       `json:"ipv4Cidr"`   // e.g. "192.168.1.1/24"
	DHCP       DHCPv4Config `json:"dhcp"`
}

type DHCPv4Config struct {
	Enable     bool   `json:"enable"`
	RangeStart string `json:"rangeStart,omitempty"` // e.g. "192.168.1.100"
	RangeEnd   string `json:"rangeEnd,omitempty"`   // e.g. "192.168.1.200"
	LeaseMins  int    `json:"leaseMins,omitempty"`  // e.g. 1440
}

type DNSConfig struct {
	EnableDNSMasq bool     `json:"enableDnsmasq"`
	Upstreams     []string `json:"upstreams,omitempty"` // e.g. ["1.1.1.1","8.8.8.8"]
	Domain        string   `json:"domain,omitempty"`
}

type WiFiConfig struct {
	Enable      bool     `json:"enable"`
	BridgeToLan bool     `json:"bridgeToLan"`
	APs         []WiFiAP `json:"aps,omitempty"`
}

type WiFiAP struct {
	SSID    string `json:"ssid"`
	Band    string `json:"band,omitempty"`    // "2g" | "5g" | "6g" (stub)
	Channel int    `json:"channel,omitempty"` // stub
	PSK     string `json:"psk,omitempty"`     // redacted
	Enable  bool   `json:"enable"`
}

type FirewallConfig struct {
	Enable      bool   `json:"enable"`
	NATEnabled  bool   `json:"natEnabled"`
	Description string `json:"description,omitempty"` // stub placeholder
	// Future: rules, zones, forwards...
}

type SSHConfig struct {
	Enable         bool     `json:"enable"`
	Port           int      `json:"port,omitempty"`
	PasswordAuth   bool     `json:"passwordAuth,omitempty"`
	AuthorizedKeys []string `json:"authorizedKeys,omitempty"`
}

type PluginConfig struct {
	Enable bool                   `json:"enable"`
	Config map[string]interface{} `json:"config,omitempty"`
}

// DefaultConfig returns a minimal, safe default config for first-time setup.
func DefaultConfig() Config {
	return Config{
		System: SystemConfig{
			Hostname: "nixos-router",
			Timezone: "UTC",
		},
		WAN: WANConfig{
			Mode: "dhcp",
		},
		LAN: LANConfig{
			BridgeName: "br-lan",
			IPv4CIDR:   "192.168.1.1/24",
			DHCP: DHCPv4Config{
				Enable:     true,
				RangeStart: "192.168.1.100",
				RangeEnd:   "192.168.1.200",
				LeaseMins:  1440,
			},
		},
		DNS: DNSConfig{
			EnableDNSMasq: true,
			Upstreams:     []string{"1.1.1.1", "8.8.8.8"},
			Domain:        "lan",
		},
		WiFi: WiFiConfig{
			Enable:      false,
			BridgeToLan: true,
			APs:         []WiFiAP{},
		},
		Firewall: FirewallConfig{
			Enable:     true,
			NATEnabled: true,
		},
		SSH: SSHConfig{
			Enable:       true,
			Port:         22,
			PasswordAuth: false,
		},
		Plugins: map[string]PluginConfig{},
	}
}

// Validate returns an error describing the first invalid setting encountered.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.System.Hostname) == "" {
		return errors.New("system.hostname must not be empty")
	}
	switch c.WAN.Mode {
	case "dhcp":
	case "static":
		if c.WAN.Static == nil {
			return errors.New("wan.static must be set when wan.mode is 'static'")
		}
		if _, _, err := net.ParseCIDR(c.WAN.Static.AddressCIDR); err != nil {
			return fmt.Errorf("wan.static.addressCidr invalid: %w", err)
		}
		if c.WAN.Static.Gateway != "" && net.ParseIP(c.WAN.Static.Gateway) == nil {
			return errors.New("wan.static.gateway invalid IP")
		}
		for _, ip := range c.WAN.Static.DNS {
			if net.ParseIP(ip) == nil {
				return fmt.Errorf("wan.static.dns contains invalid IP: %s", ip)
			}
		}
	case "pppoe":
		if c.WAN.PPPoE == nil {
			return errors.New("wan.pppoe must be set when wan.mode is 'pppoe'")
		}
		if c.WAN.PPPoE.Username == "" {
			return errors.New("wan.pppoe.username must not be empty")
		}
		if c.WAN.PPPoE.Password == "" {
			return errors.New("wan.pppoe.password must not be empty")
		}
	default:
		return errors.New("wan.mode must be one of: dhcp, static, pppoe")
	}

	if _, _, err := net.ParseCIDR(c.LAN.IPv4CIDR); err != nil {
		return fmt.Errorf("lan.ipv4Cidr invalid: %w", err)
	}
	if c.LAN.DHCP.Enable {
		if net.ParseIP(c.LAN.DHCP.RangeStart) == nil {
			return errors.New("lan.dhcp.rangeStart invalid IP")
		}
		if net.ParseIP(c.LAN.DHCP.RangeEnd) == nil {
			return errors.New("lan.dhcp.rangeEnd invalid IP")
		}
		if c.LAN.DHCP.LeaseMins < 1 {
			return errors.New("lan.dhcp.leaseMins must be >= 1")
		}
	}

	for _, u := range c.DNS.Upstreams {
		if net.ParseIP(u) == nil {
			return fmt.Errorf("dns.upstreams contains invalid IP: %s", u)
		}
	}

	// Basic WiFi checks (more in later milestones)
	for _, ap := range c.WiFi.APs {
		if ap.SSID == "" {
			return errors.New("wifi.aps[].ssid must not be empty")
		}
	}

	// SSH port sanity
	if c.SSH.Port < 0 || c.SSH.Port > 65535 {
		return errors.New("ssh.port must be 0-65535")
	}
	return nil
}

// RedactedCopy returns a deep-copied config with secrets masked for safe API output.
func (c *Config) RedactedCopy() Config {
	clone := *c
	if clone.WAN.PPPoE != nil && clone.WAN.PPPoE.Password != "" {
		pp := *clone.WAN.PPPoE
		pp.Password = "****"
		clone.WAN.PPPoE = &pp
	}
	if len(clone.WiFi.APs) > 0 {
		aps := make([]WiFiAP, len(clone.WiFi.APs))
		copy(aps, clone.WiFi.APs)
		for i := range aps {
			if aps[i].PSK != "" {
				aps[i].PSK = "****"
			}
		}
		clone.WiFi.APs = aps
	}
	return clone
}

// LoadFromFile reads JSON configuration from the given path, validates it, and returns it.
func LoadFromFile(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// SaveToFile atomically writes the given config as JSON to the provided path.
func SaveToFile(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}

