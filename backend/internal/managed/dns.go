package managed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type dnsRuntimeConfig struct {
	PhysicalWAN string `json:"physicalWan"`
	WAN         string `json:"wan"`
	Mode        string `json:"mode"`
}

func dnsContextFor(c config.Config) dnsRuntimeConfig {
	wan := c.WAN.Interface
	if c.WAN.Mode == "pppoe" {
		wan = managedPPPInterface
	}
	return dnsRuntimeConfig{c.WAN.Interface, wan, c.WAN.Mode}
}

func validProtocolDNS(a netip.Addr) bool {
	return a.IsValid() && !a.Is4In6() && !a.IsUnspecified() && !a.IsLoopback() && !a.IsMulticast() && (a.IsGlobalUnicast() || a.IsLinkLocalUnicast())
}

// Called with the generation lock held. Each protocol owns one private source;
// dnsmasq reads only the atomically published union, never competing files.
func (m *Manager) publishDNSSource(source, content string) error {
	if source != "wan4" && source != "wan6" {
		return errors.New("invalid DNS source")
	}
	values := map[string]string{source: content}
	other := "wan4"
	if source == other {
		other = "wan6"
	}
	f, err := openIPv6PrivateFile(filepath.Join(m.base(), other+"-resolv.conf"), false)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		b, e := io.ReadAll(io.LimitReader(f, 16385))
		f.Close()
		if e != nil {
			return e
		}
		values[other] = string(b)
	}
	var merged strings.Builder
	seen := map[string]bool{}
	for _, name := range []string{"wan4", "wan6"} {
		value := values[name]
		if len(value) > 16384 {
			return errors.New("DNS source too large")
		}
		count := 0
		for _, line := range strings.Split(value, "\n") {
			if line == "" {
				continue
			}
			count++
			fields := strings.Fields(line)
			if count > 128 || len(fields) != 2 || fields[0] != "nameserver" {
				return errors.New("invalid DNS source")
			}
			a, e := netip.ParseAddr(fields[1])
			if e != nil || !validProtocolDNS(a) || (name == "wan4") != a.Is4() || (a.Zone() != "" && (!a.IsLinkLocalUnicast() || !ifname.MatchString(a.Zone()))) || (a.Is6() && a.IsLinkLocalUnicast() && a.Zone() == "") {
				return errors.New("invalid DNS source address")
			}
			key := a.String()
			if !seen[key] {
				seen[key] = true
				merged.WriteString("nameserver " + key + "\n")
			}
		}
	}
	if err = atomicFile(filepath.Join(m.base(), source+"-resolv.conf"), []byte(content)); err != nil {
		return err
	}
	return atomicFile(filepath.Join(m.base(), "wan-resolv.conf"), []byte(merged.String()))
}

// Called only while protocol callbacks are excluded by the generation lock.
func (m *Manager) resetDNS() error {
	for _, name := range []string{"wan4-resolv.conf", "wan6-resolv.conf", "wan-resolv.conf"} {
		if err := atomicFile(filepath.Join(m.base(), name), nil); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) HandleIPv4DNS(ctx context.Context, generation, iface, event, servers string) error {
	if !ipv6GenerationName.MatchString(generation) || (event != "bound" && event != "unbound") {
		return errors.New("invalid DNS callback")
	}
	return m.withIPv6StateLock(ctx, func() error {
		current, err := os.Readlink(filepath.Join(m.base(), "current"))
		if err != nil {
			return err
		}
		if current != generation {
			return nil
		}
		dir := filepath.Join(m.base(), generation)
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Geteuid()) {
			return errors.New("unprotected DNS generation")
		}
		f, err := openIPv6PrivateFile(filepath.Join(dir, "dns-context.json"), false)
		if err != nil {
			return err
		}
		defer f.Close()
		decoder := json.NewDecoder(io.LimitReader(f, 16385))
		decoder.DisallowUnknownFields()
		var c dnsRuntimeConfig
		var extra any
		if decoder.Decode(&c) != nil || decoder.Decode(&extra) != io.EOF {
			return errors.New("invalid DNS runtime context")
		}
		want := c.PhysicalWAN
		if c.Mode == "pppoe" {
			want = managedPPPInterface
		} else if c.Mode != "dhcp" {
			return errors.New("DNS protocol is not enabled")
		}
		if !m.allowed[c.PhysicalWAN] || c.PhysicalWAN == m.opts.WiFiInterface || c.PhysicalWAN == managedPPPInterface || c.WAN != want || iface != want {
			return errors.New("DNS callback interface mismatch")
		}
		var content strings.Builder
		if event == "bound" {
			fields := strings.Fields(servers)
			if len(servers) > 4096 || len(fields) > 128 {
				return errors.New("DNS callback too large")
			}
			seen := map[netip.Addr]bool{}
			for _, value := range fields {
				a, err := netip.ParseAddr(value)
				if err != nil || !a.Is4() || !validProtocolDNS(a) {
					return errors.New("invalid IPv4 DNS address")
				}
				if !seen[a] {
					seen[a] = true
					content.WriteString("nameserver " + a.String() + "\n")
				}
			}
		}
		return m.publishDNSSource("wan4", content.String())
	})
}
