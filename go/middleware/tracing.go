package middleware

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// GetTraceID extracts the trace ID from the OTel span context.
// Falls back to request ID if no active span.
func GetTraceID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasTraceID() {
		return span.SpanContext().TraceID().String()
	}
	return GetRequestID(ctx)
}

// GetSpanID extracts the span ID from the OTel span context.
func GetSpanID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasSpanID() {
		return span.SpanContext().SpanID().String()
	}
	return ""
}

// Tracing creates OpenTelemetry-compatible tracing middleware.
// Extracts incoming trace context from W3C headers, creates a new span per
// request, and injects trace context into response headers.
func Tracing(tracerName string) func(http.Handler) http.Handler {
	tracer := otel.Tracer(tracerName)
	propagator := otel.GetTextMapPropagator()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path)
			defer span.End()

			propagator.Inject(ctx, propagation.HeaderCarrier(w.Header()))

			if span.SpanContext().HasTraceID() {
				w.Header().Set("X-Trace-ID", span.SpanContext().TraceID().String())
			}
			if span.SpanContext().HasSpanID() {
				w.Header().Set("X-Span-ID", span.SpanContext().SpanID().String())
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// InitOTelPropagator sets up the global W3C TraceContext propagator.
// Applications should call this during startup before using tracing middleware.
func InitOTelPropagator() {
	otel.SetTextMapPropagator(propagation.TraceContext{})
}
