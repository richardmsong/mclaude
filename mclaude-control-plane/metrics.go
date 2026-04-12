package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

var (
	// httpRequestDuration measures HTTP request latency on the main API port.
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mclaude_http_request_duration_seconds",
			Help:    "HTTP request latency by method and path.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)

	// provisioningErrors counts errors during user/project provisioning operations.
	provisioningErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mclaude_provisioning_errors_total",
			Help: "Total provisioning errors by operation.",
		},
		[]string{"operation"},
	)

	// natsReconnects counts NATS reconnection events.
	natsReconnects = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "mclaude_nats_reconnects_total",
			Help: "Total NATS reconnection events.",
		},
	)
)

// MetricsRegistry is the Prometheus registry used by this process.
// Exposed for testutil.testutil assertions.
var MetricsRegistry = prometheus.NewRegistry()

func init() {
	MetricsRegistry.MustRegister(
		httpRequestDuration,
		provisioningErrors,
		natsReconnects,
	)
}

// metricsHandler returns an HTTP handler that serves Prometheus metrics
// from MetricsRegistry.
func metricsHandler() http.Handler {
	return promhttp.HandlerFor(MetricsRegistry, promhttp.HandlerOpts{})
}

// RecordProvisioningError increments the provisioning error counter.
func RecordProvisioningError(operation string) {
	provisioningErrors.WithLabelValues(operation).Inc()
}

// RecordNATSReconnect increments the NATS reconnect counter.
func RecordNATSReconnect() {
	natsReconnects.Inc()
}
