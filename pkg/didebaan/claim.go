package didebaan

// Claim declares which incoming telemetry an adapter takes responsibility for
// when the collector receives OTLP from an agent (ADR 0008). One receiver
// serves every adapter in the process, so the receiver has to decide which
// adapter each arriving record belongs to; a Claim is that declaration, made by
// the adapter rather than inferred by the receiver.
//
// Records that match no Claim are dropped and counted. They are never handed to
// an arbitrary adapter: guessing an owner would let one agent's telemetry be
// normalized by another agent's rules, which is silent corruption rather than a
// visible gap.
//
// The three fields exist separately because the signals do not share one naming
// scheme. Metrics and log events carry a name in the agent's own namespace, so a
// name prefix identifies them. A span carries no such name — its identity comes
// from the instrumentation scope that produced it — and an agent's spans may
// legitimately carry gen_ai.* attributes rather than the agent's own namespace,
// so a name prefix cannot match them at all.
type Claim struct {
	// MetricPrefixes claims every metric whose name starts with one of these
	// (e.g. "claude_code.").
	MetricPrefixes []string

	// EventPrefixes claims every log record whose event name starts with one
	// of these.
	//
	// ⚠️ An agent's log events are not guaranteed to be namespaced. Claude Code
	// emits bare names — "api_request", "user_prompt" — with no prefix at all,
	// so a prefix claim alone matches none of them. Use ScopePrefixes for such
	// an agent and keep this for agents that do namespace their events; a bare
	// name like "api_request" is far too generic to claim across adapters.
	EventPrefixes []string

	// ScopePrefixes claims every metric and log record whose instrumentation
	// scope name starts with one of these.
	//
	// The scope is the emitting library's own identity, which makes it the one
	// identifier an agent cannot leave off — unlike a name prefix, which is a
	// convention the agent may simply not follow.
	ScopePrefixes []string

	// SpanScopes claims every span whose instrumentation scope name equals one
	// of these. This is an exact match rather than a prefix: a scope name is
	// the emitting library's identity, not a namespace that nests.
	SpanScopes []string
}

// Claimer is implemented by an [Adapter] that ingests from the collector's OTLP
// receiver. An adapter that reads its agent some other way does not implement
// it, and the receiver will never route anything to that adapter.
type Claimer interface {
	// Claim returns the adapter's claim declaration. It must be constant for
	// the adapter's lifetime: the receiver may read it once at wiring time,
	// and a claim that changed later would silently stop matching records it
	// used to own.
	Claim() Claim
}
