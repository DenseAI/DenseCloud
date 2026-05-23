package main

import (
	"net/http"

	"github.com/DenseAI/DenseCloud/go/telemetry"
)

func main() {
	grpcMetrics := telemetry.NewGRPCMetrics(telemetry.GRPCMetricsConfig{
		ServiceName: "dense-consumer",
	})
	httpMetrics := telemetry.NewHTTPMetrics(telemetry.HTTPMetricsConfig{
		ServiceName: "dense-consumer",
		Collectors:  []telemetry.PrometheusCollector{grpcMetrics},
	})

	mux := http.NewServeMux()
	mux.Handle("/metrics", httpMetrics.Handler())
	mux.Handle("/v1/status", httpMetrics.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	_ = mux
}
