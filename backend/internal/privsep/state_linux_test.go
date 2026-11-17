package privsep

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckStateRejectsUntrustedManagedFiles(t *testing.T) {
	for _, kind := range []string{"directory-link", "last-good-link", "writable-config", "generation-link", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			state := filepath.Dir(socketPath(t))
			outside := filepath.Dir(socketPath(t))
			managed := filepath.Join(state, "managed")
			if kind == "directory-link" {
				if err := os.Symlink(outside, managed); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(managed, 0700); err != nil {
					t.Fatal(err)
				}
				f := filepath.Join(managed, "last-good.json")
				switch kind {
				case "last-good-link":
					if err := os.Symlink(filepath.Join(outside, "config.json"), f); err != nil {
						t.Fatal(err)
					}
				case "writable-config":
					if err := os.WriteFile(f, []byte("{}"), 0666); err != nil {
						t.Fatal(err)
					}
					os.Chmod(f, 0666)
				case "generation-link":
					if err := os.Symlink(outside, filepath.Join(managed, "generation-foreign")); err != nil {
						t.Fatal(err)
					}
				case "hardlink":
					src := filepath.Join(outside, "config.json")
					if err := os.WriteFile(src, []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Link(src, f); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := CheckState(state, uint32(os.Geteuid())); err == nil {
				t.Fatal("untrusted state accepted")
			}
		})
	}
}
func TestCheckStateAllowsOwnedGenerationPointer(t *testing.T) {
	state := filepath.Dir(socketPath(t))
	managed := filepath.Join(state, "managed")
	if err := os.MkdirAll(filepath.Join(managed, "generation-123"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("generation-123", filepath.Join(managed, "current")); err != nil {
		t.Fatal(err)
	}
	if err := CheckState(state, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
}

func TestCheckStateAllowsReadOnlyProtocolMetadataOnly(t *testing.T) {
	state := filepath.Dir(socketPath(t))
	if err := os.Mkdir(filepath.Join(state, "managed"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dnsmasq.leases", "managed/dnsmasq.pid"} {
		path := filepath.Join(state, name)
		if err := os.WriteFile(path, []byte("protocol metadata"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckState(state, uint32(os.Geteuid())); err != nil {
		t.Fatalf("private parent already protects read-only protocol files: %v", err)
	}
	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{
		{"dnsmasq.leases", 0666}, {"managed/dnsmasq.pid", 0664}, {"managed/last-good.json", 0644},
	} {
		path := filepath.Join(state, tc.path)
		os.WriteFile(path, []byte("untrusted"), 0600)
		os.Chmod(path, tc.mode)
		if err := CheckState(state, uint32(os.Geteuid())); err == nil {
			t.Fatalf("unsafe mode accepted: %s", tc.path)
		}
		os.Chmod(path, 0600)
	}
}
