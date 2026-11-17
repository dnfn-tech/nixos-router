package config

import "fmt"

// validateWiFiRadio checks the portable radio request. Actual PHY, AP-mode and
// regulatory capabilities must also be checked before applying it.
func validateWiFiRadio(ap WiFiAP, index int, active bool) error {
	field := func(name, reason string) error {
		return invalidField(fmt.Sprintf("wifi.aps.%d.%s", index, name), reason)
	}
	switch ap.Standard {
	case "", "wifi4", "wifi5", "wifi6", "wifi7":
	default:
		return field("standard", "must be wifi4, wifi5, wifi6 or wifi7")
	}
	switch ap.ChannelWidthMHz {
	case 0, 20, 40, 80:
	default:
		return field("channelWidthMHz", "must be 20, 40 or 80 MHz")
	}
	if !active {
		return nil
	}
	width := ap.ChannelWidthMHz
	if width == 0 {
		width = 20
	}
	if ap.Standard == "" {
		if width != 20 {
			return field("standard", "explicit standard required for wider channels")
		}
		return nil
	}
	if ap.Band != "" && ap.Band != "2g" && ap.Band != "5g" {
		return field("band", "advanced radio supports 2g or 5g")
	}
	if ap.Standard == "wifi5" && ap.Band != "5g" {
		return field("band", "wifi5 requires 5g")
	}
	if width == 80 && (ap.Band != "5g" || ap.Standard == "wifi4") {
		return field("channelWidthMHz", "80 MHz requires 5g and wifi5 or later")
	}
	if ap.Band == "5g" {
		switch ap.Channel {
		case 36, 40, 44, 48, 149, 153, 157, 161:
		case 165:
			if width != 20 {
				return field("channel", "channel 165 supports only 20 MHz in this profile")
			}
		default:
			return field("channel", "explicit supported non-DFS 5g channel required")
		}
	} else if ap.Channel < 1 || ap.Channel > 11 {
		return field("channel", "2g requires channel 1-11")
	}
	return nil
}

// WiFiRadioPlan describes the requested channel geometry, not measured support.
// Channels lists every occupied 20 MHz primary channel for regulatory checks.
type WiFiRadioPlan struct {
	WidthMHz        int
	CenterChannel   int
	SecondaryOffset int
	Channels        []int
}

func PlanWiFiRadio(ap WiFiAP) (WiFiRadioPlan, error) {
	if err := validateWiFiRadio(ap, 0, true); err != nil {
		return WiFiRadioPlan{}, err
	}
	width := ap.ChannelWidthMHz
	if width == 0 {
		width = 20
	}
	p := WiFiRadioPlan{WidthMHz: width, CenterChannel: ap.Channel, Channels: []int{ap.Channel}}
	if width == 20 {
		return p, nil
	}
	if ap.Band != "5g" {
		p.SecondaryOffset = 1
		if ap.Channel > 7 {
			p.SecondaryOffset = -1
		}
		p.CenterChannel = ap.Channel + 2*p.SecondaryOffset
		lo := ap.Channel
		if p.SecondaryOffset < 0 {
			lo -= 4
		}
		p.Channels = []int{lo, lo + 4}
		return p, nil
	}
	base := 36
	if ap.Channel >= 149 {
		base = 149
	}
	position := (ap.Channel - base) / 4
	p.SecondaryOffset = 1
	if position%2 == 1 {
		p.SecondaryOffset = -1
	}
	if width == 80 {
		p.CenterChannel = base + 6
		p.Channels = []int{base, base + 4, base + 8, base + 12}
	} else {
		lo := base + (position/2)*8
		p.CenterChannel = lo + 2
		p.Channels = []int{lo, lo + 4}
	}
	return p, nil
}
