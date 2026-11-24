package wifi

import (
	"fmt"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// Supports validates a request against observations from the selected PHY.
// The kernel/hostapd still make the final regulatory and association decisions.
func (c Capabilities) Supports(ap config.WiFiAP) error {
	p, err := config.PlanWiFiRadio(ap)
	if err != nil {
		return err
	}
	band := ap.Band
	if band == "" {
		band = "2g"
	}
	b := c.Bands[band]
	if !c.AP || b == nil {
		return fmt.Errorf("AP mode unavailable for band %s", band)
	}
	if ap.Standard != "" {
		if !b.HT || p.WidthMHz > 20 && !b.HT40 {
			return fmt.Errorf("requested HT mode or width unavailable")
		}
		if band == "5g" && ap.Standard != "wifi4" && !b.VHT {
			return fmt.Errorf("requested VHT mode unavailable")
		}
		if ap.Standard == "wifi6" || ap.Standard == "wifi7" {
			if !b.HE || p.WidthMHz == 40 && !b.HE40 || p.WidthMHz == 80 && !b.HE80 {
				return fmt.Errorf("requested AP HE mode or width unavailable")
			}
		}
		if ap.Standard == "wifi7" && !b.EHT {
			return fmt.Errorf("requested AP EHT mode unavailable")
		}
	}
	frequency := func(ch int) int {
		if band == "2g" {
			return 2407 + 5*ch
		}
		return 5000 + 5*ch
	}
	for _, ch := range p.Channels {
		observed := c.Channels[frequency(ch)]
		if observed == nil || observed.Number != ch || observed.Band != band || observed.Restricted || observed.Disabled || observed.NoIR || observed.Radar || !observed.Widths["20MHz"] {
			return fmt.Errorf("channel %d unavailable for AP transmission", ch)
		}
		if p.WidthMHz == 80 && !observed.Widths["VHT80"] {
			return fmt.Errorf("channel %d disallows 80 MHz", ch)
		}
	}
	if p.WidthMHz > 20 {
		direction := "HT40+"
		if p.SecondaryOffset < 0 {
			direction = "HT40-"
		}
		primary := c.Channels[frequency(ap.Channel)]
		if primary == nil || !primary.Widths[direction] {
			return fmt.Errorf("primary channel disallows %s", direction)
		}
	}
	return nil
}
