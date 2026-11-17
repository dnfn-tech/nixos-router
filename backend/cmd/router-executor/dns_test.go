package main

import "testing"

func TestDNSCallbackOptions(t *testing.T) {
	base := []string{"--state-dir", "/var/lib/router", "--interfaces", "wan0,lan0", "--generation", "generation-test", "--interface", "wan0", "--event", "bound"}
	if _, err := parseDNSCallbackOptions(base); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"unbound", "updated", "stopped", "exec"} {
		args := append(append([]string{}, base...), "--event", event)
		_, err := parseDNSCallbackOptions(args)
		if (err == nil) != (event == "unbound") {
			t.Fatal(event, err)
		}
	}
	for _, extra := range [][]string{{"--command", "reboot"}, {"--generation", "../escape"}, {"--state-dir", "relative"}, {"--interface", "bad;cmd"}} {
		if _, err := parseDNSCallbackOptions(append(append([]string{}, base...), extra...)); err == nil {
			t.Fatal("unsafe options accepted")
		}
	}
}
