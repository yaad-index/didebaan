// Package genai maps Didebaan's normalized events onto the OpenTelemetry GenAI
// semantic conventions (gen_ai.*), the common schema Didebaan adopts (ADR 0002).
// It is the single place that knows the attribute names, the metric-instrument
// names, and how an event becomes each of the three OpenTelemetry signals, so
// adapters stay concerned only with reading their agent and the exporter stays
// concerned only with transport.
package genai

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"

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

	// AttrTokenType tags a token-usage measurement as input or output
	// (gen_ai.token.type), the dimension that splits the one token-usage
	// instrument into its two directions.
	AttrTokenType = "gen_ai.token.type"

	// AttrAgent is Didebaan's own attribute identifying the source AI coding
	// agent. It is not part of the gen_ai.* set; it lets downstream consumers
	// group telemetry by which agent produced it (ADR 0003).
	AttrAgent = "didebaan.agent"

	// AttrInstance identifies which running agent produced the activity, as
	// opposed to which kind of agent AttrAgent names. It is the dimension that
	// makes per-agent recency answerable: without it every machine's telemetry
	// aggregates into one indistinguishable series, which looks like a working
	// pipeline and answers no question about who is doing what.
	//
	// Safe as a dimension because it is bounded by the number of running
	// collectors. Contrast the unbounded identifiers (session ids, request ids)
	// that stay in the event's Attributes and never reach a metric.
	AttrInstance = "didebaan.instance"

	// AttrCostUSD carries an operation's cost on a span or log record. It is a
	// separate name from MetricCost for the same reason the token attributes
	// are separate names from the token-usage instrument: an attribute
	// describes one operation, an instrument accumulates across many, and
	// giving them one name makes a query ambiguous about which it meant.
	AttrCostUSD = "didebaan.cost.usd"
)

// GenAI metric-instrument names, from the OpenTelemetry GenAI metric
// conventions. Both are histograms: the token-usage histogram's sum yields
// cumulative "tokens used" while its distribution is preserved, and the
// duration histogram captures operation latency.
const (
	MetricTokenUsage = "gen_ai.client.token.usage"
	MetricOpDuration = "gen_ai.client.operation.duration"

	// MetricCost is Didebaan's cost metric. The GenAI conventions define no
	// cost metric, and the ad-hoc name in common use sits *inside* the
	// gen_ai.* namespace — so adopting it would squat on a name upstream may
	// define differently. This sits in Didebaan's own namespace instead
	// (ADR 0008 §5).
	//
	// When a standard cost metric exists, Didebaan emits the standard name.
	// Pre-1.0 this name may be dropped in any minor release with a changelog
	// entry, since minor is the breaking vehicle before 1.0; post-1.0 it is
	// kept as an alias for one minor cycle.
	MetricCost = "didebaan.cost.usage"
)

// tokenType values for AttrTokenType.
const (
	tokenTypeInput  = "input"
	tokenTypeOutput = "output"
)

// kv is a neutral key/value pair, mapped to the per-signal attribute type by
// the exported functions below. Keeping one internal representation means the
// attribute names and inclusion rules live in exactly one place.
type kv struct {
	key string
	val any
}

// dimensionPairs are the low-cardinality descriptive attributes safe to use as
// metric dimensions: which agent kind, which running instance, system,
// operation, and models. They deliberately exclude token counts (the measured
// values) and the free-form Attributes map, which may be high-cardinality — a
// session id or a request id there would multiply series without bound.
//
// Every field admitted here has to be bounded by something structural: the
// number of agent kinds, of running collectors, of models. "Probably small in
// practice" is not the test, because the cost of being wrong is paid downstream
// and long after.
func dimensionPairs(e didebaan.Event) []kv {
	ps := make([]kv, 0, 6)
	if e.Agent != "" {
		ps = append(ps, kv{AttrAgent, e.Agent})
	}
	if e.Instance != "" {
		ps = append(ps, kv{AttrInstance, e.Instance})
	}
	if e.System != "" {
		ps = append(ps, kv{AttrSystem, e.System})
	}
	if e.Operation != "" {
		ps = append(ps, kv{AttrOperationName, e.Operation})
	}
	if e.RequestModel != "" {
		ps = append(ps, kv{AttrRequestModel, e.RequestModel})
	}
	if e.ResponseModel != "" {
		ps = append(ps, kv{AttrResponseModel, e.ResponseModel})
	}
	return ps
}

// pairs are the full attributes for a trace span or log record: the dimensions
// plus the token counts (when reported) and any extra attributes the adapter
// carried. Unreported token counts (nil) are omitted, so a zero-value or
// partially-filled event never asserts a false 0.
func pairs(e didebaan.Event) []kv {
	ps := dimensionPairs(e)
	if e.Usage.InputTokens != nil {
		ps = append(ps, kv{AttrUsageInput, *e.Usage.InputTokens})
	}
	if e.Usage.OutputTokens != nil {
		ps = append(ps, kv{AttrUsageOutput, *e.Usage.OutputTokens})
	}
	if e.CostUSD != nil {
		ps = append(ps, kv{AttrCostUSD, *e.CostUSD})
	}
	for k, v := range e.Attributes {
		ps = append(ps, kv{k, v})
	}
	return ps
}

// Attributes translates an event into the full set of GenAI attributes for a
// trace span or log record.
func Attributes(e didebaan.Event) []attribute.KeyValue {
	ps := pairs(e)
	out := make([]attribute.KeyValue, 0, len(ps))
	for _, p := range ps {
		out = append(out, attrKV(p.key, p.val))
	}
	return out
}

// DimensionAttributes translates an event into just the low-cardinality
// descriptive attributes suitable as metric dimensions.
func DimensionAttributes(e didebaan.Event) []attribute.KeyValue {
	ps := dimensionPairs(e)
	out := make([]attribute.KeyValue, 0, len(ps))
	for _, p := range ps {
		out = append(out, attrKV(p.key, p.val))
	}
	return out
}

// LogRecord builds the OpenTelemetry log record for an event: the activity-feed
// entry, timestamped to the operation with the full GenAI attributes. The log
// API reuses the attribute package's Value/KeyValue types, so the same mapping
// as traces applies.
func LogRecord(e didebaan.Event) log.Record {
	var r log.Record
	r.SetTimestamp(e.Timestamp)
	r.SetBody(attribute.StringValue(SpanName(e)))
	r.AddAttributes(Attributes(e)...)
	return r
}

// SpanName derives the span name (and log-record body) from the event's
// operation, falling back to a generic label when the adapter did not report
// one.
func SpanName(e didebaan.Event) string {
	if e.Operation != "" {
		return e.Operation
	}
	return "gen_ai.operation"
}

// Instruments holds the GenAI metric instruments, created once from a Meter and
// reused across events.
type Instruments struct {
	tokenUsage metric.Int64Histogram
	opDuration metric.Float64Histogram
	cost       metric.Float64Counter
}

// NewInstruments creates the GenAI instruments on m.
func NewInstruments(m metric.Meter) (*Instruments, error) {
	tokenUsage, err := m.Int64Histogram(
		MetricTokenUsage,
		metric.WithUnit("{token}"),
		metric.WithDescription("Number of tokens used per GenAI operation, split by token type."),
	)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricTokenUsage, err)
	}
	opDuration, err := m.Float64Histogram(
		MetricOpDuration,
		metric.WithUnit("s"),
		metric.WithDescription("Duration of a GenAI operation."),
	)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricOpDuration, err)
	}
	// Cost is a counter rather than a histogram: the question asked of it is
	// "how much has this instance spent", which is a sum over time. A
	// distribution of per-operation costs is a different question, and one the
	// token-usage histogram already answers in the dimension that drives it.
	cost, err := m.Float64Counter(
		MetricCost,
		metric.WithUnit("{USD}"),
		metric.WithDescription("Cost attributed to GenAI operations, in US dollars."),
	)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", MetricCost, err)
	}
	return &Instruments{tokenUsage: tokenUsage, opDuration: opDuration, cost: cost}, nil
}

// Record maps an event onto the metric signals: a token-usage measurement for
// each reported direction (tagged input/output), an operation-duration
// measurement when the duration is known, and a cost contribution when the agent
// reported one. Measurements carry only the low-cardinality dimension
// attributes. Unreported token counts, an unreported cost, and an unknown (zero)
// duration all record nothing — an event that knows less says less, rather than
// asserting a false zero.
func (in *Instruments) Record(ctx context.Context, e didebaan.Event) {
	dims := DimensionAttributes(e)

	if e.Usage.InputTokens != nil {
		in.tokenUsage.Record(ctx, *e.Usage.InputTokens, metric.WithAttributes(
			withTokenType(dims, tokenTypeInput)...,
		))
	}
	if e.Usage.OutputTokens != nil {
		in.tokenUsage.Record(ctx, *e.Usage.OutputTokens, metric.WithAttributes(
			withTokenType(dims, tokenTypeOutput)...,
		))
	}
	if e.Duration > 0 {
		in.opDuration.Record(ctx, e.Duration.Seconds(), metric.WithAttributes(dims...))
	}
	if e.CostUSD != nil {
		in.cost.Add(ctx, *e.CostUSD, metric.WithAttributes(dims...))
	}
}

// withTokenType returns dims plus the token-type attribute, without mutating
// dims' backing array (each call gets its own slice).
func withTokenType(dims []attribute.KeyValue, t string) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(dims)+1)
	out = append(out, dims...)
	out = append(out, attribute.String(AttrTokenType, t))
	return out
}

// attrKV renders a neutral value as a typed trace/metric attribute, falling
// back to a string for types the conventions don't cover.
func attrKV(k string, v any) attribute.KeyValue {
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
