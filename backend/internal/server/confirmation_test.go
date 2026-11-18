package server

import (
	"context"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"net/http/httptest"
	"strings"
	"testing"
)

type confirmationRuntime struct {
	runtimeStub
	id string
}

func (m *confirmationRuntime) Candidate(context.Context) (*managed.Confirmation, error) {
	return &managed.Confirmation{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: "pending"}, nil
}
func (m *confirmationRuntime) Confirm(_ context.Context, id string) (*managed.Confirmation, error) {
	m.id = id
	return &managed.Confirmation{ID: id, State: "confirmed"}, nil
}
func TestConfirmationEndpointRequiresAuthentication(t *testing.T) {
	s, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	runtime := &confirmationRuntime{}
	s.managedRuntime = runtime
	for _, auth := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/api/v1/apply/confirm", strings.NewReader(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
		if auth {
			for _, c := range cookies {
				r.AddCookie(c)
			}
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := 401
		if auth {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("auth=%v status=%d body=%s", auth, w.Code, w.Body)
		}
	}
	if runtime.id != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatal("confirmation not forwarded")
	}
}

func TestConfirmationRejectsInvalidBodiesAndCrossOrigin(t *testing.T) {
	s, _, cookies, cleanup := setupApplyEnv(t, true)
	defer cleanup()
	runtime := &confirmationRuntime{}
	s.managedRuntime = runtime
	id := strings.Repeat("a", 48)
	for _, body := range []string{`{}`, `{"id":"bad"}`, `{"id":"` + id + `","id":"` + id + `"}`, `{"id":"` + id + `","timeout":600}`, `{"id":"` + id + `"} {}`} {
		r := httptest.NewRequest("POST", "/api/v1/apply/confirm", strings.NewReader(body))
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(body, w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("POST", "http://router/api/v1/apply/confirm", strings.NewReader(`{"id":"`+id+`"}`))
	r.Header.Set("Origin", "http://evil.example")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	if runtime.id != "" {
		t.Fatal("invalid confirmation forwarded")
	}
}
