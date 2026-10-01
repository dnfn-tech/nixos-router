package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	EnvConfigPath = "NIXOS_ROUTER_CONFIG"
	EnvStateDir   = "NIXOS_ROUTER_STATE_DIR"
	EnvDev        = "NIXOS_ROUTER_DEV"
)

type LoadOptions struct {
	StateDir   string // if empty, use default
	ConfigPath string // if empty, derived from state dir
	DevMode    bool
	// If true and config is missing, write a default file (useful in dev)
	SeedDefaultIfMissing bool
}

// ResolvePaths computes stateDir and configPath based on overrides and env.
func ResolvePaths(opts LoadOptions) (stateDir string, configPath string) {
	// Environment overrides first
	if v := os.Getenv(EnvStateDir); v != "" {
		stateDir = v
	} else if opts.StateDir != "" {
		stateDir = opts.StateDir
	} else {
		stateDir = DefaultStateDir
	}

	if v := os.Getenv(EnvConfigPath); v != "" {
		configPath = v
	} else if opts.ConfigPath != "" {
		configPath = opts.ConfigPath
	} else {
		configPath = filepath.Join(stateDir, DefaultConfigBasename)
	}
	return
}

// LoadOrDefault tries to load config from configPath. When it doesn't exist,
// returns DefaultConfig(). If SeedDefaultIfMissing is set, writes a default file
// (only when a stateDir is provided or derivable).
func LoadOrDefault(opts LoadOptions) (Config, string, error) {
	stateDir, configPath := ResolvePaths(opts)

	if _, err := os.Stat(configPath); err == nil {
		cfg, err := LoadFromFile(configPath)
		return cfg, configPath, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, configPath, fmt.Errorf("stat config: %w", err)
	}

	// Missing: seed default in memory (and optionally write it)
	cfg := DefaultConfig()
	if opts.SeedDefaultIfMissing && stateDir != "" {
		if err := SaveToFile(configPath, cfg); err != nil {
			return Config{}, configPath, fmt.Errorf("seed default config: %w", err)
		}
	}
	return cfg, configPath, nil
}

