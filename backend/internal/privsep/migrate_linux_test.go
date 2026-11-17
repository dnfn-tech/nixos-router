package privsep

import (
	"context"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPrepareAPIStatePreservesDataAndIsIdempotent(t *testing.T) {
	state := migrationState(t)
	dest := state + "-api"
	defer os.RemoveAll(dest)
	for name, data := range map[string]string{"config.json": "damaged draft remains recoverable", "state.db": "account database bytes", "state.db-wal": "pending WAL bytes"} {
		if err := os.WriteFile(filepath.Join(state, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(state, "managed"), 0700); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(state, "managed", "last-good.json")
	os.WriteFile(good, []byte("last-good"), 0600)
	if err := PrepareAPIState(state, dest, "/missing/bootstrap", uint32(os.Geteuid()), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.json", "state.db", "state.db-wal"} {
		src, _ := os.ReadFile(filepath.Join(state, name))
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(src) != string(got) {
			t.Fatalf("lost %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "managed")); !os.IsNotExist(err) {
		t.Fatal("runtime state exposed to API")
	}
	os.WriteFile(filepath.Join(dest, "config.json"), []byte("new draft"), 0600)
	if err := PrepareAPIState(state, dest, "/missing/bootstrap", uint32(os.Geteuid()), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "config.json"))
	if string(got) != "new draft" {
		t.Fatal("migration overwrote API state")
	}
	got, _ = os.ReadFile(good)
	if string(got) != "last-good" {
		t.Fatal("modified runtime state")
	}
}

func TestPrepareAPIStateRejectsUntrustedAndActiveSource(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public", "active", "unknown-target"} {
		t.Run(kind, func(t *testing.T) {
			state := migrationState(t)
			dest := state + "-api"
			defer os.RemoveAll(dest)
			source := filepath.Join(state, "config.json")
			os.WriteFile(source, []byte("draft"), 0600)
			switch kind {
			case "symlink":
				os.Remove(source)
				os.Symlink("/etc/passwd", source)
			case "hardlink":
				os.Link(source, filepath.Join(state, "alias"))
			case "public":
				os.Chmod(source, 0666)
			case "active":
				f, err := os.OpenFile(filepath.Join(state, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			case "unknown-target":
				os.Mkdir(dest, 0700)
			}
			if err := PrepareAPIState(state, dest, "/missing/bootstrap", uint32(os.Geteuid()), os.Getegid()); err == nil {
				t.Fatal("unsafe migration accepted")
			}
			if kind != "unknown-target" {
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatal("unsafe destination published")
				}
			}
		})
	}
}

func migrationState(t *testing.T) string {
	t.Helper()
	parent := filepath.Dir(socketPath(t))
	state := filepath.Join(parent, "executor")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestPrepareAPIStateBootstrapAndInterruptedPublication(t *testing.T) {
	state := migrationState(t)
	dest := state + "-api"
	defer os.RemoveAll(dest)
	bootstrap := filepath.Join(filepath.Dir(state), "bootstrap.json")
	if err := os.WriteFile(bootstrap, []byte("bootstrap bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareAPIState(state, dest, bootstrap, uint32(os.Geteuid()), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "config.json"))
	if string(got) != "bootstrap bytes" {
		t.Fatal("bootstrap not preserved")
	}
	marker := filepath.Join(state, "api-layout.json")
	// Simulate a crash after directory publication but before completing its record.
	record := apiLayout{Path: dest, UID: uint32(os.Geteuid()), GID: os.Getegid()}
	if err := writeLayout(marker, record); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dest, "config.json"), []byte("new draft"), 0600)
	if err := PrepareAPIState(state, dest, bootstrap, uint32(os.Geteuid()), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(dest, "config.json"))
	if string(got) != "new draft" {
		t.Fatal("recovery overwrote published state")
	}
	os.RemoveAll(dest)
	if err := PrepareAPIState(state, dest, bootstrap, uint32(os.Geteuid()), os.Getegid()); err == nil {
		t.Fatal("restored stale credentials after published state disappeared")
	}
}

func TestPrepareAPIStatePreservesSQLiteAccount(t *testing.T) {
	state := migrationState(t)
	dest := state + "-api"
	defer os.RemoveAll(dest)
	os.WriteFile(filepath.Join(state, "config.json"), []byte("draft"), 0600)
	original, err := db.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = original.CreateUser("existing-admin", "existing-hash"); err != nil {
		t.Fatal(err)
	}
	if err = original.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(state, "state.db"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = PrepareAPIState(state, dest, "/missing", uint32(os.Geteuid()), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	copied, err := db.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	var hash string
	if err = copied.SQL.QueryRow("SELECT password_hash FROM users WHERE username = ?", "existing-admin").Scan(&hash); err != nil || hash != "existing-hash" {
		t.Fatalf("lost account: %q %v", hash, err)
	}
}

// Exit without closing SQLite to reproduce an old daemon dying with committed
// data still in WAL. Reading the copied account proves WAL recovery, not just bytes.
func TestMigrationWALCrashHelper(t *testing.T) {
	state := os.Getenv("ROUTER_MIGRATION_TEST_WAL_DIR")
	if state == "" {
		return
	}
	syscall.Umask(0077)
	database, err := db.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.SQL.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err = database.SQL.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	if err = database.CreateUser("wal-admin", "committed-in-wal"); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
func TestPrepareAPIStateRecoversCommittedWAL(t *testing.T) {
	state := migrationState(t)
	dest := state + "-api"
	defer os.RemoveAll(dest)
	if err := os.WriteFile(filepath.Join(state, "config.json"), []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMigrationWALCrashHelper$")
	cmd.Env = append(os.Environ(), "ROUTER_MIGRATION_TEST_WAL_DIR="+state)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("WAL fixture: %s %v", out, err)
	}
	info, err := os.Stat(filepath.Join(state, "state.db-wal"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("fixture has no WAL: %v", err)
	}
	if err = PrepareAPIState(state, dest, "/missing", uint32(os.Geteuid()), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var hash string
	if err = database.SQL.QueryRow("SELECT password_hash FROM users WHERE username = ?", "wal-admin").Scan(&hash); err != nil || hash != "committed-in-wal" {
		t.Fatalf("committed WAL account lost: %q %v", hash, err)
	}
}
