// Package receiver is the collector's embedded OTLP receiver: the ingest side of
// Didebaan (ADR 0008). Agents that export OpenTelemetry natively are pointed at
// it, and each arriving record is dispatched to the adapter that claims it.
//
// The receiver is owned by the collector rather than by any one adapter, and
// there is exactly one per process. ADR 0003 commits to several adapters running
// at once, so a receiver per adapter would have N adapters contending for one
// port and failing at run time rather than at configuration time.
//
// Receiving is inherently read-only, so ADR 0007's posture holds here with no
// extra machinery: nothing in this package writes to an agent.
package receiver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/yaad-index/didebaan/pkg/didebaan"
)

// Conventional OTLP ports. They belong to the receiver rather than to the
// downstream exporter because an agent's own defaults are the ones we do not
// control: an agent pointed at its default endpoint must find the collector with
// no extra configuration (ADR 0008).
const (
	DefaultGRPCAddr = "127.0.0.1:4317"
	DefaultHTTPAddr = "127.0.0.1:4318"
)

// OTLP/HTTP request paths, fixed by the specification.
const (
	pathMetrics = "/v1/metrics"
	pathLogs    = "/v1/logs"
	pathTraces  = "/v1/traces"
)

// contentTypeProtobuf is the only encoding this receiver accepts. OTLP also
// defines a JSON encoding; see serveSignal for why it is refused rather than
// quietly ignored.
const contentTypeProtobuf = "application/x-protobuf"

// shutdownGrace bounds how long the HTTP server is given to finish in-flight
// requests before it is closed outright.
const shutdownGrace = 5 * time.Second

// Config selects where the receiver listens. Both addresses default to loopback:
// binding all interfaces on a developer machine is an accidental exposure, and
// ADR 0007's minimal-surface posture should hold for ingest too. Listening more
// widely is an explicit operator choice, made by setting the address.
//
// An empty address disables that transport. Disabling both is a configuration
// error rather than a silent no-op: a collector that receives nothing cannot
// collect anything.
type Config struct {
	GRPCAddr string
	HTTPAddr string
}

// ListenAddrs returns the addresses the receiver will bind, skipping disabled
// transports. It is what the self-export check compares the resolved export
// endpoints against.
func (c Config) ListenAddrs() []string {
	addrs := make([]string, 0, 2)
	if c.GRPCAddr != "" {
		addrs = append(addrs, c.GRPCAddr)
	}
	if c.HTTPAddr != "" {
		addrs = append(addrs, c.HTTPAddr)
	}
	return addrs
}

// MetricRecord is one metric together with the context needed to interpret it.
// The resource and scope travel with the metric because an adapter needs both —
// the resource carries the identity of the process that produced the telemetry,
// and the scope names the instrumentation that emitted it.
type MetricRecord struct {
	Resource *resourcepb.Resource
	Scope    *commonpb.InstrumentationScope
	Metric   *metricspb.Metric
}

// LogRecord is one log record with its resource and scope.
type LogRecord struct {
	Resource *resourcepb.Resource
	Scope    *commonpb.InstrumentationScope
	Log      *logspb.LogRecord
}

// SpanRecord is one span with its resource and scope.
type SpanRecord struct {
	Resource *resourcepb.Resource
	Scope    *commonpb.InstrumentationScope
	Span     *tracepb.Span
}

// Consumer is an adapter's ingest side. An adapter implements it to receive the
// records its [didebaan.Claim] matches; the receiver never calls a consumer with
// a record it did not claim.
//
// Calls may arrive concurrently from several export requests, so an
// implementation must be safe for concurrent use.
type Consumer interface {
	didebaan.Claimer

	// Name identifies the consuming adapter, for diagnostics.
	Name() string

	// ConsumeMetric handles one claimed metric.
	ConsumeMetric(ctx context.Context, rec MetricRecord) error

	// ConsumeLog handles one claimed log record.
	ConsumeLog(ctx context.Context, rec LogRecord) error

	// ConsumeSpan handles one claimed span. It is only called when span ingest
	// is enabled; see [Config] and ADR 0008 §4.
	ConsumeSpan(ctx context.Context, rec SpanRecord) error
}

// Stats counts what the receiver did with arriving records. Unclaimed records
// are counted rather than merely dropped: a silent drop and a working ingest
// look identical from outside, and the whole point of refusing to guess an owner
// is that the gap stays visible.
type Stats struct {
	MetricsClaimed   atomic.Int64
	MetricsUnclaimed atomic.Int64
	LogsClaimed      atomic.Int64
	LogsUnclaimed    atomic.Int64
	SpansIgnored     atomic.Int64
}

// Receiver serves OTLP over gRPC and HTTP and dispatches what it receives to the
// claiming adapter.
type Receiver struct {
	cfg       Config
	consumers []Consumer
	stats     Stats

	// unclaimed counts unclaimed records as an OpenTelemetry metric, so the gap
	// is visible in the same place as everything else the collector reports.
	unclaimed metric.Int64Counter

	grpcServer *grpc.Server
	httpServer *http.Server

	// Receiver serves the metrics service directly; the logs and trace services
	// are served by the small adapter types below, because all three OTLP
	// services declare a method named Export with different signatures and one
	// type cannot satisfy all three at once.
	colmetricspb.UnimplementedMetricsServiceServer
}

// New builds a receiver serving the given consumers. Passing no consumer is a
// configuration error: the receiver would accept every record and claim none.
func New(cfg Config, m metric.Meter, consumers ...Consumer) (*Receiver, error) {
	if cfg.GRPCAddr == "" && cfg.HTTPAddr == "" {
		return nil, errors.New("receiver: both gRPC and HTTP addresses are empty, so nothing would be received")
	}
	if len(consumers) == 0 {
		return nil, errors.New("receiver: no consumers, so every record would be unclaimed")
	}
	// ADR 0008 §2: every signal is claimed by instrumentation scope, and a name
	// prefix is an additional claim rather than a sole one.
	//
	// This is validated rather than documented because the failure it prevents is
	// silent. A Claim declaring only MetricPrefixes dispatches correctly for as
	// long as the agent namespaces everything, and stops matching the moment it
	// meets one that does not — which is the case the rule exists for, and the
	// case where the missing records are the token and cost figures. Nothing
	// downstream reports a gap: the records are counted as unclaimed and the
	// health counters stay green.
	for _, c := range consumers {
		if len(c.Claim().ScopePrefixes) == 0 {
			return nil, fmt.Errorf(
				"receiver: adapter %q declares no ScopePrefixes; a name prefix cannot be an adapter's only claim, "+
					"because an agent may emit records with no namespace at all (ADR 0008 §2)", c.Name())
		}
	}
	unclaimed, err := m.Int64Counter(
		"didebaan.receiver.unclaimed",
		metric.WithUnit("{record}"),
		metric.WithDescription("Records received that no adapter claimed, by signal."),
	)
	if err != nil {
		return nil, fmt.Errorf("create unclaimed counter: %w", err)
	}
	return &Receiver{cfg: cfg, consumers: consumers, unclaimed: unclaimed}, nil
}

// Stats exposes the receiver's counters.
func (r *Receiver) Stats() *Stats { return &r.stats }

// Start binds the configured listeners and serves until Shutdown is called. It
// returns once both servers have stopped, joining their errors.
//
// Binding happens before serving so that a port already in use is reported from
// Start rather than from a background goroutine nobody is watching.
func (r *Receiver) Start(ctx context.Context) error {
	var (
		grpcLis net.Listener
		httpLis net.Listener
		err     error
	)
	if r.cfg.GRPCAddr != "" {
		if grpcLis, err = net.Listen("tcp", r.cfg.GRPCAddr); err != nil {
			return fmt.Errorf("receiver: listen gRPC on %s: %w", r.cfg.GRPCAddr, err)
		}
	}
	if r.cfg.HTTPAddr != "" {
		if httpLis, err = net.Listen("tcp", r.cfg.HTTPAddr); err != nil {
			if grpcLis != nil {
				_ = grpcLis.Close()
			}
			return fmt.Errorf("receiver: listen HTTP on %s: %w", r.cfg.HTTPAddr, err)
		}
	}

	errCh := make(chan error, 2)
	running := 0

	if grpcLis != nil {
		r.grpcServer = grpc.NewServer()
		colmetricspb.RegisterMetricsServiceServer(r.grpcServer, r)
		collogspb.RegisterLogsServiceServer(r.grpcServer, logsService{r: r})
		coltracepb.RegisterTraceServiceServer(r.grpcServer, traceService{r: r})
		running++
		go func() { errCh <- r.grpcServer.Serve(grpcLis) }()
	}
	if httpLis != nil {
		mux := http.NewServeMux()
		mux.HandleFunc(pathMetrics, r.handleMetrics)
		mux.HandleFunc(pathLogs, r.handleLogs)
		mux.HandleFunc(pathTraces, r.handleTraces)
		r.httpServer = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		running++
		go func() {
			serveErr := r.httpServer.Serve(httpLis)
			if errors.Is(serveErr, http.ErrServerClosed) {
				serveErr = nil
			}
			errCh <- serveErr
		}()
	}

	var errs []error
	for i := 0; i < running; i++ {
		if e := <-errCh; e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}

// Shutdown stops both servers. In-flight requests get a short grace period; the
// receiver holds no durable buffer, so there is nothing to flush beyond them.
func (r *Receiver) Shutdown(ctx context.Context) error {
	var errs []error
	if r.httpServer != nil {
		graceCtx, cancel := context.WithTimeout(ctx, shutdownGrace)
		defer cancel()
		if err := r.httpServer.Shutdown(graceCtx); err != nil {
			errs = append(errs, err)
		}
	}
	if r.grpcServer != nil {
		r.grpcServer.GracefulStop()
	}
	return errors.Join(errs...)
}

// --- gRPC service implementations ---

// Export receives metrics over gRPC.
func (r *Receiver) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	r.dispatchMetrics(ctx, req.GetResourceMetrics())
	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

// logsService and traceService adapt the two remaining gRPC services. They are
// separate types because all three OTLP services declare a method named Export
// with different signatures, which one type cannot satisfy at once.
type logsService struct {
	collogspb.UnimplementedLogsServiceServer
	r *Receiver
}

func (s logsService) Export(ctx context.Context, req *collogspb.ExportLogsServiceRequest) (*collogspb.ExportLogsServiceResponse, error) {
	s.r.dispatchLogs(ctx, req.GetResourceLogs())
	return &collogspb.ExportLogsServiceResponse{}, nil
}

type traceService struct {
	coltracepb.UnimplementedTraceServiceServer
	r *Receiver
}

func (s traceService) Export(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	s.r.dispatchSpans(ctx, req.GetResourceSpans())
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

// --- HTTP handlers ---

func (r *Receiver) handleMetrics(w http.ResponseWriter, req *http.Request) {
	var msg colmetricspb.ExportMetricsServiceRequest
	if !r.serveSignal(w, req, &msg) {
		return
	}
	r.dispatchMetrics(req.Context(), msg.GetResourceMetrics())
	writeProto(w, &colmetricspb.ExportMetricsServiceResponse{})
}

func (r *Receiver) handleLogs(w http.ResponseWriter, req *http.Request) {
	var msg collogspb.ExportLogsServiceRequest
	if !r.serveSignal(w, req, &msg) {
		return
	}
	r.dispatchLogs(req.Context(), msg.GetResourceLogs())
	writeProto(w, &collogspb.ExportLogsServiceResponse{})
}

func (r *Receiver) handleTraces(w http.ResponseWriter, req *http.Request) {
	var msg coltracepb.ExportTraceServiceRequest
	if !r.serveSignal(w, req, &msg) {
		return
	}
	r.dispatchSpans(req.Context(), msg.GetResourceSpans())
	writeProto(w, &coltracepb.ExportTraceServiceResponse{})
}

// serveSignal performs the checks common to every OTLP/HTTP request and decodes
// the body into msg. It reports whether the caller should continue.
//
// OTLP defines both a protobuf and a JSON encoding; this receiver implements
// protobuf and refuses JSON with 415 rather than accepting the request and
// dropping it. An accept-and-drop is indistinguishable from working ingest at
// the sender, so the operator would see a healthy exporter and no data.
func (r *Receiver) serveSignal(w http.ResponseWriter, req *http.Request, msg proto.Message) bool {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "OTLP/HTTP requires POST", http.StatusMethodNotAllowed)
		return false
	}
	if ct := req.Header.Get("Content-Type"); !isProtobuf(ct) {
		http.Error(w,
			fmt.Sprintf("unsupported Content-Type %q: this receiver implements OTLP/HTTP with %s only, not the JSON encoding", ct, contentTypeProtobuf),
			http.StatusUnsupportedMediaType)
		return false
	}
	body, err := readLimited(req)
	if err != nil {
		http.Error(w, "read request body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	if err := proto.Unmarshal(body, msg); err != nil {
		http.Error(w, "decode OTLP protobuf: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeProto(w http.ResponseWriter, msg proto.Message) {
	body, err := proto.Marshal(msg)
	if err != nil {
		http.Error(w, "encode OTLP response: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentTypeProtobuf)
	// The write error says nothing actionable — the client is already gone.
	_, _ = w.Write(body)
}
