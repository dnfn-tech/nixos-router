package managed

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
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

func TestAutomaticWiFiUsesLegalFallbackWhenScanUnavailable(t *testing.T) {
	m, rec, c := fixture(t)
	m.opts.Runner = &radioRunner{recorder: rec}
	c.WiFi.Enable = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "radio", PSK: "private-password", Band: "5g", Standard: "wifi6", ChannelWidthMHz: 80, ChannelMode: "auto"}}
	_, err := m.Apply(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if m.wifiSelected != 149 {
		t.Fatalf("selected %d", m.wifiSelected)
	}
	status, _ := m.WiFiChannel(context.Background())
	if status.Mode != "auto" || status.Channel != 149 {
		t.Fatal(status)
	}
}

func TestAutomaticWidthRendersResolvedPlanAndKeepsSavedIntent(t *testing.T) {
	m, rec, c := fixture(t)
	m.opts.Runner = &radioRunner{recorder: rec}
	c.WiFi.Enable = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "radio", PSK: "private-password", Band: "5g", Standard: "wifi6", ChannelWidthMHz: config.WiFiAutoWidthMHz}}
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if m.wifiSelectedWidth != 80 || m.good.WiFi.APs[0].ChannelWidthMHz != config.WiFiAutoWidthMHz || m.good.WiFi.APs[0].Channel != 0 {
		t.Fatalf("resolved width %d or saved intent is incorrect", m.wifiSelectedWidth)
	}
	if host := m.render(c)["hostapd.conf"]; !strings.Contains(host, "vht_oper_chwidth=1\n") {
		t.Fatal("resolved 80 MHz missing from hostapd configuration")
	}
}

type radioRunner struct {
	*recorder
	restricted bool
}

type congestedRadioRunner struct {
	*radioRunner
	emptyStations bool
	switchFails   bool
	channel       int
}

func (r *congestedRadioRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name == "hostapd_cli" {
		command := strings.Join(args, " ")
		if strings.Contains(command, " chan_switch ") {
			if r.switchFails {
				return "FAIL\n", nil
			}
			r.channel = 153
			return "OK\n", nil
		}
		if strings.HasSuffix(command, " status") {
			return fmt.Sprintf("state=ENABLED\nfreq=%d\n", 5000+5*r.channel), nil
		}
	}
	if name == "iw" {
		switch strings.Join(args, " ") {
		case "dev wlan0 info":
			if r.channel == 0 {
				r.channel = 149
			}
			return fmt.Sprintf("Interface wlan0\n wiphy 0\n channel %d\n", r.channel), nil
		case "dev wlan0 scan":
			return "BSS 00:11:22:33:44:55 (on wlan0)\n signal: -35.00 dBm\n DS Parameter set: channel 149\n", nil
		case "dev wlan0 survey dump":
			return "frequency: 5745 MHz\n channel active time: 1000 ms\n channel busy time: 900 ms\nfrequency: 5765 MHz\n channel active time: 1000 ms\n channel busy time: 100 ms\n", nil
		case "dev wlan0 station dump":
			if r.emptyStations {
				return "", nil
			}
			return "Station aa:bb:cc:dd:ee:ff (on wlan0)\n signal: -50 dBm\n", nil
		}
	}
	return r.radioRunner.Run(ctx, name, args...)
}
func TestAutomaticReevaluationDefersWhileClientAssociated(t *testing.T) {
	m, rec, c := fixture(t)
	r := &radioRunner{recorder: rec}
	m.opts.Runner = r
	c.WiFi.Enable = true
	c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "radio", PSK: "private-password", Band: "5g", Standard: "wifi6", ChannelWidthMHz: 20, ChannelMode: "auto"}}
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	m.opts.Runner = &congestedRadioRunner{radioRunner: r, channel: 149}
	m.wifiLastSwitch = m.opts.Clock().Add(-time.Hour)
	for i := 0; i < 2; i++ {
		status, err := m.ReevaluateWiFi(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.Channel != 149 {
			t.Fatal(status)
		}
	}
	if m.wifiSelected != 149 {
		t.Fatal("changed channel with associated client")
	}
}

func TestAutomaticReevaluationChangesOnlyRadioAndHandlesRejection(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			m, rec, c := fixture(t)
			base := &radioRunner{recorder: rec}
			m.opts.Runner = base
			c.WiFi.Enable = true
			c.WiFi.APs = []config.WiFiAP{{Enable: true, SSID: "radio", PSK: "private-password", Band: "5g", Standard: "wifi6", ChannelWidthMHz: 20, ChannelMode: "auto"}}
			if _, err := m.Apply(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			r := &congestedRadioRunner{radioRunner: base, emptyStations: true, switchFails: reject, channel: 149}
			m.opts.Runner = r
			m.wifiLastSwitch = m.opts.Clock().Add(-time.Hour)
			before := len(rec.calls)
			for i := 0; i < 2; i++ {
				_, err := m.ReevaluateWiFi(context.Background())
				if i == 1 && (err != nil) != reject {
					t.Fatalf("result %v reject=%v", err, reject)
				}
			}
			want := 153
			if reject {
				want = 149
			}
			if m.wifiSelected != want {
				t.Fatalf("selected %d want %d", m.wifiSelected, want)
			}
			for _, call := range rec.calls[before:] {
				if strings.Contains(call, "ip link") || strings.Contains(call, "nft ") || strings.Contains(call, "systemctl") {
					t.Fatal("network mutation during radio reevaluation", call)
				}
			}
		})
	}
}

func TestChannelSwitchKeepsRequestedWidth(t *testing.T) {
	args := channelSwitchArgs("/control", "wlan0", config.WiFiAP{Band: "5g", Standard: "wifi6", ChannelWidthMHz: 80}, 149)
	joined := strings.Join(args, " ")
	for _, part := range []string{"chan_switch 5 5745", "bandwidth=80", "center_freq1=5775"} {
		if !strings.Contains(joined, part) {
			t.Fatal(joined)
		}
	}
}
func TestChannelSwitchRequiresHostapdAcceptance(t *testing.T) {
	if !hostapdAccepted("Selected interface 'wlan0'\nOK\n") || hostapdAccepted("FAIL\n") || hostapdAccepted("Selected interface 'wlan0'\n") {
		t.Fatal("unexpected hostapd response handling")
	}
}

func (r *radioRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name != "iw" {
		return r.recorder.Run(ctx, name, args...)
	}
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	switch strings.Join(args, " ") {
	case "dev wlan0 info":
		return "Interface wlan0\n wiphy 0\n channel 149 (5745 MHz), width: 80 MHz\n", nil
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
