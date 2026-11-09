package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

// notifyReady is called only after restoring last-good and starting the RPC
// server. systemd Type=notify can then order the unprivileged API after recovery.
// An absent socket supports foreground use without claiming supervisor readiness.
func notifyReady() error {
	path := os.Getenv("NOTIFY_SOCKET")
	if path == "" {
		return nil
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("notify readiness: %w", err)
	}
	defer conn.Close()
	if err = conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	if _, err = conn.Write([]byte("READY=1")); err != nil {
		return fmt.Errorf("notify readiness: %w", err)
	}
	return nil
}
