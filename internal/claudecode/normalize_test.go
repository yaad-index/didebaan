package claudecode

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/yaad-index/didebaan/internal/receiver"
	"github.com/yaad-index/didebaan/pkg/didebaan"
)

func str(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}

func num(f float64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: f}}
}

func i64(n int64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: n}}
}

func kv(k string, v *commonpb.AnyValue) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: v}
}

// running starts an adapter with a collecting sink and returns both. The adapter
// is push-driven, so it has to be running before any record can be consumed.
func running(t *testing.T, cfg map[string]any) (*Adapter, *[]didebaan.Event) {
	t.Helper()
	ad, err := New(cfg)
	require.NoError(t, err)
	a := ad.(*Adapter)

	var got []didebaan.Event
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = a.Run(ctx, func(_ context.Context, e didebaan.Event) error {
			got = append(got, e)
			return nil
		})
		close(done)
	}()
	// Wait for Run to install the sink.
	require.Eventually(t, func() bool {
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.sink != nil
	}, time.Second, time.Millisecond)

	t.Cleanup(func() { cancel(); <-done })
	return a, &got
}

func resource(attrs ...*commonpb.KeyValue) *resourcepb.Resource {
	return &resourcepb.Resource{Attributes: attrs}
}

func apiRequestLog(attrs ...*commonpb.KeyValue) receiver.LogRecord {
	return receiver.LogRecord{
		Resource: resource(kv("service.instance.id", str("host-one"))),
		Log: &logspb.LogRecord{
			EventName:    eventAPIRequest,
			TimeUnixNano: uint64(time.Unix(1757000000, 0).UnixNano()),
			Attributes:   attrs,
		},
	}
}

// TestIdentityAttributesAreDroppedByDefault is the privacy boundary of
// ADR 0008 §6. user.email and user.id in particular are documented as never
// gated by the agent's own content-redaction settings, so a collector that
// honoured only content redaction would re-export a personal email address on
// every record while reporting its privacy posture as satisfied.
func TestIdentityAttributesAreDroppedByDefault(t *testing.T) {
	a, got := running(t, nil)

	require.NoError(t, a.ConsumeLog(context.Background(), apiRequestLog(
		kv("user.email", str("someone@example.invalid")),
		kv("user.id", str("uid-1")),
		kv("user.account_uuid", str("acct-uuid")),
		kv("user.account_id", str("acct-1")),
		kv("organization.id", str("org-1")),
		kv("terminal.type", str("xterm")),
		kv("session.id", str("sess-1")),
	)))

	require.Len(t, *got, 1)
	attrs := (*got)[0].Attributes
	for _, dropped := range []string{"user.email", "user.id", "user.account_uuid", "user.account_id", "organization.id", "terminal.type"} {
		assert.NotContains(t, attrs, dropped, "identity attribute %q must not survive the boundary", dropped)
	}
	assert.Equal(t, "sess-1", attrs[AttrSessionID], "session.id carries no personal content and is what makes records groupable")
}

// TestIdentityForwardingIsAnExplicitOptIn: the operator can have per-user
// attribution by asking for it. The default is the other way because a personal
// email address exported by default cannot be un-exported.
func TestIdentityForwardingIsAnExplicitOptIn(t *testing.T) {
	a, got := running(t, map[string]any{cfgForwardIdentity: true})

	require.NoError(t, a.ConsumeLog(context.Background(), apiRequestLog(
		kv("user.email", str("someone@example.invalid")),
	)))

	require.Len(t, *got, 1)
	assert.Equal(t, "someone@example.invalid", (*got)[0].Attributes["user.email"])
}

// TestUnknownConfigKeyIsAnError: a misspelled privacy setting that reads as
// "off" while the operator believes it is "on" is the failure worth being loud
// about.
func TestUnknownConfigKeyIsAnError(t *testing.T) {
	_, err := New(map[string]any{"forward_identityy": true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown configuration key")
}

// TestSubagentNameIsRenamed. Claude Code's agent.name is the subagent within a
// session, not the machine running the agent. Carried through under a name
// containing "agent" it invites exactly the reading that it identifies which
// agent produced the record — which is what Instance is for.
func TestSubagentNameIsRenamed(t *testing.T) {
	a, got := running(t, nil)

	require.NoError(t, a.ConsumeLog(context.Background(), apiRequestLog(
		kv("agent.name", str("Explore")),
	)))

	require.Len(t, *got, 1)
	attrs := (*got)[0].Attributes
	assert.Equal(t, "Explore", attrs[AttrSubagentName])
	assert.NotContains(t, attrs, "agent.name")
	assert.NotEqual(t, "Explore", (*got)[0].Instance, "the subagent name must never become the instance identity")
}

// TestInstanceComesFromTheResource.
func TestInstanceComesFromTheResource(t *testing.T) {
	a, got := running(t, nil)

	require.NoError(t, a.ConsumeLog(context.Background(), apiRequestLog()))

	require.Len(t, *got, 1)
	assert.Equal(t, "host-one", (*got)[0].Instance)
}

// TestInstanceFallsBackToHostNameNotServiceName is the distinction that keeps
// the fallback from reproducing the very failure it exists to prevent: every
// host running the same agent reports the same service.name, so falling back to
// it would yield a value that looks like an instance identity and silently
// merges the whole fleet into one series.
func TestInstanceFallsBackToHostNameNotServiceName(t *testing.T) {
	a, got := running(t, nil)

	rec := receiver.LogRecord{
		Resource: resource(
			kv("service.name", str("claude-code")),
			kv("host.name", str("host-two")),
		),
		Log: &logspb.LogRecord{EventName: eventAPIRequest, TimeUnixNano: 1},
	}
	require.NoError(t, a.ConsumeLog(context.Background(), rec))

	require.Len(t, *got, 1)
	assert.Equal(t, "host-two", (*got)[0].Instance)
	assert.NotEqual(t, "claude-code", (*got)[0].Instance)
}

// TestMissingInstanceIsLoud: an unset instance identity must be visible in the
// data and on stderr, not blend in as an empty dimension that merges every
// machine into one series.
func TestMissingInstanceIsLoud(t *testing.T) {
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	t.Cleanup(func() { stderr = old })

	a, got := running(t, nil)

	rec := receiver.LogRecord{
		Resource: resource(kv("service.name", str("claude-code"))),
		Log:      &logspb.LogRecord{EventName: eventAPIRequest, TimeUnixNano: 1},
	}
	require.NoError(t, a.ConsumeLog(context.Background(), rec))

	require.Len(t, *got, 1)
	assert.Equal(t, InstanceUnset, (*got)[0].Instance)
	assert.NotEmpty(t, (*got)[0].Instance, "an empty instance would merge silently; the value must be conspicuous")
	assert.Contains(t, buf.String(), "OTEL_RESOURCE_ATTRIBUTES", "the warning must name the variable an operator has to set")
}

// TestAPIRequestCarriesTheMeasurement: the model call is where tokens, cost,
// duration and model arrive together.
func TestAPIRequestCarriesTheMeasurement(t *testing.T) {
	a, got := running(t, nil)

	require.NoError(t, a.ConsumeLog(context.Background(), apiRequestLog(
		kv("model", str("claude-opus-5")),
		kv("duration_ms", num(1500)),
		kv("cost_usd", num(0.042)),
		kv("input_tokens", i64(100)),
		kv("output_tokens", i64(250)),
	)))

	require.Len(t, *got, 1)
	e := (*got)[0]
	assert.Equal(t, operationChat, e.Operation)
	assert.Equal(t, "claude-opus-5", e.RequestModel)
	assert.Equal(t, 1500*time.Millisecond, e.Duration)
	require.NotNil(t, e.CostUSD)
	assert.InDelta(t, 0.042, *e.CostUSD, 1e-9)
	require.NotNil(t, e.Usage.InputTokens)
	assert.Equal(t, int64(100), *e.Usage.InputTokens)
	require.NotNil(t, e.Usage.OutputTokens)
	assert.Equal(t, int64(250), *e.Usage.OutputTokens)
	assert.Equal(t, System, e.System)
	assert.Equal(t, Name, e.Agent)
}

// TestPreAggregatedTokenAndCostMetricsAreNotRecorded pins the double-count
// decision.
//
// The agent reports the same model call twice: as an api_request event, and
// folded into the token.usage / cost.usage sums. Mapping both would record every
// token twice and the result would look entirely healthy — a plausible number,
// roughly double, with nothing to contradict it. This is the metrics-side twin
// of ADR 0008 §4's span/event suppression, and the ADR has no row for it yet.
func TestPreAggregatedTokenAndCostMetricsAreNotRecorded(t *testing.T) {
	a, got := running(t, nil)

	for _, name := range []string{metricTokenUsage, metricCostUsage} {
		rec := receiver.MetricRecord{
			Resource: resource(kv("service.instance.id", str("host-one"))),
			Metric: &metricspb.Metric{
				Name: name,
				Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
					DataPoints: []*metricspb.NumberDataPoint{{
						TimeUnixNano: 1,
						Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 999},
					}},
				}},
			},
		}
		require.NoError(t, a.ConsumeMetric(context.Background(), rec))
	}

	assert.Empty(t, *got, "the pre-aggregated token and cost metrics must not be re-recorded; the event path is the measurement")
}

// TestOtherMetricsBecomeActivityEvents: everything that is not a restatement of
// the event path still has to reach the activity feed, per data point, so
// per-point recency survives normalization.
func TestOtherMetricsBecomeActivityEvents(t *testing.T) {
	a, got := running(t, nil)

	rec := receiver.MetricRecord{
		Resource: resource(kv("service.instance.id", str("host-one"))),
		Metric: &metricspb.Metric{
			Name: metricActiveTime,
			Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
				DataPoints: []*metricspb.NumberDataPoint{
					{TimeUnixNano: uint64(time.Unix(1757000000, 0).UnixNano()), Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 12.5}},
					{TimeUnixNano: uint64(time.Unix(1757000060, 0).UnixNano()), Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 30}},
				},
			}},
		},
	}
	require.NoError(t, a.ConsumeMetric(context.Background(), rec))

	require.Len(t, *got, 2, "one event per data point, or per-point timing is lost")
	assert.Equal(t, metricActiveTime, (*got)[0].Operation)
	assert.Equal(t, "host-one", (*got)[0].Instance)
	assert.NotEqual(t, (*got)[0].Timestamp, (*got)[1].Timestamp, "each data point keeps its own timestamp")
}

// TestUnknownTimestampDoesNotBecome1970: a record stamped at the Unix epoch
// would sort and display as real data.
func TestUnknownTimestampDoesNotBecome1970(t *testing.T) {
	assert.True(t, timeFromUnixNano(0).IsZero())
	assert.False(t, timeFromUnixNano(uint64(time.Unix(1757000000, 0).UnixNano())).IsZero())
}

// TestRecordOutsideRunIsRefused: an event arriving with no sink installed must
// report that it had nowhere to go rather than be dropped.
func TestRecordOutsideRunIsRefused(t *testing.T) {
	ad, err := New(nil)
	require.NoError(t, err)
	a := ad.(*Adapter)

	err = a.ConsumeLog(context.Background(), apiRequestLog())
	require.ErrorIs(t, err, errNotRunning)
}

// --- Wire-shape invariants, verified against a live agent (Claude Code 2.1.258) ---
//
// These pin what the emitter actually sends, as opposed to how its telemetry is
// described. They are grouped because they share a failure mode: each mismatch
// between the described shape and the sent shape loses data silently, with the
// collector's own counters still reporting health.

// TestEventsAreClaimedByScopeNotNamespace guards the load-bearing one.
//
// The agent emits log events with NO namespace: "api_request", not
// "claude_code.api_request". A namespace claim matches none of them — and the
// event path is where tokens and cost are measured, since the pre-aggregated
// metrics are deliberately not recorded. Claiming events by namespace therefore
// yields no token or cost data at all, while metric ingest counters stay
// healthy and nothing reports a gap.
func TestEventsAreClaimedByScopeNotNamespace(t *testing.T) {
	c := (&Adapter{}).Claim()

	assert.Contains(t, c.ScopePrefixes, ScopePrefix,
		"log events carry no namespace, so the scope is what identifies them")
	assert.NotContains(t, c.EventPrefixes, Namespace,
		"claiming bare event names by the metric namespace matches nothing")

	// The scope on the agent's log events, verbatim from the wire.
	assert.True(t, strings.HasPrefix("com.anthropic.claude_code.events", ScopePrefix),
		"the events scope must be claimed by the same prefix as the metrics scope")
}

// TestAPIRequestEventNameIsBare pins the name as the emitter sends it.
func TestAPIRequestEventNameIsBare(t *testing.T) {
	assert.Equal(t, "api_request", eventAPIRequest)
	assert.NotContains(t, eventAPIRequest, "claude_code")
}

// TestDurationArrivesAsIntOrString: the live agent emits duration_ms as an int
// on some events and as a string on others, within a single session. Accepting
// only the numeric forms drops the duration on the string ones, and drops it
// silently — an unreported duration records nothing, which is a legitimate
// state, so nothing looks wrong.
func TestDurationArrivesAsIntOrString(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  *commonpb.AnyValue
	}{
		{"int", i64(1567)},
		{"string", str("1567")},
		{"double", num(1567)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, got := running(t, nil)
			require.NoError(t, a.ConsumeLog(context.Background(), apiRequestLog(kv("duration_ms", tc.val))))
			require.Len(t, *got, 1)
			assert.Equal(t, 1567*time.Millisecond, (*got)[0].Duration)
		})
	}
}

// TestTokenCountsAcceptStringForm, for the same reason.
func TestTokenCountsAcceptStringForm(t *testing.T) {
	a, got := running(t, nil)
	require.NoError(t, a.ConsumeLog(context.Background(), apiRequestLog(
		kv("input_tokens", str("899")),
		kv("output_tokens", str("4")),
	)))
	require.Len(t, *got, 1)
	require.NotNil(t, (*got)[0].Usage.InputTokens)
	assert.Equal(t, int64(899), *(*got)[0].Usage.InputTokens)
	require.NotNil(t, (*got)[0].Usage.OutputTokens)
	assert.Equal(t, int64(4), *(*got)[0].Usage.OutputTokens)
}

// TestNonNumericStringIsNotAZero: a string that is not a number must report
// "unreported" rather than contribute a false 0 to a running cost total.
func TestNonNumericStringIsNotAZero(t *testing.T) {
	a, got := running(t, nil)
	require.NoError(t, a.ConsumeLog(context.Background(), apiRequestLog(kv("cost_usd", str("n/a")))))
	require.Len(t, *got, 1)
	assert.Nil(t, (*got)[0].CostUSD)
}

// TestMetricsOnlyConfigurationIsLoud covers the quietest failure this design
// has. With the agent exporting metrics but not logs, the pre-aggregated token
// and cost sums are skipped (the event path is the measurement) and the event
// path is not running — so no token or cost figure is produced at all, while
// records keep arriving and every other metric still populates.
func TestMetricsOnlyConfigurationIsLoud(t *testing.T) {
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	t.Cleanup(func() { stderr = old })

	a, got := running(t, nil)

	agg := func(name string) receiver.MetricRecord {
		return receiver.MetricRecord{
			Resource: resource(kv("service.instance.id", str("host-one"))),
			Metric: &metricspb.Metric{
				Name: name,
				Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
					DataPoints: []*metricspb.NumberDataPoint{{
						TimeUnixNano: 1,
						Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 10},
					}},
				}},
			},
		}
	}

	for i := 0; i < metricsOnlyGrace; i++ {
		require.NoError(t, a.ConsumeMetric(context.Background(), agg(metricTokenUsage)))
	}

	assert.Empty(t, *got, "the aggregates are still not recorded")
	assert.Contains(t, buf.String(), "NO token or cost data",
		"the metrics-only configuration must announce itself")
	assert.Contains(t, buf.String(), "OTEL_LOGS_EXPORTER",
		"the warning must name the setting that fixes it")
}

// TestMetricsOnlyWarningDoesNotCryWolf: a metrics export legitimately arrives
// before the first model call on a freshly started agent. A warning that fires
// then is one nobody reads by the time it is true.
func TestMetricsOnlyWarningDoesNotCryWolf(t *testing.T) {
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	t.Cleanup(func() { stderr = old })

	a, _ := running(t, nil)

	// One aggregate arrives first, then the event path proves it is alive.
	rec := receiver.MetricRecord{
		Resource: resource(kv("service.instance.id", str("host-one"))),
		Metric: &metricspb.Metric{
			Name: metricTokenUsage,
			Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
				DataPoints: []*metricspb.NumberDataPoint{{TimeUnixNano: 1}},
			}},
		},
	}
	require.NoError(t, a.ConsumeMetric(context.Background(), rec))
	require.NoError(t, a.ConsumeLog(context.Background(), apiRequestLog()))

	for i := 0; i < metricsOnlyGrace+2; i++ {
		require.NoError(t, a.ConsumeMetric(context.Background(), rec))
	}

	assert.NotContains(t, buf.String(), "NO token or cost data",
		"a healthy configuration must not be warned about")
}
