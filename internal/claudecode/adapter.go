// Package claudecode is the input adapter for the Claude Code agent — the first
// adapter (ADR 0003), because Claude Code exports OpenTelemetry natively and so
// exercises the shortest path from an agent to gen_ai.*. It registers itself
// with the adapter registry on import.
//
// This is a stub: it satisfies the adapter contract and registers under
// "claude-code", but does not yet ingest Claude Code's telemetry. Real ingest
// is a follow-up; wiring the adapter in now lets the CLI, registry, and export
// path be exercised end to end.
package claudecode

import (
	"context"

	"github.com/yaad-index/didebaan/internal/adapter"
	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// Name is the registry name this adapter is selected by.
const Name = "claude-code"

func init() {
	adapter.Register(Name, New)
}

// Adapter reads Claude Code's native OpenTelemetry activity and emits normalized
// events. The stub holds no state yet.
type Adapter struct{}

// New constructs the Claude Code adapter. cfg is reserved for adapter-specific
// settings (e.g. where to read Claude Code's telemetry from) and is currently
// unused.
func New(_ map[string]any) (didebaan.Adapter, error) {
	return &Adapter{}, nil
}

// Name identifies the adapter.
func (a *Adapter) Name() string { return Name }

// Run blocks until ctx is cancelled, returning ctx.Err(). The real
// implementation will read Claude Code's OTLP/log activity and call sink once
// per normalized event; until then it emits nothing.
func (a *Adapter) Run(ctx context.Context, _ didebaan.Sink) error {
	<-ctx.Done()
	return ctx.Err()
}
