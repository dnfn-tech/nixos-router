package privsep

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type apiLayout struct {
	Path     string `json:"path"`
	UID      uint32 `json:"uid"`
	GID      int    `json:"gid"`
	Complete bool   `json:"complete"`
}

// PrepareAPIState performs an offline, one-time copy. The executor keeps the
// original root state so generated absolute paths and last-good remain intact.
// Only the root daemon entry point calls this in production; tests use their UID.
func PrepareAPIState(state, dest, bootstrap string, uid uint32, gid int) error {
	owner := uint32(os.Geteuid())
	for _, p := range []string{state, dest} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return errors.New("clean absolute state paths required")
		}
	}
	if dest == state || strings.HasPrefix(dest, state+"/") || strings.HasPrefix(state, dest+"/") {
		return errors.New("API and executor state must be disjoint")
	}
	if err := CheckDirectory(filepath.Dir(state), owner); err != nil {
		return err
	}
	if err := os.Mkdir(state, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	if err := CheckDirectory(state, owner); err != nil {
		return err
	}
	if err := CheckDirectory(filepath.Dir(dest), owner); err != nil {
		return err
	}
	migrationLock, err := lockStateFile(filepath.Join(state, "api-layout.lock"))
	if err != nil {
		return err
	}
	defer migrationLock.Close()
	marker := filepath.Join(state, "api-layout.json")
	want := apiLayout{Path: dest, UID: uid, GID: gid}
	var recorded apiLayout
	marked := false
	f, err := privateRegular(marker, owner)
	if err == nil {
		err = json.NewDecoder(io.LimitReader(f, 4096)).Decode(&recorded)
		f.Close()
		if err != nil {
			return fmt.Errorf("invalid API layout record: %w", err)
		}
		if recorded.Path != dest || recorded.UID != uid || recorded.GID != gid {
			return errors.New("API layout differs from recorded migration")
		}
		marked = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if info, err := os.Lstat(dest); err == nil {
		if !marked {
			return errors.New("refusing an unrecorded existing API directory")
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || st.Uid != uid || int(st.Gid) != gid || info.Mode().Perm() != 0700 {
			return errors.New("API directory ownership or permissions changed")
		}
		if !recorded.Complete {
			want.Complete = true
			return writeLayout(marker, want)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if marked && recorded.Complete {
		return errors.New("published API state is missing; refusing to restore stale account data")
	}
	if err := CheckState(state, owner); err != nil {
		return err
	}
	var locks []*os.File
	defer func() {
		for _, f := range locks {
			f.Close()
		}
	}()
	for _, name := range []string{"daemon.lock", "executor.lock"} {
		f, err := lockStateFile(filepath.Join(state, name))
		if err != nil {
			return err
		}
		locks = append(locks, f)
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".router-api-migrate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	names := []string{"config.json", "state.db", "state.db-wal", "state.db-journal"}
	for _, name := range names {
		source := filepath.Join(state, name)
		f, err := privateRegular(source, owner)
		if os.IsNotExist(err) && name == "config.json" {
			if err = CheckDirectory(filepath.Dir(bootstrap), owner); err != nil {
				return err
			}
			f, err = privateRegular(bootstrap, owner)
		}
		if os.IsNotExist(err) && name != "config.json" {
			continue
		}
		if err != nil {
			return err
		}
		err = copyPrivate(f, filepath.Join(stage, name), uid, gid)
		f.Close()
		if err != nil {
			return err
		}
	}
	// Persist intent before publication, so a crash after rename is recoverable
	// without overwriting the newly published API data.
	if err = writeLayout(marker, want); err != nil {
		return err
	}
	if err = os.Chown(stage, int(uid), gid); err != nil {
		return err
	}
	if err = syncDir(stage); err != nil {
		return err
	}
	if err = os.Rename(stage, dest); err != nil {
		return err
	}
	if err = syncDir(filepath.Dir(dest)); err != nil {
		return err
	}
	want.Complete = true
	return writeLayout(marker, want)
}

func lockStateFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, errors.New("untrusted state lock")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("state still owned by an active daemon: %w", err)
	}
	return f, nil
}
func privateRegular(path string, uid uint32) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Uid != uid || st.Nlink != 1 || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, fmt.Errorf("untrusted private state file: %s", path)
	}
	return f, nil
}
func copyPrivate(src *os.File, dest string, uid uint32, gid int) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = io.Copy(f, src); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Chown(int(uid), gid)
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func writeLayout(path string, layout apiLayout) error {
	body, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".api-layout-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
