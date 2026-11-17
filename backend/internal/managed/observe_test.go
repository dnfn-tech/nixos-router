package managed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeasesReadOnlyBoundedSnapshot(t *testing.T) {
	dir := t.TempDir()
	m, err := New(Options{StateDir: dir, Interfaces: []string{"wan0", "lan0"}, ManagementPort: 8080})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dnsmasq.leases")
	if _, err := m.Leases(context.Background()); err == nil {
		t.Fatal("missing file reported as empty leases")
	}
	if err := os.WriteFile(path, []byte("0 aa:bb:cc:dd:ee:ff 192.168.1.2 laptop *\n"), 0600); err != nil {
		t.Fatal(err)
	}
	leases, err := m.Leases(context.Background())
	if err != nil || len(leases) != 1 || leases[0].Hostname != "laptop" {
		t.Fatalf("leases: %+v %v", leases, err)
	}
	os.WriteFile(path, nil, 0600)
	leases, err = m.Leases(context.Background())
	if err != nil || leases == nil || len(leases) != 0 {
		t.Fatalf("valid empty leases: %+v %v", leases, err)
	}
	os.WriteFile(path, []byte(strings.Repeat("x", (1<<20)+1)), 0600)
	if _, err := m.Leases(context.Background()); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
	os.Remove(path)
	os.Symlink("/etc/passwd", path)
	if _, err := m.Leases(context.Background()); err == nil {
		t.Fatal("symlink read")
	}
}
