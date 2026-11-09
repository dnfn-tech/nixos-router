// Package privsep implements the local trust boundary between the HTTP API and
// the network executor. It never accepts an identity supplied in a message.
package privsep

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

type authenticatedListener struct {
	*net.UnixListener
	uid uint32
}

// Listen binds a new socket in a protected directory. The caller owns directory
// creation and, for distinct service UIDs, granting socket access. Initially only
// the creating UID can connect (0600). Socket access never replaces peer checks.
// Existing paths are never removed, including stale sockets: startup cleanup
// belongs to the service supervisor after it has established exclusive ownership.
func Listen(path string, allowedUID uint32) (net.Listener, error) {
	if err := checkParent(path, uint32(os.Geteuid())); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return nil, fmt.Errorf("socket path must not exist: %s", path)
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	// Never unlink by pathname on Close: a supervisor may have replaced it.
	l.SetUnlinkOnClose(false)
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return &authenticatedListener{UnixListener: l, uid: allowedUID}, nil
}

func (l *authenticatedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		if err = checkPeer(c, l.uid); err != nil {
			c.Close()
			continue
		}
		return c, nil
	}
}

// DialContext verifies the executor's kernel UID before returning a connection.
// A compromised path cannot substitute a server running under another account.
func DialContext(ctx context.Context, path string, serverUID uint32) (net.Conn, error) {
	// Validate directory safety separately from the expected peer UID. Peer
	// authentication, not directory ownership, establishes the server identity.
	if err := checkParent(path, serverUID, uint32(os.Geteuid())); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("not a Unix socket")
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	u, ok := c.(*net.UnixConn)
	if !ok {
		c.Close()
		return nil, fmt.Errorf("not a Unix connection")
	}
	if err = checkPeer(u, serverUID); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func checkPeer(c *net.UnixConn, want uint32) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var cred *syscall.Ucred
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		cred, sockErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return err
	}
	if sockErr != nil {
		return sockErr
	}
	if cred == nil || cred.Uid != want {
		return fmt.Errorf("peer UID does not match authorized service")
	}
	return nil
}

func checkParent(path string, uids ...uint32) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("socket requires a clean absolute path")
	}
	parent := filepath.Dir(path)
	for dir := parent; ; dir = filepath.Dir(dir) {
		fi, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return fmt.Errorf("socket parent path contains a non-directory or symlink")
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("cannot determine socket path owner")
		}
		serviceOwned := false
		for _, uid := range uids {
			serviceOwned = serviceOwned || st.Uid == uid
		}
		writable := fi.Mode().Perm()&0022 != 0
		if dir == parent {
			if !serviceOwned || writable {
				return fmt.Errorf("socket parent must be service-owned and not writable by other users")
			}
		} else {
			// A writable ancestor can replace a protected child directory.
			// A root-owned sticky directory (e.g. /tmp) protects that child.
			stickyRoot := st.Uid == 0 && fi.Mode()&os.ModeSticky != 0
			if (!serviceOwned && st.Uid != 0) || (writable && !stickyRoot) {
				return fmt.Errorf("socket ancestor is not protected from replacement")
			}
		}
		if dir == "/" {
			break
		}
	}
	return nil
}
