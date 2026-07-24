package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/breaker"
	coregovernance "github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/core/observability/trace"
	coreruntime "github.com/imajinyun/gofly/core/runtime"
	controladmin "github.com/imajinyun/gofly/ops/admin"
	"github.com/imajinyun/gofly/rpc/endpoint"
)

type helloRequest struct {
	Name string `json:"name"`
}
type helloResponse struct {
	Message string `json:"message"`
}

func TestHTTPServerDiagnosisFiltersMuxFlowControl(t *testing.T) {
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:     "gofly-mux/admin-flow-control-test",
		FrameCodec:   "binary",
		PayloadCodec: "identity",
		WriteTimeout: time.Millisecond,
	}
	adapter := NewExperimentalMuxCandidateServerAdapter(&timeoutWriteConn{done: make(chan struct{})}, cfg)
	defer adapter.Close()
	stream, err := adapter.transport.OpenStream(context.Background())
	if err == nil {
		_ = stream.Close(context.Background(), "unexpected")
		t.Fatal("OpenStream succeeded, want write timeout")
	}
	if adapter.DiagnosisSnapshot().FlowControl.WriteTimeouts != 1 {
		t.Fatalf("adapter diagnosis = %+v, want write timeout", adapter.DiagnosisSnapshot().FlowControl)
	}

	server := NewServer(WithExperimentalMuxServerAdapter(adapter))
	if err := server.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(context.Context, any) (any, error) {
			return helloResponse{Message: "ok"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/rpc/admin/diagnosis?flowControlEvent=write-timeout&eventFamily=flow-control&event=write-timeout", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnosis status = %d body=%s", rec.Code, rec.Body.String())
	}
	var diagnosis ServerDiagnosisSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&diagnosis); err != nil {
		t.Fatal(err)
	}
	if diagnosis.FlowControl != "write_timeout" ||
		diagnosis.EventFamily != "flow_control" ||
		diagnosis.Event != "write_timeout" ||
		diagnosis.Mux.FlowControl.WriteTimeouts != 1 ||
		len(diagnosis.Mux.FlowControl.Events) != 1 ||
		diagnosis.Mux.FlowControl.Events[0].Event != "write_timeout" ||
		diagnosis.Mux.FlowControl.Events[0].Count != 1 {
		t.Fatalf("filtered diagnosis = %+v, want structured write_timeout event", diagnosis.Mux.FlowControl)
	}
	if len(diagnosis.Mux.Events) != 1 ||
		diagnosis.Mux.Events[0].Family != "flow_control" ||
		diagnosis.Mux.Events[0].Event != "write_timeout" {
		t.Fatalf("filtered mux events = %+v, want write_timeout flow-control event", diagnosis.Mux.Events)
	}
	if diagnosis.Mux.FlowControl.CreditWaitTimeouts != 0 || diagnosis.Mux.FlowControl.ConnectionWindowExhausted != 0 {
		t.Fatalf("filtered diagnosis counters = %+v, want unrelated flow-control counters hidden", diagnosis.Mux.FlowControl)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/diagnosis?eventFamily=flow-control&event=credit-wait-timeout", nil)
	missingRec := httptest.NewRecorder()
	server.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusOK {
		t.Fatalf("missing event diagnosis status = %d body=%s", missingRec.Code, missingRec.Body.String())
	}
	var missingDiagnosis ServerDiagnosisSnapshot
	if err := json.NewDecoder(missingRec.Body).Decode(&missingDiagnosis); err != nil {
		t.Fatal(err)
	}
	if missingDiagnosis.Matched || len(missingDiagnosis.Mux.Events) != 0 {
		t.Fatalf("missing event diagnosis = %+v, want unmatched empty event view", missingDiagnosis)
	}
}

func TestHTTPServerDiagnosisExportsMuxEvents(t *testing.T) {
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:     "gofly-mux/server-export-test",
		FrameCodec:   "binary",
		PayloadCodec: "identity",
		WriteTimeout: time.Millisecond,
	}
	adapter := NewExperimentalMuxCandidateServerAdapter(&timeoutWriteConn{done: make(chan struct{})}, cfg)
	defer adapter.Close()
	if _, err := adapter.transport.OpenStream(context.Background()); err == nil {
		t.Fatal("OpenStream succeeded, want write timeout")
	}

	var records []RPCMuxDiagnosisEventRecord
	server := NewServer(
		WithExperimentalMuxServerAdapter(adapter),
		WithServerMuxDiagnosisEventExporter(
			RPCMuxDiagnosisEventExporterFunc(func(_ context.Context, record RPCMuxDiagnosisEventRecord) {
				records = append(records, record)
			}),
			RPCMuxDiagnosisFilter{EventFamily: "flow_control", Event: "write_timeout"},
		),
	)
	if err := server.RegisterService(ServiceDesc{Name: "greeter", Streams: []StreamDesc{{
		Name:       "Watch",
		NewMessage: func() any { return new(helloRequest) },
		Handler:    func(context.Context, *Stream) error { return nil },
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/rpc/admin/diagnosis?service=greeter&method=Watch&eventFamily=flow-control&event=write-timeout", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnosis status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(records) != 1 {
		t.Fatalf("server mux diagnosis records = %+v, want one write_timeout event", records)
	}
	record := records[0]
	if !record.Probe.Matched || record.Method != "greeter/Watch" || record.Target != ":8081" || record.Event.Event != "write_timeout" || record.Event.Family != "flow_control" {
		t.Fatalf("server mux diagnosis record = %+v, want server method, target, and filtered event", record)
	}
}

func TestHTTPServerUpdateMuxDiagnosisEventExporter(t *testing.T) {
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:     "gofly-mux/server-update-exporter-test",
		FrameCodec:   "binary",
		PayloadCodec: "identity",
		WriteTimeout: time.Millisecond,
	}
	adapter := NewExperimentalMuxCandidateServerAdapter(&timeoutWriteConn{done: make(chan struct{})}, cfg)
	defer adapter.Close()
	if _, err := adapter.transport.OpenStream(context.Background()); err == nil {
		t.Fatal("OpenStream succeeded, want write timeout")
	}
	records := make(chan RPCMuxDiagnosisEventRecord, 1)
	server := NewServer(WithExperimentalMuxServerAdapter(adapter))
	server.UpdateMuxDiagnosisEventExporter(RPCMuxDiagnosisEventExporterFunc(func(_ context.Context, record RPCMuxDiagnosisEventRecord) {
		records <- record
	}), RPCMuxDiagnosisFilter{EventFamily: "flow_control", Event: "write_timeout"})

	req := httptest.NewRequest(http.MethodGet, "/rpc/admin/diagnosis?eventFamily=flow-control&event=write-timeout", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnosis status = %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case record := <-records:
		if record.Event.Event != "write_timeout" {
			t.Fatalf("record = %+v, want write_timeout", record)
		}
	case <-time.After(time.Second):
		t.Fatal("updated server exporter did not receive diagnosis event")
	}

	server.UpdateMuxDiagnosisEventExporter(nil, RPCMuxDiagnosisFilter{})
	server.ServeHTTP(httptest.NewRecorder(), req)
	select {
	case record := <-records:
		t.Fatalf("disabled server exporter received event: %+v", record)
	default:
	}
}

func TestHTTPServerRuntimeSnapshotExposesMuxSinkRegistry(t *testing.T) {
	server := NewServer(WithServerMuxDiagnosisEventExporterDelivery(
		RPCMuxDiagnosisEventExporterFunc(func(context.Context, RPCMuxDiagnosisEventRecord) {}),
		RPCMuxDiagnosisFilter{},
		RPCMuxDiagnosisExporterDeliveryConfig{QueueSize: 2, Timeout: time.Second},
	))
	t.Cleanup(func() {
		if err := server.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	snapshot := server.RuntimeSnapshot(context.Background())
	for _, component := range snapshot.Components {
		if component.Name != "rpc.mux.sink.registry" {
			continue
		}
		details, ok := component.Details.(map[string]any)
		if !ok || details["registry"] == nil || details["delivery"] == nil {
			t.Fatalf("mux sink registry component details = %#v, want registry and delivery", component.Details)
		}
		return
	}
	t.Fatalf("runtime components = %+v, want rpc.mux.sink.registry", snapshot.Components)
}

func TestHTTPServerRuntimeSnapshotExposesMuxSinkSetSLO(t *testing.T) {
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "server-v1",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "slog", Priority: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(WithServerMuxDiagnosisEventExporter(sinkSet, RPCMuxDiagnosisFilter{}))
	t.Cleanup(func() {
		if err := server.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	snapshot := server.RuntimeSnapshot(context.Background())
	for _, component := range snapshot.Components {
		if component.Name != "rpc.mux.sink.registry" {
			continue
		}
		details, ok := component.Details.(map[string]any)
		sinkSetSnapshot, okSnapshot := details["sinkSet"].(RPCMuxDiagnosisSinkSetSnapshot)
		if !ok || !okSnapshot || sinkSetSnapshot.Version != "server-v1" ||
			sinkSetSnapshot.SinkCount != 1 || sinkSetSnapshot.Sinks[0].Delivery.Health != "healthy" {
			t.Fatalf("mux sink set component details = %#v", component.Details)
		}
		return
	}
	t.Fatalf("runtime components = %+v, want sink set SLO", snapshot.Components)
}

func TestRPCMuxDiagnosisSinkSetRuntimeStatus(t *testing.T) {
	tests := []struct {
		name     string
		snapshot RPCMuxDiagnosisSinkSetSnapshot
		want     string
	}{
		{name: "healthy", snapshot: RPCMuxDiagnosisSinkSetSnapshot{Sinks: []RPCMuxDiagnosisSinkRuntimeSnapshot{{Delivery: RPCMuxDiagnosisExporterDeliverySnapshot{Health: "healthy"}}}}, want: "ok"},
		{name: "degraded sink remains available", snapshot: RPCMuxDiagnosisSinkSetSnapshot{Sinks: []RPCMuxDiagnosisSinkRuntimeSnapshot{{Delivery: RPCMuxDiagnosisExporterDeliverySnapshot{Health: "degraded"}}}}, want: "ok"},
		{name: "unhealthy", snapshot: RPCMuxDiagnosisSinkSetSnapshot{Sinks: []RPCMuxDiagnosisSinkRuntimeSnapshot{{Delivery: RPCMuxDiagnosisExporterDeliverySnapshot{Health: "unhealthy"}}}}, want: "degraded"},
		{name: "closed", snapshot: RPCMuxDiagnosisSinkSetSnapshot{Closed: true}, want: "stopped"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rpcMuxDiagnosisSinkSetStatus(test.snapshot); got != test.want {
				t.Fatalf("status = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHTTPServerMuxOperatorActionsEndpoint(t *testing.T) {
	block := make(chan struct{})
	cleanup := RegisterRPCMuxOTelLogSinkProvider("operator-actions", sinkSetTestProvider{
		newExporter: func(string) RPCMuxOTelLogExporter {
			return &sinkSetTestExporter{export: func(RPCMuxDiagnosisEventOTelLogRecord) {
				<-block
			}}
		},
	})
	defer cleanup()
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "operator-v1",
		Sinks: []RPCMuxDiagnosisSinkConfig{{
			Name: "operator-actions",
			Delivery: RPCMuxDiagnosisExporterDeliveryConfig{
				Timeout:                 5 * time.Millisecond,
				MaxHungCalls:            1,
				BreakerFailureThreshold: 10,
				Isolation: RPCMuxDiagnosisSinkIsolationConfig{
					Mode: RPCMuxDiagnosisSinkIsolationWASM,
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(block)
		if err := sinkSet.Close(); err != nil {
			t.Error(err)
		}
	}()
	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	waitForMuxSinkSetSnapshot(t, sinkSet, func(snapshot RPCMuxDiagnosisSinkSetSnapshot) bool {
		return snapshot.Sinks[0].Delivery.OperatorAction == "pause_sink_hung_calls"
	})
	server := NewServer(WithServerMuxDiagnosisEventExporter(sinkSet, RPCMuxDiagnosisFilter{}))

	req := httptest.NewRequest(http.MethodGet, "/rpc/admin/mux/operator-actions", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator actions status = %d body=%s", rec.Code, rec.Body.String())
	}
	var actions []RPCMuxDiagnosisOperatorAction
	if err := json.NewDecoder(rec.Body).Decode(&actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Action != RPCMuxDiagnosisOperatorPauseSink ||
		actions[0].Reason != "hung_call_limit" || !actions[0].DryRun ||
		actions[0].Details["isolation_mode"] != RPCMuxDiagnosisSinkIsolationWASM {
		t.Fatalf("operator actions = %+v", actions)
	}

	dryRunReq := httptest.NewRequest(http.MethodPost, "/rpc/admin/mux/operator-actions", strings.NewReader(`{"sink":"operator-actions","action":"pause_sink"}`))
	dryRunRec := httptest.NewRecorder()
	server.ServeHTTP(dryRunRec, dryRunReq)
	if dryRunRec.Code != http.StatusOK {
		t.Fatalf("dry-run action status = %d body=%s", dryRunRec.Code, dryRunRec.Body.String())
	}
	var dryRun RPCMuxDiagnosisOperatorAction
	if err := json.NewDecoder(dryRunRec.Body).Decode(&dryRun); err != nil {
		t.Fatal(err)
	}
	if !dryRun.DryRun || dryRun.Approved || dryRun.Reason != "approval_required" {
		t.Fatalf("dry-run action = %+v", dryRun)
	}

	server = NewServer(
		WithServerMuxDiagnosisEventExporter(sinkSet, RPCMuxDiagnosisFilter{}),
		WithServerMuxDiagnosisOperatorApprovalToken("approve-me"),
	)
	approvedReq := httptest.NewRequest(http.MethodPost, "/rpc/admin/mux/operator-actions", strings.NewReader(`{"sink":"operator-actions","action":"pause_sink","token":"approve-me"}`))
	approvedRec := httptest.NewRecorder()
	server.ServeHTTP(approvedRec, approvedReq)
	if approvedRec.Code != http.StatusOK {
		t.Fatalf("approved action status = %d body=%s", approvedRec.Code, approvedRec.Body.String())
	}
	var approved RPCMuxDiagnosisOperatorAction
	if err := json.NewDecoder(approvedRec.Body).Decode(&approved); err != nil {
		t.Fatal(err)
	}
	if !approved.Approved || approved.DryRun || approved.Action != RPCMuxDiagnosisOperatorPauseSink {
		t.Fatalf("approved action = %+v", approved)
	}
	if snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot(); !snapshot.Sinks[0].Delivery.OperatorPaused {
		t.Fatalf("snapshot after approved pause = %+v", snapshot)
	}
	wrongTokenReq := httptest.NewRequest(http.MethodPost, "/rpc/admin/mux/operator-actions", strings.NewReader(`{"sink":"operator-actions","action":"resume_sink","token":"wrong"}`))
	wrongTokenRec := httptest.NewRecorder()
	server.ServeHTTP(wrongTokenRec, wrongTokenReq)
	if wrongTokenRec.Code != http.StatusOK {
		t.Fatalf("wrong-token action status = %d body=%s", wrongTokenRec.Code, wrongTokenRec.Body.String())
	}
	var wrongToken RPCMuxDiagnosisOperatorAction
	if err := json.NewDecoder(wrongTokenRec.Body).Decode(&wrongToken); err != nil {
		t.Fatal(err)
	}
	if !wrongToken.DryRun || wrongToken.Approved || wrongToken.Reason != "approval_required" {
		t.Fatalf("wrong-token action = %+v", wrongToken)
	}
	historyReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/mux/operator-actions/history?limit=1", nil)
	historyRec := httptest.NewRecorder()
	server.ServeHTTP(historyRec, historyReq)
	if historyRec.Code != http.StatusOK {
		t.Fatalf("operator history status = %d body=%s", historyRec.Code, historyRec.Body.String())
	}
	var history RPCMuxDiagnosisOperatorHistorySnapshot
	if err := json.NewDecoder(historyRec.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history.Actions) != 1 || history.Actions[0].Action != RPCMuxDiagnosisOperatorPauseSink || !history.Actions[0].Approved || history.Checksum == "" {
		t.Fatalf("operator history = %+v", history)
	}
}

func TestHTTPServerMuxOperatorActionBoundaries(t *testing.T) {
	if actions := (*HTTPServer)(nil).MuxDiagnosisOperatorActions(context.Background()); len(actions) != 0 {
		t.Fatalf("nil server operator actions = %+v, want none", actions)
	}
	if history := (*HTTPServer)(nil).MuxDiagnosisOperatorActionHistory(1); len(history) != 0 {
		t.Fatalf("nil server operator history = %+v, want none", history)
	}
	if snapshot := (*HTTPServer)(nil).MuxDiagnosisOperatorActionHistorySnapshot(1); len(snapshot.Actions) != 0 {
		t.Fatalf("nil server operator history snapshot = %+v, want none", snapshot)
	}
	emptyServer := NewServer()
	if history := emptyServer.MuxDiagnosisOperatorActionHistory(1); len(history) != 0 {
		t.Fatalf("server without history source = %+v, want none", history)
	}
	if snapshot := emptyServer.MuxDiagnosisOperatorActionHistorySnapshot(1); len(snapshot.Actions) != 0 {
		t.Fatalf("server without history snapshot source = %+v, want none", snapshot)
	}
	if integrity, err := emptyServer.MuxDiagnosisOperatorHistoryIntegritySnapshot(context.Background()); err != nil || integrity.Store.Enabled {
		t.Fatalf("server without history source integrity = %+v err=%v", integrity, err)
	}
	nilRec := httptest.NewRecorder()
	(*HTTPServer)(nil).serveMuxOperatorAction(nilRec, httptest.NewRequest(http.MethodPost, "/rpc/admin/mux/operator-actions", strings.NewReader(`{}`)))
	if nilRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil server operator action status = %d body=%s", nilRec.Code, nilRec.Body.String())
	}
	server := NewServer()
	emptyRec := httptest.NewRecorder()
	server.ServeHTTP(emptyRec, httptest.NewRequest(http.MethodPost, "/rpc/admin/mux/operator-actions", nil))
	if emptyRec.Code != http.StatusBadRequest {
		t.Fatalf("empty operator payload status = %d body=%s", emptyRec.Code, emptyRec.Body.String())
	}
	invalidRec := httptest.NewRecorder()
	server.ServeHTTP(invalidRec, httptest.NewRequest(http.MethodPost, "/rpc/admin/mux/operator-actions", strings.NewReader(`{`)))
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid operator payload status = %d body=%s", invalidRec.Code, invalidRec.Body.String())
	}
	missingRec := httptest.NewRecorder()
	server.ServeHTTP(missingRec, httptest.NewRequest(http.MethodPost, "/rpc/admin/mux/operator-actions", strings.NewReader(`{"sink":"missing","action":"pause_sink"}`)))
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing operator source status = %d body=%s", missingRec.Code, missingRec.Body.String())
	}

	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "boundary-v1",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "slog"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()
	manager, err := coregovernance.NewManager(coregovernance.Config{
		Rules: []coregovernance.Rule{{
			Name:      "mux-operator",
			Transport: coregovernance.TransportRPC,
			Service:   "rpc.mux.sink",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	governedServer := NewServer(
		WithServerMuxDiagnosisEventExporter(sinkSet, RPCMuxDiagnosisFilter{}),
		WithServerGovernanceManager(manager),
		WithServerMuxDiagnosisOperatorApprovalToken("approve-me"),
	)
	badActionRec := httptest.NewRecorder()
	governedServer.ServeHTTP(badActionRec, httptest.NewRequest(http.MethodPost, "/rpc/admin/mux/operator-actions", strings.NewReader(`{"sink":"slog","action":"bad","token":"approve-me"}`)))
	if badActionRec.Code != http.StatusBadRequest {
		t.Fatalf("bad operator action status = %d body=%s", badActionRec.Code, badActionRec.Body.String())
	}
}

func TestHTTPServerAdminAuditAndMethodBoundaries(t *testing.T) {
	var auditEvents []controladmin.AuditEvent
	server := NewServer(WithServerAdminAuditSink(func(_ context.Context, event controladmin.AuditEvent) {
		auditEvents = append(auditEvents, event)
	}))
	methodRec := httptest.NewRecorder()
	server.ServeHTTP(methodRec, httptest.NewRequest(http.MethodPost, "/rpc/admin/state", nil))
	if methodRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method boundary status = %d body=%s", methodRec.Code, methodRec.Body.String())
	}
	notFoundRec := httptest.NewRecorder()
	server.ServeHTTP(notFoundRec, httptest.NewRequest(http.MethodGet, "/rpc/admin/missing", nil))
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("missing admin status = %d body=%s", notFoundRec.Code, notFoundRec.Body.String())
	}
	if len(auditEvents) != 2 || auditEvents[0].Component != "rpc" || auditEvents[0].Status != http.StatusMethodNotAllowed ||
		auditEvents[1].Status != http.StatusNotFound {
		t.Fatalf("audit events = %+v", auditEvents)
	}
}

func TestHTTPServerMuxOperatorHistoryRuntimeDetails(t *testing.T) {
	store, err := NewRPCMuxDiagnosisOperatorHistoryFileStore(filepath.Join("history", "operator.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(t.TempDir(), store.path)
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "server-history-store-v1",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "slog"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()
	sinkSet.WithOperatorHistoryStore(store)
	server := NewServer(
		WithServerMuxDiagnosisEventExporter(sinkSet, RPCMuxDiagnosisFilter{}),
		WithServerMuxDiagnosisOperatorApprovalToken("approve-me"),
	)
	approved := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{
		Sink:   "slog",
		Action: RPCMuxDiagnosisOperatorPauseSink,
		Token:  "approved",
	})
	if !approved.Approved {
		t.Fatalf("approved action = %+v", approved)
	}
	if history := server.MuxDiagnosisOperatorActionHistory(1); len(history) != 1 || history[0].Action != RPCMuxDiagnosisOperatorPauseSink {
		t.Fatalf("server operator history = %+v", history)
	}
	integrity, err := server.MuxDiagnosisOperatorHistoryIntegritySnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !integrity.Store.Enabled || integrity.Verification.Primary.StoredActions != 1 ||
		integrity.Verification.Primary.IntegrityStatus != "ok" || integrity.Verification.Backup.IntegrityStatus != "missing" {
		t.Fatalf("server operator history integrity = %+v", integrity)
	}
	if nilIntegrity, err := (*HTTPServer)(nil).MuxDiagnosisOperatorHistoryIntegritySnapshot(context.Background()); err != nil || nilIntegrity.Store.Enabled {
		t.Fatalf("nil server integrity = %+v err=%v", nilIntegrity, err)
	}
	snapshot := server.RuntimeSnapshot(context.Background())
	var foundStore bool
	for _, component := range snapshot.Components {
		if component.Name != "rpc.mux.sink.registry" {
			continue
		}
		details, ok := component.Details.(map[string]any)
		if !ok {
			t.Fatalf("runtime details = %#v", component.Details)
		}
		storeDetails, ok := details["operatorHistoryStore"].(RPCMuxDiagnosisOperatorStoreSnapshot)
		if !ok || !storeDetails.Enabled || storeDetails.Kind != "file" {
			t.Fatalf("operator history store details = %#v", details["operatorHistoryStore"])
		}
		foundStore = true
	}
	if !foundStore {
		t.Fatalf("runtime snapshot missing operator history store: %+v", snapshot.Components)
	}
}

func TestHTTPServerDiagnosisConvenienceMethods(t *testing.T) {
	server := NewServer()
	snapshot := server.DiagnosisSnapshot()
	if snapshot.GeneratedAt.IsZero() {
		t.Fatalf("DiagnosisSnapshot = %+v, want generation time", snapshot)
	}
	probe := server.DiagnosisProbe("orders", "Watch")
	if probe.Service != "orders" || probe.Method != "Watch" {
		t.Fatalf("DiagnosisProbe = %+v, want requested service and method", probe)
	}
}

func TestHTTPServerServeHTTP(t *testing.T) {
	s := NewServer()
	err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
		},
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRPCErrorHelpers(t *testing.T) {
	plainErr := errors.New("plain failure")
	if got := messageOf(nil); got != "" {
		t.Fatalf("messageOf(nil) = %q, want empty", got)
	}
	if got := messageOf(NewError(CodeNotFound, "missing user")); got != "missing user" {
		t.Fatalf("messageOf(rpc error) = %q, want missing user", got)
	}
	if got := messageOf(plainErr); got != "plain failure" {
		t.Fatalf("messageOf(plain error) = %q, want plain failure", got)
	}

	statusCases := []struct {
		name string
		code Code
		want int
	}{
		{name: "ok", code: CodeOK, want: http.StatusOK},
		{name: "invalid argument", code: CodeInvalidArgument, want: http.StatusBadRequest},
		{name: "not found", code: CodeNotFound, want: http.StatusNotFound},
		{name: "unavailable", code: CodeUnavailable, want: http.StatusServiceUnavailable},
		{name: "unknown code", code: Code("unknown-test-code"), want: http.StatusInternalServerError},
	}
	for _, tt := range statusCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpStatusFromCode(tt.code); got != tt.want {
				t.Fatalf("httpStatusFromCode(%q) = %d, want %d", tt.code, got, tt.want)
			}
		})
	}

	if isRetryable(nil) {
		t.Fatal("isRetryable(nil) = true, want false")
	}
	if !isRetryable(NewError(CodeUnavailable, "temporary outage")) {
		t.Fatal("isRetryable(unavailable) = false, want true")
	}
	if isRetryable(NewError(CodeInvalidArgument, "bad request")) {
		t.Fatal("isRetryable(invalid argument) = true, want false")
	}
	if !isRetryable(plainErr) {
		t.Fatal("isRetryable(plain error) = false, want true because plain errors map to internal")
	}
}

func TestRPCTimeoutMiddlewareContracts(t *testing.T) {
	fast := TimeoutMiddleware(0)(func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	got, err := fast(context.Background(), "req")
	if err != nil || got != "ok" {
		t.Fatalf("TimeoutMiddleware(0) response = %v err=%v, want ok nil", got, err)
	}

	slow := TimeoutMiddleware(time.Millisecond)(func(ctx context.Context, req any) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	got, err = slow(context.Background(), "req")
	if got != nil || CodeOf(err) != CodeDeadlineExceeded {
		t.Fatalf("timeout response = %v err=%v code=%s, want nil deadline_exceeded", got, err, CodeOf(err))
	}
}

func TestRPCRequestIDMiddlewareAddsAndPreservesID(t *testing.T) {
	var generated string
	mw := RequestIDMiddleware()(func(ctx context.Context, req any) (any, error) {
		generated = metadata.RequestIDFromContext(ctx)
		return "ok", nil
	})
	if _, err := mw(context.Background(), "req"); err != nil {
		t.Fatalf("RequestIDMiddleware generated call: %v", err)
	}
	if generated == "" {
		t.Fatal("RequestIDMiddleware did not generate request id")
	}

	var preserved string
	mw = RequestIDMiddleware()(func(ctx context.Context, req any) (any, error) {
		preserved = metadata.RequestIDFromContext(ctx)
		return "ok", nil
	})
	ctx := metadata.Append(context.Background(), metadata.RequestIDKey, "rid-existing")
	if _, err := mw(ctx, "req"); err != nil {
		t.Fatalf("RequestIDMiddleware preserved call: %v", err)
	}
	if preserved != "rid-existing" {
		t.Fatalf("request id = %q, want rid-existing", preserved)
	}
}

func TestRPCClientStreamTraceMiddlewareForwardsCall(t *testing.T) {
	called := false
	mw := ClientStreamTraceMiddleware("orders")(func(ctx context.Context, method string) (*Stream, error) {
		called = true
		if method != "orders/Watch" {
			t.Fatalf("method = %q, want orders/Watch", method)
		}
		return nil, nil
	})
	stream, err := mw(context.Background(), "orders/Watch")
	if err != nil || stream != nil || !called {
		t.Fatalf("ClientStreamTraceMiddleware result = %#v/%v called=%t, want nil/nil/true", stream, err, called)
	}
}

func TestRPCServerAuthMiddlewareContracts(t *testing.T) {
	mw := ServerAuthMiddleware(auth.StaticTokenValidator("secret", "rpc-user"))(func(ctx context.Context, req any) (any, error) {
		return auth.SubjectFromContext(ctx), nil
	})

	if _, err := mw(context.Background(), "req"); CodeOf(err) != CodeUnauthenticated {
		t.Fatalf("missing credentials error = %v code=%s, want unauthenticated", err, CodeOf(err))
	}
	badCtx := metadata.Append(context.Background(), auth.MetadataKey, auth.BearerValue("wrong"))
	if _, err := mw(badCtx, "req"); CodeOf(err) != CodeUnauthenticated {
		t.Fatalf("invalid credentials error = %v code=%s, want unauthenticated", err, CodeOf(err))
	}
	goodCtx := metadata.Append(context.Background(), auth.AuthorizationHeader, auth.BearerValue("secret"))
	got, err := mw(goodCtx, "req")
	if err != nil || got != "rpc-user" {
		t.Fatalf("authorized response = %v err=%v, want rpc-user nil", got, err)
	}

	nilValidator := ServerAuthMiddleware(nil)(func(ctx context.Context, req any) (any, error) {
		return "unreachable", nil
	})
	if _, err := nilValidator(goodCtx, "req"); CodeOf(err) != CodeInternal {
		t.Fatalf("nil validator error = %v code=%s, want internal", err, CodeOf(err))
	}
}

func TestHTTPServerServeHTTPRejectsNilBody(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{}`))
	req.Body = nil
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusBadRequest)
	}
}

func TestHTTPServerServiceNamesAreDeterministic(t *testing.T) {
	s := NewServer()
	for _, service := range []string{"zeta", "alpha", "middle"} {
		if err := s.RegisterService(ServiceDesc{Name: service, Methods: []MethodDesc{{
			Name:       "Ping",
			NewRequest: func() any { return new(helloRequest) },
			Handler: func(ctx context.Context, req any) (any, error) {
				return helloResponse{Message: "pong"}, nil
			},
		}}}, nil); err != nil {
			t.Fatal(err)
		}
	}

	names := s.serviceNames()
	want := []string{"alpha", "middle", "zeta"}
	if len(names) != len(want) {
		t.Fatalf("serviceNames = %#v, want %#v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("serviceNames = %#v, want %#v", names, want)
		}
	}
}

type fakeLifecycleRegistrar struct {
	mu              sync.Mutex
	registerCalls   []string
	deregisterCalls []string
	registerErr     error
	withOptions     bool
	ttl             time.Duration
}

func (f *fakeLifecycleRegistrar) RegisterService(_ context.Context, service string, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerCalls = append(f.registerCalls, service+"@"+endpoint)
	return f.registerErr
}

func (f *fakeLifecycleRegistrar) DeregisterService(_ context.Context, service string, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deregisterCalls = append(f.deregisterCalls, service+"@"+endpoint)
	return nil
}

func (f *fakeLifecycleRegistrar) RegisterServiceWithOptions(ctx context.Context, service string, endpoint string, opts ...RegisterOption) error {
	var ro registerOptions
	for _, opt := range opts {
		opt(&ro)
	}
	f.mu.Lock()
	f.withOptions = true
	f.ttl = ro.ttl
	f.mu.Unlock()
	return f.RegisterService(ctx, service, endpoint)
}

func (f *fakeLifecycleRegistrar) snapshot() ([]string, []string, bool, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.registerCalls...), append([]string(nil), f.deregisterCalls...), f.withOptions, f.ttl
}

func TestHTTPServerRegistryLifecycleBoundaries(t *testing.T) {
	registrar := &fakeLifecycleRegistrar{}
	s := NewServer(WithRegistry(registrar, "", ""), WithRegistryTTL(time.Minute), WithRegistryRefreshInterval(time.Millisecond))
	for _, service := range []string{"zeta", "alpha"} {
		if err := s.RegisterService(ServiceDesc{Name: service, Methods: []MethodDesc{{Name: "Ping", NewRequest: func() any { return new(helloRequest) }, Handler: func(context.Context, any) (any, error) { return helloResponse{}, nil }}}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.register(context.Background(), "127.0.0.1:9000/"); err != nil {
		t.Fatalf("register error = %v", err)
	}
	want := []string{"alpha@http://127.0.0.1:9000", "zeta@http://127.0.0.1:9000"}
	registerCalls, _, withOptions, ttl := registrar.snapshot()
	if len(registerCalls) != len(want) || registerCalls[0] != want[0] || registerCalls[1] != want[1] {
		t.Fatalf("register calls = %#v, want %#v", registerCalls, want)
	}
	if !withOptions || ttl != time.Minute {
		t.Fatalf("register options used=%v ttl=%s, want ttl %s", withOptions, ttl, time.Minute)
	}
	if s.opts.advertiseEndpoint != "http://127.0.0.1:9000" {
		t.Fatalf("advertise endpoint = %q, want defaulted listener endpoint", s.opts.advertiseEndpoint)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.startRegistryKeepalive(ctx)
	deadline := time.After(time.Second)
	for {
		registerCalls, _, _, _ = registrar.snapshot()
		if len(registerCalls) > len(want) {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("timed out waiting for keepalive register, calls=%#v", registerCalls)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()

	s.deregister(context.Background())
	_, deregisterCalls, _, _ := registrar.snapshot()
	if len(deregisterCalls) != len(want) || deregisterCalls[0] != want[0] || deregisterCalls[1] != want[1] {
		t.Fatalf("deregister calls = %#v, want %#v", deregisterCalls, want)
	}
}

func TestHTTPServerStopTimeoutAndRegisterError(t *testing.T) {
	if got := NewServer().readHeaderTimeout(); got != 3*time.Second {
		t.Fatalf("default readHeaderTimeout = %s, want 3s", got)
	}
	if got := NewServer(WithServerReadHeaderTimeout(123 * time.Millisecond)).readHeaderTimeout(); got != 123*time.Millisecond {
		t.Fatalf("custom readHeaderTimeout = %s, want 123ms", got)
	}
	s := NewServer()
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Run error = %v", err)
	}

	registrar := &fakeLifecycleRegistrar{registerErr: errors.New("boom")}
	failing := NewServer(WithRegistry(registrar, "", ""))
	if err := failing.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{Name: "Ping", NewRequest: func() any { return new(helloRequest) }, Handler: func(context.Context, any) (any, error) { return helloResponse{}, nil }}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := failing.register(context.Background(), "127.0.0.1:9000"); err == nil || !strings.Contains(err.Error(), "register rpc service greeter") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("register error = %v, want service wrapping", err)
	}
}

func TestHTTPServerStartShutdownAliases(t *testing.T) {
	s := NewServer(WithAddress("not a valid listen address"))
	if err := s.Start(); err == nil || !strings.Contains(err.Error(), "listen rpc") {
		t.Fatalf("Start invalid address error = %v, want listen rpc error", err)
	}
	if err := NewServer().Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before Run error = %v", err)
	}
}

func TestHTTPServerRunShutdownLifecycle(t *testing.T) {
	s := NewServer(WithAddress("127.0.0.1:0"))
	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run()
	}()

	deadline := time.After(time.Second)
	for s.State().State != "running" {
		select {
		case err := <-runErr:
			t.Fatalf("Run exited before running: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for server to reach running state")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown running server error = %v", err)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run after Shutdown error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run to exit after Shutdown")
	}
}

func TestServerGovernanceManagerOverridesExplicitRuleSet(t *testing.T) {
	stale := coregovernance.NewRuleSet(coregovernance.Rule{Name: "stale", Transport: coregovernance.TransportRPC, Service: "greeter"})
	manager, err := coregovernance.NewManager(coregovernance.Config{Rules: []coregovernance.Rule{{Name: "live", Transport: coregovernance.TransportRPC, Service: "greeter"}}})
	if err != nil {
		t.Fatal(err)
	}

	s := NewServer(WithServerRuleSet(stale), WithServerGovernanceManager(manager))
	decision := s.governanceDecision(coregovernance.Request{Transport: coregovernance.TransportRPC, Service: "greeter"})
	if !decision.Matched || decision.RuleName != "live" {
		t.Fatalf("decision = %#v, want manager rule", decision)
	}
}

func TestServerGovernanceSuiteProvidesRules(t *testing.T) {
	suite := coregovernance.MustNewSuite(coregovernance.NewPlugin("rpc-default", coregovernance.Rule{Name: "suite", Transport: coregovernance.TransportRPC, Service: "greeter"}))
	s := NewServer(WithServerGovernanceSuite(suite))
	decision := s.governanceDecision(coregovernance.Request{Transport: coregovernance.TransportRPC, Service: "greeter"})
	if !decision.Matched || decision.RuleName != "suite" {
		t.Fatalf("decision = %#v, want suite rule", decision)
	}
}

func TestServerGovernanceManagerOverridesLaterSuite(t *testing.T) {
	manager, err := coregovernance.NewManager(coregovernance.Config{Rules: []coregovernance.Rule{{Name: "live", Transport: coregovernance.TransportRPC, Service: "greeter"}}})
	if err != nil {
		t.Fatal(err)
	}
	suite := coregovernance.MustNewSuite(coregovernance.NewPlugin("stale", coregovernance.Rule{Name: "stale", Transport: coregovernance.TransportRPC, Service: "greeter"}))
	s := NewServer(WithServerGovernanceManager(manager), WithServerGovernanceSuite(suite))
	decision := s.governanceDecision(coregovernance.Request{Transport: coregovernance.TransportRPC, Service: "greeter"})
	if !decision.Matched || decision.RuleName != "live" {
		t.Fatalf("decision = %#v, want manager rule", decision)
	}
}

func TestHTTPServerProtoCodec(t *testing.T) {
	s := NewServer(WithServerCodec(ProtoCodec{}))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(wrapperspb.StringValue) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return wrapperspb.String("hello " + req.(*wrapperspb.StringValue).Value), nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithCodec(ProtoCodec{}))
	if err != nil {
		t.Fatal(err)
	}
	var resp wrapperspb.StringValue
	if err := c.Call(context.Background(), "greeter/SayHello", wrapperspb.String("gofly"), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Value != "hello gofly" {
		t.Fatalf("response = %q, want hello gofly", resp.Value)
	}
}

func TestHTTPServerAdaptiveLimiterRejectsWhenSaturated(t *testing.T) {
	limiter := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
	first, err := limiter.Allow()
	if err != nil {
		t.Fatalf("pre-acquire limiter: %v", err)
	}
	defer first.Done(true)

	s := NewServer(WithServerAdaptiveLimiter(limiter))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Version: "v1", Metadata: map[string]string{"owner": "platform"}, Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Metadata:   map[string]string{"request": "helloRequest", "response": "helloResponse"},
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}

func TestHTTPServerAdaptiveBreakerRejectsAfterFailure(t *testing.T) {
	s := NewServer(WithServerAdaptiveBreaker(breaker.NewAdaptive(
		breaker.WithAdaptiveMinRequests(1),
		breaker.WithAdaptiveFailureRatio(0.1),
		breaker.WithAdaptiveK(1),
		breaker.WithAdaptiveOpenTimeout(time.Second),
	)))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Unstable",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return nil, NewError(CodeInternal, "boom")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	s.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/rpc/greeter/Unstable", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", first.Code)
	}

	second := httptest.NewRecorder()
	s.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/rpc/greeter/Unstable", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503", second.Code)
	}
}

func TestHTTPServerServiceInfosAndErrorCode(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Missing",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return nil, NewError(CodeNotFound, "hello not found")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	if len(s.GetServiceInfos()) != 1 {
		t.Fatalf("service infos len = %d, want 1", len(s.GetServiceInfos()))
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/Missing", strings.NewReader(`{"payload":{"name":"gofly"}}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHTTPServerMethodMetadataAndMiddleware(t *testing.T) {
	s := NewServer()
	methodMiddleware := func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			md, _ := metadata.FromContext(ctx)
			if md.Get("service-owner") != "platform" || md.Get("method-kind") != "read" || md.Get("shared") != "method" {
				return nil, NewError(CodeInternal, "metadata not applied")
			}
			ctx = metadata.Append(ctx, "method-middleware", "seen")
			return next(ctx, req)
		}
	}
	if err := s.RegisterService(ServiceDesc{
		Name:     "greeter",
		Version:  "v1",
		Metadata: map[string]string{"service-owner": "platform", "shared": "service"},
		Methods: []MethodDesc{{
			Name:        "SayHello",
			NewRequest:  func() any { return new(helloRequest) },
			Metadata:    map[string]string{"method-kind": "read", "shared": "method"},
			Middlewares: []endpoint.Middleware{methodMiddleware},
			Handler: func(ctx context.Context, req any) (any, error) {
				md, _ := metadata.FromContext(ctx)
				if md.Get("method-middleware") != "seen" || md.Get("rpc.service.version") != "v1" {
					return nil, NewError(CodeInternal, "method middleware not applied")
				}
				return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
			},
		}},
	}, nil); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestHTTPServerMethodTimeoutAndServiceSnapshot(t *testing.T) {
	s := NewServer()
	serviceMetadata := map[string]string{"owner": "platform"}
	methodMetadata := map[string]string{"kind": "slow"}
	streamMetadata := map[string]string{"mode": "duplex"}
	if err := s.RegisterService(ServiceDesc{
		Name:     "greeter",
		Version:  "v2",
		Metadata: serviceMetadata,
		Methods: []MethodDesc{{
			Name:       "Slow",
			NewRequest: func() any { return new(helloRequest) },
			Timeout:    time.Millisecond,
			Metadata:   methodMetadata,
			Handler: func(ctx context.Context, req any) (any, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}},
		Streams: []StreamDesc{{
			Name:        "Chat",
			NewMessage:  func() any { return new(helloRequest) },
			Timeout:     2 * time.Second,
			Metadata:    streamMetadata,
			Middlewares: []StreamMiddleware{StreamRequestIDMiddleware()},
			Handler:     func(context.Context, *Stream) error { return nil },
		}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	serviceMetadata["owner"] = "mutated"
	methodMetadata["kind"] = "mutated"
	streamMetadata["mode"] = "mutated"

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/Slow", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusGatewayTimeout)
	}

	snapshots := s.ServiceSnapshots()
	if len(snapshots) != 1 || snapshots[0].Version != "v2" || snapshots[0].Metadata["owner"] != "platform" {
		t.Fatalf("service snapshots = %#v, want version and defensive service metadata", snapshots)
	}
	if len(snapshots[0].MethodDetails) != 1 || snapshots[0].MethodDetails[0].Timeout != "1ms" || snapshots[0].MethodDetails[0].Metadata["kind"] != "slow" {
		t.Fatalf("method details = %#v, want timeout and defensive metadata", snapshots[0].MethodDetails)
	}
	if len(snapshots[0].Streams) != 1 || snapshots[0].Streams[0].Timeout != "2s" || snapshots[0].Streams[0].Metadata["mode"] != "duplex" || snapshots[0].Streams[0].Middlewares != 1 {
		t.Fatalf("stream details = %#v, want timeout, middleware count and defensive metadata", snapshots[0].Streams)
	}
}

func TestHTTPServerGovernanceRuleRuntimeRateLimit(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-rate",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			RateLimit: coregovernance.RateLimitPolicy{Rate: 1, Burst: 1},
		},
	})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		return helloResponse{Message: "hello"}, nil
	})

	first := httptest.NewRecorder()
	s.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, body = %s, want 200", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	s.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, body = %s, want 429", second.Code, second.Body.String())
	}
	var env responseEnvelope
	if err := json.NewDecoder(second.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != CodeResourceExhausted {
		t.Fatalf("code = %q, want %q", env.Code, CodeResourceExhausted)
	}
}

func TestHTTPServerGovernanceRuleRuntimeConcurrencyLimit(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-concurrency",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			Concurrency: coregovernance.ConcurrencyPolicy{Limit: 1},
		},
	})
	entered := make(chan struct{})
	release := make(chan struct{})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return helloResponse{Message: "hello"}, nil
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
		done <- rec
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first request to enter handler")
	}

	second := httptest.NewRecorder()
	s.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if second.Code != http.StatusServiceUnavailable {
		close(release)
		t.Fatalf("second status = %d, body = %s, want 503", second.Code, second.Body.String())
	}
	close(release)
	select {
	case first := <-done:
		if first.Code != http.StatusOK {
			t.Fatalf("first status = %d, body = %s, want 200", first.Code, first.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first request to finish")
	}
}

func TestHTTPServerGovernanceRuleRuntimeBreaker(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-breaker",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			Breaker: coregovernance.BreakerPolicy{Enabled: true, MinRequests: 1, FailureRatio: 0.1, OpenTimeout: time.Second},
		},
	})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		return nil, NewError(CodeInternal, "boom")
	})

	first := httptest.NewRecorder()
	s.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, body = %s, want 500", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	s.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if second.Code != http.StatusInternalServerError {
		t.Fatalf("second status = %d, body = %s, want 500", second.Code, second.Body.String())
	}

	third := httptest.NewRecorder()
	s.ServeHTTP(third, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if third.Code != http.StatusServiceUnavailable {
		t.Fatalf("third status = %d, body = %s, want 503", third.Code, third.Body.String())
	}
}

func TestHTTPServerGovernanceRuleRuntimeTimeoutAndMetadata(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-timeout-metadata",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			Timeout: 5 * time.Millisecond,
			Metadata: map[string]string{
				"x-policy": "enabled",
			},
			Canary: coregovernance.CanaryPolicy{
				Ratio:   1,
				Service: "greeter-canary",
				Headers: map[string]string{"x-canary-group": "blue"},
			},
		},
	})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		md, ok := metadata.FromContext(ctx)
		if !ok || md.Get("x-policy") != "enabled" || md.Get(coregovernance.HeaderCanary) != "true" || md.Get(coregovernance.HeaderCanaryService) != "greeter-canary" || md.Get("x-canary-group") != "blue" {
			t.Fatalf("metadata = %#v, want governance policy and canary metadata", md)
		}
		<-ctx.Done()
		return nil, nil
	})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, body = %s, want 504", rec.Code, rec.Body.String())
	}
	var env responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != CodeDeadlineExceeded {
		t.Fatalf("code = %q, want %q", env.Code, CodeDeadlineExceeded)
	}
}

func TestHTTPServerGovernanceRuleRuntimeMaxBodyBytes(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-body-limit",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			MaxBodyBytes: 8,
		},
	})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		return helloResponse{Message: "hello"}, nil
	})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s, want 413", rec.Code, rec.Body.String())
	}
}

func newGovernedGreeterServer(t *testing.T, rules *coregovernance.RuleSet, handler func(context.Context, any) (any, error)) *HTTPServer {
	t.Helper()
	s := NewServer(WithServerRuleSet(rules))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler:    handler,
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHTTPServerAdminEndpoints(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{Name: "rpc-default", Transport: coregovernance.TransportRPC, Service: "greeter"})
	clientConn, serverConn := net.Pipe()
	muxClient := NewExperimentalMuxClientAdapter(clientConn)
	muxServer := NewExperimentalMuxServerAdapter(serverConn)
	defer muxClient.Close()
	defer muxServer.Close()
	if err := muxServer.RegisterStream("greeter/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(ctx, Message{Payload: append([]byte("admin:"), msg.Payload...)}); err != nil {
			return err
		}
		return stream.Close(ctx, "ok")
	}); err != nil {
		t.Fatalf("RegisterStream: %v", err)
	}
	serveCtx, cancelMux := context.WithCancel(context.Background())
	defer cancelMux()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- muxServer.Serve(serveCtx)
	}()
	stream, err := muxClient.OpenStream(context.Background(), "greeter/Watch")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("probe")}); err != nil {
		t.Fatalf("mux probe send: %v", err)
	}
	assertMuxPayload(t, stream, "admin:probe")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("mux probe terminal receive = %v, want EOF", err)
	}

	s := NewServer(WithServerAdminToken("secret"), WithServerAdaptiveLimiter(limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1))), WithServerRuleSet(rules), WithExperimentalMuxServerAdapter(muxServer))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Version: "v1", Metadata: map[string]string{"owner": "platform"}, Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Metadata:   map[string]string{"request": "helloRequest", "response": "helloResponse"},
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRecorder()
	s.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/rpc/admin/state", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	stateReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/state", nil)
	stateReq.Header.Set("Authorization", "Bearer secret")
	stateRec := httptest.NewRecorder()
	s.ServeHTTP(stateRec, stateReq)
	if stateRec.Code != http.StatusOK {
		t.Fatalf("state status = %d, want %d", stateRec.Code, http.StatusOK)
	}
	var state StateSnapshot
	if err := json.NewDecoder(stateRec.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.State != "initialized" {
		t.Fatalf("state = %#v, want initialized", state)
	}

	servicesReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/services", nil)
	servicesReq.Header.Set("Authorization", "Bearer secret")
	servicesRec := httptest.NewRecorder()
	s.ServeHTTP(servicesRec, servicesReq)
	if servicesRec.Code != http.StatusOK {
		t.Fatalf("services status = %d, want %d", servicesRec.Code, http.StatusOK)
	}
	var services []ServiceSnapshot
	if err := json.NewDecoder(servicesRec.Body).Decode(&services); err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Name != "greeter" || len(services[0].Methods) != 1 || services[0].Methods[0] != "SayHello" {
		t.Fatalf("services = %#v, want greeter/SayHello", services)
	}

	descriptorsReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors", nil)
	descriptorsReq.Header.Set("Authorization", "Bearer secret")
	descriptorsRec := httptest.NewRecorder()
	s.ServeHTTP(descriptorsRec, descriptorsReq)
	if descriptorsRec.Code != http.StatusOK {
		t.Fatalf("descriptors status = %d, want %d", descriptorsRec.Code, http.StatusOK)
	}
	var descriptors map[string]Descriptor
	if err := json.NewDecoder(descriptorsRec.Body).Decode(&descriptors); err != nil {
		t.Fatal(err)
	}
	desc, ok := descriptors["greeter"]
	if !ok || desc.Name != "greeter" || desc.Version != "v1" || desc.Metadata["owner"] != "platform" {
		t.Fatalf("descriptors = %#v, want greeter v1 descriptor", descriptors)
	}
	if len(desc.Methods) != 1 || desc.Methods[0].Name != "SayHello" || desc.Methods[0].Request != "helloRequest" || desc.Methods[0].Response != "helloResponse" {
		t.Fatalf("descriptor methods = %#v, want SayHello request/response contract", desc.Methods)
	}

	descriptorReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors/greeter", nil)
	descriptorReq.Header.Set("Authorization", "Bearer secret")
	descriptorRec := httptest.NewRecorder()
	s.ServeHTTP(descriptorRec, descriptorReq)
	if descriptorRec.Code != http.StatusOK {
		t.Fatalf("descriptor status = %d, want %d", descriptorRec.Code, http.StatusOK)
	}
	var single Descriptor
	if err := json.NewDecoder(descriptorRec.Body).Decode(&single); err != nil {
		t.Fatal(err)
	}
	if single.Name != "greeter" || len(single.Methods) != 1 || single.Methods[0].Request != "helloRequest" {
		t.Fatalf("single descriptor = %#v, want greeter contract", single)
	}

	missingDescriptorReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors/missing", nil)
	missingDescriptorReq.Header.Set("Authorization", "Bearer secret")
	missingDescriptorRec := httptest.NewRecorder()
	s.ServeHTTP(missingDescriptorRec, missingDescriptorReq)
	if missingDescriptorRec.Code != http.StatusNotFound {
		t.Fatalf("missing descriptor status = %d, want %d", missingDescriptorRec.Code, http.StatusNotFound)
	}

	invalidDescriptorReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors/greeter%2Fv1", nil)
	invalidDescriptorReq.Header.Set("Authorization", "Bearer secret")
	invalidDescriptorRec := httptest.NewRecorder()
	s.ServeHTTP(invalidDescriptorRec, invalidDescriptorReq)
	if invalidDescriptorRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid descriptor status = %d, want %d", invalidDescriptorRec.Code, http.StatusBadRequest)
	}

	compatibilityAsDescriptorReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors/greeter/compatibility", nil)
	compatibilityAsDescriptorReq.Header.Set("Authorization", "Bearer secret")
	compatibilityAsDescriptorRec := httptest.NewRecorder()
	s.ServeHTTP(compatibilityAsDescriptorRec, compatibilityAsDescriptorReq)
	if compatibilityAsDescriptorRec.Code != http.StatusBadRequest {
		t.Fatalf("compatibility descriptor status = %d, want %d", compatibilityAsDescriptorRec.Code, http.StatusBadRequest)
	}

	candidate := single
	candidate.Methods[0].Response = "ChangedResponse"
	candidateData, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	compatibilityReq := httptest.NewRequest(http.MethodPost, "/rpc/admin/descriptors/greeter/compatibility", strings.NewReader(string(candidateData)))
	compatibilityReq.Header.Set("Authorization", "Bearer secret")
	compatibilityRec := httptest.NewRecorder()
	s.ServeHTTP(compatibilityRec, compatibilityReq)
	if compatibilityRec.Code != http.StatusConflict {
		t.Fatalf("compatibility status = %d, want %d", compatibilityRec.Code, http.StatusConflict)
	}
	var compatibility DescriptorCompatibilityReport
	if err := json.NewDecoder(compatibilityRec.Body).Decode(&compatibility); err != nil {
		t.Fatal(err)
	}
	if !compatibility.HasBreaking() {
		t.Fatalf("compatibility = %#v, want breaking response change", compatibility)
	}

	invalidTargetReq := httptest.NewRequest(
		http.MethodPost,
		"/rpc/admin/descriptors/greeter/compatibility",
		strings.NewReader(`{"methods":[{"name":"SayHello","request":"helloRequest","response":"helloResponse"}]}`),
	)
	invalidTargetReq.Header.Set("Authorization", "Bearer secret")
	invalidTargetRec := httptest.NewRecorder()
	s.ServeHTTP(invalidTargetRec, invalidTargetReq)
	if invalidTargetRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid target compatibility status = %d, want %d", invalidTargetRec.Code, http.StatusBadRequest)
	}

	oversizedCompatibilityReq := httptest.NewRequest(
		http.MethodPost,
		"/rpc/admin/descriptors/greeter/compatibility",
		strings.NewReader(`{"name":"`+strings.Repeat("x", maxDescriptorCompatibilityBytes)+`"}`),
	)
	oversizedCompatibilityReq.Header.Set("Authorization", "Bearer secret")
	oversizedCompatibilityRec := httptest.NewRecorder()
	s.ServeHTTP(oversizedCompatibilityRec, oversizedCompatibilityReq)
	if oversizedCompatibilityRec.Code != http.StatusBadRequest {
		t.Fatalf("oversized compatibility status = %d, want %d", oversizedCompatibilityRec.Code, http.StatusBadRequest)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/health", nil)
	healthReq.Header.Set("Authorization", "Bearer secret")
	healthRec := httptest.NewRecorder()
	s.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", healthRec.Code, http.StatusOK)
	}
	var health HealthSnapshot
	if err := json.NewDecoder(healthRec.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.Status != "ok" || health.State.State != "initialized" || len(health.Services) != 1 {
		t.Fatalf("health = %#v, want ok initialized with one service", health)
	}

	governanceReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/governance", nil)
	governanceReq.Header.Set("Authorization", "Bearer secret")
	governanceRec := httptest.NewRecorder()
	s.ServeHTTP(governanceRec, governanceReq)
	if governanceRec.Code != http.StatusOK {
		t.Fatalf("governance status = %d, want 200", governanceRec.Code)
	}
	var governance GovernanceSnapshot
	if err := json.NewDecoder(governanceRec.Body).Decode(&governance); err != nil {
		t.Fatal(err)
	}
	if len(governance.Components) == 0 || governance.Components[0].Kind != "adaptive_limiter" {
		t.Fatalf("governance = %#v, want adaptive limiter component", governance)
	}

	runtimeReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/runtime", nil)
	runtimeReq.Header.Set("Authorization", "Bearer secret")
	runtimeRec := httptest.NewRecorder()
	s.ServeHTTP(runtimeRec, runtimeReq)
	if runtimeRec.Code != http.StatusOK {
		t.Fatalf("runtime status = %d, want 200", runtimeRec.Code)
	}
	var runtimeSnapshot coreruntime.Snapshot
	if err := json.NewDecoder(runtimeRec.Body).Decode(&runtimeSnapshot); err != nil {
		t.Fatal(err)
	}
	runtimeByName := map[string]coreruntime.ComponentSnapshot{}
	for _, component := range runtimeSnapshot.Components {
		runtimeByName[component.Name] = component
	}
	httpRuntime, hasHTTPRuntime := runtimeByName["rpc.http.server"]
	muxRuntime, hasMuxRuntime := runtimeByName["rpc.mux.server"]
	_, hasSinkRegistry := runtimeByName["rpc.mux.sink.registry"]
	if len(runtimeSnapshot.Components) != 3 || !hasHTTPRuntime || !hasMuxRuntime || !hasSinkRegistry {
		t.Fatalf("runtime snapshot = %#v, want rpc http, mux server, and sink registry components", runtimeSnapshot)
	}
	if httpRuntime.Middleware == nil || len(httpRuntime.Middleware.Unary) == 0 {
		t.Fatalf("runtime middleware = %#v, want rpc middleware chain", httpRuntime.Middleware)
	}
	if muxRuntime.Status == "" || muxRuntime.Details == nil {
		t.Fatalf("mux runtime = %#v, want mux diagnosis details", muxRuntime)
	}

	diagnosisReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/diagnosis", nil)
	diagnosisReq.Header.Set("Authorization", "Bearer secret")
	diagnosisRec := httptest.NewRecorder()
	s.ServeHTTP(diagnosisRec, diagnosisReq)
	if diagnosisRec.Code != http.StatusOK {
		t.Fatalf("diagnosis status = %d, want 200", diagnosisRec.Code)
	}
	var diagnosis ServerDiagnosisSnapshot
	if err := json.NewDecoder(diagnosisRec.Body).Decode(&diagnosis); err != nil {
		t.Fatal(err)
	}
	if diagnosis.State.State != "initialized" ||
		len(diagnosis.Services) != 1 ||
		!diagnosis.Mux.Enabled ||
		diagnosis.Mux.Adapter.AcceptedStreams != 1 ||
		diagnosis.Mux.Transport.AcceptedStreams != 1 {
		t.Fatalf("server diagnosis = %+v, want mux adapter evidence", diagnosis)
	}

	filteredDiagnosisReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/diagnosis?service=greeter&method=SayHello", nil)
	filteredDiagnosisReq.Header.Set("Authorization", "Bearer secret")
	filteredDiagnosisRec := httptest.NewRecorder()
	s.ServeHTTP(filteredDiagnosisRec, filteredDiagnosisReq)
	if filteredDiagnosisRec.Code != http.StatusOK {
		t.Fatalf("filtered diagnosis status = %d, want 200", filteredDiagnosisRec.Code)
	}
	var filteredDiagnosis ServerDiagnosisSnapshot
	if err := json.NewDecoder(filteredDiagnosisRec.Body).Decode(&filteredDiagnosis); err != nil {
		t.Fatal(err)
	}
	if !filteredDiagnosis.Matched || filteredDiagnosis.Service != "greeter" || filteredDiagnosis.Method != "SayHello" || len(filteredDiagnosis.Services) != 1 {
		t.Fatalf("filtered diagnosis = %+v, want greeter/SayHello match", filteredDiagnosis)
	}
	missingDiagnosisReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/diagnosis?service=greeter&method=Missing", nil)
	missingDiagnosisReq.Header.Set("Authorization", "Bearer secret")
	missingDiagnosisRec := httptest.NewRecorder()
	s.ServeHTTP(missingDiagnosisRec, missingDiagnosisReq)
	if missingDiagnosisRec.Code != http.StatusOK {
		t.Fatalf("missing diagnosis status = %d, want 200", missingDiagnosisRec.Code)
	}
	var missingDiagnosis ServerDiagnosisSnapshot
	if err := json.NewDecoder(missingDiagnosisRec.Body).Decode(&missingDiagnosis); err != nil {
		t.Fatal(err)
	}
	if missingDiagnosis.Matched || len(missingDiagnosis.Services) != 0 {
		t.Fatalf("missing diagnosis = %+v, want unmatched empty service list", missingDiagnosis)
	}

	rulesReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/governance/rules", nil)
	rulesReq.Header.Set("Authorization", "Bearer secret")
	rulesRec := httptest.NewRecorder()
	s.ServeHTTP(rulesRec, rulesReq)
	if rulesRec.Code != http.StatusOK {
		t.Fatalf("governance rules status = %d, want 200", rulesRec.Code)
	}
	var gotRules []coregovernance.Rule
	if err := json.NewDecoder(rulesRec.Body).Decode(&gotRules); err != nil {
		t.Fatal(err)
	}
	if len(gotRules) != 1 || gotRules[0].Name != "rpc-default" {
		t.Fatalf("rules = %#v, want rpc-default", gotRules)
	}
}

func TestHTTPServerAdminWithoutTokenAllowsOnlyLocal(t *testing.T) {
	s := NewServer()
	local := httptest.NewRequest(http.MethodGet, "/rpc/admin/state", nil)
	local.RemoteAddr = "127.0.0.1:12345"
	localRec := httptest.NewRecorder()
	s.ServeHTTP(localRec, local)
	if localRec.Code != http.StatusOK {
		t.Fatalf("local admin status = %d, want 200", localRec.Code)
	}
	remote := httptest.NewRequest(http.MethodGet, "/rpc/admin/state", nil)
	remote.RemoteAddr = "203.0.113.10:12345"
	remoteRec := httptest.NewRecorder()
	s.ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote admin status = %d, want 403", remoteRec.Code)
	}
}

func TestServerStateNameBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		state int32
		want  string
	}{
		{name: "starting", state: serverStateStarting, want: "starting"},
		{name: "running", state: serverStateRunning, want: "running"},
		{name: "stopping", state: serverStateStopping, want: "stopping"},
		{name: "stopped", state: serverStateStopped, want: "stopped"},
		{name: "unknown", state: 999, want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := serverStateName(tt.state); got != tt.want {
				t.Fatalf("serverStateName(%d) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestHTTPServerTraceMiddlewarePropagatesMetadata(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	s := NewServer(WithServerMiddleware(TraceMiddleware("greeter.server")))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Trace",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			sc, ok := trace.FromContext(ctx)
			if !ok {
				t.Fatal("trace context missing")
			}
			if sc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || sc.SpanID == "00f067aa0ba902b7" {
				t.Fatalf("span = %#v, want child span", sc)
			}
			md, ok := metadata.FromContext(ctx)
			if !ok || md.Get(trace.TraceParentHeader) == "" {
				t.Fatalf("metadata = %#v, want traceparent", md)
			}
			return helloResponse{Message: sc.TraceID}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	body := `{"payload":{"name":"gofly"},"metadata":{"traceparent":"` + parent + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/Trace", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var env responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Metadata.Get(trace.TraceParentHeader) == "" {
		t.Fatalf("response metadata = %#v, want traceparent", env.Metadata)
	}
}

func TestHTTPServerTraceMiddlewareCanSampleOut(t *testing.T) {
	s := NewServer(WithServerMiddleware(TraceMiddlewareWithSampler("greeter.server", trace.NeverSampler())))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Trace",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			sc, ok := trace.FromContext(ctx)
			if !ok {
				t.Fatal("trace context missing")
			}
			if sc.Sampled {
				t.Fatal("span should be sampled out")
			}
			md, ok := metadata.FromContext(ctx)
			if !ok || md.Get(trace.SampledKey) != "false" {
				t.Fatalf("metadata = %#v, want sampled=false", md)
			}
			return helloResponse{Message: md.Get(trace.TraceParentHeader)}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/Trace", strings.NewReader(`{"payload":{"name":"gofly"}}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var env responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	data, _ := json.Marshal(env.Payload)
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	sc, ok := trace.ParseTraceParent(resp.Message)
	if !ok {
		t.Fatalf("traceparent = %q should parse", resp.Message)
	}
	if sc.Sampled {
		t.Fatalf("span = %#v, want sampled=false", sc)
	}
}

func TestHTTPServerRegistersAndDeregisters(t *testing.T) {
	registry := NewRegistry()
	s := NewServer(WithAddress("127.0.0.1:0"), WithRegistry(registry, "", ""))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Version: "v1", Metadata: map[string]string{"owner": "platform"}, Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Metadata:   map[string]string{"request": "helloRequest", "response": "helloResponse"},
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForRegistry(ctx, registry, "greeter", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rpc server to stop")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForRegistry(ctx, registry, "greeter", 0); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPServerRegistryTTLKeepalive(t *testing.T) {
	registry := NewRegistry()
	s := NewServer(
		WithAddress("127.0.0.1:0"),
		WithRegistry(registry, "", ""),
		WithRegistryTTL(50*time.Millisecond),
		WithRegistryRefreshInterval(10*time.Millisecond),
	)
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForRegistry(ctx, registry, "greeter", 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, err := registry.ResolveService(context.Background(), "greeter"); err != nil {
		t.Fatalf("resolve after ttl = %v, want keepalive to refresh registration", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rpc server to stop")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForRegistry(ctx, registry, "greeter", 0); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPServerStateTransitions(t *testing.T) {
	s := NewServer(WithAddress("127.0.0.1:0"))
	if got := s.State().State; got != "initialized" {
		t.Fatalf("initial state = %q, want initialized", got)
	}
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForState(ctx, s, "running"); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rpc server to stop")
	}
	if got := s.State().State; got != "stopped" {
		t.Fatalf("stopped state = %q, want stopped", got)
	}
}

func waitForRegistry(ctx context.Context, registry *Registry, service string, want int) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		endpoints, err := registry.ResolveService(ctx, service)
		if want == 0 && err != nil {
			return nil
		}
		if err == nil && len(endpoints) == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForState(ctx context.Context, server *HTTPServer, want string) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if server.State().State == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
