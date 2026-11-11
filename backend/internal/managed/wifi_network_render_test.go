package managed

import (
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"strings"
	"testing"
)

func TestRoutedWiFiDHCPAndHostapd(t *testing.T) {
	m, _, c := wifiIPv4Fixture(t)
	c.WiFi.APs = nil
	files := m.render(c)
	dns := files["dnsmasq.conf"]
	for _, want := range []string{"interface=br-lan\n", "interface=wlan0\n", "dhcp-range=set:wired,192.168.1.100,192.168.1.200,1440m\n", "dhcp-range=set:wifi,192.168.77.100,192.168.77.110,30m\n", "dhcp-option=tag:wired,option:router,192.168.1.1\n", "dhcp-option=tag:wifi,option:router,192.168.77.1\n", "dhcp-option=tag:wifi,option:dns-server,192.168.77.1\n"} {
		if !strings.Contains(dns, want) {
			t.Fatalf("missing %q in %s", want, dns)
		}
	}
	if strings.Contains(dns, "dhcp-option=option:router,") {
		t.Fatal("untagged gateway leaks across subnets", dns)
	}
	c.WiFi.APs = wifiTestAP()
	host := m.render(c)["hostapd.conf"]
	if strings.Contains(host, "\nbridge=") || !strings.Contains(host, "interface=wlan0\n") {
		t.Fatal(host)
	}
	c.DNS.EnableDNSMasq = false
	c.DNS.Upstreams = []string{"1.1.1.1"}
	c.DNS.Domain = "home.test"
	dns = m.render(c)["dnsmasq.conf"]
	if !strings.Contains(dns, "dhcp-option=tag:wifi,option:dns-server,1.1.1.1\n") || !strings.Contains(dns, "domain=home.test\n") {
		t.Fatal(dns)
	}
	c.LAN.DHCP.Enable = false
	if !needsLANService(c) {
		t.Fatal("WiFi DHCP service omitted when wired DHCP disabled")
	}
}
func TestRoutedWiFiAccessAndNAT(t *testing.T) {
	m, _, c := wifiIPv4Fixture(t)
	for _, fw := range []bool{false, true} {
		c.Firewall.Enable = fw
		nft := m.render(c)["firewall.nft"]
		for _, want := range []string{`iifname "wlan0" oifname "wan0" accept`, `ip saddr 192.168.77.0/24 oifname "wan0" masquerade`, `iifname "wlan0" oifname "br-lan" drop`, `iifname "br-lan" oifname "wlan0" drop`} {
			if !strings.Contains(nft, want) {
				t.Fatalf("fw=%v missing %s", fw, want)
			}
		}
		if strings.Index(nft, `iifname "wlan0" oifname "br-lan" drop`) > strings.Index(nft, "ct state established,related accept\n") {
			t.Fatal("cross-subnet policy follows established accept")
		}
		if !strings.Contains(nft, `iifname != { "lo", "br-lan" } tcp dport`) {
			t.Fatal("untrusted WiFi gained management")
		}
		c.WiFi.Network.AllowManagement = true
		c.WiFi.Network.AllowWiredAccess = true
		nft = m.render(c)["firewall.nft"]
		if !strings.Contains(nft, `iifname != { "lo", "br-lan", "wlan0" } tcp dport`) {
			t.Fatal("explicit management permission missing")
		}
		for _, want := range []string{`iifname "wlan0" oifname "br-lan" accept`, `iifname "br-lan" oifname "wlan0" accept`} {
			if !strings.Contains(nft, want) {
				t.Fatal("explicit cross-subnet permission missing", want)
			}
		}
		c.WiFi.Network.AllowManagement = false
		c.WiFi.Network.AllowWiredAccess = false
	}
}

func wifiTestAP() []config.WiFiAP {
	return []config.WiFiAP{{Enable: true, SSID: "routed-test", PSK: "test-password", Band: "2g", Channel: 6}}
}
