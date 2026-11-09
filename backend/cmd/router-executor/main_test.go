package main

import "testing"

func TestParseOptionsRejectIncompleteOrUnsafeArguments(t *testing.T) {
	good := []string{"--state-dir", "/var/lib/router-executor", "--socket", "/run/router-executor/control.sock", "--bootstrap", "/run/secrets/router.json", "--api-user", "router-api", "--interfaces", "wan0,lan0"}
	if _, err := parseOptions(good); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{}, append(append([]string{}, good...), "--state-dir", "relative"),
		append(append([]string{}, good...), "--ssh-ports", "22,invalid"),
		append(append([]string{}, good...), "--ssh-ports", "0"),
		append(append([]string{}, good...), "--management-port", "65536"),
		append(append([]string{}, good...), "--socket", "/run/../tmp/control.sock"),
		append(append([]string{}, good...), "extra-positional"),
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("unsafe arguments accepted: %v", args)
		}
	}
}
