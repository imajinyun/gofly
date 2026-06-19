# 🚀 Quickstart Examples

This document keeps the root README short while preserving runnable examples and longer code snippets.

中文版本：[quickstart.CN.md](quickstart.CN.md)

---

## 1. Install the CLI

```sh
go install github.com/gofly/gofly/cmd/gofly@latest
gofly version
```

---

## 2. Generate and run a service

```sh
gofly quickstart hello --module github.com/me/hello --dir hello
cd hello
go run .
```

Try the generated service:

```sh
curl localhost:8080/healthz
curl localhost:8080/users/42
curl -XPOST localhost:8080/users -d '{"name":"ada"}'
```

---

## 3. Minimal REST server

Create a fresh module, then copy the example into `main.go`:

```sh
mkdir hello-rest && cd hello-rest
go mod init github.com/me/hello-rest
go get github.com/gofly/gofly@latest
```

```go
package main

import (
	"net/http"

	"github.com/gofly/gofly/rest"
)

func main() {
	srv := rest.MustNewServer(rest.Config{
		Name: "hello",
		Port: 8080,
		Middlewares: rest.MiddlewaresConfig{
			Recover: true,
			Log:       true,
			Metrics:   true,
			Health:    true,
			RequestID: true,
		},
	})

	srv.AddRoute(rest.Route{
		Method: http.MethodGet,
		Path:   "/users/{id}",
		Handler: func(c *rest.Context) {
			c.JSON(http.StatusOK, map[string]string{"id": c.PathValue("id")})
		},
	})

	srv.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "hello", Version: "1.0.0"})
	_ = srv.Start()
}
```

Useful URLs after startup:

- `http://localhost:8080/healthz` — health probe
- `http://localhost:8080/openapi.json` — OpenAPI contract
- `http://localhost:8080/docs` — Swagger UI

Run it with:

```sh
go run .
```

---

## 4. Minimal RPC server

Create another module, then copy the example into `main.go`:

```sh
mkdir hello-rpc && cd hello-rpc
go mod init github.com/me/hello-rpc
go get github.com/gofly/gofly@latest
```

```go
package main

import (
	"context"

	"github.com/gofly/gofly/app"
	"github.com/gofly/gofly/rpc"
)

type helloReq struct{ Name string }
type helloResp struct{ Message string }

type greeter struct{}

func (greeter) SayHello(ctx context.Context, req *helloReq) (*helloResp, error) {
	return &helloResp{Message: "hello " + req.Name}, nil
}

func main() {
	conf := app.DefaultServiceConf("greeter-rpc")
	server := rpc.NewServer(append([]rpc.ServerOption{
		rpc.WithAddress(":8081"),
	}, conf.RPCServerOptions()...)...)

	server.RegisterService(rpc.ServiceDesc{
		Name: "examples.greeter.Greeter",
		Methods: []rpc.MethodDesc{{
			Name:       "SayHello",
			NewRequest: func() any { return new(helloReq) },
			Handler: func(ctx context.Context, req any) (any, error) {
				return greeter{}.SayHello(ctx, req.(*helloReq))
			},
		}},
	}, nil)

	app.RunService(context.Background(), conf, server)
}
```

Run it with:

```sh
go run .
```

The server listens on `:8081`.

---

## 5. Observability notes

For production services, enable these baseline signals:

- 📊 metrics: request count, error count, in-flight requests, latency histograms
- 🧾 logs: structured request logs with request id and trace id
- 🔎 traces: W3C `traceparent` propagation across REST/RPC boundaries
- ❤️ health: `/healthz`, `/readyz`, and `/startupz`
- 🛠️ diagnostics: admin endpoints and pprof on internal-only ports

The related packages live under `core/observability`.
