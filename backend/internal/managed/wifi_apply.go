package managed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func sameNetworkExceptAPs(a, b config.Config) bool {
	a.Revision, b.Revision = "", ""
	a.WiFi.APs, b.WiFi.APs = nil, nil
	return reflect.DeepEqual(a, b)
}

// An AP configuration change can keep the current generation so WAN callbacks,
// addresses, bridge membership, DHCP, and firewall state continue to work.
// A cold restore still reconciles the complete network.
func (m *Manager) activateAPChange(ctx context.Context, c config.Config, old *config.Config, prepared string) (bool, error) {
	live := m.published.Load()
	if !c.WiFi.Enable || old == nil || live == nil ||
		reflect.DeepEqual(c.WiFi.APs, old.WiFi.APs) ||
		!sameNetworkExceptAPs(c, *old) || !sameNetworkExceptAPs(c, *live) {
		return false, nil
	}

	generation, err := os.Readlink(filepath.Join(m.base(), "current"))
	if err != nil || !ipv6GenerationName.MatchString(generation) {
		return false, nil
	}
	active := filepath.Join(m.base(), generation, "hostapd.conf")
	info, err := os.Lstat(active)
	if err != nil || !info.Mode().IsRegular() {
		return false, nil
	}
	hostapd, err := os.ReadFile(filepath.Join(prepared, "hostapd.conf"))
	if err != nil {
		return true, fmt.Errorf("read prepared AP configuration: %w", err)
	}
	if err := atomicFile(active, hostapd); err != nil {
		return true, fmt.Errorf("replace active AP configuration: %w", err)
	}
	// Recovery also restarts hostapd, even if a failed activation already
	// restored the same bytes, because the service may no longer be running.
	return true, m.restartAP(ctx)
}

func (m *Manager) restartAP(ctx context.Context) error {
	const unit = "nixos-router-hostapd.service"
	if err := m.cmd(ctx, "systemctl", "restart", unit); err != nil {
		return err
	}
	if err := m.cmd(ctx, "systemctl", "is-active", "--quiet", unit); err != nil {
		return err
	}
	return m.waitAPReady(ctx)
}
