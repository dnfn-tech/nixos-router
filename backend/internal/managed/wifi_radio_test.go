package managed

import (
	"context"
	"fmt"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"strings"
	"testing"
)

func TestAdvancedWiFiFailsClosedWithoutCapabilityObservation(t *testing.T) {
	m, r, c := fixture(t)
	c.WiFi.Enable = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "radio", PSK: "private-password", Band: "5g", Channel: 149, Security: "wpa3-sae", Standard: "wifi7", ChannelWidthMHz: 80}}
	if _, err := m.Apply(context.Background(), c); err == nil || !strings.Contains(err.Error(), "capability preflight") {
		t.Fatalf("missing observation accepted: %v", err)
	}
	for _, call := range r.calls {
		if strings.Contains(call, " start ") || strings.Contains(call, " stop ") || strings.Contains(call, " addr add ") || strings.Contains(call, "link set") {
			t.Fatal(call)
		}
	}
}

type radioRunner struct {
	*recorder
	restricted bool
}

func (r *radioRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name != "iw" {
		return r.recorder.Run(ctx, name, args...)
	}
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	switch strings.Join(args, " ") {
	case "dev wlan0 info":
		return "Interface wlan0\n wiphy 0\n", nil
	case "phy phy0 info":
		return `Wiphy phy0
 Supported interface modes:
  * AP
 Band 2:
  HT20/HT40
  VHT Capabilities (0x0):
  HE Iftypes: AP
  HE PHY Capabilities: (0x04):
   HE40/HE80/5GHz
  EHT Iftypes: AP
  EHT PHY Capabilities (0x01):
  Frequencies:
   * 5745 MHz [149] (20.0 dBm)
`, nil
	case "phy phy0 channels":
		out := "Band 2:\n"
		for _, ch := range []int{149, 153, 157, 161} {
			out += fmt.Sprintf(" * %d MHz [%d]\n  Channel widths: 20MHz HT40+ HT40- VHT80\n", 5000+5*ch, ch)
			if ch == 157 && r.restricted {
				out += "  No IR\n"
			}
		}
		return out, nil
	}
	return "", fmt.Errorf("unexpected iw query")
}
func TestAdvancedWiFiPreflightApply(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		m, rec, c := fixture(t)
		r := &radioRunner{recorder: rec, restricted: restricted}
		m.opts.Runner = r
		c.WiFi.Enable = true
		c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "radio", PSK: "private-password", Band: "5g", Channel: 149, Security: "wpa3-sae", Standard: "wifi7", ChannelWidthMHz: 80}}
		_, err := m.Apply(context.Background(), c)
		if restricted {
			if err == nil || !strings.Contains(err.Error(), "channel 157") {
				t.Fatalf("restricted channel accepted: %v", err)
			}
			for _, call := range r.calls {
				if strings.Contains(call, "link set") || strings.Contains(call, "systemctl start") {
					t.Fatal(call)
				}
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWiFiRadioHostapdRendering(t *testing.T) {
	for _, standard := range []string{"wifi4", "wifi5", "wifi6", "wifi7"} {
		t.Run(standard, func(t *testing.T) {
			m, _, c := fixture(t)
			width := 80
			if standard == "wifi4" {
				width = 40
			}
			c.WiFi.Enable = true
			c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "radio", PSK: "private-password", Band: "5g", Channel: 149, Security: "wpa3-sae", Standard: standard, ChannelWidthMHz: width}}
			h := m.render(c)["hostapd.conf"]
			for _, line := range []string{"wmm_enabled=1\n", "ieee80211n=1\n", "ht_capab=[HT40+]\n"} {
				if !strings.Contains(h, line) {
					t.Fatalf("missing %q", line)
				}
			}
			for _, flag := range []struct {
				line string
				want bool
			}{
				{"ieee80211ac=1\n", standard != "wifi4"},
				{"ieee80211ax=1\n", standard == "wifi6" || standard == "wifi7"},
				{"ieee80211be=1\n", standard == "wifi7"},
			} {
				if strings.Contains(h, flag.line) != flag.want {
					t.Fatalf("%s flag %s", standard, flag.line)
				}
			}
			if width == 80 {
				for _, prefix := range []string{"vht", "he", "eht"} {
					enabled := prefix == "vht" || prefix == "he" && (standard == "wifi6" || standard == "wifi7") || prefix == "eht" && standard == "wifi7"
					for _, line := range []string{prefix + "_oper_chwidth=1\n", prefix + "_oper_centr_freq_seg0_idx=155\n"} {
						if strings.Contains(h, line) != enabled {
							t.Fatalf("%s line %q", standard, line)
						}
					}
				}
			}
		})
	}
	m, _, c := fixture(t)
	c.WiFi.Enable = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "legacy", PSK: "private-password", Band: "2g", Channel: 6}}
	if h := m.render(c)["hostapd.conf"]; strings.Contains(h, "ieee80211n=") || strings.Contains(h, "oper_chwidth") {
		t.Fatal("legacy radio changed")
	}
}
