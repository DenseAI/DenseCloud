package server

import (
	"context"
	"net/http"
	"sync"
)

// RuntimeExtension is an optional server extension that can inject middleware,
// routes, and startup/shutdown hooks at runtime.
//
// Implementations are typically registered from separate modules via init().
type RuntimeExtension interface {
	// Name returns a stable extension identifier.
	Name() string

	// APIMiddleware returns middleware to apply to /v1 API routes.
	APIMiddleware() []func(http.Handler) http.Handler

	// RegisterRoutes lets the extension mount HTTP handlers.
	//
	// rootMux is the top-level server mux, apiMux is the /v1 sub-mux.
	RegisterRoutes(rootMux, apiMux *http.ServeMux)

	// Startup runs before serving traffic.
	Startup(ctx context.Context) error

	// Shutdown runs during graceful termination.
	Shutdown(ctx context.Context) error
}

var (
	extensionsMu sync.RWMutex
	extensions   []RuntimeExtension
)

// RegisterRuntimeExtension registers a server runtime extension.
//
// Duplicate names are ignored to keep registration idempotent.
func RegisterRuntimeExtension(ext RuntimeExtension) {
	if ext == nil || ext.Name() == "" {
		return
	}

	extensionsMu.Lock()
	defer extensionsMu.Unlock()

	for _, existing := range extensions {
		if existing.Name() == ext.Name() {
			return
		}
	}
	extensions = append(extensions, ext)
}

// RuntimeExtensions returns a snapshot of all registered extensions.
func RuntimeExtensions() []RuntimeExtension {
	extensionsMu.RLock()
	defer extensionsMu.RUnlock()

	if len(extensions) == 0 {
		return nil
	}

	out := make([]RuntimeExtension, len(extensions))
	copy(out, extensions)
	return out
}
