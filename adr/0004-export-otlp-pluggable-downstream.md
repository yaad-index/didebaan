# ADR 0004: Export via OTLP; storage and dashboards are downstream

**Status:** Proposed (2026-08-06)

## Context
Having normalized agent activity into `gen_ai.*` (ADR 0002), Didebaan must get
that data out. It could write to a database, push to a specific vendor, or expose
its own query API — but any of those would re-couple the collector to a storage
and visualization choice that ADR 0001 deliberately pushed downstream.

## Decision
**Export via OTLP (the OpenTelemetry Protocol) to any standard collector or
backend.** OTLP is the ecosystem's transport; emitting it means Didebaan feeds,
without bespoke glue, into the OpenTelemetry Collector and from there into any
OSS store and dashboard stack — Prometheus/Grafana/Loki, SigNoz, or others.

**Storage and dashboards are downstream and pluggable, and are not built in this
repo.** Didebaan's output is OTLP; where it lands is the operator's choice,
configured by the OTLP endpoint. Swapping Grafana for SigNoz is a downstream
change with no code change here.

## Consequences
- Didebaan integrates with the existing observability ecosystem for free; no
  storage or UI code to maintain.
- The OTLP endpoint (and its transport/security settings) is the main export
  configuration surface.
- Choosing where telemetry is stored and rendered is entirely an operator
  decision, made outside this repo.
