# Responses Compat 兼容性证据登记

核实日期：2026-09-24（Asia/Shanghai）。本文件记录已核实资料与证据缺口，不将改写策略当作根因证明。

## 证据类型

- D：已读取官方文档；说明公开契约，不证明具体部署行为。
- C：当前仓库源码/历史；说明 Adapter 的实现，不证明原始请求由哪层产生。
- T：本地合成测试；证明受测实现，不证明真实提供方。
- L：真实链路观测或对照；必须注明历史错误日志、主动复现或端到端验证，记录版本、路由及日期，不能相互替代。

各类型独立，不把文档、测试和真实行为混成一个“已验证”标签。初次编写时未读取真实请求日志；2026-09-24 16:54 的后续故障排查已按用户要求检查对应错误日志，并在内存中做离线对照。没有主动调用上游模型，也没有把原始请求落入仓库。当前离线源码验证另见文末检查点；其结果仅证明本地实现，不扩大为真实提供方证据。

## 问题与规则登记

| ID | 当前行为/问题 | 已有证据 | 尚缺证据与允许结论 |
|---|---|---|---|
| schema.ref_siblings | `$ref` 与相邻相同约束曾触发 Adapter 422 | C；L：16:39:34 故障日志中该工具参数转发前后相同；T：原请求离线失败、仅移除重复 type 后通过、最小合成复现；现有 `TestRefSibling*` 回归覆盖 | 已修复 Adapter 误拒绝并通过本地测试；修改后真实上游是否接受仍未验证 |
| schema.inline_local_refs | schema.go 展开本地引用 | C；D1/D2 | 未取得原始失败请求；不能断言所有失败都是上游不支持引用 |
| schema.recursive_empty | 递归回边替换为 `{}` | C；D1/D2 支持递归公开能力 | 这是放宽约束；合法递归不等于不标准，具体上游失败仍待 L |
| tools.structured_traversal | 处理嵌套工具与 additional_tools | C；D1/D3 | 字段已有公开说明；具体模型支持和代理是否扁平化待 L |
| tools.name_alias | tool_names.go 按 len(name)>64 改名并建立映射 | C；D5 的 64 限制属于 Chat Completions | 无当前 Responses 对应字段长度约束的充分证据；无 A/B 名称对照；不认定 Codex 或上游违规 |
| reasoning.drop_id | schema.go 删除输入 reasoning 项的 id | C；D4 说明无状态 reasoning 重放 | 缺同账号/同路由 ID 来源和前后对照；“provider ID 不稳定”仅为待证假设 |
| transport.sse_flush | SSE 响应头提前刷新 | C：fdd2415，server.go | 属于 Adapter 自身处理，不归责于 Codex/上游 |
| response.name_scope | 响应恢复限定在已识别的 function_call/函数定义位置 | C/T：原 metadata.name 反例曾失败；当前回归测试验证 metadata 与 arguments 不变 | 本地合成误改已修复；不代表真实会话出现过此问题 |
| response.full_frame_limit | JSON 与完整 SSE 帧分别受扩展后预算限制 | C/T：完整帧封装超限反例曾失败；当前边界测试覆盖 | 本地预算缺口已修复；不代表真实上游流已验证 |
| transport.rewrite_boundary | 成功未知媒体类型在别名恢复时失败关闭；动态 hop-by-hop 与受控头不转发 | C/T：原本地 httptest 反例观察到 200 和头泄漏；当前测试断言 502、Connection token 过滤及长度/编码头过滤 | 本地边界已修复；不涉及真实凭据外泄事件，也未测试真实上游的媒体类型 |
| state.previous_response_id | 当前无专属拒绝逻辑，名称映射为请求级 | C | 服务端保存的历史工具映射无法由当前资料保证；未验证，不新增普遍 422，不宣称完整支持 |

源码依据：`schema.go`、`tool_names.go`、`response_rewriter.go`、`server.go`、`config.go`；相关合成样例在对应 `_test.go` 文件中。下方检查点仅登记本地实现测试，不给真实上游增加 L 证据。

## 官方资料（本轮读取成功）

以下页面于本日通过 HTTP 200 获取并检查相关内容。日期是核实日期，不是页面发布日期；记录不保证未来文档或第三方实现不变。

### D1：OpenAI Function calling

`https://developers.openai.com/api/docs/guides/function-calling`

工具参数说明包括递归对象；文档亦介绍 namespace。用途：排除“出现递归或 namespace 就一定不标准”的笼统结论。不证明某个具体请求满足所有接口和模型条件。

### D2：OpenAI Structured Outputs

`https://developers.openai.com/api/docs/guides/structured-outputs`

包含递归 Schema 支持及示例。用途：证明公开能力存在；不能把这里每项约束无条件移植到任意第三方路由。

### D3：OpenAI Tool search

`https://developers.openai.com/api/docs/guides/tools-tool-search`

介绍 namespace、additional_tools 及工具引入时序。用途：判断这些结构不是仅凭名称就可认定的私有或非法字段；具体模型支持仍应单独核对。

### D4：OpenAI Reasoning

`https://developers.openai.com/api/docs/guides/reasoning`

描述无状态 reasoning 延续及 encrypted_content。用途：保留重放语义，不把删除 ID 提升为普遍规则，也不假设加密内容可跨提供方移植。

### D5：OpenAI Chat Completions create

`https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create`

函数名称字段包含最大长度 64 的约束。用途仅限该文档中的字段。不能据此断言所有 Responses namespace/name 结构都受同一规则限制。

### D6：OpenCode Go

`https://opencode.ai/docs/go/`

端点表将 muse-spark-1.3-contributor 列在 `/responses` 下。用途：确认文档提供的端点对应；端点表不足以证明它承诺全量实现 OpenAI 的每项 Responses 功能。

## O1–O6：OpenCode 同版本源码核验（2026-09-24）

本机 CLI/App 实测均为 1.18.32；克隆标签 v1.18.32，固定提交 `545f51d26cc39a907d2867492d498d9607ea5fa4`。源码检查采用定点检索和调用链阅读，没有可用图谱工具，不作全仓穷尽审计声明。

| 编号 | 核验范围 | 结果与边界 |
|---|---|---|
| O1 | 本机全局配置中的 Muse 路由字段 | opencode-go-responses 使用 @ai-sdk/openai、OpenCode Go 基址；未输出凭据，不把全局配置当具体会话抓包 |
| O2 | provider/transform.ts 的 sanitizeOpenAISchema，以及 session/tools.ts 调用点 | MCP 路径保留引用及相邻 type/description；不一律展开或拒绝，不能等同于所有工具的完整处理 |
| O3 | tool/json-schema.ts | 内置 Effect 工具生成路径展开可解析本地引用，残余递归引用和定义保留；预置 jsonSchema 与 MCP 不一定走此路径 |
| O4 | session/llm/request.ts 与 provider/transform.ts | OpenAI 路径 strict=false、默认 store=false、非存储模式移除消息元数据 itemId；opencode 前缀路由添加会话头；这些不能直接变成代理全局清洗 |
| O5 | mcp/catalog.ts 的 toolName，及 @ai-sdk/openai 3.0.88 发布源码 | MCP 名称在该函数中仅清理并拼接；SDK 默认 languageModel 创建 Responses 模型，函数参数使用 inputSchema。SDK tarball 与 npm shasum 一致；没有据此穷尽所有插件和包装层 |
| O6 | desktop/src/main/sidecar.ts、electron.vite.config.ts | App sidecar 加载由 packages/opencode/dist/node 构建的 Server；CLI/App 复用服务端来源，不是两套独立协议实现 |

源码依据（均固定同一提交，以下为仓库相对路径）：

- `packages/opencode/src/provider/transform.ts`
- `packages/opencode/src/session/tools.ts`
- `packages/opencode/src/tool/json-schema.ts`
- `packages/opencode/src/session/llm/request.ts`
- `packages/opencode/src/provider/provider.ts`
- `packages/opencode/src/mcp/catalog.ts`
- `packages/desktop/src/main/sidecar.ts`
- `packages/desktop/electron.vite.config.ts`

固定源码入口：`https://github.com/anomalyco/opencode/tree/545f51d26cc39a907d2867492d498d9607ea5fa4`

SDK 元数据：`https://registry.npmjs.org/@ai-sdk/openai/3.0.88`

### 五项实际执行的纯函数实验

直接提取已读源码中的纯函数，只移除 TypeScript 类型，在隔离 JS 上下文内运行合成输入；没有重写其算法、安装项目依赖或启动服务。

1. sanitizeOpenAISchema：`$ref` + type 原样保留。
2. sanitizeOpenAISchema：`$ref` + description 原样保留。
3. sanitizeOpenAISchema：递归引用及定义保留。
4. 内置引用处理 helper：引用目标与相邻 type 一致时展开成功，并删除已完全消解的定义。
5. 内置引用处理 helper：递归残余引用及定义仍保留，不替换回边为 `{}`。

证据类型为 C/T，不是 OpenCode 整套测试或真实 Muse 端到端验证。未验证运行时插件/项目配置覆盖，也没有证明安装包与标签源码的逐字节构建对应关系。上述结果不能把上游接受性或全工具兼容性标为已验证。

## 最小归因流程（待独立授权执行）

1. 使用无私人内容的最小合成用例，记录客户端、CLIProxyAPI、Adapter 的版本与上游模型 ID。
2. 对照 A（客户端出站）、B（CLIProxyAPI 转发至 Adapter）和 C（Adapter 出站及上游响应）的必要结构。授权/会话信息不进入证据文件。
3. 固定路由与账号条件，每次只启用一条规则；保留原始状态码、去敏错误类别和结构差异。
4. 若 A 正确而 B 改坏，归因中间层；若 A 违反明确适用的契约，归因客户端/工具注册；若请求满足上游明确承诺而被拒绝，才归因上游实现。
5. 若只是支持范围不同，记录能力差异；若缺少 ID 来源、跨轮身份或协议依据，保留“待归因”。
6. 把可重现的必要结构人工去敏后转为合成回归样例，记录语义损失与撤销规则条件。

不得自动采集全部请求、提交完整日志或为验证读取无关私人会话。真实上游成功一次也不等于对该 provider 的全功能兼容认证。

## 已确认案例：INC-20260924-422-ref-sibling

- 状态：Task 1 已在本地实现修复并加入 `TestRefSibling*` 回归；修复提交 `a7ce48f` 已包含于主干代码候选 `a108549`，当前 `main` 仍保留该实现。离线修复通过；真实 Muse 上游接受性及运行态是否已消除故障仍未验证。
- 发生时间：错误日志 2026-09-24 16:39:34；排查核实 16:54，Asia/Shanghai。
- 故障跳点：CLIProxyAPI → `127.0.0.1:18317/v1/responses`，响应 422，正文为 Adapter 的通用错误。
- 涉及工具：`mcp__codex_app.automation_update`；节点 `parameters/$defs/__schema20`，引用 `#/$defs/__schema2`，引用与自身都约束为 string。
- A/B 对照：该工具的完整 parameters 一致；不声称整份请求或全部工具树都相同。
- 源码依据：`schema.go` 的 `expandSchemaNode` 在展开前拒绝 `$ref` 相邻 type；`server.go` 将该错误统一映射成 422。
- 离线结果：故障请求拒绝；仅在内存中删除重复 type 后归一化通过（输出 469529 字节）；最小合成请求同样被拒绝。没有改写原日志，没有发送给上游。
- 运行态：Adapter healthz 204；运行二进制构建元数据为 fdd2415 且 vcs.modified=true，来源不是可精确等同的干净提交。离线诊断使用当前仓库源码（316b450 时的代码），不能把两者声称为同一构建。
- 归因：本次 422 为 Adapter 自身的保守拒绝规则；该工具参数未被 CLIProxyAPI 改动。此证据不能证明上游接受同一请求，也不能推广到其他 Schema 问题。
- 语义：本次引用目标与相邻 type 重复，移除重复约束用于诊断，不是“删除所有相邻字段”的生产修复方案。
- 离线退出条件：同约束相邻字段可正常归一化、冲突约束仍被明确拒绝，并有回归测试；此条件已满足。运行态退出条件：经授权用最小合成请求验证目标 Muse 路由，并记录请求到达的边界及响应。完成前仅可称“离线修复完成”，不能称端到端故障关闭或上游兼容已验证。

可提交的最小合成样例（不含原请求的工具描述、提示词、会话或凭据）：

```json
{
  "model": "muse-spark-1.3-contributor",
  "tools": [
    {
      "type": "function",
      "name": "example",
      "parameters": {
        "type": "object",
        "properties": {
          "value": {"$ref": "#/$defs/Text", "type": "string"}
        },
        "$defs": {"Text": {"type": "string"}}
      }
    }
  ]
}
```

原版诊断结果为 `schema reference node has unsupported siblings`。该最小相邻约束样例已作为 `schema_refs_test.go` 中的生产回归测试加入。

## 后续真实案例记录字段

case_id、rule_id、核实日期、组件版本、模型/路由范围、模式（无状态重放或服务端状态）、最小合成输入、原始状态/去敏错误、单项修改、修改后结果、契约依据、语义损失、归因与置信度、撤销条件。

以上字段用于后续登记；当前新增一项历史日志 L 观测及离线 T 对照，见 INC-20260924-422-ref-sibling。没有新增上游端到端成功证据。

## 执行计划的红灯验证（2026-09-24，预实现历史记录）

在临时副本中运行计划任务1、2、5的五个测试函数，四个函数按预期因行为缺陷失败，冲突类型负向测试通过。新增的T证据包括：重复类型/注解被拒绝、普通metadata.name被误改、SSE输出封装超限未被拒绝、Connection指定头被转发和未知成功媒体类型未失败关闭。

详细用例与结果见 `docs/superpowers/plans/2026-09-24-responses-compat-implementation.md`。本节只描述当时临时副本红灯验证，不代表之后工作区状态。

## 当前离线实现检查点（2026-09-24）

- Task 1–2 的独立修复提交：a7ce48f、8a577c6；策略隔离提交：27fefed；严格配置及 HTTP 传输边界提交：7b4d855；更名与离线交付提交：a108549。
- 在干净代码提交 a1085497b2184c4c28e184d5a4e94888ac83d8f9 上执行 go test ./... -count=1 与 go test -race ./... -count=1，分别 146 项通过；go vet ./... 通过。Go 1.27.1 / darwin-arm64。
- 离线二进制 SHA-256：c7ad523b7f3758310a8975c54131d4cb49c30f0a76db402aeda43156084327f6；go version -m 显示 source revision a1085497b2184c4c28e184d5a4e94888ac83d8f9 且 vcs.modified=false。
- 代码提交 a108549 已 fast-forward 合并到本地 main，后续仅追加了文档验证记录；未配置 Git remote，因此未推送或发布。没有真实上游调用、CLI/App 直连对照或本机服务切换；v0.1.0 标签保持不变。
- 因此 `$ref` 修复后的上游接受性、Muse Responses 的实际 Schema/名称限制、CLI/App 是否能直接使用 Muse，以及当前运行服务状态仍未验证。不得将本地 146 项通过描述为服务端兼容或发布验收通过。

## 主干复验（2026-09-24）

- 代码验证对象：当次本地 `main` 的 Go 源码状态为 `81a1d5c21f902dfa65ea415eac33fb27a8f87b75`；与代码候选 `a108549` 相比仅有执行计划和证据登记的文档变更，没有源码差异。之后仅更新并提交了证据文档，当前 `main` 工作区干净，Go 源码未变。
- 在干净的上述 HEAD 上运行 `go test ./... -count=1`、`go test -race ./... -count=1`，均为 146 项通过；`go vet ./...` 通过。证据文档更新后、提交前再次运行三项检查，结果相同。环境为 Go 1.27.1、darwin/arm64。
- 从该干净 HEAD 执行 `go build -trimpath -o /tmp/responses-compat .` 成功。构建元数据：module `responses-compat`；版本信息 `v0.1.1-0.20260924120753-81a1d5c21f90`（Go 根据未发布提交推导的伪版本，不是正式 release）；`vcs.revision=81a1d5c21f902dfa65ea415eac33fb27a8f87b75`、`vcs.modified=false`。临时构建产物 `/tmp/responses-compat` 的 SHA-256：`d806589dc115652cfb86020c01407654b61d314be0220e2bb2903a2e67aac368`。
- 当前没有 Git remote；本地 `v0.1.0` 标签仍指向旧提交 `211fefdd685313ec1c4132ebd969e9fdbab73b40`。没有推送、创建标签或发布。
- 本节仅提供当前主干的离线代码、测试和构建证据；没有执行 E0–E4 真实路由对照、OpenCode CLI/App 直连或本机服务切换，不构成上游兼容及运行态发布验收。


## 项目目录更名复核（2026-09-24）

- 用户明确要求先完成重命名，再提交 GitHub。已确认当前任务是唯一引用该本地工作目录的 Codex task、Git worktree 仅有 `main`，`~/Documents/responses-compat` 原先不存在。
- 更名前核对当前 `com.hrygo.muse-codex-adapter` LaunchAgent：标签与实际加载状态相符；可执行文件名仍为 `muse-codex-adapter`；其 `Program`/`ProgramArguments` 不引用项目工作目录。仅核验是否引用目录，未读取 `EnvironmentVariables`。据此完成项目目录迁移 `~/Documents/muse-codex-adapter` → `~/Documents/responses-compat`，没有停止、重启或替换运行服务。
- 仓库根目录的 `muse-codex-adapter` 是被忽略的旧构建产物，构建元数据显示 module `muse-codex-adapter`、revision `fdd24150b7821da633986984940f16e47a41520e`、`vcs.modified=true`；保留原文件，不纳入 Git，也不将其当作运行服务回退备份。`.gitignore` 继续忽略此旧产物，并新增忽略当前默认输出 `responses-compat` 与 macOS `.DS_Store`。
- 运行态身份仍是旧 LaunchAgent/二进制；目录迁移不改变运行服务。服务改名或切换需按部署清单另行授权。
- Git remote 仍为空；当前 GitHub 连接账号中未找到名为 `responses-compat` 的目标仓库。用户已要求提交 GitHub，但新仓库公开/私有可见性未指定，因此本轮不创建远端、不推送、不打标签。
