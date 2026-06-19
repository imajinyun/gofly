# 🚀 快速开始示例

本文用于承载根 README 中不宜展开的大段示例代码，让根 README 保持简洁。

English version: [quickstart.md](quickstart.md)

---

## 1. 安装 CLI

```sh
go install github.com/gofly/gofly/cmd/gofly@latest
gofly version
```

---

## 2. 生成并运行服务

```sh
gofly quickstart hello --module github.com/me/hello --dir hello
cd hello
go run .
```

调用生成后的服务：

```sh
curl localhost:8080/healthz
curl localhost:8080/users/42
curl -XPOST localhost:8080/users -d '{"name":"ada"}'
```

---

## 3. 最小 REST 服务

创建一个新模块，然后把示例复制到 `main.go`：

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

服务启动后常用地址：

- `http://localhost:8080/healthz` — 健康检查
- `http://localhost:8080/openapi.json` — OpenAPI 契约
- `http://localhost:8080/docs` — Swagger UI

运行方式：

```sh
go run .
```

---

## 4. 最小 RPC 服务

再创建一个新模块，然后把示例复制到 `main.go`：

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

运行方式：

```sh
go run .
```

服务监听在 `:8081`。

---

## 5. 可观测性说明

生产服务建议默认开启这些基础信号：

- 📊 指标：请求数、错误数、in-flight、延迟直方图
- 🧾 日志：包含 request id 和 trace id 的结构化请求日志
- 🔎 链路追踪：在 REST/RPC 边界传播 W3C `traceparent`
- ❤️ 健康检查：`/healthz`、`/readyz`、`/startupz`
- 🛠️ 诊断端点：admin 和 pprof 仅暴露在内网端口

相关包位于 `core/observability`。
