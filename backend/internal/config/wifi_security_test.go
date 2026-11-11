package config

import (
	"strings"
	"testing"
)

func TestWiFiSecurityValidation(t *testing.T) {
	for _, security := range []string{"", "wpa2-psk", "wpa3-sae", "open", "SAE", "wpa3-sae\nwpa=0"} {
		c := DefaultConfig()
		c.WiFi.APs = []WiFiAP{{Enable: true, SSID: "test", PSK: "private-password", Security: security}}
		err := c.Validate()
		valid := security == "" || security == "wpa2-psk" || security == "wpa3-sae"
		if (err == nil) != valid {
			t.Fatalf("security %q error=%v", security, err)
		}
		if err != nil && (!strings.Contains(err.Error(), "wifi.aps.0.security") || strings.Contains(err.Error(), "private-password")) {
			t.Fatal(err)
		}
	}
}
