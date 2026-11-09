package managed

import (
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
	return "plugin pppoe.so\nnic-" + c.WAN.Interface + "\nifname " + managedPPPInterface + "\n" +
		"user " + quote(c.WAN.PPPoE.Username) + "\npassword " + quote(c.WAN.PPPoE.Password) + "\n" +
		"nodetach\nnoauth\nnoipdefault\nnoipv6\nnodefaultroute\n" +
		"mtu 1492\nmru 1492\npersist\nmaxfail 0\nholdoff 5\n" +
		"lcp-echo-interval 10\nlcp-echo-failure 3\nhide-password\n", nil
}
