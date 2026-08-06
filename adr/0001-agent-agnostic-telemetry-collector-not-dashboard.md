# ADR 0001: Didebaan is an agent-agnostic telemetry collector, not a dashboard

**Status:** Proposed (2026-08-06)

## Context
AI coding agents (Claude Code, Codex, Aider, and others) each produce activity
in their own shape — some export OpenTelemetry natively, others write logs or
session files. There is value in observing that activity: token usage, latency,
model calls, operation counts. But "observing agent activity" spans several
distinct concerns — collection, storage, visualization, and, eventually,
acting on what is observed. Bundling them into one product couples an
open, reusable data pipeline to opinionated storage and UI choices.

## Decision
**Didebaan is a collector: the collector is the product.** Its single
responsibility is to read an AI coding agent's activity, normalize and enrich it
into a common schema (ADR 0002), and export it over a standard protocol (ADR
0004). It is explicitly **not** a dashboard, **not** a storage layer, **not** a
gamified/RPG front-end, and **not** a control channel back to the agent (ADR
0007).

Visualization, storage, and any "fun layer" are **separate downstream
consumers** of the open data Didebaan emits. They live in other repositories and
depend on Didebaan's output, never the reverse. Keeping this repo to the
collector keeps the emitted data open and reusable: anything that speaks the
export protocol can consume it.

## Consequences
- A sharp scope boundary: features that store, render, or gamify telemetry are
  out of this repo by definition, which keeps review decisions simple.
- The emitted data is the contract. Downstream consumers integrate against the
  export, not against Didebaan's internals.
- Didebaan stays small and composable — a single binary in a pipeline — rather
  than a monolith that owns the whole observability stack.
