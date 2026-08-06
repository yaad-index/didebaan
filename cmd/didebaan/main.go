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
// e.g. `didebaan --otlp-endpoint localhost:4317 --otlp-insecure collect`.
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
	OTLPEndpoint string `name:"otlp-endpoint" help:"OTLP/gRPC endpoint (host:port). Empty uses the OpenTelemetry env defaults." placeholder:"HOST:PORT"`
	OTLPInsecure bool   `name:"otlp-insecure" help:"Use plaintext gRPC instead of TLS (e.g. a local collector on loopback)."`

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

	fmt.Fprintf(os.Stderr, "didebaan: collecting from %q, exporting over OTLP\n", ad.Name())

	// The sink is where normalized events become OTel signals (traces, metrics,
	// logs). The stub adapter emits nothing yet, so this simply blocks until
	// interrupted.
	sink, err := newSink(providers)
	if err != nil {
		return fmt.Errorf("wire sink: %w", err)
	}
	if err := ad.Run(ctx, sink); err != nil && ctx.Err() == nil {
		return fmt.Errorf("adapter %q: %w", ad.Name(), err)
	}
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
