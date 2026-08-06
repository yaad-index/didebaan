// Package exporter wires Didebaan's OpenTelemetry signal providers to an OTLP
// endpoint (ADR 0004). It constructs the trace, metric, and log providers over
// OTLP/gRPC and hands them back behind a single shutdown, so the rest of the
// collector depends only on the OpenTelemetry provider interfaces and never on
// the transport.
//
// This wires all three signals per ADR 0002; the instruments and spans that
// feed them are produced from normalized events elsewhere.
package exporter

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config selects where and how telemetry is exported. It is the collector's
// main export surface (ADR 0004): everything past the OTLP endpoint is a
// downstream concern.
type Config struct {
	// Endpoint is the OTLP/gRPC endpoint, host:port (e.g.
	// "localhost:4317"). Empty falls back to the OpenTelemetry SDK's own
	// environment-variable defaults.
	Endpoint string

	// Insecure disables transport security (plaintext gRPC). Suitable for a
	// local collector on the loopback interface; leave false to use TLS.
	Insecure bool

	// ServiceVersion stamps the exported resource's service.version.
	ServiceVersion string
}

// Providers bundles the three OpenTelemetry signal providers wired to OTLP,
// along with a single Shutdown that flushes and closes all of them.
type Providers struct {
	Tracer *sdktrace.TracerProvider
	Meter  *sdkmetric.MeterProvider
	Logger *sdklog.LoggerProvider
}

// New constructs the trace, metric, and log providers exporting over OTLP/gRPC
// to cfg.Endpoint, sharing one resource that identifies this collector. The
// caller owns the returned Providers and must call Shutdown.
func New(ctx context.Context, cfg Config) (*Providers, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("didebaan"),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, err
	}

	traceExp, err := otlptracegrpc.New(ctx, traceOpts(cfg)...)
	if err != nil {
		return nil, err
	}
	metricExp, err := otlpmetricgrpc.New(ctx, metricOpts(cfg)...)
	if err != nil {
		return nil, err
	}
	logExp, err := otlploggrpc.New(ctx, logOpts(cfg)...)
	if err != nil {
		return nil, err
	}

	return &Providers{
		Tracer: sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExp),
			sdktrace.WithResource(res),
		),
		Meter: sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
			sdkmetric.WithResource(res),
		),
		Logger: sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
			sdklog.WithResource(res),
		),
	}, nil
}

// Shutdown flushes and shuts down every provider, joining any errors so one
// failure does not skip the others.
func (p *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(
		p.Tracer.Shutdown(ctx),
		p.Meter.Shutdown(ctx),
		p.Logger.Shutdown(ctx),
	)
}

func traceOpts(cfg Config) []otlptracegrpc.Option {
	opts := []otlptracegrpc.Option{}
	if cfg.Endpoint != "" {
		opts = append(opts, otlptracegrpc.WithEndpoint(cfg.Endpoint))
	}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	return opts
}

func metricOpts(cfg Config) []otlpmetricgrpc.Option {
	opts := []otlpmetricgrpc.Option{}
	if cfg.Endpoint != "" {
		opts = append(opts, otlpmetricgrpc.WithEndpoint(cfg.Endpoint))
	}
	if cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	return opts
}

func logOpts(cfg Config) []otlploggrpc.Option {
	opts := []otlploggrpc.Option{}
	if cfg.Endpoint != "" {
		opts = append(opts, otlploggrpc.WithEndpoint(cfg.Endpoint))
	}
	if cfg.Insecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	}
	return opts
}
