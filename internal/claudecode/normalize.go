package claudecode

import (
	"context"
	"fmt"
	"strconv"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/yaad-index/didebaan/internal/receiver"
	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// Claude Code metric names this adapter recognises, without the namespace
// prefix.
const (
	metricTokenUsage = Namespace + "token.usage"
	metricCostUsage  = Namespace + "cost.usage"
	metricActiveTime = Namespace + "active_time.total"
)

// Claude Code event names this adapter recognises.
//
// ⚠️ These are bare, with no namespace prefix — verified against a live agent
// (2.1.258), which emits "api_request", not "claude_code.api_request". The
// issue that scoped this work described them as namespaced; the emitter
// disagrees, and the emitter is the authority.
const (
	eventAPIRequest = "api_request"
)

// Attribute keys on the agent's own records.
const (
	ccAttrModel      = "model"
	ccAttrDurationMS = "duration_ms"
	ccAttrCostUSD    = "cost_usd"
	ccAttrInputTok   = "input_tokens"
	ccAttrOutputTok  = "output_tokens"
)

// operationChat is the gen_ai.operation.name for a model call.
const operationChat = "chat"

// ConsumeMetric normalizes one claimed metric.
//
// 🚨 The agent's token.usage and cost.usage metrics are deliberately NOT
// recorded into the gen_ai token and cost instruments, even though they carry
// exactly those numbers.
//
// The agent reports the same model call twice: once as an api_request event
// carrying that call's tokens, cost, model and duration, and once folded into
// these pre-aggregated sums. Mapping both would record every token twice, and
// the result would look entirely healthy — a plausible number, roughly double,
// with nothing in CI or in the collector to contradict it.
//
// This is the metrics-side twin of the span/event double-representation that
// ADR 0008 §4 rules on, and the ADR's suppression table has no row for it. The
// choice made here is that the EVENT is the measurement: it is per-operation,
// it carries the dimensions the aggregate has already collapsed, and the event
// model is per-operation to begin with. The pre-aggregated metric is a restated
// summary of records we already hold.
//
// ⚠️ The cost of this choice, stated rather than discovered: an operator who
// exports the agent's metrics but not its logs gets no token or cost figures at
// all. That configuration is silent today. It is the open question this needs
// deciding on, and it should end up in the ADR as a filled row rather than
// living only here.
func (a *Adapter) ConsumeMetric(ctx context.Context, rec receiver.MetricRecord) error {
	name := rec.Metric.GetName()
	switch name {
	case metricTokenUsage, metricCostUsage:
		// See the doc comment: measured on the event path instead.
		a.warnIfMetricsOnly()
		return nil
	case metricActiveTime:
		return a.emitDataPoints(ctx, rec, name)
	default:
		// Every other agent metric (session.count, commit.count,
		// lines_of_code.count, code_edit_tool.decision, …) is activity rather
		// than a GenAI operation. It becomes an event so the activity feed and
		// the liveness view can see it, with no token or cost claim attached.
		return a.emitDataPoints(ctx, rec, name)
	}
}

// emitDataPoints turns each of a metric's data points into one normalized event.
// A data point is the unit that carries both a timestamp and its own attribute
// set, so one event per data point is what preserves recency and dimensions
// through the normalization — collapsing a metric to a single event would throw
// away exactly the per-point timing a liveness view reads.
func (a *Adapter) emitDataPoints(ctx context.Context, rec receiver.MetricRecord, operation string) error {
	instance := a.instanceFrom(rec.Resource)

	emit := func(ts uint64, attrs []*commonpb.KeyValue, value any) error {
		e := a.newEvent(instance, operation, timeFromUnixNano(ts))
		e.Attributes = a.scrubbedAttrs(attrs)
		if value != nil {
			if e.Attributes == nil {
				e.Attributes = map[string]any{}
			}
			e.Attributes[operation] = value
		}
		return a.emit(ctx, e)
	}

	switch data := rec.Metric.GetData().(type) {
	case *metricspb.Metric_Sum:
		for _, dp := range data.Sum.GetDataPoints() {
			if err := emit(dpTime(dp), dp.GetAttributes(), numberValue(dp)); err != nil {
				return err
			}
		}
	case *metricspb.Metric_Gauge:
		for _, dp := range data.Gauge.GetDataPoints() {
			if err := emit(dpTime(dp), dp.GetAttributes(), numberValue(dp)); err != nil {
				return err
			}
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range data.Histogram.GetDataPoints() {
			if err := emit(dp.GetTimeUnixNano(), dp.GetAttributes(), dp.GetSum()); err != nil {
				return err
			}
		}
	default:
		// Summary and exponential histograms are not emitted by this agent.
		// Ignoring them silently would be indistinguishable from handling them,
		// so they are reported as an error the receiver counts and logs.
		return errUnsupportedMetricShape(rec.Metric.GetName())
	}
	return nil
}

// ConsumeLog normalizes one claimed log record. The agent's api_request event is
// the model call — the record that carries tokens, cost, duration and model
// together — and every other event is activity for the feed.
func (a *Adapter) ConsumeLog(ctx context.Context, rec receiver.LogRecord) error {
	name := logEventName(rec.Log)
	attrs := rec.Log.GetAttributes()

	operation := name
	if name == eventAPIRequest {
		operation = operationChat
	}

	a.sawLog.Store(true)

	e := a.newEvent(a.instanceFrom(rec.Resource), operation, timeFromUnixNano(logTime(rec.Log)))
	e.Attributes = a.scrubbedAttrs(attrs)

	if name == eventAPIRequest {
		e.RequestModel = stringAttr(attrs, ccAttrModel)
		e.ResponseModel = e.RequestModel
		if ms, ok := floatAttr(attrs, ccAttrDurationMS); ok {
			e.Duration = time.Duration(ms * float64(time.Millisecond))
		}
		if usd, ok := floatAttr(attrs, ccAttrCostUSD); ok {
			e.CostUSD = didebaan.Dollars(usd)
		}
		if n, ok := intAttr(attrs, ccAttrInputTok); ok {
			e.Usage.InputTokens = didebaan.Tokens(n)
		}
		if n, ok := intAttr(attrs, ccAttrOutputTok); ok {
			e.Usage.OutputTokens = didebaan.Tokens(n)
		}
	}

	return a.emit(ctx, e)
}

// metricsOnlyGrace is how many pre-aggregated token/cost metrics may be skipped
// with no log event yet seen before the metrics-only warning fires. It is not
// zero because a metrics export legitimately arrives before the first model call
// on a freshly started agent, and a warning that cries wolf at startup is one
// nobody reads by the time it is true.
const metricsOnlyGrace = 3

// warnIfMetricsOnly reports the configuration in which the agent exports its
// metrics but not its logs.
//
// That combination produces no token or cost figures whatsoever: the aggregates
// are deliberately not recorded because the event path is the measurement, and
// the event path is not running. Without this it is the quietest possible
// failure — records arrive, dimensions populate, the activity feed fills from
// the other metrics, and only the two numbers anyone actually asked for are
// missing.
func (a *Adapter) warnIfMetricsOnly() {
	if a.sawLog.Load() {
		return
	}
	if a.skippedAggregates.Add(1) < metricsOnlyGrace {
		return
	}
	a.warnMetricsOnlyOnce.Do(func() {
		_, _ = fmt.Fprintf(stderr,
			"didebaan: WARNING: receiving %s/%s metrics but no log events, so NO token or cost data is being produced. "+
				"These pre-aggregated metrics are not recorded, because the same numbers arrive per-operation on the "+
				"api_request event and recording both would double-count. Set OTEL_LOGS_EXPORTER=otlp in the agent's "+
				"environment to enable the event path.\n",
			metricTokenUsage, metricCostUsage)
	})
}

// errUnsupportedMetricShape reports a metric aggregation this adapter does not
// map. Returning an error rather than ignoring it keeps the gap countable: the
// receiver logs it and the operator can see that something arrived and went
// nowhere.
func errUnsupportedMetricShape(name string) error {
	return fmt.Errorf("claude-code: metric %q uses an aggregation this adapter does not map", name)
}

// logEventName reads a log record's event name from the dedicated field,
// falling back to the event.name attribute for SDKs that predate it.
func logEventName(lr *logspb.LogRecord) string {
	if n := lr.GetEventName(); n != "" {
		return n
	}
	return stringAttr(lr.GetAttributes(), "event.name")
}

// newEvent builds the fields every normalized event from this agent shares.
func (a *Adapter) newEvent(instance, operation string, ts time.Time) didebaan.Event {
	return didebaan.Event{
		Timestamp: ts,
		Agent:     Name,
		Instance:  instance,
		System:    System,
		Operation: operation,
	}
}

// timeFromUnixNano converts an OTLP timestamp, mapping the "unknown" zero to the
// zero time rather than to the Unix epoch — a record stamped 1970 would sort and
// display as real data.
func timeFromUnixNano(ns uint64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(ns))
}

// logTime prefers the record's own timestamp and falls back to when the
// collection system observed it, per the OTLP data model.
func logTime(lr interface {
	GetTimeUnixNano() uint64
	GetObservedTimeUnixNano() uint64
}) uint64 {
	if t := lr.GetTimeUnixNano(); t != 0 {
		return t
	}
	return lr.GetObservedTimeUnixNano()
}

// numberDataPoint is the shape both Sum and Gauge points share.
type numberDataPoint interface {
	GetTimeUnixNano() uint64
	GetAsInt() int64
	GetAsDouble() float64
	GetAttributes() []*commonpb.KeyValue
}

func dpTime(dp numberDataPoint) uint64 { return dp.GetTimeUnixNano() }

// numberValue reads a number data point's value in whichever representation it
// carries.
func numberValue(dp numberDataPoint) any {
	if v := dp.GetAsInt(); v != 0 {
		return v
	}
	if v := dp.GetAsDouble(); v != 0 {
		return v
	}
	return int64(0)
}

// floatAttr reads a numeric attribute, accepting either OTLP numeric form and a
// numeric string.
//
// The string case exists because a live agent (2.1.258) types the same
// attribute differently by event: duration_ms arrives as a string on
// tool_result and mcp_server_connection, and as an int on api_request and
// subagent_completed.
//
// ⚠️ Scoped honestly: on api_request — the only event this adapter parses
// numerics from — duration_ms was an int in every sample captured, across two
// independent captures. So for the field as currently read, the string branch is
// precaution rather than a fix for observed loss. It is kept because the typing
// is demonstrably per-event rather than per-attribute, so a new event promoted
// to the measurement path could arrive typed either way, and because the
// alternative failure is silent: an unreported duration records nothing, which
// is a legitimate state and therefore indistinguishable from a parse that
// declined.
func floatAttr(attrs []*commonpb.KeyValue, key string) (float64, bool) {
	for _, kv := range attrs {
		if kv.GetKey() != key {
			continue
		}
		switch v := kv.GetValue().GetValue().(type) {
		case *commonpb.AnyValue_DoubleValue:
			return v.DoubleValue, true
		case *commonpb.AnyValue_IntValue:
			return float64(v.IntValue), true
		case *commonpb.AnyValue_StringValue:
			f, err := strconv.ParseFloat(v.StringValue, 64)
			return f, err == nil
		}
		return 0, false
	}
	return 0, false
}

// intAttr reads an integer attribute, accepting a double that is a whole number
// so a sender that encodes counts as doubles is not silently dropped, and a
// numeric string for the reason given on floatAttr.
func intAttr(attrs []*commonpb.KeyValue, key string) (int64, bool) {
	for _, kv := range attrs {
		if kv.GetKey() != key {
			continue
		}
		switch v := kv.GetValue().GetValue().(type) {
		case *commonpb.AnyValue_IntValue:
			return v.IntValue, true
		case *commonpb.AnyValue_DoubleValue:
			return int64(v.DoubleValue), true
		case *commonpb.AnyValue_StringValue:
			n, err := strconv.ParseInt(v.StringValue, 10, 64)
			return n, err == nil
		}
		return 0, false
	}
	return 0, false
}
