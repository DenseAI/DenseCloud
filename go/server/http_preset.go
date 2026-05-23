package server

import (
	"net/http"
	"time"

	densemiddleware "github.com/DenseAI/DenseCloud/go/middleware"
)

// HTTPMiddlewarePresetConfig defines the DenseCloud-owned root middleware preset.
type HTTPMiddlewarePresetConfig struct {
	TracerName     string
	RequestTimeout time.Duration
}

// DefaultHTTPMiddleware returns the canonical DenseCloud root HTTP middleware order.
// Product-specific concerns such as auth, CORS, and body limits should be appended
// by consumers after this chassis-owned baseline.
func DefaultHTTPMiddleware(cfg HTTPMiddlewarePresetConfig) []func(http.Handler) http.Handler {
	tracerName := cfg.TracerName
	if tracerName == "" {
		tracerName = "dense-service"
	}

	middlewares := []func(http.Handler) http.Handler{
		densemiddleware.RequestID(),
		densemiddleware.Recovery(),
	}
	if cfg.RequestTimeout > 0 {
		middlewares = append(middlewares, densemiddleware.RequestTimeout(cfg.RequestTimeout))
	}
	middlewares = append(middlewares,
		densemiddleware.Tracing(tracerName),
		densemiddleware.Logging(),
	)
	return middlewares
}
