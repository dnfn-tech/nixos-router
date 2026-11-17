package wifi

import (
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"testing"
)

func TestCapabilitiesScopeAPAndBand(t *testing.T) {
	info := `Wiphy phy0
 Supported interface modes:
  * managed
  * AP
 Band 1:
  Capabilities: 0x1
   HT20/HT40
  HE Iftypes: managed
   HE PHY Capabilities: (0x02):
    HE40/2.4GHz
  Frequencies:
   * 2437.0 MHz [6] (20.0 dBm)
 Band 2:
  Capabilities: 0x1
   HT20/HT40
  VHT Capabilities (0x0):
  HE Iftypes: AP
   HE PHY Capabilities: (0x04):
    HE40/HE80/5GHz
  EHT Iftypes: AP
   EHT PHY Capabilities (0x01):
  Frequencies:
   * 5745.0 MHz [149] (20.0 dBm)
`
	channels := `Band 2:
 * 5745 MHz [149]
  Channel widths: 20MHz HT40+ VHT80
 * 5765 MHz [153]
  No IR
  Channel widths: 20MHz HT40- VHT80
 * 5785 MHz [157] (disabled)
`
	c, err := ParseCapabilities(info, channels)
	if err != nil {
		t.Fatal(err)
	}
	if !c.AP || c.Bands["2g"].HE || !c.Bands["5g"].HE || !c.Bands["5g"].EHT || !c.Bands["5g"].HE80 {
		t.Fatalf("capability scope: %+v", c)
	}
	if !c.Channels[5745].Widths["VHT80"] || !c.Channels[5765].Restricted || !c.Channels[5785].Restricted {
		t.Fatalf("channel restrictions: %+v", c.Channels)
	}
}

func TestCapabilitiesRejectMissingAndOversized(t *testing.T) {
	for _, input := range []string{"", "Wiphy phy0\n", string(make([]byte, 1048577))} {
		if _, err := ParseCapabilities(input, ""); err == nil {
			t.Fatal("accepted incomplete capability output")
		}
	}
}

func TestRequestedRadioChecksEverySubchannel(t *testing.T) {
	ap := config.WiFiAP{Standard: "wifi7", Band: "5g", Channel: 149, ChannelWidthMHz: 80}
	c := Capabilities{AP: true, Bands: map[string]*Band{"5g": {HT: true, HT40: true, VHT: true, HE: true, HE40: true, HE80: true, EHT: true}}, Channels: map[int]*Channel{}}
	for _, ch := range []int{149, 153, 157, 161} {
		c.Channels[5000+ch*5] = &Channel{Band: "5g", Number: ch, Widths: map[string]bool{"20MHz": true, "HT40+": true, "VHT80": true}}
	}
	if err := c.Supports(ap); err != nil {
		t.Fatal(err)
	}
	c.Channels[5785].Restricted = true
	if err := c.Supports(ap); err == nil {
		t.Fatal("restricted secondary accepted")
	}
	c.Channels[5785].Restricted = false
	delete(c.Channels[5805].Widths, "VHT80")
	if err := c.Supports(ap); err == nil {
		t.Fatal("secondary width restriction ignored")
	}
	c.Channels[5805].Widths["VHT80"] = true
	c.Bands["5g"].EHT = false
	if err := c.Supports(ap); err == nil {
		t.Fatal("missing AP EHT accepted")
	}
}

func TestKernelBandIdentitySurvivesExtendedDisabled5GHzChannels(t *testing.T) {
	info := `Wiphy phy0
 Supported interface modes:
  * AP
 Band 2:
  HT20/HT40
  Frequencies:
   * 5925.0 MHz [185] (disabled)
 Band 4:
  HE Iftypes: AP
  HE PHY Capabilities: (0x04):
  Frequencies:
   * 5955.0 MHz [1] (20.0 dBm)
`
	channels := `Band 2:
 * 5925 MHz [185] (disabled)
Band 4:
 * 5955 MHz [1]
  Channel widths: 20MHz
Band 5:
 * 903 MHz [0]
  Channel widths: 20MHz
 * 903 MHz [0]
  Channel widths: 20MHz
`
	c, err := ParseCapabilities(info, channels)
	if err != nil {
		t.Fatal(err)
	}
	if c.Channels[5925].Band != "5g" || c.Channels[5955].Band != "6g" {
		t.Fatal("channel labels ignored kernel band identity")
	}
	if c.Bands["5g"] == nil || c.Bands["6g"] == nil || c.Bands["5g"].HE || !c.Bands["6g"].HE {
		t.Fatal("kernel bands merged by frequency boundary")
	}
}
