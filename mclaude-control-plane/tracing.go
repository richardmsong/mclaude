package main

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "mclaude-control-plane"

// tracer is the package-level OpenTelemetry tracer.
var tracer = otel.Tracer(tracerName)

// InitTracing sets up the global OpenTelemetry tracer with the given exporter.
// In production, pass an OTLP exporter (batched). In tests, pass
// tracetest.NewInMemoryExporter() with sync=true for immediate span delivery.
func InitTracing(exporter sdktrace.SpanExporter) *sdktrace.TracerProvider {
	return InitTracingWithSync(exporter, false)
}

// InitTracingWithSync sets up the tracer provider. When sync=true, spans are
// exported synchronously (ideal for tests). When sync=false, they are batched.
func InitTracingWithSync(exporter sdktrace.SpanExporter, sync bool) *sdktrace.TracerProvider {
	var opt sdktrace.TracerProviderOption
	if sync {
		opt = sdktrace.WithSyncer(exporter)
	} else {
		opt = sdktrace.WithBatcher(exporter)
	}
	tp := sdktrace.NewTracerProvider(
		opt,
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tracer = tp.Tracer(tracerName)
	return tp
}

// SpanFromHTTP starts a server-side span for an incoming HTTP request.
// The span is named "{method} {path}" and carries standard HTTP attributes.
func SpanFromHTTP(ctx context.Context, r *http.Request) (context.Context, trace.Span) {
	return tracer.Start(ctx, r.Method+" "+r.URL.Path,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.url", r.URL.String()),
			attribute.String("component", "control-plane"),
		),
	)
}

// SpanDB starts a span for a Postgres operation.
func SpanDB(ctx context.Context, operation string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "db."+operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", operation),
		),
	)
}

// SpanProvisioning starts a span for a provisioning operation (user/project lifecycle).
func SpanProvisioning(ctx context.Context, operation string, userID string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "provision."+operation,
		trace.WithAttributes(
			attribute.String("mclaude.operation", operation),
			attribute.String("mclaude.userId", userID),
			attribute.String("component", "control-plane"),
		),
	)
}
