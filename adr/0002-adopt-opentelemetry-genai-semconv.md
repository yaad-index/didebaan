# ADR 0002: Adopt the OpenTelemetry GenAI semantic conventions as the schema

**Status:** Proposed (2026-08-06)

## Context
Didebaan normalizes heterogeneous agents into one schema (ADR 0001). We need to
choose that schema. Inventing a bespoke event format would force every
downstream consumer to learn a Didebaan-specific vocabulary and would duplicate
work the observability ecosystem has already standardized. The OpenTelemetry
project maintains **GenAI semantic conventions** (`gen_ai.*`) precisely for
LLM/agent telemetry — model, operation, token usage, and the like — across all
three OpenTelemetry signals: traces, metrics, and logs.

## Decision
**Adopt the OpenTelemetry GenAI semantic conventions (`gen_ai.*`) as Didebaan's
common schema, and model all three signals — metrics, traces, and logs.** Do
not invent a protocol or an attribute vocabulary. Where an agent exposes
information that has a `gen_ai.*` attribute, we map onto that attribute; where
the conventions are still evolving, we track the upstream convention rather than
fork it.

Concretely:
- **Traces** capture an operation (e.g. a model call) as a span with `gen_ai.*`
  attributes.
- **Metrics** capture counters/histograms (token usage, operation duration).
- **Logs** capture events that are better expressed as structured records.

The internal normalized event (ADR 0003, `pkg/didebaan`) is expressed in these
terms so the mapping onto OTel is direct.

## Consequences
- Downstream consumers are any OpenTelemetry-compatible tool; no bespoke schema
  to learn.
- We inherit the conventions' evolution — including that some `gen_ai.*`
  attributes are still incubating. We accept tracking that churn as the price of
  not forking.
- Modeling all three signals is more work than one, but it is what makes the
  emitted data complete and directly consumable.
