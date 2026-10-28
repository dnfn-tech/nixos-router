// Package managed owns only explicitly allowlisted network links, one tagged LAN
// bridge, the inet nixos_router nft table and three dedicated protocol services.
package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type Runner interface {
	Run(context.Context, string, ...string) (string, error)
}
type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	b, e := cmd.CombinedOutput()
	return string(b), e
}

type Options struct {
	StateDir       string
	Interfaces     []string
	WiFiInterface  string
	ManagementPort int
	SSHPorts       []int
	CountryCode    string
	Runner         Runner
}
type Result struct {
	Stage          string   `json:"stage"`
	RollbackStatus string   `json:"rollbackStatus"`
	Warnings       []string `json:"warnings,omitempty"`
}
type Manager struct {
	opts      Options
	allowed   map[string]bool
	mu        sync.Mutex
	good      *config.Config
	published atomic.Pointer[config.Config]
}

func New(o Options) (*Manager, error) {
	if !filepath.IsAbs(o.StateDir) || !clean(o.StateDir) {
		return nil, errors.New("absolute clean state directory required")
	}
	if o.ManagementPort < 1 || o.ManagementPort > 65535 {
		return nil, errors.New("invalid management port")
	}
	ports := o.SSHPorts
	if len(ports) == 0 {
		ports = []int{22}
	}
	o.SSHPorts = nil
	seenPorts := map[int]bool{}
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, errors.New("invalid host SSH port")
		}
		if !seenPorts[port] {
			o.SSHPorts = append(o.SSHPorts, port)
			seenPorts[port] = true
		}
	}
	if o.CountryCode == "" {
		o.CountryCode = "US"
	}
	if !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(o.CountryCode) {
		return nil, errors.New("invalid country code")
	}
	if o.Runner == nil {
		o.Runner = commandRunner{}
	}
	m := &Manager{opts: o, allowed: map[string]bool{}}
	for _, s := range o.Interfaces {
		if !ifname.MatchString(s) || s == "lo" || m.allowed[s] {
			return nil, errors.New("invalid or duplicate interface allowlist")
		}
		m.allowed[s] = true
	}
	if len(m.allowed) < 2 {
		return nil, errors.New("at least two wired interfaces required")
	}
	if o.WiFiInterface != "" && (!ifname.MatchString(o.WiFiInterface) || o.WiFiInterface == "lo" || m.allowed[o.WiFiInterface]) {
		return nil, errors.New("WiFi must be a distinct valid interface")
	}
	if e := os.MkdirAll(m.base(), 0700); e != nil {
		return nil, e
	}
	if e := os.Chmod(m.base(), 0700); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(m.lastGood())
	if e == nil {
		var c config.Config
		if e = json.Unmarshal(b, &c); e != nil {
			return nil, fmt.Errorf("last-good corrupt: %w", e)
		}
		if e = m.validate(c); e != nil {
			return nil, fmt.Errorf("last-good invalid: %w", e)
		}
		m.good = &c
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	return m, nil
}

// AppliedConfig returns a deep snapshot without waiting for an ongoing Apply.
// false means this process has not verified a successful activation or recovery.
func (m *Manager) AppliedConfig() (config.Config, bool) {
	c := m.published.Load()
	if c == nil {
		return config.Config{}, false
	}
	return clone(*c), true
}
func (m *Manager) publish(c *config.Config) {
	if c == nil {
		m.published.Store(nil)
		return
	}
	v := clone(*c)
	m.published.Store(&v)
}
func (m *Manager) base() string     { return filepath.Join(m.opts.StateDir, "managed") }
func (m *Manager) lastGood() string { return filepath.Join(m.base(), "last-good.json") }
func (m *Manager) run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	s, e := m.opts.Runner.Run(ctx, name, args...)
	if e != nil {
		return s, fmt.Errorf("%s failed: %w", name, e)
	}
	return s, nil
}
func (m *Manager) cmd(ctx context.Context, name string, args ...string) error {
	_, e := m.run(ctx, name, args...)
	return e
}
func clone(c config.Config) config.Config {
	b, _ := json.Marshal(c)
	var out config.Config
	_ = json.Unmarshal(b, &out)
	return out
}

// transactionLock also protects against a second routerd process. Kernel locks
// are released automatically on crash; no stale PID file is treated as ownership.
func (m *Manager) transactionLock() (*os.File, error) {
	f, e := os.OpenFile(filepath.Join(m.base(), "apply.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("managed transaction busy: %w", e)
	}
	b, e := os.ReadFile(m.lastGood())
	if e == nil {
		var c config.Config
		e = json.Unmarshal(b, &c)
		if e == nil {
			e = m.validate(c)
		}
		if e != nil {
			f.Close()
			return nil, fmt.Errorf("last-good invalid: %w", e)
		}
		m.good = &c
	} else if !errors.Is(e, os.ErrNotExist) {
		f.Close()
		return nil, e
	}
	return f, nil
}
func (m *Manager) Apply(ctx context.Context, c config.Config) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, e := m.transactionLock()
	if e != nil {
		return Result{Stage: "lock", RollbackStatus: "not-needed"}, e
	}
	defer lock.Close()
	return m.apply(ctx, clone(c))
}
func (m *Manager) Restore(ctx context.Context, bootstrap config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, e := m.transactionLock()
	if e != nil {
		return e
	}
	defer lock.Close()
	c := clone(bootstrap)
	if m.good != nil {
		c = clone(*m.good)
	}
	_, e = m.apply(ctx, c)
	return e
}
func (m *Manager) apply(ctx context.Context, c config.Config) (res Result, err error) {
	// Called only under the process mutex and cross-process transaction lock.
	// Defer until activation/recovery no longer needs intermediate candidates.
	defer func() {
		if cleanupErr := m.pruneGenerations(2); cleanupErr != nil {
			res.Warnings = append(res.Warnings, "generation cleanup failed: "+cleanupErr.Error())
		}
	}()
	res.Stage = "validate"
	res.RollbackStatus = "not-needed"
	if err = m.validate(c); err != nil {
		return
	}
	res.Stage = "preflight"
	dir, e := m.prepare(ctx, c)
	if e != nil {
		return res, e
	}
	previous := m.good
	res.Stage = "activate"
	err = m.activate(ctx, c, previous, dir)
	if err == nil {
		res.Stage = "persist"
		b, _ := json.MarshalIndent(c, "", "  ")
		err = atomicFile(m.lastGood(), append(b, '\n'))
	}
	if err != nil {
		// Request cancellation must not cancel recovery of a partially changed network.
		recovery, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res.RollbackStatus = "failed"
		var re error
		if previous != nil {
			var oldDir string
			oldDir, re = m.prepare(recovery, *previous)
			if re == nil {
				re = m.activate(recovery, *previous, &c, oldDir)
			}
		} else {
			re = m.cleanup(recovery, c)
		}
		// A rename can succeed before directory fsync fails. Restore the durable
		// pointer too, so reboot cannot select the failed candidate.
		if res.Stage == "persist" {
			var pe error
			if previous != nil {
				b, _ := json.MarshalIndent(previous, "", "  ")
				pe = atomicFile(m.lastGood(), append(b, '\n'))
			} else {
				pe = os.Remove(m.lastGood())
				if errors.Is(pe, os.ErrNotExist) {
					pe = nil
				}
			}
			re = errors.Join(re, pe)
		}
		if re == nil {
			res.RollbackStatus = "succeeded"
			m.publish(previous)
		} else {
			m.publish(nil)
			err = fmt.Errorf("%w; rollback failed: %v", err, re)
		}
		return
	}
	m.good = &c
	m.publish(&c)
	res.Stage = "complete"
	res.Warnings = []string{"SSH and system identity remain controlled by NixOS"}
	if c.WAN.Mode == "dhcp" {
		ready, observeErr := m.wanAddressReady(ctx, c.WAN.Interface)
		if observeErr != nil {
			res.Stage = "wan-status-unknown"
			res.Warnings = append(res.Warnings, "WAN address observation failed: "+observeErr.Error()+"; configuration remains applied, WAN connectivity is unverified")
		} else if !ready {
			res.Stage = "awaiting-wan-lease"
			res.Warnings = append(res.Warnings, "WAN DHCP client started; awaiting an IPv4 lease; WAN connectivity is unverified")
		}
	}
	return
}
func atomicFile(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".pending-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (m *Manager) prepare(ctx context.Context, c config.Config) (string, error) {
	// Refuse competing generic managers at startup as well as later applies.
	for _, unit := range []string{"NetworkManager.service", "systemd-networkd.service", "dhcpcd.service", "connman.service", "ConnMan.service"} {
		out, e := m.run(ctx, "systemctl", "is-active", unit)
		if strings.TrimSpace(out) == "active" || strings.TrimSpace(out) == "activating" {
			return "", fmt.Errorf("conflicting network manager %s", unit)
		}
		if e != nil && ctx.Err() != nil {
			return "", e
		}
	}
	// Inspect ownership before touching any interface. Errors are not absence.
	s, e := m.run(ctx, "ip", "-j", "-d", "link", "show")
	if e != nil {
		return "", e
	}
	var links []struct {
		IfName   string `json:"ifname"`
		IfAlias  string `json:"ifalias"`
		Master   string `json:"master"`
		LinkInfo struct {
			Kind string `json:"info_kind"`
		} `json:"linkinfo"`
	}
	if e = json.Unmarshal([]byte(s), &links); e != nil {
		return "", fmt.Errorf("read interface ownership: %w", e)
	}
	for _, l := range links {
		if l.IfName == c.WAN.Interface && l.Master != "" {
			return "", errors.New("WAN interface must not be attached to any master")
		}
		if l.IfName == c.LAN.BridgeName && (l.IfAlias != "nixos-router-managed" || l.LinkInfo.Kind != "bridge") {
			return "", errors.New("refusing to adopt an existing foreign LAN bridge")
		}
		owned := m.allowed[l.IfName] || l.IfName == m.opts.WiFiInterface
		if owned && l.Master != "" && l.Master != c.LAN.BridgeName && (m.good == nil || l.Master != m.good.LAN.BridgeName) {
			return "", errors.New("managed interface is attached to a foreign master")
		}
	}
	dir, e := os.MkdirTemp(m.base(), "generation-")
	if e != nil {
		return "", e
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = os.RemoveAll(dir)
		}
	}()
	for n, s := range m.render(c) {
		if n == "wan.env" && c.WAN.Mode == "dhcp" {
			s += "WAN_GENERATION='" + filepath.Base(dir) + "'\n"
		}
		if e = atomicFile(filepath.Join(dir, n), []byte(s)); e != nil {
			return "", e
		}
	}
	if e = m.cmd(ctx, "nft", "--check", "-f", filepath.Join(dir, "firewall.nft")); e != nil {
		return "", e
	}
	if c.DNS.EnableDNSMasq || c.LAN.DHCP.Enable {
		if e = m.cmd(ctx, "dnsmasq", "--test", "--conf-file="+filepath.Join(dir, "dnsmasq.conf")); e != nil {
			return "", e
		}
	}
	prepared = true
	return dir, nil
}
func (m *Manager) switchFiles(dir string) error {
	tmp := filepath.Join(m.base(), ".current-next")
	if e := os.Remove(tmp); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e := os.Symlink(filepath.Base(dir), tmp); e != nil {
		return e
	}
	return os.Rename(tmp, filepath.Join(m.base(), "current"))
}
func (m *Manager) activate(ctx context.Context, c config.Config, old *config.Config, dir string) error {
	for _, unit := range []string{"hostapd", "dnsmasq", "wan"} {
		if e := m.cmd(ctx, "systemctl", "stop", "nixos-router-"+unit+".service"); e != nil {
			return e
		}
	}
	if old != nil {
		if e := m.detach(ctx, *old); e != nil {
			return e
		}
		if old.LAN.BridgeName != c.LAN.BridgeName {
			if e := m.removeBridge(ctx, old.LAN.BridgeName); e != nil {
				return e
			}
		}
	}
	if e := m.switchFiles(dir); e != nil {
		return e
	}
	s, e := m.run(ctx, "ip", "-j", "link", "show")
	if e != nil {
		return e
	}
	var links []struct {
		Name   string `json:"ifname"`
		Alias  string `json:"ifalias"`
		Master string `json:"master"`
	}
	if e = json.Unmarshal([]byte(s), &links); e != nil {
		return e
	}
	exists := false
	for _, l := range links {
		if l.Name == c.LAN.BridgeName {
			if l.Alias != "nixos-router-managed" {
				return errors.New("LAN bridge ownership changed during activation")
			}
			exists = true
		}
	}
	if exists {
		// Saved history cannot describe a candidate interrupted before commit.
		// Reconcile observed members, but only allowlisted wired interfaces on
		// our tagged bridge; unrelated links and WiFi remain outside this loop.
		desired := map[string]bool{}
		for _, port := range c.LAN.Ports {
			desired[port] = true
		}
		for _, link := range links {
			if link.Master == c.LAN.BridgeName && m.allowed[link.Name] && !desired[link.Name] {
				if e = m.cmd(ctx, "ip", "link", "set", "dev", link.Name, "nomaster"); e != nil {
					return e
				}
			}

			// The AP interface has separate authorization. If an interrupted
			// candidate enabled it, stopping an already-dead hostapd may not
			// detach it, so disabled WiFi also reconciles observed membership.
			if link.Master == c.LAN.BridgeName && link.Name == m.opts.WiFiInterface && !c.WiFi.Enable {
				if e = m.cmd(ctx, "ip", "link", "set", "dev", link.Name, "nomaster"); e != nil {
					return e
				}
			}
		}
	}
	if !exists {
		// The bridge driver's RTM_NEWLINK path can ignore IFLA_IFALIAS on
		// creation. Establish ownership explicitly before attaching ports.
		if e = m.cmd(ctx, "ip", "link", "add", "name", c.LAN.BridgeName, "type", "bridge"); e != nil {
			return e
		}
		if e = m.cmd(ctx, "ip", "link", "set", "dev", c.LAN.BridgeName, "alias", "nixos-router-managed"); e != nil {
			// Only this branch has proof that this transaction just created
			// the untagged link. Never extend adoption/deletion to pre-existing
			// untagged bridges in prepare or removeBridge.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			cleanupErr := m.cmd(cleanupCtx, "ip", "link", "delete", "dev", c.LAN.BridgeName)
			cancel()
			return errors.Join(e, cleanupErr)
		}
	}
	for _, p := range c.LAN.Ports {
		if e = m.cmd(ctx, "ip", "-4", "addr", "flush", "dev", p); e != nil {
			return e
		}
		if e = m.cmd(ctx, "ip", "link", "set", "dev", p, "master", c.LAN.BridgeName); e != nil {
			return e
		}
		if e = m.cmd(ctx, "ip", "link", "set", "dev", p, "up"); e != nil {
			return e
		}
	}
	if e = m.cmd(ctx, "ip", "-4", "addr", "flush", "dev", c.LAN.BridgeName); e != nil {
		return e
	}
	if e = m.cmd(ctx, "ip", "addr", "add", c.LAN.IPv4CIDR, "dev", c.LAN.BridgeName); e != nil {
		return e
	}
	if e = m.cmd(ctx, "ip", "link", "set", "dev", c.LAN.BridgeName, "up"); e != nil {
		return e
	}
	if e = m.cmd(ctx, "ip", "-4", "addr", "flush", "dev", c.WAN.Interface); e != nil {
		return e
	}
	if e = m.cmd(ctx, "ip", "-4", "route", "flush", "dev", c.WAN.Interface); e != nil {
		return e
	}
	if e = m.cmd(ctx, "ip", "link", "set", "dev", c.WAN.Interface, "up"); e != nil {
		return e
	}
	if e = m.cmd(ctx, "nft", "-f", filepath.Join(dir, "firewall.nft")); e != nil {
		return e
	}
	if c.WAN.Mode == "static" {
		if e = m.cmd(ctx, "ip", "addr", "add", c.WAN.Static.AddressCIDR, "dev", c.WAN.Interface); e != nil {
			return e
		}
		if c.WAN.Static.Gateway != "" {
			if e = m.cmd(ctx, "ip", "route", "replace", "default", "via", c.WAN.Static.Gateway, "dev", c.WAN.Interface); e != nil {
				return e
			}
		}
	} else {
		if e = m.cmd(ctx, "systemctl", "restart", "nixos-router-wan.service"); e != nil {
			return e
		}
		if e = m.cmd(ctx, "systemctl", "is-active", "--quiet", "nixos-router-wan.service"); e != nil {
			return e
		}
	}
	if c.DNS.EnableDNSMasq || c.LAN.DHCP.Enable {
		if e = m.cmd(ctx, "systemctl", "restart", "nixos-router-dnsmasq.service"); e != nil {
			return e
		}
		if e = m.cmd(ctx, "systemctl", "is-active", "--quiet", "nixos-router-dnsmasq.service"); e != nil {
			return e
		}
	}
	if c.WiFi.Enable {
		if e = m.cmd(ctx, "systemctl", "restart", "nixos-router-hostapd.service"); e != nil {
			return e
		}
		if e = m.cmd(ctx, "systemctl", "is-active", "--quiet", "nixos-router-hostapd.service"); e != nil {
			return e
		}
		if e = m.waitAPReady(ctx); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) detach(ctx context.Context, c config.Config) error {
	for _, p := range c.LAN.Ports {
		if e := m.cmd(ctx, "ip", "link", "set", "dev", p, "nomaster"); e != nil {
			return e
		}
	}
	if c.WiFi.Enable {
		if e := m.cmd(ctx, "ip", "link", "set", "dev", m.opts.WiFiInterface, "nomaster"); e != nil {
			return e
		}
	}
	if e := m.cmd(ctx, "ip", "-4", "addr", "flush", "dev", c.WAN.Interface); e != nil {
		return e
	}
	return m.cmd(ctx, "ip", "-4", "route", "flush", "dev", c.WAN.Interface)
}
func (m *Manager) removeBridge(ctx context.Context, name string) error {
	s, e := m.run(ctx, "ip", "-j", "link", "show")
	if e != nil {
		return e
	}
	var links []struct {
		Name  string `json:"ifname"`
		Alias string `json:"ifalias"`
	}
	if e = json.Unmarshal([]byte(s), &links); e != nil {
		return e
	}
	for _, l := range links {
		if l.Name == name {
			if l.Alias != "nixos-router-managed" {
				return errors.New("refusing to delete unowned bridge")
			}
			return m.cmd(ctx, "ip", "link", "delete", "dev", name)
		}
	}
	return nil
}
func (m *Manager) cleanup(ctx context.Context, c config.Config) error {
	var errs []error
	for _, u := range []string{"wan", "dnsmasq", "hostapd"} {
		errs = append(errs, m.cmd(ctx, "systemctl", "stop", "nixos-router-"+u+".service"))
	}
	errs = append(errs, m.detach(ctx, c), m.removeBridge(ctx, c.LAN.BridgeName))
	if _, e := m.run(ctx, "nft", "list", "table", "inet", "nixos_router"); e == nil {
		errs = append(errs, m.cmd(ctx, "nft", "delete", "table", "inet", "nixos_router"))
	}
	return errors.Join(errs...)
}

// wanAddressReady observes address acquisition separately from starting the DHCP
// client. A valid applied configuration must survive an unavailable upstream.
func (m *Manager) wanAddressReady(ctx context.Context, iface string) (bool, error) {
	out, e := m.run(ctx, "ip", "-j", "-4", "addr", "show", "dev", iface)
	if e != nil {
		return false, e
	}
	var links []struct {
		Addresses []struct {
			Family string `json:"family"`
			Local  string `json:"local"`
			Scope  string `json:"scope"`
		} `json:"addr_info"`
	}
	if e = json.Unmarshal([]byte(out), &links); e != nil {
		return false, fmt.Errorf("invalid ip address response: %w", e)
	}
	for _, l := range links {
		for _, a := range l.Addresses {
			if a.Family == "inet" && a.Scope == "global" && ipv4(a.Local) {
				return true, nil
			}
		}
	}
	return false, nil
}

// A running hostapd process may still be initializing or have failed to enable
// its radio. Commit only after the dedicated control socket confirms AP state.
func (m *Manager) waitAPReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var lastErr error
	for {
		out, e := m.run(ctx, "hostapd_cli", "-p", filepath.Join(m.base(), "hostapd-control"), "-i", m.opts.WiFiInterface, "status")
		lastErr = e
		if e == nil {
			for _, line := range strings.Split(out, "\n") {
				if strings.TrimSpace(line) == "state=ENABLED" {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("hostapd did not reach state=ENABLED: %w (last control error: %v)", ctx.Err(), lastErr)
		case <-tick.C:
		}
	}
}

// Keep the active target regardless of its age plus a small diagnostic history.
// Only direct generation directories are eligible; symlinks/foreign files are
// never traversed. This runs after rollback, not while a candidate is needed.
func (m *Manager) pruneGenerations(history int) error {
	current, e := os.Readlink(filepath.Join(m.base(), "current"))
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if current != "" && (filepath.Base(current) != current || !strings.HasPrefix(current, "generation-")) {
		return errors.New("unexpected current generation target")
	}
	entries, e := os.ReadDir(m.base())
	if e != nil {
		return e
	}
	type generation struct {
		name     string
		modified time.Time
	}
	var old []generation
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "generation-") || entry.Name() == current {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		old = append(old, generation{entry.Name(), info.ModTime()})
	}
	sort.Slice(old, func(i, j int) bool {
		if old[i].modified.Equal(old[j].modified) {
			return old[i].name > old[j].name
		}
		return old[i].modified.After(old[j].modified)
	})
	var errs []error
	for i := history; i < len(old); i++ {
		errs = append(errs, os.RemoveAll(filepath.Join(m.base(), old[i].name)))
	}
	return errors.Join(errs...)
}
