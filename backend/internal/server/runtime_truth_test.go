package server

import (
	"encoding/json"
	"errors"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"net/http/httptest"
	"testing"
)

type exitedStatusRunner struct{ stdout string }

func (*exitedStatusRunner) LookPath(string) error       { return nil }
func (*exitedStatusRunner) Run(string, ...string) error { return nil }
func (r *exitedStatusRunner) Output(string, ...string) (string, error) {
	return r.stdout, errors.New("exit status 3")
}
func TestUnitStateRecognizesNonzeroExitOutput(t *testing.T) {
	for _, tc := range []struct{ out, want string }{{"inactive\n", "inactive"}, {"failed\n", "failed"}, {"active\n", "active"}, {"not-found\n", "missing"}, {"", "unknown"}, {"transport error", "unknown"}} {
		t.Run(tc.want+tc.out, func(t *testing.T) {
			s := New(Options{Config: config.DefaultConfig()})
			s.runner = &exitedStatusRunner{stdout: tc.out}
			units, _ := s.collectUnitStatuses()
			for _, u := range units {
				if u["state"] != tc.want {
					t.Fatalf("got %v want %s", u, tc.want)
				}
			}
		})
	}
}

type appliedClientRuntime struct {
	runtimeStub
	cfg   config.Config
	known bool
}

func (r *appliedClientRuntime) AppliedConfig() (config.Config, bool) { return r.cfg, r.known }
func TestManagedClientBlockUsesAppliedSnapshot(t *testing.T) {
	for _, known := range []bool{true, false} {
		cfg := config.DefaultConfig()
		cfg.LAN.StaticLeases = []config.StaticLease{{MAC: "AA:BB:CC:DD:EE:01", IP: "192.168.1.10"}}
		cfg.Firewall.BlockedMACs = []string{"AA:BB:CC:DD:EE:01"}
		applied := cfg
		applied.Firewall.BlockedMACs = nil
		s := New(Options{Config: cfg, DevMode: true, ManagedRuntime: &appliedClientRuntime{cfg: applied, known: known}})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clients", nil))
		var body struct {
			Clients []struct {
				Blocked      bool   `json:"blocked"`
				DraftBlocked bool   `json:"draftBlocked"`
				Source       string `json:"blockedSource"`
				Known        bool   `json:"blockedRuntimeKnown"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Clients) != 1 {
			t.Fatal(w.Body.String())
		}
		c := body.Clients[0]
		if c.Blocked == known || !c.DraftBlocked || c.Known != known {
			t.Fatal(w.Body.String())
		}
		want := "config"
		if known {
			want = "last-good"
		}
		if c.Source != want {
			t.Fatal(w.Body.String())
		}
	}
}
