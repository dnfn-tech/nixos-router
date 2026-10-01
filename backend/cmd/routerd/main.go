package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/db"
	"github.com/dnfn-tech/nixos-router/backend/internal/server"
	webfs "github.com/dnfn-tech/nixos-router/backend/web"
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
	flag.Parse()

	opts := config.LoadOptions{
		StateDir:              *stateDir,
		ConfigPath:            *configPath,
		DevMode:               *dev,
		SeedDefaultIfMissing:  *seed || *dev,
	}
	cfg, cfgPath, err := config.LoadOrDefault(opts)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	database, err := db.Open(opts.StateDir)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	s := server.New(server.Options{
		Config:     cfg,
		ConfigPath: cfgPath,
		StateDir:   opts.StateDir,
		DB:         database,
		DevMode:    *dev,
		Version:    version,
		WebFS:      webfs.Embedded,
		WebDir:     *webDir,
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("routerd %s listening on %s (config=%s, stateDir=%s, dev=%v, webDir=%s)", version, *addr, cfgPath, opts.StateDir, *dev, *webDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
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

