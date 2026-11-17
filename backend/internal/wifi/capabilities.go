// Package wifi parses read-only iw capability observations shared by the API
// and network executor. It never treats compiled hostapd support as radio support.
package wifi

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

type Band struct {
	HT   bool `json:"ht"`
	HT40 bool `json:"ht40"`
	VHT  bool `json:"vht"`
	HE   bool `json:"he"`
	HE40 bool `json:"he40"`
	HE80 bool `json:"he80"`
	EHT  bool `json:"eht"`
}
type Channel struct {
	Band       string          `json:"band"`
	Disabled   bool            `json:"disabled"`
	NoIR       bool            `json:"noIR"`
	Radar      bool            `json:"radar"`
	Number     int             `json:"channel"`
	Restricted bool            `json:"restricted"`
	Widths     map[string]bool `json:"widths"`
}
type Capabilities struct {
	AP       bool             `json:"apSupported"`
	Bands    map[string]*Band `json:"bands"`
	Channels map[int]*Channel `json:"channels"` // keyed by MHz, avoiding band aliases
}

func kernelBand(line string) string {
	switch line {
	case "Band 1:":
		return "2g"
	case "Band 2:":
		return "5g"
	case "Band 4:":
		return "6g"
	}
	return ""
}

var frequencyLine = regexp.MustCompile(`^\* ([0-9]+)(?:\.0+)? MHz \[([0-9]+)\]`)

func parseFrequency(line string) (int, int, bool) {
	fields := frequencyLine.FindStringSubmatch(line)
	if fields == nil {
		return 0, 0, false
	}
	frequency, e1 := strconv.Atoi(fields[1])
	channel, e2 := strconv.Atoi(fields[2])
	return frequency, channel, e1 == nil && e2 == nil
}

func hasAP(value string) bool {
	for _, v := range strings.Split(value, ",") {
		if strings.TrimSpace(v) == "AP" {
			return true
		}
	}
	return false
}

// ParseCapabilities consumes iw phy info and iw phy channels from the same PHY.
// Missing observations are errors, not an invitation to assume capabilities.
func ParseCapabilities(info, channels string) (Capabilities, error) {
	c := Capabilities{Bands: map[string]*Band{}, Channels: map[int]*Channel{}}
	if len(info) > 1<<20 || len(channels) > 1<<20 || strings.ContainsRune(info+channels, 0) {
		return c, errors.New("invalid or oversized iw capability output")
	}
	var band *Band
	sectionBand := ""
	heAP, ehtAP, modes, identity := false, false, false, false
	for _, raw := range strings.Split(info, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "Wiphy ") {
			identity = true
		}
		if line == "Supported interface modes:" {
			modes = true
			continue
		}
		if modes {
			if strings.HasPrefix(line, "* ") {
				if line == "* AP" {
					c.AP = true
				}
			} else if line != "" {
				modes = false
			}
		}
		if strings.HasPrefix(line, "Band ") && strings.HasSuffix(line, ":") {
			band = &Band{}
			sectionBand = kernelBand(line)
			heAP = false
			ehtAP = false
			continue
		}
		if band == nil {
			continue
		}
		if strings.HasPrefix(line, "HE Iftypes:") {
			heAP = hasAP(strings.TrimPrefix(line, "HE Iftypes:"))
			ehtAP = false
			continue
		}
		if strings.HasPrefix(line, "EHT Iftypes:") {
			ehtAP = hasAP(strings.TrimPrefix(line, "EHT Iftypes:"))
			heAP = false
			continue
		}
		switch {
		case line == "HT20" || line == "HT20/HT40":
			band.HT = true
			if line == "HT20/HT40" {
				band.HT40 = true
			}
		case strings.HasPrefix(line, "VHT Capabilities ("):
			band.VHT = true
		case heAP && strings.HasPrefix(line, "HE PHY Capabilities:"):
			band.HE = true
		case heAP && line == "HE40/2.4GHz":
			band.HE40 = true
		case heAP && line == "HE40/HE80/5GHz":
			band.HE40 = true
			band.HE80 = true
		case ehtAP && strings.HasPrefix(line, "EHT PHY Capabilities ("):
			band.EHT = true
		}
		if _, _, ok := parseFrequency(line); ok {
			name := sectionBand
			if name != "" {
				if previous, ok := c.Bands[name]; ok && previous != band {
					return c, errors.New("ambiguous iw frequency band")
				}
				c.Bands[name] = band
			}
		}
	}
	var channel *Channel
	channelBand := ""
	for _, raw := range strings.Split(channels, "\n") {
		line := strings.TrimSpace(raw)
		if frequency, number, ok := parseFrequency(line); ok {
			if channelBand == "" {
				channel = nil
				continue
			}
			if _, exists := c.Channels[frequency]; exists {
				return c, errors.New("duplicate iw channel")
			}
			channel = &Channel{Band: channelBand, Number: number, Widths: map[string]bool{}, Restricted: strings.Contains(line, "disabled")}
			channel.Disabled = channel.Restricted
			c.Channels[frequency] = channel
			continue
		}
		if strings.HasPrefix(line, "Band ") {
			channelBand = kernelBand(line)
			channel = nil
			continue
		}
		if channel == nil {
			continue
		}
		switch line {
		case "No IR", "Passive scan":
			channel.NoIR = true
			channel.Restricted = true
		case "Radar detection":
			channel.Radar = true
			channel.Restricted = true
		}
		if strings.HasPrefix(line, "Channel widths:") {
			for _, width := range strings.Fields(strings.TrimPrefix(line, "Channel widths:")) {
				channel.Widths[width] = true
			}
		}
	}
	if !identity || len(c.Bands) == 0 || len(c.Channels) == 0 {
		return c, errors.New("incomplete iw capability output")
	}
	return c, nil
}
