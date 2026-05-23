package main

import (
	"context"
	"net/http"

	"github.com/DenseAI/DenseCloud/go/server"
)

type dependency struct{}

func (dependency) HealthCheck(context.Context) error {
	return nil
}

func main() {
	health := server.NewHealthRegistry()
	health.RegisterDependency("redis", dependency{}, server.HealthPhaseReady, server.HealthPhaseStartup)
	health.MarkStarted()

	mux := http.NewServeMux()
	health.RegisterHandlers(mux)
	_ = mux
}
