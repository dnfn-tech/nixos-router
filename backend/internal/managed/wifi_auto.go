package managed

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/wifi"
)

type WiFiChannelStatus struct {
	Mode    string `json:"mode"`
	Channel int    `json:"channel"`
	Reason  string `json:"reason"`
}

func (m *Manager) recordWiFiSelection(c config.Config) {
	if !c.WiFi.Enable {
		if m.wifiMonitor != nil {
			m.wifiMonitor.Stop()
		}
		return
	}
	automatic := false
	for _, ap := range c.WiFi.APs {
		if ap.Enable && ap.Channel != 0 {
			m.wifiSelected = ap.Channel
			m.wifiReason = "fixed configuration"
			break
		}
		if ap.Enable && ap.Channel == 0 {
			automatic = true
		}
	}
	if m.wifiSelected > 0 {
		_ = atomicFile(filepath.Join(m.base(), "wifi-channel"), []byte(strconv.Itoa(m.wifiSelected)+"\n"))
	}
	m.wifiLastSwitch = m.opts.Clock()
	if automatic {
		m.armWiFiMonitor()
	} else if m.wifiMonitor != nil {
		m.wifiMonitor.Stop()
	}
}

func (m *Manager) WiFiChannel(ctx context.Context) (WiFiChannelStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.good == nil || !m.good.WiFi.Enable {
		return WiFiChannelStatus{}, errors.New("WiFi is not applied")
	}
	status := WiFiChannelStatus{Mode: "fixed", Channel: m.wifiSelected, Reason: m.wifiReason}
	if cfg := m.published.Load(); cfg != nil {
		for _, ap := range cfg.WiFi.APs {
			if ap.Enable {
				if ap.Channel == 0 {
					status.Mode = "auto"
				} else {
					status.Channel = ap.Channel
					status.Reason = "fixed configuration"
				}
				break
			}
		}
	}
	raw, err := m.run(ctx, "iw", "dev", m.opts.WiFiInterface, "info")
	if err != nil {
		return WiFiChannelStatus{}, err
	}
	observed := 0
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == "channel" {
			observed, _ = strconv.Atoi(fields[1])
			break
		}
	}
	if observed == 0 {
		return WiFiChannelStatus{}, errors.New("current radio channel unavailable")
	}
	status.Channel = observed
	if observed != m.wifiSelected && status.Mode == "auto" {
		status.Reason = "observed channel differs from last selection"
	}
	return status, nil
}

func (m *Manager) armWiFiMonitor() {
	if m.wifiMonitor != nil {
		m.wifiMonitor.Stop()
	}
	m.wifiMonitor = time.AfterFunc(5*time.Minute, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = m.ReevaluateWiFi(ctx)
		cancel()
		m.mu.Lock()
		if m.good != nil && m.good.WiFi.Enable {
			m.armWiFiMonitor()
		}
		m.mu.Unlock()
	})
}

// ReevaluateWiFi serializes with Apply and changes only the trusted AP radio.
// It never touches WAN, LAN, bridges or generated configuration.
func (m *Manager) ReevaluateWiFi(ctx context.Context) (WiFiChannelStatus, error) {
	if !m.mu.TryLock() {
		return WiFiChannelStatus{}, errors.New("apply or WiFi evaluation busy")
	}
	defer m.mu.Unlock()
	lock, err := m.transactionLock()
	if err != nil {
		return WiFiChannelStatus{}, err
	}
	defer lock.Close()
	if pending, err := m.readCandidate(); err != nil {
		return WiFiChannelStatus{}, err
	} else if pending != nil && pending.unsettled() {
		return WiFiChannelStatus{}, errors.New("configuration confirmation pending")
	}
	if m.good == nil || !m.good.WiFi.Enable {
		return WiFiChannelStatus{}, errors.New("WiFi is not applied")
	}
	var apIndex int = -1
	for i, ap := range m.good.WiFi.APs {
		if ap.Enable {
			apIndex = i
			break
		}
	}
	if apIndex < 0 {
		return WiFiChannelStatus{}, errors.New("no active AP")
	}
	ap := m.good.WiFi.APs[apIndex]
	if ap.Channel != 0 {
		return WiFiChannelStatus{Mode: "fixed", Channel: ap.Channel, Reason: "fixed configuration"}, nil
	}
	if ap.ChannelWidthMHz == config.WiFiAutoWidthMHz {
		if m.wifiSelectedWidth == 0 {
			return WiFiChannelStatus{}, errors.New("current width unknown")
		}
		ap.ChannelWidthMHz = m.wifiSelectedWidth
	}
	current := m.wifiSelected
	if current == 0 {
		return WiFiChannelStatus{}, errors.New("current channel unknown")
	}
	raw, err := m.run(ctx, "iw", "dev", m.opts.WiFiInterface, "info")
	if err != nil {
		return WiFiChannelStatus{}, err
	}
	phy := ""
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "wiphy" {
			if n, e := strconv.Atoi(f[1]); e == nil && n >= 0 {
				phy = "phy" + f[1]
			}
		}
	}
	if phy == "" {
		return WiFiChannelStatus{}, errors.New("radio PHY unknown")
	}
	info, err := m.run(ctx, "iw", "phy", phy, "info")
	if err != nil {
		return WiFiChannelStatus{}, err
	}
	channels, err := m.run(ctx, "iw", "phy", phy, "channels")
	if err != nil {
		return WiFiChannelStatus{}, err
	}
	caps, err := wifi.ParseCapabilities(info, channels)
	if err != nil {
		return WiFiChannelStatus{}, err
	}
	scan, scanErr := m.run(ctx, "iw", "dev", m.opts.WiFiInterface, "scan")
	survey, _ := m.run(ctx, "iw", "dev", m.opts.WiFiInterface, "survey", "dump")
	if scanErr != nil {
		m.wifiReason = "scan unavailable; kept current channel"
		return WiFiChannelStatus{Mode: "auto", Channel: current, Reason: "scan unavailable; kept current channel"}, nil
	}
	neighbors := wifi.ParseScan(scan)
	busy := wifi.ParseSurvey(survey)
	choice, err := wifi.SelectChannel(ap, caps, neighbors, busy, current)
	if err != nil {
		return WiFiChannelStatus{}, err
	}
	status := WiFiChannelStatus{Mode: "auto", Channel: current, Reason: "current channel remains preferred"}
	oldScore := wifi.ScoreChannel(ap, current, neighbors, busy)
	if value, ok := busy[current]; !ok || value < .65 || choice.Channel == current || choice.Score > oldScore-25 {
		m.wifiBusySamples = 0
		m.wifiReason = status.Reason
		return status, nil
	}
	m.wifiBusySamples++
	if m.wifiBusySamples < 2 {
		status.Reason = "waiting for sustained congestion"
		m.wifiReason = status.Reason
		return status, nil
	}
	if m.opts.Clock().Sub(m.wifiLastSwitch) < 30*time.Minute {
		status.Reason = "channel change cooling down"
		m.wifiReason = status.Reason
		return status, nil
	}
	stations, err := m.Stations(ctx)
	if err != nil {
		return status, err
	}
	if len(stations) > 0 {
		status.Reason = "associated clients present; change deferred"
		m.wifiReason = status.Reason
		return status, nil
	}
	frequency := 2407 + 5*choice.Channel
	if ap.Band == "5g" {
		frequency = 5000 + 5*choice.Channel
	}
	control := filepath.Join(m.base(), "hostapd-control")
	args := channelSwitchArgs(control, m.opts.WiFiInterface, ap, choice.Channel)
	response, err := m.run(ctx, "hostapd_cli", args...)
	if err != nil || !hostapdAccepted(response) {
		m.wifiLastSwitch = m.opts.Clock()
		if err == nil {
			err = errors.New("hostapd rejected channel change")
		}
		return status, err
	}
	if err = m.waitChannel(ctx, frequency); err != nil {
		m.wifiLastSwitch = m.opts.Clock()
		recovery, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rollbackResponse, rollbackErr := m.run(recovery, "hostapd_cli", channelSwitchArgs(control, m.opts.WiFiInterface, ap, current)...)
		if rollbackErr == nil && !hostapdAccepted(rollbackResponse) {
			rollbackErr = errors.New("hostapd rejected rollback")
		}
		if rollbackErr == nil {
			oldFreq := 2407 + 5*current
			if ap.Band == "5g" {
				oldFreq = 5000 + 5*current
			}
			rollbackErr = m.waitChannel(recovery, oldFreq)
		}
		if rollbackErr != nil {
			m.wifiReason = "channel unknown after failed rollback"
		}
		return status, fmt.Errorf("channel change failed: %w; rollback: %v", err, rollbackErr)
	}
	m.wifiSelected = choice.Channel
	m.wifiReason = "sustained congestion; " + choice.Reason
	m.wifiBusySamples = 0
	m.wifiLastSwitch = m.opts.Clock()
	_ = atomicFile(filepath.Join(m.base(), "wifi-channel"), []byte(strconv.Itoa(choice.Channel)+"\n"))
	return WiFiChannelStatus{Mode: "auto", Channel: choice.Channel, Reason: m.wifiReason}, nil
}

func hostapdAccepted(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "FAIL" {
			return false
		}
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "OK" {
			return true
		}
	}
	return false
}

func channelSwitchArgs(control, iface string, ap config.WiFiAP, channel int) []string {
	frequency := 2407 + 5*channel
	if ap.Band == "5g" {
		frequency = 5000 + 5*channel
	}
	args := []string{"-p", control, "-s", control, "-i", iface, "chan_switch", "5", strconv.Itoa(frequency)}
	effective := ap
	effective.Channel = channel
	effective.ChannelMode = "fixed"
	if plan, err := config.PlanWiFiRadio(effective); err == nil && plan.WidthMHz > 20 {
		center := 2407 + 5*plan.CenterChannel
		if ap.Band == "5g" {
			center = 5000 + 5*plan.CenterChannel
		}
		args = append(args, "sec_channel_offset="+strconv.Itoa(plan.SecondaryOffset), "center_freq1="+strconv.Itoa(center), "bandwidth="+strconv.Itoa(plan.WidthMHz))
	}
	return args
}

func (m *Manager) waitChannel(ctx context.Context, frequency int) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	control := filepath.Join(m.base(), "hostapd-control")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := m.run(ctx, "hostapd_cli", "-p", control, "-s", control, "-i", m.opts.WiFiInterface, "status")
		if err == nil {
			ready, frequencyOK := false, false
			for _, line := range strings.Split(raw, "\n") {
				line = strings.TrimSpace(line)
				if line == "state=ENABLED" {
					ready = true
				}
				if line == "freq="+strconv.Itoa(frequency) {
					frequencyOK = true
				}
			}
			if ready && frequencyOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("AP did not confirm requested channel")
		case <-ticker.C:
		}
	}
}
