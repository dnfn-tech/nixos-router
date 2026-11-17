package managed

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestWiFiIPv4Kernel(t *testing.T) {
	if os.Getenv("ROUTERD_TEST_WIFI_IPV4_KERNEL") != "1" {
		t.Skip("set ROUTERD_TEST_WIFI_IPV4_KERNEL=1")
	}
	namespace, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	if parent := os.Getenv("ROUTERD_TEST_WIFI_IPV4_PARENT_NS"); parent == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--net", binary, "-test.run=^TestWiFiIPv4Kernel$", "-test.v")
		cmd.Env = append(os.Environ(), "ROUTERD_TEST_WIFI_IPV4_PARENT_NS="+namespace)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated WiFi IPv4: %v\n%s", err, out)
		}
		t.Log(string(out))
		return
	} else if parent == namespace {
		t.Fatal("refusing host network mutation")
	}
	m, _, c := wifiIPv4Fixture(t)
	m.opts.Runner = commandRunner{}
	ctx := context.Background()
	for _, iface := range []string{"wlan0", "foreign0"} {
		if err = m.cmd(ctx, "ip", "link", "add", iface, "type", "dummy"); err != nil {
			t.Fatal(err)
		}
		if err = m.cmd(ctx, "ip", "link", "set", iface, "up"); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"-4", "addr", "add", "198.51.100.1/24", "dev", "foreign0"}, {"-4", "route", "add", "192.0.2.0/24", "dev", "wlan0"}} {
		if err = m.cmd(ctx, "ip", args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = m.preflightWiFiIPv4(ctx, c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = m.reconcileWiFiIPv4(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	routes, err := m.run(ctx, "ip", "-4", "route", "show")
	if err != nil || !strings.Contains(routes, "192.168.77.0/24 dev wlan0 proto kernel") {
		t.Fatal(routes, err)
	}
	if err = m.cmd(ctx, "ip", "-4", "addr", "add", "203.0.113.1/24", "dev", "wlan0"); err != nil {
		t.Fatal(err)
	}
	if err = m.preflightWiFiIPv4(ctx, c); err == nil {
		t.Fatal("foreign address not rejected")
	}
	if err = m.reconcileWiFiIPv4(ctx, config.Config{}); err != nil {
		t.Fatal(err)
	}
	addresses, err := m.run(ctx, "ip", "-4", "addr", "show")
	if err != nil || strings.Contains(addresses, "192.168.77.1") || !strings.Contains(addresses, "198.51.100.1") || !strings.Contains(addresses, "203.0.113.1") {
		t.Fatal(addresses, err)
	}
	routes, err = m.run(ctx, "ip", "-4", "route", "show")
	if err != nil || strings.Contains(routes, "192.168.77.0") || !strings.Contains(routes, "192.0.2.0/24 dev wlan0") {
		t.Fatal(routes, err)
	}
}
