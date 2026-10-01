package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func setupServerForAuthTest(t *testing.T) (*Server, func()) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// seed admin: password "secret"
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	if err := database.CreateUser("admin", string(hash)); err != nil {
		t.Fatalf("create user: %v", err)
	}
	srv := New(Options{
		Config:  config.DefaultConfig(),
		DB:      database,
		Version: "test",
		DevMode: false, // enforce auth
	})
	cleanup := func() { _ = database.Close() }
	return srv, cleanup
}

func TestUnauthorizedWithoutSession(t *testing.T) {
	srv, closeDB := setupServerForAuthTest(t)
	defer closeDB()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestJobsListEmptyOK(t *testing.T) {
	srv, closeDB := setupServerForAuthTest(t)
	defer closeDB()
	// login
	body := map[string]string{"username": "admin", "password": "secret"}
	bs, _ := json.Marshal(body)
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader(bs))
	reqLogin.Header.Set("Content-Type", "application/json")
	wLogin := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wLogin, reqLogin)
	cookies := wLogin.Result().Cookies()
	// list jobs
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestLoginSuccessAndLogout(t *testing.T) {
	srv, closeDB := setupServerForAuthTest(t)
	defer closeDB()
	body := map[string]string{"username": "admin", "password": "secret"}
	bs, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader(bs))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d, body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected session cookie")
	}
	// Access protected endpoint with cookie
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}
	// Logout
	req3 := httptest.NewRequest(http.MethodDelete, "/api/v1/session", nil)
	for _, c := range cookies {
		req3.AddCookie(c)
	}
	w3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w3, req3)
	if w3.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w3.Code)
	}
	// Access again -> 401
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	for _, c := range cookies {
		req4.AddCookie(c)
	}
	w4 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w4, req4)
	if w4.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", w4.Code)
	}
	_ = os.WriteFile(filepath.Join(t.TempDir(), "noop"), []byte(time.Now().String()), 0o600)
}

func TestLoginFailure(t *testing.T) {
	srv, closeDB := setupServerForAuthTest(t)
	defer closeDB()
	body := map[string]string{"username": "admin", "password": "wrong"}
	bs, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader(bs))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", w.Code)
	}
}

