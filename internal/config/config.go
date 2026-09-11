// Package config loads researchd configuration.
//
// Loading priority (later wins): built-in defaults -> config file -> environment
// variables. Every error names the configuration field and its source (file path
// or environment variable name) so operators can fix inputs without reading code.
// The configuration intentionally holds no secret material: bearer tokens are
// referenced by file path only, and errors never include file contents.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EnvPrefix is the environment variable prefix for all overrides.
const EnvPrefix = "RESEARCHD_"

// Config is the root configuration for the researchd process.
type Config struct {
	HTTP     HTTP     `json:"http"`
	Auth     Auth     `json:"auth"`
	Database Database `json:"database"`
	Data     Data     `json:"data"`
	Worker   Worker   `json:"worker"`
	Log      Log      `json:"log"`
}

// HTTP configures the API listener.
type HTTP struct {
	// Addr is the listen address, e.g. "127.0.0.1:8080".
	Addr string `json:"addr"`
}

// Auth configures authentication mode.
type Auth struct {
	// Mode is "local" (development only, loopback bind enforced) or "bearer".
	Mode string `json:"mode"`
	// BearerTokenFile points to a file containing the accepted bearer token.
	// The file is read at startup; the token itself never enters the config.
	BearerTokenFile string `json:"bearer_token_file"`
}

// Database configures the metadata store (SQLite for M0).
type Database struct {
	// Path is the SQLite database file path. The parent directory is created
	// if missing. The file must stay on a local filesystem when WAL is used.
	Path string `json:"path"`
}

// Data configures the managed data root for files and artifacts.
type Data struct {
	Root string `json:"root"`
}

// Worker configures the in-process job worker.
type Worker struct {
	PollInterval Duration `json:"poll_interval"`
	LeaseTTL     Duration `json:"lease_ttl"`
	DrainTimeout Duration `json:"drain_timeout"`
}

// Log configures structured logging.
type Log struct {
	Level  string `json:"level"`  // debug|info|warn|error
	Format string `json:"format"` // json|text
}

// Duration is a JSON string duration like "30s" that unmarshals to time.Duration.
type Duration time.Duration

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"30s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the underlying time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Defaults returns the built-in default configuration before file and
// environment overrides are applied.
func Defaults() Config {
	return Config{
		HTTP: HTTP{Addr: "127.0.0.1:8080"},
		Auth: Auth{Mode: "local"},
		Database: Database{
			Path: filepath.Join("data", "metadata.db"),
		},
		Data: Data{Root: "data"},
		Worker: Worker{
			PollInterval: Duration(time.Second),
			LeaseTTL:     Duration(30 * time.Second),
			DrainTimeout: Duration(15 * time.Second),
		},
		Log: Log{Level: "info", Format: "json"},
	}
}

// source describes where a config value came from, for error messages.
type source struct {
	kind string // "defaults" | "file" | "environment"
	file string
	env  string
}

func (s source) String() string {
	switch s.kind {
	case "file":
		return "file " + s.file
	case "environment":
		return "environment " + s.env
	default:
		return "defaults"
	}
}

// Load builds the configuration from defaults, then applies the file at
// configPath (when non-empty) and environment overrides, then validates.
func Load(configPath string) (Config, error) {
	cfg := Defaults()
	applied := []string{"defaults"}

	if configPath != "" {
		src := source{kind: "file", file: configPath}
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return Config{}, fmt.Errorf("config: %s: %w", src, err)
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("config: %s: %w", src, err)
		}
		applied = append(applied, src.String())
	}

	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	applied = append(applied, "environment")
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w (values applied from: %s)", err, strings.Join(applied, ", "))
	}
	return cfg, nil
}

// envSpec maps environment variables to setter functions.
type envSpec struct {
	name   string
	apply  func(cfg *Config, value string) error
	target string // human readable field name for errors
}

func envSpecs() []envSpec {
	return []envSpec{
		{"HTTP_ADDR", func(c *Config, v string) error { c.HTTP.Addr = v; return nil }, "http.addr"},
		{"AUTH_MODE", func(c *Config, v string) error { c.Auth.Mode = v; return nil }, "auth.mode"},
		{"AUTH_BEARER_TOKEN_FILE", func(c *Config, v string) error { c.Auth.BearerTokenFile = v; return nil }, "auth.bearer_token_file"},
		{"DATABASE_PATH", func(c *Config, v string) error { c.Database.Path = v; return nil }, "database.path"},
		{"DATA_ROOT", func(c *Config, v string) error { c.Data.Root = v; return nil }, "data.root"},
		{"WORKER_POLL_INTERVAL", func(c *Config, v string) error { return setDuration(&c.Worker.PollInterval, v) }, "worker.poll_interval"},
		{"WORKER_LEASE_TTL", func(c *Config, v string) error { return setDuration(&c.Worker.LeaseTTL, v) }, "worker.lease_ttl"},
		{"WORKER_DRAIN_TIMEOUT", func(c *Config, v string) error { return setDuration(&c.Worker.DrainTimeout, v) }, "worker.drain_timeout"},
		{"LOG_LEVEL", func(c *Config, v string) error { c.Log.Level = v; return nil }, "log.level"},
		{"LOG_FORMAT", func(c *Config, v string) error { c.Log.Format = v; return nil }, "log.format"},
	}
}

func setDuration(d *Duration, v string) error {
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", v, err)
	}
	*d = Duration(parsed)
	return nil
}

func applyEnv(cfg *Config) error {
	for _, spec := range envSpecs() {
		name := EnvPrefix + spec.name
		value, ok := os.LookupEnv(name)
		if !ok || value == "" {
			continue
		}
		src := source{kind: "environment", env: name}
		if err := spec.apply(cfg, value); err != nil {
			return fmt.Errorf("config: field %s from %s: %w", spec.target, src, err)
		}
	}
	return nil
}

// Validate enforces cross-field invariants. Validation errors name the fields
// involved but never contain secret material.
func (c Config) Validate() error {
	if c.HTTP.Addr == "" {
		return fmt.Errorf("config: field http.addr (source: defaults): must not be empty")
	}
	switch c.Auth.Mode {
	case "local":
		if err := requireLoopback(c.HTTP.Addr); err != nil {
			return fmt.Errorf("config: field http.addr %q conflicts with auth.mode \"local\": %w", c.HTTP.Addr, err)
		}
	case "bearer":
		if c.Auth.BearerTokenFile == "" {
			return fmt.Errorf("config: field auth.bearer_token_file: required when auth.mode is \"bearer\"")
		}
	default:
		return fmt.Errorf("config: field auth.mode %q: unknown mode (allowed: local, bearer)", c.Auth.Mode)
	}
	if c.Database.Path == "" {
		return fmt.Errorf("config: field database.path (source: defaults): must not be empty")
	}
	if c.Data.Root == "" {
		return fmt.Errorf("config: field data.root (source: defaults): must not be empty")
	}
	if c.Worker.PollInterval.Std() <= 0 {
		return fmt.Errorf("config: field worker.poll_interval: must be positive")
	}
	if c.Worker.LeaseTTL.Std() <= 0 {
		return fmt.Errorf("config: field worker.lease_ttl: must be positive")
	}
	if c.Worker.DrainTimeout.Std() <= 0 {
		return fmt.Errorf("config: field worker.drain_timeout: must be positive")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: field log.level %q: unknown level (allowed: debug, info, warn, error)", c.Log.Level)
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		return fmt.Errorf("config: field log.format %q: unknown format (allowed: json, text)", c.Log.Format)
	}
	return nil
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("must bind a loopback address (e.g. 127.0.0.1), got %q", host)
}
