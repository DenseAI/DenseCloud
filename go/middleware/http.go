package middleware

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type contextKey string

const requestIDKey contextKey = "request_id"

// RequestID ensures every request has a stable request identifier.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = newRequestID()
			}
			w.Header().Set("X-Request-ID", requestID)
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetRequestID returns request ID from context.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

func requestIDForLog(r *http.Request, w http.ResponseWriter) string {
	if id := GetRequestID(r.Context()); id != "" {
		return id
	}
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	if id := w.Header().Get("X-Request-ID"); id != "" {
		return id
	}
	return ""
}

// RequestTimeout attaches a timeout to each request context.
func RequestTimeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if timeout <= 0 {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Recovery catches panics and prevents process crashes.
func Recovery() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					slog.Error("panic recovered",
						slog.String("request_id", requestIDForLog(r, w)),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.Any("panic", rec),
						slog.String("stack", string(debug.Stack())),
					)
					writeJSONError(w, http.StatusInternalServerError,
						"internal_server_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORS adds permissive CORS policy for configured origins.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowAll := len(allowedOrigins) == 1 && allowedOrigins[0] == "*"
	isAllowed := func(origin string) bool {
		if allowAll {
			return true
		}
		for _, allowed := range allowedOrigins {
			if origin == strings.TrimSpace(allowed) {
				return true
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// CORS response selection depends on Origin, so caches must vary on it.
			addVaryHeader(w.Header(), "Origin")
			// Preflight handling depends on requested method and headers.
			if r.Method == http.MethodOptions {
				addVaryHeader(w.Header(), "Access-Control-Request-Method", "Access-Control-Request-Headers")
			}

			origin := r.Header.Get("Origin")
			isPreflight := r.Method == http.MethodOptions &&
				origin != "" &&
				r.Header.Get("Access-Control-Request-Method") != ""
			originAllowed := origin != "" && isAllowed(origin)

			if originAllowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
				w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			// Only short-circuit actual CORS preflight requests.
			if isPreflight && originAllowed {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func addVaryHeader(header http.Header, fields ...string) {
	if len(fields) == 0 {
		return
	}

	existing := make(map[string]struct{})
	for _, value := range header.Values("Vary") {
		for _, part := range strings.Split(value, ",") {
			token := strings.TrimSpace(part)
			if token == "" {
				continue
			}
			existing[strings.ToLower(token)] = struct{}{}
		}
	}

	for _, field := range fields {
		token := strings.TrimSpace(field)
		if token == "" {
			continue
		}
		key := strings.ToLower(token)
		if _, ok := existing[key]; ok {
			continue
		}
		header.Add("Vary", token)
		existing[key] = struct{}{}
	}
}

// MaxBodySize rejects oversized request bodies early.
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maxBytes > 0 && r.ContentLength > maxBytes {
				writeJSONError(w, http.StatusRequestEntityTooLarge,
					"request_too_large", "request body too large")
				return
			}
			if maxBytes > 0 {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ContentType sets default content type for API endpoints.
func ContentType(contentType string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v1/") && w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", contentType)
			}
			next.ServeHTTP(w, r)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	return n, err
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacker not supported")
	}
	return h.Hijack()
}

func (rw *responseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := rw.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (rw *responseWriter) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := rw.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(src)
		rw.written += n
		return n, err
	}
	n, err := io.Copy(rw.ResponseWriter, src)
	rw.written += n
	return n, err
}

// Logging records request metadata via slog.
func Logging() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(wrapped, r)

			latencyMs := float64(time.Since(start).Microseconds()) / 1000.0
			clientIP := r.RemoteAddr
			if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
				if i := strings.Index(fwd, ","); i >= 0 {
					clientIP = strings.TrimSpace(fwd[:i])
				} else {
					clientIP = strings.TrimSpace(fwd)
				}
			}

			attrs := []any{
				slog.String("request_id", GetRequestID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status_code", wrapped.statusCode),
				slog.Float64("latency_ms", latencyMs),
				slog.String("client_ip", clientIP),
				slog.String("user_agent", r.UserAgent()),
				slog.Int64("bytes_written", wrapped.written),
			}

			switch {
			case wrapped.statusCode >= 500:
				slog.Error("request completed", attrs...)
			case wrapped.statusCode >= 400:
				slog.Warn("request completed", attrs...)
			default:
				slog.Info("request completed", attrs...)
			}
		})
	}
}

// Chain combines middleware in declaration order.
func Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}

func writeJSONError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
