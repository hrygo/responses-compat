# Responses Compat

Responses Compat 是一个本机、单固定上游的 OpenAI Responses API 适配代理：只在明确配置或预设启用时，对请求执行兼容变换，并按需恢复工具名。它不是任意 URL 转发器，也不会按每个请求动态切换上游。

## 工作流程

```text
Codex
  → CLIProxyAPI (127.0.0.1:8317)
  → Muse 路由
  → Responses Compat (127.0.0.1:18317)
  → OpenCode Go Responses API
```

这是现有 Muse 集成的默认示意，不代表其它上游已经验证，也不证明 OpenCode CLI/App 是否能直接使用 Muse。请求沿链路转发，响应沿原路返回；CLIProxyAPI 负责选路和提供请求授权，Responses Compat 只负责配置指定的 Responses 请求/响应边界处理。

## 项目边界与默认行为

- 只提供 `GET /healthz` 与 `POST /v1/responses`；监听地址必须是字面量 loopback IP。配置文件中的 `models` 是精确白名单，不支持通配符或自动发现。
- 不带 `--config` 时，保留原 Muse 升级兼容默认值：监听 `127.0.0.1:18317`、上游 `https://opencode.ai/zen/go/v1`、模型 `muse-spark-1.3-contributor`。
- 默认 `muse` 策略展开工具参数中的本地 JSON Schema `$ref`，把递归引用的回边替换为 `{}`（有损）；按 UTF-8 字节数而不是字符数计算函数工具名，超过 64 字节时改为确定性别名并在 JSON/SSE 响应中恢复；移除 reasoning 输入项中不稳定的 provider-scoped `id`。
- 启用任何新上游时，建议先使用 `passthrough`，只在逐条对照证明必要后再打开变换。`passthrough` 保留请求原始字节和 Schema `$ref`，保留 reasoning ID 并禁用工具名别名；它不会主动展开/检查 Schema 递归引用。

## 配置

严格解析 JSON 配置，必须显式提供 `config_version`、loopback `listen`、固定 `upstream_base_url`、非空 `models` 和 `profile`。未知/重复字段、错误配置不会回退到默认 Muse 路由。示例：

```sh
go run . --config examples/muse.json
```

- [`examples/muse.json`](examples/muse.json)：当前 Muse 兼容预设。
- [`examples/muse-passthrough.json`](examples/muse-passthrough.json)：使用独立端口的诊断配置，不代表该上游已通过真实调用验证。
- `transforms` 可在预设上独立覆盖 `schema_refs`、`recursive_refs`、`tool_name_max_bytes` 与 `reasoning_ids`；`tool_name_max_bytes: null` 表示明确关闭别名，省略则继承预设，数值 `0` 不接受。
- `extra_request_headers` 和 `extra_response_headers` 只列出允许透传的头名，不配置头值。授权等敏感值仍由调用方/CLIProxyAPI 在请求中提供；程序不保存或记录请求正文与凭据。

## 源码职责

- `server.go`：handler 配置、请求准入与上游 HTTP 请求编排；`headers.go`：头部 allowlist、Connection 点名和受控头过滤。
- `request_normalizer.go`：请求校验与变换顺序；`schema.go`、`schema_refs.go`：工具参数 Schema 的本地引用展开与预算。
- `response_rewriter.go`：JSON 响应中的工具名恢复；`response_sse.go`：SSE 行/帧处理、名称恢复和失败事件；`upstream_response.go`：上游响应媒体分类、状态/头复制及流式转发。

## 响应与安全边界

- 未发生工具名改写时，响应正文按流转发；SSE 会在首个事件前刷新响应头。额外头仍受 allowlist、逐跳头、`Connection` 指定头和受控长度/编码头过滤。
- 上游非 2xx 响应的状态码和正文保持不变。成功响应在安全改写后保留上游状态码。
- 请求体和 Schema 展开后最多 32 MiB，Schema 深度最多 64。函数工具名的策略字段 `tool_name_max_bytes` 只接受 16–256 的正整数；显式 `null` 才表示关闭别名。
- 有工具名别名时，只允许改写 `application/json`、`application/*+json` 或 `text/event-stream` 成功响应；其它/无效媒体类型在响应提交前以 HTTP 502 失败关闭。JSON 响应及单个完整 SSE 事件在扩展后最多为 64 MiB；无效或超限的 SSE 事件会产生终止的 `response.failed` 事件并关闭连接，不转发未恢复的别名或伪报成功的 `[DONE]`。
- 本地单元与 HTTP 测试验证适配器的确定性行为，不等于对 Muse/OpenCode Go 服务端契约、真实账号权限或 OpenCode CLI/App 直连能力完成了实测。

## 构建与验证

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -trimpath -o responses-compat .
```

服务安装与回退步骤见 [`docs/deployment.md`](docs/deployment.md)。任何真实上游验证、停止/替换已有服务或发布操作，都应先核实目标并按授权门执行。
