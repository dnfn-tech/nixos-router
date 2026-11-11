package managed

import (
	"context"
	"errors"
)

// claimIPv6Autoconfiguration is called only after the caller establishes link
// ownership. The protocol client, not the kernel RA handler, owns global IPv6
// addresses and routes. Leaving these automatic behaviors disabled on shutdown
// prevents unsolicited network state from reappearing after resource cleanup.
func (m *Manager) claimIPv6Autoconfiguration(ctx context.Context, iface string) error {
	if !ifname.MatchString(iface) || iface == "all" || iface == "default" || iface == "lo" {
		return errors.New("invalid IPv6 interface scope")
	}
	// Slash-separated keys preserve dots in legitimate interface names. Disable
	// automatic address/route creation before enabling IPv6 on an existing link.
	for _, setting := range []string{"accept_ra=0", "autoconf=0", "use_tempaddr=0", "accept_redirects=0", "disable_ipv6=0"} {
		if err := m.cmd(ctx, "sysctl", "-qw", "net/ipv6/conf/"+iface+"/"+setting); err != nil {
			return err
		}
	}
	return nil
}

// Per-interface force_forwarding gates actual transit without changing the
// host-wide forwarding policy. Older kernels may omit this knob: disabling is
// harmless there, but enabling must fail rather than widen scope globally.
func (m *Manager) setIPv6Forwarding(ctx context.Context, iface string, enabled bool) error {
	if !ifname.MatchString(iface) || iface == "all" || iface == "default" || iface == "lo" {
		return errors.New("invalid IPv6 forwarding scope")
	}
	flags := "-eqw"
	settings := []string{"force_forwarding=0", "forwarding=0"}
	if enabled {
		flags = "-qw"
		settings = []string{"forwarding=1", "force_forwarding=1"}
	}
	for _, setting := range settings {
		if err := m.cmd(ctx, "sysctl", flags, "net/ipv6/conf/"+iface+"/"+setting); err != nil {
			return err
		}
	}
	return nil
}
