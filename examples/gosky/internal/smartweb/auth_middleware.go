package smartweb

import (
	"encoding/json"
	"net/http"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/rest"
)

// Numeric session codes understood by the unchanged SmartAdmin axios
// interceptor. Both force a client-side logout to the login page; they differ
// only in the displayed message (missing session vs expired/invalid session).
// The native rest envelope uses the string code "unauthenticated", which the
// SmartAdmin client does not recognize, so compatibility routes use this
// middleware instead of rest.WithAuth.
const (
	CodeNotAuthenticated = 30007
	CodeSessionExpired   = 30008
)

// authFailure is the 401 body shape consumed by smart-web's axios interceptor:
// a top-level numeric code plus the SmartAdmin message field.
type authFailure struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	OK   bool   `json:"ok"`
}

// AuthMiddleware authenticates SmartAdmin compatibility routes with a realm
// interceptor and emits the legacy numeric 401 envelope. Authenticated
// requests carry the same principal as the native admin boundary, so
// handlers and capability checks are unaware which middleware ran.
func AuthMiddleware(interceptor authn.Interceptor) rest.Middleware {
	validator := interceptor.Validator()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := auth.ExtractBearer(r.Header.Get(auth.AuthorizationHeader))
			if !ok {
				writeAuthFailure(w, CodeNotAuthenticated, "not authenticated")
				return
			}
			ctx, err := validator(r.Context(), token)
			if err != nil {
				writeAuthFailure(w, CodeSessionExpired, "session expired")
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAuthFailure(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(authFailure{Code: code, Msg: msg, OK: false})
}
