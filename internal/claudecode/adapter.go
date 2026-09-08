// Package claudecode is the input adapter for the Claude Code agent — the first
// adapter (ADR 0003), because Claude Code exports OpenTelemetry natively and so
// exercises the shortest path from an agent to gen_ai.*.
//
// The adapter does not read the agent: it receives from the collector's embedded
// OTLP receiver (ADR 0008). The agent is pointed at the receiver with the
// standard OTEL_* environment variables, and the receiver dispatches the records
// this adapter claims. Reading the agent's transcripts instead would re-derive,
// less faithfully, data the agent already emits in a standard format, and would
// couple Didebaan to a private on-disk layout carrying no stability contract.
//
// It registers itself with the adapter registry on import.
package claudecode

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/yaad-index/didebaan/internal/adapter"
	"github.com/yaad-index/didebaan/internal/receiver"
	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// Name is the registry name this adapter is selected by.
const Name = "claude-code"

// Namespace is the metric name prefix Claude Code emits under.
//
// ⚠️ It applies to metrics only. The agent's log events are NOT namespaced —
// they arrive as bare names ("api_request", "user_prompt"), verified against a
// live agent, version 2.1.258. Claiming events by this prefix matches none of
// them, which is why the claim below leans on the instrumentation scope.
const Namespace = "claude_code."

// ScopePrefix is the instrumentation scope Claude Code emits under. Metrics
// carry "com.anthropic.claude_code" and log events carry
// "com.anthropic.claude_code.events", so one prefix claims both.
//
// This is the identifier the agent cannot leave off: a name prefix is a
// convention the agent may not follow, and here it does not.
const ScopePrefix = "com.anthropic.claude_code"

// System is the gen_ai.system value for this agent's operations.
const System = "anthropic"

// cfgForwardIdentity opts into forwarding the agent's identity attributes
// instead of dropping them at this boundary (ADR 0008 §6). Off by default: an
// operator who wants per-user attribution can say so, whereas a personal email
// address exported by default cannot be un-exported.
const cfgForwardIdentity = "forward_identity"

func init() {
	adapter.Register(Name, New)
}

// Adapter receives Claude Code's native OpenTelemetry records from the
// collector's receiver and emits normalized events.
type Adapter struct {
	// forwardIdentity is set from configuration and never mutated afterwards.
	forwardIdentity bool

	// mu guards sink, which is installed by Run and read by every Consume call.
	// Records can arrive on the receiver's goroutines before or after Run holds
	// the sink, so the two are genuinely concurrent.
	mu   sync.RWMutex
	sink didebaan.Sink

	warnInstanceOnce warnOnce

	// sawLog records whether any log event has ever been consumed, and
	// skippedAggregates counts pre-aggregated token/cost metrics dropped
	// because the event path is the measurement. Together they detect the
	// metrics-only configuration, in which no token or cost figure is produced
	// at all. See warnIfMetricsOnly.
	sawLog              atomic.Bool
	skippedAggregates   atomic.Int64
	warnMetricsOnlyOnce warnOnce
}

// New constructs the Claude Code adapter. The only recognised configuration key
// is forward_identity (bool); an unrecognised key is an error rather than a
// silent no-op, because a misspelled privacy setting that reads as "off" while
// the operator believes it is "on" is the failure worth being loud about.
func New(cfg map[string]any) (didebaan.Adapter, error) {
	a := &Adapter{}
	for k, v := range cfg {
		switch k {
		case cfgForwardIdentity:
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("claude-code: %s must be a bool, got %T", cfgForwardIdentity, v)
			}
			a.forwardIdentity = b
		default:
			return nil, fmt.Errorf("claude-code: unknown configuration key %q", k)
		}
	}
	return a, nil
}

// Name identifies the adapter.
func (a *Adapter) Name() string { return Name }

// Claim declares the telemetry this adapter takes responsibility for
// (ADR 0008 §2).
//
// ⚠️ It departs from the ADR's rule that "metrics and logs are claimed by
// namespace prefix". Verified against a live agent (2.1.258): metrics do carry
// the claude_code.* prefix, but log events carry no namespace at all —
// "api_request", "user_prompt", "assistant_response", "mcp_server_connection".
// A namespace claim over those matches nothing.
//
// Claiming them by their bare names instead would be worse than losing them:
// "api_request" is a name any agent might emit, so this adapter would start
// normalizing another agent's events with Claude Code's rules. The scope is what
// actually identifies the emitter, so the claim leans on that and the ADR needs
// its §2 rule widened to say so.
//
// SpanScopes is empty because v1 does not ingest the agent's own span tree —
// those spans are beta in the agent and behind a separate opt-in there, so their
// shape can still change (ADR 0008 §4). It is left as an empty declaration
// rather than omitted so that enabling span ingest is a matter of naming the
// scope, and so a reader can see that the omission was decided rather than
// forgotten.
func (a *Adapter) Claim() didebaan.Claim {
	return didebaan.Claim{
		MetricPrefixes: []string{Namespace},
		ScopePrefixes:  []string{ScopePrefix},
		SpanScopes:     nil,
	}
}

// Run installs the sink and blocks until ctx is cancelled, returning ctx.Err().
//
// The adapter is push-driven: the receiver calls into it as records arrive, so
// Run does no reading of its own. It still owns the sink's lifetime, which is
// what keeps the adapter contract (ADR 0003) the same shape for a push adapter
// as for one that polls.
func (a *Adapter) Run(ctx context.Context, sink didebaan.Sink) error {
	a.mu.Lock()
	a.sink = sink
	a.mu.Unlock()

	<-ctx.Done()

	// Drop the sink on the way out so a record arriving during shutdown is
	// refused rather than written into a pipeline that is being torn down.
	a.mu.Lock()
	a.sink = nil
	a.mu.Unlock()

	return ctx.Err()
}

// errNotRunning is returned when a record arrives outside Run's lifetime.
var errNotRunning = errors.New("claude-code: adapter is not running, so the event has nowhere to go")

// emit sends an event to the installed sink.
func (a *Adapter) emit(ctx context.Context, e didebaan.Event) error {
	a.mu.RLock()
	sink := a.sink
	a.mu.RUnlock()
	if sink == nil {
		return errNotRunning
	}
	return sink(ctx, e)
}

// ConsumeSpan is never called in v1: Claim declares no span scopes, so the
// receiver routes no spans here. It exists to satisfy the consumer contract.
func (a *Adapter) ConsumeSpan(context.Context, receiver.SpanRecord) error {
	return nil
}

// Compile-time check that the adapter satisfies both contracts it is used
// through: the agent-agnostic adapter interface and the receiver's consumer
// interface.
var (
	_ didebaan.Adapter  = (*Adapter)(nil)
	_ receiver.Consumer = (*Adapter)(nil)
)
