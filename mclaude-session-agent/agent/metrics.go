package main

import (
	"context"
	"net/http"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Metrics holds all Prometheus counters and gauges for the session agent.
type Metrics struct {
	activeSessions      prometheus.Gauge
	eventsPublished     *prometheus.CounterVec
	natsReconnects      prometheus.Counter
	claudeRestarts      prometheus.Counter
}

// NewMetrics registers all metrics with the given registerer.
// Pass prometheus.DefaultRegisterer in production, a fresh
// prometheus.NewRegistry() in tests (to avoid metric conflicts).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		activeSessions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mclaude_active_sessions",
			Help: "Number of currently active Claude Code sessions.",
		}),
		eventsPublished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mclaude_events_published_total",
			Help: "Total stream-json events published to NATS, by event type.",
		}, []string{"event_type"}),
		natsReconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mclaude_nats_reconnects_total",
			Help: "Total number of NATS reconnections.",
		}),
		claudeRestarts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mclaude_claude_restarts_total",
			Help: "Total number of Claude process restarts.",
		}),
	}
	reg.MustRegister(
		m.activeSessions,
		m.eventsPublished,
		m.natsReconnects,
		m.claudeRestarts,
	)
	return m
}

// SessionOpened increments active_sessions.
func (m *Metrics) SessionOpened() {
	m.activeSessions.Inc()
}

// SessionClosed decrements active_sessions.
func (m *Metrics) SessionClosed() {
	m.activeSessions.Dec()
}

// EventPublished increments the events_published counter for the given type.
func (m *Metrics) EventPublished(evType string) {
	m.eventsPublished.WithLabelValues(evType).Inc()
}

// NATSReconnect increments the nats_reconnects counter.
func (m *Metrics) NATSReconnect() {
	m.natsReconnects.Inc()
}

// ClaudeRestart increments the claude_restarts counter.
func (m *Metrics) ClaudeRestart() {
	m.claudeRestarts.Inc()
}

// StartMetricsServer starts an HTTP server exposing Prometheus metrics at
// /metrics on the given addr (e.g. ":9091").
func StartMetricsServer(addr string, reg prometheus.Gatherer) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	srv := &http.Server{Addr: addr, Handler: mux}
	go srv.ListenAndServe()
	return srv
}

// --- OpenTelemetry helpers ---

const tracerName = "mclaude-session-agent"

// Tracer returns the global tracer for this component.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// StartSpan starts a new span and returns the updated context and span.
// The caller must call span.End() when done.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, trace.WithAttributes(attrs...))
}

// NATSPublishSpan wraps a NATS publish with a trace span.
func NATSPublishSpan(ctx context.Context, subject string) (context.Context, trace.Span) {
	return StartSpan(ctx, "nats.publish",
		attribute.String("nats.subject", subject),
		attribute.String("component", "session-agent"),
	)
}

// KVWriteSpan wraps a KV write with a trace span.
func KVWriteSpan(ctx context.Context, bucket, key string) (context.Context, trace.Span) {
	return StartSpan(ctx, "kv.write",
		attribute.String("nats.kv.bucket", bucket),
		attribute.String("nats.kv.key", key),
		attribute.String("component", "session-agent"),
	)
}

// SessionSpan starts a session lifecycle span.
func SessionSpan(ctx context.Context, operation, sessionID string) (context.Context, trace.Span) {
	return StartSpan(ctx, "session."+operation,
		attribute.String("session.id", sessionID),
		attribute.String("component", "session-agent"),
	)
}

// ClaudeSpawnSpan starts a span for a Claude process spawn.
func ClaudeSpawnSpan(ctx context.Context, sessionID string, resumed bool) (context.Context, trace.Span) {
	return StartSpan(ctx, "claude.spawn",
		attribute.String("session.id", sessionID),
		attribute.Bool("session.resumed", resumed),
		attribute.String("component", "session-agent"),
	)
}

// NATSSubscribeSpan starts a span representing a NATS subscription delivery.
func NATSSubscribeSpan(ctx context.Context, subject string) (context.Context, trace.Span) {
	return StartSpan(ctx, "nats.subscribe",
		attribute.String("nats.subject", subject),
		attribute.String("component", "session-agent"),
	)
}

// --- Traceparent header propagation ---

// natsHeaderCarrier adapts a *nats.Msg's header map to the
// propagation.TextMapCarrier interface so trace context can be injected
// into / extracted from NATS message headers.
type natsHeaderCarrier nats.Header

func (c natsHeaderCarrier) Get(key string) string {
	return nats.Header(c).Get(key)
}

func (c natsHeaderCarrier) Set(key, val string) {
	nats.Header(c).Set(key, val)
}

func (c natsHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// InjectTraceparent injects the current trace context from ctx into the
// NATS message header as a W3C traceparent field.
func InjectTraceparent(ctx context.Context, msg *nats.Msg) {
	if msg.Header == nil {
		msg.Header = make(nats.Header)
	}
	otel.GetTextMapPropagator().Inject(ctx, natsHeaderCarrier(msg.Header))
}

// ExtractTraceparent extracts a trace context from the NATS message header
// and returns a new context with the remote span context attached.
func ExtractTraceparent(ctx context.Context, msg *nats.Msg) context.Context {
	if msg.Header == nil {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, natsHeaderCarrier(msg.Header))
}

// SetupPropagator installs the W3C TraceContext propagator globally so that
// InjectTraceparent / ExtractTraceparent use the standard traceparent header.
func SetupPropagator() {
	otel.SetTextMapPropagator(propagation.TraceContext{})
}
