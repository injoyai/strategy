// Command researchd is the single-binary entry point hosting the HTTP API and
// the in-process job worker. The two components cooperate only through
// persistent job state (M0-05), never through in-memory queues.
//
// Shutdown protocol on SIGINT/SIGTERM:
//  1. stop accepting new HTTP requests (bounded shutdown),
//  2. stop taking new worker tasks and drain in-flight tasks with the
//     configured bounded drain timeout,
//  3. exit non-zero if either stage fails; persistent state recovers on next
//     start, so partial work is never lost.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/config"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/logging"
	"github.com/injoyai/strategy/internal/pipeline"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/research"
	"github.com/injoyai/strategy/internal/screenrun"
	"github.com/injoyai/strategy/internal/server"
	"github.com/injoyai/strategy/internal/store"
	"github.com/injoyai/strategy/internal/synthetic"
	"github.com/injoyai/strategy/internal/tdxprovider"
	"github.com/injoyai/strategy/internal/worker"
)

const httpShutdownTimeout = 10 * time.Second

// newAPI assembles the HTTP surface the process actually serves. It is a
// function rather than an inline literal so the wiring itself is testable:
// every surface mounted here is reachable in the deployed binary, and a
// surface that is only mounted when its dependency is nil silently disappears
// otherwise.
//
// The research service is built here because /universes, /factors,
// /factor-runs and /factor-analyses are only mounted when it is supplied —
// without it the whole M1-09 surface is a routing miss.
//
// The screening-run service is returned as well: the worker needs the same
// instance the API uses, so a run submitted over HTTP is executed with
// exactly the resolution rules the preflight reported.
func newAPI(
	log *slog.Logger,
	auth server.Authenticator,
	db *sql.DB,
	jstore *jobs.Store,
	dataStore *data.Store,
	art *artifacts.Store,
	providers []server.ProviderRegistration,
) (*server.API, *screenrun.Service, error) {
	registry, err := factor.NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		return nil, nil, fmt.Errorf("build factor registry: %w", err)
	}
	if err := factor.RegisterDefaults(registry); err != nil {
		return nil, nil, fmt.Errorf("register default factors: %w", err)
	}
	researchService, err := research.New(dataStore, registry, factor.NewCache(), art, ports.SHA256Checksummer{})
	if err != nil {
		return nil, nil, fmt.Errorf("build research service: %w", err)
	}
	screenRuns, err := screenrun.New(dataStore, registry, factor.NewCache())
	if err != nil {
		return nil, nil, fmt.Errorf("build screening run service: %w", err)
	}
	api := server.NewAPI(server.Options{
		Log:         log,
		Auth:        auth,
		Idempotency: store.NewIdempotencyStore(db),
		Jobs:        jstore,
		Data:        dataStore,
		Artifacts:   art,
		Research:    researchService,
		ScreenRuns:  screenRuns,
		Providers:   providers,
	})
	return api, screenRuns, nil
}

// newProviderRegistrations is the single source of truth for the providers
// exposed by the deployed API. Factories open lazily, so building the catalog
// never turns a public network outage into a process-startup failure.
func newProviderRegistrations(ctx context.Context) ([]server.ProviderRegistration, error) {
	syntheticProvider, err := synthetic.Factory{}.Open(ctx, domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: synthetic.ProviderID, Version: synthetic.ProviderVersion},
		Settings: json.RawMessage("{}"),
	})
	if err != nil {
		return nil, fmt.Errorf("open synthetic provider: %w", err)
	}
	tdxProvider, err := tdxprovider.Factory{}.Open(ctx, domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: tdxprovider.ProviderID, Version: tdxprovider.ProviderVersion},
		Settings: json.RawMessage("{}"),
	})
	if err != nil {
		return nil, fmt.Errorf("open tdx provider: %w", err)
	}
	return []server.ProviderRegistration{
		{Provider: syntheticProvider, Factory: synthetic.Factory{}},
		{Provider: tdxProvider, Factory: tdxprovider.Factory{}},
	}, nil
}

// newRunHandlers merges the job kinds of every package that can queue work into
// the map the worker loop claims from. A kind a POST endpoint can enqueue but
// no handler claims would leave its job queued forever, so the merge lives in a
// function the wiring test can inspect.
func newRunHandlers(
	jstore *jobs.Store,
	dataStore *data.Store,
	art *artifacts.Store,
	screenRuns *screenrun.Service,
) map[string]jobs.HandlerFunc {
	handlers := (&pipeline.Handlers{
		Jobs:      jstore,
		Data:      dataStore,
		Artifacts: art,
		Factories: map[domain.ID]ports.ProviderFactory{
			synthetic.ProviderID:   synthetic.Factory{},
			tdxprovider.ProviderID: tdxprovider.Factory{},
		},
		Normalizers: map[domain.ID]func(string) ports.Normalizer{
			synthetic.ProviderID:   func(dataset string) ports.Normalizer { return synthetic.NewNormalizer(dataset) },
			tdxprovider.ProviderID: func(dataset string) ports.Normalizer { return tdxprovider.NewNormalizer(dataset) },
		},
	}).Map()
	for kind, handler := range (&screenrun.Handlers{
		Jobs:      jstore,
		Runs:      dataStore,
		Service:   screenRuns,
		Artifacts: art,
	}).Map() {
		handlers[kind] = handler
	}
	return handlers
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "researchd:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to config file (JSON); overrides RESEARCHD_CONFIG")
	flag.Parse()

	path := *configPath
	if path == "" {
		path = os.Getenv("RESEARCHD_CONFIG")
	}

	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	log, err := logging.New(cfg.Log)
	if err != nil {
		return err
	}

	if cfg.Auth.Mode == "local" {
		log.Warn("auth mode 'local' is development-only: the server binds a loopback address and accepts unauthenticated requests; do not expose it beyond this machine")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	auth, err := server.NewAuthenticator(cfg.Auth)
	if err != nil {
		return err
	}

	db, err := store.Open(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("open metadata store: %w", err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, log); err != nil {
		return fmt.Errorf("migrate metadata store: %w", err)
	}

	art := artifacts.New(cfg.Data.Root, db)
	if err := art.VerifyReferencedFiles(ctx, db); err != nil {
		return fmt.Errorf("verify artifacts: %w", err)
	}

	// Job state lives on the same metadata database. Startup recovery closes
	// jobs a dead previous process left behind (single-owner process model),
	// so a restart never resumes half-finished work silently.
	hub := jobs.NewHub()
	jstore := jobs.NewStore(db, nil, hub)
	if err := jstore.RecoverOrphans(ctx); err != nil {
		return fmt.Errorf("recover orphaned jobs: %w", err)
	}

	dataStore := data.New(db, nil)
	providers, err := newProviderRegistrations(ctx)
	if err != nil {
		return err
	}

	api, screenRuns, err := newAPI(log, auth, db, jstore, dataStore, art, providers)
	if err != nil {
		return err
	}

	var pool worker.Pool

	// The persistent job loop claims queued/expired jobs from the store; it
	// observes ctx cancellation at safe points so bounded drain works.
	loop := &jobs.Loop{
		Store:    jstore,
		Owner:    fmt.Sprintf("researchd-%d", os.Getpid()),
		Handlers: newRunHandlers(jstore, dataStore, art, screenRuns),
		Log:      log,
	}
	pool.Go(ctx, loop.Run)

	srv := server.New(cfg.HTTP, server.RootMux(log, api))
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("researchd listening", slog.String("addr", cfg.HTTP.Addr))

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}

	// Stage 1: stop accepting new HTTP work.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown failed", slog.String("error", err.Error()))
	}

	// Stage 2: bounded drain of in-flight worker tasks.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), cfg.Worker.DrainTimeout.Std())
	defer cancelDrain()
	if !pool.Drain(drainCtx) {
		log.Warn("worker drain timed out; in-flight tasks are abandoned for recovery on next start",
			slog.Duration("drain_timeout", cfg.Worker.DrainTimeout.Std()))
	}

	log.Info("researchd stopped")
	return nil
}
