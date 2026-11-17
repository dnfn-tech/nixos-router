package main

import "testing"

func TestParseIPv6CallbackOptions(t *testing.T) {
	good := []string{"--state-dir", "/var/lib/router", "--interfaces", "wan0,lan0", "--generation", "generation-123", "--interface", "wan0", "--event", "bound"}
	o, err := parseIPv6CallbackOptions(good)
	if err != nil || o.generation != "generation-123" || o.iface != "wan0" || o.state != "bound" {
		t.Fatal(o, err)
	}
	for _, extra := range [][]string{
		{"--state-dir", "relative"}, {"--state-dir", "/var/../tmp/router"},
		{"--generation", "../generation-123"}, {"--generation", ""},
		{"--interface", "wan0;reboot"}, {"--event", "exec"},
		{"--interfaces", ""}, {"--management-port", "0"}, {"unexpected"}, {"--command", "reboot"},
	} {
		if _, err := parseIPv6CallbackOptions(append(append([]string{}, good...), extra...)); err == nil {
			t.Fatal("unsafe callback arguments accepted", extra)
		}
	}
	if _, err := parseIPv6CallbackOptions(nil); err == nil {
		t.Fatal("missing options accepted")
	}
}
