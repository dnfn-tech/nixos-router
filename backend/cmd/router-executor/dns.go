package main

import (
	"context"
	"errors"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"github.com/dnfn-tech/nixos-router/backend/internal/privsep"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func parseDNSCallbackOptions(args []string) (ipv6CallbackOptions, error) {
	o, err := parseIPv6CallbackOptions(args)
	if err != nil || (o.state != "bound" && o.state != "unbound") {
		return o, errors.New("invalid DNS callback arguments")
	}
	return o, nil
}

func runDNSCallback(args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("DNS callback must run as root")
	}
	o, err := parseDNSCallbackOptions(args)
	if err != nil {
		return err
	}
	if err = privsep.CheckState(o.stateDir, 0); err != nil {
		return err
	}
	m, err := managed.New(managed.Options{StateDir: o.stateDir, Interfaces: strings.Split(o.interfaces, ","), WiFiInterface: o.wifi, ManagementPort: o.port})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return m.HandleIPv4DNS(ctx, o.generation, o.iface, o.state, os.Getenv("NIXOS_ROUTER_IPV4_DNS"))
}
