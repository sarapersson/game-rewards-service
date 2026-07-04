package httpapi

import (
	"log/slog"
	"net/http"
)

const (
	routeLivez  = "/livez"
	routeReadyz = "/readyz"
)

func newRouter(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(routeLivez, livezHandler)
	mux.HandleFunc(routeReadyz, readyzHandler)

	return withMiddleware(notFoundHandler(mux), logger)
}

func notFoundHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != routeLivez && r.URL.Path != routeReadyz {
			writeError(w, http.StatusNotFound, errorCodeNotFound, "Not found")
			return
		}

		next.ServeHTTP(w, r)
	})
}
