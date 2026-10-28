package main

import (
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAdminSeedUsesRuntimeSecretFile(t *testing.T) {
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret := filepath.Join(t.TempDir(), "password")
	if err = os.WriteFile(secret, []byte("runtime-file-password\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIXOS_ROUTER_ADMIN_PASSWORD", "")
	t.Setenv("NIXOS_ROUTER_ADMIN_PASSWORD_FILE", secret)
	if err = ensureAdminSeed(d, false, false); err != nil {
		t.Fatal(err)
	}
	hash, err := d.GetUserPasswordHash("admin")
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte("runtime-file-password")) != nil {
		t.Fatal("runtime secret did not seed the account")
	}
	t.Setenv("NIXOS_ROUTER_ADMIN_PASSWORD_FILE", "/missing/file")
	if err = ensureAdminSeed(d, false, false); err != nil {
		t.Fatal("existing account must not need seed file", err)
	}
}

func TestManagedStartupDoesNotDependOnBrokenDraft(t *testing.T) {
	for _, draft := range []string{"{broken", `{"wan":{"mode":"invalid"}}`} {
		t.Run(draft, func(t *testing.T) {
			dir := t.TempDir()
			opts := config.LoadOptions{StateDir: dir}
			os.MkdirAll(filepath.Join(dir, "managed"), 0700)
			good := config.DefaultConfig()
			good.WAN.Interface = "wan0"
			if err := config.SaveToFile(filepath.Join(dir, "managed/last-good.json"), good); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(dir, "config.json"), []byte(draft), 0600)
			cfg, path, warning, err := loadStartupConfig(opts, true)
			if err != nil || warning == "" || cfg.WAN.Interface != "wan0" || path != filepath.Join(dir, "config.json") {
				t.Fatalf("%+v %s %s %v", cfg, path, warning, err)
			}
			if b, _ := os.ReadFile(path); string(b) != draft {
				t.Fatal("damaged draft overwritten")
			}
			if _, _, _, err := loadStartupConfig(opts, false); err == nil {
				t.Fatal("preview accepted corrupt draft")
			}
			os.Remove(filepath.Join(dir, "managed/last-good.json"))
			if _, _, _, err := loadStartupConfig(opts, true); err == nil {
				t.Fatal("bootstrapped invalid draft without last-good")
			}
		})
	}
}

func TestManagedDaemonLockLifetime(t *testing.T) {
	dir := t.TempDir()
	first, err := acquireDaemonLock(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := acquireDaemonLock(dir, true); err == nil {
		second.Close()
		t.Fatal("second managed daemon acquired live state directory")
	}
	preview, err := acquireDaemonLock(dir, false)
	if err != nil || preview != nil {
		t.Fatalf("preview should not acquire lock: %v %v", preview, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := acquireDaemonLock(dir, true)
	if err != nil {
		t.Fatal("lock did not release", err)
	}
	next.Close()
}
func TestManagedMissingDraftRestoresLastGoodWithoutSeeding(t *testing.T) {
	for _, seed := range []bool{false, true} {
		dir := t.TempDir()
		good := config.DefaultConfig()
		good.System.Hostname = "last-applied"
		if err := os.MkdirAll(filepath.Join(dir, "managed"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := config.SaveToFile(filepath.Join(dir, "managed/last-good.json"), good); err != nil {
			t.Fatal(err)
		}
		cfg, path, warning, err := loadStartupConfig(config.LoadOptions{StateDir: dir, SeedDefaultIfMissing: seed}, true)
		if err != nil || warning == "" || cfg.System.Hostname != "last-applied" {
			t.Fatalf("seed=%v got %s warning=%q err=%v", seed, cfg.System.Hostname, warning, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("missing draft was unexpectedly created: %v", err)
		}
	}
}

func TestManagedFreshStateRequiresExplicitSeed(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := loadStartupConfig(config.LoadOptions{StateDir: dir}, true); err == nil {
		t.Fatal("missing draft and last-good silently bootstrapped")
	}
	cfg, path, warning, err := loadStartupConfig(config.LoadOptions{StateDir: dir, SeedDefaultIfMissing: true}, true)
	if err != nil || warning != "" || cfg.System.Hostname == "" {
		t.Fatalf("explicit seed failed: %q %v", warning, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestParseManagedSSHPorts(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []int
	}{{"22", []int{22}}, {"22,2222,65535", []int{22, 2222, 65535}}, {" 22, 2200 ", []int{22, 2200}}} {
		ports, err := parseManagedSSHPorts(tc.input)
		if err != nil || !reflect.DeepEqual(ports, tc.want) {
			t.Fatalf("%q: got %v, %v", tc.input, ports, err)
		}
	}
	for _, input := range []string{"", ",22", "22,", "0", "65536", "-1", "+22", "22,22", "22; reboot", "22\n23", "22\n", "tcp/22", "999999999999999999999999"} {
		if ports, err := parseManagedSSHPorts(input); err == nil {
			t.Fatalf("invalid %q accepted: %v", input, ports)
		}
	}
}
