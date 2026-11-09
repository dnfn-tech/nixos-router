package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func TestPluginAPISecretReferences(t *testing.T) {
	s := concurrentServer(t)
	fixture := s.configSnapshot()
	fixture.Plugins = map[string]config.PluginConfig{"custom": {Config: map[string]interface{}{"peers": []interface{}{map[string]interface{}{"name": "a", "token": "secret-a"}, map[string]interface{}{"name": "b", "token": "secret-b"}}}}}
	raw, _ := json.Marshal(fixture)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, withConfigRevision(s, httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(raw))))
	if w.Code != 200 {
		t.Fatalf("seed failed %d", w.Code)
	}
	for _, endpoint := range []struct{ method, path string }{{"PUT", "/api/v1/config"}, {"POST", "/api/v1/backup/restore"}, {"PATCH", "/api/v1/plugins/custom"}} {
		get := httptest.NewRecorder()
		s.Handler().ServeHTTP(get, httptest.NewRequest("GET", "/api/v1/config", nil))
		if strings.Contains(get.Body.String(), "secret-a") || strings.Contains(get.Body.String(), "secret-b") {
			t.Fatal("default API exposed plugin credential")
		}
		var response struct{ Config config.Config }
		if err := json.Unmarshal(get.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		peers := response.Config.Plugins["custom"].Config["peers"].([]interface{})
		peers[0], peers[1] = peers[1], peers[0]
		var body interface{} = response.Config
		if endpoint.method == "PATCH" {
			body = map[string]interface{}{"config": response.Config.Plugins["custom"].Config}
		}
		raw, _ = json.Marshal(body)
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, withConfigRevision(s, httptest.NewRequest(endpoint.method, endpoint.path, bytes.NewReader(raw))))
		if w.Code != 200 {
			t.Fatalf("reference rejected by %s: %d", endpoint.path, w.Code)
		}
		actual, err := config.LoadFromFile(s.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range actual.Plugins["custom"].Config["peers"].([]interface{}) {
			row := item.(map[string]interface{})
			if row["token"] != "secret-"+row["name"].(string) {
				t.Fatalf("%s persisted a marker or mismatched credential", endpoint.path)
			}
		}
	}
}

func TestModeSwitchPreservesInactiveSecrets(t *testing.T) {
	old := config.DefaultConfig()
	old.WAN.Mode = "pppoe"
	old.WAN.PPPoE = &config.PPPoEConfig{Username: "user", Password: "wan-secret"}
	if err := json.Unmarshal([]byte(`{"enable":false,"provider":"cloudflare","cloudflare":{"apiToken":"cloud-secret"},"duckdns":{"token":"duck-secret"},"aliyun":{"accessKey":"access-secret","secretKey":"ali-secret"},"custom":{"token":"custom-secret"}}`), &old.DDNS); err != nil {
		t.Fatal(err)
	}
	incoming := old.RedactedCopy()
	incoming.WAN.Mode = "dhcp"
	incoming.DDNS.Provider = "duckdns"
	got := (&Server{}).mergeSecrets(old, incoming)
	if got.WAN.PPPoE.Password != "wan-secret" || got.DDNS.Cloudflare.APIToken != "cloud-secret" || got.DDNS.DuckDNS.Token != "duck-secret" || got.DDNS.Aliyun.AccessKey != "access-secret" || got.DDNS.Aliyun.SecretKey != "ali-secret" || got.DDNS.Custom.Token != "custom-secret" {
		t.Fatal("mode/provider switch replaced inactive credentials with placeholders")
	}
}

func TestPluginArrayBlankDoesNotClearSecret(t *testing.T) {
	s := concurrentServer(t)
	fixture := s.configSnapshot()
	fixture.Plugins = map[string]config.PluginConfig{"custom": {Config: map[string]interface{}{"peers": []interface{}{map[string]interface{}{"name": "a", "token": "secret-a"}}}}}
	save := func(c config.Config) int {
		raw, _ := json.Marshal(c)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, withConfigRevision(s, httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(raw))))
		return w.Code
	}
	if code := save(fixture); code != 200 {
		t.Fatalf("seed %d", code)
	}
	for _, placeholder := range []interface{}{"", "****", nil} {
		current := s.configSnapshot()
		incoming := current.RedactedCopy()
		incoming.Plugins["custom"].Config["peers"].([]interface{})[0].(map[string]interface{})["token"] = placeholder
		if code := save(incoming); code != 200 {
			t.Fatalf("preserve %d", code)
		}
		actual, err := config.LoadFromFile(s.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if actual.Plugins["custom"].Config["peers"].([]interface{})[0].(map[string]interface{})["token"] != "secret-a" {
			t.Fatal("blank cleared disk secret")
		}
		incoming.Plugins["custom"].Config["peers"].([]interface{})[0].(map[string]interface{})["name"] = "different"
		if code := save(incoming); code != 400 {
			t.Fatalf("ambiguous blank status %d", code)
		}
	}
}
