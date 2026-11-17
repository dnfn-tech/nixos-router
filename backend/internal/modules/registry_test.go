package modules

import (
	"strings"
	"testing"
)

func TestRegistryRejectsConflictingDescriptors(t *testing.T) {
	for _, ds := range [][]Descriptor{{{ID: "../x"}}, {{ID: "adblock"}, {ID: "adblock"}}, {{ID: "one", Path: "/same"}, {ID: "two", Path: "/same"}}} {
		if _, err := NewRegistry(ds); err == nil {
			t.Fatal("invalid registry accepted")
		}
	}
}
func TestAdBlockValidationAndDNSRendering(t *testing.T) {
	values := map[string]interface{}{"entries": []interface{}{"Ads.Example.", "ads.example", "other.example"}}
	if err := Validate("adblock", true, values, true); err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderDNS(values)
	if err != nil || rendered != "local=/ads.example/\naddress=/ads.example/#\nlocal=/other.example/\naddress=/other.example/#\n" {
		t.Fatalf("%q %v", rendered, err)
	}
	for _, domain := range []string{"a\nserver=evil", "-bad.example", "a..example", "*", "https://a.example", strings.Repeat("a", 64) + ".example", "127.0.0.1", "例子.cn"} {
		if err := Validate("adblock", true, map[string]interface{}{"entries": []interface{}{domain}}, true); err == nil {
			t.Fatalf("unsafe domain accepted %q", domain)
		}
	}
	for _, values := range []map[string]interface{}{{"entries": "wrong"}, {"entries": []interface{}{123}}, {"lists": []interface{}{"https://example/list"}}, {"unknown": true}} {
		if err := Validate("adblock", true, values, true); err == nil {
			t.Fatal("unsupported settings accepted")
		}
	}
	if err := Validate("adblock", true, values, false); err == nil {
		t.Fatal("missing local DNS accepted")
	}
	if err := Validate("custom", true, nil, true); err == nil {
		t.Fatal("unknown enabled module accepted")
	}
	if err := Validate("custom", false, map[string]interface{}{"private": "preserved"}, true); err != nil {
		t.Fatal(err)
	}
}
