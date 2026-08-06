# ADR 0007: Read-only collection in v1; bidirectional control deferred

**Status:** Proposed (2026-08-06)

## Context
Observing an agent naturally suggests acting on what is observed — for example, a
future control channel that could respond to a blocked or stuck agent. That is a
qualitatively different capability from collection: it implies writing back to
the agent, an authority and safety model, and a two-way protocol. Conflating it
with the collector would broaden Didebaan's threat surface and scope from day
one.

## Decision
**v1 is read-only: Didebaan observes and exports, and never writes back to the
agent.** Adapters read an agent's activity; the collector normalizes and exports
it (ADR 0002, 0004). There is no path from Didebaan to the agent.

**The bidirectional respond-to-a-blocked-agent control channel is explicitly
deferred to a later phase** and, if built, will be its own design (its own ADR,
its own authority and safety model) — quite possibly its own repository,
consistent with the downstream-consumer boundary in ADR 0001.

## Consequences
- A minimal, safe surface for v1: a collector cannot alter what it observes.
- The control-channel design is not blocked — it is deliberately postponed until
  its own requirements are worked out.
- Keeping v1 read-only keeps the security review of the collector simple.
