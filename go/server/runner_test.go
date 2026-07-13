package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunBlockingReturnsShutdownErrorOnContextCancel(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("shutdown hook failed")
	runner, err := NewRunner(Options{
		HTTPServer: &http.Server{
			Addr: "127.0.0.1:0",
		},
		ShutdownTimeout: 2 * time.Second,
		ShutdownHooks: []ShutdownHook{
			func(context.Context) error {
				return sentinel
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	runErr := runner.RunBlocking(ctx)
	if !errors.Is(runErr, sentinel) {
		t.Fatalf("RunBlocking() error = %v, want %v", runErr, sentinel)
	}
}

func TestRunBlockingReturnsNilOnCleanContextCancel(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(Options{
		HTTPServer: &http.Server{
			Addr: "127.0.0.1:0",
		},
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	runErr := runner.RunBlocking(ctx)
	if runErr != nil {
		t.Fatalf("RunBlocking() error = %v, want nil", runErr)
	}
}

func TestMergeRunAndShutdownError(t *testing.T) {
	t.Parallel()

	runErr := errors.New("run failed")
	shutdownErr := errors.New("shutdown failed")

	tests := []struct {
		name           string
		runErr         error
		shutdownErr    error
		expectNil      bool
		expectRunErr   bool
		expectShutdown bool
	}{
		{
			name:      "clean cancel returns nil",
			runErr:    context.Canceled,
			expectNil: true,
		},
		{
			name:           "cancel with shutdown failure returns shutdown error",
			runErr:         context.Canceled,
			shutdownErr:    shutdownErr,
			expectShutdown: true,
		},
		{
			name:           "wrapped cancel with shutdown failure returns shutdown error",
			runErr:         fmt.Errorf("wrapped: %w", context.Canceled),
			shutdownErr:    shutdownErr,
			expectShutdown: true,
		},
		{
			name:         "run error only",
			runErr:       runErr,
			expectRunErr: true,
		},
		{
			name:           "shutdown error only",
			shutdownErr:    shutdownErr,
			expectShutdown: true,
		},
		{
			name:           "run and shutdown errors are joined",
			runErr:         runErr,
			shutdownErr:    shutdownErr,
			expectRunErr:   true,
			expectShutdown: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := mergeRunAndShutdownError(tt.runErr, tt.shutdownErr)
			if tt.expectNil {
				if err != nil {
					t.Fatalf("mergeRunAndShutdownError() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("mergeRunAndShutdownError() = nil, want error")
			}
			if tt.expectRunErr && !errors.Is(err, runErr) {
				t.Fatalf("mergeRunAndShutdownError() = %v, want run error", err)
			}
			if tt.expectShutdown && !errors.Is(err, shutdownErr) {
				t.Fatalf("mergeRunAndShutdownError() = %v, want shutdown error", err)
			}
		})
	}
}

func TestRunBlockingExecutesStartupHooksBeforeServing(t *testing.T) {
	t.Parallel()

	var startupRan atomic.Bool
	runner, err := NewRunner(Options{
		HTTPServer: &http.Server{Addr: "127.0.0.1:0"},
		StartupHooks: []StartupHook{
			func(context.Context) error {
				startupRan.Store(true)
				return nil
			},
		},
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	if err := runner.RunBlocking(ctx); err != nil {
		t.Fatalf("RunBlocking() error = %v", err)
	}
	if !startupRan.Load() {
		t.Fatalf("expected startup hook to run")
	}
}

func TestRunBlockingRunsShutdownHooksWhenStartupFails(t *testing.T) {
	t.Parallel()

	startupErr := errors.New("startup failed")
	shutdownErr := errors.New("shutdown failed")
	var cleanupRan atomic.Bool

	runner, err := NewRunner(Options{
		HTTPServer: &http.Server{Addr: "127.0.0.1:0"},
		StartupHooks: []StartupHook{
			func(context.Context) error { return nil },
			func(context.Context) error { return startupErr },
		},
		ShutdownHooks: []ShutdownHook{
			func(context.Context) error {
				cleanupRan.Store(true)
				return shutdownErr
			},
		},
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	err = runner.RunBlocking(context.Background())
	if !cleanupRan.Load() {
		t.Fatalf("expected shutdown hook to run after startup failure")
	}
	if !errors.Is(err, startupErr) {
		t.Fatalf("RunBlocking() error = %v, want startup error", err)
	}
	if !errors.Is(err, shutdownErr) {
		t.Fatalf("RunBlocking() error = %v, want shutdown error", err)
	}
}

func TestShutdownUsesGracefulGRPCServerWhenAvailable(t *testing.T) {
	t.Parallel()

	grpcSrv := &testGracefulGRPCServer{}
	runner, err := NewRunner(Options{
		HTTPServer:      &http.Server{Addr: "127.0.0.1:0"},
		GRPCServer:      grpcSrv,
		EnableGRPC:      true,
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if !grpcSrv.graceful.Load() {
		t.Fatalf("expected graceful stop to run")
	}
	if grpcSrv.forced.Load() {
		t.Fatalf("did not expect forced stop fallback")
	}
}

type testGracefulGRPCServer struct {
	graceful atomic.Bool
	forced   atomic.Bool
}

func (s *testGracefulGRPCServer) Start() error { return nil }

func (s *testGracefulGRPCServer) Stop() {
	s.forced.Store(true)
}

func (s *testGracefulGRPCServer) GracefulStop(context.Context) error {
	s.graceful.Store(true)
	return nil
}
