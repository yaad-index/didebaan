// Package genai maps Didebaan's normalized events onto the OpenTelemetry GenAI
// semantic conventions (gen_ai.*), the common schema Didebaan adopts (ADR 0002).
// It is the single place that knows the attribute names, so adapters stay
// concerned only with reading their agent and the exporter stays concerned only
// with transport.
//
// This is the v1 mapping stub: it covers the core fields of a normalized event.
// The full mapping across all three signals (the metric instruments and the log
// record shapes, beyond the span attributes here) lands in a follow-up.
package genai

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"

	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// GenAI semantic-convention attribute keys. These track the OpenTelemetry GenAI
// conventions; some are still incubating upstream, which is why they are pinned
// here as named constants rather than pulled from a semconv package that may
// rename them between releases (ADR 0002, 0006).
const (
	AttrSystem        = "gen_ai.system"
	AttrOperationName = "gen_ai.operation.name"
	AttrRequestModel  = "gen_ai.request.model"
	AttrResponseModel = "gen_ai.response.model"
	AttrUsageInput    = "gen_ai.usage.input_tokens"
	AttrUsageOutput   = "gen_ai.usage.output_tokens"

	// AttrAgent is Didebaan's resource-level attribute identifying the source
	// AI coding agent. It is not part of the gen_ai.* set; it lets downstream
	// consumers group telemetry by which agent produced it (ADR 0003).
	AttrAgent = "didebaan.agent"
)

// Attributes translates the core fields of a normalized event into GenAI
// semantic-convention attributes. Empty string fields and unknown (negative)
// token counts are omitted so the output carries only what the adapter actually
// observed. Extra attributes carried on the event are appended as-is, on the
// assumption the adapter already used fully-qualified attribute keys.
func Attributes(e didebaan.Event) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 8+len(e.Attributes))

	if e.Agent != "" {
		attrs = append(attrs, attribute.String(AttrAgent, e.Agent))
	}
	if e.System != "" {
		attrs = append(attrs, attribute.String(AttrSystem, e.System))
	}
	if e.Operation != "" {
		attrs = append(attrs, attribute.String(AttrOperationName, e.Operation))
	}
	if e.RequestModel != "" {
		attrs = append(attrs, attribute.String(AttrRequestModel, e.RequestModel))
	}
	if e.ResponseModel != "" {
		attrs = append(attrs, attribute.String(AttrResponseModel, e.ResponseModel))
	}
	if e.Usage.InputTokens >= 0 {
		attrs = append(attrs, attribute.Int64(AttrUsageInput, e.Usage.InputTokens))
	}
	if e.Usage.OutputTokens >= 0 {
		attrs = append(attrs, attribute.Int64(AttrUsageOutput, e.Usage.OutputTokens))
	}

	for k, v := range e.Attributes {
		attrs = append(attrs, anyAttr(k, v))
	}
	return attrs
}

// anyAttr renders an arbitrary extra value as a typed attribute, falling back
// to a string for types the conventions don't cover.
func anyAttr(k string, v any) attribute.KeyValue {
	switch t := v.(type) {
	case string:
		return attribute.String(k, t)
	case bool:
		return attribute.Bool(k, t)
	case int:
		return attribute.Int(k, t)
	case int64:
		return attribute.Int64(k, t)
	case float64:
		return attribute.Float64(k, t)
	default:
		return attribute.String(k, fmt.Sprintf("%v", t))
	}
}
