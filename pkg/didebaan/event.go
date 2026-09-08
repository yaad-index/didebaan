package didebaan

import "time"

// Event is a single normalized agent-activity event, expressed in the terms of
// the OpenTelemetry GenAI semantic conventions (gen_ai.*). An [Adapter]
// translates its agent's native activity into Events; the collector maps each
// Event onto OpenTelemetry traces, metrics, and logs (ADR 0002). Event is the
// stable, agent-independent shape that the whole pipeline is built around, so
// downstream signal mapping never has to know which agent a record came from.
//
// Fields correspond to gen_ai.* attributes. Optional string fields are empty
// when the source agent did not report them; the mapping omits empty
// attributes. Anything captured that has no dedicated field belongs in
// [Event.Attributes].
type Event struct {
	// Timestamp is when the operation occurred.
	Timestamp time.Time

	// Agent identifies the source AI coding agent (the adapter's origin, e.g.
	// "claude-code"). It is emitted as a per-event attribute (didebaan.agent),
	// not as a process-level resource attribute: one collector process may run
	// several adapters at once (ADR 0003), so the agent is a property of the
	// event, not of the process. Downstream consumers group telemetry by it.
	Agent string

	// Instance identifies which running agent produced the activity — the
	// fleet member, not the kind of agent. Agent above says "claude-code" for
	// every machine running Claude Code; Instance is what separates one of them
	// from another, and it is the dimension a per-agent liveness view is built
	// on.
	//
	// It comes from the OpenTelemetry Resource (service.instance.id, falling
	// back to service.name), because no agent's own telemetry carries it: an
	// agent has no concept of the fleet it belongs to. Operators set it per host
	// through OTEL_RESOURCE_ATTRIBUTES.
	//
	// It is deliberately bounded — one value per running collector — which is
	// what makes it safe as a metric dimension. See [Event.Attributes] for the
	// unbounded identifiers that stay off the dimension list.
	Instance string

	// System is the GenAI system (gen_ai.system), e.g. "anthropic".
	System string

	// Operation is the GenAI operation name (gen_ai.operation.name), e.g.
	// "chat".
	Operation string

	// RequestModel is the model the operation requested
	// (gen_ai.request.model).
	RequestModel string

	// ResponseModel is the model that actually served the response
	// (gen_ai.response.model); it may differ from RequestModel.
	ResponseModel string

	// Usage holds token accounting (gen_ai.usage.*).
	Usage Usage

	// Duration is the wall-clock latency of the operation, when known. Zero
	// means unknown.
	Duration time.Duration

	// CostUSD is what the operation cost, in US dollars, or nil if the agent
	// did not report a cost. The GenAI semantic conventions define no cost
	// metric, so this is emitted in Didebaan's own namespace (ADR 0008 §5)
	// rather than under a gen_ai.* name that upstream may later define
	// differently.
	CostUSD *float64

	// Attributes carries any additional gen_ai.* (or agent-specific) key/values
	// the adapter captured but that have no dedicated field. Keys should be the
	// fully-qualified attribute names (e.g. "gen_ai.request.temperature").
	//
	// ⚠️ Attributes reach spans and log records but deliberately never become
	// metric dimensions: an unbounded value here (a session id, a request id)
	// would multiply metric series without limit, and the resulting cost lands
	// on the downstream time-series database long after the change that caused
	// it. Anything that must be a dimension needs a field of its own, and needs
	// to be bounded to earn it.
	Attributes map[string]any
}

// Dollars returns a pointer to n, for setting a known [Event.CostUSD]. A genuine
// zero is Dollars(0); leave the field nil to mean "unreported".
func Dollars(n float64) *float64 { return &n }

// Usage is per-operation token accounting, mapping to the gen_ai.usage.*
// attributes. Each count is a pointer so that "unreported" (nil) is distinct
// from a genuine zero: the struct zero-value reports nothing, which is the safe
// default — an adapter that never learned the token counts leaves them nil and
// the mapping omits the usage attributes rather than asserting a false 0. Use
// [Tokens] to set a known count.
type Usage struct {
	// InputTokens is the number of prompt/input tokens
	// (gen_ai.usage.input_tokens), or nil if unreported.
	InputTokens *int64

	// OutputTokens is the number of completion/output tokens
	// (gen_ai.usage.output_tokens), or nil if unreported.
	OutputTokens *int64
}

// Tokens returns a pointer to n, for setting a known [Usage] count:
//
//	Usage{InputTokens: didebaan.Tokens(42)}
//
// A genuine zero is Tokens(0); leave the field nil to mean "unreported".
func Tokens(n int64) *int64 { return &n }
