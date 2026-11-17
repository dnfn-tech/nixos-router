package managed

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestIPv6ServiceEnvironmentAndLifecycle(t *testing.T) {
	for _, mode := range []string{"disabled", "dhcpv6", "slaac", "pppoe6"} {
		t.Run(mode, func(t *testing.T) {
			m, r, c := fixture(t)
			c.IPv6.Enable, c.IPv6.WANMode = mode != "disabled", mode
			c.IPv6.LANPD = mode == "dhcpv6" || mode == "pppoe6"
			dir, err := m.prepare(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", `. "$1"; printf '%s|%s|%s|%s|%s' "$WAN6_ENABLED" "$WAN6_MODE" "$WAN6_INTERFACE" "$WAN6_PD" "$WAN6_GENERATION"`, "check", filepath.Join(dir, "wan6.env"))
			got, err := cmd.CombinedOutput()
			enabled, pd, iface := "1", "0", "wan0"
			if mode == "disabled" {
				enabled = "0"
			}
			if c.IPv6.LANPD {
				pd = "1"
			}
			if mode == "pppoe6" {
				iface = "nr-ppp0"
			}
			want := strings.Join([]string{enabled, mode, iface, pd, filepath.Base(dir)}, "|")
			if err != nil || string(got) != want {
				t.Fatal(string(got), want, err)
			}
			r.calls = nil
			// Exercise activation directly to isolate service ordering and
			// generation environment from configuration validation.
			if err = m.activate(context.Background(), c, nil, dir); err != nil {
				t.Fatal(err)
			}
			calls := strings.Join(r.calls, "\n")
			stop := strings.Index(calls, "systemctl stop nixos-router-wan6.service")
			pppStop := strings.Index(calls, "systemctl stop nixos-router-pppoe.service")
			if stop < 0 || stop > pppStop {
				t.Fatal("IPv6 must stop before PPP disappears", calls)
			}
			// PPP's IPv6CP up hook starts the client once the link-local
			// endpoints exist; activation must not race that negotiation.
			if strings.Contains(calls, "systemctl restart nixos-router-wan6.service") != (c.IPv6.Enable && mode != "pppoe6") {
				t.Fatal("incorrect IPv6 service activation", calls)
			}
		})
	}
}

func TestDisabledIPv6DraftDoesNotBlockIPv4Apply(t *testing.T) {
	m, r, c := fixture(t)
	c.IPv6.Enable, c.IPv6.WANMode, c.IPv6.LANPD, c.IPv6.LANRA = false, "pppoe6", true, true
	before := c.IPv6
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if c.IPv6 != before {
		t.Fatal("inactive IPv6 draft changed")
	}
	if strings.Contains(strings.Join(r.calls, "\n"), "restart nixos-router-wan6.service") {
		t.Fatal("inactive IPv6 client started")
	}
}

// The public apply and restart paths must agree on enabled IPv6. Rejecting a
// persisted enabled configuration also prevents root protocol callbacks, which
// construct a new manager before processing the event.
func TestIPv6ApplyAndReloadLastGood(t *testing.T) {
	for _, mode := range []string{"dhcpv6", "slaac", "pppoe6"} {
		t.Run(mode, func(t *testing.T) {
			m, _, c := fixture(t)
			c.IPv6.Enable, c.IPv6.WANMode = true, mode
			c.IPv6.LANPD, c.IPv6.LANRA = mode != "slaac", mode != "slaac"
			if mode == "pppoe6" {
				c.WAN.Mode = "pppoe"
				c.WAN.PPPoE = &config.PPPoEConfig{Username: "test", Password: "test-password"}
			}
			if _, err := m.Apply(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			reloaded, err := New(m.opts)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.good == nil || reloaded.good.IPv6 != c.IPv6 {
				t.Fatal("enabled IPv6 last-good was not recovered")
			}
		})
	}
}
