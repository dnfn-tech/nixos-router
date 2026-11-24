package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type overviewRouteRunner struct{ defaultRunner }

func (overviewRouteRunner) LookPath(string) error { return nil }

func (overviewRouteRunner) Output(_ string, args ...string) (string, error) {
	if args[1] == "-4" {
		return `[{"dst":"default","dev":"wan0"},{"dst":"default","dev":"ppp7","metric":100}]`, nil
	}
	return `[{"dst":"default","dev":"ppp7"}]`, nil
}

func TestNetworkLiveUsesPPPAndBridgeCounters(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NIXOS_ROUTER_SYS_CLASS_NET", root)
	add := func(name, kind, index, state string, dirs ...string) {
		t.Helper()
		writeIfaceSysfs(t, root, name, true, 1000, "full", 0, 0)
		for path, value := range map[string]string{"type": kind, "ifindex": index, "operstate": state} {
			if err := os.WriteFile(filepath.Join(root, name, path), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, dir := range dirs {
			if err := os.Mkdir(filepath.Join(root, name, dir), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("wan0", "1", "1", "up", "device")
	add("ppp7", "512", "2", "unknown")
	add("br0", "1", "3", "up", "bridge")
	add("lan1", "1", "4", "down", "device")
	add("veth0", "1", "5", "up")
	cfg := config.DefaultConfig()
	cfg.WAN.Interface, cfg.WAN.Mode = "wan0", "pppoe"
	cfg.LAN.BridgeName, cfg.LAN.Ports = "br0", []string{"lan1", "veth0"}
	srv := New(Options{Config: cfg, DevMode: true})
	srv.runner = overviewRouteRunner{}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/network/live", nil))
	var body struct {
		WAN, LAN struct {
			Name, RXBytes, TXBytes string
		}
		Ports []struct {
			Name, Link, Duplex string
			SpeedMbps          int
		}
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if body.WAN.Name != "ppp7" || body.LAN.Name != "br0" || body.WAN.RXBytes != "1000" || body.LAN.TXBytes != "2000" {
		t.Fatalf("wrong counter sources: %s", w.Body)
	}
	if len(body.Ports) != 2 || body.Ports[0].Name != "lan1" || body.Ports[0].Link != "down" || body.Ports[0].SpeedMbps != 0 ||
		body.Ports[1].Name != "wan0" || body.Ports[1].SpeedMbps != 1000 || body.Ports[1].Duplex != "full" {
		t.Fatalf("wrong port observations: %s", w.Body)
	}
	poll := func() liveInterface {
		t.Helper()
		srv.monitor.mu.Lock()
		srv.monitor.expires = time.Time{}
		srv.monitor.mu.Unlock()
		value, ok := srv.monitoringSnapshot(context.Background())
		if !ok {
			t.Fatal("snapshot unavailable")
		}
		return value.WAN
	}
	if err := os.RemoveAll(filepath.Join(root, "ppp7")); err != nil {
		t.Fatal(err)
	}
	if gone := poll(); gone.RXBytes != "" || gone.TXBytes != "" || gone.Identity != "" {
		t.Fatalf("disconnected PPP retained stale counters: %+v", gone)
	}
	add("ppp7", "512", "22", "unknown")
	if changed := poll(); changed.Name != "ppp7" || changed.Identity != "22/" || changed.RXBytes != "1000" {
		t.Fatalf("reconnected PPP did not replace its identity and counters: %+v", changed)
	}
}

func TestNetworkLiveIndependentWiFi(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NIXOS_ROUTER_SYS_CLASS_NET", root)
	writeIfaceSysfs(t, root, "wlan-test", true, 1000, "full", 0, 0)
	if err := os.WriteFile(filepath.Join(root, "wlan-test", "ifindex"), []byte("8"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.WiFi.Enable, cfg.WiFi.BridgeToLan = true, false
	cfg.WiFi.Network = &config.WiFiNetwork{IPv4CIDR: "192.168.2.1/24"}
	srv := New(Options{Config: cfg, WiFiInterface: "wlan-test", ManagedRuntime: &networkLiveApplied{applied: cfg, known: true}})
	draft := config.DefaultConfig()
	value, _ := json.Marshal(srv.collectNetworkLive(context.Background(), draft))
	var result struct{ WiFi *liveInterface }
	if err := json.Unmarshal(value, &result); err != nil || result.WiFi == nil || result.WiFi.RXBytes != "1000" {
		t.Fatalf("independent WiFi counter absent: %s", value)
	}
	cfg.WiFi.BridgeToLan = true
	srv.managedRuntime = &networkLiveApplied{applied: cfg, known: true}
	value, _ = json.Marshal(srv.collectNetworkLive(context.Background(), draft))
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil || fields["wifi"] != nil {
		t.Fatalf("bridged WiFi should not be separately counted: %s", value)
	}
	srv.managedRuntime = &networkLiveApplied{applied: cfg}
	value, _ = json.Marshal(srv.collectNetworkLive(context.Background(), config.Config{WiFi: config.WiFiConfig{Enable: true, Network: cfg.WiFi.Network}}))
	fields = nil
	if err := json.Unmarshal(value, &fields); err != nil || fields["wifi"] != nil {
		t.Fatalf("unknown applied topology must not report draft counters: %s", value)
	}
}

type networkLiveApplied struct {
	runtimeStub
	applied config.Config
	known   bool
}

func (r networkLiveApplied) Applied(context.Context) (config.Config, bool, error) {
	return r.applied, r.known, nil
}
