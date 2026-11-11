package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestWiFiRadioValidation(t *testing.T) {
	for _, tc := range []struct {
		name, standard, band string
		channel, width       int
		field                string
	}{
		{"legacy", "", "2g", 6, 0, ""},
		{"wifi6-80", "wifi6", "5g", 149, 80, ""},
		{"wifi7-80", "wifi7", "5g", 36, 80, ""},
		{"wifi4-40", "wifi4", "2g", 6, 40, ""},
		{"unknown", "auto", "5g", 149, 80, "standard"},
		{"injection", "wifi6\nieee80211be=1", "5g", 149, 80, "standard"},
		{"width", "wifi7", "5g", 149, 320, "channelWidthMHz"},
		{"legacy-wide", "", "5g", 149, 80, "standard"},
		{"wifi4-wide", "wifi4", "5g", 149, 80, "channelWidthMHz"},
		{"2g-wide", "wifi6", "2g", 6, 80, "channelWidthMHz"},
		{"wifi5-2g", "wifi5", "2g", 6, 20, "band"},
		{"165-wide", "wifi6", "5g", 165, 80, "channel"},
		{"dfs", "wifi6", "5g", 100, 80, "channel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.WiFi.Enable = true
			c.WiFi.APs = []WiFiAP{{Enable: true, SSID: "radio", PSK: "private-password", Security: "wpa3-sae", Standard: tc.standard, Band: tc.band, Channel: tc.channel, ChannelWidthMHz: tc.width}}
			err := c.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "wifi.aps.0."+tc.field) {
				t.Fatalf("expected %s, got %v", tc.field, err)
			}
		})
	}
}

func TestWiFiRadioPlanChannels(t *testing.T) {
	for _, tc := range []struct {
		band                         string
		ch, width, center, secondary int
		channels                     string
	}{
		{"5g", 149, 80, 155, 1, "[149 153 157 161]"},
		{"5g", 161, 80, 155, -1, "[149 153 157 161]"},
		{"5g", 40, 40, 38, -1, "[36 40]"},
		{"5g", 44, 40, 46, 1, "[44 48]"},
		{"5g", 165, 20, 165, 0, "[165]"},
		{"2g", 6, 40, 8, 1, "[6 10]"},
		{"2g", 11, 40, 9, -1, "[7 11]"},
	} {
		p, err := PlanWiFiRadio(WiFiAP{Standard: "wifi6", Band: tc.band, Channel: tc.ch, ChannelWidthMHz: tc.width})
		if err != nil {
			t.Fatal(err)
		}
		if p.CenterChannel != tc.center || p.SecondaryOffset != tc.secondary || fmt.Sprint(p.Channels) != tc.channels {
			t.Fatalf("%+v => %+v", tc, p)
		}
	}
}
