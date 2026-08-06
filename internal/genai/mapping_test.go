package genai

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

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
		Usage:         didebaan.Usage{InputTokens: didebaan.Tokens(12), OutputTokens: didebaan.Tokens(34)},
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
	m := attrMap(Attributes(didebaan.Event{System: "anthropic"}))

	_, hasOp := m[AttrOperationName]
	_, hasReq := m[AttrRequestModel]
	assert.False(t, hasOp)
	assert.False(t, hasReq)
	assert.Equal(t, "anthropic", m[AttrSystem].AsString())
}

// Carry-over #2: a zero-value / unreported-token event must omit the usage
// attributes rather than assert a false 0.
func TestAttributesOmitsUnreportedTokens(t *testing.T) {
	m := attrMap(Attributes(didebaan.Event{})) // zero value: nil token pointers

	_, hasIn := m[AttrUsageInput]
	_, hasOut := m[AttrUsageOutput]
	assert.False(t, hasIn, "unreported input tokens must be omitted, not 0")
	assert.False(t, hasOut, "unreported output tokens must be omitted, not 0")
}

// A genuine zero (explicitly set) must still be emitted as 0.
func TestAttributesGenuineZeroTokens(t *testing.T) {
	m := attrMap(Attributes(didebaan.Event{
		Usage: didebaan.Usage{InputTokens: didebaan.Tokens(0), OutputTokens: didebaan.Tokens(0)},
	}))

	require.Contains(t, m, AttrUsageInput)
	require.Contains(t, m, AttrUsageOutput)
	assert.Equal(t, int64(0), m[AttrUsageInput].AsInt64())
	assert.Equal(t, int64(0), m[AttrUsageOutput].AsInt64())
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

// Metric dimensions must exclude the measured token counts and the free-form
// (possibly high-cardinality) Attributes map.
func TestDimensionAttributesExcludesTokensAndExtras(t *testing.T) {
	m := attrMap(DimensionAttributes(didebaan.Event{
		Agent:      "claude-code",
		System:     "anthropic",
		Operation:  "chat",
		Usage:      didebaan.Usage{InputTokens: didebaan.Tokens(12)},
		Attributes: map[string]any{"gen_ai.request.temperature": 0.7},
	}))

	assert.Equal(t, "claude-code", m[AttrAgent].AsString())
	assert.Equal(t, "anthropic", m[AttrSystem].AsString())
	assert.Equal(t, "chat", m[AttrOperationName].AsString())
	assert.NotContains(t, m, AttrUsageInput)
	assert.NotContains(t, m, "gen_ai.request.temperature")
}

func TestLogRecord(t *testing.T) {
	ts := time.Unix(100, 0)
	r := LogRecord(didebaan.Event{
		Timestamp: ts,
		Agent:     "claude-code",
		Operation: "chat",
		Usage:     didebaan.Usage{InputTokens: didebaan.Tokens(7)},
	})

	assert.Equal(t, ts, r.Timestamp())
	assert.Equal(t, "chat", r.Body().AsString())

	got := map[string]attribute.Value{}
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		got[string(kv.Key)] = kv.Value
		return true
	})
	assert.Equal(t, "claude-code", got[AttrAgent].AsString())
	assert.Equal(t, "chat", got[AttrOperationName].AsString())
	assert.Equal(t, int64(7), got[AttrUsageInput].AsInt64())
}

func TestLogRecordDefaultBody(t *testing.T) {
	r := LogRecord(didebaan.Event{}) // no operation
	assert.Equal(t, "gen_ai.operation", r.Body().AsString())
}

// collectMetrics records one event through fresh instruments and returns the
// collected metrics by name.
func collectMetrics(t *testing.T, e didebaan.Event) map[string]metricdata.Metrics {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	instruments, err := NewInstruments(provider.Meter("test"))
	require.NoError(t, err)
	instruments.Record(context.Background(), e)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

func TestInstrumentsRecordTokenUsageAndDuration(t *testing.T) {
	metrics := collectMetrics(t, didebaan.Event{
		Agent:     "claude-code",
		Operation: "chat",
		Usage:     didebaan.Usage{InputTokens: didebaan.Tokens(12), OutputTokens: didebaan.Tokens(34)},
		Duration:  2 * time.Second,
	})

	require.Contains(t, metrics, MetricTokenUsage)
	require.Contains(t, metrics, MetricOpDuration)

	// Token usage: one histogram data point per direction, tagged by token type.
	hist, ok := metrics[MetricTokenUsage].Data.(metricdata.Histogram[int64])
	require.True(t, ok, "token usage must be an int64 histogram")
	byType := map[string]int64{}
	for _, dp := range hist.DataPoints {
		tt, present := dp.Attributes.Value(attribute.Key(AttrTokenType))
		require.True(t, present, "each token measurement carries gen_ai.token.type")
		byType[tt.AsString()] = dp.Sum
	}
	assert.Equal(t, int64(12), byType[tokenTypeInput])
	assert.Equal(t, int64(34), byType[tokenTypeOutput])

	// Duration: one float64 histogram data point in seconds.
	dur, ok := metrics[MetricOpDuration].Data.(metricdata.Histogram[float64])
	require.True(t, ok, "duration must be a float64 histogram")
	require.Len(t, dur.DataPoints, 1)
	assert.Equal(t, 2.0, dur.DataPoints[0].Sum)
}

// Unreported tokens and an unknown (zero) duration must record nothing.
func TestInstrumentsRecordOmitsUnknown(t *testing.T) {
	metrics := collectMetrics(t, didebaan.Event{Agent: "claude-code", Operation: "chat"})

	assert.NotContains(t, metrics, MetricTokenUsage, "no token measurements ⇒ instrument has no data")
	assert.NotContains(t, metrics, MetricOpDuration, "zero duration ⇒ no duration measurement")
}
