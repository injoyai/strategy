package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/config"
)

func TestDefaultsValidate(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("defaults must load and validate: %v", err)
	}
	if cfg.HTTP.Addr != "127.0.0.1:8080" {
		t.Fatalf("unexpected default addr: %s", cfg.HTTP.Addr)
	}
	if cfg.Worker.LeaseTTL.Std() != 30*time.Second {
		t.Fatalf("unexpected default lease ttl: %s", cfg.Worker.LeaseTTL.Std())
	}
}

func TestFileOverridesDefaultsAndRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.json")
	content := `{
		"http": {"addr": "127.0.0.1:9090"},
		"worker": {"lease_ttl": "45s"}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load file: %v", err)
	}
	if cfg.HTTP.Addr != "127.0.0.1:9090" {
		t.Fatalf("file override not applied: %s", cfg.HTTP.Addr)
	}
	if cfg.Worker.LeaseTTL.Std() != 45*time.Second {
		t.Fatalf("file duration override not applied: %s", cfg.Worker.LeaseTTL.Std())
	}
	if cfg.Auth.Mode != "local" {
		t.Fatalf("defaults must survive partial file: %s", cfg.Auth.Mode)
	}

	bad := filepath.Join(dir, "unknown.json")
	if err := os.WriteFile(bad, []byte(`{"nope": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = config.Load(bad)
	if err == nil || !strings.Contains(err.Error(), "file "+bad) {
		t.Fatalf("unknown field error must name file source, got: %v", err)
	}
}

func TestEnvironmentOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.json")
	if err := os.WriteFile(path, []byte(`{"http": {"addr": "127.0.0.1:9090"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RESEARCHD_HTTP_ADDR", "127.0.0.1:9191")
	t.Setenv("RESEARCHD_WORKER_DRAIN_TIMEOUT", "7s")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.HTTP.Addr != "127.0.0.1:9191" {
		t.Fatalf("env must win over file: %s", cfg.HTTP.Addr)
	}
	if cfg.Worker.DrainTimeout.Std() != 7*time.Second {
		t.Fatalf("env duration not applied: %s", cfg.Worker.DrainTimeout.Std())
	}
}

func TestErrorsNameFieldAndSource(t *testing.T) {
	t.Run("env duration invalid", func(t *testing.T) {
		t.Setenv("RESEARCHD_WORKER_LEASE_TTL", "soon")
		_, err := config.Load("")
		if err == nil {
			t.Fatal("expected error")
		}
		for _, want := range []string{"worker.lease_ttl", "RESEARCHD_WORKER_LEASE_TTL"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error must mention %q, got: %v", want, err)
			}
		}
	})
	t.Run("file field invalid", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "dev.json")
		if err := os.WriteFile(path, []byte(`{"auth": {"mode": "ldap"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := config.Load(path)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), `auth.mode "ldap"`) || !strings.Contains(err.Error(), path) {
			t.Fatalf("error must name field and applied sources, got: %v", err)
		}
	})
}

func TestLocalModeRequiresLoopback(t *testing.T) {
	t.Setenv("RESEARCHD_AUTH_MODE", "local")
	t.Setenv("RESEARCHD_HTTP_ADDR", "0.0.0.0:8080")
	_, err := config.Load("")
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("local mode must reject non-loopback bind, got: %v", err)
	}
}

func TestBearerModeRequiresTokenFile(t *testing.T) {
	t.Setenv("RESEARCHD_AUTH_MODE", "bearer")
	_, err := config.Load("")
	if err == nil || !strings.Contains(err.Error(), "auth.bearer_token_file") {
		t.Fatalf("bearer mode must require token file, got: %v", err)
	}
}
