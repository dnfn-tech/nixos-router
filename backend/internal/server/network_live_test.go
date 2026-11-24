package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
}
