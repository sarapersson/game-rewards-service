package observability

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func durationBuckets() []float64 {
	return []float64{
		0.005, 0.010, 0.025, 0.050, 0.100, 0.250,
		0.500, 1.000, 2.500, 5.000, 10.000,
	}
}

// HTTPMetrics records low-cardinality HTTP request metrics.
type HTTPMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func NewHTTPMetrics(registerer prometheus.Registerer) (*HTTPMetrics, error) {
	metrics := &HTTPMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total number of HTTP requests handled.",
		}, []string{"route", "method", "status_code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
			Buckets:   durationBuckets(),
		}, []string{"route", "method"}),
	}

	if err := register(registerer, metrics.requests, metrics.duration); err != nil {
		return nil, err
	}
	return metrics, nil
}

func (m *HTTPMetrics) ObserveRequest(route, method string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	route = normalizeRoute(route)
	method = normalizeMethod(method)
	statusCode := "unknown"
	if status >= 100 && status <= 599 {
		statusCode = strconv.Itoa(status)
	}

	m.requests.WithLabelValues(route, method, statusCode).Inc()
	m.duration.WithLabelValues(route, method).Observe(duration.Seconds())
}

func normalizeRoute(route string) string {
	switch route {
	case "/livez", "/readyz", "/metrics", "/v1/reward-claims", "unknown":
		return route
	default:
		return "unknown"
	}
}

func normalizeMethod(method string) string {
	switch method {
	case "GET", "POST":
		return method
	default:
		return "other"
	}
}
