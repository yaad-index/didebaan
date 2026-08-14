# ADR 0008: Claude Code ingest is an embedded OTLP receiver

**Status:** Proposed (2026-08-14)

## Context
Claude Code exports OpenTelemetry natively across all three signals when
telemetry is enabled, configured through the standard `OTEL_*` environment
variables. The adapter shipped with the scaffold is a stub, so the real ingest
path is still open.

There are two ways to get an agent's activity into the collector: read the
agent's own artifacts (session transcripts, log files), or receive what the agent
already exports. Reading transcripts would re-derive — less faithfully — data the
agent already emits in a standard format, and would couple Didebaan to a private
on-disk layout that carries no stability contract. Transcript scraping is the
right tool only for an agent that offers nothing better.

Choosing the receiver still leaves four things unsettled: where the receiver
lives, which signals v1 accepts, how cost is expressed when the semantic
conventions have no cost metric, and what the receiver listens on.

## Decision

**1. The Claude Code adapter is an embedded OTLP receiver, not a log or
transcript tailer.** The collector exposes an OTLP endpoint (gRPC and
HTTP/protobuf); the agent is pointed at it with `OTEL_EXPORTER_OTLP_ENDPOINT`.
The adapter normalizes the agent's native namespace into the internal event model
and then into `gen_ai.*` (ADR 0002), and re-exports downstream (ADR 0004).

**2. The receiver is embedded in each collector, not one shared central
receiver.** Each machine's collector receives locally and re-exports to a central
destination. A central-only receiver would require every agent to be
network-reachable from one place, would make the per-agent adapter boundary of
ADR 0003 largely notional, and would lose all local activity whenever the central
sink is unreachable.

**3. The receiver binds to loopback by default.** Listening on all interfaces is
an explicit opt-in. A collector that defaults to `0.0.0.0` on a developer machine
is an accidental exposure, and the minimal-surface posture of ADR 0007 should
hold for the ingest side too.

**4. v1 ingests metrics and logs/events; traces stay behind a flag.** Traces are
beta in the agent and require their own opt-in, so their shape may change.
Gating them keeps the normalized schema stable in v1 while still allowing the
highest-fidelity source to be switched on deliberately.

**5. Cost is emitted in a Didebaan-owned extension namespace.** The `gen_ai`
semantic conventions define no cost metric, and inventing a name inside the
standard namespace would collide with whatever is standardized later. When a
standard cost metric exists, Didebaan emits the standard name and keeps the
extension as an alias for one minor cycle, then drops it.

**6. Content stays redacted by default.** The collector must never require the
agent's prompt or response logging to be switched on in order to function.
Capturing content is an explicit operator choice, never a Didebaan prerequisite.

## Consequences
- No dependency on any private transcript format, and ingest inherits OTLP's own
  compatibility guarantees rather than needing a bespoke parser per agent version.
- Receiving is inherently read-only, so ADR 0007 holds here with no extra
  machinery.
- A listening port becomes part of the deployment surface. It needs a documented
  default and a documented way to change it; the loopback default keeps the
  out-of-the-box case safe.
- The cost metric will need a rename when the conventions catch up. The
  one-minor-cycle alias bounds that migration instead of leaving it open-ended.
- Two ingest paths (with and without traces) must be tested. That cost is
  accepted in exchange for a stable v1 schema.
- Agents that export nothing will still need a different adapter shape. This ADR
  decides the Claude Code path, not a universal ingest rule.
