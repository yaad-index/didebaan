// Command didebaan is the Didebaan telemetry-collector CLI.
//
// Didebaan reads an AI coding agent's activity through a per-agent input adapter
// (ADR 0003), normalizes it into the OpenTelemetry GenAI semantic conventions
// (ADR 0002), and exports it over OTLP to a downstream collector (ADR 0004).
//
// Configuration layers as file < env < flag: a YAML config file (default
// /etc/didebaan/config.yaml or --config PATH), overridden by DIDEBAAN_* env
// variables, overridden by command-line flags. See config.go for the mechanism
// and the adr/ directory for the design. cmd/didebaan stays thin (ADR 0005):
// parsing and wiring only; the logic lives in internal/.
//
// Configuration flags are global and precede the command,
// e.g. `didebaan --otlp-endpoint localhost:4319 --otlp-insecure collect`.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"

	"github.com/yaad-index/didebaan/internal/adapter"
	"github.com/yaad-index/didebaan/internal/exporter"
	"github.com/yaad-index/didebaan/internal/receiver"

	// Input adapters register themselves on import (ADR 0003). Add a blank
	// import here to compile a new agent's adapter into the binary.
	_ "github.com/yaad-index/didebaan/internal/claudecode"
)

// version is the build version, overridden at link time via -ldflags.
var version = "dev"

// CLI is the Didebaan command surface and its configuration. Every config value
// resolves through file < env < flag (see config.go).
type CLI struct {
	Config kong.ConfigFlag `help:"Path to a YAML config file (highest-precedence file source)." placeholder:"PATH"`

	// Collector configuration (global; file < env < flag).
	Adapter      string `help:"Input adapter to collect from (see 'didebaan adapters')." default:"claude-code"`
	OTLPEndpoint string `name:"otlp-endpoint" help:"Downstream OTLP/gRPC endpoint (host:port) to export to. Empty uses the OpenTelemetry env defaults, which resolve to localhost:4317 — the receiver's own port." placeholder:"HOST:PORT"`
	OTLPInsecure bool   `name:"otlp-insecure" help:"Use plaintext gRPC instead of TLS (e.g. a local collector on loopback)."`

	// Receiver configuration. The conventional OTLP ports belong to the
	// receiver rather than to the downstream exporter: an agent pointed at its
	// own default endpoint has to find the collector with no extra
	// configuration (ADR 0008).
	ReceiverGRPC string `name:"receiver-grpc" help:"Address the embedded OTLP/gRPC receiver listens on. Empty disables it." default:"127.0.0.1:4317" placeholder:"HOST:PORT"`
	ReceiverHTTP string `name:"receiver-http" help:"Address the embedded OTLP/HTTP receiver listens on. Empty disables it." default:"127.0.0.1:4318" placeholder:"HOST:PORT"`

	Collect  CollectCmd  `cmd:"" help:"Collect telemetry from an agent and export it over OTLP."`
	Adapters AdaptersCmd `cmd:"" help:"List the input adapters compiled into this binary."`
	Version  VersionCmd  `cmd:"" help:"Print the version and exit."`
}

// CollectCmd wires the selected input adapter to the OTLP exporter and runs
// until interrupted. Its configuration comes from the global CLI flags.
type CollectCmd struct{}

// Run builds the adapter and exporter, then runs the adapter until the process
// is signalled.
func (*CollectCmd) Run(cli *CLI) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ad, err := adapter.New(cli.Adapter, nil)
	if err != nil {
		return err
	}

	// The adapter has to be able to receive before anything else is built: an
	// adapter that reads its agent some other way would leave the receiver with
	// no consumer, and every arriving record unclaimed.
	consumer, ok := ad.(receiver.Consumer)
	if !ok {
		return fmt.Errorf("adapter %q does not ingest from the OTLP receiver", ad.Name())
	}

	rcvCfg := receiver.Config{GRPCAddr: cli.ReceiverGRPC, HTTPAddr: cli.ReceiverHTTP}

	// Refuse to start when the resolved export endpoint is one of our own
	// listening addresses, which would export every received record straight
	// back into the receiver (ADR 0008 §3). This runs before the exporter is
	// built so the refusal is the first thing that happens, at the one moment
	// someone is watching.
	if err := receiver.CheckNoSelfExport(rcvCfg.ListenAddrs(), receiver.ResolveExportEndpoints(cli.OTLPEndpoint)); err != nil {
		return err
	}

	providers, err := exporter.New(ctx, exporter.Config{
		Endpoint:       cli.OTLPEndpoint,
		Insecure:       cli.OTLPInsecure,
		ServiceVersion: version,
	})
	if err != nil {
		return fmt.Errorf("wire exporter: %w", err)
	}
	defer func() {
		// Flush on the way out with a fresh context: ctx is already cancelled
		// once we reach shutdown.
		_ = providers.Shutdown(context.Background())
	}()

	// The sink is where normalized events become OTel signals (traces, metrics,
	// logs).
	sink, err := newSink(providers)
	if err != nil {
		return fmt.Errorf("wire sink: %w", err)
	}

	rcv, err := receiver.New(rcvCfg, providers.Meter.Meter(instrumentationScope), consumer)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stderr, "didebaan: collecting from %q, receiving OTLP on %v, exporting over OTLP\n",
		ad.Name(), rcvCfg.ListenAddrs())

	// The adapter installs the sink and holds it for the receiver's lifetime, so
	// it has to be running before the receiver accepts anything. Both stop on
	// ctx; the receiver additionally needs its own Shutdown to unblock Serve.
	adapterErr := make(chan error, 1)
	go func() { adapterErr <- ad.Run(ctx, sink) }()

	go func() {
		<-ctx.Done()
		// A fresh context: ctx is already cancelled by the time we get here.
		_ = rcv.Shutdown(context.Background())
	}()

	if err := rcv.Start(ctx); err != nil {
		return fmt.Errorf("receiver: %w", err)
	}
	if err := <-adapterErr; err != nil && ctx.Err() == nil {
		return fmt.Errorf("adapter %q: %w", ad.Name(), err)
	}

	st := rcv.Stats()
	_, _ = fmt.Fprintf(os.Stderr,
		"didebaan: received %d metrics (%d unclaimed), %d log records (%d unclaimed), ignored %d spans\n",
		st.MetricsClaimed.Load(), st.MetricsUnclaimed.Load(),
		st.LogsClaimed.Load(), st.LogsUnclaimed.Load(),
		st.SpansIgnored.Load())
	return nil
}

// AdaptersCmd lists the registered input adapters.
type AdaptersCmd struct{}

// Run prints the adapter names compiled into this binary.
func (*AdaptersCmd) Run() error {
	for _, name := range adapter.Names() {
		fmt.Println(name)
	}
	return nil
}

// VersionCmd prints the build version.
type VersionCmd struct{}

// Run prints the version.
func (*VersionCmd) Run() error {
	fmt.Println(version)
	return nil
}

func main() {
	var cli CLI
	parser, err := kong.New(&cli, kongOptions(resolveConfigPath(os.Args[1:]))...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "didebaan:", err)
		os.Exit(1)
	}
	kctx, err := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(err)
	// Bind the root CLI so commands can read the global collector configuration.
	kctx.FatalIfErrorf(kctx.Run(&cli))
}
