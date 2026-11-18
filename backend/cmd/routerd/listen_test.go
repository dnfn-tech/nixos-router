package main

import "testing"

func TestManagedListenSurvivesLANAddressChange(t *testing.T) {
	for _, addr := range []string{"192.168.124.1:8080", "127.0.0.1:8080", "[fd00::1]:8080", ":8080"} {
		got, err := managedListenAddress(addr)
		if err != nil || got != ":8080" {
			t.Fatal(addr, got, err)
		}
	}
	for _, addr := range []string{"bad", "host:0", "host:65536", "host:http"} {
		if _, err := managedListenAddress(addr); err == nil {
			t.Fatal(addr)
		}
	}
}

func TestManagedListenerRejectsExecutorPortMismatch(t *testing.T) {
	if err := checkManagementPort(":8080", 8080); err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{0, 8081} {
		if checkManagementPort(":8080", port) == nil {
			t.Fatal("unprotected listener accepted", port)
		}
	}
}
