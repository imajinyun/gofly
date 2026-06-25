package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- WebSocket handshake tests must use RFC 6455 SHA-1 accept calculation.
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	middleware "github.com/imajinyun/gofly/examples/middlewares"
	"github.com/imajinyun/gofly/rest"
)

func TestMiddlewareDemoJWT(t *testing.T) {
	srv := newMiddlewareDemoServer()

	missing := httptest.NewRecorder()
	srv.Handler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/jwt", nil))
	if missing.Code != http.StatusUnauthorized || !strings.Contains(missing.Body.String(), string(coreerrors.CodeUnauthenticated)) {
		t.Fatalf("GET /jwt without token = %d %s", missing.Code, missing.Body.String())
	}

	tokenResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tokenResp, httptest.NewRequest(http.MethodGet, "/jwt/token", nil))
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("GET /jwt/token = %d %s", tokenResp.Code, tokenResp.Body.String())
	}
	var tokenBody map[string]string
	if err := json.Unmarshal(tokenResp.Body.Bytes(), &tokenBody); err != nil {
		t.Fatalf("decode token response: %v", err)
	}

	protected := httptest.NewRecorder()
	protectedReq := httptest.NewRequest(http.MethodGet, "/jwt", nil)
	protectedReq.Header.Set(auth.AuthorizationHeader, auth.BearerValue(tokenBody["token"]))
	srv.Handler().ServeHTTP(protected, protectedReq)
	if protected.Code != http.StatusOK || !strings.Contains(protected.Body.String(), `"subject":"demo-user"`) {
		t.Fatalf("GET /jwt = %d %s", protected.Code, protected.Body.String())
	}
}

func TestMiddlewareDemoAuthMatrix(t *testing.T) {
	srv := newMiddlewareDemoServer()

	missingAPIKey := httptest.NewRecorder()
	srv.Handler().ServeHTTP(missingAPIKey, httptest.NewRequest(http.MethodGet, "/apikey", nil))
	if missingAPIKey.Code != http.StatusUnauthorized {
		t.Fatalf("GET /apikey without key = %d %s", missingAPIKey.Code, missingAPIKey.Body.String())
	}

	acceptedAPIKey := httptest.NewRecorder()
	apiKeyReq := httptest.NewRequest(http.MethodGet, "/apikey", nil)
	apiKeyReq.Header.Set("X-API-Key", "demo-api-key")
	srv.Handler().ServeHTTP(acceptedAPIKey, apiKeyReq)
	if acceptedAPIKey.Code != http.StatusOK || !strings.Contains(acceptedAPIKey.Body.String(), `"subject":"api-client"`) {
		t.Fatalf("GET /apikey = %d %s", acceptedAPIKey.Code, acceptedAPIKey.Body.String())
	}

	missingBasic := httptest.NewRecorder()
	srv.Handler().ServeHTTP(missingBasic, httptest.NewRequest(http.MethodGet, "/basic", nil))
	if missingBasic.Code != http.StatusUnauthorized || missingBasic.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("GET /basic without credentials = %d challenge=%q", missingBasic.Code, missingBasic.Header().Get("WWW-Authenticate"))
	}

	acceptedBasic := httptest.NewRecorder()
	basicReq := httptest.NewRequest(http.MethodGet, "/basic", nil)
	basicReq.SetBasicAuth("demo", "password")
	srv.Handler().ServeHTTP(acceptedBasic, basicReq)
	if acceptedBasic.Code != http.StatusOK || !strings.Contains(acceptedBasic.Body.String(), `"subject":"basic-user"`) {
		t.Fatalf("GET /basic = %d %s", acceptedBasic.Code, acceptedBasic.Body.String())
	}

	rbac := httptest.NewRecorder()
	rbacReq := httptest.NewRequest(http.MethodGet, "/rbac", nil)
	rbacReq.Header.Set("X-API-Key", "demo-api-key")
	srv.Handler().ServeHTTP(rbac, rbacReq)
	if rbac.Code != http.StatusOK || !strings.Contains(rbac.Body.String(), `"rbac":"ok"`) {
		t.Fatalf("GET /rbac = %d %s", rbac.Code, rbac.Body.String())
	}
}

func TestMiddlewareDemoCORS(t *testing.T) {
	srv := newMiddlewareDemoServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/cors", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("OPTIONS /cors = %d origin=%q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestMiddlewareDemoCSRF(t *testing.T) {
	srv := newMiddlewareDemoServer()

	tokenResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tokenResp, httptest.NewRequest(http.MethodGet, "/csrf/token", nil))
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("GET /csrf/token = %d %s", tokenResp.Code, tokenResp.Body.String())
	}
	csrfCookie := cookieNamed(t, tokenResp.Result().Cookies(), "middleware_demo_csrf")

	missing := httptest.NewRecorder()
	srv.Handler().ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/csrf", nil))
	if missing.Code != http.StatusForbidden || !strings.Contains(missing.Body.String(), string(coreerrors.CodePermissionDenied)) {
		t.Fatalf("POST /csrf without token = %d %s", missing.Code, missing.Body.String())
	}

	accepted := httptest.NewRecorder()
	acceptedReq := httptest.NewRequest(http.MethodPost, "/csrf", nil)
	acceptedReq.AddCookie(csrfCookie)
	acceptedReq.Header.Set("X-CSRF-Token", csrfCookie.Value)
	srv.Handler().ServeHTTP(accepted, acceptedReq)
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), `"csrf":"ok"`) {
		t.Fatalf("POST /csrf = %d %s", accepted.Code, accepted.Body.String())
	}
}

func TestMiddlewareDemoWebSecurity(t *testing.T) {
	srv := newMiddlewareDemoServer()

	securityHeaders := httptest.NewRecorder()
	srv.Handler().ServeHTTP(securityHeaders, httptest.NewRequest(http.MethodGet, "/security-headers", nil))
	if securityHeaders.Code != http.StatusOK || securityHeaders.Header().Get("X-Frame-Options") != "DENY" || securityHeaders.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("GET /security-headers = %d frame=%q content-type=%q", securityHeaders.Code, securityHeaders.Header().Get("X-Frame-Options"), securityHeaders.Header().Get("X-Content-Type-Options"))
	}

	tooLarge := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tooLarge, httptest.NewRequest(http.MethodPost, "/max-body", strings.NewReader("this body is too large")))
	if tooLarge.Code != http.StatusRequestEntityTooLarge || !strings.Contains(tooLarge.Body.String(), string(coreerrors.CodeResourceExhausted)) {
		t.Fatalf("POST /max-body too large = %d %s", tooLarge.Code, tooLarge.Body.String())
	}

	timedOut := httptest.NewRecorder()
	srv.Handler().ServeHTTP(timedOut, httptest.NewRequest(http.MethodGet, "/timeout", nil))
	if timedOut.Code != http.StatusGatewayTimeout || !strings.Contains(timedOut.Body.String(), string(coreerrors.CodeDeadlineExceeded)) {
		t.Fatalf("GET /timeout = %d %s", timedOut.Code, timedOut.Body.String())
	}
}

func TestMiddlewareDemoSession(t *testing.T) {
	srv := newMiddlewareDemoServer()

	first := httptest.NewRecorder()
	srv.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/session", nil))
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"session_id":"demo-session"`) {
		t.Fatalf("GET /session first = %d %s", first.Code, first.Body.String())
	}
	cookie := cookieNamed(t, first.Result().Cookies(), "middleware_demo_session")
	if got, ok := middleware.VerifySession(cookie.Value, demoSessionSecret); !ok || got != "demo-session" {
		t.Fatalf("VerifySession() = %q %v", got, ok)
	}

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodGet, "/session", nil)
	secondReq.AddCookie(cookie)
	srv.Handler().ServeHTTP(second, secondReq)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"session_id":"demo-session"`) {
		t.Fatalf("GET /session second = %d %s", second.Code, second.Body.String())
	}
}

func TestMiddlewareDemoOpenTelemetry(t *testing.T) {
	srv := newMiddlewareDemoServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/otel", nil)
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Traceparent") == "" || !strings.Contains(rec.Body.String(), "4bf92f3577b34da6a3ce929d0e0e4736") {
		t.Fatalf("GET /otel = %d trace=%q body=%s", rec.Code, rec.Header().Get("Traceparent"), rec.Body.String())
	}
}

func TestMiddlewareDemoPrometheus(t *testing.T) {
	srv := newMiddlewareDemoServer()
	srv.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/prometheus", nil))

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "gofly_requests_total") {
		t.Fatalf("GET /metrics = %d %s", rec.Code, rec.Body.String())
	}
}

func TestMiddlewareDemoObservability(t *testing.T) {
	srv := newMiddlewareDemoServer()

	requestID := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/request-id", nil)
	req.Header.Set(rest.RequestIDHeader, "demo-request-id")
	srv.Handler().ServeHTTP(requestID, req)
	if requestID.Code != http.StatusOK || requestID.Header().Get(rest.RequestIDHeader) != "demo-request-id" || !strings.Contains(requestID.Body.String(), "demo-request-id") {
		t.Fatalf("GET /request-id = %d header=%q body=%s", requestID.Code, requestID.Header().Get(rest.RequestIDHeader), requestID.Body.String())
	}

	accessLog := httptest.NewRecorder()
	srv.Handler().ServeHTTP(accessLog, httptest.NewRequest(http.MethodGet, "/access-log", nil))
	if accessLog.Code != http.StatusOK || !strings.Contains(accessLog.Body.String(), `"access_log":"emitted"`) {
		t.Fatalf("GET /access-log = %d %s", accessLog.Code, accessLog.Body.String())
	}

	pprofUnauthorized := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pprofUnauthorized, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if pprofUnauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("GET /debug/pprof/ without API key = %d", pprofUnauthorized.Code)
	}

	pprofAccepted := httptest.NewRecorder()
	pprofReq := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	pprofReq.Header.Set("X-API-Key", "demo-api-key")
	srv.Handler().ServeHTTP(pprofAccepted, pprofReq)
	if pprofAccepted.Code != http.StatusOK || !strings.Contains(pprofAccepted.Body.String(), "profile") {
		t.Fatalf("GET /debug/pprof/ = %d %q", pprofAccepted.Code, pprofAccepted.Body.String())
	}
}

func TestMiddlewareDemoSSE(t *testing.T) {
	srv := newMiddlewareDemoServer()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sse", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(rec.Body.String(), "event: ready") {
		t.Fatalf("GET /sse = %d content-type=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestMiddlewareDemoWebSocket(t *testing.T) {
	srv := httptest.NewServer(newMiddlewareDemoServer().Handler())
	t.Cleanup(srv.Close)

	conn, rw := dialWebSocket(t, srv.URL, "/ws")
	t.Cleanup(func() { _ = conn.Close() })
	writeClientFrame(t, rw, rest.WebSocketTextMessage, []byte("hello"))
	messageType, payload := readServerFrame(t, rw)
	if messageType != rest.WebSocketTextMessage || string(payload) != "hello" {
		t.Fatalf("websocket echo type=%d payload=%q", messageType, string(payload))
	}
	writeClientFrame(t, rw, rest.WebSocketCloseMessage, nil)

	statsReq, err := http.NewRequest(http.MethodGet, srv.URL+"/ws/stats", nil)
	if err != nil {
		t.Fatalf("new stats request: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.DefaultClient.Do(statsReq.Clone(statsReq.Context()))
		if err != nil {
			t.Fatalf("GET /ws/stats: %v", err)
		}
		var stats rest.WebSocketStats
		decodeErr := json.NewDecoder(resp.Body).Decode(&stats)
		closeErr := resp.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode ws stats: %v", decodeErr)
		}
		if closeErr != nil {
			t.Fatalf("close ws stats body: %v", closeErr)
		}
		if stats.Accepted == 1 && stats.MessagesIn == 1 && stats.MessagesOut == 1 && stats.Active == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("websocket stats did not converge")
}

func TestMiddlewareDemoValidation(t *testing.T) {
	srv := newMiddlewareDemoServer()

	invalid := httptest.NewRecorder()
	srv.Handler().ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/validation", strings.NewReader(`{"name":"go","count":0}`)))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"fields"`) {
		t.Fatalf("POST /validation invalid = %d %s", invalid.Code, invalid.Body.String())
	}

	valid := httptest.NewRecorder()
	srv.Handler().ServeHTTP(valid, httptest.NewRequest(http.MethodPost, "/validation", strings.NewReader(`{"name":"gofly","count":2}`)))
	if valid.Code != http.StatusOK || !strings.Contains(valid.Body.String(), `"name":"gofly"`) {
		t.Fatalf("POST /validation valid = %d %s", valid.Code, valid.Body.String())
	}
}

func TestMiddlewareDemoStability(t *testing.T) {
	srv := newMiddlewareDemoServer()

	recovered := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recovered, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if recovered.Code != http.StatusInternalServerError || !strings.Contains(recovered.Body.String(), string(coreerrors.CodeInternal)) {
		t.Fatalf("GET /panic = %d %s", recovered.Code, recovered.Body.String())
	}

	firstRate := httptest.NewRecorder()
	srv.Handler().ServeHTTP(firstRate, httptest.NewRequest(http.MethodGet, "/rate-limit", nil))
	if firstRate.Code != http.StatusOK {
		t.Fatalf("first GET /rate-limit = %d %s", firstRate.Code, firstRate.Body.String())
	}
	secondRate := httptest.NewRecorder()
	srv.Handler().ServeHTTP(secondRate, httptest.NewRequest(http.MethodGet, "/rate-limit", nil))
	if secondRate.Code != http.StatusTooManyRequests {
		t.Fatalf("second GET /rate-limit = %d %s", secondRate.Code, secondRate.Body.String())
	}

	maxConcurrency := httptest.NewRecorder()
	srv.Handler().ServeHTTP(maxConcurrency, httptest.NewRequest(http.MethodGet, "/max-concurrency", nil))
	if maxConcurrency.Code != http.StatusOK {
		t.Fatalf("GET /max-concurrency = %d %s", maxConcurrency.Code, maxConcurrency.Body.String())
	}

	firstBreaker := httptest.NewRecorder()
	srv.Handler().ServeHTTP(firstBreaker, httptest.NewRequest(http.MethodGet, "/breaker", nil))
	if firstBreaker.Code != http.StatusInternalServerError {
		t.Fatalf("first GET /breaker = %d %s", firstBreaker.Code, firstBreaker.Body.String())
	}
	secondBreaker := httptest.NewRecorder()
	srv.Handler().ServeHTTP(secondBreaker, httptest.NewRequest(http.MethodGet, "/breaker", nil))
	if secondBreaker.Code != http.StatusServiceUnavailable {
		t.Fatalf("second GET /breaker = %d %s", secondBreaker.Code, secondBreaker.Body.String())
	}

	adaptiveLimit := httptest.NewRecorder()
	srv.Handler().ServeHTTP(adaptiveLimit, httptest.NewRequest(http.MethodGet, "/adaptive-limit", nil))
	if adaptiveLimit.Code != http.StatusOK {
		t.Fatalf("GET /adaptive-limit = %d %s", adaptiveLimit.Code, adaptiveLimit.Body.String())
	}
}

func TestMiddlewareDemoCatalogAndOpenAPI(t *testing.T) {
	srv := newMiddlewareDemoServer()

	catalogResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(catalogResp, httptest.NewRequest(http.MethodGet, "/middleware/catalog", nil))
	if catalogResp.Code != http.StatusOK {
		t.Fatalf("GET /middleware/catalog = %d %s", catalogResp.Code, catalogResp.Body.String())
	}
	var catalog []middleware.CatalogItem
	if err := json.Unmarshal(catalogResp.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(catalog) != 19 {
		t.Fatalf("catalog length = %d, want 19", len(catalog))
	}
	for _, item := range catalog {
		if item.Docs == "" || item.Example == "" || item.Test == "" || item.ControlPlane == "" || !item.OpenAPIExpose {
			t.Fatalf("catalog item missing productization metadata: %#v", item)
		}
	}

	openapi := httptest.NewRecorder()
	srv.Handler().ServeHTTP(openapi, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if openapi.Code != http.StatusOK || !strings.Contains(openapi.Body.String(), "/middleware/catalog") || !strings.Contains(openapi.Body.String(), "/apikey") {
		t.Fatalf("GET /openapi.json = %d %s", openapi.Code, openapi.Body.String())
	}
}

func cookieNamed(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing cookie %q in %#v", name, cookies)
	return nil
}

func dialWebSocket(t *testing.T, serverURL, path string) (net.Conn, *bufio.ReadWriter) {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		t.Fatalf("generate websocket key: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if _, err := rw.WriteString("GET " + path + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n\r\n"); err != nil {
		_ = conn.Close()
		t.Fatalf("write websocket handshake: %v", err)
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		t.Fatalf("flush websocket handshake: %v", err)
	}
	status, err := rw.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		t.Fatalf("read websocket handshake: %v", err)
	}
	if !strings.Contains(status, "101") {
		_ = conn.Close()
		t.Fatalf("websocket handshake status=%q", status)
	}
	wantAccept := websocketAccept(key)
	foundAccept := false
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			t.Fatalf("read websocket header: %v", err)
		}
		if line == "\r\n" {
			break
		}
		if strings.EqualFold(strings.TrimSpace(line), "Sec-WebSocket-Accept: "+wantAccept) {
			foundAccept = true
		}
	}
	if !foundAccept {
		_ = conn.Close()
		t.Fatalf("missing websocket accept header %q", wantAccept)
	}
	return conn, rw
}

func writeClientFrame(t *testing.T, rw *bufio.ReadWriter, messageType int, payload []byte) {
	t.Helper()
	if len(payload) > 125 {
		t.Fatalf("test helper supports payloads up to 125 bytes, got %d", len(payload))
	}
	mask := [4]byte{1, 2, 3, 4}
	if err := rw.WriteByte(0x80 | byte(messageType)); err != nil {
		t.Fatalf("write frame type: %v", err)
	}
	if err := rw.WriteByte(0x80 | byte(len(payload))); err != nil {
		t.Fatalf("write frame length: %v", err)
	}
	if _, err := rw.Write(mask[:]); err != nil {
		t.Fatalf("write frame mask: %v", err)
	}
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= mask[i%4]
	}
	if _, err := rw.Write(masked); err != nil {
		t.Fatalf("write frame payload: %v", err)
	}
	if err := rw.Flush(); err != nil {
		t.Fatalf("flush frame: %v", err)
	}
}

func readServerFrame(t *testing.T, rw *bufio.ReadWriter) (int, []byte) {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(rw, header); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	messageType := int(header[0] & 0x0f)
	length := int(header[1] & 0x7f)
	if length == 126 {
		var buf [2]byte
		if _, err := io.ReadFull(rw, buf[:]); err != nil {
			t.Fatalf("read frame uint16 length: %v", err)
		}
		length = int(binary.BigEndian.Uint16(buf[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(rw, payload); err != nil {
		t.Fatalf("read frame payload: %v", err)
	}
	return messageType, payload
}

func websocketAccept(key string) string {
	h := sha1.New() // #nosec G401 -- RFC 6455 requires SHA-1 for Sec-WebSocket-Accept.
	_, _ = h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
