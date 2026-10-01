package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/rpc"
)

const defaultAddr = "127.0.0.1:9090"

type Server struct {
	addr                 string
	pathPrefix           string
	rpcServer            *rpc.HTTPServer
	controlPlaneSnapshot func(context.Context) (controlplane.Snapshot, error)
	server               *http.Server
}

type Option func(*Server)

func WithControlPlaneSnapshot(snapshot func(context.Context) (controlplane.Snapshot, error)) Option {
	return func(s *Server) {
		s.controlPlaneSnapshot = snapshot
	}
}

func NewServer(addr string, pathPrefix string, rpcServer *rpc.HTTPServer, opts ...Option) *Server {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = defaultAddr
	}
	pathPrefix = strings.TrimRight(strings.TrimSpace(pathPrefix), "/")
	s := &Server{addr: addr, pathPrefix: pathPrefix, rpcServer: rpcServer}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

func (s *Server) Start() error {
	s.server = &http.Server{Addr: s.addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.mount(mux, "")
	if s.pathPrefix != "" {
		s.mount(mux, s.pathPrefix)
	}
	return mux
}

func (s *Server) mount(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix+"/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc(prefix+"/metrics", s.serveMetrics)
	mux.HandleFunc(prefix+"/control-plane", s.serveControlPlane)
	mux.HandleFunc(prefix+"/debug/pprof/", pprof.Index)
	mux.HandleFunc(prefix+"/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc(prefix+"/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc(prefix+"/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc(prefix+"/debug/pprof/trace", pprof.Trace)
	mux.Handle(prefix+"/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle(prefix+"/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle(prefix+"/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	mux.Handle(prefix+"/debug/pprof/block", pprof.Handler("block"))
	mux.Handle(prefix+"/debug/pprof/mutex", pprof.Handler("mutex"))
	mux.HandleFunc(prefix+"/rpc/admin/", s.serveRPCAdmin(prefix))
}

func (s *Server) serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_ = metrics.Default.WritePrometheus(w)
}

func (s *Server) serveControlPlane(w http.ResponseWriter, r *http.Request) {
	if s.controlPlaneSnapshot == nil {
		http.Error(w, "control-plane snapshot is not configured", http.StatusServiceUnavailable)
		return
	}
	snapshot, err := s.controlPlaneSnapshot(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (s *Server) serveRPCAdmin(prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.rpcServer == nil {
			http.Error(w, "rpc server is not configured", http.StatusServiceUnavailable)
			return
		}
		if prefix != "" {
			r = r.Clone(r.Context())
			r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		}
		s.rpcServer.ServeHTTP(w, r)
	}
}
