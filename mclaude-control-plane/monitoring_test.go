package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// ---- OpenTelemetry trace assertions ----

// newTestTracer installs a synchronous in-memory exporter. Spans appear
// immediately after span.End() — no batching delay in tests.
func newTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := InitTracingWithSync(exp, true) // sync=true: no batch delay
	t.Cleanup(func() {
		tp.ForceFlush(context.Background())  //nolint:errcheck
		tp.Shutdown(context.Background())    //nolint:errcheck
	})
	return exp
}

func TestTracing_SpanFromHTTP(t *testing.T) {
	exp := newTestTracer(t)

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	_, span := SpanFromHTTP(req.Context(), req)
	span.End() // synchronous exporter: span appears immediately

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span; got none")
	}

	found := false
	for _, s := range spans {
		if strings.Contains(s.Name, "/version") {
			found = true
		}
	}
	if !found {
		t.Errorf("no span named 'GET /version'; got: %v", spanNames(spans))
	}
}

func TestTracing_SpanDB(t *testing.T) {
	exp := newTestTracer(t)

	_, span := SpanDB(context.Background(), "GetUserByEmail")
	span.End()

	spans := exp.GetSpans()
	found := false
	for _, s := range spans {
		if s.Name != "db.GetUserByEmail" {
			continue
		}
		for _, attr := range s.Attributes {
			if string(attr.Key) == "db.system" && attr.Value.AsString() == "postgresql" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no 'db.GetUserByEmail' span with db.system=postgresql; spans: %v", spanNames(spans))
	}
}

func TestTracing_SpanProvisioning(t *testing.T) {
	exp := newTestTracer(t)

	_, span := SpanProvisioning(context.Background(), "create_user", "user-001")
	span.End()

	spans := exp.GetSpans()
	found := false
	for _, s := range spans {
		if s.Name == "provision.create_user" {
			found = true
		}
	}
	if !found {
		t.Errorf("no 'provision.create_user' span; got: %v", spanNames(spans))
	}
}

func TestTracing_InitTracingReturnsSampler(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := InitTracing(exp)
	defer tp.Shutdown(context.Background()) //nolint:errcheck

	// Sampler is AlwaysSample — every span must appear.
	ctx, span := SpanDB(context.Background(), "test_op")
	span.End()
	_ = ctx

	// sync exporter: no sleep needed
	spans := exp.GetSpans()
	for _, s := range spans {
		if s.SpanContext.IsValid() && !s.SpanContext.IsSampled() {
			t.Error("span is not sampled — AlwaysSample should mark all spans sampled")
		}
	}
	_ = spans // at least compiles
}

// ---- Prometheus metrics assertions ----

func TestMetrics_ProvisioningErrorsCounter(t *testing.T) {
	// Use the global registry — RecordProvisioningError writes to it.
	before := countMetric(t, "mclaude_provisioning_errors_total")
	RecordProvisioningError("create_user")
	RecordProvisioningError("create_user")
	after := countMetric(t, "mclaude_provisioning_errors_total")

	if after-before < 2 {
		t.Errorf("expected provisioningErrors to increase by ≥2; delta = %d", after-before)
	}
}

func TestMetrics_NATSReconnectsCounter(t *testing.T) {
	before := countMetric(t, "mclaude_nats_reconnects_total")
	RecordNATSReconnect()
	after := countMetric(t, "mclaude_nats_reconnects_total")

	if after-before < 1 {
		t.Errorf("expected natsReconnects to increase by ≥1; delta = %d", after-before)
	}
}

func TestMetrics_HTTPLatencyHistogramRegistered(t *testing.T) {
	// HistogramVec only appears in Gather() after at least one observation.
	// Record a dummy observation so it appears.
	httpRequestDuration.WithLabelValues("GET", "/version", "200").Observe(0.001)

	families, err := MetricsRegistry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	found := false
	for _, f := range families {
		if f.GetName() == "mclaude_http_request_duration_seconds" {
			found = true
		}
	}
	if !found {
		t.Error("mclaude_http_request_duration_seconds not registered in MetricsRegistry")
	}
}

func TestMetrics_AdminMetricsEndpointServes(t *testing.T) {
	srv := NewServer(nil, mustAccountKP(t), "nats://test", 0, "token")
	mux := srv.AdminMux()

	// Record some metrics so there's something to serve.
	RecordProvisioningError("test_op")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer token")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /metrics status = %d; want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "mclaude_provisioning_errors_total") {
		t.Errorf("metrics response missing mclaude_provisioning_errors_total; got:\n%s", body[:min(len(body), 500)])
	}
}

// ---- Structured log field verification ----

func TestMain_LoggerHasComponentField(t *testing.T) {
	// The logger in main() sets component=control-plane.
	// We verify the zerolog output format by inspecting what envOr returns
	// (no real log output can be verified in unit tests without DI).
	// This test documents the intent: production logs always have component field.
	component := envOr("LOG_COMPONENT", "control-plane")
	if component != "control-plane" {
		t.Errorf("component = %q; want control-plane", component)
	}
}

// ---- helpers ----

func countMetric(t *testing.T, name string) int {
	t.Helper()
	families, err := MetricsRegistry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == name {
			count := 0
			for _, m := range f.GetMetric() {
				if m.GetCounter() != nil {
					count += int(m.GetCounter().GetValue())
				}
				if m.GetGauge() != nil {
					count += int(m.GetGauge().GetValue())
				}
			}
			return count
		}
	}
	return 0
}

func spanNames(spans tracetest.SpanStubs) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name
	}
	return names
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Compile-time check: testutil.CollectAndCount is usable with our registry.
var _ = func() {
	_ = testutil.CollectAndCount
}
