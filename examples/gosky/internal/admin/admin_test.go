package admin

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/imajinyun/gofly/core/controlplane"
	apprpc "github.com/imajinyun/gofly/examples/gosky/internal/api/grpc/v1"
	appconfig "github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rpc"
)

func TestAdminDiagnostics(t *testing.T) {
	cfg := appconfig.Config{RPC: appconfig.RPCConfig{Mux: appconfig.RPCMuxConfig{Enabled: true, Probe: true, IdleTimeout: time.Nanosecond, MaxOpenRetries: 1, OpenRetryReasons: []string{"dial_failure", "pool_exhausted"}, HealthBackoffMultiplier: 2, HealthMaxCooldown: 30 * time.Second, Trace: appconfig.RPCMuxTraceConfig{Enabled: true, AnnotateStreams: true}, Log: appconfig.RPCMuxLogConfig{Enabled: true, Diagnosis: true, ExportEvents: true, EventFamily: "retry", Event: "open-before-retry"}, Candidate: appconfig.RPCMuxCandidateConfig{Enabled: true, Protocol: "gofly-mux/generated-candidate-test", KeepaliveInterval: time.Hour, KeepaliveIdle: 2 * time.Hour, MaxFrameBytes: 256, MaxMessageBytes: 1024, MaxConcurrentStreams: 8, ReceiveQueueSize: 2, ConnectionWindow: 3, FragmentStreamWindowUpdatePolicy: "on_receive", FragmentConnectionWindowUpdatePolicy: "on_receive", FragmentStreamWindowRefillRatio: 0.5, FragmentConnectionWindowRefillRatio: 0.25, FragmentMaxDeferredFragments: 2, FragmentWindowPolicyRiskMode: "warn", PayloadCodec: "identity", FrameCodec: "binary"}}}}
	clientConn, serverConn := net.Pipe()
	muxClient := rpc.NewExperimentalMuxClientAdapter(clientConn)
	muxServer := rpc.NewExperimentalMuxServerAdapter(serverConn)
	defer func() { _ = muxClient.Close() }()
	defer func() { _ = muxServer.Close() }()
	if err := muxServer.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("generated:"), msg.Payload...)}); err != nil {
			return err
		}
		return stream.Close(ctx, "ok")
	}); err != nil {
		t.Fatal(err)
	}
	muxCtx, stopMux := context.WithCancel(context.Background())
	defer stopMux()
	muxDone := make(chan error, 1)
	go func() {
		muxDone <- muxServer.Serve(muxCtx)
	}()
	muxStream, err := muxClient.OpenStream(context.Background(), "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	if err := muxStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	got, err := muxStream.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != "generated:probe" {
		t.Fatalf("mux payload = %q, want generated probe response", got.Payload)
	}
	if _, err := muxStream.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("mux terminal receive = %v, want EOF", err)
	}

	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.RPC.Mux.Endpoints = []string{"tcp://" + tcpListener.Addr().String()}
	tcpCtx, stopTCP := context.WithCancel(context.Background())
	defer stopTCP()
	tcpDone := make(chan error, 1)
	go func() {
		tcpDone <- rpc.ServeExperimentalMuxListener(tcpCtx, tcpListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("manager:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()
	manager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver(cfg.RPC.Mux.Endpoints...),
		rpc.WithExperimentalMuxConnectionManagerIdleTimeout(cfg.RPC.Mux.IdleTimeout),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(cfg.RPC.Mux.MaxOpenRetries),
		rpc.WithExperimentalMuxConnectionManagerOpenRetryReasons(cfg.RPC.Mux.OpenRetryReasons...),
	)
	if err != nil {
		t.Fatal(err)
	}
	muxRPCClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = muxRPCClient.Close() }()
	managerStream, err := muxRPCClient.MuxStream(context.Background(), "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	if err := managerStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	managerResponse, err := managerStream.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(managerResponse.Payload) != "manager:probe" {
		t.Fatalf("manager mux payload = %q, want manager probe response", managerResponse.Payload)
	}
	if _, err := managerStream.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("manager mux terminal receive = %v, want EOF", err)
	}
	if diagnosis := muxRPCClient.RuntimeSnapshot().Diagnosis.Mux.Manager; !diagnosis.Enabled || len(diagnosis.Endpoints) != 1 {
		t.Fatalf("manager diagnosis = %+v, want generated mux manager evidence", diagnosis)
	}
	time.Sleep(time.Millisecond)
	if err := manager.CloseIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopTCP()
	if err := <-tcpDone; err != nil {
		t.Fatalf("tcp mux server stopped with error: %v", err)
	}

	candidateListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	candidateCtx, stopCandidate := context.WithCancel(context.Background())
	defer stopCandidate()
	candidateDone := make(chan error, 1)
	candidateServerCfg := cfg.RPC.Mux.CandidateServerConfig()
	candidateClientCfg := cfg.RPC.Mux.CandidateClientConfig()
	go func() {
		candidateDone <- rpc.ServeExperimentalMuxCandidateListener(candidateCtx, candidateListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("candidate:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		}, candidateServerCfg)
	}()
	candidateManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+candidateListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(candidateClientCfg),
	)
	if err != nil {
		t.Fatal(err)
	}
	candidateClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(candidateManager))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = candidateClient.Close() }()
	candidateStream, err := candidateClient.MuxStream(context.Background(), "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	if err := candidateStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	candidateResponse, err := candidateStream.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(candidateResponse.Payload) != "candidate:probe" {
		t.Fatalf("candidate mux payload = %q, want candidate probe response", candidateResponse.Payload)
	}
	if _, err := candidateStream.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("candidate mux terminal receive = %v, want EOF", err)
	}
	if diagnosis := candidateClient.RuntimeSnapshot().Diagnosis.Mux.Manager; !diagnosis.Candidate.Enabled || diagnosis.Candidate.Protocol != "gofly-mux/generated-candidate-test" || diagnosis.Candidate.FrameCodec != "binary" || diagnosis.Candidate.FragmentStreamWindowUpdatePolicy != "on_receive" || diagnosis.Candidate.FragmentConnectionWindowUpdatePolicy != "on_receive" || diagnosis.Candidate.FragmentStreamWindowRefillRatio != 0.5 || diagnosis.Candidate.FragmentConnectionWindowRefillRatio != 0.25 || diagnosis.Candidate.FragmentMaxDeferredFragments != 2 || diagnosis.Candidate.FragmentWindowPolicyRiskMode != "warn" || !diagnosis.Candidate.FragmentWindowPolicyRiskWarning || !diagnosis.Candidate.FragmentWindowPolicyRisk || diagnosis.Candidate.FragmentEstimatedMaxFragments <= diagnosis.Candidate.ConnectionWindow || len(diagnosis.Endpoints) != 1 || !diagnosis.Endpoints[0].Adapter.Candidate.Enabled || diagnosis.Endpoints[0].Adapter.Transport.ConnectionWindow != 3 || diagnosis.Endpoints[0].Adapter.Transport.FragmentStreamWindowUpdatePolicy != "on_receive" || diagnosis.Endpoints[0].Adapter.Transport.FragmentConnectionWindowUpdatePolicy != "on_receive" || diagnosis.Endpoints[0].Adapter.Transport.FragmentStreamWindowRefillRatio != 0.5 || diagnosis.Endpoints[0].Adapter.Transport.FragmentConnectionWindowRefillRatio != 0.25 || diagnosis.Endpoints[0].Adapter.Transport.FragmentMaxDeferredFragments != 2 || diagnosis.Endpoints[0].Adapter.Transport.FragmentWindowPolicyRiskMode != "warn" || !diagnosis.Endpoints[0].Adapter.Transport.FragmentWindowPolicyRisk || diagnosis.Endpoints[0].Adapter.Transport.FragmentWindowPolicyRiskReason == "" {
		t.Fatalf("candidate manager diagnosis = %+v, want generated candidate mux evidence", diagnosis)
	}
	waitMuxManagerStreamsClosed(t, candidateClient)
	if err := candidateManager.Drain(context.Background(), "generated_shutdown"); err != nil {
		t.Fatal(err)
	}
	if diagnosis := candidateClient.RuntimeSnapshot().Diagnosis.Mux.Manager; len(diagnosis.Endpoints) != 1 || diagnosis.Endpoints[0].Adapter.Transport.GoAwayFramesOut != 1 {
		t.Fatalf("candidate drain diagnosis = %+v, want generated candidate GOAWAY evidence", diagnosis)
	}
	stopCandidate()
	if err := <-candidateDone; err != nil {
		t.Fatalf("candidate mux server stopped with error: %v", err)
	}

	negotiationListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	negotiationCtx, stopNegotiation := context.WithCancel(context.Background())
	defer stopNegotiation()
	negotiationDone := make(chan error, 1)
	negotiationServerCfg := cfg.RPC.Mux.CandidateServerConfig()
	negotiationServerCfg.FrameCodec = "json"
	go func() {
		negotiationDone <- rpc.ServeExperimentalMuxCandidateListener(negotiationCtx, negotiationListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				return stream.Close(ctx, "unexpected")
			})
		}, negotiationServerCfg)
	}()
	negotiationManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+negotiationListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(cfg.RPC.Mux.CandidateClientConfig()),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	negotiationClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(negotiationManager))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := negotiationClient.MuxStream(context.Background(), "greeter/Watch"); err == nil || !strings.Contains(err.Error(), "frame codec") {
		t.Fatalf("negotiation mux stream = %v, want frame policy mismatch", err)
	}
	negotiationRec := httptest.NewRecorder()
	negotiationClient.DiagnosisHandler().ServeHTTP(negotiationRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?eventFamily=negotiation&event=frame-policy-mismatch", nil))
	if negotiationRec.Code != http.StatusOK {
		t.Fatalf("negotiation diagnosis status = %d body=%q", negotiationRec.Code, negotiationRec.Body.String())
	}
	var negotiationDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(negotiationRec.Body).Decode(&negotiationDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !negotiationDiagnosis.Matched ||
		negotiationDiagnosis.Diagnosis.Mux.Negotiation.Failures != 1 ||
		negotiationDiagnosis.Diagnosis.Mux.Negotiation.FramePolicyMismatch != 1 ||
		negotiationDiagnosis.Diagnosis.Mux.Negotiation.LastEvent != "frame_policy_mismatch" ||
		len(negotiationDiagnosis.Diagnosis.Mux.Events) != 1 ||
		negotiationDiagnosis.Diagnosis.Mux.Events[0].Event != "frame_policy_mismatch" {
		t.Fatalf("negotiation diagnosis = %+v, want generated frame policy summary evidence", negotiationDiagnosis)
	}
	if err := negotiationClient.Close(); err != nil {
		t.Fatal(err)
	}
	stopNegotiation()
	if err := <-negotiationDone; err != nil {
		t.Fatalf("negotiation mux server stopped with error: %v", err)
	}

	tlsDir := t.TempDir()
	tlsCA, tlsCAKey := generatedRPCTLSCA(t, tlsDir)
	tlsCAFile := filepath.Join(tlsDir, "ca.crt")
	tlsServerCert, tlsServerKey := generatedRPCTLSLeaf(t, tlsDir, "server", tlsCA, tlsCAKey)
	tlsClientCert, tlsClientKey := generatedRPCTLSLeaf(t, tlsDir, "client", tlsCA, tlsCAKey)
	tlsCfg := cfg
	tlsCfg.RPC.Mux.TLS = appconfig.RPCMuxTLSConfig{
		Enabled:    true,
		CertFile:   tlsServerCert,
		KeyFile:    tlsServerKey,
		CAFile:     tlsCAFile,
		ServerName: "svc",
	}
	tlsCfg.RPC.Mux.MutualTLS = appconfig.RPCMuxMutualTLSConfig{
		Enabled:        true,
		ClientCAFile:   tlsCAFile,
		ClientCertFile: tlsClientCert,
		ClientKeyFile:  tlsClientKey,
	}
	tlsCfg.RPC.Mux.ALPN = appconfig.RPCMuxALPNConfig{Enabled: true, Protocol: "gofly-mux/generated-mtls-test"}
	tlsCfg.RPC.Mux.Trace = appconfig.RPCMuxTraceConfig{Enabled: true, AnnotateStreams: true}
	tlsCfg.RPC.Mux.Log = appconfig.RPCMuxLogConfig{Enabled: true, Diagnosis: true, ExportEvents: true, EventFamily: "flow-control", Event: "fragment-window-refill"}
	tlsCfg.RPC.Mux.Log.OTelCompatible = appconfig.RPCMuxOTelCompatibleLogConfig{Enabled: true, Sink: "slog", Profile: "generated-mtls-refill"}
	tlsCandidate := tlsCfg.RPC.Mux.CandidateClientConfig()
	if tlsCandidate.Protocol != "gofly-mux/generated-mtls-test" ||
		tlsCandidate.TLS.CAFile != tlsCAFile ||
		tlsCandidate.TLS.CertFile != tlsClientCert ||
		tlsCandidate.TLS.KeyFile != tlsClientKey ||
		tlsCandidate.TLS.ServerName != "svc" {
		t.Fatalf("generated mux client TLS config = %+v, want client mTLS + ALPN profile", tlsCandidate)
	}
	tlsServerCandidate := tlsCfg.RPC.Mux.CandidateServerConfig()
	if tlsServerCandidate.Protocol != "gofly-mux/generated-mtls-test" ||
		tlsServerCandidate.TLS.CertFile != tlsServerCert ||
		tlsServerCandidate.TLS.KeyFile != tlsServerKey ||
		tlsServerCandidate.TLS.ClientCAFile != tlsCAFile {
		t.Fatalf("generated mux server TLS config = %+v, want server mTLS + ALPN profile", tlsServerCandidate)
	}
	mtlsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mtlsCtx, stopMTLS := context.WithCancel(context.Background())
	defer stopMTLS()
	mtlsDone := make(chan error, 1)
	go func() {
		mtlsDone <- rpc.ServeExperimentalMuxCandidateListener(mtlsCtx, mtlsListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("mtls:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		}, tlsServerCandidate)
	}()
	mtlsManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+mtlsListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(tlsCandidate),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	mtlsRecorder := tracetest.NewSpanRecorder()
	mtlsProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(mtlsRecorder))
	defer func() { _ = mtlsProvider.Shutdown(context.Background()) }()
	mtlsTraceCtx, mtlsSpan := mtlsProvider.Tracer("generated-rpc-admin-smoke").Start(context.Background(), "mux-mtls-success", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
	var mtlsLogBuf bytes.Buffer
	previousMTLSLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&mtlsLogBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousMTLSLogger)
	mtlsClientOptions := append(tlsCfg.RPC.Mux.ClientOptions(),
		rpc.WithExperimentalMuxConnectionManager(mtlsManager),
	)
	mtlsClient, err := rpc.NewClient("http://unused", mtlsClientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	mtlsStream, err := mtlsClient.MuxStream(mtlsTraceCtx, "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	mtlsSpan.End()
	mtlsPayload := []byte(strings.Repeat("probe-", 50))
	mtlsIOCtx, cancelMTLSIO := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelMTLSIO()
	if err := mtlsStream.Send(mtlsIOCtx, rpc.Message{Payload: mtlsPayload}); err != nil {
		t.Fatal(err)
	}
	mtlsResponse, err := mtlsStream.Receive(mtlsIOCtx)
	if err != nil {
		t.Fatal(err)
	}
	if string(mtlsResponse.Payload) != "mtls:"+string(mtlsPayload) {
		t.Fatalf("mTLS mux payload = %q, want mtls probe response", mtlsResponse.Payload)
	}
	if _, err := mtlsStream.Receive(mtlsIOCtx); !errors.Is(err, io.EOF) {
		t.Fatalf("mTLS mux terminal receive = %v, want EOF", err)
	}
	mtlsRec := httptest.NewRecorder()
	mtlsClient.DiagnosisHandler().ServeHTTP(mtlsRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis", nil))
	if mtlsRec.Code != http.StatusOK {
		t.Fatalf("mTLS diagnosis status = %d body=%q", mtlsRec.Code, mtlsRec.Body.String())
	}
	var mtlsDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(mtlsRec.Body).Decode(&mtlsDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.TLS ||
		!mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.MutualTLS ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Candidate.NegotiatedProtocol != "gofly-mux/generated-mtls-test" ||
		len(mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints) != 1 ||
		!mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Candidate.TLS ||
		!mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Candidate.MutualTLS ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Candidate.NegotiatedProtocol != "gofly-mux/generated-mtls-test" ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.OpenedStreams != 1 ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.ClosedStreams != 1 ||
		mtlsDiagnosis.Diagnosis.Mux.Manager.Endpoints[0].Adapter.Transport.ActiveStreams != 0 {
		t.Fatalf("mTLS diagnosis = %+v, want generated TLS/mTLS negotiated lifecycle evidence", mtlsDiagnosis.Diagnosis.Mux.Manager)
	}
	refillRec := httptest.NewRecorder()
	mtlsClient.DiagnosisHandler().ServeHTTP(refillRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?flowControlEvent=fragment-window-refill&eventFamily=flow-control&event=fragment-window-refill", nil))
	if refillRec.Code != http.StatusOK {
		t.Fatalf("mTLS refill diagnosis status = %d body=%q", refillRec.Code, refillRec.Body.String())
	}
	var refillDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(refillRec.Body).Decode(&refillDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !refillDiagnosis.Matched ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.Refills < 1 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.StreamWindowRefillRatio != 0.5 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.ConnectionWindowRefillRatio != 0.25 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.MaxDeferredFragments != 2 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfile.LastFlowControlEvent != "fragment_window_refill" ||
		len(refillDiagnosis.Diagnosis.Mux.Manager.RefillProfiles) != 1 ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfiles[0].Endpoint == "" ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfiles[0].ConnectionID == "" ||
		refillDiagnosis.Diagnosis.Mux.Manager.RefillProfiles[0].PoolSlot != 1 ||
		len(refillDiagnosis.Diagnosis.Mux.Events) == 0 ||
		refillDiagnosis.Diagnosis.Mux.Events[0].Event != "fragment_window_refill" {
		t.Fatalf("mTLS refill diagnosis = %+v, want generated refillProfile admin evidence", refillDiagnosis.Diagnosis.Mux.Manager)
	}
	refillProfile := refillDiagnosis.Diagnosis.Mux.Manager.RefillProfiles[0]
	refillDiagnosis.Endpoint = refillProfile.Endpoint
	refillDiagnosis.ConnectionID = refillProfile.ConnectionID
	refillDiagnosis.PoolSlot = refillProfile.PoolSlot
	mtlsRefillTraceCtx, mtlsRefillSpan := mtlsProvider.Tracer("generated-rpc-admin-smoke").Start(context.Background(), "mux-refill-profile-diagnosis", oteltrace.WithSpanKind(oteltrace.SpanKindInternal))
	mtlsClient.ObserveMuxDiagnosis(mtlsRefillTraceCtx, refillDiagnosis)
	mtlsRefillSpan.End()
	if err := mtlsClient.Close(); err != nil {
		t.Fatal(err)
	}
	mtlsTraceSpans := mtlsRecorder.Ended()
	if len(mtlsTraceSpans) != 2 {
		t.Fatalf("mTLS trace spans = %d, want generated mux mTLS and refillProfile spans", len(mtlsTraceSpans))
	}
	mtlsTraceAttrs := generatedTraceAttributeMap(mtlsTraceSpans[0].Attributes())
	if !mtlsTraceAttrs["rpc.mux.candidate.tls"].AsBool() ||
		!mtlsTraceAttrs["rpc.mux.candidate.mutual_tls"].AsBool() ||
		mtlsTraceAttrs["rpc.mux.candidate.negotiated_protocol"].AsString() != "gofly-mux/generated-mtls-test" ||
		mtlsTraceAttrs["rpc.mux.candidate.protocol"].AsString() != "gofly-mux/generated-mtls-test" {
		t.Fatalf("mTLS trace attributes = %+v, want generated TLS/mTLS negotiated protocol", mtlsTraceAttrs)
	}
	mtlsRefillTraceAttrs := generatedTraceAttributeMap(mtlsTraceSpans[1].Attributes())
	if mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.refills.count"].AsInt64() < 1 ||
		mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.stream_window_refill_ratio"].AsFloat64() != 0.5 ||
		mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.connection_window_refill_ratio"].AsFloat64() != 0.25 ||
		mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.max_deferred_fragments"].AsInt64() != 2 ||
		mtlsRefillTraceAttrs["rpc.mux.manager.refill_profile.last_flow_control_event"].AsString() != "fragment_window_refill" ||
		mtlsRefillTraceAttrs["rpc.mux.event.flow_control.count"].AsInt64() < 1 {
		t.Fatalf("mTLS refill trace attributes = %+v, want generated refillProfile OTel attributes", mtlsRefillTraceAttrs)
	}
	mtlsLogLine := mtlsLogBuf.String()
	for _, want := range []string{
		"\"msg\":\"rpc mux stream diagnosis\"",
		"\"tls\":true",
		"\"mutual_tls\":true",
		"\"negotiated_protocol\":\"gofly-mux/generated-mtls-test\"",
		"\"candidate_protocol\":\"gofly-mux/generated-mtls-test\"",
		"\"refill_profile_stream_window_refill_ratio\":0.5",
		"\"refill_profile_connection_window_refill_ratio\":0.25",
		"\"refill_profile_max_deferred_fragments\":2",
		"\"refill_profile_last_flow_control_event\":\"fragment_window_refill\"",
		"\"msg\":\"rpc mux runtime event\"",
		"\"event_family\":\"flow_control\"",
		"\"event\":\"fragment_window_refill\"",
		"\"connection_id\":\"",
		"\"pool_slot\":1",
		"\"msg\":\"rpc mux otel log event\"",
		"\"otel_log_name\":\"rpc.mux.diagnosis_event\"",
		"\"otel_log_severity\":\"WARN\"",
		"\"otel_log_profile\":\"generated-mtls-refill\"",
		"\"rpc_mux_event_family\":\"flow_control\"",
		"\"rpc_mux_event_name\":\"fragment_window_refill\"",
		"\"rpc_mux_connection_id\":\"",
		"\"rpc_mux_pool_slot\":1",
	} {
		if !strings.Contains(mtlsLogLine, want) {
			t.Fatalf("mTLS mux diagnosis log missing %s:\n%s", want, mtlsLogLine)
		}
	}
	if err := mtlsManager.Close(); err != nil {
		t.Fatal(err)
	}
	stopMTLS()
	if err := <-mtlsDone; err != nil {
		t.Fatalf("mTLS mux server stopped with error: %v", err)
	}

	tlsServer, err := rpc.NewExperimentalMuxCandidateServer("127.0.0.1:0", func(adapter *rpc.ExperimentalMuxServerAdapter) error {
		return adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
			return stream.Close(ctx, "unexpected")
		})
	}, tlsServerCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := tlsServer.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tlsServer.Shutdown(context.Background()) }()
	tlsNoClientCert := tlsCandidate
	tlsNoClientCert.TLS.CertFile = ""
	tlsNoClientCert.TLS.KeyFile = ""
	tlsFailureManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+tlsServer.Addr()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(tlsNoClientCert),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	tlsFailureClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(tlsFailureManager))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tlsFailureClient.MuxStream(context.Background(), "greeter/Watch"); err == nil {
		t.Fatal("generated mux TLS stream without client certificate succeeded, want tls_failure")
	}
	tlsFailureRec := httptest.NewRecorder()
	tlsFailureClient.DiagnosisHandler().ServeHTTP(tlsFailureRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?eventFamily=negotiation&event=tls-failure", nil))
	if tlsFailureRec.Code != http.StatusOK {
		t.Fatalf("TLS failure diagnosis status = %d body=%q", tlsFailureRec.Code, tlsFailureRec.Body.String())
	}
	var tlsFailureDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(tlsFailureRec.Body).Decode(&tlsFailureDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !tlsFailureDiagnosis.Matched ||
		tlsFailureDiagnosis.Diagnosis.Mux.Negotiation.TLSFailure != 1 ||
		tlsFailureDiagnosis.Diagnosis.Mux.Negotiation.LastEvent != "tls_failure" ||
		len(tlsFailureDiagnosis.Diagnosis.Mux.Events) != 1 ||
		tlsFailureDiagnosis.Diagnosis.Mux.Events[0].Event != "tls_failure" {
		t.Fatalf("TLS failure diagnosis = %+v, want generated tls_failure admin evidence", tlsFailureDiagnosis)
	}
	if err := tlsFailureClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tlsFailureManager.Close(); err != nil {
		t.Fatal(err)
	}

	alpnTLSCfg, err := (tlsCfg.RPC.Mux.CandidateServerConfig().TLS).ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	alpnListener, err := tls.Listen("tcp", "127.0.0.1:0", alpnTLSCfg)
	if err != nil {
		t.Fatal(err)
	}
	alpnCtx, stopALPN := context.WithCancel(context.Background())
	defer stopALPN()
	alpnDone := make(chan error, 1)
	go func() {
		for {
			conn, err := alpnListener.Accept()
			if err != nil {
				select {
				case <-alpnCtx.Done():
					alpnDone <- nil
				default:
					alpnDone <- err
				}
				return
			}
			if tlsConn, ok := conn.(*tls.Conn); ok {
				_ = tlsConn.Handshake()
			}
			_ = conn.Close()
		}
	}()
	alpnManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver("tcp://"+alpnListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerCandidateConfig(tlsCandidate),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	alpnClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxConnectionManager(alpnManager))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alpnClient.MuxStream(context.Background(), "greeter/Watch"); err == nil || !strings.Contains(err.Error(), "negotiated protocol") {
		t.Fatalf("generated mux ALPN stream = %v, want alpn_mismatch", err)
	}
	alpnRec := httptest.NewRecorder()
	alpnClient.DiagnosisHandler().ServeHTTP(alpnRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?eventFamily=negotiation&event=alpn-mismatch", nil))
	if alpnRec.Code != http.StatusOK {
		t.Fatalf("ALPN diagnosis status = %d body=%q", alpnRec.Code, alpnRec.Body.String())
	}
	var alpnDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(alpnRec.Body).Decode(&alpnDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !alpnDiagnosis.Matched ||
		alpnDiagnosis.Diagnosis.Mux.Negotiation.ALPNMismatch != 1 ||
		alpnDiagnosis.Diagnosis.Mux.Negotiation.LastEvent != "alpn_mismatch" ||
		len(alpnDiagnosis.Diagnosis.Mux.Events) != 1 ||
		alpnDiagnosis.Diagnosis.Mux.Events[0].Event != "alpn_mismatch" {
		t.Fatalf("ALPN diagnosis = %+v, want generated alpn_mismatch admin evidence", alpnDiagnosis)
	}
	if err := alpnClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := alpnManager.Close(); err != nil {
		t.Fatal(err)
	}
	stopALPN()
	_ = alpnListener.Close()
	if err := <-alpnDone; err != nil {
		t.Fatalf("ALPN listener stopped with error: %v", err)
	}

	badMuxListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	badMuxEndpoint := "tcp://" + badMuxListener.Addr().String()
	if err := badMuxListener.Close(); err != nil {
		t.Fatal(err)
	}
	retryMuxListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	retryCtx, stopRetryMux := context.WithCancel(context.Background())
	defer stopRetryMux()
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- rpc.ServeExperimentalMuxListener(retryCtx, retryMuxListener, func(adapter *rpc.ExperimentalMuxServerAdapter) error {
			if err := adapter.RegisterStream("greeter/Watch", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, rpc.Message{Payload: append([]byte("retry:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			}); err != nil {
				return err
			}
			return adapter.RegisterStream("greeter/FailAfterOpen", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
				if _, err := stream.Receive(ctx); err != nil {
					return err
				}
				return rpc.NewError(rpc.CodeUnavailable, "generated stream failed after open")
			})
		})
	}()
	retryManager, err := rpc.NewExperimentalMuxConnectionManager(
		rpc.NewStaticResolver(badMuxEndpoint, "tcp://"+retryMuxListener.Addr().String()),
		rpc.WithExperimentalMuxConnectionManagerMaxOpenRetries(cfg.RPC.Mux.MaxOpenRetries),
		rpc.WithExperimentalMuxConnectionManagerOpenRetryReasons(cfg.RPC.Mux.OpenRetryReasons...),
		rpc.WithExperimentalMuxConnectionManagerHealthBackoffMultiplier(cfg.RPC.Mux.HealthBackoffMultiplier),
		rpc.WithExperimentalMuxConnectionManagerHealthMaxCooldown(cfg.RPC.Mux.HealthMaxCooldown),
		rpc.WithExperimentalMuxConnectionManagerHealthFailureThreshold(1),
		rpc.WithExperimentalMuxConnectionManagerHealthEjectionDuration(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	retryClientOptions := append(cfg.RPC.Mux.ClientOptions(), rpc.WithExperimentalMuxConnectionManager(retryManager))
	retryClient, err := rpc.NewClient("http://unused", retryClientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = retryClient.Close() }()
	runtimeRecorder := tracetest.NewSpanRecorder()
	runtimeProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(runtimeRecorder))
	defer func() { _ = runtimeProvider.Shutdown(context.Background()) }()
	runtimeTraceCtx, runtimeSpan := runtimeProvider.Tracer("generated-rpc-admin-smoke").Start(context.Background(), "mux-runtime-open-before-retry", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
	var runtimeLogBuf bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&runtimeLogBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)
	retryStream, err := retryClient.MuxStream(runtimeTraceCtx, "greeter/Watch")
	if err != nil {
		t.Fatal(err)
	}
	runtimeSpan.End()
	if err := retryStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	retryResponse, err := retryStream.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(retryResponse.Payload) != "retry:probe" {
		t.Fatalf("retry mux payload = %q, want retry probe response", retryResponse.Payload)
	}
	if _, err := retryStream.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("retry mux terminal receive = %v, want EOF", err)
	}
	if diagnosis := retryClient.RuntimeSnapshot().Diagnosis.Mux.Manager; diagnosis.OpenRetries != 1 || diagnosis.LastRetriedFrom != badMuxEndpoint || diagnosis.RetryReasons["dial_failure"] != 1 || diagnosis.HealthBackoffMultiplier != 2 || diagnosis.HealthMaxCooldown != 30*time.Second {
		t.Fatalf("retry manager diagnosis = %+v, want generated retry policy evidence", diagnosis)
	}
	if events := retryClient.RuntimeSnapshot().Diagnosis.Mux.Events; len(events) < 2 {
		t.Fatalf("retry mux diagnosis events = %+v, want generated retry/health event evidence", events)
	}
	runtimeTraceSpans := runtimeRecorder.Ended()
	if len(runtimeTraceSpans) != 1 {
		t.Fatalf("runtime trace spans = %d, want generated mux runtime span", len(runtimeTraceSpans))
	}
	runtimeTraceAttrs := generatedTraceAttributeMap(runtimeTraceSpans[0].Attributes())
	if runtimeTraceAttrs["rpc.mux.manager.open_retries.count"].AsInt64() != 1 ||
		runtimeTraceAttrs["rpc.mux.manager.last_retried_from"].AsString() != badMuxEndpoint ||
		runtimeTraceAttrs["rpc.mux.manager.retry_reason.dial_failure.count"].AsInt64() != 1 ||
		runtimeTraceAttrs["rpc.mux.manager.health.reason"].AsString() != "dial_failure" {
		t.Fatalf("runtime mux trace attributes = %+v, want generated open-before retry attributes", runtimeTraceAttrs)
	}
	runtimeLogLine := runtimeLogBuf.String()
	for _, want := range []string{
		"\"msg\":\"rpc mux stream diagnosis\"",
		"\"last_retried_from\":\"" + badMuxEndpoint + "\"",
		"\"health_reason\":\"dial_failure\"",
		"\"msg\":\"rpc mux runtime event\"",
		"\"event_family\":\"retry\"",
		"\"event\":\"open_before_retry\"",
		"\"msg\":\"rpc mux exported event\"",
	} {
		if !strings.Contains(runtimeLogLine, want) {
			t.Fatalf("runtime mux diagnosis log missing %s:\n%s", want, runtimeLogLine)
		}
	}
	failStream, err := retryClient.MuxStream(context.Background(), "greeter/FailAfterOpen")
	if err != nil {
		t.Fatal(err)
	}
	if err := failStream.Send(context.Background(), rpc.Message{Payload: []byte("probe")}); err != nil {
		t.Fatal(err)
	}
	if _, err := failStream.Receive(context.Background()); rpc.CodeOf(err) != rpc.CodeUnavailable {
		t.Fatalf("fail-after-open receive = %v, want CodeUnavailable", err)
	}
	if diagnosis := retryClient.RuntimeSnapshot().Diagnosis.Mux.Manager; diagnosis.OpenRetries != 1 || diagnosis.RetryReasons["open_stream"] != 0 {
		t.Fatalf("fail-after-open diagnosis = %+v, want no post-open retry replay", diagnosis)
	}
	stopRetryMux()
	if err := <-retryDone; err != nil {
		t.Fatalf("retry mux server stopped with error: %v", err)
	}

	flowClientConn, flowServerConn := net.Pipe()
	flowCfg := cfg.RPC.Mux.CandidateConfig()
	flowCfg.Protocol = "gofly-mux/generated-flow-control-test"
	flowCfg.ConnectionWindow = 1
	flowCfg.ReceiveQueueSize = 2
	// Keep the first data frame outside a scheduler-sized timeout while the
	// second frame still proves connection-credit exhaustion deterministically.
	flowCfg.CreditWaitTimeout = 50 * time.Millisecond
	flowClient := rpc.NewExperimentalMuxCandidateClientAdapter(flowClientConn, flowCfg)
	flowServer := rpc.NewExperimentalMuxCandidateServerAdapter(flowServerConn, flowCfg)
	defer func() { _ = flowClient.Close() }()
	defer func() { _ = flowServer.Close() }()
	holdFlow := make(chan struct{})
	if err := flowServer.RegisterStream("greeter/Hold", func(ctx context.Context, stream *rpc.ExperimentalMuxStream) error {
		select {
		case <-holdFlow:
		case <-ctx.Done():
			return ctx.Err()
		}
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		return stream.Close(ctx, string(msg.Payload))
	}); err != nil {
		t.Fatal(err)
	}
	flowCtx, stopFlow := context.WithCancel(context.Background())
	defer stopFlow()
	flowDone := make(chan error, 1)
	go func() {
		flowDone <- flowServer.Serve(flowCtx)
	}()
	flowStream, err := flowClient.OpenStream(context.Background(), "greeter/Hold")
	if err != nil {
		t.Fatal(err)
	}
	if err := flowStream.Send(context.Background(), rpc.Message{Payload: []byte("first")}); err != nil {
		t.Fatal(err)
	}
	if err := flowStream.Send(context.Background(), rpc.Message{Payload: []byte("second")}); rpc.CodeOf(err) != rpc.CodeDeadlineExceeded {
		t.Fatalf("flow-control send = %v, want CodeDeadlineExceeded", err)
	}
	if diagnosis := flowClient.DiagnosisSnapshot().FlowControl; diagnosis.CreditWaitTimeouts != 1 || diagnosis.ConnectionWindowExhausted < 1 {
		t.Fatalf("flow-control diagnosis = %+v, want credit timeout and window exhaustion", diagnosis)
	}
	close(holdFlow)
	stopFlow()
	if err := <-flowDone; err != nil {
		t.Fatalf("flow-control mux server stopped with error: %v", err)
	}

	writeTimeoutCfg := cfg.RPC.Mux.CandidateConfig()
	writeTimeoutCfg.Protocol = "gofly-mux/generated-write-timeout-test"
	writeTimeoutCfg.WriteTimeout = time.Millisecond
	writeTimeoutClient := rpc.NewExperimentalMuxCandidateClientAdapter(&generatedTimeoutWriteConn{done: make(chan struct{})}, writeTimeoutCfg)
	writeTimeoutStream, err := writeTimeoutClient.OpenStream(context.Background(), "greeter/Write")
	if err == nil {
		_ = writeTimeoutStream.Close(context.Background(), "unexpected")
		t.Fatal("write-timeout mux stream opened, want timeout")
	}
	if diagnosis := writeTimeoutClient.DiagnosisSnapshot().FlowControl; diagnosis.WriteTimeouts != 1 {
		t.Fatalf("write-timeout diagnosis = %+v, want one write timeout", diagnosis)
	}
	writeTimeoutRPCClient, err := rpc.NewClient("http://unused", rpc.WithExperimentalMuxClientAdapter(writeTimeoutClient))
	if err != nil {
		t.Fatal(err)
	}
	flowDiagnosisRec := httptest.NewRecorder()
	writeTimeoutRPCClient.DiagnosisHandler().ServeHTTP(flowDiagnosisRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?endpoint=http://unused&flowControlEvent=write-timeout&eventFamily=flow-control&event=write-timeout", nil))
	if flowDiagnosisRec.Code != http.StatusOK {
		t.Fatalf("flow-control diagnosis status = %d body=%q", flowDiagnosisRec.Code, flowDiagnosisRec.Body.String())
	}
	var flowDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(flowDiagnosisRec.Body).Decode(&flowDiagnosis); err != nil {
		t.Fatal(err)
	}
	if flowDiagnosis.Endpoint != "http://unused" ||
		flowDiagnosis.FlowControl != "write_timeout" ||
		flowDiagnosis.EventFamily != "flow_control" ||
		flowDiagnosis.Event != "write_timeout" ||
		flowDiagnosis.Diagnosis.Mux.FlowControl.WriteTimeouts != 1 ||
		len(flowDiagnosis.Diagnosis.Mux.FlowControl.Events) != 1 ||
		flowDiagnosis.Diagnosis.Mux.FlowControl.Events[0].Event != "write_timeout" ||
		flowDiagnosis.Diagnosis.Mux.FlowControl.Events[0].Count != 1 {
		t.Fatalf("flow-control diagnosis = %+v, want generated write_timeout event evidence", flowDiagnosis.Diagnosis.Mux.FlowControl)
	}
	if len(flowDiagnosis.Diagnosis.Mux.Events) != 1 ||
		flowDiagnosis.Diagnosis.Mux.Events[0].Family != "flow_control" ||
		flowDiagnosis.Diagnosis.Mux.Events[0].Event != "write_timeout" {
		t.Fatalf("flow-control diagnosis events = %+v, want generated write_timeout mux event evidence", flowDiagnosis.Diagnosis.Mux.Events)
	}
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	traceCtx, span := provider.Tracer("generated-rpc-admin-smoke").Start(context.Background(), "mux-diagnosis", oteltrace.WithSpanKind(oteltrace.SpanKindInternal))
	rpc.AnnotateMuxDiagnosisSpan(traceCtx, flowDiagnosis)
	span.End()
	traceSpans := recorder.Ended()
	if len(traceSpans) != 1 {
		t.Fatalf("trace spans = %d, want generated mux diagnosis span", len(traceSpans))
	}
	traceAttrs := generatedTraceAttributeMap(traceSpans[0].Attributes())
	if traceAttrs["rpc.mux.endpoint"].AsString() != "http://unused" ||
		traceAttrs["rpc.mux.flow_control.event"].AsString() != "write_timeout" ||
		traceAttrs["rpc.mux.flow_control.write_timeout.count"].AsInt64() != 1 {
		t.Fatalf("mux trace attributes = %+v, want generated mux flow-control attributes", traceAttrs)
	}

	connectionDiagnosisRec := httptest.NewRecorder()
	writeTimeoutRPCClient.DiagnosisHandler().ServeHTTP(connectionDiagnosisRec, httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?connectionId=missing&poolSlot=1&flowControlEvent=write-timeout", nil))
	if connectionDiagnosisRec.Code != http.StatusOK {
		t.Fatalf("connection flow-control diagnosis status = %d body=%q", connectionDiagnosisRec.Code, connectionDiagnosisRec.Body.String())
	}
	var connectionDiagnosis rpc.RPCDiagnosisProbe
	if err := json.NewDecoder(connectionDiagnosisRec.Body).Decode(&connectionDiagnosis); err != nil {
		t.Fatal(err)
	}
	if connectionDiagnosis.ConnectionID != "missing" ||
		connectionDiagnosis.PoolSlot != 1 ||
		len(connectionDiagnosis.Diagnosis.Mux.Manager.Endpoints) != 0 {
		t.Fatalf("connection flow-control diagnosis = %+v, want generated connection filter evidence", connectionDiagnosis)
	}
	if err := writeTimeoutRPCClient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeTimeoutClient.Close(); err != nil {
		t.Fatal(err)
	}

	rpcOptions := []rpc.ServerOption{}
	if cfg.RPC.Mux.Enabled && cfg.RPC.Mux.Probe {
		rpcOptions = append(rpcOptions, rpc.WithExperimentalMuxServerAdapter(muxServer))
	}
	rpcServer := rpc.NewServer(rpcOptions...)
	if err := apprpc.RegisterServices(rpcServer, svc.NewServiceContext(cfg)); err != nil {
		t.Fatal(err)
	}
	adminServer := NewServer("", "/admin", rpcServer, WithControlPlaneSnapshot(func(ctx context.Context) (controlplane.Snapshot, error) {
		return cfg.ControlPlaneSnapshot(ctx)
	}))
	handler := adminServer.Handler()

	metricsRec := httptest.NewRecorder()
	handler.ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metricsRec.Code != http.StatusOK ||
		!strings.Contains(metricsRec.Body.String(), "gofly_requests_total") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_connections{frame_codec=\"binary\",payload_codec=\"identity\",downgraded=\"false\"} 1") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_drain_total{drain_reason=\"generated_shutdown\",direction=\"out\"} 1") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_active_streams{drain_reason=\"generated_shutdown\",state=\"draining\"}") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_flow_control_events_total{event=\"write_timeout\"}") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_flow_control_events_total{event=\"credit_wait_timeout\"}") ||
		!strings.Contains(metricsRec.Body.String(), "gofly_rpc_mux_candidate_flow_control_events_total{event=\"connection_window_exhausted\"}") {
		t.Fatalf("metrics response = %d %q", metricsRec.Code, metricsRec.Body.String())
	}

	pprofRec := httptest.NewRecorder()
	handler.ServeHTTP(pprofRec, httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutine?debug=1", nil))
	if pprofRec.Code != http.StatusOK || !strings.Contains(pprofRec.Body.String(), "goroutine") {
		t.Fatalf("goroutine pprof response = %d %q", pprofRec.Code, pprofRec.Body.String())
	}

	controlPlaneRec := httptest.NewRecorder()
	handler.ServeHTTP(controlPlaneRec, httptest.NewRequest(http.MethodGet, "/admin/control-plane", nil))
	if controlPlaneRec.Code != http.StatusOK {
		t.Fatalf("control-plane status = %d body=%q", controlPlaneRec.Code, controlPlaneRec.Body.String())
	}
	var snapshot controlplane.Snapshot
	if err := json.NewDecoder(controlPlaneRec.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode control-plane snapshot: %v", err)
	}
	if snapshot.Metadata["generated.project"] != "available" {
		t.Fatalf("control-plane snapshot metadata = %#v, want generated project marker", snapshot.Metadata)
	}

	descRec := httptest.NewRecorder()
	handler.ServeHTTP(descRec, httptest.NewRequest(http.MethodGet, "/admin/rpc/admin/descriptors/greeter", nil))
	if descRec.Code != http.StatusOK {
		t.Fatalf("descriptor status = %d body=%q", descRec.Code, descRec.Body.String())
	}
	var descriptor rpc.Descriptor
	if err := json.NewDecoder(descRec.Body).Decode(&descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Name != "greeter" || len(descriptor.Methods) != 1 || descriptor.Methods[0].Name != "SayHello" {
		t.Fatalf("descriptor = %#v, want greeter/SayHello", descriptor)
	}

	diagnosisRec := httptest.NewRecorder()
	handler.ServeHTTP(diagnosisRec, httptest.NewRequest(http.MethodGet, "/admin/rpc/admin/diagnosis", nil))
	if diagnosisRec.Code != http.StatusOK {
		t.Fatalf("diagnosis status = %d body=%q", diagnosisRec.Code, diagnosisRec.Body.String())
	}
	var diagnosis rpc.ServerDiagnosisSnapshot
	if err := json.NewDecoder(diagnosisRec.Body).Decode(&diagnosis); err != nil {
		t.Fatal(err)
	}
	if !diagnosis.Mux.Enabled || diagnosis.Mux.Adapter.AcceptedStreams != 1 || diagnosis.Mux.Transport.AcceptedStreams != 1 {
		t.Fatalf("diagnosis mux = %+v, want generated mux probe evidence", diagnosis.Mux)
	}
	stopMux()
	if err := <-muxDone; err != nil {
		t.Fatalf("mux server stopped with error: %v", err)
	}
}

func waitMuxManagerStreamsClosed(t *testing.T, client *rpc.HTTPClient) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
		if len(diagnosis.Endpoints) == 1 && diagnosis.Endpoints[0].Adapter.Transport.ActiveStreams == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("mux stream did not reach terminal state: %+v", diagnosis.Endpoints)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAdminDiagnosticsCustomOTelLogSink(t *testing.T) {
	customRecords := make(chan rpc.RPCMuxDiagnosisEventOTelLogRecord, 4)
	var customProfile string
	cleanup := rpc.RegisterRPCMuxOTelLogSink("otel-test", func(profile string) rpc.RPCMuxOTelLogExporter {
		customProfile = profile
		return rpc.RPCMuxOTelLogExporterFunc(func(_ context.Context, record rpc.RPCMuxDiagnosisEventOTelLogRecord) {
			customRecords <- record
		})
	})
	defer cleanup()

	if !rpc.RPCMuxOTelLogSinkRegistered("otel-test") {
		t.Fatal("custom otel-test sink not found in registry")
	}
	if !rpc.RPCMuxOTelLogSinkRegistered("  OTEL-TEST  ") {
		t.Fatal("custom otel-test sink should be discovered case-insensitively")
	}

	customCfg := appconfig.Config{RPC: appconfig.RPCConfig{Mux: appconfig.RPCMuxConfig{Enabled: true, Log: appconfig.RPCMuxLogConfig{Enabled: true, ExportEvents: true, EventFamily: "flow-control", Event: "fragment-window-refill", OTelCompatible: appconfig.RPCMuxOTelCompatibleLogConfig{Enabled: true, Sink: "otel-test", Profile: "generated-custom-sink", Delivery: appconfig.RPCMuxExporterDeliveryConfig{QueueSize: 4, Timeout: time.Second}}}, Candidate: appconfig.RPCMuxCandidateConfig{Enabled: true, Protocol: "gofly-mux/generated-custom-sink-test", MaxFrameBytes: 256, MaxMessageBytes: 1024, MaxConcurrentStreams: 8, ReceiveQueueSize: 2, ConnectionWindow: 3, FragmentStreamWindowUpdatePolicy: "on_receive", FragmentConnectionWindowUpdatePolicy: "on_receive", FragmentStreamWindowRefillRatio: 0.5, FragmentConnectionWindowRefillRatio: 0.25, FragmentMaxDeferredFragments: 2, FragmentWindowPolicyRiskMode: "warn", PayloadCodec: "identity", FrameCodec: "binary"}}}}
	if err := appconfig.ValidateRPCMuxConfig(customCfg.RPC.Mux); err != nil {
		t.Fatalf("custom otel-test sink should validate: %v", err)
	}

	customClient, err := rpc.NewClient("http://unused", customCfg.RPC.Mux.ClientOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = customClient.Close() }()

	if len(customCfg.RPC.Mux.ServerOptions()) != 1 {
		t.Fatalf("custom sink server options = %d, want one exporter option", len(customCfg.RPC.Mux.ServerOptions()))
	}
	customClient.ObserveMuxDiagnosis(context.Background(), rpc.RPCDiagnosisProbe{
		Target:  "http://unused",
		Method:  "greeter/Watch",
		Matched: true,
		Diagnosis: rpc.RPCDiagnosisSnapshot{Mux: rpc.RPCMuxTransportDiagnosis{
			FlowControl: rpc.RPCMuxFlowControlDiagnosis{
				WriteTimeouts:         1,
				FragmentWindowRefills: 3,
			},
		}},
	})

	if customProfile != "generated-custom-sink" {
		t.Fatalf("custom sink profile = %q, want generated-custom-sink", customProfile)
	}
	foundRefill := false
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for !foundRefill {
		select {
		case record := <-customRecords:
			if record.Event.Family == "flow_control" && record.Event.Event == "fragment_window_refill" {
				foundRefill = true
				if record.Name != "rpc.mux.diagnosis_event" {
					t.Fatalf("custom record name = %q, want rpc.mux.diagnosis_event", record.Name)
				}
				if record.Severity != "WARN" {
					t.Fatalf("custom record severity = %q, want WARN", record.Severity)
				}
			}
		case <-timeout.C:
			t.Fatal("custom otel-test sink received no fragment_window_refill event")
		}
	}
}

type generatedTimeoutWriteConn struct {
	mu       sync.Mutex
	deadline time.Time
	closed   bool
	done     chan struct{}
}

func (c *generatedTimeoutWriteConn) Read([]byte) (int, error) {
	<-c.done
	return 0, net.ErrClosed
}

func (c *generatedTimeoutWriteConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	if !deadline.IsZero() {
		return 0, generatedTimeoutNetError{msg: "write timeout"}
	}
	return len(p), nil
}

func (c *generatedTimeoutWriteConn) Close() error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.done)
	}
	c.mu.Unlock()
	return nil
}

func (c *generatedTimeoutWriteConn) LocalAddr() net.Addr  { return generatedDummyAddr("local") }
func (c *generatedTimeoutWriteConn) RemoteAddr() net.Addr { return generatedDummyAddr("remote") }
func (c *generatedTimeoutWriteConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}
func (c *generatedTimeoutWriteConn) SetReadDeadline(time.Time) error    { return nil }
func (c *generatedTimeoutWriteConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

type generatedTimeoutNetError struct{ msg string }

func (e generatedTimeoutNetError) Error() string   { return e.msg }
func (e generatedTimeoutNetError) Timeout() bool   { return true }
func (e generatedTimeoutNetError) Temporary() bool { return true }

type generatedDummyAddr string

func (a generatedDummyAddr) Network() string { return string(a) }
func (a generatedDummyAddr) String() string  { return string(a) }

func generatedTraceAttributeMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}

func generatedRPCTLSCA(t *testing.T, dir string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "generated-rpc-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}
	generatedRPCTestPEM(t, filepath.Join(dir, "ca.crt"), "CERTIFICATE", der)
	return cert, key
}

func generatedRPCTLSLeaf(t *testing.T, dir, name string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"svc"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	certFile = filepath.Join(dir, name+".crt")
	keyFile = filepath.Join(dir, name+".key")
	generatedRPCTestPEM(t, certFile, "CERTIFICATE", der)
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	generatedRPCTestPEM(t, keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func generatedRPCTestPEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
