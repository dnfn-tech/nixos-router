package managed

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Opt-in real-kernel coverage. All network commands run in a disposable child
// user/network namespace, never in the test caller's network namespace.
func TestIPv6ResourcesKernel(t *testing.T) {
	if os.Getenv("ROUTERD_TEST_IPV6_KERNEL") != "1" {
		t.Skip("set ROUTERD_TEST_IPV6_KERNEL=1 for isolated kernel coverage")
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	if parent := os.Getenv("ROUTERD_TEST_IPV6_PARENT_NS"); parent == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--net", binary, "-test.run=^TestIPv6ResourcesKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "ROUTERD_TEST_IPV6_PARENT_NS="+ns)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated kernel test: %v\n%s", err, out)
		}
		t.Log(string(out))
		return
	} else if parent == ns {
		t.Fatal("refusing network commands without namespace isolation")
	}
	m, gen := ipv6GenerationFixture(t)
	m.opts.Runner = commandRunner{}
	ctx := context.Background()
	for _, iface := range []string{"wan0", "br-lan", "test.6"} {
		if err = m.cmd(ctx, "ip", "link", "add", iface, "type", "dummy"); err != nil {
			t.Fatal(err)
		}
		if err = m.claimIPv6Autoconfiguration(ctx, iface); err != nil {
			t.Fatal(err)
		}
		for _, setting := range []string{"accept_ra", "autoconf", "use_tempaddr", "accept_redirects", "disable_ipv6"} {
			value, err := os.ReadFile("/proc/sys/net/ipv6/conf/" + iface + "/" + setting)
			if err != nil || strings.TrimSpace(string(value)) != "0" {
				t.Fatalf("%s/%s: %q %v", iface, setting, value, err)
			}
		}
		if err = m.cmd(ctx, "ip", "link", "set", iface, "up"); err != nil {
			t.Fatal(err)
		}
	}
	// A foreign address must survive both reconciliation and withdrawal.
	if err = m.cmd(ctx, "ip", "-6", "address", "add", "2001:db8:ffff::1/128", "dev", "br-lan"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"bound", "updated", "stopped"} {
		event := leaseEvent(t, state)
		wantMTU := "1500"
		if state != "stopped" {
			event.MTU = 1400
			wantMTU = "1400"
		}
		if err = m.reconcileIPv6Event(ctx, gen, "wan0", event, time.Now()); err != nil {
			t.Fatal(state, err)
		}
		for _, iface := range []string{"wan0", "br-lan", "test.6"} {
			value, readErr := os.ReadFile("/proc/sys/net/ipv6/conf/" + iface + "/mtu")
			expected := "1500"
			if iface == "wan0" {
				expected = wantMTU
			}
			if readErr != nil || strings.TrimSpace(string(value)) != expected {
				t.Fatalf("%s %s MTU=%q want=%s err=%v", state, iface, value, expected, readErr)
			}
		}
		links, linkErr := m.run(ctx, "ip", "-j", "link", "show", "dev", "wan0")
		var physical []struct {
			MTU int `json:"mtu"`
		}
		if linkErr != nil || json.Unmarshal([]byte(links), &physical) != nil || len(physical) != 1 || physical[0].MTU != 1500 {
			t.Fatalf("physical MTU changed: %s %v", links, linkErr)
		}
		addresses, err := m.run(ctx, "ip", "-j", "-6", "address", "show")
		if err != nil {
			t.Fatal(err)
		}
		routes, err := m.run(ctx, "ip", "-j", "-6", "route", "show", "table", "main")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(addresses, "2001:db8:ffff::1") {
			t.Fatal("foreign address lost", addresses)
		}
		want := state != "stopped"
		for _, address := range []string{"2001:db8::2", "2001:db8::3", "2001:db8:100::1"} {
			if strings.Contains(addresses, `"local":"`+address+`"`) != want {
				t.Fatal(state, addresses)
			}
		}
		if strings.Contains(routes, `"protocol":"242"`) != want {
			t.Fatal(state, routes)
		}
	}
}
