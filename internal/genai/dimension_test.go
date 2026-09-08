package genai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// TestInstanceIsAMetricDimension pins the field that makes a per-agent view
// possible. Without it every machine's telemetry aggregates into one series and
// "which agent is active" cannot be asked.
func TestInstanceIsAMetricDimension(t *testing.T) {
	dims := DimensionAttributes(didebaan.Event{Agent: "claude-code", Instance: "host-one"})

	found := false
	for _, kv := range dims {
		if string(kv.Key) == AttrInstance {
			found = true
			assert.Equal(t, "host-one", kv.Value.AsString())
		}
	}
	require.True(t, found, "%s must be a metric dimension", AttrInstance)
}

// TestUnboundedIdentifiersNeverBecomeDimensions is the guard the whole
// dimension design rests on: an unbounded value carried on an event must reach
// spans and logs and must NOT reach metrics.
//
// This is deliberately asserted on session.id specifically. It is the value
// most likely to be promoted by a well-meaning change — it is the obvious
// grouping key, and promoting it costs nothing visible here, in CI, or in the
// collector. The bill arrives on the downstream time-series database, one
// series per session forever, long after the change that caused it.
func TestUnboundedIdentifiersNeverBecomeDimensions(t *testing.T) {
	e := didebaan.Event{
		Agent:    "claude-code",
		Instance: "host-one",
		Attributes: map[string]any{
			"session.id":        "0f9c2a7e-unbounded",
			"client_request_id": "req-also-unbounded",
		},
	}

	for _, kv := range DimensionAttributes(e) {
		assert.NotContains(t, []string{"session.id", "client_request_id"}, string(kv.Key),
			"an unbounded identifier reached the metric dimensions")
	}

	// The same values must still be present on the span/log side, or the
	// exclusion above would have been achieved by losing them entirely.
	var keys []string
	for _, kv := range Attributes(e) {
		keys = append(keys, string(kv.Key))
	}
	assert.Contains(t, keys, "session.id")
	assert.Contains(t, keys, "client_request_id")
}

// TestCostRecordsOnlyWhenReported checks that an unreported cost asserts
// nothing, rather than contributing a false zero to a running total.
func TestCostRecordsOnlyWhenReported(t *testing.T) {
	withCost := Attributes(didebaan.Event{CostUSD: didebaan.Dollars(0.25)})
	var found bool
	for _, kv := range withCost {
		if string(kv.Key) == AttrCostUSD {
			found = true
			assert.InDelta(t, 0.25, kv.Value.AsFloat64(), 1e-9)
		}
	}
	require.True(t, found, "a reported cost must appear on the span/log attributes")

	for _, kv := range Attributes(didebaan.Event{}) {
		assert.NotEqual(t, AttrCostUSD, string(kv.Key), "an unreported cost must assert nothing")
	}
}
