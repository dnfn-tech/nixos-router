package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// Opt in with ROUTERD_TEST_NFT=1 and nft/unshare in PATH. Every nft command runs
// in a new user+network namespace; the host firewall is never modified.
func TestNftAtomicReplacement(t *testing.T) {
	if os.Getenv("ROUTERD_TEST_NFT") != "1" {
		t.Skip("set ROUTERD_TEST_NFT=1 to test isolated nft transactions")
	}
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := &Server{}
	cfg := config.DefaultConfig()
	cfg.Firewall.BlockedMACs = []string{"aa:bb:cc:dd:ee:ff"}
	cfg.Firewall.PortForwards = []config.PortForward{{Protocol: "tcp", ExternalPort: 12345, DestIP: "192.168.1.2", DestPort: 80}}
	for _, name := range []string{"old", "new"} {
		out := filepath.Join(dir, name)
		if err := os.MkdirAll(out, 0755); err != nil {
			t.Fatal(err)
		}
		if err := s.generateFragments(cfg, out); err != nil {
			t.Fatal(err)
		}
		cfg.Firewall.NATEnabled = false
		cfg.Firewall.BlockedMACs = nil
		cfg.Firewall.PortForwards = nil
	}
	old := filepath.Join(dir, "old", "nftables.nft.fragment")
	candidate := filepath.Join(dir, "new", "nftables.nft.fragment")
	data, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "invalid.nft")
	if err := os.WriteFile(bad, append(data, []byte("add rule inet routerd_filter missing_chain accept\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	script := `
nft=$1
old=$2
candidate=$3
bad=$4
"$nft" 'add table inet unrelated; add chain inet unrelated keep; add rule inet unrelated keep counter accept'
"$nft" -s list table inet unrelated > "$5/unrelated"
"$nft" -f "$old"
"$nft" -s list ruleset > "$5/before"
# Repeated application must neither append rules nor change unrelated tables.
"$nft" -f "$old"
"$nft" -s list ruleset > "$5/after"
cmp "$5/before" "$5/after"
# A failed batch must preserve the entire previously active ruleset.
if "$nft" -f "$bad"; then exit 1; fi
"$nft" -s list ruleset > "$5/after"
cmp "$5/before" "$5/after"
# Removing NAT, DNAT and blocked MACs must remove their runtime rules.
"$nft" -f "$candidate"
"$nft" list table ip routerd_nat > "$5/nat"
if grep -E 'masquerade|dnat' "$5/nat"; then exit 1; fi
if "$nft" list table inet routerd_tmp; then exit 1; fi
# Restoring the old snapshot must restore the exact old ruleset.
"$nft" -f "$old"
"$nft" -s list ruleset > "$5/after"
cmp "$5/before" "$5/after"
"$nft" -s list table inet unrelated > "$5/unrelated-after"
cmp "$5/unrelated" "$5/unrelated-after"
`
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--net", "sh", "-eu", "-c", script, "nft-test", nft, old, candidate, bad, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated nft test: %v\n%s", err, out)
	}
}
