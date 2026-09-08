# Using Didebaan

Didebaan collects telemetry from an AI coding agent and exports it over OTLP.
This page covers the commands and configuration; for the design, see the
[ADRs](adr/).

## Commands

```
didebaan [flags] <command>
```

Configuration flags are global and precede the command, e.g.
`didebaan --otlp-endpoint localhost:4319 collect`.

### `collect`

Collect telemetry from the selected input adapter and export it over OTLP until
interrupted (Ctrl-C / SIGTERM).

```sh
didebaan --otlp-endpoint localhost:4319 --otlp-insecure collect
```

### `adapters`

List the input adapters compiled into this binary. Each corresponds to one AI
coding agent (ADR 0003).

```sh
didebaan adapters
# claude-code
```

### `version`

Print the build version.

```sh
didebaan version
```

## Configuration

Every value resolves through **file < env < flag**:

| Flag | Env | Config key | Meaning |
|---|---|---|---|
| `--adapter` | `DIDEBAAN_ADAPTER` | `adapter` | Input adapter to collect from (default `claude-code`). |
| `--otlp-endpoint` | `DIDEBAAN_OTLP_ENDPOINT` | `otlp-endpoint` | OTLP/gRPC endpoint, `host:port`. Empty falls back to the OpenTelemetry `OTEL_EXPORTER_OTLP_*` env defaults. |
| `--otlp-insecure` | `DIDEBAAN_OTLP_INSECURE` | `otlp-insecure` | Use plaintext gRPC instead of TLS. Only for a local collector on loopback. |
| `--config` | `DIDEBAAN_CONFIG` | — | Path to a YAML config file (highest-precedence file source). |

The config file defaults to `/etc/didebaan/config.yaml`; see
[`config.example.yaml`](config.example.yaml) for a documented example.

## What it emits

Didebaan normalizes agent activity into the OpenTelemetry GenAI semantic
conventions (`gen_ai.*`) and exports traces, metrics, and logs over OTLP (ADR
0002, 0004). Any OpenTelemetry-compatible backend — the OpenTelemetry Collector,
Prometheus/Grafana/Loki, SigNoz, and others — can consume it. Where the telemetry
is stored and visualized is your choice, made downstream of Didebaan.

## Scope

v1 is read-only: Didebaan observes and exports, and never writes back to the
agent (ADR 0007).

## Pointing an agent at the collector

Didebaan receives OpenTelemetry rather than reading an agent's files (ADR 0008).
The collector listens on the conventional OTLP ports, so an agent using its own
defaults finds it with no endpoint configuration:

```sh
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_METRICS_EXPORTER=otlp
export OTEL_LOGS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
```

### Identify the machine — required, and silent if you skip it

```sh
export OTEL_RESOURCE_ATTRIBUTES=service.instance.id=<a-name-for-this-machine>
```

🚨 **Set this on every host, with a different value on each.** It is what
populates the `didebaan.instance` metric dimension, and it is the only thing that
separates one running agent from another: no agent's own telemetry carries the
identity of the machine it runs on, because an agent has no concept of the fleet
it belongs to.

**Skipping it does not look like a missing step.** Records still arrive, metrics
still export, and every dashboard still draws — but every machine shares one
dimension value, so "which agent is active" collapses into "some agent is
active", which is a question nobody asked. The collector warns once on stderr
when it sees telemetry with no instance identity, and stamps the conspicuous
value `UNSET-see-OTEL_RESOURCE_ATTRIBUTES` rather than an empty string, so the
gap is visible in the data instead of blending into it. `host.name` is accepted
as a fallback if the agent already sets it.

### What is dropped on the way through

Claude Code attaches `user.email`, `user.id`, `user.account_uuid`,
`user.account_id`, `organization.id` and `terminal.type` to every record whenever
telemetry is enabled, and **`user.email` and `user.id` are not suppressed by the
agent's own content-redaction settings**. Didebaan drops all six at the adapter
boundary by default (ADR 0008 §6). `session.id` is kept: it groups a session and
carries no personal content.

Forwarding them instead is an explicit choice, per adapter:

```yaml
adapters:
  claude-code:
    forward_identity: true
```
