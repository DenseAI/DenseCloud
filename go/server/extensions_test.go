package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type testExtension struct {
	name string
}

func replaceRuntimeExtensionsForTest(t *testing.T, replacements ...RuntimeExtension) {
	t.Helper()

	extensionsMu.Lock()
	previous := append([]RuntimeExtension(nil), extensions...)
	extensions = append([]RuntimeExtension(nil), replacements...)
	extensionsMu.Unlock()

	t.Cleanup(func() {
		extensionsMu.Lock()
		extensions = previous
		extensionsMu.Unlock()
	})
}

func (e testExtension) Name() string { return e.name }

func (e testExtension) APIMiddleware() []func(http.Handler) http.Handler { return nil }

func (e testExtension) RegisterRoutes(_ *http.ServeMux, _ *http.ServeMux) {}

func (e testExtension) Startup(context.Context) error { return nil }

func (e testExtension) Shutdown(context.Context) error { return nil }

func TestRegisterRuntimeExtension_DeduplicatesByName(t *testing.T) {
	replaceRuntimeExtensionsForTest(t)

	before := len(RuntimeExtensions())
	name := fmt.Sprintf("test-ext-%d", time.Now().UnixNano())

	RegisterRuntimeExtension(nil)
	RegisterRuntimeExtension(testExtension{name: ""})
	RegisterRuntimeExtension(testExtension{name: name})
	RegisterRuntimeExtension(testExtension{name: name})

	after := len(RuntimeExtensions())
	if after != before+1 {
		t.Fatalf("expected one new extension, before=%d after=%d", before, after)
	}
}
