# 外部强制复位（`agent.ForceHandoff`）

> 与 `next_session` / `RequestHandoff` 是**两条不同的路**：后者由模型在回合内自驱动、置 pending、回合出口才落地（见 `session-handoff.md`）；本文这条由**外部调用方**发起、回合外同步执行、一次调用即落地。两者共用同一段落地代码（`settleHandoff`），语义与失败面各自独立。

## 1 定位

`ForceHandoff` 是对 Agent **整个状态的一次功能复位**。调用方（常驻后台的调度器）在上下文达阈值、模型又不肯自己交接时，用它一步完成「取摘要 → 旧会话落地 → 新会话成立」。

三个核心指标（违反即回滚该改动）：

1. **旧数据安稳落地**：旧会话 append-only，只多一条移交事实行，不裁剪、不改写、不重排。
2. **摘要数据正常**：摘要来自一次真实请求且非空、不超限；取不到摘要 → 整次复位不成立，状态与文件零变化。
3. **新会话状态正常**：会话 id 轮转、history 只含摘要、`prompt`/`stats` 复位、文件与新 system 快照齐备、`SessionEvent(SessionHandoff)` 上报。

## 2 接口

```go
type ForceHandoffOptions struct {
	Instruction string // 必填：调用方给模型的摘要指令（库零默认文案）
	Continue    bool   // 只回填 Handoff.Continue，库自己不续跑
}

func (a *Agent) ForceHandoff(ctx context.Context, opt ForceHandoffOptions) (Handoff, error)
```

- `Instruction` 为空白 → 报错，不发请求。库不预设「写摘要」之类文案：复位是外部行为，摘要口径由调用方决定。
- 回合进行中（`Ask`/`Continue` 未返回）调用 → 报错 `回合进行中不能强制复位状态`。库用内部 `turning` 标志守这条不变量，不靠文档约束。
- 归档只读会话 → 返回 `ErrArchiveReadOnly`。
- `NoSave`（`-n`）下允许：内存复位，`Handoff.NoSave` 为真。
- 当前会话没有历史 → 报错，不发请求。

## 3 实现顺序

```
护栏（回合中 / 归档只读 / 指令必填 / 有历史）
  → 摘要请求：system 快照 + summaryMessages(history) + 指令 user
      一次全新的 LLMRequest（无工具、无 sink）；不写 history、不落盘、不计 stats、
      不触发任何请求切点钩子
  → 校验摘要（归一、非空、长度上限 = handoffSummaryLimit）
  → settleHandoff(pendingHandoff{summary, cont})：与回合出口路径同一段落地代码
      快照旧会话（id/file/stats/条数）→ 旧 history + 移交事实行 → save
      → rotateTo(nextID) → history = [交接摘要] → prompt/stats reset → save
      → SessionEvent(SessionHandoff)
  → 返回 Handoff 事实（不写 lastHandoff，不占 TakeHandoff 通道）
```

摘要请求排在所有写动作之前——这就是「失败零副作用」的来由：任何一步在动旧会话之前失败，Agent 状态与文件都不变。

## 4 摘要请求的形态

`summaryMessages` 把 history 整理成一次性请求可用的形态：

| 处理 | 规则 |
|---|---|
| 工具轮 | 带 `ToolCalls` 的 assistant 与随后的 `tool` 消息整段省略，换成一行占位 `[已省略工具调用：run_shell ×2, calc ×1]`；assistant 原有正文保留在占位行之前 |
| 无主 `tool` | 直接省略（异常中断的回合会留下没有发起者的 tool 消息） |
| 图片 | `user.Images` 去掉，正文补 `[图片 N 张]` 计数占位 |
| 思维链 | 清空 `ReasoningContent`/`ReasoningItems`/`Usage`/`Duration`/`TS` |

请求本身是一份**全新的 `LLMRequest`**——`a.client.Request(ctx, LLMRequest{Messages: msgs}, nil)`，`Tools` 为零值即本次不带工具；无 sink（内容事件丢弃），沿用配置的模型/温度/effort/协议/重试策略。`Client` 不持有任何请求形态（不含工具集），所以「这一次不要工具」不需要开关参数。不做预算截断——调用方语义就是全量历史；上游上下文超限即失败、由调用方决定降阈值或拆任务，库不自动降级重发。

## 5 失败面

| 情形 | 行为 |
|---|---|
| `Instruction` 空白 | 报错，不发请求 |
| 回合进行中 | 报错，不发请求 |
| 归档只读会话 | `ErrArchiveReadOnly` |
| 会话无历史 | 报错，不发请求 |
| 摘要请求失败（网络/上游/ctx 取消） | 返回错误；session id、history、文件全不变 |
| 摘要为空或超 `handoffSummaryLimit` | 返回错误；同上 |
| 落地期磁盘失败 | 返回错误；旧会话只会多一行事实行，不丢内容（不引入文件事务） |

## 6 与既有面的关系

- **与 `RequestHandoff`（模型自驱动）**：互不替代。`RequestHandoff` 只在回合内可用、靠 pending 在出口生效；`ForceHandoff` 在回合外同步落地。落地逻辑共用 `settleHandoff`，交接事实结构与事件一致。
- **与钩子**：一次性请求不是回合、不是请求切点，**不触发** `BeforeRequest`/`AfterResponse`；`SessionHandoff` 事件照发（它是已发生的会话边界事实）。
- **与 `TakeHandoff`**：外部路径把事实直接返回给调用方，不写 `lastHandoff`，避免表现层把同一次交接消费两遍。
- **与 `Stats`**：摘要请求的用量不计入 `Stats()`（它不属于本会话的对话上下文），需要记账的调用方自己从自己的用量统计里累计。

## 7 不做

- 不做预算截断、不做超限自动降级重试
- 不提供工作区参数（跨项目复位仍走 `next_session` 的 `workspace`）
- 不把摘要写进旧会话（旧会话只多一行移交事实行）、不复位后自动续跑（由调用方决定）
- 不引入文件事务（先临时文件再原子 rename）
- 不把一次性请求做成通用的公开「随便问一次」接口：它的契约只有「为复位取一份摘要」
