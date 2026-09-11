// Authentication port for the HTTP shell.
//
// The deployment mode is never assumed: bearer authentication is always
// available, and the "local" development mode merely accepts unauthenticated
// requests after config validation has already pinned the listener to a
// loopback address. There is no constructor that silently disables auth.
package server

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/injoyai/strategy/internal/config"
)

// Caller is the authenticated identity of a request. M0 has a single caller
// per mode; the ID scopes idempotency keys and will carry workspace
// authorization once multi-workspace deployment is decided.
type Caller struct {
	ID string
}

// Authenticator validates a request and returns its caller. Implementations
// must not log tokens or embed them in returned errors.
type Authenticator interface {
	Authenticate(r *http.Request) (Caller, error)
}

// LocalAuth accepts every request as caller "local". Config validation
// guarantees a loopback bind in this mode (see config.Config.Validate).
type LocalAuth struct{}

// Authenticate implements Authenticator.
func (LocalAuth) Authenticate(*http.Request) (Caller, error) {
	return Caller{ID: "local"}, nil
}

// BearerAuth accepts the single configured token via the Authorization
// header. Comparison is constant time.
type BearerAuth struct {
	token []byte
}

// NewBearerAuth builds an authenticator for one token.
func NewBearerAuth(token string) BearerAuth {
	return BearerAuth{token: []byte(token)}
}

// Authenticate implements Authenticator.
func (b BearerAuth) Authenticate(r *http.Request) (Caller, error) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return Caller{}, errors.New("missing bearer credentials")
	}
	if subtle.ConstantTimeCompare([]byte(header[len(prefix):]), b.token) != 1 {
		return Caller{}, errors.New("invalid bearer credentials")
	}
	return Caller{ID: "bearer"}, nil
}

// NewAuthenticator builds the authenticator required by auth.mode. In bearer
// mode the token is read from the configured file at startup; the file must
// hold exactly one token and nothing else of value.
func NewAuthenticator(cfg config.Auth) (Authenticator, error) {
	switch cfg.Mode {
	case "local":
		return LocalAuth{}, nil
	case "bearer":
		raw, err := os.ReadFile(cfg.BearerTokenFile)
		if err != nil {
			return nil, fmt.Errorf("auth: read bearer token file: %w", err)
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return nil, errors.New("auth: bearer token file is empty")
		}
		if len(token) < 16 {
			return nil, errors.New("auth: bearer token is shorter than 16 characters")
		}
		return NewBearerAuth(token), nil
	default:
		// config.Validate already rejects unknown modes; this guard keeps the
		// security boundary closed even if validation changes.
		return nil, fmt.Errorf("auth: unknown mode %q", cfg.Mode)
	}
}
