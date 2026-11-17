package managed

import (
	"context"
	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"strings"
	"testing"
)

func TestAdBlockParticipatesInManagedGeneration(t *testing.T) {
	m, _, c := fixture(t)
	c.Plugins = map[string]config.PluginConfig{"adblock": {Enable: true, Config: map[string]interface{}{"entries": []interface{}{"ads.example"}}}}
	if err := m.validate(c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.render(c)["dnsmasq.conf"], "address=/ads.example/#") {
		t.Fatal("blocking missing from owned DNS")
	}
	c.Plugins["adblock"] = config.PluginConfig{}
	if strings.Contains(m.render(c)["dnsmasq.conf"], "ads.example") {
		t.Fatal("disabled rule remains")
	}
	m.opts.Modules = []string{}
	c.Plugins["adblock"] = config.PluginConfig{Enable: true}
	if _, err := m.Apply(context.Background(), c); err == nil {
		t.Fatal("deployment module deny ignored")
	}
}

func TestAdBlockActivationFailureDoesNotPublishCandidate(t *testing.T) {
	m, r, c := fixture(t)
	if _, err := m.Apply(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	c.Plugins = map[string]config.PluginConfig{"adblock": {Enable: true, Config: map[string]interface{}{"entries": []interface{}{"ads.example"}}}}
	r.fail = "systemctl restart nixos-router-dnsmasq"
	result, err := m.Apply(context.Background(), c)
	if err == nil || result.RollbackStatus != "succeeded" {
		t.Fatalf("failure was not propagated: %+v %v", result, err)
	}
	actual, known := m.AppliedConfig()
	if !known || actual.Plugins["adblock"].Enable {
		t.Fatal("failed candidate published as applied")
	}
}

func TestNoSSHListenerDoesNotInventPort22(t *testing.T) {
	m, _, c := fixture(t)
	opts := m.opts
	opts.SSHPorts = nil
	empty, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	rules := empty.render(c)["firewall.nft"]
	if !strings.Contains(rules, `tcp dport { 8080 } drop`) || strings.Contains(rules, `8080, 22`) {
		t.Fatal(rules)
	}
}
