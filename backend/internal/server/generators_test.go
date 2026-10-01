package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestGenerateParentalAndQoSFragments(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{
		Config:   config.DefaultConfig(),
		DevMode:  true,
		StateDir: dir,
	})
	// Build a config enabling parental and QoS
	cfg := config.DefaultConfig()
	cfg.Parental.Enable = true
	cfg.Firewall.BlockedMACs = []string{"AA:BB:CC:DD:EE:FF"}
	cfg.QoS.Enable = true
	cfg.QoS.UpMbps = 10
	cfg.QoS.DownMbps = 20

	genDir := filepath.Join(dir, "generated")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := s.generateFragments(cfg, genDir); err != nil {
		t.Fatalf("generateFragments: %v", err)
	}
	// Parental nft fragment generated with set
	parPath := filepath.Join(genDir, "parental.nft.fragment")
	data, err := os.ReadFile(parPath)
	if err != nil {
		t.Fatalf("expected parental.nft.fragment, err=%v", err)
	}
	txt := string(data)
	if !strings.Contains(strings.ToLower(txt), "set blocked_macs") {
		t.Fatalf("expected blocked_macs set in parental fragment")
	}
	if !strings.Contains(strings.ToLower(txt), "aa:bb:cc:dd:ee:ff") {
		t.Fatalf("expected MAC in parental fragment")
	}
	// QoS script generated
	qosPath := filepath.Join(genDir, "qos.sh")
	data2, err := os.ReadFile(qosPath)
	if err != nil {
		t.Fatalf("expected qos.sh, err=%v", err)
	}
	txt2 := string(data2)
	if !strings.Contains(txt2, "tc qdisc replace dev wan0 root") {
		t.Fatalf("expected egress qdisc setup in qos.sh")
	}
	if !strings.Contains(txt2, "20mbit") {
		t.Fatalf("expected down mbit in qos.sh")
	}
}

