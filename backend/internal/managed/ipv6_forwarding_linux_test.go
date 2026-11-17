package managed

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestIPv6ForwardingKernel(t *testing.T) {
	nft := os.Getenv("ROUTERD_TEST_NFT_BINARY")
	if nft == "" {
		t.Skip("set ROUTERD_TEST_NFT_BINARY")
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if parent := os.Getenv("ROUTERD_TEST_FORWARD_PARENT_NS"); parent == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--net", binary, "-test.run=^TestIPv6ForwardingKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "ROUTERD_TEST_FORWARD_PARENT_NS="+ns)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated forwarding: %v\n%s", err, out)
		}
		return
	} else if parent == ns {
		t.Fatal("refusing host network mutation")
	}
	m, _, c := fixture(t)
	if action := os.Getenv("ROUTERD_TEST_FORWARD_ACTION"); action != "" {
		if action != "enable" && action != "disable" {
			t.Fatal("invalid helper action")
		}
		m.opts.Runner = commandRunner{}
		for _, iface := range []string{"br-lan", "wan0"} {
			if err := m.setIPv6Forwarding(context.Background(), iface, action == "enable"); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	c.IPv6.Enable, c.IPv6.LANPD, c.IPv6.WANMode = true, true, "dhcpv6"
	file := filepath.Join(t.TempDir(), "firewall.nft")
	if err := os.WriteFile(file, []byte(m.render(c)["firewall.nft"]), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(nft, "-f", file).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	cmd := exec.Command("python", filepath.Join("..", "..", "..", "tests", "ipv6-forwarding-packets.py"), binary)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("forwarding packets: %v\n%s", err, out)
	}
}
