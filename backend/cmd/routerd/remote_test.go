package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

type appliedFixture struct {
	cfg       config.Config
	available bool
}

func (a appliedFixture) Applied(context.Context) (config.Config, bool, error) {
	return a.cfg, a.available, nil
}
func TestRemoteStartupPreservesCorruptDraft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("broken draft"), 0600); err != nil {
		t.Fatal(err)
	}
	want := config.DefaultConfig()
	want.System.Hostname = "applied-router"
	got, _, warning, err := loadRemoteStartupConfig(config.LoadOptions{StateDir: dir}, appliedFixture{want, true})
	if err != nil || warning == "" || got.System.Hostname != "applied-router" {
		t.Fatalf("fallback %v %q %v", got.System, warning, err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "broken draft" {
		t.Fatal("draft overwritten")
	}
	if _, _, _, err = loadRemoteStartupConfig(config.LoadOptions{StateDir: dir}, appliedFixture{want, false}); err == nil {
		t.Fatal("used unverified runtime")
	}
}
func TestRemoteModeRejectsRootAndLegacyPrivilegeFlags(t *testing.T) {
	if err := validateExecutorMode("/run/executor/control.sock", true, false, false, 1000); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path                 string
		managed, dev, legacy bool
		uid                  int
	}{
		{"/run/executor/control.sock", true, false, false, 0},
		{"/run/executor/control.sock", false, false, false, 1000},
		{"/run/executor/control.sock", true, true, false, 1000},
		{"/run/executor/control.sock", true, false, true, 1000},
		{"relative", true, false, false, 1000},
	} {
		if err := validateExecutorMode(tc.path, tc.managed, tc.dev, tc.legacy, tc.uid); err == nil {
			t.Fatalf("unsafe remote mode accepted: %+v", tc)
		}
	}
}
