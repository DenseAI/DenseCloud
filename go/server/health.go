package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// HealthCheck verifies runtime liveness/readiness/startup conditions.
type HealthCheck func(context.Context) error

// HealthPhase describes a shared DenseCloud health lifecycle phase.
type HealthPhase string

const (
	HealthPhaseLive    HealthPhase = "live"
	HealthPhaseReady   HealthPhase = "ready"
	HealthPhaseStartup HealthPhase = "startup"

	// DefaultHealthCheckTimeout keeps each dependency check below the chart's
	// shortest default Kubernetes probe timeout.
	DefaultHealthCheckTimeout = 2 * time.Second
)

// HealthDependency is implemented by dependencies that can self-check.
type HealthDependency interface {
	HealthCheck(context.Context) error
}

type healthCheckEntry struct {
	name      string
	fn        HealthCheck
	execution *healthCheckExecution
}

type healthCheckExecution struct {
	mu      sync.Mutex
	running bool
}

func (e *healthCheckExecution) tryStart() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return false
	}
	e.running = true
	return true
}

func (e *healthCheckExecution) finish() {
	e.mu.Lock()
	e.running = false
	e.mu.Unlock()
}

// HealthCheckResult captures the state of a named health check.
type HealthCheckResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// HealthReport is the JSON payload returned by health endpoints.
type HealthReport struct {
	Status string              `json:"status"`
	Phase  string              `json:"phase"`
	Checks []HealthCheckResult `json:"checks,omitempty"`
}

// HealthRegistry owns DenseCloud's shared health lifecycle contract.
type HealthRegistry struct {
	mu           sync.RWMutex
	liveness     []healthCheckEntry
	readiness    []healthCheckEntry
	startup      []healthCheckEntry
	checkTimeout time.Duration

	started      atomic.Bool
	shuttingDown atomic.Bool
}

// HealthRegistryOption configures the shared health registry.
type HealthRegistryOption func(*HealthRegistry)

// WithHealthCheckTimeout bounds each registered dependency check. Checks should
// still honor context cancellation so timed-out work can release resources.
func WithHealthCheckTimeout(timeout time.Duration) HealthRegistryOption {
	return func(registry *HealthRegistry) {
		if timeout > 0 {
			registry.checkTimeout = timeout
		}
	}
}

// NewHealthRegistry creates a registry with conservative startup defaults.
func NewHealthRegistry(options ...HealthRegistryOption) *HealthRegistry {
	registry := &HealthRegistry{checkTimeout: DefaultHealthCheckTimeout}
	for _, option := range options {
		if option != nil {
			option(registry)
		}
	}
	return registry
}

// MarkStarted flips startup/readiness into serviceable mode.
func (r *HealthRegistry) MarkStarted() {
	r.started.Store(true)
}

// MarkShuttingDown flips readiness to fail-close mode.
func (r *HealthRegistry) MarkShuttingDown() {
	r.shuttingDown.Store(true)
}

// RegisterLiveness adds a liveness check.
func (r *HealthRegistry) RegisterLiveness(name string, fn HealthCheck) {
	r.register(&r.liveness, newHealthCheckEntry(name, fn))
}

// RegisterReadiness adds a readiness check.
func (r *HealthRegistry) RegisterReadiness(name string, fn HealthCheck) {
	r.register(&r.readiness, newHealthCheckEntry(name, fn))
}

// RegisterStartup adds a startup check.
func (r *HealthRegistry) RegisterStartup(name string, fn HealthCheck) {
	r.register(&r.startup, newHealthCheckEntry(name, fn))
}

// RegisterCheck registers the same check across one or more phases.
func (r *HealthRegistry) RegisterCheck(name string, fn HealthCheck, phases ...HealthPhase) {
	if fn == nil || name == "" {
		return
	}
	if len(phases) == 0 {
		phases = []HealthPhase{HealthPhaseReady}
	}

	entry := newHealthCheckEntry(name, fn)
	for _, phase := range phases {
		switch phase {
		case HealthPhaseLive:
			r.register(&r.liveness, entry)
		case HealthPhaseReady:
			r.register(&r.readiness, entry)
		case HealthPhaseStartup:
			r.register(&r.startup, entry)
		}
	}
}

// RegisterDependency registers a dependency health check across one or more phases.
func (r *HealthRegistry) RegisterDependency(name string, dependency HealthDependency, phases ...HealthPhase) {
	if dependency == nil {
		return
	}
	r.RegisterCheck(name, dependency.HealthCheck, phases...)
}

// RegisterHandlers mounts the standard DenseCloud health endpoints.
func (r *HealthRegistry) RegisterHandlers(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.Handle("/health", r.summaryHandler())
	mux.Handle("/health/live", r.phaseHandler("live"))
	mux.Handle("/health/ready", r.phaseHandler("ready"))
	mux.Handle("/health/startup", r.phaseHandler("startup"))
}

func newHealthCheckEntry(name string, fn HealthCheck) healthCheckEntry {
	return healthCheckEntry{
		name:      name,
		fn:        fn,
		execution: &healthCheckExecution{},
	}
}

func (r *HealthRegistry) register(target *[]healthCheckEntry, entry healthCheckEntry) {
	if entry.name == "" || entry.fn == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	*target = append(*target, entry)
}

func (r *HealthRegistry) summaryHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		live, _ := r.evaluate(req.Context(), "live")
		ready, _ := r.evaluate(req.Context(), "ready")
		startup, _ := r.evaluate(req.Context(), "startup")

		status := "ok"
		if live.Status != "ok" || ready.Status != "ok" || startup.Status != "ok" {
			status = "degraded"
		}

		code := http.StatusOK
		if ready.Status != "ok" || startup.Status != "ok" {
			code = http.StatusServiceUnavailable
		}

		writeHealthJSON(w, code, map[string]any{
			"status":  status,
			"live":    live,
			"ready":   ready,
			"startup": startup,
		})
	})
}

func (r *HealthRegistry) phaseHandler(phase string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		report, ok := r.evaluate(req.Context(), phase)
		if ok {
			writeHealthJSON(w, http.StatusOK, report)
			return
		}
		writeHealthJSON(w, http.StatusServiceUnavailable, report)
	})
}

func (r *HealthRegistry) evaluate(ctx context.Context, phase string) (HealthReport, bool) {
	report := HealthReport{Status: "ok", Phase: phase}

	switch phase {
	case "live":
		return r.runChecks(ctx, phase, r.snapshot(r.liveness), report)
	case "ready":
		if !r.started.Load() {
			report.Status = "unavailable"
			report.Checks = append(report.Checks, HealthCheckResult{
				Name:   "runtime_started",
				Status: "unavailable",
				Error:  "runtime has not completed startup",
			})
			return report, false
		}
		if r.shuttingDown.Load() {
			report.Status = "unavailable"
			report.Checks = append(report.Checks, HealthCheckResult{
				Name:   "shutdown_state",
				Status: "unavailable",
				Error:  "runtime is shutting down",
			})
			return report, false
		}
		return r.runChecks(ctx, phase, r.snapshot(r.readiness), report)
	case "startup":
		if !r.started.Load() {
			report.Status = "starting"
			report.Checks = append(report.Checks, HealthCheckResult{
				Name:   "runtime_started",
				Status: "starting",
				Error:  "runtime has not completed startup",
			})
			return report, false
		}
		return r.runChecks(ctx, phase, r.snapshot(r.startup), report)
	default:
		report.Status = "unknown"
		report.Checks = append(report.Checks, HealthCheckResult{
			Name:   "phase",
			Status: "unknown",
			Error:  "unsupported health phase",
		})
		return report, false
	}
}

func (r *HealthRegistry) snapshot(entries []healthCheckEntry) []healthCheckEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(entries) == 0 {
		return nil
	}

	out := make([]healthCheckEntry, len(entries))
	copy(out, entries)
	return out
}

func (r *HealthRegistry) runChecks(ctx context.Context, phase string, checks []healthCheckEntry, report HealthReport) (HealthReport, bool) {
	healthy := true
	for _, check := range checks {
		result := HealthCheckResult{Name: check.name, Status: "ok"}
		if err := r.runCheck(ctx, check); err != nil {
			healthy = false
			result.Status = "unavailable"
			result.Error = err.Error()
		}
		report.Checks = append(report.Checks, result)
	}

	if !healthy {
		report.Status = "unavailable"
		return report, false
	}

	if report.Status == "" {
		report.Status = "ok"
	}
	return report, true
}

func (r *HealthRegistry) runCheck(ctx context.Context, check healthCheckEntry) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := r.healthCheckTimeout()
	if timeout <= 0 {
		timeout = DefaultHealthCheckTimeout
	}
	if check.execution == nil {
		return fmt.Errorf("health check execution state is unavailable")
	}
	if !check.execution.tryStart() {
		return fmt.Errorf("health check is still running from previous evaluation")
	}

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := make(chan error, 1)
	go func() {
		var checkErr error
		defer func() {
			if recovered := recover(); recovered != nil {
				checkErr = fmt.Errorf("health check panicked: %v", recovered)
			}
			check.execution.finish()
			result <- checkErr
		}()
		checkErr = check.fn(checkCtx)
	}()

	select {
	case err := <-result:
		return err
	case <-checkCtx.Done():
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if checkCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("health check timed out after %s: %w", timeout, checkCtx.Err())
		}
		return checkCtx.Err()
	}
}

func (r *HealthRegistry) setHealthCheckTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	r.mu.Lock()
	r.checkTimeout = timeout
	r.mu.Unlock()
}

func (r *HealthRegistry) healthCheckTimeout() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.checkTimeout
}

func writeHealthJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
