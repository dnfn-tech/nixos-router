package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

func validateExecutorMode(path string, managed, dev, legacy bool, uid int) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("executor socket requires a clean absolute path")
	}
	if !managed || dev || legacy || uid == 0 {
		return fmt.Errorf("executor mode requires authenticated managed API under a non-root UID without legacy runtime flags")
	}
	return nil
}

type appliedReader interface {
	Applied(context.Context) (config.Config, bool, error)
}

func loadRemoteStartupConfig(opts config.LoadOptions, runtime appliedReader) (config.Config, string, string, error) {
	_, path := config.ResolvePaths(opts)
	cfg, err := config.LoadFromFile(path)
	if err == nil {
		err = cfg.Validate()
	}
	if err == nil {
		return cfg, path, "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	applied, ok, readErr := runtime.Applied(ctx)
	if readErr != nil || !ok {
		return cfg, path, "", fmt.Errorf("draft invalid: %v; executor applied snapshot unavailable: %v", err, readErr)
	}
	if readErr = applied.Validate(); readErr != nil {
		return cfg, path, "", fmt.Errorf("invalid executor snapshot: %w", readErr)
	}
	return applied, path, "Saved draft is missing or invalid; showing executor's applied configuration. Repair and save before applying.", nil
}
