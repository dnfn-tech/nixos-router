package managed

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestIPv6DualLANResourcesKernel(t *testing.T) {
	if os.Getenv("ROUTERD_TEST_IPV6_KERNEL") != "1" {
		t.Skip("set ROUTERD_TEST_IPV6_KERNEL=1")
	}
	namespace, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	if parent := os.Getenv("ROUTERD_TEST_DUAL_IPV6_PARENT_NS"); parent == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--net", binary, "-test.run=^TestIPv6DualLANResourcesKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "ROUTERD_TEST_DUAL_IPV6_PARENT_NS="+namespace)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated dual IPv6: %v\n%s", err, out)
		}
		t.Log(string(out))
		return
	} else if parent == namespace {
		t.Fatal("refusing host network mutation")
	}
	m, _, gen := dualIPv6Fixture(t)
	m.opts.Runner = commandRunner{}
	m.opts.Clock = time.Now
	ctx := context.Background()
	for _, iface := range []string{"wan0", "br-lan", "wlan0"} {
		if err = m.cmd(ctx, "ip", "link", "add", iface, "type", "dummy"); err != nil {
			t.Fatal(err)
		}
		if err = m.claimIPv6Autoconfiguration(ctx, iface); err != nil {
			t.Fatal(err)
		}
		if err = m.cmd(ctx, "ip", "link", "set", iface, "up"); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"-6", "addr", "add", "2001:db8:ffff::1/128", "dev", "wlan0"}, {"-6", "route", "add", "2001:db8:eeee::/64", "dev", "wlan0", "metric", "256"}} {
		if err = m.cmd(ctx, "ip", args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = m.reconcileIPv6Event(ctx, gen, "wan0", leaseEvent(t, "bound"), time.Now()); err != nil {
		t.Fatal(err)
	}
	wired, err := m.run(ctx, "ip", "-6", "addr", "show", "dev", "br-lan")
	if err != nil || !strings.Contains(wired, "2001:db8:100::1/64") {
		t.Fatal(wired, err)
	}
	wifi, err := m.run(ctx, "ip", "-6", "addr", "show", "dev", "wlan0")
	if err != nil || !strings.Contains(wifi, "2001:db8:100:1::1/64") {
		t.Fatal(wifi, err)
	}
	if err = m.reconcileIPv6Event(ctx, gen, "wan0", ipv6Event{State: "stopped"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	addresses, err := m.run(ctx, "ip", "-6", "addr", "show")
	if err != nil || strings.Contains(addresses, "2001:db8:100:") || !strings.Contains(addresses, "2001:db8:ffff::1/128") {
		t.Fatal(addresses, err)
	}
	routes, err := m.run(ctx, "ip", "-6", "route", "show")
	if err != nil || strings.Contains(routes, "proto 242") || !strings.Contains(routes, "2001:db8:eeee::/64 dev wlan0") {
		t.Fatal(routes, err)
	}
}
