package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigValidate(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default config should be valid: %v", err)
	}
}

func TestValidateWANStaticInvalid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Mode = "static"
	cfg.WAN.Static = &StaticIP{
		AddressCIDR: "not-a-cidr",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for invalid static wan cidr")
	}
}

func TestValidateStaticLeasesAndPortForward(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LAN.StaticLeases = []StaticLease{
		{MAC: "AA:BB:CC:DD:EE:FF", IP: "192.168.1.10"},
	}
	cfg.Firewall.PortForwards = []PortForward{
		{Protocol: "tcp", ExternalPort: 8080, DestIP: "192.168.1.10", DestPort: 80},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validate error: %v", err)
	}
	// bad MAC
	cfg.LAN.StaticLeases[0].MAC = "bad-mac"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected bad mac error")
	}
}

func TestRedactedCopy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Mode = "pppoe"
	cfg.WAN.PPPoE = &PPPoEConfig{Username: "u", Password: "secret"}
	cfg.WiFi.Enable = true
	cfg.WiFi.APs = []WiFiAP{{SSID: "a", PSK: "p", Enable: true}}
	cfg.DDNS.Enable = true
	cfg.DDNS.Provider = "cloudflare"
	cfg.DDNS.Cloudflare = &CloudflareDDNS{APIToken: "tok", Zone: "z", Record: "r"}
	red := cfg.RedactedCopy()
	if red.WAN.PPPoE.Password == "secret" {
		t.Fatalf("wan.pppoe.password should be redacted")
	}
	if red.WiFi.APs[0].PSK == "p" {
		t.Fatalf("wifi aps psk should be redacted")
	}
	if red.DDNS.Cloudflare.APIToken == "tok" {
		t.Fatalf("ddns token should be redacted")
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	orig := DefaultConfig()
	orig.System.Hostname = "testnode"
	if err := SaveToFile(path, orig); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.System.Hostname != "testnode" {
		t.Fatalf("round-trip mismatch: %v", loaded.System.Hostname)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		t.Fatalf("file not written properly")
	}
}

