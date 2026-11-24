package wifi

import (
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestSelectChannelFiltersRestrictionsAndScoresInterference(t *testing.T) {
	caps := Capabilities{AP: true, Bands: map[string]*Band{"2g": {}}, Channels: map[int]*Channel{}}
	for _, n := range []int{1, 6, 11} {
		caps.Channels[2407+n*5] = &Channel{Band: "2g", Number: n, Widths: map[string]bool{"20MHz": true}}
	}
	caps.Channels[2437].NoIR = true
	ap := config.WiFiAP{Band: "2g", ChannelWidthMHz: 20}
	got, err := SelectChannel(ap, caps, []Neighbor{{Channel: 1, SignalDBm: -35}}, map[int]float64{1: .1, 11: .2}, 0)
	if err != nil || got.Channel != 11 {
		t.Fatalf("got %+v, %v", got, err)
	}
	got, err = SelectChannel(ap, caps, nil, nil, 11)
	if err != nil || got.Channel != 11 {
		t.Fatalf("fallback %+v, %v", got, err)
	}
	got, err = SelectChannel(ap, caps, nil, nil, 6)
	if err != nil || got.Channel != 1 {
		t.Fatalf("invalid fallback %+v, %v", got, err)
	}
}

func TestParseRadioObservations(t *testing.T) {
	neighbors := ParseScan("BSS 00:11:22:33:44:55 (on wlan0)\n freq: 2412\n signal: -48.00 dBm\n")
	if len(neighbors) != 1 || neighbors[0].Channel != 1 || neighbors[0].SignalDBm != -48 {
		t.Fatal(neighbors)
	}
	busy := ParseSurvey("frequency: 2412 MHz\n channel active time: 1000 ms\n channel busy time: 700 ms\nfrequency: 2437 MHz\n channel active time: 1000 ms\n")
	if busy[1] != .7 {
		t.Fatal(busy)
	}
	if _, ok := busy[6]; ok {
		t.Fatal("incomplete survey treated as idle")
	}
}

func TestSelectAutomaticWidthChecksEverySubchannel(t *testing.T) {
	caps := Capabilities{
		AP:       true,
		Bands:    map[string]*Band{"5g": {HT: true, HT40: true, VHT: true}},
		Channels: map[int]*Channel{},
	}
	for _, n := range []int{149, 153, 157, 161} {
		caps.Channels[5000+n*5] = &Channel{Band: "5g", Number: n, Widths: map[string]bool{
			"20MHz": true, "HT40+": true, "HT40-": true, "VHT80": true,
		}}
	}
	ap := config.WiFiAP{Band: "5g", Standard: "wifi5", Channel: 149, ChannelWidthMHz: config.WiFiAutoWidthMHz}
	check := func(want int) {
		t.Helper()
		got, err := SelectChannel(ap, caps, nil, nil, 0)
		if err != nil || got.Channel != 149 || got.WidthMHz != want {
			t.Fatalf("want channel 149 width %d, got %+v (%v)", want, got, err)
		}
	}
	check(80)
	caps.Channels[5805].Radar = true // 161 is required by 80 MHz, outside 149's 40 MHz pair.
	check(40)
	caps.Channels[5765].NoIR = true // 153 is required by the 40 MHz pair.
	check(20)
	caps.Channels[5745].Disabled = true
	if got, err := SelectChannel(ap, caps, nil, nil, 0); err == nil {
		t.Fatalf("disabled primary selected: %+v", got)
	}
}
