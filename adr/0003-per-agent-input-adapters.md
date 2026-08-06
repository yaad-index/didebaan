# ADR 0003: Per-agent input adapters normalize into `gen_ai.*`

**Status:** Proposed (2026-08-06)

## Context
Each AI coding agent exposes its activity differently: Claude Code exports
OpenTelemetry natively, while others emit JSON logs, session transcripts, or
their own metrics endpoints. Didebaan must turn all of these into the one
normalized schema (ADR 0002) without letting any single agent's format leak into
the core.

We also have to choose *how* adapters are selected. One option is a **build-tag**
plugin pattern that fixes the choice at compile time and ships a single
implementation in the binary — appropriate when the selection is a
security-sensitive policy fixed at deploy time. Didebaan's situation is
different: an operator may run several agents on one machine and want to collect
from whichever are present, chosen at runtime.

## Decision
**Each agent has its own input adapter** that knows that agent's native format
and produces the normalized event. Adapters share one interface (defined in the
public `pkg/didebaan` API): given a context and a sink, read the agent's activity
and emit normalized events until cancelled.

**Claude Code is the first adapter**, because it exports OpenTelemetry natively
and so exercises the shortest path from agent to `gen_ai.*`. Codex, Aider, and
others follow as additional adapters.

**Adapters are selected at runtime via a registry, not at compile time via build
tags.** Each adapter registers a factory under a stable name; the CLI selects one
(or more, later) by name from config. A runtime registry is the right trade-off
*here* — as opposed to a single-implementation compile-time plugin — because:
- The set of agents present is an operator/runtime fact, not a build-time policy.
- Adapters are additive and non-exclusive: shipping all of them in one binary is
  desirable, and a registry makes "list what's available" trivial (`didebaan
  adapters`).
- Adapters are input readers, not a security boundary, so there is no isolation
  argument for compiling only one in.

## Consequences
- Adding an agent is a self-contained package that registers itself; the core
  and other adapters are untouched.
- One binary supports every known agent; the operator picks at runtime.
- The registry is a small piece of in-house wiring we own (consistent with ADR
  0006, which reserves in-house code for Didebaan's own logic).
