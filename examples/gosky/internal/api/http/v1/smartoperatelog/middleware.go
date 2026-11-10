package smartoperatelog

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartprotect"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

const (
	maxCapturedBody = 1 << 16 // 64 KiB per captured audit body
	insertTimeout   = 3 * time.Second
)

// Middleware records audited mutations best-effort after the response. It
// never changes the response, blocks business traffic, or fails the request;
// capture and insert errors are discarded like the Java async aspect.
func Middleware(svcCtx *svc.ServiceContext) rest.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			service := svcCtx.CurrentOperateLogService()
			if service == nil {
				next.ServeHTTP(w, r)
				return
			}
			_, _, audited := catalogLabel(r.URL.Path)
			if !audited {
				next.ServeHTTP(w, r)
				return
			}
			principal, hasPrincipal := authn.PrincipalFromContext(r.Context())

			// Capture a bounded copy of a JSON request body after restoring
			// the original stream for the handler. Audited routes are JSON
			// forms; multipart uploads are deliberately not audited.
			var requestJSON string
			if r.Body != nil && isJSONRequest(r) {
				if captured, err := captureAndRestore(r, maxCapturedBody); err == nil {
					requestJSON = captured
				}
			}

			recorder := &responseRecorder{ResponseWriter: w, limit: maxCapturedBody}
			next.ServeHTTP(recorder, r)

			if !hasPrincipal {
				return
			}
			entry := buildEntry(principal, r.URL.Path, r.Method, requestJSON,
				recorder.body.String(), recorder.status, smartprotect.ClientIP(r), r.UserAgent())
			// The request context is canceled after the response returns, so
			// the best-effort insert uses a detached context with a timeout.
			go func(entry app.OperateLogEntry) {
				ctx, cancel := context.WithTimeout(context.Background(), insertTimeout)
				defer cancel()
				_ = service.Record(ctx, entry)
			}(entry)
		})
	}
}

// buildEntry turns one completed request into the service write model.
func buildEntry(principal authn.RequestPrincipal, route, method, requestJSON, responseBody string,
	status int, ip, userAgent string) app.OperateLogEntry {
	module, content, _ := catalogLabel(route)
	success := status == 0 || status < http.StatusBadRequest
	entry := app.OperateLogEntry{
		TenantID:    principal.TenantID,
		Subject:     principal.Subject,
		Module:      module,
		Content:     content,
		URL:         route,
		Method:      method,
		Param:       requestJSON,
		Response:    responseBody,
		IP:          ip,
		UserAgent:   userAgent,
		SuccessFlag: success,
	}
	if !success {
		entry.FailReason = http.StatusText(status)
	}
	return entry
}

func isJSONRequest(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	if semi := strings.IndexByte(contentType, ';'); semi >= 0 {
		contentType = contentType[:semi]
	}
	return strings.EqualFold(strings.TrimSpace(contentType), "application/json")
}

func captureAndRestore(r *http.Request, limit int) (string, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > limit {
		body = body[:limit]
	}
	return string(body), nil
}

type responseRecorder struct {
	http.ResponseWriter
	body   bytes.Buffer
	status int
	limit  int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if r.body.Len() < r.limit {
		remaining := r.limit - r.body.Len()
		if remaining >= len(body) {
			r.body.Write(body)
		} else {
			r.body.Write(body[:remaining])
		}
	}
	return r.ResponseWriter.Write(body)
}
