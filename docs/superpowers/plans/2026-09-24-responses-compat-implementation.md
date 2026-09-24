# Responses Compat 落地实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> 本环境可用技能名为 `executing-plans`；不为技能别名自动安装工具。推荐主会话顺序执行，未获明确委派授权时不启动子代理。

**Goal:** 先修复已确认的 Adapter 422，再将必要兼容规则做成可配置、可关闭、可验证的 Responses Compat，最后独立完成更名和授权后的部署。

**Architecture:** 单实例、单固定上游，CLIProxyAPI 继续负责账号和路由。Go 标准库完成配置、HTTP 和有界 JSON/SSE 处理；passthrough 不变换正文，muse 保留旧策略选择但修复已确认缺陷。

**Tech Stack:** 现有 Go 1.27 module，标准库、testing、httptest；不新增 OpenCode/AI SDK/Effect 依赖。

**Spec:** `docs/superpowers/specs/2026-09-24-responses-compatibility-proxy-design.md`

**Evidence:** `docs/compatibility/2026-09-24-evidence-register.md`

**Date / status:** 2026-09-24；Task 1–6 离线工作已提交并验证。代码提交 a1085497b2184c4c28e184d5a4e94888ac83d8f9 已 fast-forward 到本地 main；其后的最终证据记录仅改文档。146 项全量/race 测试和 vet 均通过，清洁提交构建已完成。没有配置 Git remote，故未推送/发布；没有真实上游请求或服务切换；v0.1.0 标签不动。

## Global Constraints

- 原项目/服务身份为 muse-codex-adapter；代码、module、命令与 README 已使用 Responses Compat / responses-compat。工作目录已按用户指示迁移至 `~/Documents/responses-compat`；运行中的旧 LaunchAgent 尚未切换。
- 不修改 v0.1.0 标签；v0.2.0 是候选目标，不预先打标签或宣称发布。
- 保留默认 127.0.0.1:18317、Muse 模型和上游地址；不更改默认 64 字节策略的 `muse_` 别名结果。
- 32 MiB 请求/展开预算、Schema 深度 64、64 MiB JSON 和完整 SSE 帧上限。
- 不伪造客户端身份，不新增 strict/store 的强制改写，不清洗任意对象 ID；不重试生成、不跨上游切换。
- previous_response_id 不新增一律拒绝；仅声明完整工具上下文无状态重放的已测范围，未知历史映射不假装可恢复。
- 已保留并整合用户此前要求的 README 中文链路与说明；任何新出现的并行编辑先核实，不能整体回滚或覆盖。
- 真实日志/原请求不进入仓库；只使用合成 Schema 和伪凭据。测试不能依赖个人目录、真实模型或参考仓库存在。
- OpenCode 依据固定为 v1.18.32 / 545f51d26cc39a907d2867492d498d9607ea5fa4；借鉴证据，不拷贝其实现或依赖。
- 任务 1–6 只产生代码、测试、文档和离线候选构建。本地目录更名已获用户明确指示并完成；任务 7 的真实请求、安装/服务切换及 GitHub 仓库目标/发布仍须按各自边界确认。

## 综合判断与最终落地决策（2026-09-24）

### 根因结论：先归因到具体边界，不给协议两端整体定罪

- 已确认的 422 发生在 Responses Compat 当前 Adapter 的请求归一化阶段：本次 `$ref` 与相同 `type` 相邻时，被本地 `expandSchemaNode` 规则一律拒绝。CLIProxyAPI 转发前后该工具参数结构相同；因此这个具体故障是 Adapter 的误拒绝，不是 Codex 非标准请求或 Muse 上游拒绝的证据。
- 修复后本地归一化通过，只能证明 Adapter 不再误拒绝该结构；不能证明 Muse 上游接受，更不能证明 Codex、OpenCode、CLIProxyAPI 与全部 Responses 服务之间不存在其他差异。
- 对其他失败必须按请求在客户端、CLIProxyAPI、Adapter 和上游各边界的实际观测重新归因。公开规范、源码实现及离线纯函数测试用于提出假设，不替代真实同请求链路证据。

### OpenCode 的结论：可作客户端对照，不是上游兼容认证

- 固定版本源码显示，OpenCode CLI 与 App 共用 OpenCode 服务端实现；本机配置采用 `@ai-sdk/openai` 的 Responses provider 路由及 OpenCode Go 基址。这说明可以把 OpenCode CLI 配置为 Muse Spark 的直接客户端对照；若需要验证 App 打包/配置注入差异，再单独覆盖 App。
- 这些证据没有证明本机曾成功向 Muse Spark 发出真实请求，也没有证明同一账号、模型权限、工具、递归 Schema、SSE 或多轮 Responses 能力。因此“OpenCode 能否直连 Muse Spark”保持待真实对照验证；不能仅凭配置或 SDK 行为宣称可用。
- 不把 OpenCode 的请求清理逻辑整体移植到代理，也不把 OpenCode 请求当作 Codex 的唯一合规基准。对照的目的在于隔离客户端行为与中间层行为。

### 产品范围：通用化为可配置兼容层，不扩张为通用 API Gateway

- 对外定位仍为 **Responses Compat（Responses API 兼容性代理）**：面向不同 Responses 客户端、代理和上游之间有证据支持的实现差异。
- 首阶段仍是本机单实例、单固定上游、精确模型白名单、单一显式策略；“更通用”指策略可配置、可关闭、便于增加经过验证的 provider 配置，不代表任意 OpenAI-compatible 认证、协议互转、账号池、动态路由或多租户网关。
- 无配置启动保留既有 Muse 升级兼容预设；新建/新上游配置必须明确选择 profile，并从 `passthrough` 开始，只在对照证明必要后单独启用规则。无变换时保留请求原始字节；不得顺带改写 `strict`、`store`、`previous_response_id`、未知字段或客户端身份。
- 若真实对照显示直连与 passthrough 均成功，非必要变换保持关闭；若仅特定规则改善某个明确失败，记录适用范围、语义损失和撤销条件，不据单次成功宣称通用兼容。

### 最终执行顺序与当前状态

1. **已完成并提交：** Task 1 修复 Schema 引用与相邻字段的误拒绝；Task 2 限定工具名恢复范围并把完整 SSE 帧计入预算。提交 a7ce48f、8a577c6。
2. **已实现并提交：** Task 3 保留有价值的 compatibility_test.go 并完成策略隔离，提交 27fefed；Task 4–5 完成严格配置、实例隔离和 HTTP 边界，因共用 handler 文件合并提交 7b4d855。
3. **离线候选：** Task 6 已提交 a108549，并在清洁工作区完成验证构建；二进制仍是离线候选，不是已安装/已发布版本。
4. **未执行/待条件：** 代码已合并到本地 main；工作目录已迁移至 `~/Documents/responses-compat`。没有配置 remote，未推送/发布/打标签，未调用真实上游、未安装或切换服务。旧 `v0.1.0` 保留；`v0.2.0` 仍待真实对照和 GitHub 目标确认。

## Review Focus

1. `$ref` 相邻约束：重复类型和注解应通过，不同断言不能被浅覆盖或静默删除。任务 1 覆盖。
2. 普通 name 恰好等于别名：不能误改 metadata、用户文本或参数；预估大小与实际改写范围必须一致。任务 2 覆盖。
3. 原始字节与配置覆盖：显式 null 应禁用名称适配，缺省则继承预设；passthrough 不重编码。任务 3–4 覆盖。
4. 边界串联：JSON 未超限但 SSE 封装后超限、成功未知媒体类型、Connection 指定的头、取消后的连接释放。任务 2、5 覆盖。
5. 更名与真实性：旧服务二进制是 dirty 构建，不能用 Git 标签冒充运行态备份；配置直连不等于真实调用已验证。任务 6–7 覆盖。

## 文件职责与接口契约

### 保留并修改

- `schema.go`：请求解析及 Schema 处理，不承担路由发现。
- `tool_names.go`：请求内确定性别名与冲突检测。
- `response_rewriter.go`：已识别工具位置恢复、JSON/SSE 输出预算。
- `server.go`：单上游 HTTP 生命周期、白名单头、错误状态。
- `main.go`：读取配置后启动，不管理其他进程。
- 对应现有 `_test.go`：保留回归意图，逐条说明需要调整的旧断言。
- `go.mod`、`README.md`、`CHANGELOG.md`：最后才做命名和交付说明。

### 新建

- `schema_refs.go`、`schema_refs_test.go`：范围受控的引用相邻字段处理及回归。
- `request_errors.go`、`request_errors_test.go`：固定错误类别，不携带私人载荷。
- `compatibility.go`、`compatibility_test.go`：策略及预设。
- `config.go`、`config_test.go`：严格 JSON 配置、预设覆盖、启动校验。
- `examples/muse.json`、`examples/muse-passthrough.json`：无凭据配置；后者仅用于诊断，不声明已可替代 Muse 预设。
- `docs/deployment.md`：授权后的迁移和回退清单。

以下是任务间约定，不是要求先生成空实现：

```go
// compatibility.go
// ToolNameMaxBytes == 0 disables aliases; external JSON uses null.
type CompatibilityPolicy struct {
    SchemaRefs       string
    RecursiveRefs    string
    ToolNameMaxBytes  int
    ReasoningIDs     string
}
func musePolicy() CompatibilityPolicy
func passthroughPolicy() CompatibilityPolicy

// schema.go: wrappers preserve existing tests/callers.
func NormalizeRequest(data []byte) ([]byte, error)
func normalizeRequestWithToolNames(data []byte) ([]byte, *toolNameAliases, error)
func normalizeRequestWithPolicy(data []byte, models []string, policy CompatibilityPolicy) ([]byte, *toolNameAliases, error)

// config.go
// Import net/url. Copy slices when handing configuration to handlers.
type RuntimeConfig struct {
    Listen               string
    Upstream             *url.URL
    Models               []string
    Policy               CompatibilityPolicy
    ExtraRequestHeaders  []string
    ExtraResponseHeaders []string
}
func defaultConfig() RuntimeConfig
func decodeConfig(data []byte) (RuntimeConfig, error)
func loadConfig(path string) (RuntimeConfig, error)

// server.go: existing constructor delegates to default policy.
func NewHandler(upstream *url.URL, client *http.Client) http.Handler
func NewConfiguredHandler(config RuntimeConfig, client *http.Client) http.Handler

// request_errors.go: Code and Message come from a finite internal list.
type normalizationError struct { Code string; Message string }
func (e *normalizationError) Error() string
func normalizationCode(err error) string
```

解析错误、模型不允许、大小超限和适配失败仍分别映射到现有 400/400/413/422 类别；未知内部错误不回显内容。归一化错误新增固定 `error.code`，保留已有 `error.message` / `error.type` 结构。上游非 2xx 不替换为本地错误。

---

## Task 1：先修已确认的 422，与通用化解耦

**Files:** 新建 schema_refs.go/schema_refs_test.go/request_errors.go/request_errors_test.go；修改 schema.go、schema_test.go、server.go、server_test.go。

**Consumes:** 当前 NormalizeRequest、expandSchemaNode、schemaBudget、NewHandler。

**Produces:** 本次相邻字段正常归一化，受控错误码；不改变监听或模型选择。

- [x] **1.1 保护现场并建立基线。** 检查 `git status --short`、README diff 和运行态元数据记录；在开始代码实施时创建 `codex/responses-compat` 分支。已有同名分支先检查，不覆盖。运行并记录：

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
```

- [x] **1.2 先加入真实缺陷的合成回归。** 在 schema_refs_test.go 写入以下测试；它在当前版本必须因 unsupported siblings 失败。

```go
package main

import (
    "strings"
    "testing"
)

func TestRefSiblingRedundantTypeAndDescription(t *testing.T) {
    body := []byte(`{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"example","parameters":{"type":"object","properties":{"value":{"$ref":"#/$defs/Text","type":"string","description":"synthetic annotation"}},"$defs":{"Text":{"type":"string"}}}}]}`)
    got, err := NormalizeRequest(body)
    if err != nil { t.Fatal(err) }
    if strings.Contains(string(got), `"$ref"`) { t.Fatal("reference not expanded") }
    if !strings.Contains(string(got), `"description":"synthetic annotation"`) { t.Fatal("annotation lost") }
    if !strings.Contains(string(got), `"type":"string"`) { t.Fatal("type lost") }
}

func TestRefSiblingConflictingTypeIsNotSilentlyOverwritten(t *testing.T) {
    body := []byte(`{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"example","parameters":{"$ref":"#/$defs/Text","type":"number","$defs":{"Text":{"type":"string"}}}}]}`)
    if _, err := NormalizeRequest(body); err == nil { t.Fatal("unsupported constraint combination accepted") }
}
```

- [x] **1.3 确认红灯。** 执行 `go test ./... -run 'TestRefSibling' -count=1`，记录第一项失败；不以第二项已通过代替红灯证明。
- [x] **1.4 修复引用相邻字段。** 保留当前 root 解析、深度和循环检测；展开目标后，只接受与目标完全同值的重复断言及 description/title/$comment 字符串注解。注解由相邻节点优先；不要修改引用目标原对象。其他新增/冲突断言返回固定的 schema_ref_sibling_unsupported，不误称其为非法 JSON Schema。实现中的判定应明确使用相等检查，而非对象浅合并：

```go
// After the target and sibling values are normalized, both decoded with UseNumber:
if existing, ok := target[key]; ok && reflect.DeepEqual(existing, value) {
    continue // duplicate assertion: no new output and no semantic change
}
// For description/title/$comment, validate string and add/update with budget accounting.
// Other unmatched assertions return the fixed error below.
return nil, &normalizationError{
    Code: "schema_ref_sibling_unsupported",
    Message: "unsupported constraint beside schema reference",
}
```

本段为分支逻辑；target/key/value 来自展开目标与相邻字段遍历。不得把“重复检查通过”扩展为任意相邻约束安全可合并。按最终输出计入新增注解和包围结构预算；超预算返回错误，不先创建无界序列化缓冲。

- [x] **1.5 错误分类与旧测试。** normalizationCode 使用 errors.As 提取固定 Code，未知情况返回 invalid_request；server.go 的本地 422 增加 code，但不拼接原始工具名、JSON Pointer、URL 或载荷。拆开 TestNormalizeRequestRejectsMissingExternalAndSiblingRefs：缺失和外部引用仍拒绝；有效目标加注解则保留。HTTP 测试必须断言本次请求到达 httptest 上游且不再返回 422，冲突请求仍拒绝且错误无载荷。
- [x] **1.6 绿灯并独立提交。** 运行回归、全量、race 和 vet；补测注解计入大小预算、输入定义未变。提交 `fix: accept supported schema reference siblings`，不要夹带更名或服务操作。

## Task 2：修正响应恢复范围和完整 SSE 预算

**Files:** response_rewriter.go、response_rewriter_test.go；server_test.go 仅更新既有 SSE 压力测试的 response.completed 事件包装。

**Consumes/Produces:** 保留 restoreToolNamesInJSON、rewriteSSEFrame、streamSSEWithToolNameRestore 签名；改变误改范围与完整帧超限行为。

- [x] **2.1 写反例并确认当前红灯。** 在 response_rewriter_test.go 增加以下测试；补入 errors import。

```go
func TestRestoreIgnoresNonToolMetadataName(t *testing.T) {
    body := []byte(`{"metadata":{"name":"alias"},"output":[{"type":"function_call","name":"alias","arguments":"{\"name\":\"alias\"}"}]}`)
    got, changed, err := restoreToolNamesInJSON(body, map[string]string{"alias":"original"}, maxResponseRewriteBytes)
    if err != nil || !changed { t.Fatalf("changed=%v err=%v", changed, err) }
    if !bytes.Contains(got, []byte(`"metadata":{"name":"alias"}`)) { t.Fatal("metadata changed") }
    if !bytes.Contains(got, []byte(`"arguments":"{\"name\":\"alias\"}"`)) { t.Fatal("arguments changed") }
    if !bytes.Contains(got, []byte(`"name":"original"`)) { t.Fatal("tool name not restored") }
}

func TestSSEBudgetIncludesEnvelope(t *testing.T) {
    payload := []byte(`{"type":"response.output_item.done","item":{"type":"function_call","name":"a"}}`)
    aliases := map[string]string{"a":strings.Repeat("x", 256)}
    rewritten, _, err := restoreToolNamesInJSON(payload, aliases, 4096)
    if err != nil { t.Fatal(err) }
    frame := []byte("event: response.output_item.done\ndata: " + string(payload) + "\n\n")
    _, _, err = rewriteSSEFrame(frame, aliases, len(rewritten))
    if !errors.Is(err, errResponseRewriteLimit) { t.Fatalf("expected full-frame limit, got %v", err) }
}
```

- [x] **2.2 统一语义范围。** 用同一套已识别路径供大小预估和实际改写使用：根 function_call；response output 中的 function_call；response tools/namespace 中的 function 定义；output_item.added/done 的 item；function_call_arguments.delta/done 的顶层 name。明确 response.created/in_progress/completed/incomplete/failed 的 response 包装。不能深入 metadata 或 arguments；未知事件默认保持，不添加正则文本替换。
- [x] **2.3 完整帧预算。** 在构造新帧前计算保留的 event/id/comment 行、data 前缀、改写 JSON 及行尾总长度；超限返回 errResponseRewriteLimit。不要仅给 JSON 64 MiB，再在其外无界附加封装。保留原始帧上限。
- [x] **2.4 验证完整矩阵。** 单独断言 LF/CRLF、多行 data、EOF 尾帧、无工具映射原样返回、完整帧恰好上限通过/多一字节拒绝。保留 response.failed 终止且不继续 [DONE] 的现有测试；检查测试夹具不能因收窄路径而意外失去扩展压力。
- [x] **2.5 绿灯和提交。** `go test ./... -run 'TestRestore|TestRewrite|TestSSE|TestStream|TestWrite' -count=1` 后运行全量/race/vet；提交 `fix: scope tool name restoration and bound full SSE frames`。

## Task 3：引入最小策略，不搬运客户端行为

**Files:** compatibility.go、compatibility_test.go、schema.go、tool_names.go、tool_names_test.go。

**Consumes:** 任务 1 的 Schema 行为；保留旧 normalizer/alias 函数包装。

**Produces:** 文首 CompatibilityPolicy、musePolicy、passthroughPolicy、normalizeRequestWithPolicy。

- [x] **3.1 用测试规定透传和策略隔离。** 新建并保留 `compatibility_test.go`；覆盖原始字节、策略隔离和递归引用模式。

```go
package main

import (
    "bytes"
    "testing"
)

func TestPassthroughPreservesBytesAndClientChoices(t *testing.T) {
    body := []byte("{\n  \"model\": \"example-model\", \"store\": true, \"previous_response_id\": \"synthetic\", \"input\": [{\"type\":\"reasoning\",\"id\":\"r1\"}], \"tools\": [{\"type\":\"function\",\"name\":\"f\",\"strict\":true,\"parameters\":{\"$ref\":\"#/$defs/Text\",\"$defs\":{\"Text\":{\"type\":\"string\"}}}}]\n}")
    got, aliases, err := normalizeRequestWithPolicy(body, []string{"example-model"}, passthroughPolicy())
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(got, body) || aliases.hasAliases() { t.Fatal("passthrough transformed the request") }
}
```

- [x] **3.2 实现预设并接入现有算法。** 不修改 strict/store，不新增 previous_response_id 拒绝。旧入口包装 Muse 白名单及 `musePolicy`；新 normalizer 使用精确白名单，只在实际变换时编码并保持 `UseNumber`。

```go
func musePolicy() CompatibilityPolicy {
    return CompatibilityPolicy{"inline", "empty_schema", 64, "drop"}
}
func passthroughPolicy() CompatibilityPolicy {
    return CompatibilityPolicy{"preserve", "reject", 0, "preserve"}
}
```

- [x] **3.3 可配置别名。** 接收 `maxBytes`，0 禁用，64 字节策略保留原别名；检查请求内原名称/别名冲突，无跨请求缓存。
- [x] **3.4 补充表驱动策略断言。** 覆盖策略隔离、精确模型白名单、原字节、别名长度阈值和哈希兼容，以及递归引用模式。
- [x] **3.5 绿灯和提交。** 定向及全量/race/vet 验证通过；提交 27fefed feat: isolate compatibility policies and add passthrough。

## Task 4：配置读取与进程接线

**Files:** config.go、config_test.go、main.go、server.go。

**Consumes:** 任务 3 的 policy normalizer。

**Produces:** RuntimeConfig、defaultConfig、decodeConfig、loadConfig、NewConfiguredHandler；旧 NewHandler 包装新入口，保持测试和默认行为。

- [x] **4.1 写配置核心反例。** `config_test.go` 覆盖显式 `null`、重复键和无效配置。

```go
package main

import "testing"

func TestConfigExplicitNullDisablesAlias(t *testing.T) {
    raw := []byte(`{"config_version":1,"listen":"127.0.0.1:18317","upstream_base_url":"https://opencode.ai/zen/go/v1","models":["muse-spark-1.3-contributor"],"profile":"muse","transforms":{"tool_name_max_bytes":null}}`)
    cfg, err := decodeConfig(raw)
    if err != nil { t.Fatal(err) }
    if cfg.Policy.ToolNameMaxBytes != 0 { t.Fatal("null did not disable aliases") }
}

func TestConfigRejectsDuplicateKey(t *testing.T) {
    raw := []byte(`{"config_version":1,"profile":"muse","profile":"passthrough"}`)
    if _, err := decodeConfig(raw); err == nil { t.Fatal("duplicate key accepted") }
}
```

- [x] **4.2 严格解码。** 递归检查重复键、拒绝未知键/尾随数据；区分缺省、`null` 与数值覆盖。
- [x] **4.3 启动校验。** 校验版本、模型、profile/policy、loopback、固定上游 URL 和安全头名单，不发网络请求。
- [x] **4.4 接入入口。** 标准 flag 解析 `--config`；加载失败早于服务器启动；配置切片复制；保留请求 Context 和现有超时/退出策略。
- [x] **4.5 验证矩阵。** 覆盖重复/未知键、监听/上游非法值、头名单、模型路由隔离、配置不可变和缺失 `--config` 不启动。
- [x] **4.6 绿灯和提交。** 全量/race/vet 通过；与 Task 5 合并为原子提交 7b4d855 feat: configure and harden Responses proxy instances，原因是配置 handler 与传输策略共用 server.go。

## Task 5：HTTP 传输边界与媒体类型失败关闭

**Files:** server.go、server_test.go；必要的 header 校验辅助逻辑放 config.go，不新建框架。

**Consumes:** NewConfiguredHandler 和配置头白名单；任务 2 的响应恢复。

**Produces:** 有名称映射时未知成功媒体类型 502；受控头不泄漏；原非2xx/取消/SSE行为有测试证明。

- [x] **5.1 写最小失败用例。** 覆盖未知媒体类型失败关闭与 `Connection` 动态指定头过滤。

```go
func TestUnknownSuccessMediaWithAliasesFailsClosed(t *testing.T) {
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("X-Opencode-Session") != "" { t.Error("hop-by-hop nominated header leaked") }
        w.Header().Set("Content-Type", "text/plain")
        _, _ = io.WriteString(w, "synthetic upstream text")
    }))
    defer upstream.Close()
    base, err := url.Parse(upstream.URL + "/v1")
    if err != nil { t.Fatal(err) }
    front := httptest.NewServer(NewHandler(base, upstream.Client()))
    defer front.Close()
    body := `{"model":"muse-spark-1.3-contributor","tools":[{"type":"function","name":"` + strings.Repeat("x", 65) + `","parameters":{"type":"object"}}]}`
    req, err := http.NewRequest(http.MethodPost, front.URL+"/v1/responses", strings.NewReader(body))
    if err != nil { t.Fatal(err) }
    req.Header.Set("Connection", "X-Opencode-Session")
    req.Header.Set("X-Opencode-Session", "synthetic-session")
    resp, err := front.Client().Do(req)
    if err != nil { t.Fatal(err) }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusBadGateway { t.Fatalf("status=%d", resp.StatusCode) }
}
```

- [x] **5.2 实现分支。** 严格解析 MIME 类型；未知成功类型且需恢复别名时在响应头提交前返回 502；无别名和非 2xx 仍按原边界转发。
- [x] **5.3 头与生命周期。** 固定/附加头 allowlist 排除逐跳、`Connection` 指定头和受控长度/编码头；禁止重定向，已有取消测试以同步 channel/超时验证。
- [x] **5.4 错误可观测性。** 仅返回固定错误，不新增正文日志或输入载荷诊断；SSE 终止错误使用 Responses Compat 标识。
- [x] **5.5 绿灯和提交。** 全量/race/vet 通过；与 Task 4 共用提交 7b4d855，保留上游非 2xx、200 业务错误、首事件前 flush 和取消测试。

## Task 6：最后更名，交付离线候选版本

**Files:** go.mod、main.go、response_rewriter.go、README.md、CHANGELOG.md、examples/muse.json、examples/muse-passthrough.json、docs/deployment.md、docs/compatibility/2026-09-24-evidence-register.md。

**Consumes:** 任务1–5全部通过。

**Produces:** responses-compat 源码/二进制身份与无凭据文档；不更名工作目录、不安装、不重启、不打标签。

- [x] **6.1 更名前验证。** 已复核并保留原 README 中文链路内容；搜索旧品牌引用，保留协议别名、模型 ID、历史版本与旧服务标识。
- [x] **6.2 更新身份。** go.mod、CLI/log 前缀及构建说明已使用 responses-compat；CHANGELOG 以 Unreleased 记录，保留 v0.1.0 历史且不宣称真实兼容通过。
- [x] **6.3 添加配置示例。** 两份无凭据示例与预期 profile/listen 一致，并由自动测试解析验证。
- [x] **6.4 README/部署文档。** 记录可配置机制、默认有损行为、未验证边界和授权后的备份/切换/验证/回退流程。
- [x] **6.5 全量验证后提交并构建。** 提交 a108549 后执行全量、race、vet 和清洁工作区构建，均通过。

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go list -m
git rev-parse HEAD
ARTIFACT_DIR=$(mktemp -d)
go build -trimpath -o "$ARTIFACT_DIR/responses-compat" .
go version -m "$ARTIFACT_DIR/responses-compat"
shasum -a 256 "$ARTIFACT_DIR/responses-compat"
```

实测：module responses-compat；Go 1.27.1 darwin/arm64；source commit a1085497b2184c4c28e184d5a4e94888ac83d8f9；vcs.modified=false；SHA-256 c7ad523b7f3758310a8975c54131d4cb49c30f0a76db402aeda43156084327f6。候选构建不是已安装版本，不能写成服务已更新。

## Task 7：授权后再做真实对照、服务迁移和发布

**Files:** 只有获得授权且产生实际结果后，新增 docs/validation/v0.2.0.md；不提前生成假结果。

**Consumes:** 已验证的候选构建、主方案和证据登记；需要明确真实请求/安装/发布范围。

**Produces:** 三者分开：上游兼容结论、运行态部署结果、远端发布结果。任一缺少条件不得笼统称“完成发布”。

- [ ] **7.1 停在授权门。** 确认真实模型调用数量/费用范围、是否允许读取所需本机凭据、是否安装新服务、远端发布目标。凭据不进入命令参数、日志、仓库或报告。授权前只做前六项，不以方案批准代替运行态批准。
- [ ] **7.2 建立分层对照，而不是先证明 Adapter 或某客户端必需。** 在同一模型/账号权限条件下使用最小合成请求，分别记录：E0 原始 HTTP 直连上游，用于隔离上游契约；E1 OpenCode CLI 直连 Muse，用于验证其 provider/SDK 请求路径；仅当需确认桌面配置注入或打包差异时增加 E1a OpenCode App 直连；E2 目标客户端经 CLIProxyAPI 但不经过 Adapter；E3 经 CLIProxyAPI + Adapter passthrough；E4 在 E3 基础上每次只启用一条兼容规则。请求结构、模型权限、版本和错误边界分开记录，不把不同客户端的请求当作字节级同一请求，也不在原生产路由上直接改动，优先使用隔离端口/合成请求。以上真实调用均须先取得对应授权。
- [ ] **7.3 五类用例。** 普通文本；本次 ref+同type；递归工具；超长工具名；无外部副作用的echo工具完整多轮+SSE。记录每类在哪个边界失败、错误类别及单规则前后结果；不复制真实会话来省事。失败不无限重试，未完成上游验证的能力继续标“未验证”。
- [ ] **7.4 按证据裁剪规则。** E0 成功只证明该合成 HTTP 请求被上游接受；E1 成功只证明 OpenCode CLI 对该用例可直连，不能替代目标客户端结果。若目标客户端 E2 成功而 E3 失败，归因并修复 Adapter 透传路径；若 E3 成功而单条 E4 失败，关闭或修复该条规则；若 E2/E3 无需变换均成功，则对应变换保持关闭。只有请求到达上游且有可归因响应，才评估上游行为；任何单次结果都不能宣称全功能兼容。必要规则需记录适用范围、语义损失和撤销条件。
- [ ] **7.5 服务切换。** 核实旧运行进程的实际可执行文件，备份当前二进制与 plist 并记录校验值；历史排查中为 fdd2415+dirty，部署时重新核实；实际运行构建不能由 Git 标签或历史记录替代。准备新二进制和 `com.hrygo.responses-compat`，停旧后启新，不能争用 18317。当前只确认旧 LaunchAgent `com.hrygo.muse-codex-adapter` 仍已加载、可执行文件名为 `muse-codex-adapter`，其命令参数不引用项目工作目录；未读取环境变量、未重启或切换服务。服务改名/切换仍须单独授权。
- [x] **7.6a 本地目录更名。** 已核实仅当前任务使用该工作目录、Git worktree 仅有本地 `main`、目标目录原先不存在；LaunchAgent 命令参数不引用旧目录。按用户指示完成 `~/Documents/muse-codex-adapter` → `~/Documents/responses-compat`。运行中的旧 LaunchAgent 未切换。
- [ ] **7.6b GitHub 仓库与发布。** 当前无 Git remote；已在已连接 GitHub 账号下搜索 `responses-compat`，未找到目标仓库。用户要求提交 GitHub，但仓库是已有还是需新建及其公开/私有可见性尚未确定；不猜测创建。确认精确目标后再推送；真实验收和版本门槛满足前不打 `v0.2.0` 标签，不移动 `v0.1.0`。
- [ ] **7.7 最终报告。** 分别列出源码提交、构建校验值、实际运行版本、每个真实测试结果、未验证项、远端发布状态和回退点。未解决的422禁止将候选版本标为可发布。

## 自审与覆盖映射

- 命名/历史/迁移：任务6–7；默认路由不变：任务3–4；已有422：任务1。
- 引用语义与预算：任务1、3；工具名称范围/双向映射：任务2、3；完整SSE预算：任务2。
- 严格配置/实例隔离：任务4；媒体类型/头/取消：任务5。
- 不搬运客户端strict/store/itemId行为：任务3的字节保持与单策略断言。
- 真实证据与离线结果分离、兼容规则必要性检验及 OpenCode CLI/App 直连对照：任务7；源码结论与限制见证据登记 O1–O6。
- 不在本轮实施、部署或委派：全局约束及任务7授权门。

建议执行方式：在当前主会话按任务1→7顺序实施；在任务1、任务2和部署前重点复核，不并行改写同一组schema/server文件。用户明确选择并行或独立审查后再委派。任务7可因缺少真实请求或部署授权保持未执行，但必须如实报告，不能把候选构建当成完整上线。

## 实施前离线预检记录（历史，仅临时副本）

2026-09-24 已对8段Go代码块执行gofmt语法解析，全部通过。将任务1、2、5的回归代码放入当前仓库源码的临时副本运行，结果如下；源码工作区没有新增测试文件。

| 计划用例 | 当前版本结果 | 证明范围 |
|---|---|---|
| TestRefSiblingRedundantTypeAndDescription | 失败：unsupported siblings | 重复类型/注解误拒绝可以复现 |
| TestRefSiblingConflictingTypeIsNotSilentlyOverwritten | 通过 | 当前拒绝冲突的行为被保留为负向边界，不是修复成功证明 |
| TestRestoreIgnoresNonToolMetadataName | 失败：metadata changed | 当前恢复范围确实误改普通name |
| TestSSEBudgetIncludesEnvelope | 失败：未返回超限错误 | 当前预算遗漏完整输出帧封装开销 |
| TestUnknownSuccessMediaWithAliasesFailsClosed | 失败：Connection指定头被转发、状态200 | 当前HTTP边界需要修复 |

这些是预期红灯，不是完整测试套件失败报告，也不是已修复的绿灯。任务3、4涉及新接口的片段仅验证了语法，须在实施时完成编译/红绿验证。没有调用真实上游、安装依赖或重启服务。
