package managed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestPPPoEOptionsAndEgress(t *testing.T) {
	c := config.DefaultConfig()
	c.WAN.Interface = "wan0"
	for _, mode := range []string{"dhcp", "static"} {
		c.WAN.Mode = mode
		if got := wanEgress(c); got != "wan0" {
			t.Fatal(got)
		}
		if got, err := renderPPPoE(c); err != nil || got != "" {
			t.Fatalf("inactive PPP options: %q %v", got, err)
		}
	}
	c.WAN.Mode = "pppoe"
	c.WAN.PPPoE = &config.PPPoEConfig{Username: `name with # and "quote"`, Password: `secret\with "quotes" # spaces`}
	got, err := renderPPPoE(c)
	if err != nil {
		t.Fatal(err)
	}
	if wanEgress(c) != "nr-ppp0" {
		t.Fatal("physical WAN used for PPP traffic")
	}
	for _, line := range []string{`plugin pppoe.so`, `nic-wan0`, `ifname nr-ppp0`, `user "name with # and \"quote\""`, `password "secret\\with \"quotes\" # spaces"`, `mtu 1492`, `mru 1492`, `nodetach`, `persist`, `maxfail 0`, `holdoff 5`, `lcp-echo-interval 10`, `lcp-echo-failure 3`, `hide-password`} {
		if !strings.Contains("\n"+got, "\n"+line+"\n") {
			t.Fatalf("missing %q", line)
		}
	}
	if strings.Contains(got, "debug\n") || strings.Contains(got, "dump\n") {
		t.Fatal("unsafe credential logging")
	}
}

func TestPPPoERejectsUnsafeOptions(t *testing.T) {
	for _, value := range []string{"", "x\nplugin evil.so", "x\rsecret", "x\x00", "x\tvalue", strings.Repeat("a", 256)} {
		for _, field := range []string{"username", "password"} {
			c := config.DefaultConfig()
			c.WAN.Mode = "pppoe"
			c.WAN.Interface = "wan0"
			c.WAN.PPPoE = &config.PPPoEConfig{Username: "user", Password: "pass"}
			if field == "username" {
				c.WAN.PPPoE.Username = value
			} else {
				c.WAN.PPPoE.Password = value
			}
			got, err := renderPPPoE(c)
			if err == nil || got != "" {
				t.Fatalf("accepted unsafe %s", field)
			}
			if value != "" && strings.Contains(err.Error(), value) {
				t.Fatal("error exposed input")
			}
		}
	}
	c := config.DefaultConfig()
	c.WAN.Mode = "pppoe"
	if _, err := renderPPPoE(c); err == nil {
		t.Fatal("missing credentials accepted")
	}
	c.WAN.PPPoE = &config.PPPoEConfig{Username: "u", Password: "p"}
	for _, iface := range []string{"wan0\nfile /tmp/evil", "nr-ppp0", "lo", ""} {
		c.WAN.Interface = iface
		if _, err := renderPPPoE(c); err == nil {
			t.Fatal("invalid physical WAN accepted")
		}
	}
}

func TestPPPoEApplyAndSwitchBack(t *testing.T) {
	m, r, c := fixture(t)
	c.WAN.Mode = "pppoe"
	c.WAN.PPPoE = &config.PPPoEConfig{Username: "test-user", Password: "test-secret"}
	res, err := m.Apply(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stage != "awaiting-pppoe-session" {
		t.Fatal(res)
	}
	root := filepath.Join(m.base(), "current")
	options, err := os.ReadFile(filepath.Join(root, "pppoe.options"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(options), "ipparam generation-") {
		t.Fatal("missing generation")
	}
	info, _ := os.Stat(filepath.Join(root, "pppoe.options"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
	rules, _ := os.ReadFile(filepath.Join(root, "firewall.nft"))
	if !strings.Contains(string(rules), `oifname "nr-ppp0" masquerade`) || strings.Contains(string(rules), `oifname "wan0" masquerade`) {
		t.Fatal("wrong NAT exit")
	}
	calls := strings.Join(r.calls, "\n")
	if !strings.Contains(calls, "systemctl restart nixos-router-pppoe.service") || strings.Contains(calls, "systemctl restart nixos-router-wan.service") {
		t.Fatal(calls)
	}
	if strings.Contains(calls, "test-secret") {
		t.Fatal("password in command arguments")
	}
	if err = os.WriteFile(filepath.Join(m.base(), "wan-resolv.conf"), []byte("nameserver 10.77.0.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r.calls = nil
	c.WAN.Mode = "dhcp"
	if _, err = m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	calls = strings.Join(r.calls, "\n")
	if !strings.Contains(calls, "systemctl stop nixos-router-pppoe.service") || !strings.Contains(calls, "systemctl restart nixos-router-wan.service") {
		t.Fatal(calls)
	}
	resolver, err := os.ReadFile(filepath.Join(m.base(), "wan-resolv.conf"))
	if err != nil || len(resolver) != 0 {
		t.Fatal("new WAN candidate retained old peer DNS")
	}
	options, _ = os.ReadFile(filepath.Join(root, "pppoe.options"))
	if len(options) != 0 {
		t.Fatal("inactive candidate retained PPP credentials")
	}
}

type foreignPPPRunner struct{ recorder }

func (r *foreignPPPRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name == "ip" && strings.Join(args, " ") == "-j -d link show" {
		return `[{"ifname":"nr-ppp0","ifalias":"foreign"}]`, nil
	}
	return r.recorder.Run(ctx, name, args...)
}
func TestPPPoERejectsForeignInterfaceBeforeMutation(t *testing.T) {
	m, _, c := fixture(t)
	r := &foreignPPPRunner{}
	m.opts.Runner = r
	c.WAN.Mode = "pppoe"
	c.WAN.PPPoE = &config.PPPoEConfig{Username: "u", Password: "p"}
	_, err := m.Apply(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "foreign PPP") {
		t.Fatalf("unexpected result %v", err)
	}
	for _, call := range r.calls {
		if strings.HasPrefix(call, "systemctl stop") || strings.Contains(call, "ip link set") {
			t.Fatal("mutated before ownership check")
		}
	}
}
