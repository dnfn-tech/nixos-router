package managed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// WiFi IPv4 has no asynchronous lease writer: the existing process and kernel
// transaction locks serialize this journal with Apply, recovery and shutdown.
// Addresses is a write-ahead union, so either side of a failed update remains
// owned until a later reconciliation observes and removes it precisely.
type wifiIPv4Journal struct {
	Version   int
	Interface string
	Addresses []string
}

func routedWiFi(c config.Config) bool {
	return c.WiFi.Enable && !c.WiFi.BridgeToLan && c.WiFi.Network != nil
}
func wifiIPv4Desired(c config.Config) string {
	if routedWiFi(c) {
		return c.WiFi.Network.IPv4CIDR
	}
	return ""
}
func (m *Manager) wifiIPv4Path() string { return filepath.Join(m.base(), "wifi-ipv4.json") }
func validWiFiIPv4Address(value string) bool {
	p, err := netip.ParsePrefix(value)
	if err != nil || !p.Addr().Is4() || p.Bits() < 1 || p.Bits() > 30 || p.String() != value || p.Addr() == p.Masked().Addr() {
		return false
	}
	a := p.Addr().As4()
	n := p.Masked().Addr().As4()
	bits := uint32(0xffffffff) >> p.Bits()
	address := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	network := uint32(n[0])<<24 | uint32(n[1])<<16 | uint32(n[2])<<8 | uint32(n[3])
	return address != network|bits
}
func (m *Manager) readWiFiIPv4Journal() (*wifiIPv4Journal, error) {
	f, err := openIPv6PrivateFile(m.wifiIPv4Path(), false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 16385))
	decoder.DisallowUnknownFields()
	var j wifiIPv4Journal
	if decoder.Decode(&j) != nil {
		return nil, errors.New("invalid WiFi IPv4 journal")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || j.Version != 1 || j.Interface != m.opts.WiFiInterface || !ifname.MatchString(j.Interface) || j.Interface == "lo" || len(j.Addresses) > 8 {
		return nil, errors.New("invalid WiFi IPv4 journal")
	}
	seen := map[string]bool{}
	for _, address := range j.Addresses {
		if !validWiFiIPv4Address(address) || seen[address] {
			return nil, errors.New("invalid WiFi IPv4 journal address")
		}
		seen[address] = true
	}
	return &j, nil
}
func (m *Manager) observeWiFiIPv4(ctx context.Context) (bool, []string, error) {
	raw, err := m.run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		return false, nil, err
	}
	var links []struct {
		Name      string `json:"ifname"`
		Addresses []struct {
			Family string `json:"family"`
			Local  string `json:"local"`
			Prefix int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if json.Unmarshal([]byte(raw), &links) != nil {
		return false, nil, errors.New("invalid WiFi IPv4 observation")
	}
	present := false
	var addresses []string
	for _, link := range links {
		if link.Name != m.opts.WiFiInterface {
			continue
		}
		if present {
			return false, nil, errors.New("ambiguous WiFi IPv4 interface")
		}
		present = true
		for _, a := range link.Addresses {
			if a.Family != "inet" {
				continue
			}
			ip, err := netip.ParseAddr(a.Local)
			if err != nil || !ip.Is4() || a.Prefix < 0 || a.Prefix > 32 {
				return false, nil, errors.New("invalid WiFi IPv4 address observation")
			}
			addresses = append(addresses, netip.PrefixFrom(ip, a.Prefix).String())
		}
	}
	return present, addresses, nil
}
func checkWiFiIPv4Ownership(observed []string, j *wifiIPv4Journal) error {
	for _, address := range observed {
		if j == nil || !slices.Contains(j.Addresses, address) {
			return errors.New("refusing to adopt a foreign WiFi IPv4 address")
		}
	}
	return nil
}
func (m *Manager) preflightWiFiIPv4(ctx context.Context, c config.Config) error {
	old, err := m.readWiFiIPv4Journal()
	if err != nil {
		return err
	}
	if old == nil && (!c.WiFi.Enable || m.opts.WiFiInterface == "") {
		return nil
	}
	present, observed, err := m.observeWiFiIPv4(ctx)
	if err != nil {
		return err
	}
	if !present && wifiIPv4Desired(c) != "" {
		return errors.New("WiFi IPv4 interface unavailable")
	}
	if c.WiFi.Enable {
		return checkWiFiIPv4Ownership(observed, old)
	}
	return nil
}
func (m *Manager) writeWiFiIPv4Journal(addresses []string) error {
	raw, err := json.Marshal(wifiIPv4Journal{Version: 1, Interface: m.opts.WiFiInterface, Addresses: addresses})
	if err != nil {
		return err
	}
	return atomicFile(m.wifiIPv4Path(), raw)
}
func (m *Manager) reconcileWiFiIPv4(ctx context.Context, c config.Config) error {
	desired := wifiIPv4Desired(c)
	if desired != "" && (!validWiFiIPv4Address(desired) || !ifname.MatchString(m.opts.WiFiInterface) || m.opts.WiFiInterface == "lo") {
		return errors.New("invalid WiFi IPv4 assignment")
	}
	old, err := m.readWiFiIPv4Journal()
	if err != nil {
		return err
	}
	if old == nil && desired == "" {
		return nil
	}
	present, observed, err := m.observeWiFiIPv4(ctx)
	if err != nil {
		return err
	}
	if desired != "" {
		if !present {
			return errors.New("WiFi IPv4 interface unavailable")
		}
		if err = checkWiFiIPv4Ownership(observed, old); err != nil {
			return err
		}
	}
	var union []string
	if old != nil {
		union = append(union, old.Addresses...)
	}
	if desired != "" && !slices.Contains(union, desired) {
		union = append(union, desired)
	}
	if len(union) > 8 {
		return errors.New("WiFi IPv4 pending resource limit")
	}
	sort.Strings(union)
	if err = m.writeWiFiIPv4Journal(union); err != nil {
		return err
	}
	for _, address := range union {
		if address == desired || !present || !slices.Contains(observed, address) {
			continue
		}
		if err = m.cmd(ctx, "ip", "-4", "addr", "del", address, "dev", m.opts.WiFiInterface); err != nil {
			return err
		}
	}
	if desired != "" && !slices.Contains(observed, desired) {
		if err = m.cmd(ctx, "ip", "-4", "addr", "add", desired, "dev", m.opts.WiFiInterface); err != nil {
			return err
		}
	}
	if desired != "" {
		return m.writeWiFiIPv4Journal([]string{desired})
	}
	// An empty durable journal is harmless if a power loss undoes the unlink.
	if err = m.writeWiFiIPv4Journal(nil); err != nil {
		return err
	}
	if err = os.Remove(m.wifiIPv4Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
