# ADR 0000: Record architecture decisions

**Status:** Proposed (2026-08-06)

## Context
Didebaan makes significant architectural decisions that shape the collector. We
want the reasoning behind each one to be durable and reviewable rather than
implicit, so that anyone reading the code later can see why it is the way it is.

## Decision
We use Architecture Decision Records. Each ADR is a numbered Markdown file in
`adr/` with Status, Context, Decision, Consequences. ADRs are immutable once
Accepted; a later ADR can supersede an earlier one.

## Consequences
Decisions are auditable and onboardable. Changing a decision is itself a
recorded, deliberate act.
