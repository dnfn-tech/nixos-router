package privsep

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func socketPath(t *testing.T) string {
	t.Helper()
	// Keep AF_UNIX paths short and under a private directory.
	d, err := os.MkdirTemp("/tmp", "router-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return filepath.Join(d, "executor.sock")
}

func TestAuthenticatedRoundTrip(t *testing.T) {
	p := socketPath(t)
	l, err := Listen(p, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan error, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(time.Second))
		b := make([]byte, 4)
		_, e = io.ReadFull(c, b)
		if e == nil && string(b) != "ping" {
			e = io.ErrUnexpectedEOF
		}
		if e == nil {
			_, e = c.Write([]byte("pong"))
		}
		done <- e
	}()
	c, err := DialContext(context.Background(), p, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	if _, err = c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	if _, err = io.ReadFull(c, b); err != nil || string(b) != "pong" {
		t.Fatalf("reply %q: %v", b, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions: %v %v", fi, err)
	}
}

func TestRejectUnexpectedServerUID(t *testing.T) {
	p := socketPath(t)
	l, err := Listen(p, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := DialContext(context.Background(), p, uint32(os.Geteuid()+1))
	if c != nil {
		c.Close()
		t.Fatal("returned connection to untrusted server")
	}
	if err == nil || !strings.Contains(err.Error(), "peer UID") {
		t.Fatalf("expected identity rejection, got %v", err)
	}
}

func TestRejectUnexpectedClientUID(t *testing.T) {
	p := socketPath(t)
	l, err := Listen(p, uint32(os.Geteuid()+1))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan net.Conn, 1)
	go func() { c, _ := l.Accept(); done <- c }()
	c, err := net.Dial("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 1)
	_, err = c.Read(b)
	if err == nil {
		t.Fatal("unauthorized peer not closed")
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("unauthorized connection left open")
	}
	l.Close()
	if c := <-done; c != nil {
		c.Close()
		t.Fatal("unauthorized peer returned from Accept")
	}
}

func TestListenRejectUnsafePaths(t *testing.T) {
	for _, kind := range []string{"writable-parent", "writable-ancestor", "symlink-parent", "existing-file", "existing-socket", "relative"} {
		t.Run(kind, func(t *testing.T) {
			p := socketPath(t)
			switch kind {
			case "writable-parent":
				if err := os.Chmod(filepath.Dir(p), 0777); err != nil {
					t.Fatal(err)
				}
			case "writable-ancestor":
				middle := filepath.Join(filepath.Dir(p), "shared")
				private := filepath.Join(middle, "private")
				if err := os.MkdirAll(private, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(middle, 0777); err != nil {
					t.Fatal(err)
				}
				p = filepath.Join(private, "executor.sock")
			case "symlink-parent":
				alias := filepath.Join(filepath.Dir(p), "alias")
				if err := os.Symlink(filepath.Dir(p), alias); err != nil {
					t.Fatal(err)
				}
				p = filepath.Join(alias, "executor.sock")
			case "existing-file":
				if err := os.WriteFile(p, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "existing-socket":
				l, err := net.Listen("unix", p)
				if err != nil {
					t.Fatal(err)
				}
				defer l.Close()
			case "relative":
				p = "executor.sock"
			}
			l, err := Listen(p, uint32(os.Geteuid()))
			if l != nil {
				l.Close()
			}
			if err == nil {
				t.Fatal("unsafe socket path accepted")
			}
			if kind == "existing-file" {
				b, e := os.ReadFile(p)
				if e != nil || string(b) != "preserve" {
					t.Fatal("existing file altered")
				}
			}
		})
	}
}

func TestCloseDoesNotRemoveReplacement(t *testing.T) {
	p := socketPath(t)
	l, err := Listen(p, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err = os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "replacement" {
		t.Fatalf("Close removed replacement file: %q %v", b, err)
	}
}
