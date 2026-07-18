// Package adminhttp provides the worker process health and metrics server.
package adminhttp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/sarapersson/game-rewards-service/internal/config"
	"github.com/sarapersson/game-rewards-service/internal/health"
)

const (
	routeLivez   = "/livez"
	routeReadyz  = "/readyz"
	routeMetrics = "/metrics"
)

type RequestObserver interface {
	ObserveRequest(route, method string, status int, duration time.Duration)
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
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

// NewServer builds the worker's isolated admin server.
func NewServer(
	cfg config.Config,
	logger *slog.Logger,
	metricsHandler http.Handler,
	observer RequestObserver,
	checks ...health.Check,
) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(routeLivez, health.LiveHandler())
	mux.Handle(routeReadyz, health.ReadyHandler(checks...))
	if metricsHandler != nil {
		mux.Handle(routeMetrics, metricsHandler)
	}
	mux.HandleFunc("/", notFound)

	return &http.Server{
		Addr:              cfg.Worker.AdminAddr,
		Handler:           middleware(mux, logger, observer),
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}
}

func middleware(next http.Handler, logger *slog.Logger, observer RequestObserver) http.Handler {
	return secureHeaders(requestLogger(recoverer(next, logger), logger, observer))
}

func recoverer(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(
					r.Context(),
					"worker admin panic recovered",
					slog.String("panic_type", fmt.Sprintf("%T", recovered)),
					slog.String("stack", string(debug.Stack())),
				)
				writeError(w, http.StatusInternalServerError, "internal_error", "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestLogger(next http.Handler, logger *slog.Logger, observer RequestObserver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)

		duration := time.Since(started)
		route := routeName(r)
		if observer != nil {
			observer.ObserveRequest(route, r.Method, recorder.status, duration)
		}

		level := slog.LevelDebug
		if recorder.status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		logger.Log(
			r.Context(),
			level,
			"worker admin request",
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.Int("status", recorder.status),
			slog.Int64("duration_ms", duration.Milliseconds()),
		)
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func routeName(r *http.Request) string {
	switch r.URL.Path {
	case routeLivez, routeReadyz, routeMetrics:
		return r.URL.Path
	default:
		return "unknown"
	}
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "Not found")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
