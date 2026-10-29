package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestHealthDeploymentCapabilities(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "preview", true: "runtime"}[enabled], func(t *testing.T) {
			t.Setenv("NIXOS_ROUTER_ALLOW_REBOOT", "0")
			s := New(Options{ApplyReload: enabled, DevMode: true})
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/health", nil))
			var data struct {
				Deployment struct {
					Mode          string `json:"mode"`
					RuntimeApply  bool   `json:"runtimeApply"`
					RebootAllowed bool   `json:"rebootAllowed"`
				} `json:"deployment"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			want := "generate-only"
			if enabled {
				want = "runtime"
			}
			if data.Deployment.Mode != want || data.Deployment.RuntimeApply != enabled || data.Deployment.RebootAllowed {
				t.Fatalf("unexpected capabilities: %+v", data.Deployment)
			}
		})
	}
}
