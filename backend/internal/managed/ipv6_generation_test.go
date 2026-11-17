package managed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ipv6GenerationFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	m, _, c := fixture(t)
	dir, err := m.prepare(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.switchFiles(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	context := ipv6RuntimeConfig{Enabled: true, Mode: "dhcpv6", WAN: "wan0", PhysicalWAN: "wan0", LAN: c.LAN.BridgeName, PD: true, RA: true}
	b, _ := json.Marshal(context)
	if err = atomicFile(filepath.Join(dir, "ipv6-context.json"), b); err != nil {
		t.Fatal(err)
	}
	return m, filepath.Base(dir)
}

func TestIPv6GenerationGateAndSwitchLock(t *testing.T) {
	m, generation := ipv6GenerationFixture(t)
	called := false
	callback := func(c ipv6RuntimeConfig) error {
		called = true
		if c.WAN != "wan0" {
			t.Fatal(c)
		}
		return nil
	}
	if err := m.withIPv6Generation(context.Background(), "generation-old", "wan0", callback); err != nil || called {
		t.Fatal("stale callback ran", err)
	}
	if err := m.withIPv6Generation(context.Background(), generation, "foreign0", callback); err == nil || called {
		t.Fatal("foreign interface accepted")
	}
	if err := m.withIPv6Generation(context.Background(), generation, "wan0", callback); err != nil || !called {
		t.Fatal(err)
	}
	other := filepath.Join(m.base(), "generation-next")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	err := m.withIPv6Generation(context.Background(), generation, "wan0", func(ipv6RuntimeConfig) error {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		if err := m.switchFiles(ctx, other); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("generation switched while callback owns lock: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.switchFiles(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	called = false
	if err = m.withIPv6Generation(context.Background(), generation, "wan0", callback); err != nil || called {
		t.Fatal("old generation mutated new configuration", err)
	}
}

func TestIPv6GenerationRejectsUnsafeContext(t *testing.T) {
	for _, mode := range []string{"symlink", "hardlink", "public", "foreign", "disabled", "traversal"} {
		t.Run(mode, func(t *testing.T) {
			m, generation := ipv6GenerationFixture(t)
			path := filepath.Join(m.base(), generation, "ipv6-context.json")
			switch mode {
			case "symlink", "hardlink":
				copyPath := path + "-copy"
				if err := os.Rename(path, copyPath); err != nil {
					t.Fatal(err)
				}
				var err error
				if mode == "symlink" {
					err = os.Symlink(copyPath, path)
				} else {
					err = os.Link(copyPath, path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "foreign", "disabled":
				c := ipv6RuntimeConfig{Enabled: mode != "disabled", Mode: "dhcpv6", WAN: "wan0", PhysicalWAN: "wan0", LAN: "br-lan"}
				if mode == "foreign" {
					c.WAN, c.PhysicalWAN = "foreign0", "foreign0"
				}
				b, _ := json.Marshal(c)
				if err := atomicFile(path, b); err != nil {
					t.Fatal(err)
				}
			case "traversal":
				generation = "../generation-other"
			}
			called := false
			err := m.withIPv6Generation(context.Background(), generation, "wan0", func(ipv6RuntimeConfig) error { called = true; return nil })
			if err == nil || called {
				t.Fatal("unsafe callback accepted", err)
			}
		})
	}
}
