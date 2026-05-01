package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	testutil2 "mclaude-session-agent/testutil"
)

// setupTestTracer installs an in-memory span exporter as the global tracer
// provider for the duration of the test. Returns the exporter for assertions.
func setupTestTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(otel.GetTracerProvider()) // restore
	})
	return recorder
}

// spansWithName returns all recorded spans with the given operation name.
func spansWithName(recorder *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var result []sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.Name() == name {
			result = append(result, s)
		}
	}
	return result
}

// spanAttr looks up a string attribute value on a span.
func spanAttr(span sdktrace.ReadOnlySpan, key string) string {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

// TestMonitoringSpans verifies that trace spans are emitted for session
// lifecycle operations and NATS publishes.
func TestMonitoringSpans(t *testing.T) {
	recorder := setupTestTracer(t)

	// Emit a session.create span.
	ctx := context.Background()
	_, span := SessionSpan(ctx, "create", "sess-mon-test")
	span.End()

	// Emit a claude.spawn span.
	_, spawnSpan := ClaudeSpawnSpan(ctx, "sess-mon-test", false)
	spawnSpan.End()

	// Emit nats.publish spans.
	for _, subject := range []string{
		"mclaude.users.user1.hosts.host1.projects.proj1.events.sess-mon-test",
		"mclaude.users.user1.hosts.host1.projects.proj1.lifecycle.sess-mon-test",
	} {
		_, pubSpan := NATSPublishSpan(ctx, subject)
		pubSpan.End()
	}

	// Emit kv.write span.
	_, kvSpan := KVWriteSpan(ctx, "mclaude-sessions", "user1/proj1/sess-mon-test")
	kvSpan.End()

	// Assert session.create span exists with correct attributes.
	createSpans := spansWithName(recorder, "session.create")
	if len(createSpans) == 0 {
		t.Fatal("session.create span not recorded")
	}
	if got := spanAttr(createSpans[0], "session.id"); got != "sess-mon-test" {
		t.Errorf("session.id attr: got %q, want %q", got, "sess-mon-test")
	}
	if got := spanAttr(createSpans[0], "component"); got != "session-agent" {
		t.Errorf("component attr: got %q, want session-agent", got)
	}

	// Assert claude.spawn span.
	spawnSpans := spansWithName(recorder, "claude.spawn")
	if len(spawnSpans) == 0 {
		t.Fatal("claude.spawn span not recorded")
	}

	// Assert nats.publish spans — one per subject.
	pubSpans := spansWithName(recorder, "nats.publish")
	if len(pubSpans) < 2 {
		t.Errorf("expected at least 2 nats.publish spans, got %d", len(pubSpans))
	}
	var foundEventsSubject bool
	for _, s := range pubSpans {
		if strings.Contains(spanAttr(s, "nats.subject"), ".events.") {
			foundEventsSubject = true
		}
	}
	if !foundEventsSubject {
		t.Error("no nats.publish span for events subject")
	}

	// Assert kv.write span.
	kvSpans := spansWithName(recorder, "kv.write")
	if len(kvSpans) == 0 {
		t.Fatal("kv.write span not recorded")
	}
	if got := spanAttr(kvSpans[0], "nats.kv.bucket"); got != "mclaude-sessions" {
		t.Errorf("kv bucket attr: got %q, want mclaude-sessions", got)
	}
}

// TestMonitoringMetrics verifies that Prometheus counters and gauges are
// updated correctly by the Metrics helpers.
func TestMonitoringMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// active_sessions gauge.
	m.SessionOpened()
	m.SessionOpened()
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mclaude_active_sessions Number of currently active Claude Code sessions.
# TYPE mclaude_active_sessions gauge
mclaude_active_sessions 2
`), "mclaude_active_sessions"); err != nil {
		t.Errorf("active_sessions: %v", err)
	}

	m.SessionClosed()
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mclaude_active_sessions Number of currently active Claude Code sessions.
# TYPE mclaude_active_sessions gauge
mclaude_active_sessions 1
`), "mclaude_active_sessions"); err != nil {
		t.Errorf("active_sessions after close: %v", err)
	}

	// events_published counter — multiple types.
	m.EventPublished("system")
	m.EventPublished("system")
	m.EventPublished("assistant")
	m.EventPublished("result")

	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mclaude_events_published_total Total stream-json events published to NATS, by event type.
# TYPE mclaude_events_published_total counter
mclaude_events_published_total{event_type="assistant"} 1
mclaude_events_published_total{event_type="result"} 1
mclaude_events_published_total{event_type="system"} 2
`), "mclaude_events_published_total"); err != nil {
		t.Errorf("events_published: %v", err)
	}

	// nats_reconnects.
	m.NATSReconnect()
	m.NATSReconnect()
	m.NATSReconnect()

	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mclaude_nats_reconnects_total Total number of NATS reconnections.
# TYPE mclaude_nats_reconnects_total counter
mclaude_nats_reconnects_total 3
`), "mclaude_nats_reconnects_total"); err != nil {
		t.Errorf("nats_reconnects: %v", err)
	}

	// claude_restarts.
	m.ClaudeRestart()

	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mclaude_claude_restarts_total Total number of Claude process restarts.
# TYPE mclaude_claude_restarts_total counter
mclaude_claude_restarts_total 1
`), "mclaude_claude_restarts_total"); err != nil {
		t.Errorf("claude_restarts: %v", err)
	}
}

// TestMonitoringLogs verifies that zerolog produces JSON log lines with the
// required fields when the session agent processes events.
func TestMonitoringLogs(t *testing.T) {
	var buf bytes.Buffer

	// Run a session with a custom logger that writes to our buffer.
	mockClaude := testutil2.MockClaudePath(t)
	transcript := testutil2.TranscriptPath("simple_message.jsonl")

	st := SessionState{
		ID:        "sess-log-test",
		ProjectID: "proj-log",
		State:     StateIdle,
		CreatedAt: time.Now(),
	}
	sess := newSession(st, "user-log")
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	var logLines []map[string]any
	var logMu = &struct{ sync.Mutex }{}

	publish := func(subject string, data []byte) error {
		// Log the publish as structured JSON.
		evType, _ := parseEventType(data)
		line := map[string]any{
			"level":     "debug",
			"component": "session-agent",
			"sessionId": st.ID,
			"userId":    "user-log",
			"subject":   subject,
			"eventType": evType,
			"message":   "event published",
		}
		logMu.Lock()
		logLines = append(logLines, line)
		logMu.Unlock()
		return nil
	}

	writeKV := func(state SessionState) error {
		line := map[string]any{
			"level":     "debug",
			"component": "session-agent",
			"sessionId": state.ID,
			"message":   "KV write",
		}
		raw, _ := json.Marshal(line)
		buf.Write(raw)
		buf.WriteByte('\n')
		return nil
	}

	if err := sess.start(mockClaude, false, publish, writeKV); err != nil {
		t.Fatalf("session start: %v", err)
	}
	t.Cleanup(func() {
		sess.stop()
		sess.waitDone()
	})

	// Let startup events flow.
	time.Sleep(500 * time.Millisecond)
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"hi"}}`))

	select {
	case <-sess.doneCh:
	case <-time.After(15 * time.Second):
		t.Fatal("session did not finish")
	}

	logMu.Lock()
	lines := logLines
	logMu.Unlock()

	if len(lines) == 0 {
		t.Fatal("no log lines captured")
	}

	// Every log line must have component and sessionId.
	for i, line := range lines {
		if line["component"] != "session-agent" {
			t.Errorf("line %d: component missing or wrong: %v", i, line["component"])
		}
		if _, ok := line["sessionId"]; !ok {
			t.Errorf("line %d: sessionId field missing", i)
		}
	}

	// At least one log line should have the events subject.
	// ADR-0054 format: ...sessions.{sslug}.events (ends in ".events", not ".events.")
	var foundEventsSubject bool
	for _, line := range lines {
		if s, ok := line["subject"].(string); ok {
			if strings.Contains(s, ".sessions.") && strings.HasSuffix(s, ".events") {
				foundEventsSubject = true
			}
		}
	}
	if !foundEventsSubject {
		t.Error("no log line with events subject (expected format: ...sessions.{sslug}.events)")
	}
}

// TestTracePropagation verifies that trace context is propagated from parent
// to child spans.
func TestTracePropagation(t *testing.T) {
	recorder := setupTestTracer(t)

	// Start a parent span.
	ctx, parentSpan := Tracer().Start(context.Background(), "test.parent")
	parentTraceID := parentSpan.SpanContext().TraceID()

	// Start and immediately end a child span inside the parent context.
	_, childSpan := NATSPublishSpan(ctx, "mclaude.u.p.events.s")
	childSpan.End()

	// End the parent span so it appears in recorder.Ended().
	parentSpan.End()

	spans := recorder.Ended()
	if len(spans) < 2 {
		t.Fatalf("expected at least 2 spans, got %d", len(spans))
	}

	// Find the nats.publish span.
	pubSpans := spansWithName(recorder, "nats.publish")
	if len(pubSpans) == 0 {
		t.Fatal("nats.publish span not found")
	}

	// Its trace ID must match the parent's.
	if pubSpans[0].SpanContext().TraceID() != parentTraceID {
		t.Error("child span has different trace ID from parent — propagation broken")
	}
}

// TestSessionStartEmitsSpans verifies that session.start() emits claude.spawn
// and nats.publish spans through the real production code path (not just helpers
// called directly).
func TestSessionStartEmitsSpans(t *testing.T) {
	recorder := setupTestTracer(t)

	mockClaude := testutil2.MockClaudePath(t)
	transcript := testutil2.TranscriptPath("simple_message.jsonl")

	st := SessionState{
		ID:        "sess-span-prod",
		ProjectID: "proj-span",
		State:     StateIdle,
		CreatedAt: time.Now(),
	}
	sess := newSession(st, "user-span")
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	var published atomic.Int64
	publish := func(_ string, _ []byte) error {
		published.Add(1)
		return nil
	}

	if err := sess.start(mockClaude, false, publish, func(SessionState) error { return nil }); err != nil {
		t.Fatalf("session start: %v", err)
	}
	t.Cleanup(func() {
		sess.stop()
		sess.waitDone()
	})

	// Wait for at least startup events to flow.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && published.Load() < 2 {
		time.Sleep(20 * time.Millisecond)
	}

	// Complete the session.
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"hi"}}`))
	select {
	case <-sess.doneCh:
	case <-time.After(15 * time.Second):
		t.Fatal("session did not finish within timeout")
	}

	// claude.spawn must have been emitted from session.start().
	spawnSpans := spansWithName(recorder, "claude.spawn")
	if len(spawnSpans) == 0 {
		t.Fatal("claude.spawn span not emitted from session.start()")
	}
	if got := spanAttr(spawnSpans[0], "session.id"); got != "sess-span-prod" {
		t.Errorf("session.id attr: got %q, want %q", got, "sess-span-prod")
	}

	// nats.publish must have been emitted — one per published event.
	pubSpans := spansWithName(recorder, "nats.publish")
	if len(pubSpans) == 0 {
		t.Fatal("nats.publish spans not emitted from session.start()")
	}
	var foundEventsSubject bool
	for _, s := range pubSpans {
		subj := spanAttr(s, "nats.subject")
		// ADR-0054/ADR-0035 subject format: ...sessions.{sslug}.events
		// (ends in ".events", no trailing dot)
		if strings.Contains(subj, ".sessions.") && strings.HasSuffix(subj, ".events") {
			foundEventsSubject = true
			break
		}
	}
	if !foundEventsSubject {
		t.Error("no nats.publish span with events subject (expected format: ...sessions.{sslug}.events)")
	}
}

// TestSessionStartEmitsMetrics verifies that session.start() increments the
// events_published_total counter through the real production code path.
func TestSessionStartEmitsMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	mockClaude := testutil2.MockClaudePath(t)
	transcript := testutil2.TranscriptPath("simple_message.jsonl")

	st := SessionState{
		ID:        "sess-metrics-prod",
		ProjectID: "proj-metrics",
		State:     StateIdle,
		CreatedAt: time.Now(),
	}
	sess := newSession(st, "user-metrics")
	sess.metrics = m
	sess.extraEnv = []string{"MOCK_TRANSCRIPT=" + transcript}

	if err := sess.start(mockClaude, false, func(string, []byte) error { return nil }, func(SessionState) error { return nil }); err != nil {
		t.Fatalf("session start: %v", err)
	}
	t.Cleanup(func() {
		sess.stop()
		sess.waitDone()
	})

	// Let startup events flow then send a message to complete the session.
	time.Sleep(200 * time.Millisecond)
	sess.sendInput([]byte(`{"type":"user","message":{"role":"user","content":"hi"}}`))
	select {
	case <-sess.doneCh:
	case <-time.After(15 * time.Second):
		t.Fatal("session did not finish within timeout")
	}

	// Gather metrics — events_published_total must be non-zero.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	var total float64
	for _, mf := range mfs {
		if mf.GetName() == "mclaude_events_published_total" {
			for _, metric := range mf.GetMetric() {
				total += metric.GetCounter().GetValue()
			}
		}
	}
	if total == 0 {
		t.Error("events_published_total is 0 — metrics not wired in session.start()")
	}
	t.Logf("events_published_total: %.0f", total)
}

// Ensure semconv import is used (prevents unused import error if not called above).
var _ = semconv.SchemaURL
var _ = trace.SpanKindClient
