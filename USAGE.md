# Using Didebaan

Didebaan collects telemetry from an AI coding agent and exports it over OTLP.
This page covers the commands and configuration; for the design, see the
[ADRs](adr/).

## Commands

```
didebaan [flags] <command>
```

Configuration flags are global and precede the command, e.g.
`didebaan --otlp-endpoint localhost:4317 collect`.

### `collect`

Collect telemetry from the selected input adapter and export it over OTLP until
interrupted (Ctrl-C / SIGTERM).

```sh
didebaan --otlp-endpoint localhost:4317 --otlp-insecure collect
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
