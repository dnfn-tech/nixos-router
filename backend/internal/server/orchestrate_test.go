package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type fakeRunner struct {
	lookups []string
	cmds    [][]string
	missing map[string]bool
}

func (f *fakeRunner) LookPath(name string) error {
	f.lookups = append(f.lookups, name)
	return nil
}
func (f *fakeRunner) Run(name string, args ...string) error {
	rec := append([]string{name}, args...)
	f.cmds = append(f.cmds, rec)
	// If last arg (unit) is in missing set, return error to simulate absent unit
	if len(args) > 0 {
		unit := args[len(args)-1]
		if f.missing != nil && f.missing[unit] {
			return os.ErrNotExist
		}
	}
	return nil
}
func (f *fakeRunner) Output(name string, args ...string) (string, error) {
	// Not used in these tests
	return "", nil
}

func TestNoSystemctlWhenApplyReloadDisabled(t *testing.T) {
	srv, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	fr := &fakeRunner{}
	srv.runner = fr
	// default applyReload is false
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	// ensure no systemctl lookups or runs occurred
	if len(fr.lookups) != 0 || len(fr.cmds) != 0 {
		t.Fatalf("expected no systemctl calls when applyReload=false, got lookups=%d cmds=%d", len(fr.lookups), len(fr.cmds))
	}
}

func TestPluginOrchestrationEnabledDisabledAndMissingUnit(t *testing.T) {
	srv, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	// Enable applyReload
	srv.applyReload = true
	// Save a config with plugin states
	cfg, err := config.LoadFromFile(srv.cfgPath)
	if err != nil {
		t.Fatalf("load cfg: %v", err)
	}
	if cfg.Plugins == nil {
		cfg.Plugins = map[string]config.PluginConfig{}
	}
	cfg.Plugins["mihomo"] = config.PluginConfig{Enable: true, Config: map[string]interface{}{"profile": "def"}}
	cfg.Plugins["tailscale"] = config.PluginConfig{Enable: true, Config: map[string]interface{}{"controlPlane": "headscale"}}
	cfg.Plugins["zerotier"] = config.PluginConfig{Enable: false, Config: map[string]interface{}{}}
	if err := config.SaveToFile(srv.cfgPath, cfg); err != nil {
		t.Fatalf("save cfg: %v", err)
	}
	// Inject fake runner: simulate missing mihomo.service
	fr := &fakeRunner{missing: map[string]bool{"mihomo.service": true}}
	srv.runner = fr
	// Auth cookie already set up
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(nil))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	// Response should include notes
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["notes"]; !ok {
		t.Fatalf("expected notes in response when applyReload=true")
	}
	// Verify plugin-related systemctl invocations were planned
	want := map[string]bool{
		"try-reload-or-restart mihomo.service":     false,
		"try-reload-or-restart tailscaled.service": false,
		"try-reload-or-restart headscale.service":  false,
		"try-stop zerotier-one.service":            false,
	}
	for _, rec := range fr.cmds {
		// Expect "systemctl -q <verb> <unit>"
		if len(rec) == 4 && rec[0] == "systemctl" && rec[1] == "-q" {
			key := rec[2] + " " + rec[3]
			if _, ok := want[key]; ok {
				want[key] = true
			}
		}
	}
	for k, ok := range want {
		if !ok {
			t.Fatalf("expected command not executed: %s", k)
		}
	}
	// Ensure generated plugin fragments exist and contain documented units comments (mihomo, tailscale, zerotier)
	gen := filepath.Join(srv.stateDir, "generated", "plugins")
	if _, err := os.Stat(filepath.Join(gen, "mihomo.yaml")); err != nil {
		t.Fatalf("expected mihomo.yaml to exist")
	}
	if _, err := os.Stat(filepath.Join(gen, "tailscale.env.fragment")); err != nil {
		t.Fatalf("expected tailscale.env.fragment to exist")
	}
	// zerotier is disabled in this test: .DISABLED should be present
	if _, err := os.Stat(filepath.Join(gen, "zerotier.conf.fragment")); err != nil {
		if _, err2 := os.Stat(filepath.Join(gen, "zerotier.DISABLED")); err2 != nil {
			t.Fatalf("expected zerotier.conf.fragment or zerotier.DISABLED to exist")
		}
	}
}

