package managed

import (
	"context"
	"time"
)

// HandleIPv6Event accepts only odhcp6c's protocol fields. The root executable
// must validate its private state before constructing the manager. Generation,
// interface ownership and serialized resource updates are checked here again.
func (m *Manager) HandleIPv6Event(ctx context.Context, generation, iface, state string, env map[string]string, observedAt time.Time) error {
	event, err := parseIPv6Event(state, env)
	if err != nil {
		return err
	}
	return m.reconcileIPv6Event(ctx, generation, iface, event, observedAt)
}
