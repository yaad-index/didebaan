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
each adapter declares which incoming telemetry it claims. **Every signal is
claimed by instrumentation scope; a name prefix is an additional claim an
adapter may declare, never its only one.** The receiver dispatches each record
to the claiming adapter. Records no adapter claims are dropped and counted,
never guessed at.

🚨 **This rule was widened after the implementation disproved the original one,
which said metrics and logs are claimed by namespace prefix.** Measured against
a live agent (Claude Code 2.1.258): metrics do carry `claude_code.*`, but **log
events carry no namespace at all** — `api_request`, `user_prompt`,
`assistant_response`, `mcp_server_connection`. A namespace claim over those
matches nothing.

**The consequence is why this is recorded rather than quietly fixed in code: the
event path is where token and cost figures live**, so a prefix-only claim would
have dropped every one of them while the receiver's ingest counters, the
activity feed and the metric dimensions all read healthy. **The spec was wrong
in the direction that produces no error.**

⚠️ **Claiming those events by their bare names instead is worse than losing
them.** `api_request` is a name any agent might emit, so a bare-name claim would
route another agent's events into Claude Code's normalization rules — silent
corruption in place of a visible gap.

🔑 **The scope is the emitting library's own identity, which makes it the one
identifier an agent cannot leave off**; a name prefix is a convention the agent
may simply not follow, and here it does not. **Span claims match the scope
exactly rather than by prefix**, because a scope name is an identity and not a
namespace that nests.

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
someone is watching. **The comparison is on resolved addresses, not strings:**
`localhost:4317`, `127.0.0.1:4317` and `[::1]:4317` are one socket and compare
unequal as text, and once the all-interfaces opt-in is used the right relation is
containment rather than equality.

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
skipped for any event describing an operation whose span was ingested.** The
matching key is **per operation class**, because no single key joins every pair:

| Operation class | Joining key | Notes |
|---|---|---|
| Model call | `client_request_id` | On `llm_request` spans and `api_request` events. |
| Tool call | `gen_ai.tool.call.id` / `tool_use_id` | `client_request_id` is on `tool.execution` spans but **not** on `tool_result` / `tool_decision` events, so it cannot join this pair. |
| *(future classes)* | *(unfilled)* | A new operation type must appear here as a blank row before it is assumed covered. |

The table is deliberate rather than a list: **a rule with one key looks complete,
whereas a table with one row filled and the rest empty does not.** `prompt.id` and
`message.uuid` are events-only and never appear on spans, so they cannot serve as
joining keys at all.

### The second double-count axis: pre-aggregated metrics against per-operation events

The table above governs *ingested span against synthesized span*. A second,
independent double count exists on the metric side, and it is decided the same
way — **once, in favour of the per-operation record.**

| Double count | Records dropped | Kept because |
|---|---|---|
| Span against synthetic span | Synthesis skipped per the table above | The ingested span is the agent's own timing. |
| `claude_code.token.usage` / `claude_code.cost.usage` against `api_request` | **The pre-aggregated metrics** | The event carries the dimensions the aggregate has already collapsed, and the event model is per-operation to begin with. |

**The aggregate is a restatement of the events, so recording both would count
every token twice.**

⚠️ **This creates one configuration that produces no token or cost data at all,
and it is not an obviously broken one:** an operator who enables the agent's
metrics but not its logs (`OTEL_LOGS_EXPORTER` unset) gets aggregates that are
deliberately not recorded and an event path that is not running. **Records
arrive, dimensions populate, the activity feed fills from the other metrics, and
only the two numbers anyone actually asked for are missing** — the quietest
possible failure.

**So the adapter must warn when it has skipped pre-aggregated token or cost
metrics and has never seen a log event**, naming the environment variable that
fixes it. A grace count before warning keeps a slow-starting log exporter from
tripping it. **The warning is part of the decision, not an implementation
detail: the decision to drop the aggregates is what creates the silent state, so
the same decision owes it a voice.**

Keying on provenance instead would be vacuous in exactly the state it exists for
— under pass-through, ingested spans never become events, so nothing would ever
be "derived from" them and nothing would be suppressed.

**Intended edge:** the span carries `client_request_id` from the final attempt
while `api_request` events are per-attempt, so on a retried call the earlier
attempts still synthesize spans. That is deliberate: a retried attempt is a real,
separately-timed operation that the ingested span does not represent.

**5. Cost is emitted as `didebaan.cost.usage`, in the project's own `didebaan.*`
namespace** (the collector already emits `didebaan.agent`). The GenAI semantic
conventions define no cost metric, and the ad-hoc name in common use sits *inside*
the standard namespace — precisely the collision this avoids.

When a standard cost metric exists, Didebaan emits the standard name. **Pre-1.0,
the extension name may be dropped in any minor release, called out in the
changelog, since minor is the breaking vehicle before 1.0. Post-1.0 it is kept as
an alias for one minor cycle.**

**6. Content stays redacted by default, and identity attributes are dropped at
the adapter boundary by default.** The collector must never require the agent's
prompt or response logging to be switched on in order to function. Capturing
content is an explicit operator choice, never a Didebaan prerequisite.

⚠️ **Redacting content is not sufficient, because identity does not travel in the
content.** The agent attaches a standard attribute set to every metric, event and
span whenever telemetry is enabled: `user.email`, `user.id`, `user.account_uuid`,
`user.account_id`, `organization.id`, `terminal.type`. **`user.email` and
`user.id` are documented as never gated** — no content-redaction flag suppresses
them. A collector that only honoured content redaction would therefore ingest a
personal email address on every record and re-export it to an
operator-chosen backend **by default**, while reporting its privacy posture as
satisfied.

**So the adapter drops the identity attributes by default; forwarding them is an
explicit opt-in**, the same shape this decision already uses for content.
**`session.id` is retained**, because it is what makes telemetry groupable and it
carries no personal content.

This applies across all three signals and to v1, not only to the flagged trace
path — the attribute set is shared, so the boundary is the right place for it.

## Consequences
- No dependency on any private transcript format, and ingest inherits OTLP's own
  compatibility guarantees rather than needing a bespoke parser per agent version.
- Receiving is inherently read-only, so ADR 0007 holds here with no extra
  machinery.
- The adapter interface gains a claim declaration (scope, plus optional name
  prefixes), and the collector gains a dispatch step and a counter for unclaimed
  records.
- An agent that namespaces neither its metrics nor its events is still claimable,
  because the scope claim does not depend on the agent following any naming
  convention.
- A listening port becomes part of the deployment surface, and the documented
  downstream export default has to move off `localhost:4317` in the same change.
- Two ingest paths (with and without agent spans) must be tested, including the
  suppression rule that keeps one operation from being represented twice.
- The cost metric will need a rename when the conventions catch up; pre-1.0 that
  is a changelog entry rather than a migration window.
- Dropping identity attributes costs per-user attribution downstream unless the
  operator opts in. That is the intended default: an operator who wants it can
  say so, whereas a personal email exported by default cannot be un-exported.
- Agents that export nothing will still need a different adapter shape. This ADR
  decides the Claude Code path, not a universal ingest rule.

## Not decided here
- **Durable local buffering.** Export today is the SDK's in-memory batch and
  periodic processors: bounded, dropped on overflow, lost on exit. A collector
  therefore does not survive a downstream outage any better than the agent would;
  that is a real gap, but it is orthogonal to where the receiver lives and wants
  its own ADR rather than being smuggled in as a justification here.
