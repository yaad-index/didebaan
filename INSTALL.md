# Installing Didebaan

Didebaan is a single static Go binary. It requires **Go 1.26+** to build from
source.

## Build from source

```sh
git clone https://github.com/yaad-index/didebaan
cd didebaan
go build -o didebaan ./cmd/didebaan
./didebaan version
```

Or install straight into your `GOBIN`:

```sh
go install github.com/yaad-index/didebaan/cmd/didebaan@latest
```

## Run

Didebaan reads an agent's activity and exports it over OTLP to a downstream
collector. Point it at your collector's OTLP/gRPC endpoint:

```sh
didebaan --otlp-endpoint localhost:4317 --otlp-insecure collect
```

Use `--otlp-insecure` only for a local collector on the loopback interface; drop
it to use TLS. See [USAGE.md](USAGE.md) for all commands and configuration.

## Configuration

Configuration layers as **file < env < flag**:

1. A YAML config file — `/etc/didebaan/config.yaml` by default, or `--config PATH`.
   See [`config.example.yaml`](config.example.yaml).
2. `DIDEBAAN_*` environment variables (e.g. `DIDEBAAN_OTLP_ENDPOINT`). See
   [`.env.example`](.env.example).
3. Command-line flags, which win.

## Docker

A container image is published to GHCR on each release:

```sh
docker run --rm \
  -e DIDEBAAN_OTLP_ENDPOINT=host.docker.internal:4317 \
  ghcr.io/yaad-index/didebaan:latest
```

The image runs `didebaan collect` as a non-root user by default. Nothing is
baked into the image — supply configuration via env or a mounted config file.

To build the image locally:

```sh
docker build -t didebaan:dev .
```
