# Didebaan

**Open, agent-agnostic telemetry collector for AI coding agents.**

Didebaan reads an AI coding agent's activity, normalizes it into the
OpenTelemetry **GenAI semantic conventions** (`gen_ai.*`), and exports it over
**OTLP** to any standard collector. It turns whatever your agent does —
token usage, model calls, operation latency — into open, standard telemetry that
any OpenTelemetry-compatible backend can store and visualize.

Didebaan is a **collector, and only a collector**. Storage, dashboards, and any
"fun layer" on top are separate downstream consumers of the open data it emits —
they are not built here. See [ADR 0001](adr/0001-agent-agnostic-telemetry-collector-not-dashboard.md).

## Why

- **Agent-agnostic.** Each agent plugs in through its own input adapter; the core
  and every downstream consumer see one normalized shape, no matter which agent
  produced it.
- **Standard schema, standard protocol.** `gen_ai.*` over OTLP means no bespoke
  format to learn and no vendor lock-in — it drops straight into the
  OpenTelemetry ecosystem (Prometheus/Grafana/Loki, SigNoz, and others).
- **Composable.** A single static binary in a pipeline, not a monolith that owns
  your whole observability stack.

## How it works

```
AI coding agent ──▶ input adapter ──▶ gen_ai.* normalized event ──▶ OTLP ──▶ downstream collector / store / dashboards
   (Claude Code,      (per agent;         (traces, metrics, logs)                  (Prometheus, SigNoz, …)
    Codex, Aider…)      ADR 0003)
```

- **Input adapters** ([ADR 0003](adr/0003-per-agent-input-adapters.md)) each know
  one agent's native format and emit the same normalized event. **Claude Code**
  is the first adapter (it exports OpenTelemetry natively). Adapters are selected
  at runtime — `didebaan adapters` lists what a binary supports.
- **The schema** ([ADR 0002](adr/0002-adopt-opentelemetry-genai-semconv.md)) is the
  OpenTelemetry GenAI semantic conventions, modeled across all three signals:
  traces, metrics, and logs.
- **Export** ([ADR 0004](adr/0004-export-otlp-pluggable-downstream.md)) is OTLP;
  where telemetry lands is your choice, configured by the OTLP endpoint.

v1 is **read-only**: Didebaan observes and exports, and never writes back to the
agent ([ADR 0007](adr/0007-read-only-collection-v1.md)).

## Status

Early scaffold. The collector's structure, adapter interface, registry, and OTLP
export wiring are in place, with a Claude Code adapter stub; real ingest and the
full signal mapping land in follow-ups.

## Quick start

```sh
go build ./cmd/didebaan
./didebaan adapters                                  # list available adapters
./didebaan --otlp-endpoint localhost:4317 --otlp-insecure collect
```

See [INSTALL.md](INSTALL.md) for building and running (including Docker) and
[USAGE.md](USAGE.md) for the commands and configuration.

## Design

Decisions are recorded as [Architecture Decision Records](adr/). Start with
[ADR 0001](adr/0001-agent-agnostic-telemetry-collector-not-dashboard.md).

## License

[MIT](LICENSE).
