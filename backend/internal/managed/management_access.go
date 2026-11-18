package managed

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// ManagementAccess is trusted deployment policy, never supplied by a draft/RPC.
// An exact ingress interface AND source prefix must both match.
type ManagementAccess struct {
	Interface string   `json:"interface"`
	Sources   []string `json:"sources"`
}

func (m *Manager) validateManagementAccess() error {
	if len(m.opts.ManagementAccess) > 32 {
		return errors.New("too many management grants")
	}
	grants := make([]ManagementAccess, 0, len(m.opts.ManagementAccess))
	seen := map[string]bool{}
	for _, g := range m.opts.ManagementAccess {
		if !ifname.MatchString(g.Interface) || g.Interface == "lo" || g.Interface == managedPPPInterface || m.allowed[g.Interface] || g.Interface == m.opts.WiFiInterface || seen[g.Interface] {
			return errors.New("management interface must be distinct, exact and outside owned interfaces")
		}
		if len(g.Sources) == 0 || len(g.Sources) > 32 {
			return errors.New("management grant requires 1..32 source prefixes")
		}
		seen[g.Interface] = true
		copyGrant := ManagementAccess{Interface: g.Interface}
		for _, v := range g.Sources {
			p, err := netip.ParsePrefix(v)
			if err != nil || p.Bits() == 0 || p.Addr().Is4In6() || p.Addr().IsMulticast() || p.Addr().IsUnspecified() || p != p.Masked() {
				return errors.New("management source must be a canonical non-default unicast prefix")
			}
			copyGrant.Sources = append(copyGrant.Sources, p.String())
		}
		grants = append(grants, copyGrant)
	}
	m.opts.ManagementAccess = grants
	return nil
}
func (m *Manager) validateManagementConfig(c config.Config) error {
	for _, g := range m.opts.ManagementAccess {
		if g.Interface == c.WAN.Interface || g.Interface == c.LAN.BridgeName {
			return errors.New("management grant conflicts with router-owned interface")
		}
	}
	return nil
}

// Failed initial activation must not remove mandatory management protection.
func (m *Manager) installManagementGuard(ctx context.Context) error {
	ports := []string{fmt.Sprint(m.opts.ManagementPort)}
	for _, p := range m.opts.SSHPorts {
		ports = append(ports, fmt.Sprint(p))
	}
	rules := fmt.Sprintf("add table inet nixos_router\nflush table inet nixos_router\ntable inet nixos_router { chain input { type filter hook input priority 0; policy accept; iifname != \"lo\" tcp dport { %s } drop; } }\n", strings.Join(ports, ", "))
	path := filepath.Join(m.base(), "management-guard.nft")
	if err := atomicFile(path, []byte(rules)); err != nil {
		return err
	}
	return m.cmd(ctx, "nft", "-f", path)
}
