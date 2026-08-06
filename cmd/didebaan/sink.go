package main

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"github.com/yaad-index/didebaan/internal/exporter"
	"github.com/yaad-index/didebaan/internal/genai"
	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// newSink returns a Sink that records each normalized event as a span carrying
// its gen_ai.* attributes, exported through providers. This is the v1 wiring
// from a normalized event to an OpenTelemetry signal; the metric and log signals
// (ADR 0002) are wired in a follow-up.
func newSink(providers *exporter.Providers) didebaan.Sink {
	tracer := providers.Tracer.Tracer("github.com/yaad-index/didebaan")
	return func(ctx context.Context, e didebaan.Event) error {
		_, span := tracer.Start(ctx, spanName(e),
			trace.WithTimestamp(e.Timestamp),
			trace.WithAttributes(genai.Attributes(e)...),
		)
		span.End(trace.WithTimestamp(e.Timestamp.Add(e.Duration)))
		return nil
	}
}

// spanName derives a span name from the event's operation, falling back to a
// generic label when the adapter did not report one.
func spanName(e didebaan.Event) string {
	if e.Operation != "" {
		return e.Operation
	}
	return "gen_ai.operation"
}
