package managed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/wifi"
)

// renderWiFiRadio uses only portable mode/width settings. PHY-specific antenna,
// beamforming and MCS capabilities are deliberately left to hostapd and driver.
func renderWiFiRadio(ap config.WiFiAP) string {
	if ap.Standard == "" {
		return ""
	}
	p, err := config.PlanWiFiRadio(ap)
	if err != nil {
		return ""
	} // caller validates before creating a generation
	var out strings.Builder
	out.WriteString("wmm_enabled=1\nieee80211n=1\n")
	if p.SecondaryOffset > 0 {
		out.WriteString("ht_capab=[HT40+]\n")
	}
	if p.SecondaryOffset < 0 {
		out.WriteString("ht_capab=[HT40-]\n")
	}
	vht := ap.Band == "5g" && ap.Standard != "wifi4"
	he := ap.Standard == "wifi6" || ap.Standard == "wifi7"
	eht := ap.Standard == "wifi7"
	if vht {
		out.WriteString("ieee80211ac=1\n")
	}
	if he {
		out.WriteString("ieee80211ax=1\n")
	}
	if eht {
		out.WriteString("ieee80211be=1\n")
	}
	if ap.Band == "5g" {
		width := 0
		if p.WidthMHz == 80 {
			width = 1
		}
		for _, mode := range []struct {
			name    string
			enabled bool
		}{{"vht", vht}, {"he", he}, {"eht", eht}} {
			if mode.enabled {
				fmt.Fprintf(&out, "%s_oper_chwidth=%d\n%s_oper_centr_freq_seg0_idx=%d\n", mode.name, width, mode.name, p.CenterChannel)
			}
		}
	}
	return out.String()
}

// preflightWiFiRadio only observes the allowlisted interface. It runs before
// generation creation or any service/link change, including during restore.
func (m *Manager) preflightWiFiRadio(ctx context.Context, c config.Config) error {
	if !c.WiFi.Enable {
		return nil
	}
	for _, ap := range c.WiFi.APs {
		if !ap.Enable || ap.Channel != 0 && ap.Standard == "" && ap.ChannelWidthMHz == 0 {
			continue
		}
		check := func() error {
			raw, err := m.run(ctx, "iw", "dev", m.opts.WiFiInterface, "info")
			if err != nil {
				return fmt.Errorf("cannot query allowlisted interface: %w", err)
			}
			phy := ""
			for _, line := range strings.Split(raw, "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[0] == "wiphy" {
					index, e := strconv.ParseUint(fields[1], 10, 31)
					if e != nil || phy != "" {
						return fmt.Errorf("invalid or ambiguous interface PHY")
					}
					phy = "phy" + strconv.FormatUint(index, 10)
				}
			}
			if phy == "" {
				return fmt.Errorf("interface PHY unavailable")
			}
			info, err := m.run(ctx, "iw", "phy", phy, "info")
			if err != nil {
				return fmt.Errorf("cannot query PHY capabilities: %w", err)
			}
			if !strings.HasPrefix(strings.TrimSpace(info), "Wiphy "+phy+"\n") {
				return fmt.Errorf("PHY observation identity mismatch")
			}
			channels, err := m.run(ctx, "iw", "phy", phy, "channels")
			if err != nil {
				return fmt.Errorf("cannot query channel restrictions: %w", err)
			}
			observed, err := wifi.ParseCapabilities(info, channels)
			if err != nil {
				return err
			}
			if ap.Channel == 0 || ap.ChannelWidthMHz == config.WiFiAutoWidthMHz {
				var neighbors []wifi.Neighbor
				scanOK := false
				if raw, e := m.run(ctx, "iw", "dev", m.opts.WiFiInterface, "scan"); e == nil {
					neighbors = wifi.ParseScan(raw)
					scanOK = true
				}
				busy := map[int]float64{}
				if raw, e := m.run(ctx, "iw", "dev", m.opts.WiFiInterface, "survey", "dump"); e == nil {
					busy = wifi.ParseSurvey(raw)
				}
				if !scanOK {
					busy = nil
				}
				previous := m.wifiSelected
				if previous == 0 {
					if raw, e := os.ReadFile(filepath.Join(m.base(), "wifi-channel")); e == nil {
						previous, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
					}
				}
				selection, e := wifi.SelectChannel(ap, observed, neighbors, busy, previous)
				if e != nil {
					return e
				}
				m.wifiSelected = selection.Channel
				m.wifiSelectedWidth = selection.WidthMHz
				m.wifiReason = selection.Reason
				return nil
			}
			return observed.Supports(ap)
		}
		if err := check(); err != nil {
			return fmt.Errorf("WiFi capability preflight: %w", err)
		}
	}
	return nil
}
