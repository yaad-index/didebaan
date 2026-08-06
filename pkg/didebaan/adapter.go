package didebaan

import "context"

// Sink receives normalized events from an [Adapter]. The collector supplies the
// Sink; an adapter calls it once per event it reads. A Sink may block (for
// example while exporting), and it may return an error, which the adapter
// should propagate.
type Sink func(context.Context, Event) error

// Adapter reads a single AI coding agent's native activity and emits normalized
// [Event]s. Each supported agent (Claude Code, Codex, Aider, …) has its own
// Adapter that understands that agent's telemetry or log format; every Adapter
// produces the same Event shape, which is what makes the collector core
// agent-agnostic (ADR 0003).
//
// Adapters are registered under their [Name] and selected at runtime; see the
// internal adapter registry.
type Adapter interface {
	// Name is the adapter's stable identifier, used to select it from
	// configuration (e.g. "claude-code"). It must match the name the adapter
	// registered under.
	Name() string

	// Run reads the agent's activity and emits each normalized event to sink,
	// blocking until ctx is cancelled or an unrecoverable error occurs. It
	// returns ctx.Err() on clean shutdown. Callers run Run in its own
	// goroutine.
	Run(ctx context.Context, sink Sink) error
}
