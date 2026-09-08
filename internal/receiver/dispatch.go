package receiver

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// stderr is where the receiver reports an adapter's per-record failures. It is a
// variable so a test can capture the reports rather than let them escape to the
// process's own stderr.
var stderr io.Writer = os.Stderr

// maxBodyBytes caps an OTLP/HTTP request body. Without a cap the receiver would
// allocate whatever a sender claims to be sending, on a socket that by default
// needs no authentication to reach.
const maxBodyBytes = 16 << 20 // 16 MiB

// attrEventName carries a log record's event name on SDKs that predate the
// dedicated LogRecord.event_name field. Both are checked when matching a claim,
// because which one an agent populates depends on its SDK version rather than on
// anything we control.
const attrEventName = "event.name"

// signal labels the unclaimed counter, so one instrument reports all three
// signals without three instruments to keep in step.
const attrSignal = "didebaan.signal"

// dispatchMetrics routes each metric to the adapter claiming its name.
func (r *Receiver) dispatchMetrics(ctx context.Context, resourceMetrics []*metricspb.ResourceMetrics) {
	for _, rm := range resourceMetrics {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				c := r.claimMetric(m.GetName(), sm.GetScope().GetName())
				if c == nil {
					r.countUnclaimed(ctx, "metrics", &r.stats.MetricsUnclaimed)
					continue
				}
				r.stats.MetricsClaimed.Add(1)
				rec := MetricRecord{Resource: rm.GetResource(), Scope: sm.GetScope(), Metric: m}
				if err := c.ConsumeMetric(ctx, rec); err != nil {
					r.reportConsumeError(c.Name(), "metric", m.GetName(), err)
				}
			}
		}
	}
}

// dispatchLogs routes each log record to the adapter claiming its event name.
func (r *Receiver) dispatchLogs(ctx context.Context, resourceLogs []*logspb.ResourceLogs) {
	for _, rl := range resourceLogs {
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				name := eventName(lr)
				c := r.claimEvent(name, sl.GetScope().GetName())
				if c == nil {
					r.countUnclaimed(ctx, "logs", &r.stats.LogsUnclaimed)
					continue
				}
				r.stats.LogsClaimed.Add(1)
				rec := LogRecord{Resource: rl.GetResource(), Scope: sl.GetScope(), Log: lr}
				if err := c.ConsumeLog(ctx, rec); err != nil {
					r.reportConsumeError(c.Name(), "log", name, err)
				}
			}
		}
	}
}

// dispatchSpans accounts for arriving spans without ingesting them.
//
// v1 does not ingest an agent's own span tree: those spans are beta in the agent
// and behind a separate opt-in there, so their shape can still change
// (ADR 0008 §4). The trace service is nevertheless registered and answers
// successfully, because refusing the RPC outright would make an agent that
// exports all three signals retry a permanent failure forever.
//
// This is not a statement that v1 emits no traces — the collector still
// synthesizes one span per normalized event, unconditionally.
func (r *Receiver) dispatchSpans(ctx context.Context, resourceSpans []*tracepb.ResourceSpans) {
	for _, rs := range resourceSpans {
		for _, ss := range rs.GetScopeSpans() {
			n := int64(len(ss.GetSpans()))
			if n == 0 {
				continue
			}
			r.stats.SpansIgnored.Add(n)
			r.unclaimed.Add(ctx, n, metric.WithAttributes(attribute.String(attrSignal, "traces")))
		}
	}
}

// claimMetric returns the consumer claiming a metric, by metric name or by the
// instrumentation scope that emitted it.
func (r *Receiver) claimMetric(name, scope string) Consumer {
	for _, c := range r.consumers {
		claim := c.Claim()
		if hasAnyPrefix(name, claim.MetricPrefixes) || hasAnyPrefix(scope, claim.ScopePrefixes) {
			return c
		}
	}
	return nil
}

// claimEvent returns the consumer claiming a log record, by event name or by the
// instrumentation scope that emitted it.
//
// The scope is checked because an agent's event names may carry no namespace at
// all — Claude Code emits "api_request" rather than "claude_code.api_request" —
// and claiming such a name by prefix would either match nothing or claim a name
// generic enough to belong to any agent.
func (r *Receiver) claimEvent(name, scope string) Consumer {
	for _, c := range r.consumers {
		claim := c.Claim()
		if hasAnyPrefix(name, claim.EventPrefixes) || hasAnyPrefix(scope, claim.ScopePrefixes) {
			return c
		}
	}
	return nil
}

// hasAnyPrefix reports whether s starts with any non-empty prefix in prefixes.
// An empty prefix is skipped rather than matching everything: a claim left empty
// by accident must claim nothing, not claim the whole receiver.
func hasAnyPrefix(s string, prefixes []string) bool {
	if s == "" {
		return false
	}
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// eventName reads a log record's event name from the dedicated field, falling
// back to the event.name attribute.
func eventName(lr *logspb.LogRecord) string {
	if n := lr.GetEventName(); n != "" {
		return n
	}
	for _, kv := range lr.GetAttributes() {
		if kv.GetKey() == attrEventName {
			return kv.GetValue().GetStringValue()
		}
	}
	return ""
}

// countUnclaimed records an unclaimed record on both the in-process counter and
// the exported metric.
func (r *Receiver) countUnclaimed(ctx context.Context, signal string, stat interface{ Add(int64) int64 }) {
	stat.Add(1)
	r.unclaimed.Add(ctx, 1, metric.WithAttributes(attribute.String(attrSignal, signal)))
}

// reportConsumeError surfaces an adapter's failure to handle a record it
// claimed. One bad record must not abort the rest of the export request: the
// sender would retry the whole batch and the same record would fail again.
func (r *Receiver) reportConsumeError(adapter, kind, name string, err error) {
	_, _ = fmt.Fprintf(stderr, "didebaan: adapter %q failed on %s %q: %v\n", adapter, kind, name, err)
}

// isProtobuf reports whether a Content-Type names the OTLP protobuf encoding,
// ignoring any parameters (a charset, for instance) the sender attached.
func isProtobuf(contentType string) bool {
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == contentTypeProtobuf
}

// readLimited reads a request body up to maxBodyBytes, reporting an error when
// the sender exceeds it rather than silently truncating — a truncated protobuf
// would fail to decode with a message about the wire format, which would send
// whoever reads it looking in the wrong place.
func readLimited(req *http.Request) ([]byte, error) {
	limited := io.LimitReader(req.Body, maxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("request body exceeds the %d-byte limit", maxBodyBytes)
	}
	return body, nil
}
