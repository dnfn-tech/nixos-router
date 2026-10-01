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

// Create a minimal sysfs-like tree for one interface
func writeIfaceSysfs(t *testing.T, base, ifname string, up bool, speed int, duplex string, rx, tx uint64) {
	t.Helper()
	ifdir := filepath.Join(base, ifname)
	if err := os.MkdirAll(filepath.Join(ifdir, "statistics"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state := "down"
	if up {
		state = "up"
	}
	_ = os.WriteFile(filepath.Join(ifdir, "operstate"), []byte(state+"\n"), 0o644)
	if speed > 0 {
		_ = os.WriteFile(filepath.Join(ifdir, "speed"), []byte("1000\n"), 0o644)
	}
	if duplex != "" {
		_ = os.WriteFile(filepath.Join(ifdir, "duplex"), []byte(duplex+"\n"), 0o644)
	}
	_ = os.WriteFile(filepath.Join(ifdir, "statistics", "rx_bytes"), []byte("1000"), 0o644)
	_ = os.WriteFile(filepath.Join(ifdir, "statistics", "tx_bytes"), []byte("2000"), 0o644)
}

func TestStatusInterfacesFromSysfs(t *testing.T) {
	tmp := t.TempDir()
	// Our server expects NIXOS_ROUTER_SYS_CLASS_NET to point to the directory with ifaces
	t.Setenv("NIXOS_ROUTER_SYS_CLASS_NET", tmp)
	writeIfaceSysfs(t, tmp, "wan0", true, 1000, "full", 1000, 2000)
	writeIfaceSysfs(t, tmp, "br-lan", true, 1000, "full", 500, 800)

	srv := New(Options{
		Config:  config.DefaultConfig(),
		DevMode: true,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	ifs, _ := body["interfaces"].([]interface{})
	if len(ifs) < 2 {
		t.Fatalf("expected at least 2 interfaces, got %d", len(ifs))
	}
	foundWan := false
	foundLan := false
	for _, it := range ifs {
		m, _ := it.(map[string]interface{})
		if m == nil {
			continue
		}
		switch m["name"] {
		case "wan0":
			foundWan = true
			if up, _ := m["up"].(bool); !up {
				t.Fatalf("wan0 up should be true")
			}
		case "br-lan":
			foundLan = true
		}
	}
	if !foundWan || !foundLan {
		t.Fatalf("wan0 or br-lan not found in interfaces")
	}
	// sources summary
	srcs, _ := body["sources"].(map[string]interface{})
	if srcs == nil || srcs["interfaces"] != "sysfs" {
		t.Fatalf("expected sources.interfaces=sysfs, got %v", srcs)
	}
}

