package smoke

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
)

const smokeJWTSecret = "gosky-smoke-jwt-secret-that-is-at-least-32-bytes"

func TestGeneratedProductionServiceSmoke(t *testing.T) {
	if os.Getenv("GOFLY_SKIP_GENERATED_SMOKE") == "true" {
		t.Skip("generated service smoke test disabled by GOFLY_SKIP_GENERATED_SMOKE")
	}
	repo := generatedProjectRoot(t)
	configPath := filepath.Join(repo, "etc", "gosky.json")
	originalConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config before smoke: %v", err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(configPath, originalConfig, 0o644); err != nil {
			t.Errorf("restore generated config after smoke: %v", err)
		}
	})
	assertInvalidRequestEnvelope(t)
	buildCtx, cancelBuild := context.WithTimeout(t.Context(), 45*time.Second)
	binary := generatedServiceBinary(t, buildCtx, repo)
	cancelBuild()
	reservation := reserveLocalAddrs(t, 4)
	restAddr, rpcAddr, muxAddr, adminAddr := reservation.addresses[0], reservation.addresses[1], reservation.addresses[2], reservation.addresses[3]
	rewriteSmokeConfig(t, repo, restAddr, rpcAddr, muxAddr, adminAddr)
	controlPlane := runGeneratedControlPlaneSmoke(t, binary, repo, restAddr, rpcAddr, muxAddr, adminAddr, reservation)
	metadata, ok := controlPlane["metadata"].(map[string]any)
	if !ok || metadata["generated.project"] != "available" || metadata["generated.project.runtime"] != "service,rest,rpc,governance,discovery" {
		t.Fatalf("control-plane metadata = %#v, want generated project runtime markers", metadata)
	}
	if metadata["generated.project.resilience"] != "timeout,rate,concurrency,breaker,retry" {
		t.Fatalf("control-plane resilience metadata = %#v, want generated resilience marker", metadata)
	}
	assertControlPlaneResilience(t, controlPlane)
	assertControlPlaneMuxOperatorHistory(t, controlPlane)

	recommendedReservation := reserveLocalAddrs(t, 4)
	recommendedRestAddr, recommendedRPCAddr, recommendedMuxAddr, recommendedAdminAddr := recommendedReservation.addresses[0], recommendedReservation.addresses[1], recommendedReservation.addresses[2], recommendedReservation.addresses[3]
	restoreRecommendedSmokeConfig(t, repo, recommendedRestAddr, recommendedRPCAddr, recommendedMuxAddr, recommendedAdminAddr)
	recommendedControlPlane := runGeneratedControlPlaneSmoke(t, binary, repo, recommendedRestAddr, recommendedRPCAddr, recommendedMuxAddr, recommendedAdminAddr, recommendedReservation)
	assertControlPlaneMuxConfigWarningsCleared(t, recommendedControlPlane)
}

func runGeneratedControlPlaneSmoke(t *testing.T, binary string, repo string, restAddr string, rpcAddr string, muxAddr string, adminAddr string, reservation *localAddrReservation) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GOFLAGS=-count=1", "GOSKY_JWT_SECRET="+smokeJWTSecret)
	cmd.WaitDelay = 3 * time.Second
	output := strings.Builder{}
	cmd.Stdout = &output
	cmd.Stderr = &output
	reservation.Release()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start generated service: %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			stopGeneratedService(t, cmd, &output)
		}
	})
	waitHTTPStatus(t, ctx, "http://"+restAddr+"/healthz", http.StatusOK, &output)
	assertAuthorizationHTTPContract(t, ctx, restAddr, &output)
	assertAuthorizationRPCContract(t, ctx, rpcAddr, &output)
	assertAuthorizationMuxContract(t, ctx, muxAddr, &output)
	waitOpenAPI(t, ctx, "http://"+restAddr+"/openapi.json", &output)
	controlPlane := waitControlPlane(t, ctx, "http://"+adminAddr+"/admin/control-plane", &output)
	stopGeneratedService(t, cmd, &output)
	stopped = true
	return controlPlane
}

func assertAuthorizationRPCContract(t *testing.T, ctx context.Context, rpcAddr string, output *strings.Builder) {
	t.Helper()
	validToken, err := auth.SignJWT(auth.JWTClaims{
		Subject:   "demo-reader",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra:     map[string]any{"tenant_id": "tenant:demo"},
	}, []byte(smokeJWTSecret))
	if err != nil {
		t.Fatalf("sign RPC authorization smoke token: %v", err)
	}
	crossTenantToken, err := auth.SignJWT(auth.JWTClaims{
		Subject:   "demo-reader",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra:     map[string]any{"tenant_id": "tenant:other"},
	}, []byte(smokeJWTSecret))
	if err != nil {
		t.Fatalf("sign RPC cross-tenant authorization smoke token: %v", err)
	}
	client, err := rpc.NewClient("http://"+rpcAddr, rpc.WithRetry(1))
	if err != nil {
		t.Fatalf("create authorization RPC client: %v", err)
	}
	defer func() { _ = client.Close() }()
	tests := []struct {
		name     string
		token    string
		tenantMD string
		wantCode rpc.Code
	}{
		{name: "missing JWT", wantCode: rpc.CodeUnauthenticated},
		{name: "same tenant policy", token: validToken, wantCode: rpc.CodeOK},
		{name: "cross tenant policy", token: crossTenantToken, tenantMD: "tenant:demo", wantCode: rpc.CodePermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCtx := ctx
			if tt.token != "" || tt.tenantMD != "" {
				callCtx = metadata.NewContext(callCtx, metadata.MD{
					auth.MetadataKey: auth.BearerValue(tt.token),
					"tenant_id":      tt.tenantMD,
				})
			}
			var response struct {
				Message string `json:"message"`
			}
			err := client.Call(callCtx, "greeter/SayHello", map[string]string{"name": "rpc-client"}, &response)
			if got := rpc.CodeOf(err); got != tt.wantCode {
				t.Fatalf("RPC code = %s, want %s; error=%v\n%s", got, tt.wantCode, err, output.String())
			}
			if tt.wantCode == rpc.CodeOK && response.Message != "hello rpc-client" {
				t.Fatalf("RPC response = %#v, want greeting\n%s", response, output.String())
			}
		})
	}
}

func assertAuthorizationMuxContract(t *testing.T, ctx context.Context, muxAddr string, output *strings.Builder) {
	t.Helper()
	validToken, err := auth.SignJWT(auth.JWTClaims{
		Subject:   "demo-reader",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra:     map[string]any{"tenant_id": "tenant:demo"},
	}, []byte(smokeJWTSecret))
	if err != nil {
		t.Fatalf("sign mux authorization smoke token: %v", err)
	}
	crossTenantToken, err := auth.SignJWT(auth.JWTClaims{
		Subject:   "demo-reader",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra:     map[string]any{"tenant_id": "tenant:other"},
	}, []byte(smokeJWTSecret))
	if err != nil {
		t.Fatalf("sign mux cross-tenant authorization smoke token: %v", err)
	}
	tests := []struct {
		name     string
		token    string
		tenantMD string
		wantCode rpc.Code
	}{
		{name: "missing JWT", wantCode: rpc.CodeUnauthenticated},
		{name: "same tenant policy", token: validToken, wantCode: rpc.CodeOK},
		{name: "cross tenant policy", token: crossTenantToken, tenantMD: "tenant:demo", wantCode: rpc.CodePermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if tt.token != "" || tt.tenantMD != "" {
				callCtx = metadata.NewContext(callCtx, metadata.MD{
					auth.MetadataKey: auth.BearerValue(tt.token),
					"tenant_id":      tt.tenantMD,
				})
			}
			client, err := rpc.DialExperimentalMuxClientAdapter(callCtx, "tcp", muxAddr)
			if err != nil {
				t.Fatalf("dial authorization mux client: %v\n%s", err, output.String())
			}
			defer func() { _ = client.Close() }()
			stream, err := client.OpenStream(callCtx, "greeter/Watch")
			if err != nil {
				t.Fatalf("open authorization mux stream: %v\n%s", err, output.String())
			}
			if tt.wantCode != rpc.CodeOK {
				if _, err := stream.Receive(callCtx); rpc.CodeOf(err) != tt.wantCode {
					t.Fatalf("mux stream code = %s, want %s; error=%v\n%s", rpc.CodeOf(err), tt.wantCode, err, output.String())
				}
				return
			}
			if err := stream.Send(callCtx, rpc.Message{Payload: []byte("mux-client")}); err != nil {
				t.Fatalf("send authorization mux stream: %v\n%s", err, output.String())
			}
			response, err := stream.Receive(callCtx)
			if err != nil || string(response.Payload) != "generated:mux-client" {
				t.Fatalf("mux stream response = %#v, err=%v; want generated:mux-client\n%s", response, err, output.String())
			}
		})
	}
}

func assertAuthorizationHTTPContract(t *testing.T, ctx context.Context, restAddr string, output *strings.Builder) {
	t.Helper()
	validToken, err := auth.SignJWT(auth.JWTClaims{
		Subject:   "project-reader",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra:     map[string]any{"tenant_id": "1001"},
	}, []byte(smokeJWTSecret))
	if err != nil {
		t.Fatalf("sign authorization smoke token: %v", err)
	}
	crossTenantToken, err := auth.SignJWT(auth.JWTClaims{
		Subject:   "project-reader",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra:     map[string]any{"tenant_id": "2002"},
	}, []byte(smokeJWTSecret))
	if err != nil {
		t.Fatalf("sign cross-tenant authorization smoke token: %v", err)
	}
	tests := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "missing JWT", wantStatus: http.StatusUnauthorized},
		{name: "same tenant requires configured project persistence", token: validToken, wantStatus: http.StatusServiceUnavailable},
		{name: "cross tenant requires configured project persistence", token: crossTenantToken, wantStatus: http.StatusServiceUnavailable},
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+restAddr+"/api/v1/projects/10001", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.token != "" {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(tt.token))
			}
			req.Header.Set("X-Tenant-ID", "1001")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("authorization request: %v\n%s", err, output.String())
			}
			defer func() {
				if closeErr := resp.Body.Close(); closeErr != nil {
					t.Errorf("close authorization response body: %v", closeErr)
				}
			}()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d\n%s", resp.StatusCode, tt.wantStatus, output.String())
			}
		})
	}
}

func generatedServiceBinary(t *testing.T, ctx context.Context, repo string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "gosky")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/gosky")
	build.Dir = repo
	build.Env = append(os.Environ(), "GOFLAGS=-count=1")
	output := strings.Builder{}
	build.Stdout = &output
	build.Stderr = &output
	if err := build.Run(); err != nil {
		t.Fatalf("build generated service smoke binary: %v\n%s", err, output.String())
	}
	return binary
}

func generatedProjectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate smoke test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

type localAddrReservation struct {
	addresses []string
	listeners []net.Listener
}

func reserveLocalAddrs(t *testing.T, count int) *localAddrReservation {
	t.Helper()
	reservation := &localAddrReservation{
		addresses: make([]string, 0, count),
		listeners: make([]net.Listener, 0, count),
	}
	t.Cleanup(reservation.Release)
	for range count {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve local port: %v", err)
		}
		reservation.listeners = append(reservation.listeners, listener)
		reservation.addresses = append(reservation.addresses, listener.Addr().String())
	}
	return reservation
}

func (r *localAddrReservation) Release() {
	for _, listener := range r.listeners {
		_ = listener.Close()
	}
	r.listeners = nil
}

func rewriteSmokeConfig(t *testing.T, repo string, restAddr string, rpcAddr string, muxAddr string, adminAddr string) {
	t.Helper()
	rewriteSmokeConfigWithOperatorHistory(t, repo, restAddr, rpcAddr, muxAddr, adminAddr, 4096, 8388608)
}

func restoreRecommendedSmokeConfig(t *testing.T, repo string, restAddr string, rpcAddr string, muxAddr string, adminAddr string) {
	t.Helper()
	rewriteSmokeConfigWithOperatorHistory(t, repo, restAddr, rpcAddr, muxAddr, adminAddr, 16, 65536)
}

func rewriteSmokeConfigWithOperatorHistory(t *testing.T, repo string, restAddr string, rpcAddr string, muxAddr string, adminAddr string, maxActions int, maxSizeBytes int) {
	t.Helper()
	path := filepath.Join(repo, "etc", "gosky.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	restHost, restPortText, err := net.SplitHostPort(restAddr)
	if err != nil {
		t.Fatalf("split rest addr: %v", err)
	}
	restPort, err := strconv.Atoi(restPortText)
	if err != nil {
		t.Fatalf("parse rest port %q: %v", restPortText, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode generated config: %v", err)
	}
	restConfig := jsonObject(t, cfg, "rest")
	restConfig["host"] = restHost
	restConfig["port"] = restPort
	adminConfig := jsonObject(t, cfg, "admin")
	adminConfig["addr"] = adminAddr
	rpcConfig := jsonObject(t, cfg, "rpc")
	rpcConfig["addr"] = rpcAddr
	rpcConfig["advertise"] = "http://" + rpcAddr
	muxConfig := jsonObject(t, rpcConfig, "mux")
	muxConfig["enabled"] = true
	muxConfig["probe"] = true
	muxConfig["addr"] = muxAddr
	logConfig := jsonObject(t, muxConfig, "log")
	logConfig["enabled"] = true
	logConfig["exportEvents"] = true
	otelConfig := jsonObject(t, logConfig, "otelCompatible")
	otelConfig["enabled"] = true
	otelConfig["sink"] = "slog"
	otelConfig["profileRef"] = ""
	otelConfig["sinks"] = []any{}
	otelConfig["operatorHistory"] = map[string]any{
		"enabled":      true,
		"store":        "file://mux-operator-history.jsonl",
		"maxActions":   maxActions,
		"maxSizeBytes": maxSizeBytes,
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("encode generated config: %v", err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write generated smoke config: %v", err)
	}
}

func jsonObject(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("generated config %q = %#v, want object", key, parent[key])
	}
	return value
}

func stopGeneratedService(t *testing.T, cmd *exec.Cmd, output *strings.Builder) {
	t.Helper()
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Logf("generated service process did not exit cleanly after kill; service output:\n%s", output.String())
		}
	}
}

func waitHTTPStatus(t *testing.T, ctx context.Context, url string, want int, output *strings.Builder) {
	t.Helper()
	client := http.Client{Timeout: time.Second}
	for ctx.Err() == nil {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d before timeout; service output:\n%s", url, want, output.String())
}

func waitOpenAPI(t *testing.T, ctx context.Context, url string, output *strings.Builder) {
	t.Helper()
	client := http.Client{Timeout: time.Second}
	for ctx.Err() == nil {
		resp, err := client.Get(url)
		if err == nil {
			var doc map[string]any
			decodeErr := json.NewDecoder(resp.Body).Decode(&doc)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil && doc["openapi"] != "" {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s did not return OpenAPI JSON before timeout; service output:\n%s", url, output.String())
}

func assertInvalidRequestEnvelope(t *testing.T) {
	t.Helper()
	rec := httptest.NewRecorder()
	rest.WriteError(rec, coreerrors.New(coreerrors.CodeInvalidArgument, "invalid request"))
	var envelope rest.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode invalid request rest.ErrorResponse: %v", err)
	}
	if rec.Code != http.StatusBadRequest || envelope.Code != coreerrors.CodeInvalidArgument || envelope.Status != http.StatusBadRequest {
		t.Fatalf("invalid request envelope = status %d body %+v, want rest.ErrorResponse invalid_argument", rec.Code, envelope)
	}
}

func waitControlPlane(t *testing.T, ctx context.Context, url string, output *strings.Builder) map[string]any {
	t.Helper()
	client := http.Client{Timeout: time.Second}
	for ctx.Err() == nil {
		resp, err := client.Get(url)
		if err == nil {
			var snapshot map[string]any
			decodeErr := json.NewDecoder(resp.Body).Decode(&snapshot)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil {
				return snapshot
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s did not return a control-plane snapshot before timeout; service output:\n%s", url, output.String())
	return nil
}

func assertControlPlaneResilience(t *testing.T, snapshot map[string]any) {
	t.Helper()
	configs, ok := snapshot["configs"].(map[string]any)
	if !ok {
		t.Fatalf("control-plane configs = %#v, want generated configs", snapshot["configs"])
	}
	resilience, ok := configs["generated.resilience"].(map[string]any)
	if !ok {
		t.Fatalf("generated.resilience config = %#v, want resilience profile", configs["generated.resilience"])
	}
	for _, key := range []string{"timeout", "rateLimit", "concurrency", "breaker", "retry", "adaptiveLimit", "restEnabled", "rpcEnabled", "gatewayEnabled"} {
		if resilience[key] != true {
			t.Fatalf("generated resilience[%s] = %#v in %#v, want true", key, resilience[key], resilience)
		}
	}
}

func assertControlPlaneMuxOperatorHistory(t *testing.T, snapshot map[string]any) {
	t.Helper()
	configs, ok := snapshot["configs"].(map[string]any)
	if !ok {
		t.Fatalf("control-plane configs = %#v, want generated configs", snapshot["configs"])
	}
	rpcConfig, ok := configs["generated.rpc"].(map[string]any)
	if !ok {
		t.Fatalf("generated.rpc config = %#v, want rpc config", configs["generated.rpc"])
	}
	mux, ok := rpcConfig["mux"].(map[string]any)
	if !ok {
		t.Fatalf("generated.rpc.mux config = %#v, want mux config", rpcConfig["mux"])
	}
	logConfig, ok := mux["log"].(map[string]any)
	if !ok {
		t.Fatalf("generated.rpc.mux.log config = %#v, want log config", mux["log"])
	}
	otel, ok := logConfig["otelCompatible"].(map[string]any)
	if !ok {
		t.Fatalf("generated.rpc.mux.log.otelCompatible config = %#v, want otel config", logConfig["otelCompatible"])
	}
	history, ok := otel["operatorHistory"].(map[string]any)
	if !ok {
		t.Fatalf("operatorHistory config = %#v, want map", otel["operatorHistory"])
	}
	if history["enabled"] != true {
		t.Fatalf("operatorHistory config = %#v, want smoke operator history enabled", history)
	}
	if history["store"] != "file://mux-operator-history.jsonl" {
		t.Fatalf("operatorHistory config = %#v, want file store", history)
	}
	if _, ok := history["maxActions"]; ok {
		if history["maxActions"] != float64(4096) || history["maxSizeBytes"] != float64(8388608) {
			t.Fatalf("operatorHistory config = %#v, want smoke warning tuning", history)
		}
	}
	historyStore, ok := configs["generated.rpcMuxOperatorHistoryStore"].(map[string]any)
	if !ok {
		t.Fatalf("generated.rpcMuxOperatorHistoryStore = %#v, want store evidence", configs["generated.rpcMuxOperatorHistoryStore"])
	}
	if containsJSONKey(historyStore, "actions") {
		t.Fatalf("generated.rpcMuxOperatorHistoryStore leaked actions at some level: %#v", historyStore)
	}
	store, ok := historyStore["store"].(map[string]any)
	if !ok || store["enabled"] != true || store["kind"] != "file" {
		t.Fatalf("generated rpc mux operator history store = %#v, want enabled file summary", historyStore["store"])
	}
	if status, ok := historyStore["integrityStatus"]; ok && status != "" && status != "empty" && status != "missing" && status != "missing_header" && status != "ok" {
		t.Fatalf("generated rpc mux operator history integrity = %#v, want redacted file integrity summary", historyStore["integrityStatus"])
	}
	warnings, ok := configs["generated.rpcMuxConfigWarnings"].([]any)
	if !ok || len(warnings) != 2 {
		t.Fatalf("generated.rpcMuxConfigWarnings = %#v, want two consumable warnings", configs["generated.rpcMuxConfigWarnings"])
	}
	assertRPCMuxConfigWarningSchemaConfig(t, configs)
	assertControlPlaneSchemaChecksumConfig(t, configs)
	assertRPCMuxConfigWarningValue(t, warnings[0], "maxActions", 4096, 1024)
	assertRPCMuxConfigWarningValue(t, warnings[1], "maxSizeBytes", 8388608, 1048576)
}

func assertControlPlaneMuxConfigWarningsCleared(t *testing.T, snapshot map[string]any) {
	t.Helper()
	configs, ok := snapshot["configs"].(map[string]any)
	if !ok {
		t.Fatalf("control-plane configs = %#v, want generated configs", snapshot["configs"])
	}
	assertRPCMuxConfigWarningSchemaConfig(t, configs)
	assertControlPlaneSchemaChecksumConfig(t, configs)
	if warnings, ok := configs["generated.rpcMuxConfigWarnings"]; ok {
		t.Fatalf("generated.rpcMuxConfigWarnings = %#v, want warning blob removed after recommended config restore", warnings)
	}
}

func assertRPCMuxConfigWarningSchemaConfig(t *testing.T, configs map[string]any) {
	t.Helper()
	schema, ok := configs["generated.rpcMuxConfigWarningSchema"].(map[string]any)
	if !ok {
		t.Fatalf("generated.rpcMuxConfigWarningSchema = %#v, want JSON schema object", configs["generated.rpcMuxConfigWarningSchema"])
	}
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("generated.rpcMuxConfigWarningSchema = %#v, want strict JSON object schema", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("generated.rpcMuxConfigWarningSchema properties = %#v, want property map", schema["properties"])
	}
	if _, ok := properties["recommended"]; !ok {
		t.Fatalf("generated.rpcMuxConfigWarningSchema properties = %#v, want recommended field", properties)
	}
}

func assertControlPlaneSchemaChecksumConfig(t *testing.T, configs map[string]any) {
	t.Helper()
	checksums, ok := configs["generated.controlPlaneSchemaChecksums"].(map[string]any)
	if !ok {
		t.Fatalf("generated.controlPlaneSchemaChecksums = %#v, want checksum map", configs["generated.controlPlaneSchemaChecksums"])
	}
	for _, key := range []string{"generated.rpcMuxConfigWarningSchema", "generated.rpcMuxOperatorAuditSchemas", "aiManifestSchema"} {
		if checksums[key] == "" {
			t.Fatalf("generated.controlPlaneSchemaChecksums[%s] is empty: %#v", key, checksums)
		}
	}
}

const rpcMuxConfigWarningSchema = "gofly.rpc_mux_config_warning.v1"

func assertRPCMuxConfigWarningValue(t *testing.T, value any, field string, current float64, recommended float64) {
	t.Helper()
	raw, ok := value.(string)
	if !ok {
		t.Fatalf("warning value = %#v, want JSON string", value)
	}
	var warning struct {
		Schema      string  `json:"schema"`
		Field       string  `json:"field"`
		Message     string  `json:"message"`
		Current     float64 `json:"current"`
		Recommended float64 `json:"recommended"`
	}
	if err := json.Unmarshal([]byte(raw), &warning); err != nil {
		t.Fatalf("decode warning %q: %v", raw, err)
	}
	if warning.Schema != rpcMuxConfigWarningSchema ||
		warning.Field != field ||
		!strings.Contains(warning.Message, field+" exceeds recommended") ||
		warning.Current != current ||
		warning.Recommended != recommended {
		t.Fatalf("warning = %+v, want %s current=%v recommended=%v", warning, field, current, recommended)
	}
}

func containsJSONKey(value any, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed[key]; ok {
			return true
		}
		for _, child := range typed {
			if containsJSONKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsJSONKey(child, key) {
				return true
			}
		}
	}
	return false
}
