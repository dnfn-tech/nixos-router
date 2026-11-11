package managed

import (
	"strings"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestWiFiSecurityProfiles(t *testing.T) {
	for _, profile := range []string{"", "wpa2-psk", "wpa3-sae"} {
		t.Run(profile, func(t *testing.T) {
			m, _, c := fixture(t)
			c.WiFi.Enable = true
			c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "SAE test", PSK: "private-password", Band: "2g", Channel: 6, Security: profile}}
			if err := m.validate(c); err != nil {
				t.Fatal(err)
			}
			host := m.render(c)["hostapd.conf"]
			if profile == "wpa3-sae" {
				for _, want := range []string{"wpa_key_mgmt=SAE\n", "ieee80211w=2\n", "wpa_passphrase=private-password\n"} {
					if !strings.Contains(host, want) {
						t.Fatalf("missing %q", want)
					}
				}
				if strings.Contains(host, "WPA-PSK") || strings.Contains(host, "sae_password=") {
					t.Fatal("SAE downgraded")
				}
			} else if !strings.Contains(host, "wpa_key_mgmt=WPA-PSK\n") || !strings.Contains(host, "wpa_passphrase=private-password\n") {
				t.Fatal("legacy WPA2 changed")
			}
		})
	}
	m, r, c := fixture(t)
	c.WiFi.Enable = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "test", PSK: "private-password", Band: "2g", Channel: 6, Security: "open"}}
	if err := m.validate(c); err == nil {
		t.Fatal("unknown security accepted")
	}
	if len(r.calls) != 0 {
		t.Fatal("invalid security ran commands")
	}
}
