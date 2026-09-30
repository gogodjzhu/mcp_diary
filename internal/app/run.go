package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gogodjzhu/mcp-diary/internal/config"
)

// Run starts the server over the configured transport and blocks until the
// context is cancelled or the transport fails.
func (a *App) Run(ctx context.Context) error {
	a.logger.Info("starting server",
		"name", a.cfg.Name,
		"version", a.cfg.Version,
		"transport", a.cfg.Transport,
		"tools", len(a.registry.Tools()),
		"web_enabled", a.web != nil,
	)

	switch a.cfg.Transport {
	case config.TransportStreamableHTTP:
		return a.runHTTP(ctx)
	case config.TransportStdio:
		return a.mcp.ServeStdio(ctx)
	default:
		return fmt.Errorf("unsupported transport %q", a.cfg.Transport)
	}
}

// runHTTP serves the assembled handler over HTTP with graceful shutdown.
func (a *App) runHTTP(ctx context.Context) error {
	srv := &http.Server{
		Addr:              a.cfg.Addr,
		Handler:           a.HTTPHandler(),
		ReadHeaderTimeout: a.cfg.ReadHeaderTimeout,
		ReadTimeout:       a.cfg.ReadTimeout,
		WriteTimeout:      a.cfg.WriteTimeout,
		IdleTimeout:       a.cfg.IdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("HTTP server listening",
			"addr", a.cfg.Addr,
			"endpoint", a.cfg.EndpointPath,
			"health", "/healthz",
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		a.logger.Info("shutting down HTTP server", "timeout", a.cfg.ShutdownTimeout)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		return nil
	}
}
