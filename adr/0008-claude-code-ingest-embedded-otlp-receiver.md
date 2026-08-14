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
lives and what it listens on, how it coexists with the ports the collector
already documents, which signals v1 accepts given that the collector already
synthesizes spans of its own, and how cost is expressed when the semantic
conventions define no cost metric.

## Decision

**1. The Claude Code adapter is an embedded OTLP receiver, not a log or
transcript tailer.** The collector exposes an OTLP endpoint (gRPC and
HTTP/protobuf); the agent is pointed at it. The adapter normalizes the agent's
native namespace into the internal event model and then into `gen_ai.*`
(ADR 0002), and re-exports downstream (ADR 0004).

**2. The receiver is owned by the collector, not by each adapter, and there is
one per collector process.** ADR 0003 commits to several adapters running at
once in one process, so a receiver per adapter would have N adapters contending
for one port and failing at run time rather than at config validation.

**Routing is part of this decision, because it defines the adapter interface:**
each adapter declares which incoming telemetry it claims. Metrics and logs are
claimed by namespace prefix (Claude Code claims `claude_code.*`); **spans are
claimed by instrumentation scope**, since a span is neither a metric nor a log
name and the agent's beta spans carry `gen_ai.*` rather than `claude_code.*`.
The receiver dispatches each record to the claiming adapter. Records no adapter
claims are dropped and counted, never guessed at.

**3. The receiver binds to loopback by default, on the conventional OTLP ports**
(`127.0.0.1:4317` gRPC, `127.0.0.1:4318` HTTP). Listening on all interfaces is an
explicit opt-in: defaulting to `0.0.0.0` on a developer machine is an accidental
exposure, and the minimal-surface posture of ADR 0007 should hold for ingest too.

The conventional ports belong to the *receiver* rather than to the downstream
exporter because the agent's defaults are the ones we do not control — an agent
pointed at its own default endpoint must find the collector with no extra
configuration.

⚠️ **Consequently the collector must refuse to start when the resolved export
endpoint equals its own listening address.** The current documentation uses
`localhost:4317` as the export example, which against a conventional receiver
default would make the collector export into its own receiver.

**The constraint binds the *resolved* endpoint, not the documented one**, because
two ways of complying literally still land on the collision:
- Removing the configured default looks like the minimal fix, but an empty
  endpoint falls back to the SDK's environment defaults, and the OTLP default is
  `localhost:4317`. Compliance on paper, loop in practice.
- More likely still, the agent is pointed at the collector *by* exporting
  `OTEL_EXPORTER_OTLP_ENDPOINT`, so any process inheriting that environment
  resolves to the receiver through the same fallback. The loop would then depend
  on how a process was launched rather than on any file anyone reviewed.

A startup refusal turns a silent feedback loop into an error at the one moment
someone is watching.

**4. v1 ingests the agent's metrics and logs/events. Ingesting the agent's own
span tree stays behind a flag,** because those spans are beta in the agent and
behind a separate opt-in there, so their shape can still change.

This gates *ingest only*. It is not a statement that v1 has no traces: the
collector already synthesizes one span per normalized event, unconditionally, and
that continues.

**The duplicate does not come from the ingested spans; it comes from the event
path.** An `api_request` event becomes a normalized event and the sink
synthesizes a span from it. Switch span ingest on and the same call is
represented twice: the agent's own `llm_request` span, plus a synthetic span
built from that call's `api_request` event.

**So suppression is keyed on the operation, not on provenance: synthesis is
skipped for any event describing an operation whose span was ingested**, matched
on the agent's correlation keys (`prompt.id`, `message.uuid`,
`client_request_id`). Keying on provenance instead would be vacuous in exactly
the state it exists for — under pass-through, ingested spans never become events,
so nothing would ever be "derived from" them and nothing would be suppressed.

**5. Cost is emitted as `didebaan.cost.usage`, in the project's own `didebaan.*`
namespace** (the collector already emits `didebaan.agent`). The GenAI semantic
conventions define no cost metric, and the ad-hoc name in common use sits *inside*
the standard namespace — precisely the collision this avoids.

When a standard cost metric exists, Didebaan emits the standard name. **Pre-1.0,
the extension name may be dropped in any minor release, called out in the
changelog, since minor is the breaking vehicle before 1.0. Post-1.0 it is kept as
an alias for one minor cycle.**

**6. Content stays redacted by default.** The collector must never require the
agent's prompt or response logging to be switched on in order to function.
Capturing content is an explicit operator choice, never a Didebaan prerequisite.

## Consequences
- No dependency on any private transcript format, and ingest inherits OTLP's own
  compatibility guarantees rather than needing a bespoke parser per agent version.
- Receiving is inherently read-only, so ADR 0007 holds here with no extra
  machinery.
- The adapter interface gains a claim declaration (scope/namespace), and the
  collector gains a dispatch step and a counter for unclaimed records.
- A listening port becomes part of the deployment surface, and the documented
  downstream export default has to move off `localhost:4317` in the same change.
- Two ingest paths (with and without agent spans) must be tested, including the
  suppression rule that keeps one operation from being represented twice.
- The cost metric will need a rename when the conventions catch up; pre-1.0 that
  is a changelog entry rather than a migration window.
- Agents that export nothing will still need a different adapter shape. This ADR
  decides the Claude Code path, not a universal ingest rule.

## Not decided here
- **Durable local buffering.** Export today is the SDK's in-memory batch and
  periodic processors: bounded, dropped on overflow, lost on exit. A collector
  therefore does not survive a downstream outage any better than the agent would;
  that is a real gap, but it is orthogonal to where the receiver lives and wants
  its own ADR rather than being smuggled in as a justification here.
