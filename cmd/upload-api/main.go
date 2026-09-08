package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"upload-api/internal/config"
	"upload-api/internal/httpapi"
	"upload-api/internal/storage"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	store, err := storage.New(cfg.StorageDir)
	if err != nil {
		logger.Error("initialize storage", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := store.CleanupExpired(time.Now(), cfg.TempFileTTL); err != nil {
		logger.Error("startup temporary file cleanup failed", "error", err)
	}
	go store.RunCleanup(ctx, cfg.TempFileTTL, time.Hour, logger)

	api := httpapi.New(cfg, store, logger)
	server := &http.Server{
		Addr: cfg.ListenAddr, Handler: api.Handler(),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("upload API starting", "listen_addr", cfg.ListenAddr)
		errCh <- server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}
}
