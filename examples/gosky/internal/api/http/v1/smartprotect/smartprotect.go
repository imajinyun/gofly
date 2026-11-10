// Package smartprotect exposes the unmodified SmartAdmin login-security
// contracts: login log pages, consecutive-fail lock state, and the level-3
// protection configuration console.
package smartprotect

import (
	"net"
	"net/http"
	"strings"

	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

const maxClientIPLength = 45

// MetaFromRequest extracts bounded IP/UA metadata for login audit rows.
func MetaFromRequest(r *http.Request, requestID string) smartweb.LoginTransportMeta {
	return smartweb.LoginTransportMeta{
		IP:        ClientIP(r),
		UserAgent: truncate(strings.TrimSpace(r.UserAgent()), 512),
		Device:    "PC",
		RequestID: requestID,
	}
}

// ClientIP honors a single-hop reverse proxy and falls back to the remote
// address. Bounded to the VARCHAR(45) login_ip column.
func ClientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		if ip := strings.TrimSpace(forwarded); ip != "" {
			return truncate(ip, maxClientIPLength)
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return truncate(r.RemoteAddr, maxClientIPLength)
	}
	return truncate(host, maxClientIPLength)
}

func truncate(value string, max int) string {
	if len(value) > max {
		return value[:max]
	}
	return value
}

func serviceOf(svcCtx *svc.ServiceContext) *app.LoginProtectionService {
	return svcCtx.CurrentLoginProtectionService()
}

func unavailable(ctx *rest.Context) {
	ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
}

func principal(ctx *rest.Context) (authn.RequestPrincipal, bool) {
	principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
	return principal, ok
}
