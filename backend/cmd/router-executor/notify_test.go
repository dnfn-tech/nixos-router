package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNotifyReady(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "router-notify-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "notify.sock")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("NOTIFY_SOCKET", path)
	if err := notifyReady(); err != nil {
		t.Fatal(err)
	}
	listener.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1024)
	n, _, err := listener.ReadFromUnix(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "READY=1" {
		t.Fatalf("unexpected notification: %q", buffer[:n])
	}
}
func TestNotifyReadyWithoutSupervisor(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := notifyReady(); err != nil {
		t.Fatal(err)
	}
}
func TestNotifyReadyReportsDeliveryFailure(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "absent.sock"))
	if err := notifyReady(); err == nil {
		t.Fatal("lost readiness treated as delivered")
	}
}
