// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"net/http"

	"github.com/imajinyun/gofly/core/breaker"
)

// gatewayRouteDispatcher dispatches proxy requests to HTTP or transcoded RPC.
type gatewayRouteDispatcher struct {
	gateway *Gateway
}

// proxy routes the request to the appropriate backend protocol.
func (d gatewayRouteDispatcher) proxy(
	r *http.Request,
	route Route,
	endpoint string,
	body []byte,
	brk *breaker.AdaptiveBreaker,
) (proxyResult, error) {
	if route.Transcode.Enabled {
		return d.gateway.transcodeOnce(r, route, endpoint, body, brk)
	}
	return d.gateway.proxyHTTPOnce(r, route, endpoint, body, brk)
}
