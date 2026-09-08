package claudecode

import (
	"fmt"
	"io"
	"os"
	"sync"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// stderr is where the adapter warns about missing instance identity. A variable
// so tests can capture the warning instead of letting it escape.
var stderr io.Writer = os.Stderr

// identityAttrs are the attributes Claude Code attaches to every metric, event
// and span whenever telemetry is enabled, and which this adapter drops at the
// boundary unless an operator opts in (ADR 0008 §6).
//
// ⚠️ Redacting content is not sufficient, because identity does not travel in
// the content. user.email and user.id in particular are documented as never
// gated by the agent's content-redaction settings — no privacy flag on the agent
// suppresses them. A collector honouring only content redaction would ingest a
// personal email address on every record and re-export it to an
// operator-chosen backend by default, while reporting its privacy posture as
// satisfied.
var identityAttrs = map[string]bool{
	"user.email":        true,
	"user.id":           true,
	"user.account_uuid": true,
	"user.account_id":   true,
	"organization.id":   true,
	"terminal.type":     true,
}

// AttrSessionID is retained rather than dropped: it carries no personal content
// and it is what makes an agent's records groupable into a session. It stays in
// the event's Attributes and never becomes a metric dimension — it is unbounded,
// one value per session forever.
const AttrSessionID = "session.id"

// AttrSubagentName is Claude Code's agent.name, renamed on the way through.
//
// ⚠️ The rename is the point. Claude Code's agent.name is the *subagent* within a
// session (it travels with query_source main/subagent/auxiliary and with
// skill.name), not the fleet member running the agent. Carrying it through under
// a name containing "agent" invites exactly the reading that it identifies which
// machine produced the record, which it does not. Instance is that, and it comes
// from the resource.
const AttrSubagentName = "claude_code.subagent.name"

// Resource attribute keys the instance identity is resolved from.
const (
	resourceInstanceID = "service.instance.id"
	resourceHostName   = "host.name"
)

// InstanceUnset is the value used when nothing on the resource identifies which
// agent produced the telemetry. It is a deliberately conspicuous value rather
// than an empty string, because an empty dimension silently merges every
// instance into one series — a working-looking pipeline that answers no
// per-agent question. See [instanceFrom].
const InstanceUnset = "UNSET-see-OTEL_RESOURCE_ATTRIBUTES"

// instanceFrom resolves which running agent produced a record, from the OTLP
// resource. It warns once per process when nothing identifies the instance.
//
// The fallback chain is service.instance.id, then host.name.
//
// ⚠️ service.name is deliberately NOT in that chain, though it is the obvious
// third candidate. Every host running the same agent reports the same
// service.name, so falling back to it would yield a value that looks like an
// instance identity, groups cleanly, and silently merges the whole fleet into
// one series. host.name differs per host, which is the property actually needed;
// a value that cannot distinguish instances is worse than one that announces it
// is missing.
func (a *Adapter) instanceFrom(res *resourcepb.Resource) string {
	attrs := res.GetAttributes()
	if v := stringAttr(attrs, resourceInstanceID); v != "" {
		return v
	}
	if v := stringAttr(attrs, resourceHostName); v != "" {
		return v
	}
	a.warnInstanceOnce.Do(func() {
		_, _ = fmt.Fprintf(stderr,
			"didebaan: WARNING: received telemetry carries no %s or %s on its resource, so every record from every "+
				"machine will share the instance dimension %q and per-agent queries cannot distinguish them. "+
				"Set OTEL_RESOURCE_ATTRIBUTES=%s=<name> in the agent's environment on each host.\n",
			resourceInstanceID, resourceHostName, InstanceUnset, resourceInstanceID)
	})
	return InstanceUnset
}

// warnOnce guards the instance warning; see [Adapter].
type warnOnce = sync.Once

// stringAttr returns the string value of key in attrs, or "".
func stringAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.GetKey() == key {
			return kv.GetValue().GetStringValue()
		}
	}
	return ""
}

// scrubbedAttrs converts OTLP attributes into the event's attribute map,
// dropping the identity set unless forwarding was opted into, and renaming
// agent.name to say what it actually is.
func (a *Adapter) scrubbedAttrs(attrs []*commonpb.KeyValue) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for _, kv := range attrs {
		key := kv.GetKey()
		if identityAttrs[key] && !a.forwardIdentity {
			continue
		}
		if key == "agent.name" {
			key = AttrSubagentName
		}
		if v, ok := anyValue(kv.GetValue()); ok {
			out[key] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// anyValue converts an OTLP AnyValue to a Go value. Composite values (arrays,
// nested key/value lists) are rendered as their string form: the event model's
// attributes are flat by design, and a structured value flattened silently would
// be worse than one that is visibly a string.
func anyValue(v *commonpb.AnyValue) (any, bool) {
	if v == nil {
		return nil, false
	}
	switch t := v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return t.StringValue, true
	case *commonpb.AnyValue_BoolValue:
		return t.BoolValue, true
	case *commonpb.AnyValue_IntValue:
		return t.IntValue, true
	case *commonpb.AnyValue_DoubleValue:
		return t.DoubleValue, true
	case *commonpb.AnyValue_BytesValue:
		return fmt.Sprintf("%x", t.BytesValue), true
	case nil:
		return nil, false
	default:
		return fmt.Sprintf("%v", v.GetValue()), true
	}
}
