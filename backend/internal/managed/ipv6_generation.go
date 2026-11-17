package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// Only the non-secret data needed by protocol callbacks lives in a generation.
// The root executor's state/ancestor validation remains the entrypoint boundary.
type ipv6RuntimeConfig struct {
	WiFiLAN     string `json:"wifiLan,omitempty"`
	Enabled     bool   `json:"enabled"`
	Mode        string `json:"mode"`
	WAN         string `json:"wan"`
	PhysicalWAN string `json:"physicalWan"`
	LAN         string `json:"lan"`
	PD          bool   `json:"pd"`
	RA          bool   `json:"ra"`
}

func ipv6ContextFor(c config.Config) ipv6RuntimeConfig {
	wan := c.WAN.Interface
	if c.IPv6.WANMode == "pppoe6" {
		wan = managedPPPInterface
	}
	return ipv6RuntimeConfig{Enabled: c.IPv6.Enable, Mode: c.IPv6.WANMode, WAN: wan, PhysicalWAN: c.WAN.Interface, LAN: c.LAN.BridgeName, PD: c.IPv6.LANPD, RA: c.IPv6.LANRA}
}

var ipv6GenerationName = regexp.MustCompile(`^generation-[a-zA-Z0-9_-]+$`)

func ipv6ServiceEnvironment(c config.Config, generation string) (string, error) {
	v := ipv6ContextFor(c)
	if !ipv6GenerationName.MatchString(generation) || !ifname.MatchString(v.WAN) {
		return "", errors.New("invalid IPv6 service identity")
	}
	enabled, pd := 0, 0
	if v.Enabled {
		switch v.Mode {
		case "dhcpv6", "slaac", "pppoe6":
		default:
			return "", errors.New("invalid IPv6 service mode")
		}
		enabled = 1
		if v.PD {
			pd = 1
		}
	} else {
		v.Mode = "disabled"
	}
	return fmt.Sprintf("WAN6_ENABLED='%d'\nWAN6_MODE='%s'\nWAN6_INTERFACE='%s'\nWAN6_PD='%d'\nWAN6_GENERATION='%s'\n", enabled, v.Mode, v.WAN, pd, generation), nil
}

func (m *Manager) withIPv6Generation(ctx context.Context, generation, iface string, fn func(ipv6RuntimeConfig) error) error {
	if !ipv6GenerationName.MatchString(generation) {
		return errors.New("invalid IPv6 generation")
	}
	return m.withIPv6StateLock(ctx, func() error {
		current, err := os.Readlink(filepath.Join(m.base(), "current"))
		if err != nil {
			return err
		}
		if current != generation {
			return nil
		} // Superseded callback, no mutation.
		dir := filepath.Join(m.base(), generation)
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Geteuid()) {
			return errors.New("unprotected IPv6 generation")
		}
		f, err := openIPv6PrivateFile(filepath.Join(dir, "ipv6-context.json"), false)
		if err != nil {
			return err
		}
		defer f.Close()
		decoder := json.NewDecoder(io.LimitReader(f, 16385))
		decoder.DisallowUnknownFields()
		var c ipv6RuntimeConfig
		if err = decoder.Decode(&c); err != nil {
			return errors.New("invalid IPv6 runtime context")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return errors.New("invalid IPv6 runtime context")
		}
		if !c.Enabled || !m.allowed[c.PhysicalWAN] || c.PhysicalWAN == m.opts.WiFiInterface || c.PhysicalWAN == managedPPPInterface {
			return errors.New("IPv6 context has no authorized physical WAN")
		}
		wantWAN := c.PhysicalWAN
		switch c.Mode {
		case "pppoe6":
			wantWAN = managedPPPInterface
		case "dhcpv6", "slaac":
		default:
			return errors.New("invalid IPv6 runtime mode")
		}
		if c.WAN != wantWAN || iface != wantWAN || !ifname.MatchString(c.LAN) || m.allowed[c.LAN] || c.LAN == m.opts.WiFiInterface || c.LAN == "lo" || c.LAN == managedPPPInterface {
			return errors.New("IPv6 callback interface mismatch")
		}
		if !m.validIPv6WiFiLAN(c) {
			return errors.New("unauthorized IPv6 WiFi LAN")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return fn(c)
	})
}

// switchFiles takes this same lock, after stopping protocol services. Never
// hold it while waiting for systemctl stop: the service may await its callback.
func (m *Manager) withIPv6StateLock(ctx context.Context, fn func() error) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	f, err := openIPv6PrivateFile(filepath.Join(m.base(), ".ipv6.lock"), true)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err = ctx.Err(); err != nil {
		return err
	}
	return fn()
}

func openIPv6PrivateFile(path string, create bool) (*os.File, error) {
	return openIPv6PrivateFileLimit(path, create, 16384)
}

func openIPv6PrivateFileLimit(path string, create bool, limit int64) (*os.File, error) {
	flags := syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if create {
		flags = syscall.O_RDWR | syscall.O_CREAT | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	}
	fd, err := syscall.Open(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || (!create && info.Size() > limit) {
		f.Close()
		return nil, errors.New("unprotected IPv6 runtime file")
	}
	return f, nil
}

func (c ipv6RuntimeConfig) isLAN(iface string) bool {
	return iface == c.LAN || (c.WiFiLAN != "" && iface == c.WiFiLAN)
}
func (m *Manager) validIPv6WiFiLAN(c ipv6RuntimeConfig) bool {
	return c.WiFiLAN == "" || (m.opts.WiFiInterface != "" && c.WiFiLAN == m.opts.WiFiInterface && c.WiFiLAN != c.WAN && c.WiFiLAN != c.LAN && c.WiFiLAN != managedPPPInterface && c.WiFiLAN != "lo")
}
