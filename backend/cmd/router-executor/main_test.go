package main

import (
	"strings"
	"testing"
)

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

func TestWANUDPServiceArguments(t *testing.T) {
	good := []string{"--state-dir", "/var/lib/router-executor", "--socket", "/run/router-executor/control.sock", "--bootstrap", "/run/secrets/router.json", "--api-user", "router-api", "--interfaces", "wan0,lan0"}
	for _, value := range []string{"", "9993,51820"} {
		o, err := parseOptions(append(append([]string{}, good...), "--wan-udp-ports", value))
		if err != nil {
			t.Fatal(err)
		}
		if value == "" && len(o.wanUDPPorts) != 0 {
			t.Fatal(o.wanUDPPorts)
		}
		if value != "" && (len(o.wanUDPPorts) != 2 || o.wanUDPPorts[0] != 9993) {
			t.Fatal(o.wanUDPPorts)
		}
	}
	for _, value := range []string{"0", "65536", "9993,", "udp/9993", "-1", "9993,1;true", strings.Repeat("9993,", 64) + "9993"} {
		if _, err := parseOptions(append(append([]string{}, good...), "--wan-udp-ports", value)); err == nil {
			t.Fatal("unsafe UDP port accepted", value)
		}
	}
}

func TestConfirmationPolicyArguments(t *testing.T) {
	good := []string{"--state-dir", "/var/lib/router-executor", "--socket", "/run/router-executor/control.sock", "--bootstrap", "/run/secrets/router.json", "--api-user", "router-api", "--interfaces", "wan0,lan0"}
	o, err := parseOptions(append(append([]string{}, good...), "--management-access", `[{"interface":"zt0","sources":["10.0.0.0/24"]}]`, "--confirmation-timeout", "30s"))
	if err != nil || len(o.managementAccess) != 1 || o.confirmationTimeout.Seconds() != 30 {
		t.Fatal(o, err)
	}
	for _, timeout := range []string{"0s", "9s", "601s"} {
		if _, err := parseOptions(append(append([]string{}, good...), "--confirmation-timeout", timeout)); err == nil {
			t.Fatal(timeout)
		}
	}
}
