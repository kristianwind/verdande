// Command verdande is the whole application: API, WebSocket, background jobs and
// the web interface, in one binary with SQLite underneath. There is no second
// process to run and no database to provision.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kristianwind/verdande/internal/config"
	"github.com/kristianwind/verdande/internal/httpapi"
	"github.com/kristianwind/verdande/internal/jobs"
	"github.com/kristianwind/verdande/internal/store"
)

// version is stamped at build time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "verdande: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := httpapi.NewLogger(cfg.Dev)
	log.Info("starting verdande", "version", version, "data_dir", cfg.DataDir, "base_url", cfg.BaseURL)

	// The data volume is the only state that matters; create the whole layout up
	// front so a failure here is a startup error rather than a surprise at the
	// first upload or the first backup at three in the morning.
	for _, dir := range []string{cfg.DataDir, cfg.FilesDir(), cfg.BackupsDir()} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	log.Info("database ready", "path", db.Path())

	api := httpapi.New(cfg, db, log, webAssets(log))

	// Background work: reminders, the nightly backup, and the trash emptying
	// itself. Started before the listener so a reminder that came due while the
	// process was down goes out immediately rather than on the next tick.
	jobCtx, stopJobs := context.WithCancel(context.Background())
	defer stopJobs()
	runner := jobs.New(cfg, db, log, api.Mail(), api.Hub())
	runner.Start(jobCtx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: it would also cap the WebSocket and any long file
		// download. Per-handler timeouts do that job with the right granularity.
		IdleTimeout: 120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	// SIGTERM is what Docker — and so the Yggdrasil panel — sends on stop. Draining
	// in-flight requests before exiting is what keeps a restart from showing up as
	// a handful of failed saves.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errc:
		return fmt.Errorf("listen: %w", err)
	case sig := <-stop:
		log.Info("shutting down", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	// Let the background jobs finish the pass they are in. A shutdown during a
	// backup would otherwise leave a partial file that looks like a backup.
	stopJobs()
	runner.Wait()

	// WAL mode leaves recent writes in the -wal file. Checkpointing on the way out
	// means a backup taken of a stopped container is complete.
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Warn("wal checkpoint on shutdown", "err", err)
	}
	log.Info("stopped")
	return nil
}

// webAssets returns the built frontend if it was compiled into this binary.
//
// The web build is embedded through a build tag so that `go build ./...` works on a
// clean checkout with no Node toolchain: without the tag the app serves the API
// alone and says so, rather than failing to compile because web/build is missing.
func webAssets(log logger) fs.FS {
	assets, err := embeddedWeb()
	if err != nil {
		log.Warn("no web assets in this build; serving the API only", "err", err)
		return nil
	}
	return assets
}

type logger interface {
	Warn(msg string, args ...any)
}
