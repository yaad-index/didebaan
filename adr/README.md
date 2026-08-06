# Architecture Decision Records

Didebaan records its significant decisions as ADRs. Each file is one decision:
its context, the decision, and the consequences. ADRs are immutable once
Accepted — to change one, add a new ADR that supersedes it.

These records (0000–0007) capture the initial design of Didebaan as an
agent-agnostic telemetry collector: what it is (and is not), the schema and
protocol it adopts, how agents plug in, and the read-only scope of v1.

| ADR | Title |
|---|---|
| [0000](0000-record-architecture-decisions.md) | Record architecture decisions |
| [0001](0001-agent-agnostic-telemetry-collector-not-dashboard.md) | Didebaan is an agent-agnostic telemetry collector, not a dashboard |
| [0002](0002-adopt-opentelemetry-genai-semconv.md) | Adopt the OpenTelemetry GenAI semantic conventions as the schema |
| [0003](0003-per-agent-input-adapters.md) | Per-agent input adapters normalize into `gen_ai.*` |
| [0004](0004-export-otlp-pluggable-downstream.md) | Export via OTLP; storage and dashboards are downstream |
| [0005](0005-go-and-mit.md) | Go (latest stable), MIT license |
| [0006](0006-prefer-established-libraries.md) | Prefer established libraries over reinventing |
| [0007](0007-read-only-collection-v1.md) | Read-only collection in v1; bidirectional control deferred |
