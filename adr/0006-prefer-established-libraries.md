# ADR 0006: Prefer established libraries over reinventing

**Status:** Proposed (2026-08-06)

## Context
Didebaan sits on top of a well-developed ecosystem: the OpenTelemetry protocol,
its Go SDK, its exporters, and its semantic conventions. It also does commodity
work — CLI/argument parsing and layered configuration. Hand-rolling any of this
would duplicate maintained, vetted code and add risk for no gain.

## Decision
**Build on well-established, maintained, MIT-compatible libraries wherever they
fit.** Specifically:
- **Do not reinvent the telemetry protocol or its SDK.** Use the OpenTelemetry Go
  SDK and its OTLP exporters for traces, metrics, and logs, and the `gen_ai.*`
  semantic conventions (ADR 0002). Didebaan does not define its own wire format.
- **CLI/argument parsing and configuration** (file < env < flag layering) are
  library work — reach for the vetted library rather than hand-roll.

Reserve in-house code for **Didebaan's own logic**: the adapter registry, each
adapter's normalization of its agent's native format into the common event, and
the mapping from that event onto OTel signals. Prefer the standard library where
it suffices; reach for a vetted third-party library rather than reimplement; but
avoid trivial micro-dependencies that add supply-chain surface for little gain.

## Consequences
- Less protocol risk and a faster path to a correct v1.
- Notable dependency choices are themselves decisions and should be recorded.
- All dependencies must be license-compatible with MIT.
- We accept some supply-chain surface as the price of building on the
  ecosystem, and keep that surface deliberate rather than incidental.
