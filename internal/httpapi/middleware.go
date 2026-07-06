package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

const (
	headerRequestID    = "X-Request-ID"
	maxRequestIDLen    = 128
	maxUserAgentLogLen = 256
)

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}

	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}

	return r.ResponseWriter.Write(body)
}

func withMiddleware(handler http.Handler, logger *slog.Logger) http.Handler {
	return requestID(
		requestLogger(logger)(
			recoverer(logger)(
				secureHeaders(handler),
			),
		),
	)
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.ErrorContext(
						r.Context(),
						"panic recovered",
						slog.String("panic_type", fmt.Sprintf("%T", recovered)),
						slog.String("request_id", requestIDFromRequest(r)),
						slog.String("stack", string(debug.Stack())),
					)

					writeError(w, http.StatusInternalServerError, errorCodeInternal, "Internal server error")
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := sanitizeRequestID(r.Header.Get(headerRequestID))
		if requestID == "" {
			requestID = newRequestID()
		}

		w.Header().Set(headerRequestID, requestID)

		ctx := contextWithRequestID(r.Context(), requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		next.ServeHTTP(w, r)
	})
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rec := &statusRecorder{
				ResponseWriter: w,
				status:         http.StatusOK,
			}

			next.ServeHTTP(rec, r)

			logger.InfoContext(
				r.Context(),
				"http request",
				slog.String("request_id", requestIDFromRequest(r)),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("route", routeName(r)),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", time.Since(started).Milliseconds()),
				slog.String("remote_addr", clientIP(r)),
				slog.String("user_agent", truncateString(r.UserAgent(), maxUserAgentLogLen)),
			)
		})
	}
}

func sanitizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxRequestIDLen {
		return ""
	}

	for _, r := range value {
		if !isAllowedRequestIDRune(r) {
			return ""
		}
	}

	return value
}

func isAllowedRequestIDRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-' ||
		r == '_' ||
		r == '.' ||
		r == ':'
}

func newRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}

	return hex.EncodeToString(bytes[:])
}

func routeName(r *http.Request) string {
	switch r.URL.Path {
	case routeLivez:
		return routeLivez
	case routeReadyz:
		return routeReadyz
	default:
		return "unknown"
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

func truncateString(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}

	return value[:maxLen]
}
