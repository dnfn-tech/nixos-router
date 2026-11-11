package managed

import (
	"context"
	"testing"
	"time"
)

func TestIPv6CallbackValidatesBeforeNetworkCommands(t *testing.T) {
	m, r, gen := resourceFixture(t)
	now := time.Unix(1000, 0)
	if err := m.HandleIPv6Event(context.Background(), gen, "wan0", "bound", map[string]string{"ADDRESSES": "invalid-input"}, now); err == nil || r.mutations != 0 {
		t.Fatal("invalid protocol data reached execution", err)
	}
	if err := m.HandleIPv6Event(context.Background(), gen, "foreign0", "bound", nil, now); err == nil || r.mutations != 0 {
		t.Fatal("foreign interface accepted", err)
	}
	if err := m.HandleIPv6Event(context.Background(), gen, "wan0", "bound", map[string]string{"ADDRESSES": "2001:db8::2/128,30,60", "COMMAND": "untrusted"}, now); err != nil || len(r.live) != 1 {
		t.Fatal("valid callback failed", err)
	}
}
