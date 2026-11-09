package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// WithNewRevision gives saved intent a fresh identity, including when restoring
// an older backup. Revision identity never depends on secret configuration data.
func WithNewRevision(c Config) (Config, error) {
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Config{}, err
	}
	c.SchemaVersion = CurrentSchemaVersion
	c.Revision = hex.EncodeToString(token[:])
	return c, nil
}

// LoadDraft upgrades only editable intent. Runtime snapshots use LoadFromFile
// and are never rewritten merely by being observed or restored.
func LoadDraft(path string) (Config, error) {
	lock, err := os.OpenFile(path+".migration.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return Config{}, err
	}
	defer lock.Close()
	info, err := lock.Stat()
	if err != nil {
		return Config{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) {
		return Config{}, fmt.Errorf("unsafe draft migration lock")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Config{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	original, err := readDraftFile(path, false)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(original, &c); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	if c.SchemaVersion == CurrentSchemaVersion && c.Revision != "" {
		return c, nil
	}
	next, err := WithNewRevision(c)
	if err != nil {
		return Config{}, err
	}
	backup := fmt.Sprintf("%s.v%d.bak", path, c.SchemaVersion)
	if err := preserveDraftBackup(backup, original); err != nil {
		return Config{}, err
	}
	if err := SaveToFile(path, next); err != nil {
		return Config{}, err
	}
	return next, nil
}

func readDraftFile(path string, private bool) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || (private && info.Mode().Perm()&0077 != 0) {
		return nil, fmt.Errorf("draft or backup is not a private regular file")
	}
	const limit = 16 << 20
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("configuration exceeds size limit")
	}
	return b, nil
}

func preserveDraftBackup(path string, original []byte) error {
	temp, err := writeSyncedTemp(path, original)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	// Link publishes without replacing an earlier backup, even under a race.
	if err := os.Link(temp, path); err != nil {
		if !os.IsExist(err) {
			return err
		}
		existing, err := readDraftFile(path, true)
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, original) {
			return fmt.Errorf("configuration migration backup conflicts; original preserved")
		}
	}
	return syncConfigDirectory(path)
}

func writeSyncedTemp(path string, data []byte) (name string, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	name = f.Name()
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(name)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return name, err
	}
	if err = f.Sync(); err != nil {
		return name, err
	}
	err = f.Close()
	return name, err
}

func syncConfigDirectory(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
