package middleware

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Logger creates a structured logging middleware
func Logger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			ww := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(ww, r)

			duration := time.Since(start)

			logger.Info("HTTP Request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("query", r.URL.RawQuery),
				zap.Int("status", ww.statusCode),
				zap.Duration("duration", duration),
				zap.String("ip", getClientIP(r)),
				zap.String("user_agent", r.UserAgent()),
				zap.String("request_id", w.Header().Get("X-Request-ID")),
			)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// HealthExclude excludes health check paths from logging
func HealthExclude(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip logging for health endpoints
			if r.URL.Path == "/health" || r.URL.Path == "/ready" || r.URL.Path == "/health/docker" {
				next.ServeHTTP(w, r)
				return
			}

			Logger(logger)(next).ServeHTTP(w, r)
		})
	}
}
