# Responses Compat 重构实施方案（Luna Guide）

# 1. 问题结论

**推荐结论：保留单一 `package main` 与 Go 标准库实现，做一次行为保持型的职责拆分；不重写代理，不引入框架，不改变部署。**

- Goal：让 HTTP 编排、头部规则、请求归一化、Schema 算法、JSON 恢复、SSE 帧处理分别有清晰代码边界，以便后续单项兼容规则可以独立修改和验证。
- Architecture：新增 4 个生产文件，迁移既有实现；只对头部复制的相同机制做小范围去重，并集中定义已有策略字符串常量。不同协议语义不统一成通用框架。
- Tech Stack：`module responses-compat`，`go 1.27`，标准库、`testing`、`httptest`；不新增依赖。
- 状态：方案形成时授权仅覆盖分析与本方案文档。用户于 2026-09-24 明确要求在独立 worktree 实施；本次授权覆盖该 worktree 内的代码和文档修改及离线验证，不包括提交、推送、发布、真实模型调用或服务切换。
- 执行方式：获实现授权后，由 Luna 顺序执行；可使用当前可用的 `executing-plans` 技能。本方案不授权启动子代理，不要求安装缺失技能。

## 基线与证据

- 工作区：`/Users/hrygo/Documents/responses-compat`。
- 分支/提交：`main` / `72c5a6d4ccebbbc8ffcf715e766f506770f8dacf`；分析开始时工作区干净。
- 核实日期：2026-09-24，Asia/Shanghai；本轮运行工具链 `go1.27.1 darwin/arm64`。这是本次运行环境，不增加项目平台限制。
- 当前根目录 9 个生产 Go 文件、8 个 Go 测试文件。规模不支持为了“架构整齐”而引入多层包或 DI 容器。
- 图谱项目 `responses-compat`，generation `2026-09-24T13:23:03Z`，full 模式，342 节点、1,132 条边；`index_status=ready`。
- 已对全部根目录 Go 文件及所用文档调用 `check_index_coverage`，文件 freshness 为 `metadata_match`；根范围排除 `.git`、`.DS_Store`、`muse-codex-adapter`。该信号仅为 best effort，不是完整正确性证明。
- 图谱查找 9 个核心函数并完成结果分页；对 `serveResponses`、`normalizeRequestWithPolicy`、`normalizeExtraHeaderNames` 作深度 1 双向追踪（含测试、无后续分页）。关键本地调用有 LSP 依据；`Close`、`Write` 等启发式边不作可靠跨类型关系，已回到源码检查。

**本轮实测（重构前）：**

| 命令 | 当前结果 | 范围 |
|---|---|---|
| `go test ./... -count=1` | 通过；工具报告 146 项通过，1 个包，包含子测试 | 离线单元与本地 HTTP 测试 |
| `go test -race ./... -count=1` | 通过；工具报告 146 项通过 | 同上，启用 race 检查 |
| `go vet ./...` | 通过，无诊断 | 静态检查 |
| `go build -trimpath -o <临时目录>/responses-compat .` | 通过；产物 10,251,634 字节，随后随临时目录删除 | 未安装、未启动或替换服务 |

本轮没有复现新的功能故障，也没有跑性能基准。以下“根因”指职责耦合的来源，不声称已经证明线上 Bug 或性能瓶颈。

阅读依据：`README.md`；`docs/superpowers/specs/2026-09-24-responses-compatibility-proxy-design.md`；`docs/superpowers/plans/2026-09-24-responses-compat-implementation.md`；`docs/compatibility/2026-09-24-evidence-register.md`；`docs/deployment.md`。旧设计中的“待修复/更名”描述有历史性质，以当前源码、测试及后续证据记录为准，不重做已经完成的迁移。

# 2. 当前实现与根因

## 当前调用路径

```text
main → runCommand → loadConfig/decodeConfig → runServer
  → NewConfiguredHandler
      → 配置验证、URL 解析、头名归一化、复制配置/client、禁用重定向
      → GET /healthz：本地 204
      → POST /v1/responses：serveResponses
          → 有界读请求 + envelope.model 检查
          → normalizeRequestWithPolicy
              → 工具名别名收集/替换
              → Schema 本地引用展开/相邻字段处理
              → reasoning ID 处理、additional_tools Schema 处理
              → 未改变则返回原字节，否则重新编码并检查大小
          → 生成固定上游 URL、受控透传头、client.Do
          → 非成功/无别名直传，或 JSON 恢复，或 SSE 恢复
```

## 文件级发现

行号对应上述提交；实施时以函数符号定位，不能把行号当作永久范围。

| 位置 | 已确认现状 | 维护问题 | 本次处理 |
|---|---|---|---|
| `server.go:102–198`，`serveResponses` | 同时负责请求验证、发送、媒体分类、JSON 缓冲与改写、SSE 提交/刷新/转发 | 修改响应分支时容易影响请求处理或提前提交状态码 | 将接收上游响应后的策略分支抽至 `upstream_response.go` |
| `config.go:197–276` 与 `server.go:14–38,214–265` | 配置头校验依赖 server 中的默认列表；请求/响应复制循环重复，受控头定义分散 | 同一头部规则变更要跨配置和 HTTP 文件追踪 | 全部归属 `headers.go`，保留方向差异 |
| `schema.go:20–127` | 整个 Responses 请求的模型、别名、reasoning 逻辑放在 Schema 算法文件 | 文件名不能表达职责，Schema 改动与请求入口混在一起 | 迁移到 `request_normalizer.go` |
| `response_rewriter.go:320–489` | SSE 字段解析、帧预算、流读取和终止事件与 JSON 路径恢复混写 | 流式行为和对象遍历难以分别审查 | 迁移到 `response_sse.go`，JSON 核心原样保留 |
| `compatibility.go` 及调用点 | profile/规则字符串在默认值、验证、分支中重复 | 后续新增规则容易拼写漂移 | 增加非导出字符串常量，保持现有字段类型和 JSON 值 |

## 不应误判为“可以直接合并”的重复

1. **请求两次解码**：`serveResponses` 的结构体 envelope 检查与 `normalizeRequestWithPolicy` 的 map 解码不是严格等价。前者决定 400，后者可能返回 422；大小写字段匹配也不同。本轮不合并解码，不宣称有性能收益。
2. **配置严格 JSON 与请求 JSON**：配置拒绝未知键和大小写归一后的重复键；请求需要保留未知业务字段和 passthrough 原始字节。不能共用配置 decoder 清洗请求。
3. **Schema 与响应的大小计算**：`schemaBudget` 控制展开工作和输出，`encodedJSONSize` 控制响应恢复扩张，SSE 还包含封装开销。不能简单合并预算/计数器。
4. **请求/响应工具遍历**：请求工具支持的结构与响应允许恢复的路径不同。不能用递归查找任意 `name` 的通用 walker 替代，否则会重新误改 metadata/arguments。
5. **配置构造时重复校验**：`decodeConfig` 和 `NewConfiguredHandler` 面向不同调用入口；直接构造 `RuntimeConfig` 也必须验证。本轮保留防御性验证。

# 3. 目标行为

以下均为重构不变量，不是新增功能：

## 请求、配置与名称

- 保留 `go run .`、`--config`、现有 `NewHandler`、`NewConfiguredHandler`、`NormalizeRequest` 签名；保留可直接构造的 `RuntimeConfig`/`CompatibilityPolicy` 字段类型。
- 未指定配置文件仍使用既有 Muse 地址、端口、模型与变换；错误配置不能回退到默认值。
- 只提供现有路由，不增加 `/models`、动态路由、认证管理、重试或模型重命名。
- 配置上限 1 MiB；请求及归一化输出上限 32 MiB；Schema 深度 64；JSON 恢复及完整 SSE 帧上限 64 MiB。
- `tool_name_max_bytes` 省略继承、null 关闭、数值 0 拒绝；运行时 `ToolNameMaxBytes=0` 仍表示关闭。有效数值范围仍为 16–256。
- 模型精确匹配；passthrough/no-change 保留请求原始字节，包括空白、未知字段、数字和顺序。发生变换时不承诺对象原始键序。
- 原有别名算法、`muse_` 前缀、64 字节默认阈值及既有结果保持不变；名称长度按 UTF-8 字节而非 rune 个数。
- 不扩大 reasoning ID 删除范围，不改 strict/store/previous_response_id，不全局删除任意 ID。
- `$ref` 相邻等价约束、注解覆盖、冲突错误分类和递归策略均保持现状；`preserve` 不因 `recursive_refs=reject` 主动扫描递归。

## HTTP 与流式状态

| 条件 | 保持的行为 |
|---|---|
| 请求读取失败/初步 JSON 或模型检查失败 | 400，正文不暴露原始内容 |
| 请求超过上限 | 413，不发出上游请求 |
| 请求归一化失败 | 422，保留现有固定错误码分类 |
| 上游连接失败 | 502；不自动重试 |
| 上游非 2xx | 保留状态、正文；仍执行原有头部过滤，不改写工具名 |
| 成功响应，无工具名别名 | 正文直接转发；SSE 在首事件前 Flush |
| 成功 JSON/+json，有别名 | 完整读取、大小检查、恢复全部成功后才提交上游状态和正文；失败为 502 |
| 成功 SSE，有别名 | 先提交并刷新响应头，然后按完整帧恢复并输出 |
| 成功未知/无效媒体类型，有别名 | 提交前 502，不能泄漏别名 |
| SSE 已提交后遇到改写 JSON 无效/预算超限 | 输出终止 `response.failed` 后结束；不得追加成功 `[DONE]` 或尝试改写 HTTP 状态 |
| SSE 读取或下游写入出现其它 I/O 错误 | 保留当前退出行为；不在本轮扩大为新的错误事件策略 |

头部规则继续过滤 hop-by-hop、`Connection`/`Proxy-Connection` 点名头和受控编码/长度头。`Content-Type` 的配置禁用规则与响应实际转发规则不同，必须保留。客户端取消继续传播到上游；成功取得的 `resp.Body` 在全部分支均由调用者关闭。

# 4. 推荐解决方案

## 方案比较与决定

- A：只补注释。风险低，但 HTTP 头与 JSON/SSE 的职责混杂依旧存在。
- **B：同包职责拆分 + 极小去重（采用）**。既有测试不需要跨包迁移，保留构建入口与所有兼容包装。
- C：迁移到 `cmd/` + 多个 `internal/` 包，抽接口/插件管线。当前无多消费方需求，会扩大导出面、测试迁移和依赖边界；本轮不采用。

## 最终文件职责（相对仓库根目录）

| 文件 | 操作与职责 |
|---|---|
| `main.go` | 保留启动、信号、transport 设置；不修改生命周期 |
| `config.go` | 保留配置载入、解码、overrides、地址/模型验证和 defensive copy；移出头名规则 |
| `compatibility.go` | 保留策略结构与验证；新增策略/profile 常量 |
| `headers.go`（拟新增） | 默认 allowlists、头名合法性、配置校验、动态 hop-by-hop 过滤、请求/响应复制 |
| `server.go` | handler 装配、请求准入、固定上游构造、发送与关闭 Body、本地 JSON 错误输出 |
| `upstream_response.go`（拟新增） | 媒体类型分类、提交前验证、JSON/SSE/直传分支、flushWriter |
| `request_normalizer.go`（拟新增） | 原请求归一化入口、变换顺序与 no-change 字节保留、模型白名单辅助 |
| `schema.go` | Schema 预算、工具参数树归一化、JSON Pointer、递归展开 |
| `schema_refs.go` | 原样保留引用相邻字段规则及其预算 |
| `tool_names.go` | 原样保留别名生成、冲突检测、请求名称替换 |
| `response_rewriter.go` | JSON 名称恢复、限定路径遍历、编码大小计算；不再含帧读取/写入 |
| `response_sse.go`（拟新增） | SSE 帧/行解析、流读取、错误终止事件 |
| `request_errors.go` | 保留固定错误码与包装提取，不与 HTTP 错误包装强行统一 |

由 9 个生产文件变为 13 个；不新建 package、interface、registry 或 `utils.go`。测试按职责迁移，不能复制同名测试保留两份。

# 5. 详细实施步骤

所有命令在仓库根目录执行；每一步测试通过再进入下一步。新增刻画测试应先在旧实现上通过，证明冻结的是已有行为；纯重构无需人为制造功能失败。若新测试揭示真实缺陷，单独记录并停下相关行为变更，不把修复混进重构。

## Task 0：核对基线并冻结易变边界

**文件：**`server_test.go`、`config_test.go`（修改）；`headers_test.go`（拟新增）。

- [x] 核对 `git status --short`、`git rev-parse HEAD` 和 `go version`。不同于本方案基线时先检查差异；不覆盖并行改动，不使用 reset/checkout 清除工作。
- [x] 在 `server_test.go` 新增 `TestHandlerRequestAdmissionContract`，使用现有 `roundTripperFunc` 和计数器，按第 7 节固定 400/413/422 及零上游调用。
- [x] 在 `headers_test.go` 新增第 7 节的配置/复制矩阵，先直接测试现有函数，不依赖尚未新增的辅助函数。
- [x] 在 `server_test.go` 新增 `TestHandlerPreservesSuccessfulCreatedStatus`，验证带别名的 JSON 201 恢复后仍为 201。
- [x] 新增 `TestHandlerClosesUpstreamBodyAcrossResponseBranches`，使用只在测试中声明的 close-tracking ReadCloser（包装 `strings.Reader`，Close 增加计数），测试正常、拒绝和恢复失败分支 Close 恰好一次。
- [x] 跑聚焦测试及全量测试，将新测例是否通过写入执行记录。不得把以下方案中的测试当成本轮已执行。

```sh
go test ./... -run 'Test(HandlerRequestAdmissionContract|HeaderPolicyContract|HeaderCopyPreservesValues|HandlerPreservesSuccessfulCreatedStatus|HandlerClosesUpstreamBodyAcrossResponseBranches)$' -count=1
go test ./... -count=1
```

## Task 1：统一头部规则归属

**文件：**`config.go`、`server.go`（修改），`headers.go`（拟新增），`headers_test.go`、既有配置/HTTP 测试。

1. 从 `server.go` 移入 `forwardedRequestHeaders`、`forwardedResponseHeaders`、`copyRequestHeaders`、`copyResponseHeaders`、`connectionHeaderTokens`、`isRequestControlledHeader`、`isResponseControlledHeader`。
2. 从 `config.go` 移入 `normalizeExtraHeaderNames`、`validHeaderName`、`isHTTPTokenByte`、`isHopByHopHeader`、`isControlledHeader`、`isAlreadyForwardedHeader`。
3. `decodeExtraHeaderNames` 留在配置文件，只负责 RawMessage 的省略/null/数组语义，再调用移入的新文件函数。
4. 保留已有函数签名和错误文案。将复制循环收敛为一个非导出辅助函数；两个方向的 wrapper 继续存在。
5. 不把两套默认 allowlists 合并；不缓存 Connection tokens，不修改 src Header。

**拟新增辅助函数（完整机制）：**

```go
func copyAllowedHeaders(dst, src http.Header, allowed []string, controlled func(string) bool) {
    nominated := connectionHeaderTokens(src)
    for _, name := range allowed {
        lower := strings.ToLower(name)
        if isHopByHopHeader(lower) || controlled(lower) || nominated[lower] {
            continue
        }
        for _, value := range src.Values(name) {
            dst.Add(name, value)
        }
    }
}
```

两个 wrapper 分别传原有的请求/响应 controlled 函数。构建 allowed 时沿用 `append(append([]string(nil), defaults...), extra...)`，不能把 extra append 到默认列表底层数组，也不顺手改变直接调用 wrapper 时的去重行为。

`isControlledHeader` 可改为 `isRequestControlledHeader(lower) || isResponseControlledHeader(lower)`；这是**配置禁止集合的并集**，不是所有响应转发的禁止集合。`Content-Type` 仍能通过内建响应 allowlist 转发。

**完成条件：**头部规则只在 `headers.go` 定义；调用方行为与 Task 0 矩阵相同；无跨文件反向依赖于 server 默认列表。

```sh
go test ./... -run 'Test(Header|DecodeConfigValidates|ConfiguredHandlerFilters|ConfiguredHandlerCopies)' -count=1
go test ./... -count=1
```

## Task 2：集中策略词汇，分离请求编排与 Schema 算法

**文件：**`compatibility.go`、`config.go`、`schema.go`、`schema_refs.go`（修改），`request_normalizer.go`（拟新增），`request_normalizer_test.go`（拟新增，迁移测试）。

在 `compatibility.go` 增加以下**非导出、非自定义类型**常量：

```go
const (
    profileMuse = "muse"
    profilePassthrough = "passthrough"
    schemaRefsInline = "inline"
    schemaRefsPreserve = "preserve"
    recursiveRefsReject = "reject"
    recursiveRefsEmptySchema = "empty_schema"
    reasoningIDsDrop = "drop"
    reasoningIDsPreserve = "preserve"
)
```

- 用它们替换生产代码中的同义策略比较和默认值；不要替换 JSON 字段名、reasoning 对象类型或错误文案。测试至少保留外部字面字符串断言，避免常量改错时测试同步错。
- 从 `schema.go` 原样移出 `NormalizeRequest`、`normalizeRequestWithToolNames`、`normalizeRequestWithPolicy`、`modelAllowed`，签名不变。
- `maxRequestBytes` 随请求入口放在 `request_normalizer.go`；`maxSchemaDepth` 留在 `schema.go`；`museModel` 移到 `compatibility.go`。不改变数值。
- `normalizeToolTree`、`normalizeToolTreeWithRecursiveRefs` 及 `schemaBudget` 留在 `schema.go`；它们是参数 Schema 变换实现，不是通用 Responses 输入编排。
- `schema_refs.go` 只替换适用的策略字面常量；不改变合并算法和临时预算。
- 请求变换顺序严格沿用：校验 → decode/模型 → 别名 → 根 tools Schema → 按 input 原顺序处理 reasoning/additional_tools → changed 判定 → encode/上限。
- 原有 `schema_test.go` 中的 NormalizeRequest 入口测试迁移到 `request_normalizer_test.go`；`TestExpandSchemaNodeEnforcesExpansionBudget` 仍留在 `schema_test.go`，`decodeObject` 测试辅助保留单一定义。保留原测试函数名。

**完成条件：**Schema 算法文件不再负责顶层模型/变换顺序；所有旧入口仍可调用；no-change 字节与别名算法不变。

```sh
go test ./... -run 'Test(Normalize|Passthrough|BuiltInCompatibility|CompatibilityPolicies|BuildToolName|FunctionToolAlias|RefSibling|ExpandSchema)' -count=1
go test ./... -count=1
```

## Task 3：分离 SSE 编码与 JSON 名称恢复

**文件：**`response_rewriter.go`（修改），`response_sse.go`、`response_sse_test.go`（拟新增），`response_rewriter_test.go`（修改）。

原样迁移以下完整符号到 `response_sse.go`：

- `rewriteSSEFrame`、`isBlankSSELine`、`splitSSELine`。
- `streamSSEWithToolNameRestore`、`writeSSERewriteError`、`writeSSEFrame`。
- `maxSSEFrameBytes`、`rewriteFailureSequence`，以及相应 `bufio`/`fmt`/`net/http`/`sync/atomic` imports。

保留 `restoreToolNamesInJSON`、`restoreToolNamesInSSEJSON`、`restoreToolNames`、路径选择函数、JSON 大小计算与两个 sentinel errors 在 `response_rewriter.go`；`maxResponseRewriteBytes` 也留在那里。SSE 调用现有 JSON 恢复函数，不复制其实现。

迁移 `response_rewriter_test.go` 中名字以 `TestRewriteSSE`、`TestWriteSSE`、`TestStreamSSE`、`TestSSE`、`TestUnknownSSE` 开头的测试到 `response_sse_test.go`，函数名与断言保持不变；其它 JSON 测试留原地。如共享辅助被使用，两文件中只能保留一个定义。

新增 `TestSSESplitLineChunkingContract`（第 7 节）：重点保证长单行 `ReadSlice` 返回 `bufio.ErrBufferFull` 时不是一帧结束，CRLF、多 data、无末尾换行的 EOF 行均按原语义处理。

**不做：**替换成 `bufio.Scanner`、给每个连接新建 goroutine、改事件类型集合、改全局失败序列号、为无别名直传路径强加解析。

```sh
go test ./... -run 'Test(Restore|EncodedJSON|RewriteSSE|WriteSSE|StreamSSE|SSE|UnknownSSE)' -count=1
go test ./... -count=1
```

## Task 4：抽取上游响应转发，缩短 handler

**文件：**`server.go`（修改），`upstream_response.go`、`upstream_response_test.go`（拟新增），`server_test.go`（继续保留 HTTP 集成测试）。

拟新增：

```go
// relayUpstreamResponse writes the downstream response but does not close resp.Body.
// serveResponses retains ownership of the upstream body.
func relayUpstreamResponse(w http.ResponseWriter, resp *http.Response,
    aliases *toolNameAliases, extraHeaders []string) {
    // 将既有 serveResponses 中 parsedMediaType 开始至函数结束的分支
    // 原样迁入；唯一调用参数替换是 config.ExtraResponseHeaders → extraHeaders。
}
```

以上表示明确的代码迁移范围，不要求新写一套算法。同时迁移 `parsedMediaType`、`isJSONMediaType`、`flushWriter` 及其 `Write` 到 `upstream_response.go`。

`serveResponses` 尾部精确变为：

```go
resp, err := client.Do(out)
if err != nil {
    writeJSONError(w, http.StatusBadGateway, "upstream request failed")
    return
}
defer resp.Body.Close()
relayUpstreamResponse(w, resp, aliases, config.ExtraResponseHeaders)
```

保留原函数前面的请求读取、envelope 检查、归一化、URL Path 拼接、RawPath/RawQuery/Fragment 清空、请求头与 identity 编码设置。不要同时引入“已编译配置”、新的模型集合、transport interface 或 URL 缓存。

`relayUpstreamResponse` 不返回要求 handler 再输出 JSON 的错误；这避免 SSE 已提交后出现第二个 HTTP 错误正文。现有读写错误处理保持不变，此次不做新日志系统或错误协议。

在 `upstream_response_test.go` 新增 `TestRelayUpstreamResponseDecisionMatrix`，直接构造 `http.Response` + `httptest.ResponseRecorder`，固定第 7 节响应矩阵；为无 Flusher 情况使用仅实现 Header/Write/WriteHeader 的 wrapper。现有真实 httptest 流式集成测试保留，不能只依赖 Recorder 证明时间行为。

```sh
go test ./... -run 'Test(RelayUpstream|Handler|ConfiguredHandler|ConfiguredHandlers|NewHandler|UnknownSuccess|Non2xx|VendorJSON)' -count=1
go test ./... -count=1
```

## Task 5：更新维护地图，最终验证

**文件：**`README.md`；`docs/compatibility/2026-09-24-evidence-register.md`；本方案执行记录。

- README 增加简短源码职责说明，仅列请求归一化、Schema、头部、JSON/SSE 和 HTTP 转发位置；不改产品定位与外部行为。
- 证据登记只更新“当前源码位置”或追加重构后离线验证条目。保留历史故障当时的路径/提交，不能把历史记录全部改成新路径；不增加真实链路 L 证据。
- 删除因迁移留下的重复定义和不用的 imports，不删除兼容入口或测试包装；不改 module、版本标签、examples、部署脚本、LaunchAgent。
- 按第 8 节执行验收；只在全部通过后声称“离线重构完成”，不声称已部署。

# 6. 关键实现说明

## 6.1 HTTP 提交状态只有两种

```text
尚未提交：允许本地 4xx/502；JSON 恢复必须在这里完整成功。
已经提交：只能写协议合法的流数据或结束连接，不能重新设置状态/附加普通 JSON 错误。
```

无需引入公开状态枚举或可变 state machine 对象；用分支顺序保持边界即可。`relayUpstreamResponse` 中 `copyResponseHeaders`/`WriteHeader` 的位置不得提到 JSON 恢复验证之前。

## 6.2 头部三种限制不能混淆

- 配置 extra 禁止：hop-by-hop、所有受控头、已在对应默认列表的头。
- 请求实际复制禁止：hop-by-hop、请求受控头、Connection 点名头。
- 响应实际复制禁止：hop-by-hop、响应受控头、Connection 点名头；允许默认的 Content-Type。

去重仅合并复制算法，不合并上述三个集合。配置层已去重的大小写 header 名仍返回 canonical form；HTTP 多值逐项 `Add`，不能 `Set` 导致丢值。

## 6.3 no-change 的“原样”是字节级

不要把所有请求先 decode 再 encode，即使对象语义相同。以下现有分支必须保留：

```go
if !changed {
    return append([]byte(nil), data...), aliases, nil
}
```

输出仍是输入副本；不要以省分配为理由返回调用方可变缓冲。不要删除 `UseNumber` 或改变 `SetEscapeHTML(false)`。

## 6.4 预算和恢复范围

- Schema 子展开预算与最终合并预算各自存在的理由不变；不要将 `targetBudget`/`siblingBudget` 强行共用。
- JSON 恢复前估算扩张后的大小，编码后再次检查；保留 `json.Number`、转义和无效 UTF-8 的当前处理。
- SSE 最终大小包含 event/id/retry/注释、data 前缀和换行；不能只限制 JSON data。
- 未知 SSE 事件的合法 JSON 不扩大名称恢复范围；有别名时无效 JSON 的当前失败关闭行为不改变。
- nil `*toolNameAliases` 通过 `hasAliases` 方法安全处理，不能提前直接解引用。

## 6.5 所有权与安全

- `NewConfiguredHandler` 的配置 slice 防御性复制和 client 副本保留；不能修改调用方 `client.CheckRedirect`。
- `resp.Body.Close` 始终由 `serveResponses` 持有，relay 不提前 Close，不能把 Body 放入跨请求全局状态。
- 名称映射维持请求级，不能缓存到全局或跨请求复用。
- 不把错误内部文本、请求体、配置值、授权头写入新日志；原固定错误文案和错误码过滤继续生效。

# 7. 测试方案

## 已有回归资产（必须保留）

| 测试文件 | 关键现有测例/责任 |
|---|---|
| `config_test.go` | `TestDecodeConfigDefaultsAndNullAliasOverride`、`TestDecodeConfigRejectsDuplicateKeysAtEveryDepth`、`TestDecodeConfigRejectsInvalidListenersAndUpstreams`、`TestConfiguredHandlersKeepUpstreamsAndModelAllowlistIsolated`、`TestConfiguredHandlerCopiesConfigSlices` |
| `compatibility_test.go` | `TestPassthroughPreservesBytesAndClientChoices`、`TestCompatibilityPoliciesIsolateTransforms`、`TestNormalizeRequestReturnsOriginalBytesWhenPolicyDoesNotTransform`、`TestFunctionToolAlias64RetainsLegacyResult`、`TestNormalizeRequestWithPolicyRecursiveReferenceModes` |
| `schema_test.go` / `schema_refs_test.go` | 直接/间接循环、深度边界、转义 JSON Pointer、缺失/外部引用、相邻等价约束、注解不污染定义、最终合并预算 |
| `tool_names_test.go` | 嵌套/additional_tools、稳定别名、名称冲突 |
| `response_rewriter_test.go` | `TestRestoreIgnoresNonToolMetadataName`、`TestRestoreUsesOnlyRecognizedResponsePaths`、`TestEncodedJSONQuotedStringSizeMatchesEncoder`、输出精确预算及无效 JSON |
| SSE 测试（迁移后 `response_sse_test.go`） | `TestSSEBudgetIncludesEnvelope`、`TestRewriteSSEFramePreservesEOFDataLine`、`TestStreamSSERewritesEOFDataFrame`、终止错误事件 |
| `server_test.go` | `TestHandlerDoesNotFollowUpstreamRedirect`、`TestHandlerStreamsBeforeUpstreamCompletesAndPropagatesCancel`、`TestHandlerFlushesSSEHeadersBeforeFirstEvent`、失败读取/媒体分类/201/连接头 |
| `request_errors_test.go` | `TestNormalizationCodeUsesFixedWrappedCode`，不泄漏任意动态分类 |

## 新增测试的精确输入/断言

### A. `TestHandlerRequestAdmissionContract`（HTTP 层）

统一用 `NewConfiguredHandler(defaultConfig(), fakeClient)`；正常模型用 `museModel`，fake transport 若被调用就递增计数。以下均必须为零上游调用。

| 输入 | 预期 status / error.code |
|---|---|
| `{"model":` | 400 / 无 code |
| `[]` | 400 / 无 code |
| `null` | 400 / 无 code |
| `{"model":17}` | 400 / 无 code |
| `{"model":"not-allowed","tools":[{"parameters":17}]}` | 400 / 无 code，模型准入先于 Schema |
| 仅有大写键 `{"Model":"muse-spark-1.3-contributor"}` | 422 / `invalid_request`，冻结现有两阶段大小写差异，不在重构中修复 |
| 正常请求后追加第二个 JSON 对象 | 400 / 无 code |
| 合法 model、function parameters 为数字 17 | 422 / `invalid_request` |
| 合法 model、引用相邻 type 冲突 | 422 / `schema_ref_sibling_unsupported`，复用现有最小冲突样例 |
| 长度 `maxRequestBytes+1` 的请求体 | 413 / 无 code，避免意外转为 422 |

这些新增预期基于当前源码推导，尚未在新增测试中执行；Task 0 必须先验证。若与实测不符，以实测和已确认契约定位原因，不偷偷改生产逻辑配合表格。

### B. `TestHeaderPolicyContract` / `TestHeaderCopyPreservesValues`

配置方向 request/response 均覆盖：`Content-Type`、`Content-Length`、`Content-Encoding`、`Host`、`Accept-Encoding`、`Connection` 被拒绝；`X-Trace-Id` 与 `x-trace-id` 去重为一个 canonical 名称；空串、空格、CR/LF 头名拒绝；已在各自默认 allowlist 的头不能作为 extra。

运行时分别测试：默认允许头、多值 extra、Connection 和 Proxy-Connection 大小写/多 token 点名、受控头不输出、响应 Content-Type 保留。下面的小测试可直接放进新文件，先在旧实现上运行：

```go
func TestHeaderCopyPreservesValues(t *testing.T) {
    src := make(http.Header)
    src.Set("Content-Type", "application/json")
    src.Set("Content-Encoding", "gzip")
    src.Add("X-Trace-Id", "one")
    src.Add("X-Trace-Id", "two")
    dst := make(http.Header)
    copyResponseHeaders(dst, src, []string{"X-Trace-Id", "Content-Encoding"})
    if dst.Get("Content-Type") != "application/json" || dst.Get("Content-Encoding") != "" {
        t.Fatalf("response policy changed: %v", dst)
    }
    values := dst.Values("X-Trace-Id")
    if len(values) != 2 || values[0] != "one" || values[1] != "two" {
        t.Fatalf("lost multivalue header: %v", values)
    }
}
```

所需 imports 为 `net/http`、`testing`。测试仅用合成 header，不放入真实 Authorization。

### C. `TestRelayUpstreamResponseDecisionMatrix`

aliases 用测试内构造的双向 map；响应 JSON 形状采用 `{"output":[{"type":"function_call","name":"muse_alias"}]}`，toClient 映射至合成原名。

| 上游 | aliases | 断言 |
|---|---|---|
| 201 + application/json | 有 | 201、name 恢复、没有受控长度/编码头 |
| 200 + application/vnd.example+json | 有 | name 恢复 |
| 200 + text/plain / 无效 Content-Type | 有 | 502、上游正文不泄漏 |
| 200 + text/plain | 无 | 200、正文逐字节相同 |
| 429 + text/plain | 有 | 429、正文逐字节相同、Retry-After 保留 |
| 200 + application/json，Body 读取错误 | 有 | 502、不输出已读的半段 JSON |
| 200 + text/event-stream，合法目标事件 | 有 | 名称恢复并 Flush |
| 200 + text/event-stream，注释/未知合法事件 | 无 | 逐字节相同、不解析/重编码 |
| 200 + text/event-stream，下游 writer 不实现 Flusher | 任意 | 正常输出、不 panic |

用自定义记录 writer 额外捕获“JSON 失败前没有提交上游 201”，不能仅检查最后正文。Body Close 测试通过完整 handler 做，不要求 relay 直接调用时关闭 Body。

### D. `TestSSESplitLineChunkingContract`

- 输入一个 JSON SSE data 行，其中字符串长度 8 KiB（超过默认 reader buffer），别名在目标 function_call 的 name 中：只输出一帧、名称恢复、无伪空帧。
- 同一事件使用 CRLF：保留非 data 行和原有换行规则，不重复事件。
- 多个 data 行构成一个合法 JSON：按现有拼接规则恢复；不把每行当独立 JSON。
- 最后一行无换行/无空白结束符：EOF 时仍写出最后一帧。
- malformed 事件后紧跟 `[DONE]`：输出一次 `response.failed`，后面的 `[DONE]` 不输出。
- 下游 writer 返回短写且无 error：`writeSSEFrame` 返回 `io.ErrShortWrite`；不继续 flush 或补成功帧。

已有等价测例可扩展 table cases，而非新增重复测试。真实时间/取消断言继续由原 HTTP 集成测试负责。

### E. 策略与迁移验证

保留 `TestBuiltInCompatibilityPolicies` 对字符串字面值和默认 64 的断言；配置测试继续覆盖 null/省略/0 区别。测试移动前后保留每一个原有 Test 名称；允许新增，不允许以移动为名删除/Skip/缩减已有断言。无前端、数据库或持久化状态迁移，相关测试不适用。

# 8. 验收标准

## 可执行命令

在仓库根目录运行。构建必须使用临时目录，避免覆盖未知或运行中的二进制：

```sh
git diff --check
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
build_dir=$(mktemp -d)
go build -trimpath -o "$build_dir/responses-compat" .
```

检查 build 退出码和产物存在；这个命令不启动服务。临时产物可留作本次验收证据，之后仅清理明确由本次创建的目录。

## Checklist（以下为实施后的验收，当前未勾选）

- [x] 4 个新增生产文件各自负责明确边界，所有生产文件仍为 `package main`。
- [x] 新增刻画测试在迁移前后均通过；原有 Test 名称全部保留，无新增 Skip。
- [x] 400/413/422/502、非 2xx 原样正文和成功 JSON 状态码保持一致。
- [x] passthrough 与 no-change 字节相等；现有 64 字节别名结果相等。
- [x] 默认/extra/dynamic Connection 头部规则矩阵通过，无新增受控头泄漏。
- [x] JSON 失败仍在响应提交前；SSE 首事件前 Flush、流式输出和取消传播测试通过。
- [x] SSE 完整帧预算、未知事件路径、EOF、终止 response.failed 测试通过。
- [x] `resp.Body` 全部分支关闭，调用方配置/client 不被修改；多 handler 隔离测试通过。
- [x] 普通测试、race、vet、临时构建和 diff 检查均通过。
- [x] `go.mod`、examples、启动参数/默认值、部署文件、运行中的服务未改变；无新依赖。
- [x] README 当前代码地图与实际一致；历史证据未篡改，不声称真实上游兼容已验证。

不设人为覆盖率阈值，也不把“文件变短”或“146 项数量没变”单独当作正确性证明。新测试加入后通过项数应增长或因表结构改变而变化；关键是旧断言保留及新边界得到实际验证。

# 9. 风险与注意事项

1. **兼容风险最高：**调整提交顺序、请求解码规则、header 控制集合、Schema 合并或工具名匹配路径。为此先冻结行为，再机械迁移，不混做优化。
2. **新功能不在范围：**不新增 provider adapter、自动重试、缓存、动态配置、认证、metrics/OTel、SDK、UI、数据库；不改生成策略和协议字段。
3. **已知但不顺手处理：**请求重复解码、配置重复验证、SSE I/O 错误缺少额外诊断、函数签名风格、全局错误事件序列号。它们可另行评估，不能为本轮纯重构扩大行为差异。
4. **参考文档有时间性：**历史更名与修复已经完成，不重复执行旧实施计划中的部署、改名、标签步骤。
5. **图谱局限：**已有记录未覆盖被忽略项，且含部分启发式边；不据此删除“零调用”函数，尤其保留测试和兼容包装。实施后图谱会变旧；获准实施包含必要索引刷新时再更新，或用源码验证补足。
6. **回退：**每个任务作为可独立审查的变更单元；没有提交授权不自动提交。已提交的重构可在确认无并行冲突后 revert 对应提交；未提交变更按任务 diff 精确撤回，不 reset 工作区。此方案不改变持久化数据，无需数据迁移。
7. **发布门：**所有检查都是离线；不调用真实上游模型、读取真实日志/凭据或切换 LaunchAgent。重构验收不关闭证据登记中仍待真实链路验证的问题。

# 10. Luna 执行清单

| 顺序 | 修改目标 | 明确完成条件 | 独立验证 |
|---|---|---|---|
| 0 | 基线；`server_test.go`、`headers_test.go` | 新刻画测试在原实现上通过，工作区差异已核实 | Task 0 聚焦 + 全量测试 |
| 1 | `headers.go`；移出 config/server 的头部规则 | 单一规则归属，方向差异和多值头保持 | Header/Config/Connection 测试 |
| 2 | `compatibility.go`、`request_normalizer.go`、`schema.go` | 常量集中、入口签名/字节/变换顺序不变 | Normalize/Policy/RefSibling/别名测试 |
| 3 | `response_sse.go` 与对应测试 | SSE/JSON 边界分离，长行/EOF/终止行为不变 | SSE + JSON 大小/恢复测试 |
| 4 | `upstream_response.go`；`server.go` | 提交前后边界明确，Body 所有权不变 | relay 矩阵 + 真实本地 HTTP 流式测试 |
| 5 | README 与证据登记 | 当前源码地图准确、历史事实不改写 | 文档路径核查、diff 自审 |
| 6 | 全项目 | 所有验收项得到实际证据，业务范围无扩张 | test/race/vet/build/diff 全套 |

- [x] 执行前确认获得“实施重构”授权；本文件自身不是授权。
- [x] 按上表顺序完成，不同时重排文件、修改行为、更新工具链或引入依赖。
- [x] 完成后报告实际改动文件、各命令结果、未验证的真实上游行为；只称离线重构完成。


## 本次执行记录（2026-09-24）

- Worktree：`/Users/hrygo/.codex/worktrees/responses-compat-refactor/responses-compat`；分支 `codex/responses-compat-refactor`；起始 HEAD `3d60f95`（包含本计划文档的提交）。起始工作区干净；相较文档分析时记录的源码提交，仅 HEAD 先包含执行计划文档，保留该提交并未重写历史。
- 新增生产边界：`headers.go`、`request_normalizer.go`、`response_sse.go`、`upstream_response.go`；保留单一 `package main`、Go 标准库及原公开/兼容入口。未改 `go.mod`、示例、启动参数/默认值或部署文件。
- 新增契约覆盖：请求准入和零上游调用、请求/响应头策略与多值复制、JSON 201 状态、上游 Body 关闭、SSE 长行/CRLF/多 data/EOF/短写，以及上游响应决策矩阵。测试迁移后原有 88 个 Test 函数名称全部保留；当前共 98 个 Test 函数。
- 最终离线验证（Go 1.27.1 darwin/arm64）：聚焦测试通过；`go test ./... -count=1` 通过；`go test -race ./... -count=1` 报告 183 passed；`go vet ./...` 通过；`go build -trimpath` 输出到本次创建并随后清理的临时目录，成功且产物非空；`git diff --check` 通过。
- 仅为离线重构与验证；未调用真实上游、未读真实凭据/日志、未切换或发布运行服务。因此真实 Muse/OpenCode Go 上游契约及运行态仍未验证。
