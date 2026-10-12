package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

// Exercise the HTTP flow, including rollback after a core reload failure.
func TestApplyRuntimeGates(t *testing.T) {
	for _, reload := range []bool{false, true} {
		for _, consume := range []bool{false, true} {
			for _, privileged := range []bool{false, true} {
				for _, tc := range []bool{false, true} {
					for _, failReload := range []bool{false, true} {
						name := fmt.Sprintf("reload=%v/consume=%v/privileged=%v/tc=%v/failure=%v", reload, consume, privileged, tc, failReload)
						t.Run(name, func(t *testing.T) {
							srv, dir, cookies, cleanup := setupApplyEnv(t, true)
							defer cleanup()
							srv.applyReload, srv.consumeGenerated = reload, consume
							srv.privilegedApply, srv.applyTrafficControl = privileged, tc
							cfg, err := config.LoadFromFile(srv.cfgPath)
							if err != nil {
								t.Fatal(err)
							}
							cfg.QoS.Enable, cfg.QoS.UpMbps = true, 10
							if err := config.SaveToFile(srv.cfgPath, cfg); err != nil {
								t.Fatal(err)
							}
							last := filepath.Join(dir, "revisions", "previous")
							if err := os.MkdirAll(last, 0755); err != nil {
								t.Fatal(err)
							}
							if err := srv.generateFragments(cfg, last); err != nil {
								t.Fatal(err)
							}
							if err := srv.writeLastGoodRev("previous"); err != nil {
								t.Fatal(err)
							}
							runner := &fakeRunner{}
							if failReload {
								runner.missing = map[string]bool{"dnsmasq.service": true}
							}
							srv.runner = runner
							req := httptest.NewRequest(http.MethodPost, "/api/v1/apply", nil)
							for _, cookie := range cookies {
								req.AddCookie(cookie)
							}
							response := httptest.NewRecorder()
							srv.Handler().ServeHTTP(response, req)
							if response.Code != http.StatusOK {
								t.Fatalf("apply: %d %s", response.Code, response.Body.String())
							}
							var nftCalls, qosCalls, nftReloads int
							for _, cmd := range runner.cmds {
								switch cmd[0] {
								case "nft":
									nftCalls++
								case filepath.Join(dir, "generated", "qos.sh"):
									qosCalls++
								case "systemctl":
									if cmd[len(cmd)-1] == "nftables.service" {
										nftReloads++
									}
								}
							}
							expectedNft := 0
							if reload && consume && privileged {
								expectedNft = 1
								if failReload {
									expectedNft++
								}
							}
							if nftCalls != expectedNft {
								t.Fatalf("nft calls=%d, want %d; commands=%v", nftCalls, expectedNft, runner.cmds)
							}
							expectedQoS := 0
							if reload && tc && privileged {
								expectedQoS = 1
							}
							if qosCalls != expectedQoS {
								t.Fatalf("qos calls=%d, want %d", qosCalls, expectedQoS)
							}
							if consume && privileged && nftReloads != 0 {
								t.Fatalf("direct nft apply overwritten by unit reload: %v", runner.cmds)
							}
							if !reload && len(runner.cmds) != 0 {
								t.Fatalf("generate-only executed commands: %v", runner.cmds)
							}
						})
					}
				}
			}
		}
	}
}
