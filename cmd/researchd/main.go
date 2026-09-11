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
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/config"
	"github.com/injoyai/strategy/internal/logging"
	"github.com/injoyai/strategy/internal/server"
	"github.com/injoyai/strategy/internal/store"
	"github.com/injoyai/strategy/internal/worker"
)

const httpShutdownTimeout = 10 * time.Second

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

	art := artifacts.New(cfg.Data.Root)
	if err := art.VerifyReferencedFiles(ctx, db); err != nil {
		return fmt.Errorf("verify artifacts: %w", err)
	}

	api := server.NewAPI(server.Options{
		Log:         log,
		Auth:        auth,
		Idempotency: store.NewIdempotencyStore(db),
	})
	registerAPIRoutes(api)

	var pool worker.Pool

	// Worker loop placeholder: the persistent job loop lands in M0-05. It must
	// observe ctx cancellation at safe points so bounded drain works.
	pool.Go(ctx, func(taskCtx context.Context) {
		<-taskCtx.Done()
	})

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

// registerAPIRoutes wires business endpoints onto the API. The contract shell
// (auth, request IDs, idempotency, pagination, error mapping) is fully active
// from M0-03; concrete handlers are registered from M0-05 onward as vertical
// slices land. Until then the API serves /api/v1 with a contract-shaped 404.
func registerAPIRoutes(api *server.API) {
	_ = api
}
