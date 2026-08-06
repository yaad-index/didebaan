# syntax=docker/dockerfile:1

# --- builder: compile a static binary -------------------------------------
FROM golang:1.26 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
# CGO_ENABLED=0 ⇒ a fully static binary that runs on distroless/static.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/didebaan ./cmd/didebaan

# --- runtime: minimal, non-root -------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/didebaan /usr/local/bin/didebaan

# Config layers as file < env < flag. Point the collector at a downstream OTLP
# endpoint via DIDEBAAN_OTLP_ENDPOINT (or the standard OTEL_EXPORTER_OTLP_*
# env). Nothing is baked into the image.
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/didebaan"]
CMD ["collect"]
