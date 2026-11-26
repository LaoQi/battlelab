# 生命周期钩子（`agent.WithHooks`）

> 归属：本仓 `agent` 包。状态：**已实施**（2026-11-26）。
> 相邻文档：`agent-event-seams.md`（出站观察面 = 事件）、`session-handoff.md`（交接方法的语义）、`extraction.md`（抽取边界）。

## 1 定位

钩子是**生命周期回调**：库在固定时机把只读快照交给调用方，调用方在该时刻做自己的事（通常是一次 `Agent` 公开方法调用）。它**没有返回值、不参与控制流**——库不读取、不短路、不据此改变回合走向。

四条面的分工：

| 面 | 装配 | 方向 | 能力 |
|---|---|---|---|
| `Option` | 装配期 | — | 决定组件与配置 |
| `Tool` | 装配期 | 模型 → 库 | 模型可调用的动作 |
| `EventSink` | 装配期 | 库 → 调用方（只读、高频、不得阻塞/panic/反向调用） | 观察过程 |
| **`Hook`** | 装配期 | 库 → 调用方（可阻塞、可在该时刻调用公开方法） | 在指定时机做调用方自己的事 |

**两面正交**：钩子机制不携带任何业务概念（无「通知」「停止」「交接」字段）。这类动作属 `Agent` 的公开方法面，其校验、时机与失败语义由各方法自负——例如 `RequestHandoff` 只是置 pending，回合出口才落地（见 `session-handoff.md`）。

## 2 切点

八个接口，一个实例可实现任意多个；同一实例在每个切点按注册顺序被调用。

| 接口 | 时机 | history 尾部 | 在回合中间写 history 的安全性 |
|---|---|---|---|
| `BeforeTurnHook.BeforeTurn` | `Ask` 追加用户消息之后 / `Continue` 入口，尚未发请求 | user | 安全 |
| `BeforeRequestHook.BeforeRequest` | 每轮请求发出前（`EventRequestStart` 之后、构造请求之前） | user / tool | 安全（上一轮整批 tool 消息已落地） |
| `AfterResponseHook.AfterResponse` | 响应已入 history、工具尚未执行（`EventResponse` 之后） | 含 `tool_calls` 的 assistant | **不安全**：此点追加 user 消息会破坏 `tool_calls → tool` 相邻性 |
| `BeforeToolHook.BeforeTool` | 每个工具调用前（`EventToolStart` 之后） | 批次只落了部分 tool | **不安全**（同上） |
| `AfterToolHook.AfterTool` | 每个工具调用后（`EventToolEnd` 之后） | 同上 | **不安全**（同上） |
| `AfterTurnHook.AfterTurn` | 回合出口（`EventTurnEnd` 之后、交接应用之前） | 回合完整 | 安全 |
| `SessionHook.SessionEvent` | 会话边界切换之后（`SessionNew`/`SessionLoad`/`SessionAdopt`/`SessionFork`/`SessionSwitchWorkspace`/`SessionHandoff`） | — | 安全 |
| `AfterSaveHook.AfterSave` | 每次落盘尝试之后（成功与失败都呼叫，`SaveInfo.Err` 告知） | — | 安全 |

顺序规则一句话：**事件先、钩子后**——有对应事件的切点一律在事件 emit 之后调用（`BeforeRequest` 因此在 `EventRequestStart` 之后、真正构造请求之前，注入的内容能进本次请求）。

`AfterSave` 与 `SessionEvent` 不在回合上下文里（`AppendNotice`、`Fork` 等公开方法本身没有 ctx），这两类钩子收到 `context.Background()`，取消信号不适用；其余切点收到的是**回合 ctx**（回合被取消/中断时，钩子里发起的外部调用也随之取消）。

## 3 装配

```go
type Hook any
func WithHooks(hooks ...Hook) Option   // 仅 New 生效；多次调用累加
```

- **只认接口实现**：`New` 里对每个实例做类型断言，按它实现的切点接口登记；不提供适配器/构造器函数
- 一个实例可实现多个切点接口（在每个切点都参与）；同一切点多个实例的顺序 = 注册顺序
- `nil` 元素被忽略；一个切点都没命中的实例 → `New` 返回 `不支持的生命周期钩子类型 %T`
- 装配期固定：没有运行期注册/注销，也没有顺序配置

## 4 调用契约

- **同步、在发起 `Ask`/`Continue` 的同一 goroutine 上调用**：库不加锁、不保证并发语义
- **可阻塞**：库不加超时、不做看门狗；钩子内耗时完全由调用方负责（要限时自己 `select ctx.Done()`）
- **不得 panic 逃逸**：库不做 `recover`（与 `EventSink` 同一约定）
- 钩子内**不得**调用会撕碎回合假设的方法：`NewSession`/`LoadSession`/`AdoptSession`/`Fork`/`SwitchWorkspace`/`SetModel`/`FreezeSession`。库不拦（调用方必然持有 `*Agent`，类型上封不住），属约定
- 注入内容（`AppendNotice` 等）不需要额外保存：各方法自带落盘，下一轮构造请求时自然带上
- 回合开始钩子在 ctx 已取消时仍会被调用（`BeforeTurn` 在 `Ask` 入口，库此处不判 ctx）；`BeforeRequest` 之前库会先判 `ctx.Err()` 并直接返回取消错误，故已取消的回合不会触发请求前钩子

## 5 入参

```go
type TurnStart struct   { Continue bool; Input string; SessionID string; History func() []Message; Stats func() Stats }
type RequestInfo struct { Turn int; SessionID string; History func() []Message; Stats func() Stats }
type SessionEvent struct{ Kind SessionEventKind; SessionID string; Workspace string; Handoff *Handoff }
type SaveInfo struct    { Session string; Messages int; Err error }
```

- `Turn` 是本回合内第几次请求（0 起，工具轮之后递增），不是跨回合计数
- `History`/`Stats` 是惰性访问器：返回库内部切片与现算统计，**只读**，不得修改
- `AfterResponse` 的第二个参数是 `ResponseInfo`（`Duration`/`Usage`/`ContextTokens`）
- `BeforeTool`/`AfterTool` 传模型侧原始 `ToolCall` 与 `ToolResult`
- `SaveInfo.Session` 在 `NoSave`（`-n`）下为空
- `SessionEvent.Handoff` 仅在 `SessionHandoff` 时非空（已发生的交接事实）

## 6 典型用法

| 需求 | 做法 |
|---|---|
| 上下文达阈值提醒 | `BeforeRequest` 里判 `Stats().Est` 与 `Stats().ContextTokens`，够阈则 `AppendNotice(...)` |
| 外部触发转段 | `BeforeRequest` 里 `RequestHandoff(summary, cont, ws)`（方法自带校验与 pending 语义，回合出口生效） |
| 回合耗时/用量记账 | `AfterTurn` 里读 `TurnInfo` 与 `Stats()` |
| 落盘审计 | `AfterSave` 里读 `SaveInfo` |
| 危险命令观测 | `BeforeTool` 里读 `ToolCall`（要拦就得自己包 `Tool`，不是钩子） |

## 7 不做

- 无返回值、无 `Stop`/`Skip` 之类控制决定：中止回合走 `Ask` 的 `ctx`，转段走 `RequestHandoff`
- 不校验钩子的写入行为、不做回合内注入防护（文档划线，调用方自负）
- 不做运行期注册/注销、不做钩子顺序配置
- 不替代 `EventSink`：高频增量（content/reasoning delta）仍走事件
