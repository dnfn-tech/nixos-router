package managed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDNSProtocolSourcesAndGeneration(t *testing.T) {
	m, _, c := fixture(t)
	dir, err := m.prepare(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.switchFiles(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	gen := filepath.Base(dir)
	check := func(want string) {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(m.base(), "wan-resolv.conf"))
		if e != nil || string(b) != want {
			t.Fatalf("resolver %q, want %q: %v", b, want, e)
		}
	}
	if err = m.HandleIPv4DNS(context.Background(), gen, c.WAN.Interface, "bound", "1.1.1.1 1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if err = m.withIPv6StateLock(context.Background(), func() error { return m.publishDNSSource("wan6", "nameserver fe80::53%wan0\n") }); err != nil {
		t.Fatal(err)
	}
	check("nameserver 1.1.1.1\nnameserver fe80::53%wan0\n")
	if err = m.HandleIPv4DNS(context.Background(), gen, c.WAN.Interface, "bound", "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	check("nameserver 8.8.8.8\nnameserver fe80::53%wan0\n")
	if err = m.HandleIPv4DNS(context.Background(), gen, c.WAN.Interface, "unbound", "ignored old lease"); err != nil {
		t.Fatal(err)
	}
	check("nameserver fe80::53%wan0\n")
	if err = m.HandleIPv4DNS(context.Background(), "generation-stale", c.WAN.Interface, "bound", "9.9.9.9"); err != nil {
		t.Fatal(err)
	}
	check("nameserver fe80::53%wan0\n")
	if err = m.HandleIPv4DNS(context.Background(), gen, "foreign0", "bound", "9.9.9.9"); err == nil {
		t.Fatal("foreign callback accepted")
	}
	for _, bad := range []string{"127.0.0.1", "::1", "0.0.0.0", "224.1.1.1", "1.1.1.1\nsearch evil", "999.1.1.1", strings.Repeat("1.1.1.1 ", 129)} {
		if err = m.HandleIPv4DNS(context.Background(), gen, c.WAN.Interface, "bound", bad); err == nil {
			t.Fatal("bad DNS accepted")
		}
	}
	check("nameserver fe80::53%wan0\n")
	next, err := m.prepare(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.switchFiles(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	check("")
	if err = m.HandleIPv4DNS(context.Background(), gen, c.WAN.Interface, "bound", "9.9.9.9"); err != nil {
		t.Fatal(err)
	}
	check("")
}

func TestDNSRejectsUnsafeSource(t *testing.T) {
	m, _, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(m.base(), "wan6-resolv.conf"), []byte("nameserver ::1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.publishDNSSource("wan4", "nameserver 1.1.1.1\n"); err == nil {
		t.Fatal("corrupt other source accepted")
	}
	if err := m.publishDNSSource("../escape", ""); err == nil {
		t.Fatal("path accepted")
	}
}

func TestDNSConcurrentProtocolUpdatesPreserveBothSources(t *testing.T) {
	m, _, c := fixture(t)
	dir, err := m.prepare(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.switchFiles(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, source := range []string{"wan4", "wan6"} {
		wg.Add(1)
		go func(source string) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				var e error
				if source == "wan4" {
					e = m.HandleIPv4DNS(context.Background(), filepath.Base(dir), c.WAN.Interface, "bound", "1.1.1.1")
				} else {
					e = m.withIPv6StateLock(context.Background(), func() error { return m.publishDNSSource("wan6", "nameserver 2001:db8::53\n") })
				}
				if e != nil {
					errors <- e
					return
				}
			}
		}(source)
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		t.Fatal(e)
	}
	got, err := os.ReadFile(filepath.Join(m.base(), "wan-resolv.conf"))
	if err != nil || string(got) != "nameserver 1.1.1.1\nnameserver 2001:db8::53\n" {
		t.Fatal(string(got), err)
	}
}
