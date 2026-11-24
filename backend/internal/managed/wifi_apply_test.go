package managed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestWiFiRadioApplyAndRecoveryPreserveWiredNetwork(t *testing.T) {
	for _, failAP := range []bool{false, true} {
		name := "confirmation timeout"
		if failAP {
			name = "AP restart failure"
		}
		t.Run(name, func(t *testing.T) {
			m, r, c := fixture(t)
			m.opts.Runner = &radioRunner{recorder: r}
			c.WAN.Mode = "pppoe"
			c.WAN.PPPoE = &config.PPPoEConfig{Username: "test", Password: "secret"}
			c.WiFi.Enable, c.WiFi.BridgeToLan = true, true
			c.WiFi.APs = []config.WiFiAP{{
				Enable: true, SSID: "radio", PSK: "private-password", Band: "5g",
				Standard: "wifi6", Channel: 149, ChannelMode: "fixed", ChannelWidthMHz: 20,
			}}
			ctx := context.Background()
			if err := m.Restore(ctx, c); err != nil {
				t.Fatal(err)
			}
			current := filepath.Join(m.base(), "current")
			generation, err := os.Readlink(current)
			if err != nil {
				t.Fatal(err)
			}
			oldAP, err := os.ReadFile(filepath.Join(current, "hostapd.conf"))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			m.opts.Clock = func() time.Time { return now }
			m.opts.ConfirmationTimeout = time.Minute
			t.Cleanup(m.Close)
			next := clone(c)
			next.WiFi.APs[0].Channel = 0
			next.WiFi.APs[0].ChannelMode = "auto"
			next.WiFi.APs[0].ChannelWidthMHz = 80
			r.calls = nil
			if failAP {
				r.fail = "systemctl restart nixos-router-hostapd.service"
			}
			result, err := m.Apply(ctx, next)
			if failAP {
				if err == nil || result.RollbackStatus != "succeeded" {
					t.Fatalf("failed AP restart did not recover: %+v %v", result, err)
				}
			} else {
				if err != nil || result.Stage != "awaiting-confirmation" {
					t.Fatalf("WiFi apply failed: %+v %v", result, err)
				}
				activeAP, err := os.ReadFile(filepath.Join(current, "hostapd.conf"))
				if err != nil || !strings.Contains(string(activeAP), "vht_oper_chwidth=1\n") {
					t.Fatalf("new width was not applied: %v", err)
				}
				now = now.Add(2 * time.Minute)
				if err := m.ExpireCandidate(ctx); err != nil {
					t.Fatal(err)
				}
			}

			const restart = "systemctl restart nixos-router-hostapd.service"
			restarts := 0
			for _, call := range r.calls {
				switch {
				case call == restart:
					restarts++
				case strings.HasPrefix(call, "ip link set "), strings.HasPrefix(call, "ip link add "),
					strings.HasPrefix(call, "ip link delete "), strings.HasPrefix(call, "ip -4 addr flush "),
					strings.HasPrefix(call, "systemctl stop "), strings.HasPrefix(call, "systemctl restart "):
					t.Fatalf("WiFi change disrupted another network component: %s", call)
				}
			}
			if after, err := os.Readlink(current); err != nil || after != generation || restarts != 2 {
				t.Fatalf("expected unchanged WAN generation and two AP restart attempts, got %q, %d, %v", after, restarts, err)
			}
			restoredAP, err := os.ReadFile(filepath.Join(current, "hostapd.conf"))
			if err != nil || string(restoredAP) != string(oldAP) {
				t.Fatalf("previous AP configuration not restored: %v", err)
			}
			state, err := m.Candidate(ctx)
			if err != nil || state.State != "rolled-back" {
				t.Fatalf("unexpected confirmation state: %+v %v", state, err)
			}
		})
	}
}
