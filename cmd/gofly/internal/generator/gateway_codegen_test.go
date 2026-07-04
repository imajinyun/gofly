package generator

import (
	"bufio"
	"context"
	"crypto/sha1" // #nosec G505 -- RFC 6455 requires SHA-1 for Sec-WebSocket-Accept in tests.
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imajinyun/gofly/gateway"
	"github.com/imajinyun/gofly/rest"
)

func TestGenerateGatewayWiresGovernanceManager(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateGateway(GatewayOptions{Name: "edge", Module: "example.com/edge", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	mainData, err := os.ReadFile(filepath.Join(dir, "cmd", "edge", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"github.com/imajinyun/gofly/core/governance"`,
		`appmq "example.com/edge/internal/mq"`,
		"configPath := appconfig.ResolveConfigPath(\"edge\")",
		"config.Load(configPath",
		"config.WithEnvExpansion()",
		"config.WithStrictFields()",
		"config.WithLoadValidator(appconfig.Validate)",
		"app.Bootstrap",
		"serviceConf.BootstrapConfig",
		"restConf := serviceConf.RESTConfig(c.Rest)",
		"rest.MustNewServer(\n\t\trestConf,",
		"serviceConf.RunOptions()",
		"governance.NewManager",
		"governance.WithPlugin(serviceConf.ProductionGovernancePlugin())",
		"appmq.NewBroker(c.MQ, governanceManager)",
		"gateway.WithGovernanceManager(governanceManager)",
		"rest.WithGovernanceManager(governanceManager)",
		"svc.NewServiceContext(c, mqBroker)",
		"gatewayConf, err := c.GatewayConfig(ctx)",
		"gateway.NewFromConfig(gatewayConf, nil",
	} {
		if !strings.Contains(string(mainData), want) {
			t.Fatalf("main.go missing governance wiring %q:\n%s", want, mainData)
		}
	}
	configData, err := os.ReadFile(filepath.Join(dir, "internal", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), "Governance") || !strings.Contains(string(configData), "governance.Config") || !strings.Contains(string(configData), "app.ServiceConf") || !strings.Contains(string(configData), "MQConfig") {
		t.Fatalf("config.go missing governance config:\n%s", configData)
	}
	for _, want := range []string{"Service        app.ServiceConf", "func ConfigPaths(name string) []string", "func ResolveConfigPath(name string) string", `paths := []string{"config.yaml", "config.yml", "config.toml", "config.json"}`, "func (c Config) ServiceConf() app.ServiceConf", "func Validate(c Config) error", "app.ValidateProductionConfig", "rest.ValidateProductionConfig", "production gateway admin requires"} {
		if !strings.Contains(string(configData), want) {
			t.Fatalf("gateway config.go missing production validator %q:\n%s", want, configData)
		}
	}
	for _, want := range []string{
		"OpenAPIImports []OpenAPIImportConfig",
		"func (c Config) GatewayConfig(ctx context.Context) (gateway.Config, error)",
		"gateway.RouteConfigsFromOpenAPIURL",
		"type OpenAPIImportConfig struct",
		"type OpenAPIImportGroupConfig struct",
	} {
		if !strings.Contains(string(configData), want) {
			t.Fatalf("gateway config.go missing OpenAPI import profile %q:\n%s", want, configData)
		}
	}
	configTestData, err := os.ReadFile(filepath.Join(dir, "internal", "config", "config_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"TestGatewayConfigLoadsOpenAPIImportProfile",
		"TestGatewayConfigSkipsDisabledOpenAPIImportProfile",
		"httptest.NewServer",
		"GatewayConfig(context.Background())",
	} {
		if !strings.Contains(string(configTestData), want) {
			t.Fatalf("gateway config_test.go missing OpenAPI import smoke %q:\n%s", want, configTestData)
		}
	}
	jsonData, err := os.ReadFile(filepath.Join(dir, "etc", "edge.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"environment": "development"`,
		`"service": {"name": "edge"`,
		`"startupTimeout": 5000000000`,
		`"timeoutConfig": {"duration": 3000000000`,
		`"breakerConfig": {"openTimeout": 5000000000`,
		`"metrics": {"enabled": true}`,
		`"mq": {"enabled": true`,
		`"driver": "memory"`,
		`"transport": "mq"`,
		`"gateway": {`,
		`"timeout": 3000000000`,
		`"pathPrefix": "/api"`,
		`"pathPrefix": "/events"`,
		`"pathPrefix": "/ws"`,
		`"pathPrefix": "/bff"`,
		`"aggregation": {"enabled": true`,
		`"openapiImports": [`,
		`"enabled": false`,
		`"url": "http://127.0.0.1:8081/openapi.json"`,
		`"gatewayPrefix": "/contract"`,
		`"matchTags": ["orders"]`,
		`"retry": {"attempts": 2, "backoff": 100000000, "statuses": [502, 503, 504], "methods": ["GET", "HEAD"]}`,
		`"rateLimit": {"rate": 100, "burst": 100}`,
		`"concurrency": {"limit": 64}`,
		`"transport": "gateway"`,
	} {
		if !strings.Contains(string(jsonData), want) {
			t.Fatalf("gateway config missing %q:\n%s", want, jsonData)
		}
	}
	assertEmbeddedGovernanceConfigLoads(t, jsonData)
	brokerData, err := os.ReadFile(filepath.Join(dir, "internal", "mq", "broker.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"case \"kafka\":", "case \"rabbitmq\":", "case \"redisstream\":"} {
		if !strings.Contains(string(brokerData), want) {
			t.Fatalf("gateway broker.go missing %q:\n%s", want, brokerData)
		}
	}
	svcData, err := os.ReadFile(filepath.Join(dir, "internal", "svc", "service.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(svcData), `"github.com/imajinyun/gofly/core/mq"`) || !strings.Contains(string(svcData), "MQ     mq.Broker") {
		t.Fatalf("gateway service context missing mq broker wiring:\n%s", svcData)
	}
	assertGeneratedProjectCompiles(t, dir)
}

func TestGenerateGatewayDefaultResilienceProfileReachesRuntime(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateGateway(GatewayOptions{Name: "edge", Module: "example.com/edge", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	configData, err := os.ReadFile(filepath.Join(dir, "etc", "edge.json"))
	if err != nil {
		t.Fatal(err)
	}
	var generated struct {
		Gateway        gateway.Config `json:"gateway"`
		OpenAPIImports []struct {
			Enabled bool `json:"enabled"`
		} `json:"openapiImports"`
	}
	if err := json.Unmarshal(configData, &generated); err != nil {
		t.Fatalf("decode generated gateway config: %v\n%s", err, configData)
	}
	if len(generated.OpenAPIImports) != 1 || generated.OpenAPIImports[0].Enabled {
		t.Fatalf("generated openapi import profile = %#v, want one disabled profile", generated.OpenAPIImports)
	}
	var apiCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orders":
			if apiCalls.Add(1) == 1 {
				http.Error(w, "retry", http.StatusBadGateway)
				return
			}
			_, _ = fmt.Fprint(w, "ok")
		case "/events/live":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: ready\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		case "/ws/chat":
			ctx := &rest.Context{Response: w, Request: r}
			_ = ctx.WebSocket(func(_ context.Context, conn *rest.WebSocketConn) {
				messageType, payload, err := conn.ReadMessage()
				if err != nil {
					t.Errorf("upstream read generated websocket: %v", err)
					return
				}
				if err := conn.WriteMessage(messageType, append([]byte("echo:"), payload...)); err != nil {
					t.Errorf("upstream write generated websocket: %v", err)
				}
			})
		case "/profile":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"u1"}`)
		case "/orders-summary":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"id":"o1"}]`)
		default:
			http.Error(w, "retry", http.StatusBadGateway)
		}
	}))
	t.Cleanup(upstream.Close)
	if len(generated.Gateway.Routes) != 4 {
		t.Fatalf("generated gateway routes = %#v, want four default routes", generated.Gateway.Routes)
	}
	for i := range generated.Gateway.Routes {
		generated.Gateway.Routes[i].Targets = []string{upstream.URL}
	}
	bffRoute := generatedGatewayRouteByName(t, generated.Gateway.Routes, "bff-home")
	bffRoute.Aggregation.Steps[1].Path = "/orders-summary"
	for i := range generated.Gateway.Routes {
		if generated.Gateway.Routes[i].Name == "bff-home" {
			generated.Gateway.Routes[i] = bffRoute
			break
		}
	}
	gw, err := gateway.NewFromConfig(generated.Gateway, nil)
	if err != nil {
		t.Fatalf("build gateway from generated config: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	before := gw.RuntimeSnapshot()
	if before.DefaultTimeout != 3*time.Second || before.RouteCount != 4 || len(before.Routes) != 4 {
		t.Fatalf("gateway runtime = %+v, want generated default timeout and four routes", before)
	}
	route := generatedGatewayRuntimeRoute(t, before.Routes, "api-proxy")
	if route.Timeout != 5*time.Second || route.EffectiveTimeout != 5*time.Second {
		t.Fatalf("generated route timeout = %+v, want explicit gateway route timeout", route)
	}
	if route.Retry.Attempts != 2 || route.Retry.Backoff != 100*time.Millisecond {
		t.Fatalf("generated route retry = %+v, want shared default retry profile", route.Retry)
	}
	if !route.Breaker.Enabled || route.Breaker.OpenTimeout != 5*time.Second || route.RateLimit.Rate != 100 || route.Concurrency.Limit != 64 {
		t.Fatalf("generated route resilience = breaker %+v rate %+v concurrency %+v", route.Breaker, route.RateLimit, route.Concurrency)
	}

	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orders", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" || apiCalls.Load() != 2 {
		t.Fatalf("generated gateway response = %d body = %q calls = %d, want retry success", rec.Code, rec.Body.String(), apiCalls.Load())
	}
	events := httptest.NewRecorder()
	gw.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/events/live", nil))
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), "data: ready") {
		t.Fatalf("generated gateway sse response = %d body = %q", events.Code, events.Body.String())
	}
	gatewayServer := httptest.NewServer(gw)
	t.Cleanup(gatewayServer.Close)
	conn, rw := dialGeneratedGatewayWebSocket(t, gatewayServer.URL, "/ws/chat")
	defer conn.Close()
	writeGeneratedGatewayClientFrame(t, rw, 1, []byte("hello"))
	messageType, payload := readGeneratedGatewayServerFrame(t, rw)
	if messageType != 1 || string(payload) != "echo:hello" {
		t.Fatalf("generated gateway websocket frame type=%d payload=%q", messageType, payload)
	}
	bff := httptest.NewRecorder()
	gw.ServeHTTP(bff, httptest.NewRequest(http.MethodGet, "/bff/home", nil))
	if bff.Code != http.StatusOK || !strings.Contains(bff.Body.String(), `"profile":{"id":"u1"}`) || !strings.Contains(bff.Body.String(), `"orders":[{"id":"o1"}]`) {
		t.Fatalf("generated gateway bff response = %d body = %q", bff.Code, bff.Body.String())
	}
	after := gw.RuntimeSnapshot()
	if after.Cache.RateLimiters != 1 || after.Cache.ConcurrencyLimiters != 1 || after.Cache.Breakers != 4 {
		t.Fatalf("generated gateway runtime cache = %+v, want materialized web adaptive primitives", after.Cache)
	}
}

func generatedGatewayRouteByName(t *testing.T, routes []gateway.RouteConfig, name string) gateway.RouteConfig {
	t.Helper()
	for _, route := range routes {
		if route.Name == name {
			return route
		}
	}
	t.Fatalf("generated gateway route %q not found in %#v", name, routes)
	return gateway.RouteConfig{}
}

func generatedGatewayRuntimeRoute(t *testing.T, routes []gateway.RouteRuntimeSnapshot, name string) gateway.RouteRuntimeSnapshot {
	t.Helper()
	for _, route := range routes {
		if route.Name == name {
			return route
		}
	}
	t.Fatalf("generated gateway runtime route %q not found in %#v", name, routes)
	return gateway.RouteRuntimeSnapshot{}
}

func dialGeneratedGatewayWebSocket(t *testing.T, serverURL, path string) (net.Conn, *bufio.ReadWriter) {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	key := base64.StdEncoding.EncodeToString([]byte("gofly-generated-gateway-ws"))
	for _, line := range []string{
		"GET " + path + " HTTP/1.1\r\n",
		"Host: " + u.Host + "\r\n",
		"Upgrade: websocket\r\n",
		"Connection: Upgrade\r\n",
		"Sec-WebSocket-Version: 13\r\n",
		"Sec-WebSocket-Key: " + key + "\r\n\r\n",
	} {
		if _, err := rw.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}
	status, err := rw.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("handshake status = %q, want switching protocols", status)
	}
	wantAccept := generatedGatewayWebSocketAccept(key)
	foundAccept := false
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
		if strings.EqualFold(strings.TrimSpace(line), "Sec-WebSocket-Accept: "+wantAccept) {
			foundAccept = true
		}
	}
	if !foundAccept {
		t.Fatal("missing websocket accept header")
	}
	return conn, rw
}

func writeGeneratedGatewayClientFrame(t *testing.T, rw *bufio.ReadWriter, messageType int, payload []byte) {
	t.Helper()
	if err := rw.WriteByte(0x80 | byte(messageType)); err != nil {
		t.Fatal(err)
	}
	mask := [4]byte{1, 2, 3, 4}
	length := len(payload)
	switch {
	case length < 126:
		if err := rw.WriteByte(0x80 | byte(length)); err != nil {
			t.Fatal(err)
		}
	case length <= 65535:
		if err := rw.WriteByte(0x80 | 126); err != nil {
			t.Fatal(err)
		}
		var buf [2]byte
		binary.BigEndian.PutUint16(buf[:], uint16(length))
		if _, err := rw.Write(buf[:]); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("test frame too large")
	}
	if _, err := rw.Write(mask[:]); err != nil {
		t.Fatal(err)
	}
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= mask[i%4]
	}
	if _, err := rw.Write(masked); err != nil {
		t.Fatal(err)
	}
	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}
}

func readGeneratedGatewayServerFrame(t *testing.T, rw *bufio.ReadWriter) (int, []byte) {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(rw, header); err != nil {
		t.Fatal(err)
	}
	messageType := int(header[0] & 0x0f)
	length := int(header[1] & 0x7f)
	if length == 126 {
		var buf [2]byte
		if _, err := io.ReadFull(rw, buf[:]); err != nil {
			t.Fatal(err)
		}
		length = int(binary.BigEndian.Uint16(buf[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(rw, payload); err != nil {
		t.Fatal(err)
	}
	return messageType, payload
}

func generatedGatewayWebSocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}
