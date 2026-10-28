package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"github.com/dnfn-tech/nixos-router/backend/internal/server"
	webfs "github.com/dnfn-tech/nixos-router/backend/web"
	"golang.org/x/crypto/bcrypt"
)

var version = "dev"

func main() {
	// Load .env in dev if present, ignore errors silently
	_ = godotenv.Load()

	addr := flag.String("addr", getEnv("NIXOS_ROUTER_ADDR", ":8080"), "HTTP listen address")
	stateDir := flag.String("state-dir", getEnv("NIXOS_ROUTER_STATE_DIR", config.DefaultStateDir), "State directory (contains config.json and state.db)")
	configPath := flag.String("config-path", os.Getenv(config.EnvConfigPath), "Path to config.json (overrides --state-dir)")
	dev := flag.Bool("dev", envBool(config.EnvDev, false), "Developer mode: no auth, permissive CORS")
	seed := flag.Bool("seed-default-config", false, "If set and config file missing, write default config.json (dev convenience)")
	webDir := flag.String("web-dir", "", "Serve WebUI from local directory instead of embedded assets (dev only)")
	applyReload := flag.Bool("apply-reload", envBool("NIXOS_ROUTER_APPLY_RELOAD", false), "Allow runtime reload during apply")
	applyTC := flag.Bool("apply-traffic-control", envBool("NIXOS_ROUTER_APPLY_TRAFFIC_CONTROL", false), "Allow applying generated QoS (tc) script during apply; requires privileges")
	consumeGen := flag.Bool("consume-generated", envBool("NIXOS_ROUTER_CONSUME_GENERATED", false), "Consume generated fragments (nft apply, rely on system units to include fragments); safe-by-default off")
	privApply := flag.Bool("privileged-apply", envBool("NIXOS_ROUTER_PRIVILEGED_APPLY", false), "Allow runtime actions (nft/tc); requires existing privileges; default off")
	allowReboot := flag.Bool("allow-reboot", envBool("NIXOS_ROUTER_ALLOW_REBOOT", false), "Allow system reboot endpoint to execute reboot")
	managedMode := flag.Bool("managed", envBool("NIXOS_ROUTER_MANAGED", false), "Explicitly own allowlisted router interfaces")
	managedInterfaces := flag.String("managed-interfaces", os.Getenv("NIXOS_ROUTER_MANAGED_INTERFACES"), "Comma-separated physical wired interface allowlist")
	wifiInterface := flag.String("wifi-interface", os.Getenv("NIXOS_ROUTER_WIFI_INTERFACE"), "Explicit AP interface")
	country := flag.String("country-code", getEnv("NIXOS_ROUTER_COUNTRY_CODE", "CN"), "AP regulatory country")
	managedSSHPorts := flag.String("managed-ssh-ports", getEnv("NIXOS_ROUTER_MANAGED_SSH_PORTS", "22"), "Trusted NixOS sshd ports to preserve, comma-separated (not HTTP draft SSH settings)")
	flag.Parse()

	opts := config.LoadOptions{
		StateDir:             *stateDir,
		ConfigPath:           *configPath,
		DevMode:              *dev,
		SeedDefaultIfMissing: *seed || *dev,
	}
	opts.StateDir, opts.ConfigPath = config.ResolvePaths(opts)
	daemonLock, err := acquireDaemonLock(opts.StateDir, *managedMode)
	if err != nil {
		log.Fatalf("managed daemon lock: %v", err)
	}
	if daemonLock != nil {
		defer daemonLock.Close()
	}
	cfg, cfgPath, startupWarning, err := loadStartupConfig(opts, *managedMode)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if startupWarning != "" {
		log.Print(startupWarning)
	}

	database, err := db.Open(opts.StateDir)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	// Seed admin user if empty
	if err := ensureAdminSeed(database, *dev, *seed); err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	var runtime server.ManagedRuntime
	if *managedMode {
		if *dev || !*applyReload || !*privApply {
			log.Fatal("managed mode requires authentication, apply-reload and privileged-apply")
		}
		has, e := database.HasAnyUser()
		if e != nil || !has {
			log.Fatal("managed mode requires an initial administrator credential")
		}
		_, portText, e := net.SplitHostPort(*addr)
		if e != nil {
			log.Fatalf("managed listen address: %v", e)
		}
		port, e := strconv.Atoi(portText)
		if e != nil {
			log.Fatal("invalid management port")
		}
		sshPorts, e := parseManagedSSHPorts(*managedSSHPorts)
		if e != nil {
			log.Fatalf("managed SSH ports: %v", e)
		}
		manager, e := managed.New(managed.Options{StateDir: opts.StateDir, Interfaces: strings.Split(*managedInterfaces, ","), WiFiInterface: *wifiInterface, ManagementPort: port, CountryCode: *country, SSHPorts: sshPorts})
		if e != nil {
			log.Fatalf("managed initialization: %v", e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		e = manager.Restore(ctx, cfg)
		cancel()
		if e != nil {
			log.Fatalf("managed startup restore failed: %v", e)
		}
		if e = database.InterruptRunningJobs(); e != nil {
			log.Fatalf("recover job state: %v", e)
		}
		runtime = manager
	}
	s := server.New(server.Options{
		ManagedRuntime:      runtime,
		StartupWarning:      startupWarning,
		WiFiInterface:       *wifiInterface,
		Config:              cfg,
		ConfigPath:          cfgPath,
		StateDir:            opts.StateDir,
		DB:                  database,
		DevMode:             *dev,
		Version:             version,
		WebFS:               webfs.Embedded,
		WebDir:              *webDir,
		ApplyReload:         *applyReload,
		ApplyTrafficControl: *applyTC,
		ConsumeGenerated:    *consumeGen,
		PrivilegedApply:     *privApply,
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	_ = allowReboot // flag currently only gates env; printed hint below
	log.Printf("routerd %s listening on %s (config=%s, stateDir=%s, dev=%v, webDir=%s, allowReboot=%v, applyReload=%v, applyTrafficControl=%v, consumeGenerated=%v, privilegedApply=%v)", version, *addr, cfgPath, opts.StateDir, *dev, *webDir, *allowReboot, *applyReload, *applyTC, *consumeGen, *privApply)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}

func ensureAdminSeed(database *db.DB, dev bool, seed bool) error {
	has, err := database.HasAnyUser()
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	pw := os.Getenv("NIXOS_ROUTER_ADMIN_PASSWORD")
	if pw == "" {
		if path := os.Getenv("NIXOS_ROUTER_ADMIN_PASSWORD_FILE"); path != "" {
			contents, e := os.ReadFile(path)
			if e != nil {
				return fmt.Errorf("read initial administrator secret: %w", e)
			}
			pw = strings.TrimRight(string(contents), "\r\n")
			if len(pw) < 8 {
				return fmt.Errorf("initial administrator secret must contain at least 8 bytes")
			}
		}
	}
	if pw == "" {
		if dev || seed {
			pw = "adminadmin"
			log.Printf("WARNING: seeding default admin password for dev (--dev or --seed-default-config): username=admin password=%s (DO NOT USE IN PRODUCTION)", pw)
		} else {
			log.Printf("WARNING: no admin user present; set NIXOS_ROUTER_ADMIN_PASSWORD to create initial admin account. Until then, login is disabled.")
			return nil
		}
	}
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	if err := database.CreateUser("admin", string(hashBytes)); err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}
	database.AddAudit("system", "seed_admin", "created initial admin user")
	return nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch v {
		case "1", "true", "TRUE", "yes", "on":
			return true
		case "0", "false", "FALSE", "no", "off":
			return false
		default:
			return def
		}
	}
	return def
}

// A corrupt saved draft must not prevent restoring the last applied network.
// Preserve the draft file for diagnosis; the API can replace it explicitly.
func loadStartupConfig(opts config.LoadOptions, managedMode bool) (config.Config, string, string, error) {
	stateDir, path := config.ResolvePaths(opts)
	var cfg config.Config
	var err error
	if managedMode {
		// Do not let LoadOrDefault hide a deleted draft or overwrite it via seeding.
		cfg, err = config.LoadFromFile(path)
	} else {
		cfg, path, err = config.LoadOrDefault(opts)
	}
	if err == nil {
		err = cfg.Validate()
	}
	if err == nil {
		return cfg, path, "", nil
	}
	if !managedMode {
		return cfg, path, "", err
	}
	good, goodErr := config.LoadFromFile(filepath.Join(stateDir, "managed/last-good.json"))
	if goodErr == nil {
		goodErr = good.Validate()
	}
	if goodErr != nil {
		if errors.Is(err, os.ErrNotExist) && errors.Is(goodErr, os.ErrNotExist) && opts.SeedDefaultIfMissing {
			cfg, path, err = config.LoadOrDefault(opts)
			if err == nil {
				err = cfg.Validate()
			}
			return cfg, path, "", err
		}
		return cfg, path, "", fmt.Errorf("draft invalid: %v; last-good unavailable: %w", err, goodErr)
	}
	return good, path, "Saved draft is missing or invalid; restored last-good. Repair and save configuration before applying.", nil
}

// Keep this descriptor open for the daemon lifetime. Never unlink the lock path:
// another process could otherwise lock a different inode while this one is held.
func acquireDaemonLock(stateDir string, managedMode bool) (*os.File, error) {
	if !managedMode {
		return nil, nil
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, "daemon.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("state directory is already owned by another daemon or cannot be locked: %w", err)
	}
	return f, nil
}

// Parse trusted startup configuration, never the editable HTTP configuration.
func parseManagedSSHPorts(value string) ([]int, error) {
	if strings.ContainsAny(value, "\r\n") {
		return nil, fmt.Errorf("SSH ports must be comma-separated integers in 1..65535")
	}
	ports := []int{}
	seen := map[int]bool{}
	for _, token := range strings.Split(value, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			return nil, fmt.Errorf("SSH ports must not contain empty entries")
		}
		for _, ch := range token {
			if ch < '0' || ch > '9' {
				return nil, fmt.Errorf("SSH port %q must contain only decimal digits", token)
			}
		}
		port, err := strconv.Atoi(token)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("SSH port %q must be in 1..65535", token)
		}
		if seen[port] {
			return nil, fmt.Errorf("duplicate SSH port %d", port)
		}
		seen[port] = true
		ports = append(ports, port)
	}
	return ports, nil
}
