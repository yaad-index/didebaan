package receiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelfExportDetectedAcrossSpellings is the point of comparing resolved
// addresses rather than strings: these three spell one socket and compare
// unequal as text, so a string check would pass every one of them.
func TestSelfExportDetectedAcrossSpellings(t *testing.T) {
	for _, export := range []string{"localhost:4317", "127.0.0.1:4317", "[::1]:4317"} {
		t.Run(export, func(t *testing.T) {
			err := CheckNoSelfExport(
				[]string{"127.0.0.1:4317"},
				map[string]string{"traces": export},
			)
			require.Error(t, err, "exporting to %s while listening on 127.0.0.1:4317 is a loop", export)
			assert.Contains(t, err.Error(), "refusing to start")
		})
	}
}

// TestDifferentPortIsNotACollision keeps the guard from refusing a legitimate
// downstream that happens to be on the same host.
func TestDifferentPortIsNotACollision(t *testing.T) {
	require.NoError(t, CheckNoSelfExport(
		[]string{"127.0.0.1:4317"},
		map[string]string{"traces": "127.0.0.1:4319"},
	))
}

// TestUnspecifiedListenAddressUsesContainment covers the all-interfaces opt-in,
// where the right relation stops being equality: a socket on 0.0.0.0 accepts on
// every local address, so exporting to loopback still loops.
func TestUnspecifiedListenAddressUsesContainment(t *testing.T) {
	err := CheckNoSelfExport(
		[]string{"0.0.0.0:4317"},
		map[string]string{"metrics": "127.0.0.1:4317"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to start")
}

// TestUnresolvableExportHostIsNotACollision: nothing can connect to a name that
// does not resolve, so it cannot be our own socket. Refusing to start over an
// unreachable downstream would be the worse failure.
func TestUnresolvableExportHostIsNotACollision(t *testing.T) {
	require.NoError(t, CheckNoSelfExport(
		[]string{"127.0.0.1:4317"},
		map[string]string{"logs": "no-such-host.invalid:4317"},
	))
}

// TestEmptyEndpointResolvesToTheSDKDefault is the case ADR 0008 calls
// "compliance on paper, loop in practice": removing the configured endpoint
// looks like the minimal fix, and lands straight back on the receiver's port.
func TestEmptyEndpointResolvesToTheSDKDefault(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")

	endpoints := ResolveExportEndpoints("")
	for signal, ep := range endpoints {
		assert.Equal(t, sdkDefaultEndpoint, ep, "signal %s", signal)
	}

	require.Error(t, CheckNoSelfExport([]string{"127.0.0.1:4317"}, endpoints))
}

// TestEnvironmentEndpointIsResolved is the second way ADR 0008 describes the
// loop arriving: the agent is pointed at the collector by exporting
// OTEL_EXPORTER_OTLP_ENDPOINT, and any process inheriting that environment
// resolves to the receiver through the same fallback. Nothing in any reviewed
// file would show it.
func TestEnvironmentEndpointIsResolved(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")

	endpoints := ResolveExportEndpoints("")
	assert.Equal(t, "localhost:4317", endpoints["traces"])

	require.Error(t, CheckNoSelfExport([]string{"127.0.0.1:4317"}, endpoints))
}

// TestPerSignalEndpointIsResolved: the per-signal variables can point one signal
// somewhere else, and a loop on any one of them is a loop.
func TestPerSignalEndpointIsResolved(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://downstream.example:4319")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://127.0.0.1:4317")

	endpoints := ResolveExportEndpoints("")
	assert.Equal(t, "downstream.example:4319", endpoints["traces"])
	assert.Equal(t, "127.0.0.1:4317", endpoints["logs"])

	err := CheckNoSelfExport([]string{"127.0.0.1:4317"}, endpoints)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logs")
}

// TestConfiguredEndpointOverridesEnvironment mirrors the SDK's own precedence,
// so the guard does not refuse a configuration the exporter would not have used.
func TestConfiguredEndpointOverridesEnvironment(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")

	endpoints := ResolveExportEndpoints("downstream.example:4319")
	for _, ep := range endpoints {
		assert.Equal(t, "downstream.example:4319", ep)
	}
	require.NoError(t, CheckNoSelfExport([]string{"127.0.0.1:4317"}, endpoints))
}

// TestSchemeQualifiedEndpointDefaultsToTheGRPCPort covers an endpoint given as a
// URL with no explicit port.
//
// The scheme selects TLS; the port distinguishes the OTLP transports. Deriving
// 4318 from "https" conflates them, and this project's exporters are gRPC-only,
// so the real connection goes to 4317 regardless. The failure direction is what
// makes it worth a test: a guard resolving the wrong port compares the wrong
// address and misses a real loop, rather than raising a false alarm someone
// would notice.
func TestSchemeQualifiedEndpointDefaultsToTheGRPCPort(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", scheme+"://localhost")
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")

			endpoints := ResolveExportEndpoints("")
			for signal, ep := range endpoints {
				assert.Equal(t, "localhost:4317", ep, "signal %s", signal)
			}

			// With the HTTP receiver disabled, only a correctly resolved port
			// catches this loop.
			require.Error(t, CheckNoSelfExport([]string{"127.0.0.1:4317"}, endpoints),
				"the gRPC exporter reaches :4317, which is the receiver's own address")
		})
	}
}
