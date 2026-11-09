package privsep

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// CheckState rejects inherited state that a lower-privilege account can edit.
// Call before managed.New: that constructor reads last-good and chmods its base.
// We inspect links without following them; only the engine's relative generation
// pointers are allowed. This is validation, not ownership-changing migration.
func CheckState(dir string, uid uint32) error {
	if err := CheckDirectory(dir, uid); err != nil {
		return err
	}
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uid {
			return fmt.Errorf("untrusted state owner: %s", path)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if rel != "managed/current" && rel != "managed/.current-next" {
				return fmt.Errorf("unexpected state symlink: %s", path)
			}
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.Base(target) != target || !strings.HasPrefix(target, "generation-") {
				return fmt.Errorf("invalid generation pointer: %s", path)
			}
			return nil
		}
		// hostapd creates a group-accessible control directory/socket. Only root's
		// group is allowed here, inside the otherwise private managed directory.
		control := rel == "managed/hostapd-control" || filepath.Dir(rel) == "managed/hostapd-control"
		rootControl := control && uid == 0 && st.Gid == 0
		// dnsmasq creates read-only metadata with mode 0644 even under a
		// restrictive service umask. The private parent still prevents API
		// access. Never extend this exception to configs or writable files.
		protocolMetadata := (rel == "dnsmasq.leases" || rel == "managed/dnsmasq.pid") && info.Mode().IsRegular() && info.Mode().Perm()&0133 == 0
		if info.Mode().Perm()&0077 != 0 && !protocolMetadata && !(rootControl && info.Mode().Perm()&0007 == 0) {
			return fmt.Errorf("state is not private: %s", path)
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode().IsRegular() && st.Nlink == 1 {
			return nil
		}
		if rootControl && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		return fmt.Errorf("unsupported state file type or hardlink: %s", path)
	})
}
