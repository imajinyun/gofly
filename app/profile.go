// Package app provides the gofly application runtime lifecycle management.
// It coordinates server startup, graceful shutdown, hooks, and production
// configuration defaults.
package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	core "github.com/imajinyun/gofly/core"
	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/security"
)

// ProfileConfig configures the optional pprof HTTP server.
//
// When Enabled is true, the runtime starts a separate HTTP server (default
// 127.0.0.1:6060) exposing standard pprof endpoints under PathPrefix
// (default "/debug/pprof"). Remote access requires AllowRemote + Token.
type ProfileConfig struct {
	// Enabled starts the profile server when true.
	Enabled bool `json:"enabled"`
	// Addr is the listen address. Default: "127.0.0.1:6060".
	Addr string `json:"addr,omitempty"`
	// PathPrefix is the URL prefix for pprof endpoints. Default: "/debug/pprof".
	PathPrefix string `json:"pathPrefix,omitempty"`
	// ReadHeaderTimeout is the per-connection header read timeout.
	// Default: 5s.
	ReadHeaderTimeout time.Duration `json:"readHeaderTimeout,omitempty"`
	// Token requires Bearer token authentication on all profile endpoints.
	// Required when AllowRemote is true or Addr binds 0.0.0.0.
	Token string `json:"token,omitempty"`
	// AllowRemote allows access from non-localhost clients. Requires Token.
	AllowRemote bool `json:"allowRemote,omitempty"`
}

type ProfileServer struct {
	conf     ProfileConfig
	server   *http.Server
	handlers []profileHandler
}

type ProfileOption func(*ProfileServer)

type profileHandler struct {
	path    string
	handler http.Handler
}

// WithProfileHandler mounts an additional handler on the profile server. The
// handler is protected by the same localhost/token checks as pprof endpoints.
func WithProfileHandler(path string, handler http.Handler) ProfileOption {
	return func(s *ProfileServer) {
		path = cleanProfileHandlerPath(path)
		if path == "" || handler == nil {
			return
		}
		s.handlers = append(s.handlers, profileHandler{path: path, handler: handler})
	}
}

func NewProfileServer(conf ProfileConfig, opts ...ProfileOption) *ProfileServer {
	addr := conf.Addr
	if addr == "" {
		addr = "127.0.0.1:6060"
	}
	readHeaderTimeout := conf.ReadHeaderTimeout
	if readHeaderTimeout <= 0 {
		readHeaderTimeout = 5 * time.Second
	}
	conf.Addr = addr
	conf.ReadHeaderTimeout = readHeaderTimeout
	s := &ProfileServer{conf: conf}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	s.server = &http.Server{Addr: addr, Handler: s.handler(), ReadHeaderTimeout: readHeaderTimeout}
	return s
}

func (s *ProfileServer) Start() error {
	if s == nil || !s.conf.Enabled {
		return nil
	}
	err := s.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *ProfileServer) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	ctx = core.Context(ctx)
	return s.server.Shutdown(ctx)
}

func (s *ProfileServer) Handler() http.Handler {
	if s == nil || s.server == nil || !s.conf.Enabled {
		return http.NewServeMux()
	}
	return s.server.Handler
}

func (s *ProfileServer) handler() http.Handler {
	mux := http.NewServeMux()
	prefix := cleanProfilePrefix(s.conf.PathPrefix)
	mux.HandleFunc(prefix+"/", s.secure(pprof.Index))
	mux.HandleFunc(prefix+"/cmdline", s.secure(pprof.Cmdline))
	mux.HandleFunc(prefix+"/profile", s.secure(pprof.Profile))
	mux.HandleFunc(prefix+"/symbol", s.secure(pprof.Symbol))
	mux.HandleFunc(prefix+"/trace", s.secure(pprof.Trace))
	for _, route := range s.handlers {
		mux.Handle(route.path, s.secureHandler(route.handler))
	}
	return mux
}

func (s *ProfileServer) secure(next http.HandlerFunc) http.HandlerFunc {
	return s.secureHandler(next).ServeHTTP
}

func (s *ProfileServer) secureHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.conf.AllowRemote && !security.IsLocalRemote(r.RemoteAddr) {
			http.Error(w, "pprof is only available from localhost", http.StatusForbidden)
			return
		}
		if s.conf.Token != "" {
			token, ok := auth.ExtractBearer(r.Header.Get(auth.AuthorizationHeader))
			if !ok || !security.ConstantTimeEqual(token, s.conf.Token) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if s.conf.AllowRemote && s.conf.Token == "" && !security.IsLocalRemote(r.RemoteAddr) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func cleanProfilePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return "/debug/pprof"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimRight(prefix, "/")
}

func cleanProfileHandlerPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	return path
}
