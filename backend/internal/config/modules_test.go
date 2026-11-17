package config

import "testing"

func TestModuleValidationAtConfigBoundary(t *testing.T) {
	c := DefaultConfig()
	c.Plugins = map[string]PluginConfig{"adblock": {Enable: true, Config: map[string]interface{}{"entries": []interface{}{"a\nserver=bad"}}}}
	if err := c.Validate(); err == nil {
		t.Fatal("unsafe module accepted")
	}
	c.Plugins["adblock"] = PluginConfig{Enable: true, Config: map[string]interface{}{"entries": []interface{}{"ads.example"}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.DNS.EnableDNSMasq = false
	if err := c.Validate(); err == nil {
		t.Fatal("missing local DNS accepted")
	}
	c.Plugins = map[string]PluginConfig{"unknown": {Enable: true}}
	if err := c.Validate(); err == nil {
		t.Fatal("unknown enabled module accepted")
	}
	c.Plugins["unknown"] = PluginConfig{Config: map[string]interface{}{"untouched": "value"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
