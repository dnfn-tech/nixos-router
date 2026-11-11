package managed

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// This name belongs exclusively to the dedicated PPP service. Activation must
// reject foreign ownership before starting it, just as it does for the LAN bridge.
const managedPPPInterface = "nr-ppp0"

func wanEgress(c config.Config) string {
	if c.WAN.Mode == "pppoe" {
		return managedPPPInterface
	}
	return c.WAN.Interface
}

// renderPPPoE produces a private options file, never command-line arguments.
// Hook paths and process supervision belong to the Nix service definition.
// This helper alone does not enable PPPoE in managed validation/activation.
func renderPPPoE(c config.Config) (string, error) {
	if c.WAN.Mode != "pppoe" {
		return "", nil
	}
	if !ifname.MatchString(c.WAN.Interface) || c.WAN.Interface == managedPPPInterface || c.WAN.Interface == "lo" {
		return "", errors.New("PPPoE requires a valid physical WAN interface")
	}
	valid := func(value string) bool {
		if len(value) == 0 || len(value) > 255 || !utf8.ValidString(value) {
			return false
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return false
			}
		}
		return true
	}
	if c.WAN.PPPoE == nil || !valid(c.WAN.PPPoE.Username) || !valid(c.WAN.PPPoE.Password) {
		return "", errors.New("PPPoE credentials must contain 1-255 bytes without control characters")
	}
	// pppd options use double-quoted words and backslash escapes, not shell or
	// JSON escaping. Reject controls above; escape only backslash and quote.
	quote := func(value string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"` }
	ipv6 := "noipv6\n"
	if c.IPv6.Enable && c.IPv6.WANMode == "pppoe6" {
		ipv6 = "+ipv6\n"
	}
	return "plugin pppoe.so\nnic-" + c.WAN.Interface + "\nifname " + managedPPPInterface + "\n" +
		"user " + quote(c.WAN.PPPoE.Username) + "\npassword " + quote(c.WAN.PPPoE.Password) + "\n" +
		"nodetach\nnoauth\nnoipdefault\n" + ipv6 + "nodefaultroute\n" +
		"mtu 1492\nmru 1492\npersist\nmaxfail 0\nholdoff 5\n" +
		"lcp-echo-interval 10\nlcp-echo-failure 3\nhide-password\n", nil
}

// Query all links: an interface absent while pppd is reconnecting is waiting,
// whereas command/JSON errors mean observation is unknown.
func (m *Manager) pppAddressReady(ctx context.Context) (bool, error) {
	raw, err := m.run(ctx, "ip", "-j", "-4", "addr", "show")
	if err != nil {
		return false, err
	}
	var links []struct {
		Name      string `json:"ifname"`
		Addresses []struct {
			Local string `json:"local"`
			Scope string `json:"scope"`
		} `json:"addr_info"`
	}
	if err = json.Unmarshal([]byte(raw), &links); err != nil {
		return false, err
	}
	for _, link := range links {
		if link.Name == managedPPPInterface {
			for _, a := range link.Addresses {
				if a.Scope == "global" && ipv4(a.Local) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
