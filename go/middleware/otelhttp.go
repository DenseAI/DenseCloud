package middleware

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// OTelHTTPMiddleware returns HTTP middleware with OpenTelemetry instrumentation.
func OTelHTTPMiddleware(serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, serviceName,
			otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
				return fmt.Sprintf("%s %s", r.Method, r.URL.Path)
			}),
			otelhttp.WithFilter(func(r *http.Request) bool {
				path := r.URL.Path
				if path == "/health" || path == "/health/live" || path == "/health/ready" || path == "/health/startup" {
					return false
				}
				return true
			}),
		)
	}
}
