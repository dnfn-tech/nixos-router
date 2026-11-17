package server

import (
	"net/http"
	"net/http/httptest"
	"os"
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
	// Runtime ownership preflight sees no other active network manager.
	if name == "systemctl" && len(args) == 4 && args[0] == "show" && args[1] == "--property=ActiveState" {
		return "inactive", nil
	}
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

func TestPluginOrchestrationDoesNotControlSystemServices(t *testing.T) {
	srv, _, _, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	runner := &fakeRunner{}
	srv.runner = runner
	cfg := config.DefaultConfig()
	cfg.Plugins = map[string]config.PluginConfig{"mihomo": {Enable: true}, "tailscale": {Enable: true}, "zerotier": {Enable: false}}
	srv.orchestratePlugins(cfg)
	if len(runner.cmds) != 0 || len(runner.lookups) != 0 {
		t.Fatal("system-owned services were touched", runner.cmds)
	}
	for _, id := range []string{"mihomo", "tailscale"} {
		candidate := config.DefaultConfig()
		candidate.Plugins = map[string]config.PluginConfig{id: {Enable: true}}
		if err := candidate.Validate(); err == nil {
			t.Fatal("system service enabled through router config", id)
		}
	}
}
