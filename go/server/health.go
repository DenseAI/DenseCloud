package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
)

// HealthCheck verifies runtime liveness/readiness/startup conditions.
type HealthCheck func(context.Context) error

// HealthPhase describes a shared DenseCloud health lifecycle phase.
type HealthPhase string

const (
	HealthPhaseLive    HealthPhase = "live"
	HealthPhaseReady   HealthPhase = "ready"
	HealthPhaseStartup HealthPhase = "startup"
)

// HealthDependency is implemented by dependencies that can self-check.
type HealthDependency interface {
	HealthCheck(context.Context) error
}

type healthCheckEntry struct {
	name string
	fn   HealthCheck
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
	mu        sync.RWMutex
	liveness  []healthCheckEntry
	readiness []healthCheckEntry
	startup   []healthCheckEntry

	started      atomic.Bool
	shuttingDown atomic.Bool
}

// NewHealthRegistry creates a registry with conservative startup defaults.
func NewHealthRegistry() *HealthRegistry {
	return &HealthRegistry{}
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
	r.register(&r.liveness, name, fn)
}

// RegisterReadiness adds a readiness check.
func (r *HealthRegistry) RegisterReadiness(name string, fn HealthCheck) {
	r.register(&r.readiness, name, fn)
}

// RegisterStartup adds a startup check.
func (r *HealthRegistry) RegisterStartup(name string, fn HealthCheck) {
	r.register(&r.startup, name, fn)
}

// RegisterCheck registers the same check across one or more phases.
func (r *HealthRegistry) RegisterCheck(name string, fn HealthCheck, phases ...HealthPhase) {
	if fn == nil || name == "" {
		return
	}
	if len(phases) == 0 {
		phases = []HealthPhase{HealthPhaseReady}
	}

	for _, phase := range phases {
		switch phase {
		case HealthPhaseLive:
			r.RegisterLiveness(name, fn)
		case HealthPhaseReady:
			r.RegisterReadiness(name, fn)
		case HealthPhaseStartup:
			r.RegisterStartup(name, fn)
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

func (r *HealthRegistry) register(target *[]healthCheckEntry, name string, fn HealthCheck) {
	if name == "" || fn == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	*target = append(*target, healthCheckEntry{name: name, fn: fn})
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
		if err := check.fn(ctx); err != nil {
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

func writeHealthJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
