# Responses Compat 兼容性证据登记

核实日期：2026-09-24（Asia/Shanghai）。本文件记录已核实资料与证据缺口，不将改写策略当作根因证明。

## 证据类型

- D：已读取官方文档；说明公开契约，不证明具体部署行为。
- C：当前仓库源码/历史；说明 Adapter 的实现，不证明原始请求由哪层产生。
- T：本地合成测试；证明受测实现，不证明真实提供方。
- L：同一条件的真实链路对照；记录版本、路由及日期后才用于具体归因。

各类型独立，不把文档、测试和真实行为混成一个“已验证”标签。本轮未执行真实模型请求、未检查私人请求日志、未重新运行代码测试。前序会话的测试结果仅作历史信息。

## 问题与规则登记

| ID | 当前行为/问题 | 已有证据 | 尚缺证据与允许结论 |
|---|---|---|---|
| schema.inline_local_refs | schema.go 展开本地引用 | C；D1/D2 | 未取得原始失败请求；不能断言所有失败都是上游不支持引用 |
| schema.recursive_empty | 递归回边替换为 `{}` | C；D1/D2 支持递归公开能力 | 这是放宽约束；合法递归不等于不标准，具体上游失败仍待 L |
| tools.structured_traversal | 处理嵌套工具与 additional_tools | C；D1/D3 | 字段已有公开说明；具体模型支持和代理是否扁平化待 L |
| tools.name_alias | tool_names.go 按 len(name)>64 改名并建立映射 | C；D5 的 64 限制属于 Chat Completions | 无当前 Responses 对应字段长度约束的充分证据；无 A/B 名称对照；不认定 Codex 或上游违规 |
| reasoning.drop_id | schema.go 删除输入 reasoning 项的 id | C；D4 说明无状态 reasoning 重放 | 缺同账号/同路由 ID 来源和前后对照；“provider ID 不稳定”仅为待证假设 |
| transport.sse_flush | SSE 响应头提前刷新 | C：fdd2415，server.go | 属于 Adapter 自身处理，不归责于 Codex/上游 |
| response.name_scope | 当前 response_rewriter.go 按任意 name 递归恢复 | C | 通用化需增加非工具对象反例并按对象语义收窄；尚未在本轮证明真实误改事件 |
| response.full_frame_limit | 原始帧及 JSON 恢复已有预算 | C | 需针对完整重写帧封装开销新增边界测试，不能声称现实现已完整满足最终契约 |
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

## 最小归因流程（待独立授权执行）

1. 使用无私人内容的最小合成用例，记录客户端、CLIProxyAPI、Adapter 的版本与上游模型 ID。
2. 对照 A（客户端出站）、B（CLIProxyAPI 转发至 Adapter）和 C（Adapter 出站及上游响应）的必要结构。授权/会话信息不进入证据文件。
3. 固定路由与账号条件，每次只启用一条规则；保留原始状态码、去敏错误类别和结构差异。
4. 若 A 正确而 B 改坏，归因中间层；若 A 违反明确适用的契约，归因客户端/工具注册；若请求满足上游明确承诺而被拒绝，才归因上游实现。
5. 若只是支持范围不同，记录能力差异；若缺少 ID 来源、跨轮身份或协议依据，保留“待归因”。
6. 把可重现的必要结构人工去敏后转为合成回归样例，记录语义损失与撤销规则条件。

不得自动采集全部请求、提交完整日志或为验证读取无关私人会话。真实上游成功一次也不等于对该 provider 的全功能兼容认证。

## 后续真实案例记录字段

case_id、rule_id、核实日期、组件版本、模型/路由范围、模式（无状态重放或服务端状态）、最小合成输入、原始状态/去敏错误、单项修改、修改后结果、契约依据、语义损失、归因与置信度、撤销条件。

这是后续登记契约，不是已有真实案例；本轮没有新增 L 证据。
