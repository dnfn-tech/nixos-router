package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"github.com/dnfn-tech/nixos-router/backend/internal/privsep"
)

type ipv6CallbackOptions struct {
	stateDir, interfaces, wifi, generation, iface, state string
	port                                                 int
}

func parseIPv6CallbackOptions(args []string) (ipv6CallbackOptions, error) {
	var o ipv6CallbackOptions
	fs := flag.NewFlagSet("ipv6-event", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.stateDir, "state-dir", "", "Private executor state")
	fs.StringVar(&o.interfaces, "interfaces", "", "Wired interface allowlist")
	fs.StringVar(&o.wifi, "wifi-interface", "", "AP interface")
	fs.StringVar(&o.generation, "generation", "", "Immutable service generation")
	fs.StringVar(&o.iface, "interface", "", "Protocol interface")
	fs.StringVar(&o.state, "event", "", "Protocol event")
	fs.IntVar(&o.port, "management-port", 8080, "Management port")
	if err := fs.Parse(args); err != nil {
		return o, errors.New("invalid IPv6 callback arguments")
	}
	if fs.NArg() != 0 || !filepath.IsAbs(o.stateDir) || filepath.Clean(o.stateDir) != o.stateDir || o.interfaces == "" || o.port < 1 || o.port > 65535 ||
		!regexp.MustCompile(`^generation-[A-Za-z0-9_-]+$`).MatchString(o.generation) || !regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`).MatchString(o.iface) {
		return o, errors.New("invalid IPv6 callback arguments")
	}
	switch o.state {
	case "started", "bound", "informed", "updated", "ra-updated", "rebound", "unbound", "stopped":
	default:
		return o, errors.New("invalid IPv6 callback event")
	}
	return o, nil
}

func runIPv6Callback(args []string) error {
	// Capture before parsing, state validation or waiting for the generation
	// lock. Time spent in those operations must not extend protocol leases.
	observedAt := time.Now()
	if os.Geteuid() != 0 {
		return errors.New("IPv6 callback must run as root")
	}
	o, err := parseIPv6CallbackOptions(args)
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
	env := map[string]string{}
	for _, name := range []string{"ADDRESSES", "RA_ADDRESSES", "PREFIXES", "RA_ROUTES", "RDNSS", "RA_DNS", "RA_MTU"} {
		env[name] = os.Getenv(name)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return m.HandleIPv6Event(ctx, o.generation, o.iface, o.state, env, observedAt)
}
