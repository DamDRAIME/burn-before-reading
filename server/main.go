package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // Import tzdata to ensure time zone data is available in the binary

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"bbr/internal/database"
	"bbr/internal/messages"
	"bbr/internal/printers"
	"bbr/internal/web"
)

const (
	// shutdownTimeout bounds how long in-flight requests get to finish on SIGINT/SIGTERM.
	shutdownTimeout = 15 * time.Second

	// claimTimeout is how long a message may stay claimed before it is returned to the queue.
	claimTimeout = 2 * time.Minute

	// claimReaperInterval is how often stale claims are checked for.
	claimReaperInterval = 10 * time.Minute
)

func main() {
	// Initialize logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("Application error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL environment variable is not set")
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		return errors.New("ADMIN_TOKEN environment variable is not set")
	}

	// Initialize database connection pool
	pool, err := database.NewPostgresPool(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Initialize message store and service
	printerStore := database.NewPrinterStore(pool)
	printerService := printers.NewService(printerStore)
	printerHandler := web.NewPrintersHandler(printerService, logger)
	msgStore := database.NewMessageStore(pool)
	msgService := messages.NewService(msgStore, printerService)
	msgHandler := web.NewMessagesHandler(msgService, logger)

	// Set up router
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))

	// Register routes
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "Database connection error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	r.Mount("/api/v1", web.NewAPIRouter(msgHandler, printerHandler, adminToken))

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Background jobs: stopped by ctx and waited for during shutdown, before the pool is closed.
	var background sync.WaitGroup
	background.Go(func() {
		msgService.RunClaimReaper(ctx, claimTimeout, claimReaperInterval, logger)
	})

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", addr)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		// The server failed on its own (e.g. port already in use): stop background jobs and bail out.
		stop()
		background.Wait()
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	// Restore default signal handling so a second Ctrl-C kills the process immediately.
	stop()
	logger.Info("shutting down")

	// Shutdown blocks until in-flight requests finish (or the timeout expires),
	// so the deferred pool.Close() only runs once nothing is using the pool any more.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	background.Wait()
	if shutdownErr != nil {
		return fmt.Errorf("shutdown: %w", shutdownErr)
	}

	logger.Info("shutdown complete")
	return nil
}
