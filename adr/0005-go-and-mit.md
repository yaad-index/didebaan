# ADR 0005: Go (latest stable), MIT license

**Status:** Proposed (2026-08-06)

## Context
We need a language and license for an open-source, single-binary telemetry
collector that ships every input adapter in one artifact (ADR 0003) and speaks
OTLP (ADR 0004). It should be trivial to deploy alongside an AI coding agent on
a developer's machine or a build host.

## Decision
**Go** (track the latest stable release, currently **Go 1.26**), module
`github.com/yaad-index/didebaan`, license **MIT**. Layout:
- `cmd/didebaan/` — a thin CLI (parsing and wiring only).
- `internal/` — private packages: the adapter registry, the `gen_ai` mapping,
  the OTLP exporter wiring, and the per-agent adapters.
- `pkg/didebaan/` — the public, embeddable API: the adapter interface and the
  normalized event type.
- `adr/` — these records.

The mature OpenTelemetry Go SDK (ADR 0002, 0006) makes Go a natural fit.

Policy: **track the latest stable Go**, bumping `go.mod` as new releases land
(Go supports the latest two releases).

## Consequences
- A single static binary, easy to run next to any agent.
- Idiomatic use of the OpenTelemetry Go SDK.
- A conventional, predictable layout that a Go contributor can navigate without
  relearning where things live.
