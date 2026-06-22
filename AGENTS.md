# gofly Agent 工作流

本文件定义本项目的默认自动化协作方式。任何 AI Agent、自动化脚本或人工执行治理任务时，都应优先遵循这里的约束。

## 治理目标与优先级

gofly 是 Go 微服务工具链与代码生成项目，治理优先级按以下顺序执行：

1. **安全与可恢复性优先**：任何涉及路径、模板、插件、外部进程、网络下载、密钥、SQL/命令执行的改动，先保证不会扩大攻击面或破坏用户工程。
2. **生成物确定性优先**：代码生成、模板扩展、RPC/API/model 输出必须可重复、可格式化、可编译，并避免写出根目录之外。
3. **根模块卫生优先**：根模块只保留 gofly 自身真实导入的依赖；生成项目专用依赖只能进入被生成项目自己的 `go.mod`。
4. **质量门禁优先于功能扩展**：新增功能必须补齐测试、文档/帮助文本和治理门禁，不允许以“后续补测”为默认策略。
5. **最小变更与可追踪性**：优先修复根因；对历史基线暂不修复的 gosec/lint 项必须给出具体理由、范围和后续收敛建议。

## 治理变更控制

治理类改动包括 `AGENTS.md`、`Makefile`、`bin/scripts/`、`.golangci.yml`、CI workflow、release 配置、go.mod tool 指令和安全基线说明。处理这类改动时必须遵循：

- **脚本优先于文字**：文档描述必须能在 `Makefile` 或 `bin/scripts/` 中找到对应入口；若只修改文字，应说明当前是治理规范补充而非自动化落地。
- **单一事实源**：质量门禁命令以 `Makefile` 和 `bin/scripts/` 为准，`AGENTS.md` 只描述约束、分层策略和异常处理；发现不一致时优先修正脚本或明确脚本暂未覆盖。
- **兼容性保护**：新增阻塞门禁前先评估历史基线；已有历史问题应先以审计/报告模式运行，再分模块收敛，避免一次性阻断所有开发。
- **可回滚性**：治理脚本不得不可逆修改用户工程；执行 `go mod tidy`、生成物检查、临时 worktree 时必须能恢复或只在临时目录操作。
- **最小验证**：文档治理至少执行 shell 语法检查和相关 make 目标 dry-run；脚本治理至少执行目标脚本的最小真实路径。

## 风险分级与处置 SLA

| 等级 | 触发条件 | 默认处置 |
| --- | --- | --- |
| P0 阻断 | 破坏构建、测试全红、根模块依赖污染、路径逃逸、命令注入、凭据泄露、发布产物不可验证 | 立即修复；不得只写报告；修复后跑对应 L2/L3 门禁 |
| P1 高优先 | 新增 gosec 高置信告警、race、契约 breaking、生成物不可编译、coverage ratchet 回退 | 当前任务内修复或明确阻塞原因；补回归测试 |
| P2 中优先 | 历史 gosec/lint 基线、非关键覆盖缺口、文档/CLI help 不一致、可观测性缺口 | 建议纳入下一轮治理；记录模块、命令和影响 |
| P3 低优先 | 风格统一、注释清理、报告措辞、非阻塞脚本体验优化 | opportunistic 修复；不得挤占 P0/P1 |

任何降级都必须写明：原始风险、已有防护、降级理由、后续触发条件。

## 默认执行模式

- 使用中文汇报结论、风险和验证结果。
- 作为自治高级结对程序员执行任务：主动审计、规划、实现、测试、修复、复测和报告。
- 用户给出方向后，除非需求存在破坏性风险或权限缺失，否则不要在每一轮之间询问确认。
- 保持“基线 → 审计 → 修复 → 测试 → 验证 → 报告”的闭环。
- 修改 Go 代码时优先加载并遵循 Go 相关能力：`golang-how-to`、`golang-testing`、`golang-lint`、`golang-safety`；涉及 CLI、并发、安全、性能、数据库、gRPC、Swagger 等时叠加对应专项能力。
- 修改 `AGENTS.md`、`Makefile`、`bin/scripts/`、`.golangci.yml` 或 CI 配置时，按治理变更处理：先对齐现有脚本，再更新文档，最后至少执行相关脚本的最小可验证子集。
- 不要静默忽略外部变更；如果文件被用户、脚本或 linter 修改，保留其意图，并在后续编辑中避免回滚无关内容。

## 必需 Go 能力

执行 Go 相关任务时按场景叠加以下能力，避免只凭通用经验修改代码：

- **默认必用**：`golang-how-to`、`golang-testing`、`golang-lint`、`golang-safety`。
- **安全/外部输入**：涉及插件、模板、文件系统、网络、TLS、命令执行、SQL、鉴权时叠加 `golang-security`。
- **CLI/配置**：涉及 `cmd/gofly`、flag、completion、退出码、配置加载时叠加 `golang-cli`、`golang-spf13-cobra` 或 `golang-spf13-viper`。
- **RPC/API 契约**：涉及 proto/thrift/OpenAPI/Swagger/REST/gRPC 生成时叠加 `golang-grpc`、`golang-swagger`、`golang-error-handling`。
- **并发/生命周期**：涉及 goroutine、context、stream、watch、缓存刷新时叠加 `golang-concurrency`、`golang-context`。
- **性能治理**：只有在已有 benchmark/profile/生产指标指出瓶颈后才做优化；测量用 `golang-benchmark`，优化用 `golang-performance`。

## 缓存策略

治理和质量门禁默认禁用项目级缓存语义：

- 对需要验证缓存旁路或启动 gofly 运行时的步骤，设置 `GOFLY_CACHE_DISABLED=true`，确保 gofly 运行时缓存、分层缓存本地 L1 和远端 L2 旁路。
- 对普通缓存组件行为测试，不全局注入 `GOFLY_CACHE_DISABLED=true`，避免把“缓存应当生效”的单元测试变成旁路模式测试。
- Go 测试使用 `GOFLAGS=-count=1`，避免测试结果缓存；治理和 CI 测试默认追加 `-shuffle=on`，暴露测试顺序耦合。
- 使用临时 `GOCACHE` 和 `GOTMPDIR`，执行结束后删除，避免复用本机持久构建缓存。
- 默认复用 Go 模块下载缓存，避免全量依赖下载耗尽本机临时空间；需要强隔离模块缓存时显式设置 `GOVERNANCE_ISOLATE_GOMODCACHE=true`。
- 使用 `GOPROXY=direct`，避免依赖远端模块代理缓存。
- 远程插件下载不得复用 `$USER_CACHE_DIR/gofly/plugins`，必须使用一次性临时文件。
- 治理工具（`govulncheck`、`gosec`、`apidiff`）使用 Go `tool` 指令固定版本，CI 和本地脚本优先通过 `go tool <name>` 调用。
- `gorm.io/gorm` 等生成项目专用依赖只能写入被生成项目自己的 `go.mod`，不得作为根模块依赖；`bin/scripts/check-mod-tidy.sh` 会拒绝未被根模块实际导入的生成专用依赖。

## 依赖与模块治理

- 根模块 `go.mod` 的直接依赖必须满足“根模块实际导入”原则；临时代码生成、样例工程、测试 fixture 所需依赖不得留在根模块。
- 添加依赖前先确认是否已有标准库或现有依赖可满足需求；确需添加时说明用途、导入点、替代方案和安全影响。
- `go mod tidy` 只能作为检查/收敛步骤执行；若 tidy 删除生成专用依赖，应保留删除结果，不得反复重新加入根模块。
- 对生成项目依赖（例如 GORM 风格 model 输出）应由生成器写入目标工程的 `go.mod`，并用临时工程测试验证，而不是污染 gofly 根模块。
- `go.sum` 变化必须能由根模块依赖图解释；无法解释的间接依赖变化应回溯到触发命令或外部 fixture。
- Go 1.24+ 工具依赖使用 `go.mod` 的 `tool` 指令固定版本；不得新增 legacy `tools.go`，除非项目显式降级到 Go 1.24 之前。
- 依赖升级优先 patch/minor；涉及网络、序列化、数据库、认证、加密、代码生成或 CLI 行为的依赖升级必须阅读 release notes 并执行相关子系统测试。
- 如需临时验证生成项目依赖，应在 `t.TempDir()`、`.tmp-test` 或外部临时目录中创建独立模块；不得在根模块执行会留下生成项目依赖的 `go get`。

## 公共 API 与契约治理

- 公共 Go API 兼容性由 `make api-compat` / `bin/scripts/check-public-api.sh` 负责；没有可用 git base ref 时脚本会跳过，报告中必须说明跳过原因。
- CLI 命令、flag、JSON 输出、plugin protocol、OpenAPI/proto/thrift/API diff 均视为外部契约；字段删除、重命名、语义变化必须提供迁移说明或 breaking 报告。
- 新增 JSON 字段优先保持向后兼容；删除字段或改变类型属于 P1/P0 风险，必须有兼容窗口或明确版本边界。
- 生成器模板变更必须验证旧输入仍可生成；如果输出 diff 是预期行为，应说明 diff 类型：格式化、功能新增、兼容修复或 breaking。

## 7 阶段治理路线图

项目级质量治理按以下 7 个阶段顺序推进，每阶段完成后进入下一阶段：

1. **补齐测试用例**：识别低覆盖率包，优先用纯单元测试覆盖 0% 函数，再补齐边界分支；记录基线、目标和最终覆盖率。
2. **补齐错误处理**：检查所有 error 返回值是否被处理，补充缺失的 `if err != nil` 和错误包装；优先覆盖对外接口和 goroutine 边界。
3. **补齐日志与可观测性**：统一日志格式、补充关键路径的 metrics/trace、验证生产配置默认值。
4. **补齐文档与注释**：公共 API 补充 godoc、CLI 命令补充 help 文本、复杂逻辑补充行内注释。
5. **性能与并发治理**：识别热点路径、补充 benchmark、检查 goroutine 泄漏和 race condition。
6. **安全与依赖治理**：执行 gosec/govulncheck、清理未使用依赖、验证路径安全和输入校验。
7. **架构与契约治理**：检查公共 API 兼容性、proto/OpenAPI 契约一致性、生成物确定性。

每阶段执行时必须遵循：基线 → 审计 → 修复 → 测试 → 验证 → 报告。

推荐入口：

```bash
make governance-10-rounds
```

可按场景执行更小闭环：

```bash
make tidy
make cover-check
make govulncheck
make gosec
```

## 10 轮架构与质量治理

每次用户要求“架构治理”“质量治理”“重新规划治理”“执行多轮治理”时，默认执行以下 10 轮；每轮结束后自动进入下一轮，最后输出一次总报告。

1. **基线与边界盘点**：确认模块、命令、生成器、运行时、缓存、治理、RPC/REST 边界和现有质量门禁。
2. **上下文与生命周期治理**：检查 `context.Context` 传播、超时、取消、进程生命周期、goroutine 泄漏风险。
3. **CLI 与配置治理**：检查命令参数、usage error、退出码、配置加载、配置热更新、默认值和兼容性。
4. **代码生成治理**：检查 proto/API/model/template 生成结果的确定性、路径安全、兼容性和可编译性。
5. **插件与外部进程治理**：检查插件协议、远程下载、输出限制、错误语义、临时文件、命令注入和资源清理。
6. **缓存与远端依赖治理**：检查本地缓存、远端缓存、分层缓存、模块代理、远程模板和外部下载的可控性。
7. **REST/RPC/API 契约治理**：检查 OpenAPI、descriptor、admin endpoint、path 参数、错误响应和兼容字段。
8. **安全与防御式编码治理**：检查路径穿越、symlink、nil、类型断言、body 限制、URL scheme、敏感信息泄露。
9. **可观测性与生产默认值治理**：检查日志、metrics、trace、profile、governance manager、生产/测试/开发配置。
10. **收敛验证与报告**：运行格式化、测试、race、vet、lint、tidy；汇总变更、风险、验证命令和后续建议。

每轮治理都必须输出可追踪结论：发现了什么、改了什么、为何暂不修复什么、用什么命令验证。

## 5 轮产品化治理工作流

当用户要求执行产品化治理时，可按以下 5 轮推进；目标是把 gofly 从“功能可用”推进到“可采纳、可验证、可运营、可传播”。

1. **信任与采纳基础**：审计公共 API、CLI flag/JSON、control-plane snapshot、生成器输出和升级计划；补齐契约输入、dry-run plan、兼容性测试和机器可读验证入口。
2. **REST/DX 增强**：审计 binding、validation、错误响应、middleware、OpenAPI/schema、path/query/header 参数体验；补齐稳定错误 envelope、文档化 response schema、生成物示例和回归测试。
3. **微服务完整度**：审计配置、服务发现、治理规则、admin/control-plane、K8s、Helm、observability、生产检查脚本；补齐生产默认值、可观测资产、脚本权限和 generated project smoke test。
4. **性能可信度**：统一 benchmark 工作区，维护 `bench/` baseline/matrix/evidence；补齐 REST/RPC/governance/OpenAPI 场景，执行 `make bench-evidence-check` 和至少一个 benchmark smoke，避免仅凭单次数据宣传性能结论。
5. **社区增长与迁移**：审计 README、docs taxonomy、quickstart、migration/case study、P1 roadmap、release/CI 入口；运行 `contract-docs-check`、`p1-growth-check`、`migration-docs-check` 等最小门禁，确保新增能力可被新用户发现和复现。

执行约束：

- 每轮都先盘点已完成项，避免回滚用户、脚本或 linter 的外部变更。
- 涉及生成器、契约或生产资产的改动必须补测试；`gofly new service` 相关改动优先跑 `make test-generated-matrix`。
- 涉及 benchmark 的改动必须保持 `bench/` 为唯一公开工作区；`benchmarks/` 不得重新引入。
- 最终报告必须按 5 轮列出：改了什么、关键文件位置、验证命令、失败或降级项、后续建议。

## AI 治理流水线

LLM 调用经过 9 阶段治理流水线，manifest 通过 `governancePipeline` 字段对外暴露：

```
request-redaction  →  rate-limit  →  token-budget  →  response-cache  →  circuit-breaker  →  provider-call  →  usage-accounting  →  audit-log  →  telemetry-emit
```

| 阶段 | 职责 | 可选 |
|---|---|---|
| `request-redaction` | 在进入流水线前脱敏 prompt 中的密钥和敏感 metadata | 是 |
| `rate-limit` | token-bucket 限流；桶空时返回 `ErrRateLimited` | 是 |
| `token-budget` | 检查累计 token 预算；超限时返回 `token_budget_exceeded` | 是 |
| `response-cache` | 查询响应缓存；命中直接返回，未命中合并并发请求 | 是 |
| `circuit-breaker` | 熔断器开路时快速拒绝；允许探测请求半开恢复 | 否 |
| `provider-call` | 将请求转发给 LLM provider，含超时、重试、failover 包装 | 否 |
| `usage-accounting` | 记录 token 用量（input/output/total）并扣减预算 | 否 |
| `audit-log` | 输出结构化审计日志：操作、provider、model、状态、耗时、token、错误 | 否 |
| `telemetry-emit` | 发射低基数 metric/trace 字段：`cache_status`、`error_class`、`provider_status_code` | 否 |

验证方式：

```bash
gofly ai manifest --json | python3 -c "import json,sys; d=json.load(sys.stdin); gp=d['data']['llmGovernance']['governancePipeline']; assert len(gp)==9, f'expected 9 stages, got {len(gp)}'"
```

## 分层治理门禁

根据改动范围选择门禁层级；越靠近发布或跨模块改动，层级越高。

| 层级 | 适用范围 | 必跑命令 |
| --- | --- | --- |
| L0 文档/注释 | 仅 `AGENTS.md`、注释、说明文本 | 读取相关脚本/配置确认一致性；如改治理命令，执行对应脚本的 help/最小检查 |
| L1 单包改动 | 单个 package 内代码或测试 | `go test -shuffle=on <pkg>`、`go vet <pkg>`；涉及安全时追加 `go tool gosec -quiet <pkg>/...` |
| L2 子系统改动 | generator、cmd、cache、rpc、rest 等子树 | `go test -shuffle=on ./子树/...`、`go test -shuffle=on -race ./子树/...`、`go vet ./子树/...`、覆盖率/安全定向扫描 |
| L3 全仓治理 | 跨模块、依赖、脚本、CI、发布前 | `make governance-10-rounds`、`make cover-check`、`make govulncheck`、`make gosec`、`make tidy` |

若本地环境无法完成高层级门禁，应继续执行低层级可运行命令，并在报告中写明阻塞原因和复现命令。

门禁选择规则：

- 改动跨越两个以上子系统，默认升到 L3。
- 改动 `go.mod`、`go.sum`、`Makefile`、`bin/scripts/`、`.golangci.yml`、发布配置，默认至少 L2；影响全仓命令时升到 L3。
- 改动安全边界（路径、插件、模板、外部进程、网络、TLS、认证、密钥），即使只在单包内，也必须追加安全扫描和攻击面说明。
- 测试只跑目标包时，报告中必须说明为什么未跑全仓，以及未覆盖风险。

## 必跑质量门禁

完成治理后至少执行：

```bash
make governance-10-rounds
```

该入口会按无持久缓存策略执行：

- `gofmt` 检查
- `go test -shuffle=on ./...`
- `go vet ./...`
- `golangci-lint run ./...`
- `go tool govulncheck -scan=package ./...` / `go tool gosec ...`（gosec 历史基线已清零，作为阻塞门禁执行）
- `go test -shuffle=on -race ./...`
- `bin/scripts/coverage-check.sh` 覆盖率门禁同时执行最低阈值和 `COVERAGE_RATCHET` 防回退检查
- `go mod tidy` / `go mod verify` 一致性检查

质量门禁的执行约束：

- `TESTFLAGS` 默认包含 `-shuffle=on`；禁止为了绕过顺序依赖而关闭 shuffle，除非报告中说明具体 flaky 根因和临时豁免范围。
- `GOFLAGS` 默认包含 `-count=1`；不得用缓存结果作为治理结论。
- `bin/scripts/coverage-check.sh` 同时检查 `COVERAGE_THRESHOLD` 和 `COVERAGE_RATCHET`，提高覆盖率后应同步提高 ratchet，防止后续回退。
- `go tool govulncheck` 默认使用 `-scan=package`；如果 source 模式因工具链问题失败，可降级到 package 模式，但必须记录降级原因。
- `go tool gosec` 当前是阻塞门禁；新增代码不得引入安全告警。`#nosec` 仅用于误报或协议/生成器必需场景，必须写明具体规则号和理由。
- `golangci-lint` 失败时优先修复根因；`//nolint` 必须指定 linter 名称和理由，不得使用无理由的宽泛豁免。
- `bin/scripts/governance-10-rounds.sh` 的第 10 轮会执行 coverage ratchet、`govulncheck`、`gosec` 和最终依赖列表检查；只有在工具链或环境限制明确时才允许设置 `GOVERNANCE_SKIP_SECURITY=true`，且报告必须说明跳过原因。

发布治理还应包含 CodeQL、Dependency Review、release checksum provenance attestation 和 GoReleaser SBOM 生成。

如某项因本地环境缺少工具失败，应继续执行其余可执行门禁，并在最终报告中明确失败原因、影响范围和复现命令。

## CI 与发布治理

- CI 必须以 `make governance-10-rounds` 或等价步骤作为主质量入口，并显式包含 `-shuffle=on`、`-race`、`go vet`、`golangci-lint`、coverage ratchet、tidy/verify。
- CI 供应链门禁必须覆盖 GitHub Actions workflow 语法、治理脚本 shellcheck、OSV 依赖扫描、OpenSSF Scorecard、CodeQL、Dependency Review 和 Trivy。
- GitHub Actions 或其他 CI job 必须使用最小权限；发布 job 才允许 `contents: write`、`packages: write`、`id-token: write` 或 attestations 权限。
- GitHub Actions `uses:` 默认 pin 到完整 commit SHA；升级由 Dependabot 分组 PR 统一维护，除非有明确降级理由不得回退到浮动 tag。
- PR 上构建 Docker 镜像不得 push；发布镜像必须带 SBOM、provenance attestation 和漏洞扫描结果；Docker base/builder image 默认 pin digest。
- 依赖更新机器人（Dependabot/Renovate）只能在所有必需门禁通过后合入；自动合入必须依赖分支保护，而不是仅依赖 actor 判断。
- release 前必须记录版本、commit、构建时间、校验和、SBOM/provenance 位置和 go tool 安全扫描结果。
- 任何跳过 release 门禁的行为都必须有人工批准记录，并在最终报告中列为风险。

## 安全治理基线

- **路径安全**：所有写入用户指定目录的代码必须使用相对路径校验、根目录约束、symlink parent 拒绝或 Go 1.24+ `os.Root` 等机制；不能只依赖 `filepath.Clean` + 字符串前缀判断。
- **插件安全**：远程插件只允许 HTTPS 或明确的本地测试来源；下载必须有大小限制、超时、一次性临时文件、sha256 digest 和不复用用户缓存的测试。
- **模板安全**：远程模板同步必须拒绝危险目标目录；模板目录中的 symlink 不得被跟随读取或复制。
- **外部进程**：`exec.Command` 必须传递拆分后的参数，禁止 shell 拼接；可执行文件路径、插件参数和输出大小必须有边界。
- **网络与 TLS**：允许 `InsecureSkipVerify` 的配置必须是显式 opt-in，并在文档/报告中标为风险项。
- **随机数与加密**：安全 token、密钥、nonce 必须使用 `crypto/rand`；非安全用途使用 `math/rand` 时应在代码或报告中说明用途边界。
- **敏感信息**：日志、错误、CLI 输出、测试快照不得泄露 token、密钥、Cookie、Authorization header 或内部凭据。

## gosec / govulncheck 基线治理

- `gosec` 当前以 `-quiet` 作为阻塞门禁；新增代码不允许引入未解释的安全告警。
- `#nosec` 只允许用于误报或设计上必须保留的生成器/CLI 行为，格式必须包含规则号和原因，例如 `#nosec G306 -- generated source files are intentionally user-readable`。
- 每次安全治理报告应记录扫描范围、issue 数量、已收敛数量、是否存在新增/回归告警和下一步模块化清理计划。
- `govulncheck` 以 `-scan=package` 作为稳定默认；如果切换到 source 模式，需记录工具链版本和失败时的降级路径。
- 依赖漏洞优先按“可达性 + 暴露面 + 是否存在缓解配置”排序；仅因间接依赖存在 CVE 但不可达时，不得夸大为 P0。

## 代码生成治理

- 生成器输出必须满足：路径不逃逸、目录可创建、Go 文件 gofmt、输出确定性、重复运行不会产生无关 diff。
- API/RPC/Model/Template 生成变更必须补充 fixture 或临时工程编译测试，证明生成物可编译。
- 契约相关变更必须覆盖 breaking detection、diff、format 和兼容输出；不得只验证 happy path。
- soft delete、streaming RPC、OpenAPI schema/path params、plugin protocol、remote template/cache 是高风险路径，新增行为必须有回归测试。
- 生成项目中的 `go.mod` 变更必须发生在目标目录；根模块依赖卫生由 `bin/scripts/check-mod-tidy.sh` 兜底。
- 生成器文件写入应集中复用安全 helper；新增 `os.WriteFile`、`os.MkdirAll`、`os.ReadFile`、`os.Open` 调用时必须说明路径来源和根目录约束。
- 插件或模板产生的文件列表必须区分“声明文件数”和“实际写入文件数”；自动化 JSON 输出应报告实际写入数。
- 生成物验证优先使用临时模块执行 `go test` 或 `go test ./...`；不能只断言字符串片段。

## 覆盖率与测试治理

- 新增或修复功能应优先写行为测试，不为覆盖率编写只断言实现细节的脆弱测试。
- 对复杂生成器路径使用临时目录和独立 fixture；测试不得依赖执行顺序、全局 HOME、用户插件缓存或持久构建缓存。
- 覆盖率提升任务必须记录基线、目标、最终覆盖率和关键新增用例；若达到新稳定水平，应同步建议提高 `COVERAGE_RATCHET`。
- 涉及 goroutine、stream、watch、cache refresh 的测试应优先跑 `-race`；发现 flaky 先定位根因，不以降低并发或关闭 shuffle 作为长期方案。
- 新增测试不得依赖真实用户 HOME、全局插件缓存、系统 git 配置、固定端口、外部网络或本地持久缓存；确需外部资源时必须可跳过并说明条件。
- 表驱动测试必须包含可读 `name`；失败信息应显示输入、实际值和期望值，避免只输出 “failed”。
- 修改治理脚本时，应至少执行 `sh -n` 和相关 make dry-run；修改测试策略时，应执行最小目标包测试证明策略可用。

### 纯单元测试策略

治理阶段“补齐测试用例”优先使用纯单元测试，避免引入 Docker 或真实网络依赖：

- **gRPC 拦截器**：用 fake handler/invoker/streamer 直接调用拦截器闭包，验证 metadata、重试、熔断、限流行为。
- **Resolver**：用 fake `rpc.WatchResolver` 和 fake `resolver.ClientConn` 测试 `Build`、`ResolveNow`、`watch`、`update` 全生命周期。
- **Server**：用 `httptest` 请求 admin 端点验证 `/healthz`、`/metrics`、`/governance/rules`；用 `healthpb.Health_ServiceDesc` + `health.NewServer()` 测试 `RegisterService`。
- **OTel trace**：用 `metadata.NewIncomingContext`/`NewOutgoingContext` 构造上下文，验证 traceparent 提取和注入；用 fake `ClientStream` 测试 `otelClientStream` 包装器的错误路径。
- **通用原则**：优先覆盖 0% 函数建立行为基线，再补齐边界分支（nil guard、空切片、error path）。

### 覆盖率 ratchet 实践

- `Makefile` 中 `COVERAGE_RATCHET` 当前为 `82%`，`COVERAGE_THRESHOLD` 为 `60%`。
- 单包子系统（如 `rpc/grpc`）治理后若覆盖率显著提升，应在报告中建议是否上调全局 `COVERAGE_RATCHET`。
- 示例：`rpc/grpc` 基线 71.9% → 90.3%，无 0% 函数剩余；全仓稳定后已将 ratchet 收敛到 82%。

## 评审清单

提交前按以下清单自检，并在报告中覆盖相关项：

- 是否存在路径逃逸、symlink、外部进程、网络下载、凭据泄露或 TLS 降级风险？
- 是否新增或删除根模块依赖？是否能由根模块导入图解释？
- 是否影响 CLI/API/RPC/plugin/JSON 输出契约？是否需要 breaking 或迁移说明？
- 是否新增足够行为测试、shuffle 测试、race 测试或临时生成物编译测试？
- 是否保持覆盖率不回退？是否应提升 `COVERAGE_RATCHET`？
- 是否运行了与改动层级匹配的门禁？未运行项是否有原因和复现命令？

## 异常与降级处理

- 如果 `utree flush`、`git diff`、`git status` 因当前目录不是 git worktree 失败，应记录为环境限制，不得把它归类为代码验证失败。
- 如果安全/治理工具因为工具链 bug 崩溃，先尝试推荐降级参数（例如 `govulncheck -scan=package`）和更小扫描范围；仍失败时记录版本、命令、错误摘要。
- 如果依赖下载、模块代理或缓存目录受限，优先使用项目临时 `GOCACHE/GOTMPDIR`，必要时复用模块下载缓存；不得把临时依赖写入仓库。
- 如果外部进程或测试生成临时工程，清理失败时报告路径和影响，避免把临时目录误认为仓库改动。

## 报告格式

最终报告包含：

- 本次治理目标
- 10 轮执行摘要
- 修改文件清单，引用格式为 `file_path:line_number`
- 质量门禁结果
- 已知风险和未完成项
- 后续建议

报告要求：

- 引用代码或脚本位置时使用 `file_path:line_number` 格式。
- 对每条失败门禁给出：命令、失败类型、是否环境限制、是否影响本次目标。
- 对安全扫描输出区分“新增问题”“已收敛问题”“历史基线”；历史基线不得掩盖本次新增风险。
- 对依赖变更说明是否影响根模块、生成项目或测试 fixture。
- 结论必须明确是否达到用户目标，不能只罗列执行过程。
