// Command rpc-idl-matrix demonstrates gofly's copyable RPC IDL adoption matrix.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/observability/metrics"
	"github.com/gofly/gofly/core/retry"
	"github.com/gofly/gofly/rpc"
	"github.com/gofly/gofly/rpc/endpoint"
)

type helloRequest struct {
	Name string `json:"name,omitempty"`
}

type helloResponse struct {
	Message string `json:"message,omitempty"`
}

type chatMessage struct {
	From string `json:"from,omitempty"`
	Text string `json:"text,omitempty"`
}

type matrixReport struct {
	Schema       string              `json:"schema"`
	IDL          idlMatrix           `json:"idl"`
	Streams      []streamPath        `json:"streams"`
	Interceptors interceptorMatrix   `json:"interceptors"`
	Resolver     resolverMatrix      `json:"resolver"`
	Balancers    map[string][]string `json:"balancers"`
	Runtime      map[string]string   `json:"runtime"`
	Results      map[string]string   `json:"results"`
}

type idlMatrix struct {
	Proto     string   `json:"proto"`
	Thrift    string   `json:"thrift"`
	Methods   []string `json:"methods"`
	Contracts []string `json:"contracts"`
}

type streamPath struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

type interceptorMatrix struct {
	Unary  []string `json:"unary"`
	Stream []string `json:"stream"`
}

type resolverMatrix struct {
	Kind      string   `json:"kind"`
	Endpoints []string `json:"endpoints"`
	Updated   []string `json:"updated"`
}

func main() {
	report, err := runMatrix(context.Background())
	if err != nil {
		panic(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		panic(err)
	}
}

func runMatrix(ctx context.Context) (matrixReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	reg := metrics.NewRegistry()
	attempts := 0
	desc := matrixServiceDesc(&attempts)
	if err := desc.Validate(); err != nil {
		return matrixReport{}, fmt.Errorf("validate matrix service descriptor: %w", err)
	}

	srv := rpc.NewServer(
		rpc.WithServerMiddleware(endpoint.Chain(
			rpc.RecoverMiddleware(),
			rpc.RequestIDMiddleware(),
			rpc.TraceMiddleware("rpc-idl-matrix.server"),
			validationMiddleware(),
			rpc.TimeoutMiddleware(2*time.Second),
			rpc.MetricsMiddleware("rpc-idl-matrix.server", reg),
			rpc.LoggingMiddleware("rpc-idl-matrix.server"),
		)),
		rpc.WithServerStreamMiddleware(chainStream(
			rpc.StreamRecoverMiddleware(),
			rpc.StreamRequestIDMiddleware(),
			rpc.StreamTraceMiddleware("rpc-idl-matrix.server"),
			rpc.StreamMetricsMiddleware("rpc-idl-matrix.server", reg),
			rpc.StreamLoggingMiddleware("rpc-idl-matrix.server"),
		)),
	)
	if err := srv.RegisterService(desc, nil); err != nil {
		return matrixReport{}, fmt.Errorf("register matrix service: %w", err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	registry := rpc.NewRegistry()
	if err := registry.RegisterInstance(ctx, "matrix-greeter", rpc.ServiceInstance{Endpoint: ts.URL, Weight: 3, Version: "v1", Zone: "local-a", Status: "healthy"}); err != nil {
		return matrixReport{}, fmt.Errorf("register primary endpoint: %w", err)
	}
	if err := registry.RegisterInstance(ctx, "matrix-greeter", rpc.ServiceInstance{Endpoint: "http://127.0.0.1:65535", Weight: 1, Version: "v1", Zone: "local-b", Status: "standby"}); err != nil {
		return matrixReport{}, fmt.Errorf("register standby endpoint: %w", err)
	}
	resolver := registry.Resolver("matrix-greeter")
	endpoints, err := resolver.Resolve(ctx)
	if err != nil {
		return matrixReport{}, fmt.Errorf("resolve matrix endpoints: %w", err)
	}

	updated := append([]string(nil), endpoints...)
	if err := registry.DeregisterService(ctx, "matrix-greeter", "http://127.0.0.1:65535"); err != nil {
		return matrixReport{}, fmt.Errorf("deregister standby endpoint: %w", err)
	}
	updated, err = resolver.Resolve(ctx)
	if err != nil {
		return matrixReport{}, fmt.Errorf("resolve updated matrix endpoints: %w", err)
	}

	client, err := rpc.NewClient(ts.URL,
		rpc.WithResolver(rpc.NewStaticResolver(ts.URL)),
		rpc.WithBalancer(rpc.NewHealthBalancer(rpc.WithHealthFailureThreshold(1))),
		rpc.WithRetryPolicy(retry.Policy{
			Attempts:    2,
			Backoff:     time.Millisecond,
			ShouldRetry: func(err error) bool { return rpc.CodeOf(err) == rpc.CodeUnavailable },
		}),
		rpc.WithBreaker(breaker.New(breaker.WithFailureThreshold(3), breaker.WithOpenTimeout(time.Second))),
		rpc.WithRPCPolicy(rpc.RPCPolicy{
			Timeout: 2 * time.Second,
			Retry:   governance.RetryPolicy{Attempts: 2, Backoff: time.Millisecond},
			Methods: map[string]rpc.RPCPolicy{
				desc.MustMethodPath("SayHello"): {Timeout: time.Second},
			},
		}),
		rpc.WithClientMiddleware(endpoint.Chain(
			rpc.RequestIDMiddleware(),
			rpc.TraceMiddleware("rpc-idl-matrix.client"),
			rpc.TimeoutMiddleware(2*time.Second),
			rpc.MetricsMiddleware("rpc-idl-matrix.client", reg),
			rpc.LoggingMiddleware("rpc-idl-matrix.client"),
		)),
		rpc.WithClientStreamMiddleware(chainClientStream(
			rpc.ClientStreamRequestIDMiddleware(),
			rpc.ClientStreamTraceMiddleware("rpc-idl-matrix.client"),
			rpc.ClientStreamMetricsMiddleware("rpc-idl-matrix.client", reg),
			rpc.ClientStreamLoggingMiddleware("rpc-idl-matrix.client"),
			rpc.ClientStreamBreakerMiddleware(breaker.New()),
		)),
	)
	if err != nil {
		return matrixReport{}, fmt.Errorf("create matrix client: %w", err)
	}
	defer client.Close()

	results, err := exerciseMatrix(ctx, desc, client)
	if err != nil {
		return matrixReport{}, err
	}
	results["retryAttempts"] = fmt.Sprint(attempts)
	snapshot := reg.Snapshot()
	if snapshot.Requests == 0 {
		return matrixReport{}, errors.New("metrics middleware did not observe rpc calls")
	}

	balancers, err := balancerMatrix(ctx, endpoints)
	if err != nil {
		return matrixReport{}, err
	}

	return matrixReport{
		Schema: "gofly.rpc_idl_matrix.v1",
		IDL: idlMatrix{
			Proto:     "contracts/greeter.proto",
			Thrift:    "contracts/greeter.thrift",
			Methods:   []string{"SayHello", "WatchHello", "CollectHello", "Chat"},
			Contracts: []string{"proto compatibility", "thrift compatibility", "runtime descriptor validation"},
		},
		Streams: []streamPath{
			{Name: "SayHello", Mode: string(rpc.StreamModeUnary)},
			{Name: "WatchHello", Mode: string(rpc.StreamModeServerStream)},
			{Name: "CollectHello", Mode: string(rpc.StreamModeClientStream)},
			{Name: "Chat", Mode: string(rpc.StreamModeBidiStream)},
		},
		Interceptors: interceptorMatrix{
			Unary:  []string{"recovery", "trace", "logging", "timeout", "retry", "breaker", "validation", "metrics"},
			Stream: []string{"recovery", "trace", "logging", "timeout", "breaker", "metrics", "request_id"},
		},
		Resolver:  resolverMatrix{Kind: "registry", Endpoints: endpoints, Updated: updated},
		Balancers: balancers,
		Runtime: map[string]string{
			"service":  desc.Name,
			"version":  desc.Version,
			"requests": fmt.Sprint(snapshot.Requests),
		},
		Results: results,
	}, nil
}

func exerciseMatrix(ctx context.Context, desc rpc.ServiceDesc, client *rpc.HTTPClient) (map[string]string, error) {
	results := make(map[string]string)
	var unary helloResponse
	if err := client.Call(metadata.Append(ctx, metadata.RequestIDKey, "matrix-unary"), desc.MustMethodPath("SayHello"), helloRequest{Name: "gofly"}, &unary); err != nil {
		return nil, fmt.Errorf("call unary path: %w", err)
	}
	results["unary"] = unary.Message

	watch, err := client.Stream(metadata.Append(ctx, metadata.RequestIDKey, "matrix-watch"), desc.MustStreamPath("WatchHello"))
	if err != nil {
		return nil, fmt.Errorf("open server stream: %w", err)
	}
	defer watch.Close()
	if err := watch.Send(helloRequest{Name: "stream"}); err != nil {
		return nil, fmt.Errorf("send server stream request: %w", err)
	}
	var first helloResponse
	if err := watch.Recv(&first); err != nil {
		return nil, fmt.Errorf("recv server stream first response: %w", err)
	}
	var second helloResponse
	if err := watch.Recv(&second); err != nil {
		return nil, fmt.Errorf("recv server stream second response: %w", err)
	}
	results["serverStream"] = first.Message + "|" + second.Message

	collect, err := client.Stream(metadata.Append(ctx, metadata.RequestIDKey, "matrix-collect"), desc.MustStreamPath("CollectHello"))
	if err != nil {
		return nil, fmt.Errorf("open client stream: %w", err)
	}
	defer collect.Close()
	for _, name := range []string{"alice", "bob"} {
		if err := collect.Send(helloRequest{Name: name}); err != nil {
			return nil, fmt.Errorf("send client stream request: %w", err)
		}
	}
	var collected helloResponse
	if err := collect.Recv(&collected); err != nil {
		return nil, fmt.Errorf("recv client stream response: %w", err)
	}
	results["clientStream"] = collected.Message

	chat, err := client.Stream(metadata.Append(ctx, metadata.RequestIDKey, "matrix-chat"), desc.MustStreamPath("Chat"))
	if err != nil {
		return nil, fmt.Errorf("open bidi stream: %w", err)
	}
	defer chat.Close()
	if err := chat.Send(chatMessage{From: "client", Text: "ping"}); err != nil {
		return nil, fmt.Errorf("send bidi stream message: %w", err)
	}
	var reply chatMessage
	if err := chat.Recv(&reply); err != nil {
		return nil, fmt.Errorf("recv bidi stream message: %w", err)
	}
	results["bidiStream"] = reply.From + ":" + reply.Text
	return results, nil
}

func matrixServiceDesc(attempts *int) rpc.ServiceDesc {
	return rpc.ServiceDesc{
		Name:    "examples.rpcmatrix.v1.MatrixGreeter",
		Version: "v1",
		Metadata: map[string]string{
			"proto":  "contracts/greeter.proto",
			"thrift": "contracts/greeter.thrift",
		},
		Methods: []rpc.MethodDesc{{
			Name:       "SayHello",
			NewRequest: func() any { return new(helloRequest) },
			Request:    "HelloRequest",
			Response:   "HelloResponse",
			Codec:      "json",
			Metadata:   map[string]string{"idl": "proto+thrift", "stream": string(rpc.StreamModeUnary)},
			Handler: func(ctx context.Context, req any) (any, error) {
				if attempts != nil {
					(*attempts)++
					if *attempts == 1 {
						return nil, rpc.NewError(rpc.CodeUnavailable, "transient matrix dependency")
					}
				}
				typed, ok := req.(*helloRequest)
				if !ok {
					return nil, rpc.NewError(rpc.CodeInvalidArgument, "unexpected SayHello request")
				}
				return helloResponse{Message: "hello " + typed.Name}, nil
			},
		}},
		Streams: []rpc.StreamDesc{
			{
				Name:       "WatchHello",
				NewMessage: func() any { return new(helloRequest) },
				Message:    "HelloRequest|HelloResponse",
				Codec:      "json",
				Mode:       rpc.StreamModeServerStream,
				Metadata:   map[string]string{"idl": "proto", "stream": string(rpc.StreamModeServerStream)},
				Handler: func(ctx context.Context, stream *rpc.Stream) error {
					var req helloRequest
					if err := stream.Recv(&req); err != nil {
						return err
					}
					for _, suffix := range []string{"first", "second"} {
						if err := stream.Send(helloResponse{Message: req.Name + ":" + suffix}); err != nil {
							return err
						}
					}
					return nil
				},
			},
			{
				Name:       "CollectHello",
				NewMessage: func() any { return new(helloRequest) },
				Message:    "HelloRequest|HelloResponse",
				Codec:      "json",
				Mode:       rpc.StreamModeClientStream,
				Metadata:   map[string]string{"idl": "proto+thrift", "stream": string(rpc.StreamModeClientStream)},
				Handler: func(ctx context.Context, stream *rpc.Stream) error {
					names := make([]string, 0, 2)
					for len(names) < 2 {
						var req helloRequest
						if err := stream.Recv(&req); err != nil {
							if errors.Is(err, io.EOF) {
								break
							}
							return err
						}
						names = append(names, req.Name)
					}
					return stream.Send(helloResponse{Message: "collected " + strings.Join(names, ",")})
				},
			},
			{
				Name:       "Chat",
				NewMessage: func() any { return new(chatMessage) },
				Message:    "ChatMessage",
				Codec:      "json",
				Mode:       rpc.StreamModeBidiStream,
				Metadata:   map[string]string{"idl": "proto", "stream": string(rpc.StreamModeBidiStream)},
				Handler: func(ctx context.Context, stream *rpc.Stream) error {
					var msg chatMessage
					if err := stream.Recv(&msg); err != nil {
						return err
					}
					return stream.Send(chatMessage{From: "server", Text: "ack:" + msg.Text})
				},
			},
		},
	}
}

func validationMiddleware() endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if typed, ok := req.(*helloRequest); ok && strings.TrimSpace(typed.Name) == "" {
				return nil, rpc.NewError(rpc.CodeInvalidArgument, "name is required")
			}
			return next(ctx, req)
		}
	}
}

func balancerMatrix(ctx context.Context, endpoints []string) (map[string][]string, error) {
	normalized := append([]string(nil), endpoints...)
	sort.Strings(normalized)
	if len(normalized) == 0 {
		return nil, errors.New("balancer matrix endpoints are required")
	}
	weighted := rpc.NewWeightedRoundRobinBalancer(map[string]int{normalized[0]: 3})
	health := rpc.NewHealthBalancer(rpc.WithHealthFailureThreshold(1), rpc.WithHealthEjectionDuration(time.Minute))
	health.Report(ctx, "http://127.0.0.1:65535", rpc.NewError(rpc.CodeUnavailable, "unhealthy"))
	cases := map[string]rpc.Balancer{
		"round_robin":          &rpc.RoundRobinBalancer{},
		"weighted_round_robin": weighted,
		"p2c":                  rpc.NewP2CBalancer(),
		"consistent_hash":      rpc.NewConsistentHashBalancer(rpc.WithConsistentHashKey("tenant-a")),
		"health_aware":         health,
	}
	out := make(map[string][]string, len(cases))
	for name, balancer := range cases {
		picks := make([]string, 0, 4)
		for i := 0; i < 4; i++ {
			pick, err := balancer.Pick(ctx, normalized)
			if err != nil {
				return nil, fmt.Errorf("pick %s endpoint: %w", name, err)
			}
			picks = append(picks, pick)
			if reporter, ok := balancer.(rpc.EndpointReporter); ok {
				reporter.Report(ctx, pick, nil)
			}
		}
		out[name] = picks
	}
	return out, nil
}

func chainStream(mws ...rpc.StreamMiddleware) rpc.StreamMiddleware {
	return func(next rpc.StreamHandler) rpc.StreamHandler {
		for i := len(mws) - 1; i >= 0; i-- {
			if mws[i] != nil {
				next = mws[i](next)
			}
		}
		return next
	}
}

func chainClientStream(mws ...rpc.ClientStreamMiddleware) rpc.ClientStreamMiddleware {
	return func(next rpc.ClientStreamHandler) rpc.ClientStreamHandler {
		for i := len(mws) - 1; i >= 0; i-- {
			if mws[i] != nil {
				next = mws[i](next)
			}
		}
		return next
	}
}
