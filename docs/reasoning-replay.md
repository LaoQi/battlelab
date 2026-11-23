# 思维链回传：兼容面、实测记录与实施方案

> 日期：2026-11-24（全部实测与文档核查都在这一天完成）。本文是《`responses` 协议思维链回传》的实测与决策记录：先给结论与方案，再逐节留档每一组测试（含失败、噪声与撤回过的主意），供后续复核与偏移时重测。

## 结论（一句话）

**回传形态只做一个，不按接入线分叉：正文双写（`content[].reasoning_text` + `summary[].summary_text` 各一份同文）、保留上游 `id`、不带 `status`、空正文整条跳过。** 依据是「按各家 input schema 的并集构造 + 实测各家对并集之外的键宽容 + 各家都不双计费」，不是取交集的最小能力子集。

```json
{"type":"reasoning",
 "id":"<上游 id，原样带回>",
 "summary":[{"type":"summary_text","text":"<正文>"}],
 "content":[{"type":"reasoning_text","text":"<正文>"}]}
```

## 为什么是这个形态

| 依据 | 指向 |
|---|---|
| 智谱 `InputReasoning`（`/api/v1/responses`） | 只有 `type` + `content`（`reasoning_text`）；没有 `summary`、没有 `id`、没有 `status` → **正文必须在 `content`** |
| Kimi `ResponsesReasoningItem` | `content` 与 `summary` 都收，文档写明「`content` 优先于 `summary`」→ 双写不冲突 |
| DeepSeek 文档 | 「明文 content 归并到相邻 assistant 消息」；`summary`、`encrypted_content` 输入不支持 |
| opencode 线实测 | `summary` 键缺失（或 null）→ 400 `input[].summary: expected an array` → **`summary` 键必须恒在** |
| LongCat 实测 | 两侧都空的 reasoning item → 400 `reasoning item requires summary, content, or encrypted_content` → **空正文必须跳过** |
| 7 条线实测 | `id` / `status` / `encrypted_content:null` / 摘要同文副本 **都不改变 `input_tokens`**（不双计费） |

`status` 不带：它只是 output item 的生成状态位（流式 `in_progress` → 终态 `completed`），我们只从终态 `output` 取 item，读它没有信息量；带上去等于多一处要跟上游 enum 对齐的耦合。智谱 input schema 里也没有它。

## 设计方案（已实施，2026-11-24）

### 改动清单

| 仓 | 文件 | 改动 |
|---|---|---|
| battlelab | `agent/llm.go` | 删 `ReasoningItem.FromSummary`（jsonl 键 `from_summary` 不再写入；旧文件该键被 `encoding/json` 忽略） |
| battlelab | `agent/llm_responses.go` | `responsesReasoningItem` → 上面的双写 + `id` 形态；`reasoningItemText` 只返回正文（不再回报来源形态）；`applyCompletedOutput` 去掉形态字段；delta 兜底两桶合并不再标形态（仍明文优先） |
| battlelab | `agent/llm_responses_test.go` | 改写 9 个用例：删 `FromSummary` 断言；新增整 item 形态断言 `assertReplayReasoningItem`（键集合 + 两侧同文 + id 有/无 + 无 `status`/`encrypted_content` 等多余键），工具循环回传 / 双写回传 / 旧 `from_summary` 键忽略三处复用 |
| battlelab | `CHANGELOG.md` | 置顶 `fix(agent)!:` 条目（BREAKING：删公开字段 `ReasoningItem.FromSummary`），依据本文 |
| battlelab | `AGENTS.md` | docs 清单加本文 |
| tanya | `repl/history_test.go` | 去掉 `FromSummary` 构造（字段已删）；**保留**含 `from_summary` 的 jsonl 字面量，改注释为「旧会话键被忽略、照常渲染」 |
| tanya | `docs/design.md` | 六处：请求构造 / 流式解析 / 思维链回传小节 / 「形态必须原样，不能归一化」段**整段重写**（与新方案相反，不是润色）/ 计费口径段 / 字段表删 `FromSummary` 条目 |
| tanya | `README.md`、`CHANGELOG.md` | 同步口径与条目 |
| 发版 | — | battlelab 提交标 `!`、发 **v0.6.0**；tanya `go.mod`/`go.sum` 同批升到 v0.6.0（根 AGENTS.md 规矩；`FromSummary` 已随 v0.5.1/v0.5.2 发布，删除是编译期破坏）。ivan 零引用，不动 |

### 各线净变化

「改前」以 v0.5.2 为基线（按来源形态回传、`id` 非空即带）。

| 线 | 改前 | 改后 | 变化 |
|---|---|---|---|
| DeepSeek（官方 responses） | 明文 + id | 明文 + 摘要副本 + id | `input_tokens` 不变（实测），行为不变 |
| 智谱 GLM（原生 responses） | 明文 + id | 明文 + 摘要副本 + id | 同线 `input_tokens` 不变；**跨线**会话里摘要来源的条目从 `+0`（不读 `summary`）变为全价计入（该线无前缀缓存）——收益是一致性，代价是成本 |
| Kimi K3 | 摘要 | 双写（content 优先） | `input_tokens` 不变，生效不变 |
| LongCat | 按来源 | 双写 | 不变（两形态都生效） |
| opencode / Opencode Zen | 按来源 | 双写 | `summary` 键恒在，满足硬约束 |
| 火山方舟 | 按来源 | 双写 | 不变（两形态都不生效） |

缓存：双写不增加 token 序列，**稳态不改变前缀缓存命中**；改动本身是跨版本变化（`summary[]` 由空变非空），每个活跃会话的首个请求 miss 一次，属项目已裁定的可忽略代价。

### 验证计划

1. `battlelab`：`go build/vet ./...`、`go test ./...`、`go test -race ./agent/`、`make cross`。
2. `tanya`：`go build/vet ./...`、`go test ./...`。
3. 端到端 7 条线：用 Go 侧 `buildResponsesInput` 产出的真实请求体打网关，断言 200，且 `input_tokens` 与「仅 content」一致（R4 已测，此处复核 Go 序列化产物被各线接受）。
4. 变异复验 5 处：只写 `content` 侧、去掉 `summary` 键、空正文不跳过、多带 `status`、丢 `id` → 对应用例必红。

落地结果（2026-11-24）：1、2、4 全部通过（5 处变异均被拦下）；3 见下。

### 端到端复核（落地后，Go 序列化产物）

请求体由 `buildResponsesInput` + `responsesRequest` 真实序列化产出（`stream:false`，history 含一条带 `id` 与约 200 字正文的
reasoning item），「仅 content」变体只把该 item 的 `summary` 改为 `[]`，其余逐字节相同；经网关逐线发送，比对状态码与 `input_tokens`。

| 线 | 双写 | 仅 content | `input_tokens` |
|---|---|---|---|
| glm-5.3（智谱原生） | 200 | 200 | 127 = 127 |
| glm-5.3-flash | 200 | 200 | 127 = 127 |
| k3 / k3-256k | 200 | 200 | 227 = 227 |
| LongCat-2.5 | 200 | 200 | 130 = 130 |
| deepseek-flash | 200 | 200 | 44 = 44（no-op） |
| ark-dsv41f | 200 | 200 | 44 = 44（no-op） |
| oc-dsv41-flash | 200 | 200 | 44 = 44（no-op） |
| oc-glm53-flash | 200 | 200 | 127 = 127 |

9 条线全部接受 Go 侧双写形态，`input_tokens` 与「仅 content」逐一相等（不双计费）。另用真实客户端（`ChatStream`，`stream:true`）
对 glm-5.3-flash 与 oc-glm53-flash 两条硬约束线各跑一轮，均 200 且正常回传。**探针注意**：opencode 两条线经 Cloudflare，
Python urllib 默认 UA 会被 `error code: 1010` 拒收（与请求体无关，两种变体同时 403），换常规 UA 即 200。

### 风险与回退

- **并集策略的单点假设**：并集之外的键靠上游宽容（智谱对 `summary`/`id`，各线对 `id`/`status`）。这不是本方案新增的假设——v0.5.2 已经在给智谱发其 schema 外的 `summary: []`，靠的是同一宽容。若某线启用严格校验（`additionalProperties: false`），去 `summary` 破 opencode、去 `content` 破智谱，并集不可维持，只能回到按接入线分叉。
- **未测线双读双计费**：双写相对单写唯一真正新增的运行时风险。7/7 已测线都取其一，概率低；可观测（该线 `input_tokens` 翻倍立即可见）；回退为单写是局部改动。**接入新线的核对项**：对比「仅 content」与「双写」的 `input_tokens`，不等即该线双读、不可用并集策略。
- `id` 在智谱原生 responses 上是忽略而非拒收（实测）；若某线将来要求 `id` 合法，回退即一行。
- 智谱原生线只能经内网聚合网关验证（网关直连其 `api/v1/responses`）；直连公网 `open.bigmodel.cn` 需独立 key，本轮未做。
- 回退方式：单提交 revert，无配置/数据迁移（旧 jsonl 可读）。

---

## 实测记录

> 环境：2026-11-24，Go `agent` 的 `responses` 请求构造 + 真实网关/官方端点。请求体一律 `store:false`、`stream:false`，`reasoning.effort` 按需。以下「网关」指内网聚合网关（`/v1/responses` 直连各家上游的 `/responses`）。

### R1 DeepSeek 文档口径（api-docs.deepseek.com）

- input items 表：`reasoning` 支持，**明文 content 归并到相邻 assistant 消息**；`summary`、`encrypted_content` 不支持。
- 顶层参数：`store` 不支持（响应恒 `store:false`）、`previous_response_id` 不支持、`truncation` 不支持、`prompt_cache_key`/`prompt_cache_retention` 不支持。
- 流式事件含 `response.reasoning_text.delta/.done`；结束事件 `response.completed/incomplete/failed`，无 `data: [DONE]`。
- usage：`input_tokens_details.cached_tokens`、`output_tokens_details.reasoning_tokens`。

### R2 DeepSeek 实测：回传 reasoning item 是 no-op

官方直连 `api.deepseek.com`（`/responses` 与 `/v1/responses` 行为相同）与网关均一致。

**624 字符正文（含唯一事实 PELICAN-9）**：

| 变体 | `input_tokens` | 事实被读出 |
|---|---|---|
| baseline 无 item | 60 | 否 |
| 明文，在 assistant 之前 | 60 | 否 |
| 明文，在 assistant 之后 | 63 | 否 |
| 明文 content 块用 `input_text` | 60 | 否 |
| content 直接给字符串 | 60 | 否 |
| 缺 `summary` 键 | 60 | 否 |
| 带 `id`+`status` | 60 | 否 |
| 摘要形态 | 60 | 否 |
| 对照：同一事实放 user 消息 | 60 | **是** |

**真实端到端**（抓官方自己产出的 reasoning item：276 字明文 + 合法 `id` + `encrypted_content`，原样/仅 content/仅 encrypted 三种回传）：均 `input_tokens=92`（等于不回传的 92），事实判据全部失败。

结论：DS 文档承诺的「明文归并」在当前实现里不可观测；回传对 DS 是「不计费、不进上下文、不影响缓存」的纯 no-op。**这是唯一一家文档与实测不一致的线。**

### R3 网关结构与 proxy 边界

- `/api/agent/providers`：公开厂商与 `base_url`（DeepSeek `api.deepseek.com`、Kimi `api.kimi.com/coding/v1`、GLM `open.bigmodel.cn/api/coding/paas/v4`、Opencode Go `opencode.ai/zen/go/v1`、火山 `ark.cn-beijing.volces.com/api/v3`、LongCat `api.longcat.chat/openai/v1` 等）。
- `/api/agent/models`：`oc-glm53-flash` 映射到 **Opencode Go**（不是智谱）；`glm-5.3*` 映射到 GLM；`k3` 映射到 Kimi。
- 正式 `/v1/responses` **直连上游 `/responses`**（不是 chat 转译）：网关指南的跨后端注意事项按各家 responses 端点差异书写；响应 `id` 风格即上游风格（GLM 的 `resp_<时间戳><hex>` 与其 chat id 同款、DS 为 uuid、Kimi/LongCat 各自格式）。
- `ANY /api/agent/providers/{id}/proxy/{path...}` 只能打到 `base_url` 字面拼接的路径（8 MB、10 s 超时）：`proxy/models`、`proxy/chat/completions` 200；`proxy/responses` 404（GLM 的 responses 在 `api/v1`，与 coding base 不同前缀）；`../../` 穿越被网关规范化，**无法用 proxy 直连智谱原生 responses**。
- `store` / `previous_response_id` 经网关不透传：4 条线（glm/deepseek/k3/LongCat）第二轮凭 `previous_response_id` 全部答不出第一轮内容。→ 自回传全历史是唯一路径。

### R4 计费矩阵（同一正文，各线 `input_tokens`）

**A 组（1760 汉字 ≈ 983 token）**

| 线 | 无 item | 明文 | 明文+id | 摘要 | 空 item |
|---|---|---|---|---|---|
| deepseek-flash / oc-dsv41-flash / ark-dsv41f / vole / auto-* | 35 | 38 | 38 | 35 | 35 |
| glm-5.3 / glm-5.3-flash | 16 | **980** | 980 | 16 | 16 |
| k3 / k3-256k | 100 | **1083** | 1083 | **1083** | 100 |
| LongCat-2.5 | 12 | **1063** | 1063 | **1063** | 空 item 直接 400 |

**B 组（624 字符，含 id/status/encrypted/缺 summary/双写）**

| 线 | 无 item | 仅 content | content+id | content+summary 双份 |
|---|---|---|---|---|
| deepseek-flash（官方直连） | 51 | 51 | 51 | 51 |
| k3 | 147 | 303 | 303 | 303 |
| LongCat-2.5 | 36 | 204 | 204 | 204 |
| ark-dsv41f | 51 | 51 | 51 | 51 |
| oc-glm53-flash | 34 | 190 | 190 | 190 |
| glm-5.3-flash / glm-5.3 | 43 | 201 | 201 | 201 |

**C 组（GLM 线专项，390 字符）**：无 item 43 / 明文 201 / +id 201 / +id+status 201 / +encrypted:null 201 / 缺 summary 键 201 / 双份 201。`oc-glm53-flash` 缺 `summary` 键 400（其余变体 190）。

结论：各家只把 `content`/`summary` 当同一份正文的两个视图，取其一（`content` 优先），**双写不双计费**；`id`/`status`/`encrypted_content` 全被忽略且零成本。

### R5 append-only 与缓存（合成内容、唯一 seed、三轮）

| 线 | 模式 | turn1 | turn2 | turn3 | 命中/上轮 |
|---|---|---|---|---|---|
| deepseek-flash | 回传 | 485 | 935 | 1377 | 79.2% / **95.8%** |
| deepseek-flash | 不回传 | 472 | 922 | 1358 | 81.4% / **97.2%** |
| k3 | 回传 | 585 | 1487 | 2374 | 87.5% / 86.1% |
| k3 | 不回传 | 589 | 1112 | 1614 | 86.9% / 92.1% |
| glm-5.3-flash | 回传 | 429 | 1244 | 2072 | 0 / 0 |
| glm-5.3-flash | 不回传 | 428 | 867 | 1274 | 0 / 0 |

另一组 seed（deepseek-flash）：回传 485/935/1377 对不回传 472/922/1358，`cached` 两版完全相同（384 → 896）。→ DS 线回传对缓存零影响；k3/GLM 线回传只增体积（k3 每轮 +375 token 量级、命中率 92% → 86%），GLM 线本就无可依赖的前缀缓存（全价）。

### R6 行为判据（是否真被上游读入）

方法：把「回答末尾必须带 `_RSMARK_`」或唯一事实写进回传正文，看回答是否体现。

| 线 | 明文回传 | 摘要回传 | 不回传 |
|---|---|---|---|
| glm-5.3-flash / glm-5.3 / oc-glm53-flash | 生效（`input` +237） | glm 原生：不生效（+0）；oc-glm53：生效 | 不生效 |
| k3 | 生效（+216） | 生效 | 不生效 |
| LongCat-2.5 | 生效（+236） | 生效 | 不生效 |
| deepseek-flash（官方） | **不生效** | 不生效 | 不生效 |
| ark-dsv41f | 不生效 | 不生效 | 不生效 |

**判据噪声记录**：同一形态在不同变体间 `obey` 会跳动（如 GLM 双份变体 `obey=False` 但事实被引用），且 `max_output_tokens` 过小时思考会吃满输出、答案为空造成「不生效」假象。**判断是否计入只认 `input_tokens`；判断是否读入要多次复测，不采信单次。**

### R7 会话样本体量（本地 31 个会话 jsonl）

- 思维链正文 / 全部字节：整体 6.6%（含修复前无正文的旧会话）。
- 有正文的样本：思维链占会话 12.9%–57.4%，最极端一例 **207%**（思维链比其余内容总和还长）。
- → 在「计入」线上回传全量历史的成本台阶可以到「prompt 翻倍」级别，这是 `recent:N`/`off` 类策略（本轮**不做**）的唯一真实理由。

### R8 智谱文档口径（docs.bigmodel.cn）

- 独立协议：`POST https://open.bigmodel.cn/api/v1/responses`；`store` + `previous_response_id` 支持（7 天有效期）。
- `InputReasoning`：required `type` + `content(reasoning_text)`；无 `summary`/`id`/`status`/`encrypted_content`。`OutputReasoning`：`type`/`id`/`status`/`content`。
- 上下文缓存：隐式前缀缓存、命中按 50% 价、前缀建议 ≥500 token、异步生效；`usage.input_tokens_details.cached_tokens`。
- 思考计入**输出** token；`reasoning.effort` 支持（GLM-5.3 系强制思考，low/high/max）。

### R9 Kimi 文档口径（platform.kimi.com/docs）

- `ResponsesReasoningItem`（输入回放）：`content` 与 `summary` 都收，**`content` 优先于 `summary`**；可选 `id`、`status`（仅 `completed`）。
- `ResponsesOutputReasoningItem`：只有 `summary`（`encrypted_content` 恒 `null`）。
- `store` 固定 `false`、`previous_response_id` 恒 `null` → 无状态，必须自回传历史。
- 缓存：隐式，TTL `5m`/`1h`，`cache_write_tokens` **写入也计费**，支持 `prompt_cache_key`。
- chat 线 Preserved Thinking：历史 `reasoning_content` **计入输入 token**（明确文档口径）。

---

## 未决与待验

- 智谱原生 `store`/`previous_response_id` 经网关不透传，无法验证其服务端多轮能力；直连公网需独立 key（本轮未做）。
- DeepSeek 文档的「明文归并」是否只在 Codex 客户端特定形态下成立，未定位。
- OpenAI 官方 `/responses` 未接入：那边要求 reasoning item 原样回放（含 `encrypted_content`），只回文本大概率 400；接入时再重估 `status`/`encrypted_content`。
- `oc-glm53-flash` 走 Opencode Go（第三方转发），其「摘要也生效、缺 `summary` 键 400」是该线自身行为，不是智谱口径。
