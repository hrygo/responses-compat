# Responses 兼容性代理设计（方案 B）

日期：2026-09-24
状态：设计待确认；方向已选定，尚未授权实施本设计
基线：本地 `v0.1.0`，不移动或重写该标签

## 1. 定位与目标

将 Muse 专用 Adapter 演进为轻量、可配置的 Responses API 兼容性代理。通用化适配算法和配置边界，不把 Muse 的特殊规则应用于所有提供方。

保持既有链路：

```text
Codex / Responses 客户端
  → CLIProxyAPI（入口、账号选择、模型路由）
  → Adapter 实例（显式兼容配置）
  → 该实例固定的 Responses 上游
```

首阶段一个进程只服务一个上游和一套兼容配置。多上游使用同一二进制启动多个实例，由 CLIProxyAPI 选择实例。无需建设第二个路由器。

成功标准：现有 Muse 用法不变；可以通过配置连接另一个 Responses 上游、复用适配规则或不做语义变换；能证明没有把 Muse 字段处理泄漏到其他配置。

## 2. 范围与非目标

本阶段提供：配置读取和启动校验、固定上游与模型白名单、可组合的 Schema/工具名/reasoning 规则、有界 JSON/SSE 处理、脱敏诊断和契约测试。

不提供：账号池、凭据存储、OAuth、自动模型发现、负载均衡、跨上游容灾、自动生成重试、跨 API 协议转换、工具执行、管理 UI、动态插件、热更新、公网监听或多租户服务。

暂不更改仓库、Go module、二进制或 LaunchAgent 名称。品牌改名与协议能力扩展分开，避免影响已有安装。

## 3. 配置契约

新增 `--config <path>`，读取本机 JSON 文件；不依赖新配置库。

- 不传 `--config`：使用内置 Muse 配置，保留 `127.0.0.1:18317`、原上游、原模型和原变换策略。
- 指定配置：文件必须存在且合法；不自动回退到 Muse。配置只在启动时加载。
- 拒绝未知键、重复键、尾随 JSON、未知版本/策略、不合理数值及非法 URL；错误只报告字段和原因，不回显文件内容、URL 或凭据。
- `config_version` 固定为 `1`。`listen`、`upstream_base_url`、非空 `models` 和 `profile` 为显式配置必填项。
- `models` 为精确模型 ID 白名单，无通配符、模型名重写或自动匹配。
- `profile` 支持 `muse` 与 `passthrough`；先应用预设，再应用显式 `transforms` 覆盖。未知 profile 拒绝启动。
- 配置文件不接受凭据字段。授权仍由 CLIProxyAPI 随请求提供。
- 可选 `extra_request_headers` 和 `extra_response_headers` 均为头名字符串数组，默认空；只指定允许转发的头名，不配置头值。校验合法 HTTP 头名并按大小写无关方式去重。

保持 Muse 行为的显式配置：

```json
{
  "config_version": 1,
  "listen": "127.0.0.1:18317",
  "upstream_base_url": "https://opencode.ai/zen/go/v1",
  "models": ["muse-spark-1.3-contributor"],
  "profile": "muse"
}
```

可覆盖的变换策略：

| 配置项 | 取值 | muse 预设 | passthrough 预设 |
|---|---|---|---|
| `transforms.schema_refs` | `preserve` / `inline` | `inline` | `preserve` |
| `transforms.recursive_refs` | `reject` / `empty_schema` | `empty_schema` | `reject` |
| `transforms.tool_name_max_bytes` | `null` 或 16–256 的整数 | `64` | `null` |
| `transforms.reasoning_ids` | `preserve` / `drop` | `drop` | `preserve` |

`recursive_refs` 仅在 `schema_refs=inline` 时生效。`tool_name_max_bytes=null` 禁用名称变换。限制按 UTF-8 字节计算，并在文档中明确，不笼统宣称是所有提供方的字符计数方式。

上游地址由管理员配置，客户端请求不能覆盖。只接受 HTTPS，且 URL 不能包含 userinfo、query 或 fragment；仅对字面量回环 IP 允许 HTTP，供本地测试使用。监听地址也只接受字面量回环 IP 和合法端口。上游连接不跟随重定向。

## 4. 请求与变换语义

固定顺序：大小限制 → JSON 对象及模型校验 → 建立请求级别名表 → 工具名改写 → Schema 处理 → reasoning 字段处理 → 转发。

### 4.1 透传配置

默认不删除字段、不展开引用、不产生工具别名。校验通过且没有实际变换时，转发原始请求正文，不重新编码。未知 Responses 字段保持不变。

这里的透传指正文和协议语义，不意味着任意 HTTP 请求头、压缩方式和传输分块都逐字节透传。

### 4.2 Schema

`inline` 只解析本地引用，不抓取外部 URL。无法解析或不支持的引用明确失败，不静默删除约束。

`empty_schema` 明确表示有损降级：递归回边变为 `{}`，该位置的约束被放宽。`reject` 则拒绝递归 Schema。请求体上限 32 MiB、Schema 深度上限 64，保留现有展开预算。

### 4.3 工具名称

覆盖工具声明、嵌套工具声明、additional_tools、tool_choice 和输入中的 function_call；名称冲突时拒绝请求。

别名基于原名确定性生成，受配置长度上限约束；默认 64 字节策略保持 v0.1.0 的别名算法和结果，避免升级改变名称。调整长度策略会改变部分别名，需在会话边界应用新配置。

映射仅保存在请求上下文中，不引入磁盘或跨请求缓存。恢复限于已识别的工具声明、function_call 对象和有测试支持的 Responses 工具事件；不能递归改写任意对象的 `name` 字段。参数字符串、用户文本、非工具对象保持不变。

当开启名称适配且请求含 `previous_response_id` 时，本阶段返回明确的 422：尚不能保证服务端保存的工具声明与请求级映射一致。此为相对 v0.1.0 新增的显式限制，必须写入升级说明。客户端需使用带完整上下文和工具声明的重放方式；名称适配禁用时不施加此限制。

### 4.4 Reasoning

只有 `drop` 策略删除输入 reasoning 项的 `id`；保留 encrypted_content、summary 及未知字段。`preserve` 不改变这些内容。不把删除 ID 描述为跨提供方通用修复。

## 5. HTTP、JSON 与 SSE

保留 `GET /healthz` 和 `POST /v1/responses`，不增加其他 Responses 子资源。healthz 只证明本地进程可服务，不证明凭据、上游或模型可用。

保留请求取消传播、优雅退出和现有响应头超时；不添加自动重试，不在 SSE 中途换上游。

- 非 2xx 响应保留状态与正文，不做工具名称改写。
- 没有名称映射时，响应正文流式转发。
- 有映射时，JSON 正文和 SSE 事件分别有界改写；成功保留上游状态。
- JSON 输入与展开后输出均限 64 MiB；SSE 原始事件及完整改写后事件均限 64 MiB，包含事件封装开销。
- JSON 改写失败时，在响应头提交前返回 502。
- SSE 改写失败时，提交终止 response.failed 事件并结束，不继续转发 [DONE]。
- 需要恢复名称但成功响应的媒体类型不受支持时返回 502，不能绕过恢复逻辑直接透传。
- SSE 保留事件顺序，支持 LF/CRLF、多行 data、注释和 EOF 尾帧；首个上游事件之前刷新下游响应头。

请求头以现有必要授权/会话头为基础，允许配置额外头名；禁止配置 Host、Content-Length、Content-Type、Accept-Encoding、Connection 等传输控制或逐跳头，并排除 Connection 声明的头。响应额外头也只能通过独立白名单开放，不能复制失效的正文长度或编码头。

## 6. 内部结构

采用静态 Go 组合，不设计动态插件 ABI，不创建通用 workflow 引擎：

1. 配置层：解析、校验、预设解析，生成不可变的实例配置。
2. 转发层：HTTP 生命周期、头处理、固定上游、错误和流式传输。
3. 请求适配层：按策略调用现有 Schema、工具名称、reasoning 算法，返回正文和请求级映射。
4. 响应适配层：利用同一请求的映射处理 JSON/SSE。

现有算法能直接复用就保留；不为文件整齐而整体搬迁代码。具体签名、文件拆分和任务顺序在设计批准后的实施计划中确定。

## 7. 诊断、安全与运维

默认仍仅本机可用；回环监听并不等于对其他本地进程具备身份隔离。

日志不得包含请求/响应正文、工具原名及参数、模型输出、encrypted_content、授权头、完整上游 URL 或会话标识。可记录本地生成的请求 ID、profile、触发的规则名、HTTP 状态、错误类别和耗时。

不修改 CLIProxyAPI、现有 LaunchAgent、已安装二进制或运行服务。后续本机部署作为独立步骤，先核实配置和回退点，再按用户授权执行。

## 8. 验收与审查重点

- 现有 50 项测试保持通过；若有与新限制冲突的断言，逐条记录行为变更，不能简单删除测试。
- 无参数启动保持原 Muse 地址、模型、请求变换、授权转发及主要错误行为。
- 配置错误、模型不匹配、非回环监听、非法上游和受限请求头，在产生网络副作用前拒绝。
- passthrough 不删除 reasoning ID，不处理 Schema，不改工具名称，并保持原始正文；显式覆盖只启用对应规则。
- 不同实例/并发请求的规则和名称映射不会交叉污染。
- 覆盖名称边界、别名冲突、工具调用完整回环、JSON/SSE 恢复及 previous_response_id 限制。
- 覆盖非工具 name、参数字符串和未知字段不被误改。
- 覆盖有损递归策略与拒绝策略、请求及响应超限、展开后完整 SSE 帧超限。
- 覆盖取消、超时、EOF、多行 SSE、畸形事件、失败后不转发 [DONE]、非 2xx 透传和媒体类型不支持。
- 使用两个本地模拟上游验证固定目标、凭据不串路由、不跟随重定向；不调用付费服务，不冒称第二个真实 provider 已兼容。
- 验证 `go test ./... -count=1`、`go test -race ./... -count=1`、`go vet ./...`、构建和脱敏日志测试。

## 9. 交付和回退

交付范围：代码、契约测试、无凭据配置示例、README 配置说明及兼容规则说明。首阶段兼容声明仍只覆盖已验证的 Muse 路由；其他上游是可配置能力，不是普遍兼容保证。

不移动 v0.1.0 标签；后续发版时使用新版本并突出 previous_response_id 的显式限制。回退方式是恢复旧二进制和旧启动方式；无持久化数据迁移。不得在运行中自动切换 profile 作为所谓回退。

## 10. 待确认的设计取舍

本方案建议：单实例单上游；标准库 JSON 配置；内置 muse/passthrough 预设加显式规则覆盖；继续本机监听；保留现有名称；名称适配场景暂不支持 previous_response_id。

这些是待审阅的具体设计，不代表用户已经逐项批准。设计确认后再编写实施计划并确认执行方式。
