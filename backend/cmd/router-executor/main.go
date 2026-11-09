// router-executor owns network transactions and private runtime state. It has
// no TCP listener, Web assets, administrator sessions, or shell-command API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"github.com/dnfn-tech/nixos-router/backend/internal/privsep"
)

type options struct {
	stateDir, socket, bootstrap, apiUser, interfaces, wifi, country string
	port                                                            int
	sshPorts                                                        []int
}

func parseOptions(args []string) (options, error) {
	var o options
	var ports string
	fs := flag.NewFlagSet("router-executor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.stateDir, "state-dir", "", "Private executor state directory, provisioned by supervisor")
	fs.StringVar(&o.socket, "socket", "", "Unix socket in a root-owned, supervisor-provisioned directory")
	fs.StringVar(&o.bootstrap, "bootstrap", "", "Private initial config; only used without last-good")
	fs.StringVar(&o.apiUser, "api-user", "", "Dedicated unprivileged API account")
	fs.StringVar(&o.interfaces, "interfaces", "", "Comma-separated wired interface allowlist")
	fs.StringVar(&o.wifi, "wifi-interface", "", "Explicit AP interface")
	fs.StringVar(&o.country, "country-code", "CN", "AP regulatory country")
	fs.IntVar(&o.port, "management-port", 8080, "Trusted HTTP management port")
	fs.StringVar(&ports, "ssh-ports", "22", "Trusted host SSH listener ports")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() != 0 || o.apiUser == "" || o.interfaces == "" {
		return o, errors.New("API account, interfaces and named paths required")
	}
	for _, p := range []string{o.stateDir, o.socket, o.bootstrap} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return o, errors.New("clean absolute paths required")
		}
	}
	if o.port < 1 || o.port > 65535 {
		return o, errors.New("invalid management port")
	}
	for _, s := range strings.Split(ports, ",") {
		port, err := strconv.Atoi(s)
		if err != nil || port < 1 || port > 65535 {
			return o, errors.New("invalid SSH ports")
		}
		o.sshPorts = append(o.sshPorts, port)
	}
	return o, nil
}
func main() {
	o, err := parseOptions(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if err = run(o); err != nil {
		log.Fatal(err)
	}
}
func run(o options) error {
	if os.Geteuid() != 0 {
		return errors.New("executor must run as root; HTTP API must use its own account")
	}
	account, err := user.Lookup(o.apiUser)
	if err != nil {
		return err
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return errors.New("API UID must be non-root")
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil || gid == 0 {
		return errors.New("API group must be non-root")
	}
	if err = privsep.CheckState(o.stateDir, 0); err != nil {
		return err
	}
	if err = privsep.CheckDirectory(filepath.Dir(o.socket), 0); err != nil {
		return err
	}
	// The root-owned directory grants traversal to the API group, never write.
	if err = os.Chown(filepath.Dir(o.socket), 0, int(gid)); err != nil {
		return err
	}
	if err = os.Chmod(filepath.Dir(o.socket), 0710); err != nil {
		return err
	}
	l, err := privsep.Listen(o.socket, uint32(uid))
	if err != nil {
		return err
	}
	defer l.Close()
	if err = os.Chown(o.socket, int(uid), int(gid)); err != nil {
		return err
	}
	manager, err := managed.New(managed.Options{StateDir: o.stateDir, Interfaces: strings.Split(o.interfaces, ","), WiFiInterface: o.wifi, ManagementPort: o.port, SSHPorts: o.sshPorts, CountryCode: o.country})
	if err != nil {
		return err
	}
	executor, err := privsep.OpenExecutor(o.stateDir, manager)
	if err != nil {
		return err
	}
	defer executor.Close()
	var bootstrap config.Config
	if _, err = os.Stat(filepath.Join(o.stateDir, "managed", "last-good.json")); os.IsNotExist(err) {
		bootstrap, err = readBootstrap(o.bootstrap)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	err = manager.Restore(ctx, bootstrap)
	cancel()
	if err != nil {
		return fmt.Errorf("startup restoration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- executor.Serve(l) }()
	if err := notifyReady(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return executor.Close()
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
func readBootstrap(path string) (config.Config, error) {
	var zero config.Config
	if err := privsep.CheckDirectory(filepath.Dir(path), 0); err != nil {
		return zero, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return zero, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.Mode().IsRegular() || st.Uid != 0 || st.Nlink != 1 || fi.Mode().Perm()&0077 != 0 {
		return zero, errors.New("bootstrap must be a private root-owned regular file")
	}
	return config.LoadFromFile(path)
}
