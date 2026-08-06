package genai

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"

	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// attrMap collapses a KeyValue slice into a map for order-independent assertions.
func attrMap(kvs []attribute.KeyValue) map[string]attribute.Value {
	m := make(map[string]attribute.Value, len(kvs))
	for _, kv := range kvs {
		m[string(kv.Key)] = kv.Value
	}
	return m
}

func TestAttributesCoreFields(t *testing.T) {
	e := didebaan.Event{
		Timestamp:     time.Unix(0, 0),
		Agent:         "claude-code",
		System:        "anthropic",
		Operation:     "chat",
		RequestModel:  "model-a",
		ResponseModel: "model-b",
		Usage:         didebaan.Usage{InputTokens: 12, OutputTokens: 34},
	}
	m := attrMap(Attributes(e))

	assert.Equal(t, "claude-code", m[AttrAgent].AsString())
	assert.Equal(t, "anthropic", m[AttrSystem].AsString())
	assert.Equal(t, "chat", m[AttrOperationName].AsString())
	assert.Equal(t, "model-a", m[AttrRequestModel].AsString())
	assert.Equal(t, "model-b", m[AttrResponseModel].AsString())
	assert.Equal(t, int64(12), m[AttrUsageInput].AsInt64())
	assert.Equal(t, int64(34), m[AttrUsageOutput].AsInt64())
}

func TestAttributesOmitsEmptyStrings(t *testing.T) {
	// Only System set; the other string fields must not appear.
	m := attrMap(Attributes(didebaan.Event{System: "anthropic"}))

	_, hasOp := m[AttrOperationName]
	_, hasReq := m[AttrRequestModel]
	assert.False(t, hasOp)
	assert.False(t, hasReq)
	assert.Equal(t, "anthropic", m[AttrSystem].AsString())
}

func TestAttributesOmitsUnknownTokenCounts(t *testing.T) {
	// Negative counts mean "unknown" and must be omitted.
	m := attrMap(Attributes(didebaan.Event{
		Usage: didebaan.Usage{InputTokens: -1, OutputTokens: -1},
	}))

	_, hasIn := m[AttrUsageInput]
	_, hasOut := m[AttrUsageOutput]
	assert.False(t, hasIn)
	assert.False(t, hasOut)
}

func TestAttributesExtraTypes(t *testing.T) {
	m := attrMap(Attributes(didebaan.Event{
		Attributes: map[string]any{
			"gen_ai.request.temperature": 0.7,
			"gen_ai.request.stream":      true,
			"gen_ai.request.max_tokens":  1024,
		},
	}))

	assert.Equal(t, 0.7, m["gen_ai.request.temperature"].AsFloat64())
	assert.Equal(t, true, m["gen_ai.request.stream"].AsBool())
	assert.Equal(t, int64(1024), m["gen_ai.request.max_tokens"].AsInt64())
}
