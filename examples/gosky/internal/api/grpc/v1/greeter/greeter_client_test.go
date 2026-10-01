package greeterrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rpc"
)

func TestGreeterRPCClient(t *testing.T) {
	cfg := config.Config{Service: generatedServiceConfFixture()}
	serviceConf := cfg.ServiceConf()
	server := rpc.NewServer(serviceConf.RPCServerOptions()...)
	svcCtx := svc.NewServiceContext(cfg)
	if err := server.RegisterService(GreeterService(svcCtx), nil); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	descriptorResp, err := http.Get(httpServer.URL + "/rpc/admin/descriptors/greeter")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = descriptorResp.Body.Close() }()
	if descriptorResp.StatusCode != http.StatusOK {
		t.Fatalf("descriptor status = %d, want %d", descriptorResp.StatusCode, http.StatusOK)
	}
	var descriptor rpc.Descriptor
	if err := json.NewDecoder(descriptorResp.Body).Decode(&descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Name != "greeter" || len(descriptor.Methods) != 1 || descriptor.Methods[0].Name != "SayHello" {
		t.Fatalf("descriptor = %#v, want greeter/SayHello", descriptor)
	}
	descriptorPayload, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	compatResp, err := http.Post(httpServer.URL+"/rpc/admin/descriptors/greeter/compatibility", "application/json", bytes.NewReader(descriptorPayload))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = compatResp.Body.Close() }()
	if compatResp.StatusCode != http.StatusOK {
		t.Fatalf("descriptor compatibility status = %d, want %d", compatResp.StatusCode, http.StatusOK)
	}
	var report rpc.DescriptorCompatibilityReport
	if err := json.NewDecoder(compatResp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if !report.IsCompatible() {
		t.Fatalf("descriptor compatibility report = %#v, want compatible", report)
	}

	registry := rpc.NewRegistry()
	if err := registry.RegisterService(context.Background(), "greeter", httpServer.URL); err != nil {
		t.Fatal(err)
	}
	clientOptions := append(serviceConf.RPCClientOptions(),
		rpc.WithResolver(registry.Resolver("greeter")),
		rpc.WithBalancer(rpc.NewHealthBalancer()),
	)
	client, err := rpc.NewClient(httpServer.URL, clientOptions...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	unregisterClient := svcCtx.RegisterRPCClient(client)
	defer unregisterClient()
	records := make(chan rpc.RPCMuxDiagnosisEventRecord, 1)
	svcCtx.UpdateRPCMuxDiagnosisExporters(rpc.RPCMuxDiagnosisEventExporterFunc(func(_ context.Context, record rpc.RPCMuxDiagnosisEventRecord) {
		records <- record
	}), rpc.RPCMuxDiagnosisFilter{EventFamily: "flow_control", Event: "write_timeout"})
	client.ObserveMuxDiagnosis(context.Background(), rpc.RPCDiagnosisProbe{
		Target:  httpServer.URL,
		Method:  "greeter/Watch",
		Matched: true,
		Diagnosis: rpc.RPCDiagnosisSnapshot{Mux: rpc.RPCMuxTransportDiagnosis{
			FlowControl: rpc.RPCMuxFlowControlDiagnosis{WriteTimeouts: 1},
		}},
	})
	select {
	case record := <-records:
		if record.Event.Event != "write_timeout" {
			t.Fatalf("registered client exporter record = %+v, want write_timeout", record)
		}
	case <-time.After(time.Second):
		t.Fatal("registered client did not receive mux diagnosis exporter update")
	}
	unregisterClient()
	unregisteredRecords := make(chan rpc.RPCMuxDiagnosisEventRecord, 1)
	svcCtx.UpdateRPCMuxDiagnosisExporters(rpc.RPCMuxDiagnosisEventExporterFunc(func(_ context.Context, record rpc.RPCMuxDiagnosisEventRecord) {
		unregisteredRecords <- record
	}), rpc.RPCMuxDiagnosisFilter{EventFamily: "flow_control", Event: "write_timeout"})
	client.ObserveMuxDiagnosis(context.Background(), rpc.RPCDiagnosisProbe{
		Target:  httpServer.URL,
		Method:  "greeter/Watch",
		Matched: true,
		Diagnosis: rpc.RPCDiagnosisSnapshot{Mux: rpc.RPCMuxTransportDiagnosis{
			FlowControl: rpc.RPCMuxFlowControlDiagnosis{WriteTimeouts: 1},
		}},
	})
	select {
	case record := <-unregisteredRecords:
		t.Fatalf("unregistered client received exporter update: %+v", record)
	default:
	}
	runtimeState := client.PolicyRuntimeSnapshot().State
	if !runtimeState.TimeoutEnforced || runtimeState.EffectiveTimeout != 3*time.Second {
		t.Fatalf("rpc client timeout state = %+v, want generated service timeout", runtimeState)
	}
	if runtimeState.RetryAttempts != 2 || runtimeState.RetryBackoff != 100*time.Millisecond {
		t.Fatalf("rpc client retry state = %+v, want generated service retry profile", runtimeState)
	}
	if !runtimeState.BreakerEnabled || runtimeState.Balancer != rpc.RPCBalancerHealth {
		t.Fatalf("rpc client resilience state = %+v, want breaker/health balancer", runtimeState)
	}
	clientRuntime := client.RuntimeSnapshot()
	if clientRuntime.Middlewares.Unary == 0 || clientRuntime.Middlewares.Stream == 0 {
		t.Fatalf("rpc client middleware state = %+v, want generated governance middleware", clientRuntime.Middlewares)
	}
	if clientRuntime.Transport.Timeout != 30*time.Second {
		t.Fatalf("rpc client transport timeout = %s, want generated transport timeout", clientRuntime.Transport.Timeout)
	}
	var resp SayHelloResponse
	ctx := metadata.Append(context.Background(), metadata.RequestIDKey, "test-request-id")
	if err := client.Call(ctx, "greeter/SayHello", SayHelloRequest{Name: "client"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "hello client" {
		t.Fatalf("message = %q, want hello client", resp.Message)
	}
}

func generatedServiceConfFixture() app.ServiceConf {
	return app.ServiceConf{
		Name:        "hello",
		Mode:        "dev",
		Environment: "development",
		Governance: app.ServiceGovernance{
			Timeout:           3 * time.Second,
			ReadHeaderTimeout: 3 * time.Second,
			Breaker:           true,
			Retry:             app.ServiceRetry{Attempts: 2, Backoff: 100 * time.Millisecond},
			RateLimit:         app.ServiceRateLimit{Rate: 100, Burst: 100},
			MaxConcurrency:    64,
			AdaptiveLimit:     true,
			RPCTimeout:        rpc.RPCTimeoutConfig{Server: 3 * time.Second, Client: 3 * time.Second},
			RPCTransport: rpc.TransportConfig{
				Timeout:               30 * time.Second,
				MaxIdleConns:          200,
				MaxIdleConnsPerHost:   100,
				DialTimeout:           30 * time.Second,
				KeepAlive:             30 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second,
			},
		},
	}
}
