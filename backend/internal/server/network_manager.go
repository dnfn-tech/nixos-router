package server

import (
	"fmt"
	"strings"
)

// Runtime apply must not share network ownership with another general manager.
// Only an explicitly inactive unit permits proceeding. Missing units are reported
// inactive by systemctl show; a bus/query failure is not proof of exclusivity.
func (s *Server) checkNetworkManagers() (string, error) {
	for _, unit := range []string{"NetworkManager.service", "systemd-networkd.service", "dhcpcd.service", "connman.service"} {
		state, err := s.runner.Output("systemctl", "show", "--property=ActiveState", "--value", unit)
		if err != nil || strings.TrimSpace(state) == "" {
			return "network_manager_check_failed", fmt.Errorf("cannot establish network ownership: %s", unit)
		}
		if strings.TrimSpace(state) != "inactive" {
			return "network_manager_conflict", fmt.Errorf("network manager must be stopped before runtime apply: %s (%s)", unit, strings.TrimSpace(state))
		}
	}
	return "", nil
}
