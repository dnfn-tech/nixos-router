package managed

import (
	"context"
	"errors"

	"github.com/dnfn-tech/nixos-router/backend/internal/wifi"
)

// Stations is a read-only observation of the one trusted AP interface.
func (m *Manager) Stations(ctx context.Context) ([]wifi.Station, error) {
	if m.opts.WiFiInterface == "" {
		return nil, errors.New("no managed WiFi interface")
	}
	raw, err := m.run(ctx, "iw", "dev", m.opts.WiFiInterface, "station", "dump")
	if err != nil {
		return nil, err
	}
	return wifi.ParseStations(raw)
}
