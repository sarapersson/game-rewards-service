// Package observability owns process-local Prometheus registries and metrics.
package observability

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "game_rewards"

// NewRegistry creates an isolated process registry with Go runtime and process collectors.
func NewRegistry() (*prometheus.Registry, error) {
	registry := prometheus.NewRegistry()
	if err := registry.Register(collectors.NewGoCollector()); err != nil {
		return nil, fmt.Errorf("register Go collector: %w", err)
	}
	if err := registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, fmt.Errorf("register process collector: %w", err)
	}
	return registry, nil
}

// Handler exposes a specific registry and accepts GET requests only.
func Handler(gatherer prometheus.Gatherer) http.Handler {
	handler := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		MaxRequestsInFlight: 2,
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func register(registerer prometheus.Registerer, collectors ...prometheus.Collector) error {
	if registerer == nil {
		return fmt.Errorf("metrics registerer must not be nil")
	}

	for _, collector := range collectors {
		if err := registerer.Register(collector); err != nil {
			return err
		}
	}
	return nil
}
