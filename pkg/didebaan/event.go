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
	// "claude-code"). It is carried as a resource-level attribute so downstream
	// consumers can group telemetry by agent.
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
// attributes. A negative value is treated as unknown.
type Usage struct {
	// InputTokens is the number of prompt/input tokens
	// (gen_ai.usage.input_tokens).
	InputTokens int64

	// OutputTokens is the number of completion/output tokens
	// (gen_ai.usage.output_tokens).
	OutputTokens int64
}
