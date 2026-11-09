package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPluginSecretsRedactedAndReordered(t *testing.T) {
	c := DefaultConfig()
	c.Revision = "11111111111111111111111111111111"
	c.SchemaVersion = 1
	c.Plugins = map[string]PluginConfig{"custom": {Config: map[string]interface{}{"peers": []interface{}{
		map[string]interface{}{"name": "a", "token": "secret-a"}, map[string]interface{}{"name": "b", "token": "secret-b"},
	}}}}
	red := c.RedactedCopy()
	raw, _ := json.Marshal(red)
	if strings.Contains(string(raw), "secret-a") || strings.Contains(string(raw), "secret-b") {
		t.Fatal("default redaction exposed plugin secrets")
	}
	peers := red.Plugins["custom"].Config["peers"].([]interface{})
	peers[0], peers[1] = peers[1], peers[0]
	restored, err := ResolvePluginSecrets(c, red.Plugins)
	if err != nil {
		t.Fatal(err)
	}
	got := restored["custom"].Config["peers"].([]interface{})
	if got[0].(map[string]interface{})["token"] != "secret-b" || got[1].(map[string]interface{})["token"] != "secret-a" {
		t.Fatal("reordering changed credentials")
	}
	if c.Plugins["custom"].Config["peers"].([]interface{})[0].(map[string]interface{})["name"] != "a" {
		t.Fatal("redaction mutated source")
	}
	c.Revision = "22222222222222222222222222222222"
	if _, err := ResolvePluginSecrets(c, red.Plugins); err == nil {
		t.Fatal("stale reference accepted")
	}
}

func TestPluginSecretReferencesCannotBecomePublicFields(t *testing.T) {
	c := DefaultConfig()
	c.Plugins = map[string]PluginConfig{"custom": {Config: map[string]interface{}{"privateKey": map[string]interface{}{"nested": "hidden"}, "label": "public"}}}
	red := c.RedactedCopy()
	ref := red.Plugins["custom"].Config["privateKey"]
	if _, ok := ref.(string); !ok {
		t.Fatal("secret object not redacted")
	}
	red.Plugins["custom"].Config["label"] = ref
	if _, err := ResolvePluginSecrets(c, red.Plugins); err == nil {
		t.Fatal("reference resolved into public field")
	}
	red.Plugins["custom"].Config["label"] = "public"
	restored, err := ResolvePluginSecrets(c, red.Plugins)
	if err != nil {
		t.Fatal(err)
	}
	restored["custom"].Config["privateKey"].(map[string]interface{})["nested"] = "changed"
	if c.Plugins["custom"].Config["privateKey"].(map[string]interface{})["nested"] != "hidden" {
		t.Fatal("resolver aliased source secret")
	}
	red.Plugins["custom"].Config["privateKey"] = "__router_secret_ref_v1_unknown__"
	if _, err := ResolvePluginSecrets(c, red.Plugins); err == nil || strings.Contains(err.Error(), "unknown__") {
		t.Fatal("invalid reference accepted or echoed")
	}
}

func TestClearSecretsPreservesPluginNumberLiterals(t *testing.T) {
	for _, literal := range []string{"9007199254740993", "1e400", "1.234567890123456789"} {
		t.Run(literal, func(t *testing.T) {
			c := DefaultConfig()
			c.Plugins = map[string]PluginConfig{"custom": {Config: map[string]interface{}{"token": "saved-secret", "number": json.Number(literal)}}}
			out, err := ClearSecrets(c, [][]string{{"plugins", "custom", "config", "token"}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(out.Plugins["custom"].Config["number"])
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != literal {
				t.Fatalf("number changed: got %s, want %s", got, literal)
			}
		})
	}
}

func TestArraySecretPlaceholdersPreserveOnlyUnambiguousItems(t *testing.T) {
	old := DefaultConfig()
	old.Plugins = map[string]PluginConfig{"custom": {Config: map[string]interface{}{"peers": []interface{}{
		map[string]interface{}{"name": "a", "token": "secret-a"}, map[string]interface{}{"name": "b", "token": ""},
	}}}}
	for _, blank := range []interface{}{"", "****", nil} {
		incoming := old.RedactedCopy().Plugins
		rows := incoming["custom"].Config["peers"].([]interface{})
		rows[0].(map[string]interface{})["token"] = blank
		rows[0], rows[1] = rows[1], rows[0]
		got, err := ResolvePluginSecrets(old, incoming)
		if err != nil {
			t.Fatal(err)
		}
		if got["custom"].Config["peers"].([]interface{})[1].(map[string]interface{})["token"] != "secret-a" {
			t.Fatal("placeholder erased existing secret")
		}
		rows[1].(map[string]interface{})["name"] = "changed"
		if _, err := ResolvePluginSecrets(old, incoming); err == nil {
			t.Fatal("ambiguous blank accepted")
		}
	}
}

func TestArraySecretAmbiguityPropagatesIntoNestedArrays(t *testing.T) {
	old := DefaultConfig()
	old.Plugins = map[string]PluginConfig{"custom": {Config: map[string]interface{}{"groups": []interface{}{map[string]interface{}{"name": "old", "peers": []interface{}{map[string]interface{}{"token": "secret"}}}}}}}
	incoming := old.RedactedCopy().Plugins
	group := incoming["custom"].Config["groups"].([]interface{})[0].(map[string]interface{})
	group["name"] = "new"
	group["peers"].([]interface{})[0].(map[string]interface{})["token"] = ""
	if _, err := ResolvePluginSecrets(old, incoming); err == nil {
		t.Fatal("nested ambiguous blank accepted")
	}
}

func TestDuplicateArrayItemsRequireSecretReferences(t *testing.T) {
	old := DefaultConfig()
	old.Plugins = map[string]PluginConfig{"custom": {Config: map[string]interface{}{"peers": []interface{}{map[string]interface{}{"token": "one"}, map[string]interface{}{"token": "two"}}}}}
	incoming := old.RedactedCopy().Plugins
	if _, err := ResolvePluginSecrets(old, incoming); err != nil {
		t.Fatal(err)
	}
	incoming["custom"].Config["peers"].([]interface{})[0].(map[string]interface{})["token"] = ""
	if _, err := ResolvePluginSecrets(old, incoming); err == nil {
		t.Fatal("duplicate public shapes accepted blank")
	}
}
