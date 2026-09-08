package receiver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/protobuf/proto"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// fakeConsumer records what the receiver routes to it.
type fakeConsumer struct {
	claim   didebaan.Claim
	metrics []string
	logs    []string
}

func (f *fakeConsumer) Name() string                                  { return "fake" }
func (f *fakeConsumer) Claim() didebaan.Claim                         { return f.claim }
func (f *fakeConsumer) ConsumeSpan(context.Context, SpanRecord) error { return nil }

func (f *fakeConsumer) ConsumeMetric(_ context.Context, rec MetricRecord) error {
	f.metrics = append(f.metrics, rec.Metric.GetName())
	return nil
}

func (f *fakeConsumer) ConsumeLog(_ context.Context, rec LogRecord) error {
	f.logs = append(f.logs, rec.Log.GetEventName())
	return nil
}

// newTestReceiver builds a receiver around one consumer, bypassing New's claim
// validation.
//
// The bypass is deliberate. New refuses a claim that declares no ScopePrefixes
// (ADR 0008 §2), but the dispatch tests below need exactly such a claim: the
// reason the rule exists is that a name-prefix claim silently matches nothing,
// and that is a property of dispatch, not of construction. Routing it through
// New would make the negative untestable and leave the rule asserted only in
// prose. The validation itself is covered by
// TestNewRefusesAClaimWithoutScopePrefixes.
func newTestReceiver(t *testing.T, claim didebaan.Claim) (*Receiver, *fakeConsumer) {
	t.Helper()
	c := &fakeConsumer{claim: claim}
	r, err := New(
		Config{GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0"},
		noop.NewMeterProvider().Meter("test"),
		&scopedConsumer{Consumer: c},
	)
	require.NoError(t, err)
	// Swap the real claim back in now that construction has passed.
	r.consumers = []Consumer{c}
	return r, c
}

// scopedConsumer satisfies New's validation without changing what the wrapped
// consumer claims once dispatch begins.
type scopedConsumer struct{ Consumer }

func (scopedConsumer) Claim() didebaan.Claim {
	return didebaan.Claim{ScopePrefixes: []string{"test.scope"}}
}

// TestNewRefusesAClaimWithoutScopePrefixes covers the rule ADR 0008 §2 states.
//
// It is validated rather than documented because the failure is silent: a
// prefix-only claim dispatches correctly for as long as the agent namespaces
// everything, and stops matching the moment it meets one that does not. The
// records then count as unclaimed while every health counter stays green.
func TestNewRefusesAClaimWithoutScopePrefixes(t *testing.T) {
	c := &fakeConsumer{claim: didebaan.Claim{MetricPrefixes: []string{"claude_code."}}}
	_, err := New(Config{GRPCAddr: "127.0.0.1:0"}, noop.NewMeterProvider().Meter("test"), c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ScopePrefixes")
	assert.Contains(t, err.Error(), "fake", "the error must name the offending adapter")
}

func postProto(t *testing.T, h http.HandlerFunc, msg proto.Message) *httptest.ResponseRecorder {
	t.Helper()
	body, err := proto.Marshal(msg)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/x", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentTypeProtobuf)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func metricsRequest(name string) *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{Name: name}},
			}},
		}},
	}
}

// TestClaimedMetricReachesItsAdapter is the happy path of the dispatch rule.
func TestClaimedMetricReachesItsAdapter(t *testing.T) {
	r, c := newTestReceiver(t, didebaan.Claim{MetricPrefixes: []string{"claude_code."}})

	w := postProto(t, r.handleMetrics, metricsRequest("claude_code.token.usage"))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"claude_code.token.usage"}, c.metrics)
	assert.Equal(t, int64(1), r.Stats().MetricsClaimed.Load())
	assert.Equal(t, int64(0), r.Stats().MetricsUnclaimed.Load())
}

// TestUnclaimedMetricIsCountedNotGuessed: a record no adapter claims must not be
// handed to an arbitrary adapter, and must not vanish silently either. A silent
// drop and a working ingest look identical from outside.
func TestUnclaimedMetricIsCountedNotGuessed(t *testing.T) {
	r, c := newTestReceiver(t, didebaan.Claim{MetricPrefixes: []string{"claude_code."}})

	w := postProto(t, r.handleMetrics, metricsRequest("some_other_agent.tokens"))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, c.metrics, "an unclaimed record must never reach an adapter that did not claim it")
	assert.Equal(t, int64(1), r.Stats().MetricsUnclaimed.Load())
}

// TestJSONIsRefusedExplicitly. OTLP also defines a JSON encoding; accepting the
// request and dropping it would look like working ingest at the sender, so the
// refusal is explicit and carries a status a caller can act on.
func TestJSONIsRefusedExplicitly(t *testing.T) {
	r, _ := newTestReceiver(t, didebaan.Claim{MetricPrefixes: []string{"claude_code."}})

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.handleMetrics(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	assert.Contains(t, w.Body.String(), "application/x-protobuf")
}

// TestContentTypeParametersAreTolerated: a sender may append a charset, and
// rejecting that would refuse a compliant client over punctuation.
func TestContentTypeParametersAreTolerated(t *testing.T) {
	r, c := newTestReceiver(t, didebaan.Claim{MetricPrefixes: []string{"claude_code."}})

	body, err := proto.Marshal(metricsRequest("claude_code.session.count"))
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf; charset=utf-8")
	w := httptest.NewRecorder()
	r.handleMetrics(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, c.metrics, 1)
}

// TestLogEventNameFallsBackToAttribute: which field carries the event name
// depends on the sender's SDK version, so both are matched. Missing this would
// leave every record from an older agent unclaimed, with the counter as the only
// evidence.
func TestLogEventNameFallsBackToAttribute(t *testing.T) {
	r, c := newTestReceiver(t, didebaan.Claim{EventPrefixes: []string{"claude_code."}})

	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{{
					Attributes: []*commonpb.KeyValue{{
						Key:   "event.name",
						Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "claude_code.api_request"}},
					}},
				}},
			}},
		}},
	}
	w := postProto(t, r.handleLogs, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(1), r.Stats().LogsClaimed.Load())
	assert.Len(t, c.logs, 1)
}

// TestGetIsRefused keeps the receiver from answering a browser probe as if it
// had received telemetry.
func TestGetIsRefused(t *testing.T) {
	r, _ := newTestReceiver(t, didebaan.Claim{})
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics", nil)
	w := httptest.NewRecorder()
	r.handleMetrics(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestNoConsumersIsAConfigurationError: a receiver with no consumer would accept
// every record and claim none, which is a collector that appears to work.
func TestNoConsumersIsAConfigurationError(t *testing.T) {
	_, err := New(Config{GRPCAddr: "127.0.0.1:0"}, noop.NewMeterProvider().Meter("test"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no consumers")
}

// TestBothTransportsDisabledIsAConfigurationError.
func TestBothTransportsDisabledIsAConfigurationError(t *testing.T) {
	c := &fakeConsumer{}
	_, err := New(Config{}, noop.NewMeterProvider().Meter("test"), c)
	require.Error(t, err)
}

// TestBareEventNameIsClaimedByScope reproduces the wire shape of a live Claude
// Code agent (2.1.258) exactly: the LogRecord.event_name field is EMPTY, the
// name lives in the event.name attribute, that name carries NO namespace, and
// the instrumentation scope is what identifies the emitter.
//
// Claiming these by the "claude_code." namespace matches none of them and drops
// every event. Because the pre-aggregated token and cost metrics are
// deliberately not recorded, that leaves the collector exporting no token or
// cost data at all while its metric ingest counters look entirely healthy —
// which is why this is asserted end to end rather than on the claim struct.
func TestBareEventNameIsClaimedByScope(t *testing.T) {
	r, c := newTestReceiver(t, didebaan.Claim{
		MetricPrefixes: []string{"claude_code."},
		ScopePrefixes:  []string{"com.anthropic.claude_code"},
	})

	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{{
				Scope: &commonpb.InstrumentationScope{
					Name:    "com.anthropic.claude_code.events",
					Version: "2.1.258",
				},
				LogRecords: []*logspb.LogRecord{{
					// event_name deliberately unset, as the agent sends it.
					Attributes: []*commonpb.KeyValue{{
						Key:   "event.name",
						Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api_request"}},
					}},
				}},
			}},
		}},
	}
	w := postProto(t, r.handleLogs, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(1), r.Stats().LogsClaimed.Load(), "a bare-named event must be claimed via its scope")
	assert.Equal(t, int64(0), r.Stats().LogsUnclaimed.Load())
	assert.Len(t, c.logs, 1)
}

// TestNamespaceClaimAloneWouldNotMatch states the negative directly, so the
// reason the scope claim exists cannot quietly stop being true.
func TestNamespaceClaimAloneWouldNotMatch(t *testing.T) {
	r, _ := newTestReceiver(t, didebaan.Claim{EventPrefixes: []string{"claude_code."}})

	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{{
				Scope: &commonpb.InstrumentationScope{Name: "com.anthropic.claude_code.events"},
				LogRecords: []*logspb.LogRecord{{
					Attributes: []*commonpb.KeyValue{{
						Key:   "event.name",
						Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api_request"}},
					}},
				}},
			}},
		}},
	}
	postProto(t, r.handleLogs, req)

	assert.Equal(t, int64(1), r.Stats().LogsUnclaimed.Load(),
		"claiming bare event names by namespace matches nothing — this is why ScopePrefixes exists")
}
