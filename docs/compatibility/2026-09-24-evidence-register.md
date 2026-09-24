# Responses Compat 兼容性证据登记

核实日期：2026-09-24（Asia/Shanghai）。本文件记录已核实资料与证据缺口，不将改写策略当作根因证明。

## 证据类型

- D：已读取官方文档；说明公开契约，不证明具体部署行为。
- C：当前仓库源码/历史；说明 Adapter 的实现，不证明原始请求由哪层产生。
- T：本地合成测试；证明受测实现，不证明真实提供方。
- L：真实链路观测或对照；必须注明历史错误日志、主动复现或端到端验证，记录版本、路由及日期，不能相互替代。

各类型独立，不把文档、测试和真实行为混成一个“已验证”标签。初次编写时未读取真实请求日志；2026-09-24 16:54 的后续故障排查已按用户要求检查对应错误日志，并在内存中做离线对照。没有主动调用上游模型，没有把原始请求落入仓库，也未重跑完整代码测试。前序全套测试结果仅作历史信息。

## 问题与规则登记

| ID | 当前行为/问题 | 已有证据 | 尚缺证据与允许结论 |
|---|---|---|---|
| schema.ref_siblings | `$ref` 与相邻 type 触发 Adapter 422 | C；L：16:39:34 故障日志中该工具参数转发前后相同；T：原请求离线失败、仅移除重复 type 后通过、最小合成复现 | 本次可定位为 Adapter 误拒绝；尚未修复，未证明修改后上游端到端成功 |
| schema.inline_local_refs | schema.go 展开本地引用 | C；D1/D2 | 未取得原始失败请求；不能断言所有失败都是上游不支持引用 |
| schema.recursive_empty | 递归回边替换为 `{}` | C；D1/D2 支持递归公开能力 | 这是放宽约束；合法递归不等于不标准，具体上游失败仍待 L |
| tools.structured_traversal | 处理嵌套工具与 additional_tools | C；D1/D3 | 字段已有公开说明；具体模型支持和代理是否扁平化待 L |
| tools.name_alias | tool_names.go 按 len(name)>64 改名并建立映射 | C；D5 的 64 限制属于 Chat Completions | 无当前 Responses 对应字段长度约束的充分证据；无 A/B 名称对照；不认定 Codex 或上游违规 |
| reasoning.drop_id | schema.go 删除输入 reasoning 项的 id | C；D4 说明无状态 reasoning 重放 | 缺同账号/同路由 ID 来源和前后对照；“provider ID 不稳定”仅为待证假设 |
| transport.sse_flush | SSE 响应头提前刷新 | C：fdd2415，server.go | 属于 Adapter 自身处理，不归责于 Codex/上游 |
| response.name_scope | 当前 response_rewriter.go 按任意 name 递归恢复 | C/T：临时副本中的 metadata.name 反例实际失败 | 已复现合成输入误改，待修复；不是新增真实用户会话事故证据 |
| response.full_frame_limit | 原始帧及 JSON 恢复已有预算 | C/T：完整帧封装超限测试实际未返回错误 | 已复现输出帧预算缺口，待修复 |
| transport.rewrite_boundary | 有别名时成功 text/plain 仍透传；Connection指定头仍转发 | C/T：本地 httptest 反例分别观察到200和头泄漏 | 仅本地合成验证，不涉及真实凭据外泄事件；任务5修复 |
| state.previous_response_id | 当前无专属拒绝逻辑，名称映射为请求级 | C | 服务端保存的历史工具映射无法由当前资料保证；未验证，不新增普遍 422，不宣称完整支持 |

源码依据：`schema.go`、`tool_names.go`、`response_rewriter.go`、`server.go`；相关合成样例在对应 `_test.go` 文件中。只有在实际运行并记录提交及结果后，才为某个具体用例登记新的 T 证据。

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

- 状态：已定位、待统一修复；用户要求合并到优化方案，稍后一起解决。
- 发生时间：错误日志 2026-09-24 16:39:34；排查核实 16:54，Asia/Shanghai。
- 故障跳点：CLIProxyAPI → `127.0.0.1:18317/v1/responses`，响应 422，正文为 Adapter 的通用错误。
- 涉及工具：`mcp__codex_app.automation_update`；节点 `parameters/$defs/__schema20`，引用 `#/$defs/__schema2`，引用与自身都约束为 string。
- A/B 对照：该工具的完整 parameters 一致；不声称整份请求或全部工具树都相同。
- 源码依据：`schema.go` 的 `expandSchemaNode` 在展开前拒绝 `$ref` 相邻 type；`server.go` 将该错误统一映射成 422。
- 离线结果：故障请求拒绝；仅在内存中删除重复 type 后归一化通过（输出 469529 字节）；最小合成请求同样被拒绝。没有改写原日志，没有发送给上游。
- 运行态：Adapter healthz 204；运行二进制构建元数据为 fdd2415 且 vcs.modified=true，来源不是可精确等同的干净提交。离线诊断使用当前仓库源码（316b450 时的代码），不能把两者声称为同一构建。
- 归因：本次 422 为 Adapter 自身的保守拒绝规则；该工具参数未被 CLIProxyAPI 改动。此证据不能证明上游接受同一请求，也不能推广到其他 Schema 问题。
- 语义：本次引用目标与相邻 type 重复，移除重复约束用于诊断，不是“删除所有相邻字段”的生产修复方案。
- 退出条件：按主方案补齐约束组合与错误分类回归，验证修复代码；后续经授权部署并完成实际调用验证后才能标为已解决。

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

原版诊断结果为 `schema reference node has unsupported siblings`。本样例尚未写入生产测试文件；按用户要求随整体实施加入。

## 后续真实案例记录字段

case_id、rule_id、核实日期、组件版本、模型/路由范围、模式（无状态重放或服务端状态）、最小合成输入、原始状态/去敏错误、单项修改、修改后结果、契约依据、语义损失、归因与置信度、撤销条件。

以上字段用于后续登记；当前新增一项历史日志 L 观测及离线 T 对照，见 INC-20260924-422-ref-sibling。没有新增上游端到端成功证据。

## 执行计划的红灯验证（2026-09-24）

在临时副本中运行计划任务1、2、5的五个测试函数，四个函数按预期因行为缺陷失败，冲突类型负向测试通过。新增的T证据包括：重复类型/注解被拒绝、普通metadata.name被误改、SSE输出封装超限未被拒绝、Connection指定头被转发和未知成功媒体类型未失败关闭。

详细用例与结果见 `docs/superpowers/plans/2026-09-24-responses-compat-implementation.md`。没有修改生产测试或源码；未运行真实模型请求、未新增端到端成功证据，也未宣称完整测试套件已重新验证。
