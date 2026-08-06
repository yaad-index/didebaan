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

	// Attributes carries any additional gen_ai.* (or agent-specific) key/values
	// the adapter captured but that have no dedicated field. Keys should be the
	// fully-qualified attribute names (e.g. "gen_ai.request.temperature").
	Attributes map[string]any
}

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
