package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordChangeAndLogin(t *testing.T) {
	dir := t.TempDir()
	database, _ := db.Open(dir)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	_ = database.CreateUser("admin", string(hash))
	srv := New(Options{Config: config.DefaultConfig(), DB: database, DevMode: false})
	// login
	login := map[string]string{"username": "admin", "password": "secret"}
	bs, _ := json.Marshal(login)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader(bs))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("login failed: %d", w.Code)
	}
	cookies := w.Result().Cookies()
	// change password
	change := map[string]string{"currentPassword": "secret", "newPassword": "newsecret1"}
	cs, _ := json.Marshal(change)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/session/password", bytes.NewReader(cs))
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("change password failed: %d", w2.Code)
	}
	// old login fails
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader(bs))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w3, req3)
	if w3.Code == http.StatusCreated {
		t.Fatalf("old password should not work")
	}
	// new login works
	login2 := map[string]string{"username": "admin", "password": "newsecret1"}
	bs2, _ := json.Marshal(login2)
	req4 := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader(bs2))
	req4.Header.Set("Content-Type", "application/json")
	w4 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w4, req4)
	if w4.Code != http.StatusCreated {
		t.Fatalf("new password login failed: %d", w4.Code)
	}
}

func TestBackupRestoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.DefaultConfig()
	cfg.System.Hostname = "before"
	_ = config.SaveToFile(cfgPath, cfg)
	database, _ := db.Open(dir)
	srv := New(Options{Config: cfg, ConfigPath: cfgPath, DB: database, DevMode: true})
	// backup
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backup", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("backup failed: %d", w.Code)
	}
	var bundle map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &bundle)
	rawCfg := bundle["config"]
	m := rawCfg.(map[string]interface{})
	sys := m["system"].(map[string]interface{})
	sys["hostname"] = "after"
	m["system"] = sys
	body, _ := json.Marshal(map[string]interface{}{"config": m})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/backup/restore", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("restore failed: %d %s", w2.Code, w2.Body.String())
	}
	// GET should show new hostname (dev mode no auth)
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w3, req3)
	var resp struct{ Config config.Config `json:"config"` }
	_ = json.Unmarshal(w3.Body.Bytes(), &resp)
	if resp.Config.System.Hostname != "after" {
		t.Fatalf("restore did not update hostname: %s", resp.Config.System.Hostname)
	}
}

func TestRebootDisabledPath(t *testing.T) {
	srv := New(Options{Config: config.DefaultConfig(), DevMode: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/reboot", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 when reboot disabled, got %d", w.Code)
	}
}

