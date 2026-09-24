# Responses Compat 落地实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> 本环境可用技能名为 `executing-plans`；不为技能别名自动安装工具。推荐主会话顺序执行，未获明确委派授权时不启动子代理。

**Goal:** 先修复已确认的 Adapter 422，再将必要兼容规则做成可配置、可关闭、可验证的 Responses Compat，最后独立完成更名和授权后的部署。

**Architecture:** 单实例、单固定上游，CLIProxyAPI 继续负责账号和路由。Go 标准库完成配置、HTTP 和有界 JSON/SSE 处理；passthrough 不变换正文，muse 保留旧策略选择但修复已确认缺陷。

**Tech Stack:** 现有 Go 1.27 module，标准库、testing、httptest；不新增 OpenCode/AI SDK/Effect 依赖。

**Spec:** `docs/superpowers/specs/2026-09-24-responses-compatibility-proxy-design.md`

**Evidence:** `docs/compatibility/2026-09-24-evidence-register.md`

**Date / status:** 2026-09-24；实施中（分支 `codex/responses-compat`）。任务 1 已完成实现与验证；任务 2–6 待实施。真实上游、服务切换和发布仍受任务 7 授权门约束。

## Global Constraints

- 当前仓库身份在代码实施前仍是 muse-codex-adapter；目标名称 Responses Compat / responses-compat。
- 不修改 v0.1.0 标签；v0.2.0 是候选目标，不预先打标签或宣称发布。
- 保留默认 127.0.0.1:18317、Muse 模型和上游地址；不更改默认 64 字节策略的 `muse_` 别名结果。
- 32 MiB 请求/展开预算、Schema 深度 64、64 MiB JSON 和完整 SSE 帧上限。
- 不伪造客户端身份，不新增 strict/store 的强制改写，不清洗任意对象 ID；不重试生成、不跨上游切换。
- previous_response_id 不新增一律拒绝；仅声明完整工具上下文无状态重放的已测范围，未知历史映射不假装可恢复。
- 保留 README 现有未提交内容。任何新出现的并行编辑先核实，不能整体回滚或覆盖。
- 真实日志/原请求不进入仓库；只使用合成 Schema 和伪凭据。测试不能依赖个人目录、真实模型或参考仓库存在。
- OpenCode 依据固定为 v1.18.32 / 545f51d26cc39a907d2867492d498d9607ea5fa4；借鉴证据，不拷贝其实现或依赖。
- 任务 1–6 只产生代码、测试、文档和离线候选构建。任务 7 的真实请求、安装、服务切换、目录迁移、远端发布另行授权。

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

**Files:** response_rewriter.go、response_rewriter_test.go。

**Consumes/Produces:** 保留 restoreToolNamesInJSON、rewriteSSEFrame、streamSSEWithToolNameRestore 签名；改变误改范围与完整帧超限行为。

- [ ] **2.1 写反例并确认当前红灯。** 在 response_rewriter_test.go 增加以下测试；补入 errors import。

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

- [ ] **2.2 统一语义范围。** 用同一套已识别路径供大小预估和实际改写使用：根 function_call；response output 中的 function_call；response tools/namespace 中的 function 定义；output_item.added/done 的 item；function_call_arguments.delta/done 的顶层 name。明确 response.created/in_progress/completed/incomplete/failed 的 response 包装。不能深入 metadata 或 arguments；未知事件默认保持，不添加正则文本替换。
- [ ] **2.3 完整帧预算。** 在构造新帧前计算保留的 event/id/comment 行、data 前缀、改写 JSON 及行尾总长度；超限返回 errResponseRewriteLimit。不要仅给 JSON 64 MiB，再在其外无界附加封装。保留原始帧上限。
- [ ] **2.4 验证完整矩阵。** 单独断言 LF/CRLF、多行 data、EOF 尾帧、无工具映射原样返回、完整帧恰好上限通过/多一字节拒绝。保留 response.failed 终止且不继续 [DONE] 的现有测试；检查测试夹具不能因收窄路径而意外失去扩展压力。
- [ ] **2.5 绿灯和提交。** `go test ./... -run 'TestRestore|TestRewrite|TestSSE|TestStream|TestWrite' -count=1` 后运行全量/race/vet；提交 `fix: scope tool name restoration and bound full SSE frames`。

## Task 3：引入最小策略，不搬运客户端行为

**Files:** compatibility.go、compatibility_test.go、schema.go、tool_names.go、tool_names_test.go。

**Consumes:** 任务 1 的 Schema 行为；保留旧 normalizer/alias 函数包装。

**Produces:** 文首 CompatibilityPolicy、musePolicy、passthroughPolicy、normalizeRequestWithPolicy。

- [ ] **3.1 用测试规定透传和策略隔离。** 新建 compatibility_test.go；当前因为接口不存在红灯。

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

- [ ] **3.2 实现预设并接入现有算法。** 不修改 strict/store，不新增 previous_response_id 拒绝。旧入口明确包装 Muse 模型白名单及 musePolicy；新 normalizer 用传入 models 做精确匹配。全透传配置仍校验 JSON 对象、模型、大小与尾随数据，但不运行 Schema 展开。只在实际变换时编码，数字继续 UseNumber。

```go
func musePolicy() CompatibilityPolicy {
    return CompatibilityPolicy{"inline", "empty_schema", 64, "drop"}
}
func passthroughPolicy() CompatibilityPolicy {
    return CompatibilityPolicy{"preserve", "reject", 0, "preserve"}
}
```

- [ ] **3.3 可配置别名。** 新内部函数接收 maxBytes，0 不产生映射；保留 functionToolAlias 默认结果。缩短算法为 `muse_` 加 SHA-256 十六进制摘要前 `min(maxBytes-len("muse_"), 58)` 位。64 策略仍输出旧版 63 字节名称；不同策略不可在同一会话中途隐式切换。每次请求检查原名称冲突和别名冲突，无跨请求缓存。
- [ ] **3.4 补充表驱动策略断言。** 同一合成请求分别验证：仅 drop 删除 reasoning.id；仅 alias 修改名称；仅 inline 处理引用；其余 strict/store/unknown 字段保持。reject 递归返回固定错误，empty_schema 保持旧降级。测试别名阈值 16/32/64/256、零值禁用、跨模型白名单、未变换原字节；旧 64 哈希结果做精确对照。
- [ ] **3.5 绿灯和提交。** `go test ./... -run 'TestPassthrough|TestNormalize|TestBuildToolName|TestFunctionToolAlias' -count=1`，再全量/race/vet。提交 `feat: isolate compatibility policies and add passthrough`。

## Task 4：配置读取与进程接线

**Files:** config.go、config_test.go、main.go、server.go。

**Consumes:** 任务 3 的 policy normalizer。

**Produces:** RuntimeConfig、defaultConfig、decodeConfig、loadConfig、NewConfiguredHandler；旧 NewHandler 包装新入口，保持测试和默认行为。

- [ ] **4.1 写配置核心反例。** 新建 config_test.go。

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

- [ ] **4.2 严格解码。** 先通过 Decoder.Token 递归检查每个对象的重复键，再 DisallowUnknownFields 解码结构，要求 EOF。预设后应用覆盖；tool_name_max_bytes 使用 json.RawMessage 区分缺省、null、数值，不能用单个 *int 混淆继承与禁用。显式 0、负数、小数、超出 16–256 均拒绝。
- [ ] **4.3 启动校验。** 检查版本=1、非空不重复 models、已知 profile/策略、字面量 loopback 监听、合法端口、固定上游 URL。拒绝 userinfo/query/fragment；HTTP 仅限字面量 loopback。额外头只接收合法头名并去重，受控/逐跳头不能额外放行。不发网络请求来验证配置。
- [ ] **4.4 接入入口。** 标准 flag 解析 `--config`；loadConfig("") 返回 defaultConfig，非空路径错误则退出，不能静默回落 Muse。main 创建同一超时/退出策略的 Server，handler 收到不可变实例配置；复制配置切片，避免调用方修改带来竞争。保留 Request 的 Context。
- [ ] **4.5 验证矩阵。** 表驱动覆盖缺省64与显式null不同、嵌套重复键、未知键/策略、非回环监听、带 userinfo/query 的 URL、禁止头和模型不匹配。用两个 httptest 上游和不同配置实例验证请求与授权不串路由；`--config` 不存在必须退出而不是占用默认端口。
- [ ] **4.6 绿灯和提交。** 全量/race/vet，通过后提交 `feat: configure fixed upstream compatibility instances`。

## Task 5：HTTP 传输边界与媒体类型失败关闭

**Files:** server.go、server_test.go；必要的 header 校验辅助逻辑放 config.go，不新建框架。

**Consumes:** NewConfiguredHandler 和配置头白名单；任务 2 的响应恢复。

**Produces:** 有名称映射时未知成功媒体类型 502；受控头不泄漏；原非2xx/取消/SSE行为有测试证明。

- [ ] **5.1 写最小失败用例。** 增加 server_test.go 测试，复用现有 imports。

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

- [ ] **5.2 实现分支。** 成功响应有别名时，以 mime.ParseMediaType 判断 application/json、application/*+json 或 text/event-stream，不使用任意字符串 contains("json")。不支持的成功类型在下游提交响应头前返回502。无别名和非2xx按原策略转发，不错误地重写非2xx正文。
- [ ] **5.3 头与生命周期。** 固定头 + 配置额外头形成 allowlist，再减去逐跳头、Connection token 和受控长度/编码头。请求和响应分别处理，不能让配置覆盖排除集合。不新增自动重定向/重试。取消测试用同步 channel 观察上游 Context.Done，并设置有界等待，不用任意 sleep 判成功。
- [ ] **5.4 错误可观测性。** 固定错误 code 满足本轮定位要求，不开启正文日志。若记录诊断，只含本地请求 ID、规则 ID、计数、状态、耗时；写测试输入含 synthetic-secret，断言响应/日志不包含它。拒绝全量日志作为默认排障方式。
- [ ] **5.5 绿灯和提交。** 全量/race/vet，保留上游非2xx、200业务错误、首事件前flush测试。提交 `fix: enforce proxy transport and rewrite boundaries`。

## Task 6：最后更名，交付离线候选版本

**Files:** go.mod、main.go、README.md、CHANGELOG.md、examples/muse.json、examples/muse-passthrough.json、docs/deployment.md。

**Consumes:** 任务1–5全部通过。

**Produces:** responses-compat 源码/二进制身份与无凭据文档；不更名工作目录、不安装、不重启、不打标签。

- [ ] **6.1 更名前验证。** 复核 README 既有 diff 的来源，在保留其内容基础上更新，不用旧 HEAD 覆盖。搜索旧品牌引用，区分历史记录、协议前缀、旧安装标识和需要修改的展示名称，不能全局替换 muse_ 或模型 ID。
- [ ] **6.2 更新身份。** go.mod 模块名改 responses-compat；日志前缀改 responses-compat:；构建说明使用新二进制名。历史文档和 v0.1.0 保留，CHANGELOG 使用 Unreleased 记录，不提前给出发布日期或标记真实兼容验证通过。
- [ ] **6.3 添加配置示例。** muse.json 精确使用主方案样例。muse-passthrough.json 使用相同上游/模型、profile=passthrough、listen=127.0.0.1:18318；明确它是诊断配置。文件不含凭据，自动测试用 decodeConfig 验证两份示例。
- [ ] **6.4 README/部署文档。** 区分可配置机制与真实已验证路由；写清旧默认策略、有损递归、字节长度、未验证服务端状态。描述旧二进制/LaunchAgent 的备份、停旧启新、healthz和工具/SSE验收、回退步骤；实际安装路径在部署时发现，不将个人凭据或机器 wrapper 写进文档。
- [ ] **6.5 全量验证后提交并构建。** 提交 `refactor: rename project to Responses Compat` 后，在确认没有未知源代码改动时执行：

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

预期 module 为 responses-compat；记录实际 Go/平台、提交、vcs.modified 和 SHA-256。候选构建不是已安装版本，不能写成服务已更新。

## Task 7：授权后再做真实对照、服务迁移和发布

**Files:** 只有获得授权且产生实际结果后，新增 docs/validation/v0.2.0.md；不提前生成假结果。

**Consumes:** 已验证的候选构建、主方案和证据登记；需要明确真实请求/安装/发布范围。

**Produces:** 三者分开：上游兼容结论、运行态部署结果、远端发布结果。任一缺少条件不得笼统称“完成发布”。

- [ ] **7.1 停在授权门。** 确认真实模型调用数量/费用范围、是否允许读取所需本机凭据、是否安装新服务、远端发布目标。凭据不进入命令参数、日志、仓库或报告。授权前只做前六项，不以方案批准代替运行态批准。
- [ ] **7.2 建立对照，而不是先证明 Adapter 必需。** 同一模型/账号条件下使用最小合成请求，分开记录：E0直接上游；E1经CLIProxyAPI不经Adapter；E2经CLIProxyAPI+passthrough；E3在E2上每次仅启用一条规则。不要在原生产路由上直接改来改去，优先隔离测试端口/请求。
- [ ] **7.3 五类用例。** 普通文本；本次 ref+同type；递归工具；超长工具名；无外部副作用的echo工具完整多轮+SSE。记录每类在哪个边界失败、错误类别及单规则前后结果；不复制真实会话来省事。失败不无限重试，未完成上游验证的能力继续标“未验证”。
- [ ] **7.4 按证据裁剪规则。** 若E1已成功而E2/E3变坏，修代理，不责怪上游；若E0/E1/E2都成功，相关规则保持关闭，不增加更多变换。若确需某条规则，记录适用范围、语义损失、退出条件。不能凭源码函数测试宣称上游接受。
- [ ] **7.5 服务切换。** 核实旧运行进程的实际可执行文件，备份当前二进制与plist并记录校验值；历史排查中为 fdd2415+dirty，部署时重新核实；实际运行构建不能由Git标签或历史记录替代。准备新二进制和com.hrygo.responses-compat，停旧后启新，不能争用18317。失败则停新并恢复实际备份；不执行破坏性Git回退。healthz 204只是第一步，还需实际工具闭环和SSE。
- [ ] **7.6 目录、远端和发布。** 当前工作目录移名及托管仓库更名各自核实其他任务/工作树后执行。尚无远端时只报告本地候选或本地部署，不能创建猜测的远端。确认版本门槛后才能新增v0.2.0标签与发布说明，不移动v0.1.0。
- [ ] **7.7 最终报告。** 分别列出源码提交、构建校验值、实际运行版本、每个真实测试结果、未验证项、远端发布状态和回退点。未解决的422禁止将候选版本标为可发布。

## 自审与覆盖映射

- 命名/历史/迁移：任务6–7；默认路由不变：任务3–4；已有422：任务1。
- 引用语义与预算：任务1、3；工具名称范围/双向映射：任务2、3；完整SSE预算：任务2。
- 严格配置/实例隔离：任务4；媒体类型/头/取消：任务5。
- 不搬运客户端strict/store/itemId行为：任务3的字节保持与单策略断言。
- 真实证据与离线结果分离、必要性检验：任务7；OpenCode结论在证据登记O1–O6。
- 不在本轮实施、部署或委派：全局约束及任务7授权门。

建议执行方式：在当前主会话按任务1→7顺序实施；在任务1、任务2和部署前重点复核，不并行改写同一组schema/server文件。用户明确选择并行或独立审查后再委派。任务7可因缺少真实请求或部署授权保持未执行，但必须如实报告，不能把候选构建当成完整上线。

## 本轮计划验证记录（仅临时副本）

2026-09-24 已对8段Go代码块执行gofmt语法解析，全部通过。将任务1、2、5的回归代码放入当前仓库源码的临时副本运行，结果如下；源码工作区没有新增测试文件。

| 计划用例 | 当前版本结果 | 证明范围 |
|---|---|---|
| TestRefSiblingRedundantTypeAndDescription | 失败：unsupported siblings | 重复类型/注解误拒绝可以复现 |
| TestRefSiblingConflictingTypeIsNotSilentlyOverwritten | 通过 | 当前拒绝冲突的行为被保留为负向边界，不是修复成功证明 |
| TestRestoreIgnoresNonToolMetadataName | 失败：metadata changed | 当前恢复范围确实误改普通name |
| TestSSEBudgetIncludesEnvelope | 失败：未返回超限错误 | 当前预算遗漏完整输出帧封装开销 |
| TestUnknownSuccessMediaWithAliasesFailsClosed | 失败：Connection指定头被转发、状态200 | 当前HTTP边界需要修复 |

这些是预期红灯，不是完整测试套件失败报告，也不是已修复的绿灯。任务3、4涉及新接口的片段仅验证了语法，须在实施时完成编译/红绿验证。没有调用真实上游、安装依赖或重启服务。
