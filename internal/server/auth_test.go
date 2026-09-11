package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/injoyai/strategy/internal/config"
)

func TestLocalAuthAccepts(t *testing.T) {
	caller, err := LocalAuth{}.Authenticate(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caller.ID != "local" {
		t.Errorf("caller.ID = %q, want %q", caller.ID, "local")
	}
}

func TestBearerAuth(t *testing.T) {
	const token = "unit-test-token-0123456789"
	auth := NewBearerAuth(token)

	cases := []struct {
		name    string
		header  string
		wantErr bool
	}{
		{"correct token", "Bearer " + token, false},
		{"case-insensitive scheme", "bearer " + token, false},
		{"wrong token", "Bearer wrong-token-xxxxxxxx", true},
		{"empty credentials", "Bearer ", true},
		{"missing header", "", true},
		{"wrong scheme", "Basic " + token, true},
		{"token only", token, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptestRequest(t, tc.header)
			caller, err := auth.Authenticate(req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for header %q", tc.header)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if caller.ID != "bearer" {
				t.Errorf("caller.ID = %q, want %q", caller.ID, "bearer")
			}
		})
	}
}

func TestNewAuthenticator(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("bearer-token-with-padding-1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyFile := filepath.Join(dir, "empty")
	if err := os.WriteFile(emptyFile, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shortFile := filepath.Join(dir, "short")
	if err := os.WriteFile(shortFile, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("local mode", func(t *testing.T) {
		a, err := NewAuthenticator(config.Auth{Mode: "local"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := a.(LocalAuth); !ok {
			t.Errorf("want LocalAuth, got %T", a)
		}
	})

	t.Run("bearer mode reads trimmed token", func(t *testing.T) {
		a, err := NewAuthenticator(config.Auth{Mode: "bearer", BearerTokenFile: tokenFile})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		req := httptestRequest(t, "Bearer bearer-token-with-padding-1234")
		if _, err := a.Authenticate(req); err != nil {
			t.Errorf("expected authentication to succeed: %v", err)
		}
	})

	t.Run("empty token file rejected", func(t *testing.T) {
		if _, err := NewAuthenticator(config.Auth{Mode: "bearer", BearerTokenFile: emptyFile}); err == nil {
			t.Error("expected error for empty token file")
		}
	})

	t.Run("short token rejected", func(t *testing.T) {
		if _, err := NewAuthenticator(config.Auth{Mode: "bearer", BearerTokenFile: shortFile}); err == nil {
			t.Error("expected error for token shorter than 16 characters")
		}
	})

	t.Run("missing token file rejected", func(t *testing.T) {
		if _, err := NewAuthenticator(config.Auth{Mode: "bearer", BearerTokenFile: filepath.Join(dir, "nope")}); err == nil {
			t.Error("expected error for missing token file")
		}
	})

	t.Run("unknown mode rejected", func(t *testing.T) {
		if _, err := NewAuthenticator(config.Auth{Mode: "none"}); err == nil {
			t.Error("expected error for unknown auth mode; there must be no way to disable auth")
		}
	})
}

func httptestRequest(t *testing.T, authorization string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "/api/v1/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	return req
}
