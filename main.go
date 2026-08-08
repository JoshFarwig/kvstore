package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JoshFarwig/kvstore/server"
	"github.com/JoshFarwig/kvstore/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		slog.Error("config could not load", "err", err)
		os.Exit(1)
	}

	s := store.NewStore()

	if err := seedThreshold(s, cfg); err != nil {
		slog.Error("threshold seed failed", "err", err)
		os.Exit(1)
	}

	server.StartHeartbeat(ctx, s, cfg.NodeID, 3*time.Second)

	srv := newHTTPServer(cfg, server.NewServer(s))

	fmt.Println("Ἀεὶ ὁ θεὸς ὁ μέγας γεωμετρεῖ τὸ σύμπαν...")
	if err := runServer(ctx, srv); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func seedThreshold(s *store.Store, cfg Config) error {
	if cfg.CPUThresholdPct == -1 || cfg.MemThresholdPct == -1 {
		return nil
	}
	return server.SetThrottleThreshold(s, cfg.NodeID, server.ThrottleThreshold{
		CPUPctCap: cfg.CPUThresholdPct,
		MemPctCap: cfg.MemThresholdPct,
	})
}

func newHTTPServer(cfg Config, handler http.Handler) *http.Server {
	return &http.Server{Addr: net.JoinHostPort(cfg.Host, cfg.Port), Handler: handler}
}

// runServer starts srv and blocks until ctx is cancelled, then shuts it down gracefully.
func runServer(ctx context.Context, srv *http.Server) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("http server shutdown failed", "err", err)
			os.Exit(1)
		}
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
