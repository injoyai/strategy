// API assembles the /api/v1 contract surface: one middleware chain shared
// by every route plus per-route options (idempotency enforcement).
package server

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/research"
)

// apiPrefix is the mount point of every route registered through Handle.
const apiPrefix = "/api/v1"

// Options configures an API instance. Zero values select safe defaults so
// no route can accidentally run without auth, request IDs or a body limit.
type Options struct {
	Log          *slog.Logger
	Auth         Authenticator // required; NewAPI panics without one
	Clock        ports.Clock
	IDs          ports.IDGenerator
	Idempotency  IdempotencyStore
	MaxBodyBytes int64 // 0 -> defaultMaxBodyBytes
	// Jobs, when set, mounts the /jobs contract surface on the API.
	Jobs *jobs.Store

	Data      *data.Store
	Providers []ProviderRegistration

	// Artifacts, when set, mounts the /imports and /artifacts surface.
	Artifacts *artifacts.Store

	// Research, when set, mounts the M1-09 research surface: universes,
	// the factor catalog, synchronous factor runs and factor analyses.
	Research *research.Service
}

// API is the contract surface served under /api/v1.
type API struct {
	log          *slog.Logger
	auth         Authenticator
	clock        ports.Clock
	ids          ports.IDGenerator
	idem         IdempotencyStore
	maxBodyBytes int64
	jobs         *jobs.Store
	mux          *http.ServeMux

	data      *data.Store
	providers []domain.ProviderDescriptor
	factories map[domain.ID]ports.ProviderFactory

	artifacts *artifacts.Store
	research  *research.Service
}

// NewAPI builds the API with defaults for omitted dependencies.
func NewAPI(opts Options) *API {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Auth == nil {
		// Fail fast: an unauthenticated API must never be assembled by
		// accident (M0-03 contract rule).
		panic("server: NewAPI requires an Authenticator (use LocalAuth for development)")
	}
	if opts.Clock == nil {
		opts.Clock = ports.SystemClock{}
	}
	if opts.IDs == nil {
		opts.IDs = ports.RandomIDGenerator{Prefix: "req"}
	}
	if opts.Idempotency == nil {
		opts.Idempotency = NewMemoryIdempotencyStore()
	}
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultMaxBodyBytes
	}
	providers, factories := describeProviders(opts.Providers)
	api := &API{
		log:          opts.Log,
		auth:         opts.Auth,
		clock:        opts.Clock,
		ids:          opts.IDs,
		idem:         opts.Idempotency,
		maxBodyBytes: maxBody,
		jobs:         opts.Jobs,
		data:         opts.Data,
		artifacts:    opts.Artifacts,
		research:     opts.Research,
		providers:    providers,
		factories:    factories,
		mux:          http.NewServeMux(),
	}
	if opts.Jobs != nil {
		api.RegisterJobs()
	}
	if opts.Data != nil {
		api.RegisterData()
		// Screener versions persist through the same store, so the surface
		// travels with it rather than with the research orchestration.
		api.RegisterScreeners()
	}
	if opts.Artifacts != nil {
		api.RegisterImports()
	}
	if opts.Research != nil {
		api.RegisterUniverses()
		api.RegisterFactors()
	}
	return api
}

// RouteOptions carries per-route contract obligations.
type RouteOptions struct {
	// IdempotencyRequired mirrors the OpenAPI Idempotency-Key header
	// parameter: true for POST commands, false for reads, queries and
	// preflight/resolve previews.
	IdempotencyRequired bool
}

// Handle registers a route under /api/v1. Patterns are given relative to the
// API root ("/things") and are mounted with the /api/v1 prefix, matching the
// server base URL in the OpenAPI document.
func (a *API) Handle(method, pattern string, handler http.HandlerFunc, opts RouteOptions) {
	if !strings.HasPrefix(pattern, apiPrefix+"/") {
		pattern = apiPrefix + pattern
	}
	route := method + " " + pattern
	if opts.IdempotencyRequired {
		a.mux.Handle(route, a.idempotencyMiddleware(handler))
		return
	}
	a.mux.Handle(route, handler)
}

// Handler returns the fully wrapped API handler. Unmatched paths answer the
// contract envelope (404 resource.not_found) so every error under /api/v1
// parses as the same Error schema, including routing misses.
func (a *API) Handler() http.Handler {
	routed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := a.mux.Handler(r); pattern == "" {
			a.writeError(w, r, domain.NewError(domain.CodeResourceNotFound, "no such resource"))
			return
		}
		a.mux.ServeHTTP(w, r)
	})
	return a.requestIDMiddleware(a.recoverMiddleware(a.accessLogMiddleware(a.bodyLimitMiddleware(a.authMiddleware(routed)))))
}

// RootMux wires health endpoints (unauthenticated, process-local) and the
// contract API. Unknown paths answer 404 with the M0 shell envelope.
func RootMux(log *slog.Logger, api *API) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeHealthMethod(w)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeHealthMethod(w)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.Handle("/api/v1", api.Handler())
	mux.Handle("/api/v1/", api.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.DebugContext(r.Context(), "unmatched route",
			slog.String("method", r.Method), slog.String("path", r.URL.Path))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}` + "\n"))
	})
	return mux
}

func writeHealthMethod(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusMethodNotAllowed)
	_, _ = w.Write([]byte(`{"error":"method not allowed"}` + "\n"))
}
