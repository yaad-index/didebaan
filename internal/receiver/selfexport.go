package receiver

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Environment variables the OpenTelemetry SDK resolves an OTLP endpoint from
// when none is configured in-process. They are read here rather than left to the
// SDK because the collision check has to compare the endpoint that will actually
// be used, and that endpoint may exist only in the environment (ADR 0008).
const (
	envEndpoint       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envTraceEndpoint  = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envMetricEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	envLogEndpoint    = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
)

// sdkDefaultEndpoint is the OTLP/gRPC endpoint the OpenTelemetry SDK falls back
// to when neither configuration nor environment names one. It is the reason an
// empty export endpoint is not a safe value: it resolves to the same conventional
// port the receiver listens on by default.
const sdkDefaultEndpoint = "localhost:" + defaultGRPCPort

// defaultGRPCPort is the conventional OTLP/gRPC port, and the port an endpoint
// with no explicit one resolves to. It is not conditional on the URL scheme: see
// hostPort.
const defaultGRPCPort = "4317"

// ResolveExportEndpoints returns the OTLP/gRPC endpoints the three signal
// exporters will actually use, given the endpoint configured in-process
// (configured may be empty). The result always has three entries — one per
// signal — because the per-signal environment variables can point the signals at
// different places, and a loop on any one of them is a loop.
//
// The precedence mirrors the SDK's: an endpoint configured in-process wins for
// every signal; otherwise each signal takes its own environment variable, then
// the shared one, then the SDK default.
func ResolveExportEndpoints(configured string) map[string]string {
	if configured != "" {
		return map[string]string{
			"traces":  hostPort(configured),
			"metrics": hostPort(configured),
			"logs":    hostPort(configured),
		}
	}
	shared := os.Getenv(envEndpoint)
	pick := func(signalEnv string) string {
		if v := os.Getenv(signalEnv); v != "" {
			return hostPort(v)
		}
		if shared != "" {
			return hostPort(shared)
		}
		return sdkDefaultEndpoint
	}
	return map[string]string{
		"traces":  pick(envTraceEndpoint),
		"metrics": pick(envMetricEndpoint),
		"logs":    pick(envLogEndpoint),
	}
}

// hostPort reduces an endpoint to host:port. The environment variables are
// documented as URLs while the in-process option takes a bare host:port, so both
// forms arrive here; anything without a scheme is already in the wanted shape.
func hostPort(endpoint string) string {
	if !strings.Contains(endpoint, "://") {
		return strings.TrimSuffix(endpoint, "/")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return endpoint
	}
	if u.Port() == "" {
		// A URL with no explicit port leaves it to the transport's default.
		//
		// ⚠️ That default is 4317 whatever the scheme. The scheme selects TLS
		// (http vs https); the port distinguishes the OTLP transports (4317
		// gRPC, 4318 HTTP). Deriving 4318 from "https" conflates the two, and
		// it fails in the dangerous direction: this project's exporters are
		// gRPC-only, so the real connection goes to 4317, and a guard that
		// computed 4318 would compare the wrong address and miss a genuine
		// loop rather than raise a false alarm.
		//
		// If an OTLP/HTTP exporter is ever added here, this has to become a
		// function of the configured transport rather than a constant.
		return net.JoinHostPort(u.Hostname(), defaultGRPCPort)
	}
	return u.Host
}

// CheckNoSelfExport reports an error when any resolved export endpoint would
// reach one of the receiver's own listening addresses — the configuration in
// which the collector exports into itself and every record loops forever
// (ADR 0008).
//
// The comparison is on resolved addresses rather than on strings, because
// "localhost:4317", "127.0.0.1:4317" and "[::1]:4317" name one socket and
// compare unequal as text. When a listening address is unspecified (the
// all-interfaces opt-in) the relation is containment rather than equality: such
// a socket accepts on every local address, so exporting to any address of this
// host collides with it.
//
// An export host that does not resolve is not treated as a collision. Nothing
// can connect to a name that does not resolve, so it cannot be our own socket;
// the export will fail loudly on its own, which is a better failure than
// refusing to start over an unreachable downstream.
func CheckNoSelfExport(listenAddrs []string, exportEndpoints map[string]string) error {
	for _, listen := range listenAddrs {
		if listen == "" {
			continue
		}
		for signal, export := range exportEndpoints {
			collides, err := addrsCollide(listen, export)
			if err != nil {
				return fmt.Errorf("checking %s export endpoint %q against receiver address %q: %w", signal, export, listen, err)
			}
			if collides {
				return fmt.Errorf(
					"refusing to start: the %s OTLP export endpoint %q resolves to this collector's own receiver address %q, "+
						"which would export every received record back into the receiver; point the export at a downstream "+
						"collector, or move the receiver with --receiver-grpc/--receiver-http",
					signal, export, listen,
				)
			}
		}
	}
	return nil
}

// addrsCollide reports whether traffic sent to export would arrive at the socket
// bound to listen.
func addrsCollide(listen, export string) (bool, error) {
	listenHost, listenPort, err := net.SplitHostPort(listen)
	if err != nil {
		return false, fmt.Errorf("parse receiver address: %w", err)
	}
	exportHost, exportPort, err := net.SplitHostPort(export)
	if err != nil {
		// An export endpoint we cannot parse is not demonstrably a loop.
		return false, nil //nolint:nilerr // unparseable endpoint fails later, loudly
	}
	if listenPort != exportPort {
		return false, nil
	}

	exportIPs, err := lookupIPs(exportHost)
	if err != nil || len(exportIPs) == 0 {
		// Unresolvable: see the doc comment on CheckNoSelfExport.
		return false, nil //nolint:nilerr // unresolvable host cannot be our socket
	}

	listenIPs, err := lookupIPs(listenHost)
	if err != nil {
		return false, fmt.Errorf("resolve receiver host %q: %w", listenHost, err)
	}

	// An unspecified listening address accepts on every local address, so the
	// question becomes whether the export target is one of ours at all.
	for _, lip := range listenIPs {
		if lip.IsUnspecified() {
			return anyIsLocal(exportIPs)
		}
	}

	for _, lip := range listenIPs {
		for _, eip := range exportIPs {
			if lip.Equal(eip) {
				return true, nil
			}
		}
	}

	// Loopback addresses are treated as one equivalence class, deliberately
	// wider than IP equality.
	//
	// ⚠️ Strictly, 127.0.0.1 and ::1 are different addresses on different
	// stacks, and a listener bound to one does not accept connections to the
	// other — so an exact reading would report no collision here. ADR 0008 §3
	// nonetheless names "localhost:4317", "127.0.0.1:4317" and "[::1]:4317" as
	// one socket, and the wider reading is the safe one in both directions:
	//
	//   - A missed collision is a silent feedback loop that re-ingests every
	//     record forever, and it looks like unusually busy telemetry.
	//   - A false refusal is one loud error naming both addresses, fixable by
	//     moving either. The only configuration it wrongly rejects is a
	//     downstream collector that listens on the *other* loopback stack, on
	//     the same port, on this same machine.
	//
	// The failure modes are not symmetric, so this does not sit on the exact
	// reading.
	if anyLoopback(listenIPs) && anyLoopback(exportIPs) {
		return true, nil
	}
	return false, nil
}

// anyLoopback reports whether any address in ips is a loopback address.
func anyLoopback(ips []net.IP) bool {
	for _, ip := range ips {
		if ip.IsLoopback() {
			return true
		}
	}
	return false
}

// lookupIPs resolves a host to IP addresses. An empty host means the unspecified
// address, which is how net.SplitHostPort renders ":4317".
func lookupIPs(host string) ([]net.IP, error) {
	if host == "" {
		return []net.IP{net.IPv4zero}, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return net.LookupIP(host)
}

// anyIsLocal reports whether any of ips is an address this host answers on:
// loopback, or an address assigned to one of its interfaces.
func anyIsLocal(ips []net.IP) (bool, error) {
	for _, ip := range ips {
		if ip.IsLoopback() {
			return true, nil
		}
	}
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return false, fmt.Errorf("enumerate interface addresses: %w", err)
	}
	for _, a := range ifaceAddrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		for _, ip := range ips {
			if ipNet.IP.Equal(ip) {
				return true, nil
			}
		}
	}
	return false, nil
}
