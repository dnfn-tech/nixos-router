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
	DDNS     DDNSConfig              `json:"ddns"`
	QoS      QoSConfig               `json:"qos"`
	Parental ParentalConfig          `json:"parental"`
	IPv6     IPv6Config              `json:"ipv6"`
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
	Ports      []string     `json:"ports,omitempty"` // physical ports in br-lan (optional)
	StaticLeases []StaticLease `json:"staticLeases,omitempty"`
}

type DHCPv4Config struct {
	Enable     bool   `json:"enable"`
	RangeStart string `json:"rangeStart,omitempty"` // e.g. "192.168.1.100"
	RangeEnd   string `json:"rangeEnd,omitempty"`   // e.g. "192.168.1.200"
	LeaseMins  int    `json:"leaseMins,omitempty"`  // e.g. 1440
}

type StaticLease struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname,omitempty"`
	Comment  string `json:"comment,omitempty"`
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
	Guest   bool   `json:"guest,omitempty"`
	Isolate bool   `json:"isolate,omitempty"`
}

type FirewallConfig struct {
	Enable      bool   `json:"enable"`
	NATEnabled  bool   `json:"natEnabled"`
	UPnPEnable  bool   `json:"upnpEnable,omitempty"`
	Description string `json:"description,omitempty"`
	PortForwards []PortForward `json:"portForwards,omitempty"`
	BlockedMACs []string `json:"blockedMacs,omitempty"`
	// Future: lanServices...
}

type PortForward struct {
	Protocol string `json:"protocol"`           // "tcp" | "udp"
	ExternalPort int `json:"externalPort"`      // 1-65535 (single port)
	ExternalPortEnd int `json:"externalPortEnd,omitempty"` // optional range end
	DestIP    string `json:"destIp"`           // internal dest IP
	DestPort  int    `json:"destPort"`         // 1-65535
	Description string `json:"description,omitempty"`
}

type SSHConfig struct {
	Enable         bool     `json:"enable"`
	Port           int      `json:"port,omitempty"`
	PasswordAuth   bool     `json:"passwordAuth,omitempty"`
	AuthorizedKeys []string `json:"authorizedKeys,omitempty"`
}

// DDNS providers (stubs)
type DDNSConfig struct {
	Enable   bool   `json:"enable"`
	Provider string `json:"provider,omitempty"` // "cloudflare" | "duckdns" | "aliyun" | "custom"
	Cloudflare *CloudflareDDNS `json:"cloudflare,omitempty"`
	DuckDNS    *DuckDNSConfig  `json:"duckdns,omitempty"`
	Aliyun     *AliyunDDNS     `json:"aliyun,omitempty"`
	Custom     *CustomDDNS     `json:"custom,omitempty"`
}

type CloudflareDDNS struct {
	APIToken string `json:"apiToken"` // secret
	Zone     string `json:"zone"`
	Record   string `json:"record"`
}

type DuckDNSConfig struct {
	Token string `json:"token"` // secret
	Domain string `json:"domain"`
}

type AliyunDDNS struct {
	AccessKey string `json:"accessKey"` // secret
	SecretKey string `json:"secretKey"` // secret
	Domain    string `json:"domain"`
	Record    string `json:"record"`
}

type CustomDDNS struct {
	URL   string `json:"url"`
	Token string `json:"token,omitempty"` // optional secret
}

type PluginConfig struct {
	Enable bool                   `json:"enable"`
	Config map[string]interface{} `json:"config,omitempty"`
}

// QoS: global bandwidth and per-device stubs
type QoSConfig struct {
	Enable       bool                 `json:"enable"`
	UpMbps       int                  `json:"upMbps,omitempty"`
	DownMbps     int                  `json:"downMbps,omitempty"`
	DevicePolicy []QoSDevicePolicy    `json:"devicePolicy,omitempty"`
}
type QoSDevicePolicy struct {
	MAC      string `json:"mac,omitempty"`
	IP       string `json:"ip,omitempty"`
	Priority string `json:"priority,omitempty"` // "low"|"normal"|"high"
	LimitKbps int   `json:"limitKbps,omitempty"`
}

// Parental control: device/group schedule stubs
type ParentalConfig struct {
	Enable bool            `json:"enable"`
	Rules  []ParentalRule  `json:"rules,omitempty"`
}
type ParentalRule struct {
	TargetMACs []string `json:"targetMacs,omitempty"`
	Group      string   `json:"group,omitempty"`
	Schedule   string   `json:"schedule,omitempty"` // stub like "Mon-Fri 22:00-07:00"
	Action     string   `json:"action"`             // "block"|"allow"
}

// IPv6: WAN mode and LAN PD/RA stubs
type IPv6Config struct {
	Enable bool   `json:"enable"`
	WANMode string `json:"wanMode,omitempty"` // "dhcpv6"|"slaac"|"pppoe6"|"disabled"
	LANPD   bool   `json:"lanPrefixDelegation,omitempty"`
	LANRA   bool   `json:"lanRouterAdvertisement,omitempty"`
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
		DDNS: DDNSConfig{
			Enable: false,
		},
		QoS: QoSConfig{
			Enable:   false,
			UpMbps:   0,
			DownMbps: 0,
		},
		Parental: ParentalConfig{
			Enable: false,
			Rules:  []ParentalRule{},
		},
		IPv6: IPv6Config{
			Enable: false,
			WANMode: "disabled",
			LANPD:  false,
			LANRA:  false,
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
	// Static leases
	if len(c.LAN.StaticLeases) > 0 {
		_, lanNet, _ := net.ParseCIDR(c.LAN.IPv4CIDR)
		for _, sl := range c.LAN.StaticLeases {
			if _, err := net.ParseMAC(sl.MAC); err != nil {
				return fmt.Errorf("lan.staticLeases mac invalid: %s", sl.MAC)
			}
			ip := net.ParseIP(sl.IP)
			if ip == nil {
				return fmt.Errorf("lan.staticLeases ip invalid: %s", sl.IP)
			}
			if lanNet != nil && !lanNet.Contains(ip) {
				return fmt.Errorf("lan.staticLeases ip %s not in %s", sl.IP, c.LAN.IPv4CIDR)
			}
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
		if ap.PSK != "" && len(ap.PSK) < 8 {
			return errors.New("wifi.aps[].psk must be at least 8 characters if set")
		}
		switch ap.Band {
		case "", "2g", "5g", "6g":
		default:
			return fmt.Errorf("wifi.aps[].band invalid: %s", ap.Band)
		}
	}

	// SSH port sanity
	if c.SSH.Port < 0 || c.SSH.Port > 65535 {
		return errors.New("ssh.port must be 0-65535")
	}
	// QoS sanity
	for _, dp := range c.QoS.DevicePolicy {
		if dp.MAC == "" && dp.IP == "" {
			return errors.New("qos.devicePolicy[] requires mac or ip")
		}
		if dp.MAC != "" {
			if _, err := net.ParseMAC(dp.MAC); err != nil {
				return fmt.Errorf("qos.devicePolicy mac invalid: %s", dp.MAC)
			}
		}
		if dp.IP != "" && net.ParseIP(dp.IP) == nil {
			return fmt.Errorf("qos.devicePolicy ip invalid: %s", dp.IP)
		}
		if dp.LimitKbps < 0 {
			return errors.New("qos.devicePolicy.limitKbps must be >=0")
		}
	}
	// IPv6 sanity
	switch c.IPv6.WANMode {
	case "dhcpv6", "slaac", "pppoe6", "disabled", "":
	default:
		return errors.New("ipv6.wanMode must be one of: dhcpv6, slaac, pppoe6, disabled")
	}
	// Firewall port forwards
	for _, pf := range c.Firewall.PortForwards {
		if pf.Protocol != "tcp" && pf.Protocol != "udp" {
			return fmt.Errorf("firewall.portForwards[].protocol invalid: %s", pf.Protocol)
		}
		if pf.ExternalPort < 1 || pf.ExternalPort > 65535 {
			return errors.New("firewall.portForwards[].externalPort 1-65535")
		}
		if pf.ExternalPortEnd != 0 {
			if pf.ExternalPortEnd < pf.ExternalPort || pf.ExternalPortEnd > 65535 {
				return errors.New("firewall.portForwards[].externalPortEnd invalid")
			}
		}
		if net.ParseIP(pf.DestIP) == nil {
			return fmt.Errorf("firewall.portForwards[].destIp invalid: %s", pf.DestIP)
		}
		if pf.DestPort < 1 || pf.DestPort > 65535 {
			return errors.New("firewall.portForwards[].destPort 1-65535")
		}
	}
	// Firewall blocked MACs
	for _, m := range c.Firewall.BlockedMACs {
		if _, err := net.ParseMAC(m); err != nil {
			return fmt.Errorf("firewall.blockedMacs invalid: %s", m)
		}
	}
	// DDNS sanity
	if c.DDNS.Enable {
		switch c.DDNS.Provider {
		case "cloudflare":
			if c.DDNS.Cloudflare == nil || c.DDNS.Cloudflare.APIToken == "" || c.DDNS.Cloudflare.Zone == "" || c.DDNS.Cloudflare.Record == "" {
				return errors.New("ddns.cloudflare requires apiToken, zone, record")
			}
		case "duckdns":
			if c.DDNS.DuckDNS == nil || c.DDNS.DuckDNS.Token == "" || c.DDNS.DuckDNS.Domain == "" {
				return errors.New("ddns.duckdns requires token, domain")
			}
		case "aliyun":
			if c.DDNS.Aliyun == nil || c.DDNS.Aliyun.AccessKey == "" || c.DDNS.Aliyun.SecretKey == "" || c.DDNS.Aliyun.Domain == "" || c.DDNS.Aliyun.Record == "" {
				return errors.New("ddns.aliyun requires accessKey, secretKey, domain, record")
			}
		case "custom":
			if c.DDNS.Custom == nil || c.DDNS.Custom.URL == "" {
				return errors.New("ddns.custom requires url")
			}
		default:
			return errors.New("ddns.provider must be one of: cloudflare, duckdns, aliyun, custom")
		}
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
	// DDNS redact
	if clone.DDNS.Cloudflare != nil && clone.DDNS.Cloudflare.APIToken != "" {
		cc := *clone.DDNS.Cloudflare
		cc.APIToken = "****"
		clone.DDNS.Cloudflare = &cc
	}
	if clone.DDNS.DuckDNS != nil && clone.DDNS.DuckDNS.Token != "" {
		dd := *clone.DDNS.DuckDNS
		dd.Token = "****"
		clone.DDNS.DuckDNS = &dd
	}
	if clone.DDNS.Aliyun != nil {
		al := *clone.DDNS.Aliyun
		if al.AccessKey != "" {
			al.AccessKey = "****"
		}
		if al.SecretKey != "" {
			al.SecretKey = "****"
		}
		clone.DDNS.Aliyun = &al
	}
	if clone.DDNS.Custom != nil && clone.DDNS.Custom.Token != "" {
		cu := *clone.DDNS.Custom
		cu.Token = "****"
		clone.DDNS.Custom = &cu
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

