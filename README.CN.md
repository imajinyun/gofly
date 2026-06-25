# 🪽 gofly

[English](README.md) | **简体中文**

`gofly` 是一个 Go 微服务工具链：它将类似 `goctl` 的代码生成 CLI 与生产可用的运行时能力组合在一起，覆盖 REST、RPC、网关、可观测性、韧性治理和服务治理。

- 📦 **模块:** `github.com/imajinyun/gofly`
- 🧭 **Go:** 1.26+
- 🚀 **CLI:** `gofly`

---

## ✨ 为什么选择 gofly？

- 🛠️ **快速生成**：生成 REST/RPC 服务、handler、gateway、model、Dockerfile 和 Kubernetes 清单。
- 🌐 **服务运行时**：开箱提供 REST、RPC、gateway、cache、MQ、config、discovery 和应用生命周期能力。
- 📊 **默认可观测**：内置日志、指标、链路追踪、健康检查、pprof 和 admin 端点。
- 🛡️ **安全上线**：内置限流、熔断、重试、认证辅助、安全中间件和治理规则。
- ✅ **质量门禁**：集成 lint、vet、race 测试、覆盖率门禁、API 兼容性检查、安全扫描和发布检查。

---

## ⚡ 快速开始

```sh
go install github.com/imajinyun/gofly/cmd/gofly@latest

gofly quickstart hello --module github.com/me/hello --dir hello
cd hello && go run .
```

CLI 安装和库导入均使用 `github.com/imajinyun/gofly`。

📖 需要完整示例和大段代码？请阅读 [快速开始示例](docs/doc/quickstart.CN.md)。

---

## 🧰 常用命令

| 命令 | 说明 |
| --- | --- |
| `gofly quickstart <name> --module <m>` | 一步生成并运行服务骨架 |
| `gofly new api\|rpc <name> --module <m>` | 创建 REST 或 RPC 项目 |
| `gofly api gen --file <s.api> --dir <d>` | 从 `.api` IDL 生成 REST 代码 |
| `gofly rpc gen --file <s.proto> --out <d>` | 从 `.proto` 文件生成 RPC 代码 |
| `gofly gen model --ddl <schema.sql> --dir <d>` | 从 DDL 生成数据模型 |
| `gofly api diff\|breaking` | 对比 API 契约并检测破坏性变更 |
| `gofly rpc check\|breaking` | 校验 RPC 契约并检测破坏性变更 |
| `gofly release check --strict` | 执行发布前检查 |
| `gofly env\|doctor\|version\|completion` | 诊断与开发工具 |

运行 `gofly help` 查看完整命令。

---

## 📚 文档

| 主题 | 链接 |
| --- | --- |
| 🚀 示例和完整代码 | [docs/doc/quickstart.CN.md](docs/doc/quickstart.CN.md) |
| 🌐 REST / RPC 示例 | [docs/doc/quickstart.CN.md](docs/doc/quickstart.CN.md) |
| 📊 可观测性说明 | [docs/doc/quickstart.CN.md](docs/doc/quickstart.CN.md) |
| 🧪 本地开发 | [开发](#-开发) |

---

## 🗂️ 目录结构

```text
cmd/gofly/        # CLI 命令和代码生成器
app/              # 应用生命周期 runner
rest/             # REST 服务、中间件、OpenAPI、健康检查
rpc/              # RPC 服务/客户端、服务发现、负载均衡、流式调用
gateway/          # API 网关运行时
cache/            # 本地缓存和分层缓存
ops/admin/        # 共享 admin/control-plane HTTP 基础能力
core/             # 可复用运行时基础能力
  observability/  # 日志、指标、追踪、性能分析
  governance/     # 运行时规则和诊断
  config/         # 本地和远程配置源
  discovery/      # 服务发现适配器
  mq/             # Kafka、RabbitMQ、Redis Stream 抽象
  kv/             # KV 抽象和 Redis 后端
```

经验法则：顶层包面向业务服务直接使用；`core/` 放可复用的底层能力。

---

## 🧪 开发

```sh
make build          # 构建 CLI
make test           # 运行测试
make lint           # 运行 golangci-lint
make cover-check    # 覆盖率阈值和 ratchet 检查
make governance     # 仓库治理检查
gofly release check --strict
```

完整无缓存治理流程：

```sh
make governance-10-rounds
```

---

## 📄 License

gofly 基于 [MIT License](./LICENSE) 发布。

文档、测试或兼容适配中提到的 go-zero、Kitex 等第三方框架名称，仅用于生态兼容和迁移语境。gofly 不包含、不依赖这些项目的源码，也不代表获得其背书或关联。
