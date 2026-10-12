package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeRunnerConsume allows controlling failures for specific commands.
type fakeRunnerConsume struct {
	look []string
	run  [][]string
	failNftForPath string
}

func (f *fakeRunnerConsume) LookPath(name string) error {
	f.look = append(f.look, name)
	return nil
}
func (f *fakeRunnerConsume) Run(name string, args ...string) error {
	rec := append([]string{name}, args...)
	f.run = append(f.run, rec)
	if name == "nft" && len(args) >= 2 && args[0] == "-f" {
		if f.failNftForPath != "" && args[1] == f.failNftForPath {
			return os.ErrPermission
		}
	}
	return nil
}
func (f *fakeRunnerConsume) Output(name string, args ...string) (string, error) {
	return "", nil
}

func TestConsumeNftApplyAndSnapshot(t *testing.T) {
	srv, dir, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	// enable runtime
	srv.applyReload = true
	srv.consumeGenerated = true
	srv.privilegedApply = true
	fr := &fakeRunnerConsume{}
	srv.runner = fr
	// perform apply
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(nil))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if v, _ := resp["appliedRuntime"].(bool); !v {
		t.Fatalf("expected appliedRuntime=true")
	}
	notesAny := resp["notes"]
	notesJSON, _ := json.Marshal(notesAny)
	if !strings.Contains(string(notesJSON), "nftables: applied from generated") {
		t.Fatalf("expected nft apply note, got %s", string(notesJSON))
	}
	// last-good.rev should exist and snapshot dir contains nftables fragment
	lgrevPath := filepath.Join(dir, "last-good.rev")
	b, err := os.ReadFile(lgrevPath)
	if err != nil {
		t.Fatalf("missing last-good.rev: %v", err)
	}
	rev := strings.TrimSpace(string(b))
	if rev == "" {
		t.Fatalf("empty last-good.rev")
	}
	if _, err := os.Stat(filepath.Join(dir, "revisions", rev, "nftables.nft.fragment")); err != nil {
		t.Fatalf("missing snapshot nftables fragment: %v", err)
	}
	// runner should have executed nft -f <generated>
	genNft := filepath.Join(dir, "generated", "nftables.nft.fragment")
	found := false
	for _, rec := range fr.run {
		if len(rec) == 3 && rec[0] == "nft" && rec[1] == "-f" && rec[2] == genNft {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected nft -f %s to be executed", genNft)
	}
}

func TestConsumeFailureRollbackToLastGood(t *testing.T) {
	srv, dir, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	// seed last-good revision
	pre := "pre1"
	preDir := filepath.Join(dir, "revisions", pre)
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatalf("mkdir pre: %v", err)
	}
	// write a distinct nftables fragment
	want := "# lastgood\n"
	if err := os.WriteFile(filepath.Join(preDir, "nftables.nft.fragment"), []byte(want), 0o644); err != nil {
		t.Fatalf("write pre nft: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "last-good.rev"), []byte(pre+"\n"), 0o644); err != nil {
		t.Fatalf("write last-good.rev: %v", err)
	}
	// enable runtime + consumption
	srv.applyReload = true
	srv.consumeGenerated = true
	srv.privilegedApply = true
	// fake runner: fail nft when pointing to generated, succeed otherwise
	genNft := filepath.Join(dir, "generated", "nftables.nft.fragment")
	fr := &fakeRunnerConsume{failNftForPath: genNft}
	srv.runner = fr
	// perform apply
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(nil))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if v, _ := resp["appliedRuntime"].(bool); v {
		t.Fatalf("expected appliedRuntime=false on failure")
	}
	if _, ok := resp["error"].(string); !ok {
		t.Fatalf("expected error in response")
	}
	// generated should be restored to last-good content
	got, err := os.ReadFile(genNft)
	if err != nil {
		t.Fatalf("read generated nft after rollback: %v", err)
	}
	if string(got) != want {
		t.Fatalf("generated nft not rolled back, got=%q want=%q", string(got), want)
	}
	// and runner should have run nft for last-good path
	lastNft := filepath.Join(preDir, "nftables.nft.fragment")
	ranLast := false
	for _, rec := range fr.run {
		if len(rec) == 3 && rec[0] == "nft" && rec[1] == "-f" && rec[2] == lastNft {
			ranLast = true
			break
		}
	}
	if !ranLast {
		t.Fatalf("expected nft -f %s to be executed for rollback", lastNft)
	}
}

