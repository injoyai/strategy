// Package logging builds the process-wide structured logger.
//
// Log fields are stable snake_case keys so downstream collection can rely on
// them: request_id, job_id, run_id, workspace_id, worker_id, method, path,
// status, duration_ms. Packages add contextual fields via slog.With.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/injoyai/strategy/internal/config"
)

// New builds the root logger from configuration.
func New(cfg config.Log) (*slog.Logger, error) {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("logging: unknown level %q", cfg.Level)
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("logging: unknown format %q", cfg.Format)
	}

	logger := slog.New(handler).With(
		slog.String("service", "researchd"),
	)
	slog.SetDefault(logger)
	return logger, nil
}
