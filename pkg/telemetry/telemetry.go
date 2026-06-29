// Package telemetry wires OpenTelemetry tracing for the orchestrator (DoD: OTLP
// traces emitted and verified). It exposes a tiny tracing facade so the rest of
// the code stays decoupled from the OTel SDK: Init configures an OTLP exporter
// when an endpoint is set (OTEL_EXPORTER_OTLP_ENDPOINT or Config.Endpoint), and
// otherwise installs a no-op tracer so the orchestrator runs with zero overhead
// and no collector dependency on a dev box.
package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "aibox-orch"

// Config controls telemetry initialization.
type Config struct {
	// Endpoint is the OTLP gRPC collector endpoint (e.g. "localhost:4317").
	// If empty, telemetry falls back to a no-op tracer.
	Endpoint string
	// Insecure disables TLS to the collector (common for local collectors).
	Insecure bool
}

// Shutdown flushes and closes the tracer provider. Safe to call on a no-op setup.
type Shutdown func(context.Context) error

// Init installs a global tracer provider. With no endpoint it installs a no-op
// provider and returns a no-op Shutdown, so callers can always defer Shutdown.
func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	if cfg.Endpoint == "" {
		// No-op: leave the default global tracer (no exporter) in place.
		return func(context.Context) error { return nil }, nil
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exp, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName(serviceName),
	))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(ctx)
	}, nil
}

// Tracer returns the named tracer from the global provider.
func Tracer() trace.Tracer { return otel.Tracer(serviceName) }

// StartSpan starts a span named op with optional string key/value attributes
// (passed as alternating key, value pairs). Returns the new context and span.
func StartSpan(ctx context.Context, op string, kv ...string) (context.Context, trace.Span) {
	var attrs []attribute.KeyValue
	for i := 0; i+1 < len(kv); i += 2 {
		attrs = append(attrs, attribute.String(kv[i], kv[i+1]))
	}
	return Tracer().Start(ctx, op, trace.WithAttributes(attrs...))
}
