package main

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"github.com/yaad-index/didebaan/internal/exporter"
	"github.com/yaad-index/didebaan/internal/genai"
	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// instrumentationScope names Didebaan as the source of the telemetry it emits.
const instrumentationScope = "github.com/yaad-index/didebaan"

// newSink returns a Sink that maps each normalized event onto all three
// OpenTelemetry signals (ADR 0002) through providers: a trace span carrying the
// gen_ai.* attributes, the GenAI metric instruments (token usage + operation
// duration), and a structured log record (the activity feed). The genai package
// owns every attribute and instrument name; this only wires the providers to it.
func newSink(providers *exporter.Providers) (didebaan.Sink, error) {
	tracer := providers.Tracer.Tracer(instrumentationScope)
	logger := providers.Logger.Logger(instrumentationScope)
	instruments, err := genai.NewInstruments(providers.Meter.Meter(instrumentationScope))
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context, e didebaan.Event) error {
		// Traces: one span per operation, spanning its duration.
		_, span := tracer.Start(ctx, genai.SpanName(e),
			trace.WithTimestamp(e.Timestamp),
			trace.WithAttributes(genai.Attributes(e)...),
		)
		span.End(trace.WithTimestamp(e.Timestamp.Add(e.Duration)))

		// Metrics: token usage + operation duration.
		instruments.Record(ctx, e)

		// Logs: a structured activity-feed record.
		logger.Emit(ctx, genai.LogRecord(e))

		return nil
	}, nil
}
