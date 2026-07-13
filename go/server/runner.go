package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// GRPCServer abstracts product-specific gRPC implementation.
type GRPCServer interface {
	Start() error
	Stop()
}

// GracefulGRPCServer optionally supports context-aware graceful shutdown.
type GracefulGRPCServer interface {
	GRPCServer
	GracefulStop(context.Context) error
}

// StartupHook allows apps to initialize shared runtime resources before serving.
type StartupHook func(context.Context) error

// ShutdownHook allows apps to close domain resources during shutdown.
type ShutdownHook func(context.Context) error

// Options define shared server runtime behavior.
type Options struct {
	HTTPServer       *http.Server
	GRPCServer       GRPCServer
	EnableGRPC       bool
	ShutdownTimeout  time.Duration
	StartupHooks     []StartupHook
	PreShutdownHooks []ShutdownHook
	ShutdownHooks    []ShutdownHook
}

// Runner starts/stops HTTP and optional gRPC with signal-aware graceful shutdown.
type Runner struct {
	opts Options
}

// NewRunner creates a new shared runtime runner.
func NewRunner(opts Options) (*Runner, error) {
	if opts.HTTPServer == nil {
		return nil, fmt.Errorf("http server is required")
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 30 * time.Second
	}
	return &Runner{opts: opts}, nil
}

// RunBlocking runs until signal, context cancellation, or server error.
func (r *Runner) RunBlocking(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	for _, hook := range r.opts.StartupHooks {
		if hook == nil {
			continue
		}
		if err := hook(ctx); err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), r.opts.ShutdownTimeout)
			defer cancel()
			if shutdownErr := r.Shutdown(shutdownCtx); shutdownErr != nil {
				return errors.Join(err, shutdownErr)
			}
			return err
		}
	}

	errCh := make(chan error, 2)

	go func() {
		slog.Info("starting HTTP server", slog.String("address", r.opts.HTTPServer.Addr))
		err := r.opts.HTTPServer.ListenAndServe()
		errCh <- err
	}()

	if r.opts.EnableGRPC && r.opts.GRPCServer != nil {
		go func() {
			slog.Info("starting gRPC server")
			errCh <- r.opts.GRPCServer.Start()
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var runErr error
	select {
	case <-ctx.Done():
		runErr = ctx.Err()
		slog.Info("context cancellation received", slog.String("error", runErr.Error()))
	case sig := <-sigCh:
		slog.Info("shutdown signal received", slog.String("signal", sig.String()))
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			runErr = err
			slog.Error("server error", slog.String("error", err.Error()))
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), r.opts.ShutdownTimeout)
	defer cancel()
	shutdownErr := r.Shutdown(shutdownCtx)
	return mergeRunAndShutdownError(runErr, shutdownErr)
}

// Shutdown gracefully stops HTTP, gRPC and product-defined hooks.
func (r *Runner) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, hook := range r.opts.PreShutdownHooks {
		if hook == nil {
			continue
		}
		if err := hook(ctx); err != nil {
			slog.Warn("pre-shutdown hook error", slog.String("error", err.Error()))
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	if err := r.opts.HTTPServer.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
		slog.Warn("HTTP shutdown error", slog.String("error", err.Error()))
		if firstErr == nil {
			firstErr = err
		}
	}

	if r.opts.EnableGRPC && r.opts.GRPCServer != nil {
		if graceful, ok := r.opts.GRPCServer.(GracefulGRPCServer); ok {
			if err := graceful.GracefulStop(ctx); err != nil {
				slog.Warn("gRPC graceful shutdown error", slog.String("error", err.Error()))
				if firstErr == nil {
					firstErr = err
				}
				r.opts.GRPCServer.Stop()
			}
		} else {
			r.opts.GRPCServer.Stop()
		}
	}

	for _, hook := range r.opts.ShutdownHooks {
		if hook == nil {
			continue
		}
		if err := hook(ctx); err != nil {
			slog.Warn("shutdown hook error", slog.String("error", err.Error()))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func mergeRunAndShutdownError(runErr, shutdownErr error) error {
	// Treat caller cancellation as graceful stop unless cleanup itself failed.
	if errors.Is(runErr, context.Canceled) {
		return shutdownErr
	}
	if runErr == nil {
		return shutdownErr
	}
	if shutdownErr == nil {
		return runErr
	}
	return errors.Join(runErr, shutdownErr)
}
