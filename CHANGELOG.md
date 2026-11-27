## 2026-11-27 refactor(tools): 三处工具描述去掉实现细节与自相重复

原则：**描述只写模型做决策需要的因果，不写宿主实现，也不在同一份定义里说两遍**（描述与 `params` 各一遍时，
模型两处都读，冲突时还得以描述为准）。

- `run_shell` 的 `interactive` 参数：删掉「（Linux 独立 pty、Windows 继承控制台），停用等待动画，默认超时放宽」
  ——前两项是宿主实现，第三项是表现层行为，第四项与 `timeout` 参数描述（`默认 60（interactive 时 300）`）重复。
  改为「命令需要用户在终端应答（sudo/ssh/gpg/read 等交互提示）时置 true，否则命令拿不到终端、无法应答」：
  只留因果与后果
- `next_session`：描述里 `continue`/`workspace` 的语义与 `params` 重复，「上下文接近上限、或某一阶段目标已完成
  需要转段时使用」则是调用时机建议——那是 system 提示词的职责（tanya 的 `system_prompt.md` 已有同句）。
  描述只留 `params` 表达不了的副作用：摘要进新会话、本回合正常结束时生效、旧会话不再追加
- `agent_custom`：`action` 三个分支与 `key` 的可写/只读清单在描述与 `params` 各写一遍，描述只留两条行为约束
  （改动仅本次会话有效、切模型后 prompt cache 不复用），语义交给 `params`
- 测试同步：`TestRunShellParamsGolden`、`TestAgentToolDescGolden` 的整串期望更新；
  `TestNextToolDefinitionHasWorkspace` 删掉「描述应含 workspace」一条（`params` 含 workspace 已由同测试前半句断言）
- 验证：`gofmt -l` 干净；battlelab 7 包、tanya 16 包、ivan 全仓 `go test ./...` 全绿
- **生效范围**：本仓未发版，改动只在 `go.work` 联调下生效；tanya/ivan 的 `go.mod` 仍指已发布版本，
  要带到前台需发新 tag 并升 `go.mod`

## 2026-11-26 feat(agent): 钩子 ctx 携带 `Agent`（`agent.FromContext`）

钩子要在自己的时机做事（注入提醒、请求转段、记账），过去只能靠外面闭包捕获 `*Agent`——装配顺序被迫
「先有 Agent 才能写钩子」的循环，库外也没法在钩子里拿到实例。改为**由库在钩子边界把实例放进 ctx**。

- `agent/hooks.go`：私有键 `agentCtxKey{}` + `FromContext(ctx) (*Agent, bool)` + `Agent.hookCtx`；
  `fire*` 八处调用点统一走 `a.hookCtx(ctx)`（`AfterSave`/`SessionEvent` 两处是
  `hookCtx(context.Background())`，它们本就不在回合上下文里）
- **只在钩子边界注入**：回合 ctx 本身不被改写，工具与 LLM 请求拿到的 ctx 不含该值（`context.WithValue`
  返回新 ctx，调用方传入的 ctx 也不会被污染），用例锁住两条不泄漏路径
- 约束不变：钩子仍**无返回值、不参与控制流**，仍不得调 `NewSession`/`LoadSession`/`Fork`/
  `SwitchWorkspace` 等撕碎回合假设的方法——只是从「调用方必然持有 `*Agent`」变成「钩子里能取到」
- 测试：`agent/hooks_test.go` +2（三个切点均取到同一实例、仅靠 ctx 里的实例注入且进本次请求、
  工具 ctx 与调用方 ctx 均取不到）
- 文档：`docs/agent-hooks.md`（§2 时机表补 ctx 条、§4 调用契约、§5 入参、§6 典型用法）、`AGENTS.md`

## 2026-11-26 feat(agent): 外部强制复位 `ForceHandoff`——一次调用完成摘要、旧会话落地与新会话成立

模型自驱动的 `next_session` 只能在回合内请求、由回合出口生效，且依赖模型听话。常驻后台场景需要外部在
上下文达阈值时**强制**复位整个 Agent 状态，故新增这条独立通路（与 `next_session` 不互相替代）。

- `agent/forcehandoff.go`（新）：`ForceHandoffOptions{Instruction, Continue}` 与
  `Agent.ForceHandoff(ctx, opt) (Handoff, error)`。**调用方提供摘要指令，库零默认文案**；回合外同步执行，
  一次调用内完成「取摘要 → 旧会话落地 → 轮转新会话」
- 三个核心指标：①旧数据安稳落地（旧会话 append-only，只多一条移交事实行）②摘要数据正常
  （请求失败/为空/超 `handoffSummaryLimit` → 整次不成立）③新会话状态正常（id 轮转、history 只含摘要、
  `prompt`/`stats` 复位、新文件与 system 快照齐备、`SessionEvent(SessionHandoff)` 上报）
- **摘要请求是一次性的**：system 快照 + 全量 history（不作预算截断）+ 指令 user；工具轮整段省略并留占位
  `[已省略工具调用：run_shell ×2]`、无主 `tool` 消息省略、`Images` 换成 `[图片 N 张]`、思维链清空；
  **不带工具定义**、无 sink。不写 history、不落盘、不计 `Stats`、不触发请求切点钩子
- **`Client` 与请求形态解耦（BREAKING）**：`Client` 只留链路（端点/鉴权/协议/SSE/重试），不再持有工具集；
  新增 `LLMRequest{Messages, Tools}` 描述一次请求的完整形态（零值 `Tools` 即本次不带工具——请求自身的形状，
  不是开关参数），入口改为 `Client.Request(ctx, LLMRequest, sink)`；`NewClient(cfg, tools)` → `NewClient(cfg)`、
  `ChatStream(ctx, msgs, sink)` → `Request(...)`。内部 `request` 改名 `exchange`（单次发送，重试在 `Request`）。
  库外无消费者（tanya/ivan 都只用 `Agent`）
- 回合请求与复位请求因此都是**显式构造的一份新请求**：`LLMRequest{Messages: a.buildMessages(), Tools: a.tools.defs()}`
  与 `LLMRequest{Messages: msgs}`
- 落地去重：`applyHandoff` 拆出 `settleHandoff(p *pendingHandoff) (Handoff, bool)`——回合出口路径与
  外部强制路径共用同一段落地代码；外部路径**直接返回事实**、不写 `lastHandoff`（避免表现层消费两遍），
  `TakeHandoff` 仍只服务模型自驱动路径
- 新护栏：`Agent.turning`（`Ask`/`Continue` 置位，`defer` 清除）——回合进行中调用 `ForceHandoff` 直接报错；
  归档只读会话返回 `ErrArchiveReadOnly`；无历史/空指令不发请求
- 校验去重：新增 `checkSummaryLen`，`RequestHandoff` 与摘要请求共用长度上限判定

**行为变化**：`Ask`/`Continue` 仅多一个内部标志位的置位与清除，外部可观察行为不变。

**测试**（`agent/forcehandoff_test.go` 9 例）：复位后 state 三指标（含旧会话只多一行、新会话 history/统计/
事件）、摘要请求形态（不带 tools、system 首条、工具轮占位、图片占位、指令末条、思维链清空）、指令必填、
空历史、空摘要失败后状态与文件零变化、长度上限边界、回合内被拒绝（钩子内调用）与回合外可用、归档只读拒绝、
`NoSave` 路径。**验证**：`gofmt -l` 干净、`go build`/`vet`/`test ./...` 全绿、`go test -race ./agent/` 通过、
`make cross`（darwin + windows vet 与逐包测试编译）通过。

**文档**：`docs/force-handoff.md`（新：定位与三指标、接口、实现顺序、请求形态、失败面、与既有面的关系、不做）、
`docs/session-handoff.md`（§14 互指两条路）、`docs/agent-hooks.md`（典型用法表区分回合内转段与回合外复位）、
`AGENTS.md`（交接两条路的约束条 + 文档清单）。

## 2026-11-26 feat(agent): 生命周期钩子 `WithHooks`

调用方在回合内的执行权此前只有一条通路（模型触发的 `Tool`），缺「调用方在固定时机做自己的事」的面子。
新增通用钩子：库在八个时机把只读快照交给调用方，**无返回值、不参与控制流**——库不读取、不短路、不据此改变回合走向。

- `agent/hooks.go`（新）：`Hook`（任意实例）、八个切点接口（`BeforeTurn`/`BeforeRequest`/`AfterResponse`/
  `BeforeTool`/`AfterTool`/`AfterTurn`/`SessionEvent`/`AfterSave`）、`WithHooks`、入参 `TurnStart`/`RequestInfo`/
  `SessionEvent`（六种 `SessionEventKind`）/`SaveInfo`
- **只认接口实现**：`New` 里逐个类型断言登记，一实例可同时命中多个切点，同切点顺序 = 注册顺序；`nil` 忽略；
  一个切点都没命中的实例 → `New` 返回 `不支持的生命周期钩子类型 %T`；装配期固定、无运行期注册、无适配器构造器
- 调用契约：同步、与 `Ask` 同 goroutine、**可阻塞（库不加超时）**、不 `recover`（同 `EventSink` 约定）；
  回合切点收到回合 ctx，`AfterSave`/`SessionEvent` 收 `context.Background()`（这些方法本身无 ctx）
- 顺序：**事件先、钩子后**——`BeforeRequest` 在 `EventRequestStart` 之后、构造请求之前（注入内容能进本次请求）；
  `AfterResponse` 在响应入 history 之后、工具执行之前；`AfterTurn` 在 `EventTurnEnd` 之后、交接应用之前
- 接线：`Ask`/`Continue` 加 `BeforeTurn`；`runTurn` 循环头加 `ctx.Err()` 早退 + `BeforeRequest`（带本回合请求序号 `Turn`）；
  响应落地后 `AfterResponse`；工具前后 `BeforeTool`/`AfterTool`；`settleTurn` 加 `AfterTurn`；`save` 加 `AfterSave`；
  会话边界加 `SessionEvent`（`NewSession` 拆出内部 `newSession`，`loadWorkspace` 不再上报边界切点，避免构造期与
  工作区切换重复触发）；`applyHandoff` 成功路径上报 `SessionHandoff`（带 `Handoff` 事实）
- **两面正交**：钩子不携带任何业务概念（无 `Notice`/`Stop`/`Handoff` 字段）；通知、转段、落盘等动作一律走 `Agent`
  既有公开方法（`AppendNotice`/`RequestHandoff`…），校验与时机由各方法自负。前一轮设计草案里的
  `RequestDecision{Notice, Stop, Handoff}` 与「外部强制交接」入口整条撤销，随之**不再需要** `agent.HandoffNow`
- 不做：返回值与控制流、运行期注册、顺序配置、写入校验、回合内注入防护（危险点只在文档划线）

**行为变化（可观测）**：`runTurn` 每次请求前先判 `ctx.Err()`，故 ctx 已取消时不再发 `EventRequestStart`/
`EventResponse`（直接返回取消错误）；回合结果与错误类型不变。

**测试**（`agent/hooks_test.go` 9 例）：全切点调用序列与 `Turn` 计数（含构造期零调用）、跨实例注册顺序、未知类型
`New` 报错、`nil` 忽略、`BeforeRequest` 注入的 `AppendNotice` 落在本次请求末尾（用户输入之后）、工具轮之后注入落在
最后一条 `tool` 之后、会话边界五种 Kind + `SessionHandoff` 事实、`NoSave` 下照常触发且 `SaveInfo.Session` 为空、
ctx 已取消时跳过 `BeforeRequest` 且不发请求。**验证**：`gofmt -l` 干净、`go build`/`vet ./...`、
`go test ./...`（7 包）与 `go test -race ./agent/` 全绿、`make cross`（darwin + windows 的 vet 与逐包测试编译）通过。

**文档**：`docs/agent-hooks.md`（新：定位与四面表、切点表、装配、调用契约、入参、典型用法、不做）、
`docs/agent-event-seams.md`（加「事件是出站观察面、钩子是入站时机面」互指）、`AGENTS.md`（约束条 + 文档清单）。

## 2026-11-25 fix(vbash)!: Windows 路径三段形态闭环，argv 真转换与 `/tmp` 映射

**行为变化（仅 Windows；POSIX 恒等无变化）**：脚本可见面统一成宿主形态正斜杠——
`pwd`/`$PWD`/`$OLDPWD`/`cd -`/`~`/`command -v|-V`/`export`|`set` 列表显示 `C:/Users/x`（原为内部形态
`/c/Users/x`）；`$TMP`/`$TEMP`/`$APPDATA` 同批纳入；`run_shell` 的 `Result.Cwd` 用同一形态
（`filepath.ToSlash`）。出站方向：`argv[1:]` 里确凿的内部形态（`/d/x`、`//server/share`、`/tmp`）
翻成宿主形态再交给外部程序；`/tmp` 从「进程当前盘根 `\tmp`」改为映射 `%TEMP%`；子进程 `PATH`
逐段 `;` 拼接（盘符冒号不再误切）。收紧 `run_shell` 的 `cwd` 参数：接受 vbash 自己宣传的 `/d/…` 形态。

**为什么**：Windows 上路径转换只做了一半——只翻 `argv[0]` 与 `cmd.Dir`，而工具描述把「做了一半」
说成「做完了」。真机会话（136 会话 / 1458 次 vbash `run_shell`）里原生工具把 `/d/x` 按「当前盘相对」
解释，读错文件、**写错目录**，是本轮唯一的数据安全项。修法不是要求模型换写法（那条路已被数据推翻），
而是让解释器对外的路径形态唯一：内部形态（`/c/…`，`plat.FromHost` 归一，盘符小写）／对外形态
（`C:/…`，`DisplayPath`）／出站形态（`C:\…`，`plat.ToHost`+`plat.Arg`）。完整评估与逐项决策见
`issue/vbash-windows-path-design.md`（R1 方案，状态转「已实施」）。

**实现要点**：`platformOps` 新增 `SplitPath`/`Display`/`Arg` 三个钩子（均有平台无关缺省）；新增
`PathList`（`Var.Value` 仍是 PATH 真相，数组只在消费点解析）与导出 `DisplayPath`/`HostPathArg`；
`Env` 新增 `LookupDisplay`（脚本可见面取宿主形态，内部消费者继续 `Lookup`，宿主形态不回流内部状态）；
未引用上下文里 `X:\…` 的反斜杠会静默吞掉，词法阶段给一条提示（**不改语义**，与 bash 一致，上限 16 条）。
提示链路修掉两处实现缺陷：非 ASCII 单字母（`š:\x`）曾被 `byte(rune)` 截断误报；命令替换内部的提示
曾只挂在子 `List` 上、`printWarnings` 永不打印。

**回退开关**：`Config.NoArgvHost` 只关 `argv` 翻译（线上出现「参数被误翻」时止血），对外形态与
`/tmp` 映射不受影响。

**测试**：新增 8 组 Windows 用例（PATH 盘符冒号切分、`windowsHostArg` 形态矩阵 + `hostArgv` 端到端、
`DisplayPath` 矩阵与 `pwd`/`$PWD`/`cd -`/`$(pwd)` 往返、`/tmp` 落点与 `/tmpfoo` 边界、TMP/TEMP/APPDATA
三形态一致、`cwd` 参数接受 `/d/…`、警告触发与不触发，含命令替换内与非 ASCII 两类回归）；4 个既有用例
改为形态无关断言（`filepath.ToSlash`/`DisplayPath`）。Windows 真机：`vbash` 包由 3 条失败收敛到仅 1 条
既有计时 flaky，`tools/shell` 修复 2 条 Cwd 形态失败。Linux 侧 `go test ./...`、`GOOS=windows vet` +
逐包测试编译、`make cross` 全绿。

**已知限制**：目录名里字面含 `;` 的 PATH 段会被切开（Windows 下极罕见，旧实现同样无法处理）；
内部形态对 `/tmp` 有双名（`/tmp` 与 `$TMP` 归一后字符串不等，但同目录）；`DisplayPath` 对非盘符绝对
路径（msys 的 `/usr/bin`）按「当前盘根」渲染，与执行侧 `Arg` 刻意不翻的口径不一致，仅影响显示面。

## 2026-11-24 fix(agent)!: 思维链回传改为双写 + 原样 id，删除 `ReasoningItem.FromSummary`

**BREAKING**：删除公开字段 `ReasoningItem.FromSummary`（v0.5.1 引入）。jsonl 键 `from_summary` 不再写入；旧文件里的
该键被 `encoding/json` 忽略，无需迁移。调用方若引用该字段需删除（tanya 同批处理）。完整实测与决策记录见
`docs/reasoning-replay.md`。

**为什么删**：`FromSummary` 只服务于「回传形态随来源」这一个决策，而该决策的两条前提（文本放错侧会被拒收 /
会丢「明文还是摘要」的语义）在上一条复核里已被推翻——各家把 `content`/`summary` 当同一份正文的两个视图、取其一
（`content` 优先），唯一硬约束是 `summary` 键恒在且为数组（opencode 线）。继续维护一个没有读者的标记只增加出错面
（引入次日即因两派 delta 混发标错形态返工一次）。

**回传形态**（`responsesReasoningItem`）：正文双写到 `summary[].summary_text` 与 `content[].reasoning_text`
（同一份正文），上游 `id` 非空时原样带回，不带 `status` / `encrypted_content`，空正文条目仍整条跳过。依据是
各家 input schema 的并集 + 实测各家对多余键宽容：智谱只认 `content`，opencode 要求 `summary` 键恒在，Kimi 两侧都收
且 `content` 优先，DeepSeek / 火山方舟回传为 no-op，LongCat 两侧都空才 400。7 条线实测 `id` / `status` /
摘要同文副本均不改变 `input_tokens`（不双计费）。

**行为变化**：同线会话的请求字节变化（`summary[]` 由空变非空）但语义不变（各线只读其一），每个活跃会话的首个请求
前缀缓存 miss 一次；跨线会话里来自摘要派的条目，在只读 `content` 的线上（智谱原生）从不可读变为可读——收益是
一致性，代价是该线（无前缀缓存）每轮多计入这部分正文。

**简化**：`reasoningItemText` 只返回正文（不再回报来源形态）；delta 兜底保留明文/摘要两桶、明文优先择一，不再标形态。

**测试**：新增整 item 形态断言 `assertReplayReasoningItem`（键集合 + 两侧同文 + id 有/无 + 无多余键）；改写 9 个用例
（工具循环回传、两形态捕获、明文优先、双写回传、旧 `from_summary` 键被忽略、三处兜底）。**变异复验 5 处**
（只写 `content` 侧、去掉 `summary` 键、空正文不跳过、多带 `status`、丢 `id`）全部由对应用例拦下。

## 2026-11-24 fix(agent): 复核思维链两形态的兼容面，delta 兜底按形态分桶

对 `d8c6ee4` 的真实兼容面做了一轮实测复核（经内网聚合网关，5 家上游 6 条线：DeepSeek / GLM /
LongCat / Kimi / 火山方舟 / Opencode），修正两处与实测不符的口径与一处兜底形态判定缺陷。

**形态矩阵（入站）**：明文派 `content[].reasoning_text`（DeepSeek / GLM / LongCat），摘要派
`summary[].summary_text`（Kimi / 火山方舟 / Opencode），与 `d8c6ee4` 表格一致；chat 线除 GLM 外
都用 `delta.reasoning_content`（GLM 的 chat 端点不返回思维链，上游侧需 `thinking` 参数）。

**回传接受度（出站，6 条线 × 5 类形态）**：

- 按来源形态回传、两形态互换、只给一侧（另一侧键缺失）、无 `id`、跨供应商 id+形态（3×4）：
  实测全部 200，上游对文本侧错配普遍宽容。
- **唯一硬失败**：opencode 线对 `summary` 键缺失（含旧落盘只剩 `id` 的条目）报
  `input[].summary: expected an array`。`responsesReasoningItem` 两形态都显式给数组已规避；
  空正文条目在 `buildResponsesInput` 里跳过，不会撞上。
- 故「摘要塞进 content 会被拒收」的说法不成立：错配虽 200，仍按来源回传以免丢掉形态语义。

**回传收益复核**：完整回传 / 不回传 / 空占位三档的 `reasoning_tokens` 分别为 23–55 / 19–30 /
4–9（k3 空占位最低），不支持「不回传即降级为 0」；支持「空条目比不回传更糟」，与空正文条目
跳过不回传的做法一致。

**兜底形态修正**（`llm_responses.go`）：原先 delta 累积用单个 builder，且一收到
`reasoning_summary_text` 就整体标 `FromSummary`——两派 delta 混发时会把两段正文拼成一段、
形态也标错。改为明文/摘要两桶累积，兜底时明文优先（与终态解析 `reasoningItemText` 同序），
只有摘要桶非空才标 `FromSummary`。新增 2 个用例（混发取明文、纯摘要标形态），变异复验 2 处
（合并单桶、摘要优先）均由对应用例拦下。

**未覆盖的风险**：OpenAI 官方 `/responses` 未接入未测——官方在 `store: false` 下要求 reasoning
item 原样回传（含 `encrypted_content`），本项目只回传文本、丢弃该字段，接官方线大概率 400。

## 2026-11-23 fix(agent): responses 线兼容思维链的两种上游形态，摘要形态不再被整段丢弃

**症状**：会话 jsonl 里 `reasoning_items` 大面积只剩 `{"id":"rs_…"}`，正文为空——28 个会话文件中
25 个完全没有思维链正文，1844 条 reasoning item 里 1703 条（92%）只剩 id。

**根因**：`responsesOutputItem` 没有 `summary` 字段，`reasoningText` 又只认
`p.Type == "reasoning_text"`。而真实上游把正文分两派下发：

| 形态 | 正文位置 | 流式事件 | 供应商（实测） |
|---|---|---|---|
| 明文 | `content[].reasoning_text` | `response.reasoning_text.*` | GLM / DeepSeek / LongCat |
| 摘要 | `summary[].summary_text` | `response.reasoning_summary_text.*` | Kimi / 火山方舟 / opencode |

摘要派的 item 解析后 `Content` 恒为空，`omitempty` 一落盘就只剩 id。此前测试一直绿，
是因为 `mock_test.go` 按同一个错误假设只造明文形态，实现与 mock 一起错了。

- **两形态都收**（`llm_responses.go`）：`responsesOutputItem` 增 `Summary`；新的
  `reasoningItemText` 优先取明文 `reasoning_text`（比摘要完整），明文侧为空才回落
  `summary_text`，并回报来源形态。
- **回传形态随来源**（`ReasoningItem.FromSummary`，jsonl 键 `from_summary`）：摘要形态放进
  `summary[].summary_text`，明文放进 `content[].reasoning_text`，另一侧显式给空数组。
  硬约束只有一条：`summary` 键必须存在且为数组（缺失时 opencode 线报
  `input[].summary: expected an array`，见下方 2026-11-24 复核）。旧会话无该键 → 明文形态，
  向后兼容。
- **delta 兜底**：个别上游只发 reasoning delta、终态不给 reasoning item，此时用 delta 的
  本地累积补一条，避免这次思考既不落盘也不回传。
- **测试**：`mockStep.reasoningForm` 增摘要形态造流；新增 7 个用例（两形态捕获、明文优先、
  两形态回传、旧条目向后兼容、delta 兜底、有终态不重复）。**变异复验 4 处**（去掉 summary
  回落、回传忽略来源形态、去掉 delta 兜底、兜底误加重复）全部由对应用例拦下。
  另用真实上游做端到端验证：Kimi 与火山方舟线走摘要、GLM 线走明文，均正确捕获并回传成功。

**顺带修正一处口径**：回传 reasoning 的收益不在前缀缓存（回传与不回传的 `cached_tokens` 都是
256，不成立）。当时认为收益是**思考延续**（不回传时摘要派供应商静默降级为 `reasoning_tokens: 0`）；
2026-11-24 复核显示该定量结论在网关路径下不成立（不回传 19–30、完整回传 23–55），只有
**上传空 reasoning item** 时 k3 明显更低（4–9）——即「空条目比不回传更糟」。故本项按正确性守、
不按省 token 守，空正文条目继续跳过不回传。

**注意**：已丢失的正文无法恢复（上游摘要未落盘），修复只对之后的轮次生效。

## 2026-11-19 fix(tools/shell)!: 宿主协议改为传递宿主形态路径，外部命令在 Windows 上恢复可用

v0.4.0 把外部命令改由会话宿主进程承载（`fd2d757`）后，Windows 上**所有外部命令**失效
（`ls`/`git`/`grep` 一律报 `会话宿主不可用`，每次约 2s），生产路径（tanya 本体）同样受影响。
根因是协议里传的还是解释器的**内部 POSIX 形态**路径（`/c/Users/...`），宿主的
`exec.Cmd.Dir` 解不出这个目录、`cmd.Start()` 随即失败；而宿主 stderr 被丢弃、启动失败
也不回报，父进程只能等满 2s 握手超时，报出误导性的「会话宿主不可用」。修这一处时**实测又发现两个
跨平台回归**（Linux 上同样复现），一并修掉。

- **协议路径一律宿主形态**（`transport.go`、`vbash.go`、`vbash/platform.go`）：
  `reqPayload` 新增 `path` 字段（解释器已解析好的可执行文件宿主路径），`cwd` 改传
  `Runner.HostDir()`（新增导出访问器，`hostDir` 改名）。宿主拿到就 `exec` 并把
  `cmd.Dir` 设成它，**不做任何 PATH 查找与形态转换**——零平台知识，
  `sessionhost_posix.go` 也不必反向转换。注意这是**子进程 cwd**（`cmd.Dir`），
  与 §9 不变量「进程 cwd 不变、全程不 `os.Chdir`」互不影响：宿主从不调 `os.Chdir`。
  `argv` 保持用户原文，只把 `cmd.Args[0]` 设回它，子进程的 `$0` 与 bash 一致。
- **`vbash.Handler` 契约变更（BREAKING）**：签名加一个参数
  `func(ctx, r, hostPath string, argv []string, io) (int, error)`。同时**删掉 `lookPath`/
  `resolveExternal` 在注入 `Handler` 时的短路**——原先它们直接返回裸名字，让宿主自己查；
  而 `exec.Command` 的 PATH 查找读的是**宿主进程**的环境，与解释器的 `$PATH`、`cd` 无关。
  实测回归：`PATH=/tmp/x:$PATH; mytool` 在 v0.3.1 正常、v0.4.0 报「会话宿主不可用」。
  库内唯一消费者是 `tools/shell`，两个前台（tanya/ivan）不受影响。
- **命令起不来 ≠ 宿主不可用**：宿主对 `cmd.Start()` 失败回报 `frameStart` 的 pgid=0 并把
  `err` 写回 stderr；父进程据此直接取退出码 127，不再等满握手超时。实测 `no-such-cmd-xyz`
  从 2004ms 降到 3ms、且报 `command not found` 而非「会话宿主不可用」。
- **保留宿主 stderr**：`cmd.Stderr` 从 `nil` 改为 4 KiB 环形缓冲（`tailBuffer`），
  握手失败时以 `fmt.Errorf("%w: %s", ...)` 拼进错误（`errors.Is(err, errHostUnavailable)`
  仍成立）。同类问题今后不必再靠翻代码定位。
- **Windows `IsExec` 修正**（`vbash/platform_windows.go`）：不再复用 `ExecSuffixes` 的空串条目
  ——那个 `""` 是「查找时允许不带后缀的候选」，而裸名文件在 Windows 上不可执行（没有执行位）。
  混用会把 `d1/nx` 这类无扩展名普通文件判成可执行，`command -v` 于是选中它，与
  `docs/vbash-capability.md` 的「后缀在 `.exe .cmd .bat .com` 内」口径冲突。
- **测试侧补齐平台分片**：
  - `tools/main_linux_test.go`、`tools/shell/main_linux_test.go` → `main_test.go`，**去平台 tag**。
    宿主机制三个平台都启用，缺 `TestMain` 时测试二进制的 `os.Executable()` 自举会让子进程
    **再跑一遍测试**（递归自举 + 握手超时），这正是 `tools/shell` 曾 600s panic 超时的原因。
  - `vbash/builtin_command_test.go` 的 5 项 Windows 失败：可执行文件名走 `exeName`（Windows 补
    `.exe`）、`PATH` 分隔符走 `testPathSep`（Windows 为 `;`）；`TestCommandPDoesNotLeakPath`
    依赖 `sh` 读子进程 `PATH`，平台分片为 Windows/未知平台 `Skip`（语义由 POSIX 侧覆盖）。
  - 新增 `tools/shell/sessionhost_test.go` 7 例：脚本内改 `PATH` 仍按 shell 解析、命令起不来
    毫秒级失败且不误报、宿主内启动失败与「宿主不可用」区分、协议 cwd 形态与 `pwd` 实跑、
    `path`/`argv[0]` 两字段不混、宿主 stderr 附加与环形缓冲滚动，以及
    **`TestHostNeverChdirsProcess`**（源码级断言：两个平台分片只许 `cmd.Dir = req.cwd`、
    不许出现 `os.Chdir`——守住 §9 不变量 4「进程 cwd 恒为启动目录」）。
  - 新增 `vbash/platform_windows_test.go` 的 `TestWindowsIsExecNeedsKnownSuffix`（8 种后缀 +
    目录 + 大小写），锁住 PATHEXT 口径。
- **变异复验 5 处**：还原 `lookPath` 短路、还原 `pgid<=0` 误报、还原 `cwd` 传内部形态、
  还原 `IsExec` 含空串、去掉 stderr 附加——前两处与第五处准确变红；`cwd` 那处**在 POSIX 上
  无法变红**（`FromHost`/`ToHost` 恒等），已在用例注释写明「该侧要靠 Windows 真机验证」。
- **验收**：`gofmt -l` 干净、`go build/vet ./...`、`go test ./...`、`go test -race`（三包）全绿、
  `make cross` OK、`make vbash-check` 295/295；tanya `go test ./...` 全绿、`make build` 通过。
- **文档**：`docs/shell-tool.md` §17.1（路径形态条款、命令起不来≠宿主不可用、stderr 留存、
  测试钩子去 tag 与漏接症状）、`docs/vbash-impl.md` S9-3（删掉已不存在的短路理由）、
  `docs/vbash-capability.md` 与 `vbash/builtin_command.go`、`tools/shell/vbash_test.go`
  的过时注释同步。

## 2026-11-19 feat(vbash): 新增 `command` 内建（`-p`/`-v`/`-V`/执行态）

语料里 `command -v` 27 次、`command -V` 3 次，工具结果中 `command: command not found` 出现 70 次
（用法高度集中在「批量探测工具是否安装」：`for t in ...; do command -v $t || echo MISSING; done`）。
此前 `command` 在 §3.2 的「不支持」清单里按普通命令处理，模型因此拿到语义错误的 127。

- **实现**（`vbash/builtin_command.go`）：`builtinCommand` 解析 `[-p] [-v|-V] [--] [name ...]`，
  选项在第一个非选项参数处终止（`command echo -v` 里 `-v` 是 echo 的参数）；`-v`/`-V` 同时给出时
  **后出现者生效**；非法选项打 `command: -X: invalid option` + usage 到 stderr、退出码 2
  （`--xxx` 按 bash 口径报 `--`）。查找顺序 **关键字 → 函数 → 内建 → 含 `/` 直接查该路径 → PATH 逐条目**。
- **PATH 查找**（`commandPathLookup`）与既有 `lookPath` 有三处**刻意不同**，都是 bash 语义：
  ①只跳过目录、**非可执行文件也算命中**（先找可执行的，全都不行才回落到第一个非目录文件）；
  ②拼出的路径**不 clean**（`sub//`、`sub/./` 原样保留，`-V` 只额外削掉开头一个 `./`）；
  ③**不看 `r.Exec`** —— 生产环境注入 `Handler` 后 `lookPath` 会直接短路返回原名字，用它会永远报「找到了」。
- **不复刻 bash 的 hash 表**：bash 的 `command -v` 会返回陈旧缓存路径（程序删掉后仍报旧路径），
  那是实现细节不是契约；vbash 每次新鲜查找。`-p` 用 `plat.DefaultPath` 且**不改子进程 `PATH`**。
- **`-V` 函数体打印明确不做**：`FuncDef` 只存 AST 不存源码，做函数体打印需要 AST pretty-printer，
  语料里 `-V` 共 3 次且都不是查函数，收益不足。只打 `f is a function`。
- **注册方式**：经 `init()` 而非 `builtins` 字面量——实现要查内建表（`-v` 要回答 `is a shell builtin`），
  直接进字面量会形成初始化环（`builtins → builtinCommand → canBuiltin → builtins`）。
- **测试**：`vbash/builtin_command_test.go` 14 例（查找顺序含「函数优先于内建」、`-v` miss 静默、`-V` miss 诊断、
  任一命中即 0、无参即 0、PATH 相对形态 5 组、目录不算命中、优先可执行、回落非可执行、
  空条目等价 `.`、含 `/` 参数需可执行、选项簇与 `--`、非法选项、执行态跳过函数、`-p` 不漏 PATH、
  `-p` 改用默认 PATH、`-p` 执行态不回落）；`tools/shell/vbash_test.go` 新增 2 例
  （装 `Handler` 的生产路径下 `command -v` 仍正确 miss、工具描述不再把 `command` 列为不支持）；`vbash/builtin_command_windows_test.go`（windows 专属：`.cmd`/`.bat` 后缀语义）。
- **基准**（`testdata/vbash-bench/cases/C-corpus.case` 新增 6 条）：`C/031` 批量探测循环（语料最高频形态）、
  `C/032` 混合命中与 miss、`C/033` 五种形态锁查找顺序、`C/034` `-V` 文本形态、`C/035` 执行态跳函数、
  `C/036` `-V` 函数体（`optional`，已裁定的已知差异）。**总用例 295 条**，
  `make vbash-self` 与 `make vbash-check` 均 100%。
- **顺带修掉一处真 bug**（单测发现）：`command -p NAME` 执行态在默认 PATH 解析失败后会**回落到自定义
  PATH** 把命令跑起来。bash 实测是不回落的（`PATH=/tmp/pt:$PATH; command -p ptool` 在 ptool 只存在于
  `/tmp/pt` 时报 `command not found`、退出码 127）。改为解析不到即 127 + `X: command not found`。
- **变异复验 7 处**，其中 6 处准确变红（短路 `Handler`、非可执行不算命中、优先可执行改先到先得、
  不削 `./` 前缀、`-p` 不改用默认 PATH、`-p` 执行态回落）；1 处（内建优先于函数）初版**未变红**，
  补「函数与内建同名」用例后变红——`echo(){ :; }; command -V echo` 在 bash 里报 `is a function`。
- **逐条对照真 bash 5.3 实测**（`LC_ALL=C` 差分 36 例）：除 §3.3 记的 4 条已知差异外全部逐字节一致。
- **验收**：`gofmt -l` 干净、`go build/vet ./...`、`go test ./...` 全绿、`make cross` OK、
  `make vbash-check` 295/295、`make vbash-self` 295/295。
- **文档**：`docs/vbash-capability.md`（§3.1 内建 22→23、§3.2 移除 `command`、§3.3 补 4 条差异）、
  `docs/vbash-plan.md` §4（`command` 由 P2 转「已实现」）。
- **未发版**（用户口径）：`go.mod` 版本号不动，下游按工作区联调生效。

## 2026-11-17 fix(tools/shell): 输出窗口切点对齐 UTF-8 边界，不再劈开多字节字符

`streamCapture` 的头尾窗口按**字节**切，切点落在多字节字符中间就把一个汉字/emoji 劈成两半。
`toUTF8` 只在**整体**非法时才走 `platform.DecodeOutput`，而 posix 的该函数是恒等函数（原样返回），
于是半个字符直接进 tool 消息、进 history、进会话文件——`json.Marshal` 把非法字节替换成 U+FFFD，
**静默数据损坏**（模型与 `/history` 都看到 `镜�` 这样的残缺文本，且无从判断是截断产物）。

- **根因**（两处，都是字节硬切）：head 满 30000 字节时直接截断；tail 滚动时
  `c.tail = c.tail[MaxOutput/2:]` 同样按字节切。
- **修法**：新增 `runeBoundary`（返回尾部残缺序列的首字节下标，完整序列与非法输入都返回 `len(b)`）
  与 `rollTail`（滚动切点从 `MaxOutput/2` 起在 3 字节窗口内对齐）。head 满额时尾部残缺序列
  **整体移交给 tail 开头**——不丢字节、内容连续，`finish` 的 join 分支照样拼回完整流；
  `middle` 按实际丢弃字节计数。**字节上限语义不变**（只挪切点、不改容量），
  非法/二进制输入找不到序列首字节时退回硬切、保持旧行为。
- **影响面**：四条执行路径共用 `streamCapture`（`runForeground`/`runFull`/vbash 会话宿主/interactive），
  tanya 与 ivan 一并受益。
- **实证**：修复前 `"a" + "中"*40000` 产出 `ä¸` 结尾的 head 与 `¸­` 开头的 tail，
  `Tool.Invoke` 的 `res.Text` 非法、落盘含 U+FFFD；修复后 head/tail 均合法、`res.Text` 零 U+FFFD。
  扫描本机 tanya 会话库，37 个会话文件含 U+FFFD、其中 24 处紧邻 CJK（`本节点只读镜�`、
  `M11 上的临��`），与劈开汉字的形态吻合。
- **测试**（`tools/shell/decode_test.go`）：`TestStreamCaptureSplitsOnRuneBoundary`（6 组：
  1/2/3 字节前缀 + 中文、emoji、混合文本，跨 head 边界）、`TestStreamCaptureTailRollRuneBoundary`
  （跨 tail 滚动边界）、`TestStreamCaptureKeepsByteLimit`（head 不超上限 + 字节记账平）、
  `TestRuneBoundary`（8 组含残缺序列与全非法字节）。既有 `TestFinishDecodesDisjointFragments`
  （全 `0x81` 非法流）与 `TestLimitedBuffer`（纯 ASCII）按设计不受影响、继续通过。
- **验收**：`gofmt -l` 干净、`go build/vet ./...`、`go test ./...` 全绿、`make cross` OK、
  `make vbash-check` 290/290；tanya 与 ivan 的 build/vet/test 全绿，tanya `make cross` OK、
  渲染审计 17/17。
- **未发版**（用户口径）：`go.mod` 版本号不动，下游按工作区联调生效。

## 2026-11-17 fix(tools/shell): 补非交互终端锚点、修 relay 收尾死锁与桥回显泄露

会话宿主架构（fd2d757）落地后渲染审计留有三处红，逐一归因修复：

- **非 interactive 丢终端锚点**：vbash 内建重定向（`printf ... > /dev/tty`）由调用方进程直接写
  真实终端，模式污染原本靠 `LendStdin` 归还时的 `anchorTerminal` 复位，新架构该分支不借租约、
  污染无人清（`clean-tty-modes`/`clean-alt-screen-exit` 回归）。补回租约；不得与 interactive 的
  `LendFull` 并用——console 的 lend 记账是状态位，嵌套借用会在首个归还时提前唤醒前台读者。
- **relay 收尾死锁**：读 goroutine 阻塞在 `dev.Read`，`stop()` 只关通道不关设备，interactive 一
  结束 run_shell 永久挂在收尾。改为先恢复桥 termios（fd 仍有效）再关闭设备强制唤醒阻塞读；
  租约侧随后的重复关闭无害。
- **桥回显泄露密码**：sudo/ssh 在虚拟 tty 上关掉的回显，会被中间桥 pty 的无条件回显泄露到屏幕。
  `relayPipeMode` 把桥 slave 设为哑管道（关 `ECHO`/`ECHONL`/`ICANON`/`ISIG`/`OPOST`），回显与
  行编辑职责完全交给虚拟 tty——那里才是被跑程序的地盘；Windows 无 termios，stub 保持原模式。
- **陷阱留档**：`os.File.Fd()` 会把 fd 从运行时 poller 的非阻塞模式切回阻塞模式，之后阻塞读走
  裸 syscall、`Close` 再也唤不醒；ioctl 后必须恢复 `O_NONBLOCK`（`relayPipeMode` 在读 goroutine
  启动前调用，无竞态窗口）。
- **验证**：探针确认 relay 单次回显与 stop 可返回、interactive read 全链路取到注入行、
  `stty -echo` 期间输入零上屏；三仓 build/vet/test、`make cross`、`make vbash-check` 290/290、
  tanya 渲染审计 17/17 全绿（`clean-tty-modes`/`clean-alt-screen-exit`/`clean-interactive-release`
  三条转绿）。

## 2026-11-16 feat(tools/shell)!: 会话宿主自建虚拟 pty，stdio 帧协议替代 fd 传递

上一版会话宿主只覆盖 Linux：宿主消费调用方借出的 pts，靠 socketpair+`ExtraFiles` 传描述符、
`/proc/<ppid>/fd` 回开文件，darwin 走 stub 不设宿主。这三条通路在 Windows 上都不存在
（os/exec 明写 ExtraFiles is not supported on Windows），而 vbash 要求跨平台一致——宿主模型
必须只建立在「所有平台都有的东西」之上。

- **宿主自建虚拟 pty**（`sessionhost_posix.go`/`pty_linux.go`/`pty_darwin.go`）：自举后
  `BecomeSessionLeader` 自任会话首进程与前台组（真实交互 shell 的处境，只是终端是虚拟的）；
  每条命令仍 `Setpgid` 建独立进程组（保留组杀），前台命令按需设为该 pty 的前台组、后台 `&`
  不设；`GPG_TTY`/`SSH_TTY` 指向虚拟 tty。Windows 无会话语义，宿主退化为进程树容器、超时按
  `taskkill /T` 统一终止。
- **传输协议**（`transport.go`）：父子进程间只走宿主的 stdin/stdout，9 字节帧头
  `type+jobID+len`、8 种帧型，jobID 多路复用并发命令；stdio 三种形态（管道读端/命令替换
  buffer/重定向文件）由父进程如实搬运，宿主不需要父进程的任何 fd。删除 `sockpair_linux.go`/
  `sessionhost_stub.go`/`hostexec_linux.go`。
- **vbash 统一为一条执行路径**：普通与 interactive 同路，外部命令一律经宿主；interactive 只
  影响呈现（`ttyRelay` 把真实终端按键泵进虚拟 tty、虚拟 tty 输出上屏并进工具结果）。删除独立
  的 `runVbashInteractive`。
- **未重定向的 stdin 接虚拟 tty**（`tty`/`test -t 0` 与真实 shell 一致，`emptyStdin()` 改为
  哨兵类型供宿主识别）；**全屏交互程序（top/vim 类）明确不在支持范围**（无 winsize 同步）。
- **BREAKING**：宿主不再消费调用方借出的 pts；`LendFull` 退化为呈现层桥接（真实终端与虚拟
  tty 之间的双向泵），tanya 零改动适配。
- **测试**：两个测试二进制经 `TestMain` 自举宿主；交互语义用例按新模型重写（虚拟 pts 可开
  `/dev/tty`、`GPG_TTY`/`SSH_TTY`、`test -t 0`、管道各段共享终端、后台不设前台组、并发不阻塞、
  命令替换隔离、超时组杀）；`make cross`（linux/darwin/windows）通过。

## 2026-11-13 fix(ctty): `FallbackCP()` 改跟随控制台当前码页

Windows 上 `run_shell` 的中文输出会随机变成乱码（`中文` → `涓枃`，带私有区字符
`\ue15f`/`\ue17b`），且同一段合法 UTF-8 时好时坏。根因在解码侧：`FallbackCP()` 返回的是
「自己改码页前的原值」而非控制台**当前**码页。`EnsureUTF8()` 只在需要改时才存原值，
本来就 65001 的控制台（Windows Terminal / conpty 的常见形态）什么都不做、快照保持 0，
于是 `FallbackCP()` 落到 `OEMCP()`（936），`tools/shell` 拿它去 `DecodeCP`，
把合法 UTF-8 字节按 GBK 解。这也解释了「时好时坏」：936 控制台（`conhost`）下反而正常。

- **改法**：`FallbackCP()` 先读 `ConsoleOutputCP()`并返回；读不到（无控制台）才回落
  启动快照的原值，再回落 `OEMCP()`。两个语义分开——「还原用码页」（`utf8OrigOut`，
  只在 `RestoreUTF8` 里用）与「解码用码页」（子进程此刻写出的字节按控制台当前码页生成）。
- **影响面**：唯一调用点是 `tools/shell/platform_windows.go` 的 `windowsDecodeOutput`，
  语义收窄到解码，不触碰 `RestoreUTF8` 的还原路径；posix 侧 stub 恒 0 不变。
- **测试**：`TestFallbackCPFollowsConsoleOutputCP`（返回值恒等于 `ConsoleOutputCP()`）、
  `TestFallbackCPAfterEnsureUTF8`（`EnsureUTF8` 后为 65001、`RestoreUTF8` 后回到原值）；
  无控制台环境（`go test` 无 tty）自动跳过。`make cross` 覆盖 windows 编译与 vet。

**未覆盖（另行处理）**：`toUTF8()` 仍是整块 `utf8.Valid` 判定（一块混入非法字节会让其中
合法 UTF-8 陪葬）、`streamCapture` 按字节在 `MaxOutput` 截断会切断多字节字符。
本条目只修「解码目标码页选错」这一层，上述两条会让 >30KB 中文输出仍可能乱码。

## 2026-11-13 feat(agent): 会话 jsonl 逐条记录时间戳 `_ts`

会话消息此前只有会话级时间信息（文件名与文件 mtime），拿到不了一条消息的先后与间隔。
`Message` 新增 `TS int64`（毫秒，`json:"_ts,omitempty"`），在消息创建处打点，随会话落盘。

- **私有键约定**：key 以 `_` 开头 = 只落盘、绝不外发。`chatWireMessages` 里 `wm.TS = 0`
  清空（与既有的 `ReasoningItems`/`ReasoningContent` 剥离同处同构），`omitempty` 保证
  wire 上不出现该字段。responses 协议走 `buildResponsesInput` 显式白名单，天然不含。
- **打点位置**：`Ask` 的 user 输入取 `nowMS()`；assistant 取该次请求的发起时刻
  （`runTurn` 里现成的 `start`，故 `_ts` 表示「请求开始」而非「响应结束」）；tool 结果取
  执行完成时刻。**首行 system 快照不打点**——它是会话级事实，时刻已由文件名承载。
- **兼容**：老会话文件读回 `TS == 0`，不报错、不需迁移；旧读方忽略未知 key（全仓无
  `DisallowUnknownFields`）。展示层应判 `TS > 0` 才渲染，避免把零值显示成 1970。
- **归档**：卷内是源文件逐字节拷贝，新旧混杂照常，逻辑不动。
- **测试**：`TestSessionStampsMessages`（逐条 `_ts` 单调不减 + system 快照不带）、
  `TestChatWireHasNoPrivateKeys`（wire 上无任何 `_` 前缀 key，守住未来私有字段）、
  `TestSessionLoadLegacyWithoutTS`（老文件零值载入）。

## 2026-11-13 fix(tools/shell): 补上 v0.3.1 遗漏的 2 条 Windows 测试失败

上一轮 Windows 全绿修复（58e7f7e）后，`tools/shell` 在 Windows 上仍有 2 条失败，均为
**测试代码问题而非实现缺陷**（`go build`/`go vet`/`gofmt -l` 全干净，Linux/macOS 通过）。
归因留档见工作区 issue 目录。

- **`TestRunShellFullCapture`**（`bridge_test.go`）：命令 `echo hi; echo oops >&2` 用了
  POSIX fd 复制重定向，PowerShell 不支持。改为按平台分片：Windows 用跨 shell 通用的
  `[Console]::Error.WriteLine('oops')` 造 stderr，保住「桥接单流合流」这条断言的覆盖
  （优于直接 `t.Skip`）。注意不能改成 `2>&1`——语义相反，会让断言假通过。
- **`TestNewShellToolEmptyPrograms`**（`tool_test.go`）：断言裸子串 `"可用程序"` 范围过宽，
  Windows 能力句本身以「以『可用程序』清单为准。」结尾，必然误报。收紧为精确匹配清单段
  实际写入形态 `"可用程序: "`。该模式在包内是孤例，其余断言均为精确串。

## 2026-11-10 fix: Windows 下 `go test ./...` 全绿（含 3 处真缺陷与 1 处测试污染）

Windows 上跑全量测试有 58 项失败（其中 1 处 panic 会打断整个包）。逐项归因后分三类处理：
真缺陷修实现、测试假设错就按平台分片、该跳过就加门禁。**没有为迁就 Windows 放宽实现语义**
（`d > 0` 没有改成 `d >= 0`、0600 断言没有被删掉）。

真缺陷（用户可见）：

- **`$(<file)` 在 Windows 上一律返回空串**（`vbash/runner.go`）：原实现直接拿内部 POSIX 形式
  （`/d/work/a.txt`）当宿主路径去 `os.ReadFile`，Windows 上必然 `ENOENT`。改为绝对路径走
  `hostPath` 转换；同时保住「`Dir` 为空时相对路径按进程 cwd 解析」的旧语义（与 `glob` 一致），
  新增 `TestReadFileSubstitutionNoWorkDir` 守住这条边界。
- **`shortPath` / `globalLabel` 混用分隔符**（`agent/path.go`、`agent/prompt.go`）：前缀是 `~`
  或 `~/`、后缀却是原样的 `\`，于是 system prompt 里出现 `~/.config\agent\AGENTS.md` 这种
  半截归一的路径。补 `filepath.ToSlash`。

测试污染（比"测试失败"更严重）：

- **`agent` 的用例会写真实家目录**：环境隔离只设了 `HOME`，而 Windows 的 `os.UserHomeDir()`
  读的是 `USERPROFILE`。`TestSystemPromptAgents` 因此真的往 `C:\Users\<user>\.config\agent\AGENTS.md`
  写文件，且该文件随后被其它用例读到，造成用例间互相污染。按仓库既有的平台白名单约定新增
  `agent/testhome_{posix,windows,stub}_test.go` 提供 `setTestHome`，在 `isolateProcessEnv`、
  `isolatePromptEnv` 两处接线。`AGENTS.md` 里「用例不得依赖真实 HOME」的约定此前在 Windows 上
  **从未真正生效**。

测试侧按根因批量修（不改实现语义）：

- **行尾**：新增 `.gitattributes`（`* text=auto eol=lf`）。`core.autocrlf=true` 时检出为 CRLF，
  会让嵌入文本与快照类断言失配
- **`PATH` 写死 POSIX**（27 条 + 1 panic）：7 处 `runScript`/用例把 `PATH=/usr/local/bin:/usr/bin:/bin`
  写死，Windows 上找不到 `cat`/`ls`/`head`/`sh`，全部 127；`TestRunCDChangesDir` 因 `lines[1]`
  越界 panic，把该包后续用例全打断。新增 `vbash/testpath_{posix,windows,stub}_test.go` 提供平台
  感知的 `testPath`（Windows 继承宿主 `PATH`）
- **shell 探测与路径形态**（`tools/shell`）：新增 `normpath_{posix,windows,stub}_test.go` 的
  `normShellPath`，把 `pwd` 输出的 `/c/Users/...` 与宿主 `C:\Users\...` 归一到同一形态再比较
  （只用于比较，不改实现输出）；「候选探测」类用例显式给 `Override: "bash"`，不再依赖平台候选表。
  **`testShellTool` 默认仍走宿主 shell**：桥接路径（`LendFull`/`runFull`）只属于宿主 shell，
  要测它的用例用新增的 `hostShellTool` 显式声明
- **加平台门禁**（该跳过就跳过）：`TestRunningPathNotExecutable`、`TestLookPathSkipsDirectory`
  （POSIX 执行位 / PATH 语义）、`TestPromptBuilderInjectedRead` 等 3 条（用 `/fake/home` 这类
  POSIX 假路径，走 `posixPathSemantics` 常量）、`TestRefCompletionSpaceNames` 的 `"` 文件名
  （Windows 保留字符）
- **时钟精度**：`TestAskRequestCallbacks` 断言 `Duration > 0`，Windows 毫秒级 mock 可能测出 0，
  给该步骤加 `hold: 2 * time.Millisecond`
- **路径分隔符断言**：`tools/tools_test.go` 新增 `tailPath` 剥掉 `C:` / `/c` 前缀后比较
- **`os.Chdir` → `t.Chdir`**（`agent/agent_test.go`）：`os.Chdir` 配 `t.Cleanup` 会因 LIFO 顺序
  「先删 cwd 再切回」而失败（Windows 上目录被占用即报错），`t.Chdir` 自动按正确顺序注册清理
- **性能断言**：`TestBraceUnmatchedOpenLinear` 在 Windows 上因计时粒度粗误判 O(n²)，改为预热 +
  放大规模（20k/200k）+ 100µs 下限

验收：Windows 上 `GOWORK=off go build/vet/test ./...` 全绿、`gofmt -l` 干净；Linux 与 macOS
（`GOOS=darwin`）交叉编译与全量测试同样全绿（含 `-race`）。

## 2026-11-10 feat(tools/shell)!: interactive 命令改由独立会话宿主执行

一个 pts 同一时刻只能属于一个会话。此前每条外部命令各自 `setsid`+`TIOCSCTTY` 去抢同一个借出的
pts，管道各段并发存活时第二段会拿到 `EPERM`，表现为 fork/exec 直接失败（`echo x | cat | cat`
报 `Permission denied`）；同时又用「子进程 stdin 是否正好是借出的 pty slave」当授不授终端的判据，
把 `sudo ... < /dev/null`、`cmd | sudo ...` 这类显式重定向 stdin 的前台命令误判成后台，它们进了
新会话却没有控制终端，sudo 于是报「读取密码需要一个终端；请使用 -S 选项…」。

改成真实 bash 的模型：**会话只建立一次，命令只建自己的进程组**。

- **会话宿主进程**（`tools/shell/sessionhost_linux.go`）：借出终端时用**当前可执行文件**起一个
  `setsid`+`TIOCSCTTY` 的会话首进程，由它持有这台 pts 并 fork 各条命令；命令按需 `tcsetpgrp`
  设为前台组，退出后把前台组收回宿主。于是任意多条命令（含并发存活的管道各段）共享同一个控制终端，
  `sudo`/`gpg`/`ssh` 在管道任意位置都能与用户应答，`sudo` 的按 tty 凭据缓存也只生效一次。
  解释器自己不能 `setsid`（会脱离 REPL 的终端），而让每条命令各自 `setsid` 正是本节要修的病。
- **宿主自举**：调用方二进制必须在入口调用 `shell.RunSessionHostIfRequested()`（tanya 已接，
  测试经 `TestMain` 接线）；宿主进循环前回一个就绪字节，未接自举的宿主在 2s 内**明确失败**
  （`会话宿主不可用`，表现为 `interactive` 报 `无法借出终端`）而不是挂死。
- **宿主协议走纯字节流帧**，stdio 只传 **fd 编号**（宿主经 `/proc/<ppid>/fd/<n>` 打开），
  避开 `SCM_RIGHTS` 的附着语义与帧边界纠缠；内存形态的 stdio（`< /dev/null` 的空读取器、
  heredoc 的字符串读取器、捕获 buffer）由父进程建临时管道桥接。
- **取消路径改走宿主**：命令跑在宿主里，父进程手里没有它的 `*exec.Cmd`，`CancelCmd` 那条路走不通。
  宿主启动命令后先回报 pgid，父进程超时/中断时按组 `SIGCONT`+`SIGKILL`（先 CONT 唤醒可能被
  `SIGTTIN`/`SIGTTOU` 停住的组）。
- **`ctty` 新增会话原语**（`session_posix.go`）：`BecomeSessionLeader`/`SetControllingTTY`/
  `SetForeground`/`ForegroundGroup`/`IgnoreTTOU`。宿主 `setsid` 后必须 `IgnoreTTOU`，
  否则它自己做 `tcsetpgrp` 会被停住。
- **判据改为调用方给出的前台标记**：`shellPlatform.ConfigureTTY` 增 `front` 参数，控制终端经
  **额外 fd**（子进程 `fd 3`，`SysProcAttr.Ctty` 指向它）交付，与 stdin 形态彻底解耦——
  `cmd < /dev/null`、`a | cmd` 这类前台命令照样拿得到终端；后台命令只进独立会话、不授终端。
- **`vbash` 侧新增 `CmdState{Background, InPipeline}`** 随 `ConfigureCmd` 传递：终端处境由解释器
  判定（它才知道这条命令是不是 `&` 起的后台作业），调用方不必也无法从 fd 反推。同轮修掉
  `startAsync` 未标记后台作业的缺陷（`&` 起的作业被当成前台去抢终端）。
- **`ConfigureGroup` 一并带 `CmdState`**：`platform.ConfigureGroup`/`noopConfigureGroup` 的签名
  随之改为 `func(*exec.Cmd, vbash.CmdState)`。

测试：`tools/shell/vbash_interactive_linux_test.go` 新增
`TestVbashInteractiveRedirectedStdinKeepsTTY`（`< /dev/null`、`printf x | cmd`、`{ cmd; } < /dev/null`、
`( cmd ) < /dev/null` 五种形态都须拿到 `/dev/tty`）；原 `TestVbashInteractiveBackgroundSkipsTTY`
改名 `TestVbashInteractiveBackgroundSharesTerminal` 并反转断言——后台命令**同样**能打开 `/dev/tty`
（与真实 bash 一致，区别只在于读终端会被 `SIGTTIN` 停住）；`platform_posix_test.go` 的
`TestPosixConfigureTTYRule` 改为断言「前台一律授终端、与 stdin 形态无关」与「Ctty 指向 ExtraFiles
末尾那个设备」；新增 `tools/shell/main_linux_test.go` 的 `TestMain` 接线。实机：管道首/中/末段与
stdio 重定向下 `sudo` 均能取终端，多段并发管道不再报 `Permission denied`（见 tanya 同日的
`integration/vbash_e2e_test.go`）。

文档同步：`docs/shell-tool.md` §17、`docs/vbash-capability.md` §3.6、`docs/vbash-impl.md`；
顺带校正 `AGENTS.md` 与 `docs/vbash-impl.md` 中「`Config.Vbash` 默认关闭」的过时表述
（该字段已于 2026-11-09 `338a9e3` 删除，开关就是 `shell` 配置项的取值）。

BREAKING CHANGE: `ConfigureTTY` 增加 `front` 参数；`ConfigureCmd`/`ConfigureGroup` 的签名增加
`vbash.CmdState`；`interactive` 命令的进程树改由独立会话宿主承载，**宿主二进制必须在入口调用
`shell.RunSessionHostIfRequested()`**，否则 `interactive` 一律失败。

## 2026-11-09 fix(vbash): interactive 改由解释器自己拿终端，不再报 `exec: no command`

`shell: vbash` 下 `interactive: true` 必定失败：`builtinProfile()` 的 `Path` 是空串，
而 `run()` 在判 `profile.Builtin` **之前**就走 `runFull` → `exec.CommandContext(ctx, "", ...)`
→ Go 报 `exec: no command`。根因不是终端借出，而是"interactive 不经解释器"这个设计口径本身：
它让 vbash 暗中依赖了刻意要脱离的系统 shell。

- **`runVbashInteractive`**（`tools/shell/vbash.go`）：`LendFull` 借出 pty，slave 同时作解释器的
  stdin/stdout/stderr（输出经 pty 泵实时上屏，也进工具结果）；命令照旧进 `vbash.RunScript`。
  借不到终端即报 `interactive 不支持（无法借出终端）`——不回落系统 shell，也不静默降级成无终端执行。
- **控制终端只给前台命令**：新增平台能力 `ConfigureTTY`，判据是「子进程的 stdin 正好是借出的 slave」。
  后台命令（fd0 已被换成空读取器）、管道段、重定向命令都不满足。同时外部命令**一律进独立会话**
  （`Setsid` 替换 `Setpgid`）：否则它们继承调用方会话，后台命令照样能打开 `/dev/tty` 抢走终端。
- **`GPG_TTY`/`SSH_TTY` 指向借出的设备**（平台能力 `TTYEnv`），否则 gpg/ssh 会去问 agent 要终端。
- **`read` 不再劫持 fd 0**（`vbash/builtin.go`/`io.go`）：原先 `bufferedInput()` 把 stdin 包成
  `*bufio.Reader` 并写回 `IO.In`，带来两个后果——预读吞掉后一条命令的输入（`read x; head -1`），
  以及 fd 0 变成非 `*os.File` 后 `os/exec` 改走管道拷贝、在不会 EOF 的终端上双向死锁
  （实测 `read -r x; sh -c 'tty'` 永久挂起）。现在 `read` 逐字节读底层 reader，fd 0 原样留给外部命令。
- **平台表增字段**：`shellPlatform.PtyLend`/`ConfigureTTY`/`TTYEnv`，posix（linux/darwin）实现，
  Windows/stub 为 no-op 默认（`CONIN$` 直通平台仍只有 stdin 是终端，输出走结果管道）。

测试：`tools/shell/vbash_interactive_linux_test.go`（pty 集成：内建 read 可读、外部命令拿到真实 pts
与 `/dev/tty`、`GPG_TTY`/`SSH_TTY`、read 后外部命令不挂、后台命令不拿控制终端、借不到即报错、
退出码与超时）；`vbash/fixes_test.go` 增 `TestReadDoesNotConsumeNextCommandInput` 与
`TestInputNilGuard`；原 `TestVbashInteractiveBypasses` 改名 `TestVbashInteractiveStillRunsInInterpreter`。
实机：`script` 下真实 tty 的 vbash interactive 用例（含"直接读 `/dev/tty`"的加密口令式取终端）通过。
文档同步：`docs/vbash-capability.md`（新增 §3.6）、`docs/vbash-plan.md`、`docs/vbash-impl.md`、
`docs/shell-tool.md` §17。

## 2026-11-09 fix(tools): 旁路执行不再回落系统 shell，`Invocation.Run` 统一两条执行路径

上一轮把 `Resolve("vbash")` 的 `Argv` 回落到平台首个可用 shell，让通知命令在
`shell: vbash` 时悄悄回到系统 shell 执行——既与「不回落系统 shell」的既有口径
（`docs/vbash-plan.md` D9）冲突，也让默认配置暗含一个对系统 bash 的硬依赖。

- **`Invocation` 增 `Builtin`**：解析为内置解释器时 `Argv` 为空、不查 `PATH`、不依赖磁盘上有该文件；
  删除 `platformFallbackArgv`（该回落正是问题本身）。
- **新增 `Invocation.Run(ctx, script, RunOptions)`**：旁路执行的唯一入口，按 `Builtin` 分派——
  内置走 `vbash.RunScript`（`Arg0`/`ShellName`/`ConfigureCmd`/`CancelCmd`/`Fence` 都在库内组装），
  真实 shell 走 `argv + script` 子进程（同一套平台组杀与 `WaitDelay`）。调用方只给
  `Dir`/`Home`/`Env`/stdio/`Fence`，不必自己抄一遍 `vbash.Config` 的平台策略。
- **`RunOptions.Stdin` 为空时给空读取器**（内置路径用 `emptyStdin()`，与 `runVbash` 同一份），
  避免解释器侧 `read` 拿到 nil reader。
- **新增 `ErrNoInvocation`**：既非内置又没有 `Argv` 时报错而非静默成功。

测试：`TestResolveBuiltinNeedsNoHostShell`、`TestResolveBuiltinIgnoresLookPath`（`LookPath` 一次都不被调用）、
`TestRunBuiltinExecutesScript`、`TestRunBuiltinUsesDirAndEnv`、`TestRunBuiltinHonorsContext`、
`TestRunRealShellExecutesScript`、`TestRunRealShellReportsFailure`、`TestRunWithoutArgvFails`。

## 2026-11-09 refactor(tools)!: vbash 并入 shell 配置项，取消独立布尔开关

**破坏性变更**：`tools/shell.Config.Vbash` 与 `tools.Options.Vbash` 删除。内置解释器不再是
一条独立开关，而是 `shell` 配置项的一个取值（与 `bash`/`zsh`/`pwsh` 同级）：写
`Override: "vbash"`（tanya 配置为 `shell: vbash`）即启用，写成 `bash` 就走系统 shell。

- **解析**：`profile` 增加 `Builtin` 字段；`resolveProfile` 先判 `IsBuiltin`，命中即返回
  `builtinProfile()`（`Name: "vbash"`、`Kind: KindPosix`、**不查 `PATH`**、`Path` 为空）。
  大小写与首尾空白不敏感（`VBASH`、` Vbash ` 同样生效）。
- **API**：新增 `shell.BuiltinShellName`（`"vbash"`）与 `shell.IsBuiltin(name)`，后者供调用方
  判断某配置值是否指向内置解释器（tanya 用它决定是否接线进程围栏）。`isBuiltinShell` 保留为
  内部别名。
- **装配**：`tool.go` 的运行分支与描述分支全部改看 `profile.Builtin`；`Environ` 仅在
  Builtin 时为必填（`MsgVbashEnvironFmt` 文案改为「shell 配置为 vbash 时须同时注入 Environ」）。
- **旁路执行**：`Resolve("vbash")` 用于通知命令等一次性旁路，需要真实 argv——`Name` 报 `vbash`、
  `Argv` 回落到平台首个可用 shell（新增 `platformFallbackArgv`），避免内置解释器给出空 argv。
- **诊断前缀**：`vbashShellName` 对内置 profile 返回 `vbash`（原实现取 `profile.Path`，现该字段为空），
  `vbashArg0` 回落 `bash`。
- **描述**：`describeVbash` 不再恒带「盘符写作 /c/」提示，仅 `GOOS == windows` 时给出（该提示
  原先靠「宿主非 POSIX」间接判断，现在与宿主无关）。

测试：`vbash_test.go` 的 4 处配置改走 `Override: BuiltinShellName`；新增
`TestVbashViaShellConfigValue`（同级取值、`Path` 为空）、`TestVbashShellNameCaseInsensitive`、
`TestRegularShellStaysNonBuiltin`（`bash`/`sh`/`zsh` 不被误判）、
`TestResolveBuiltinFallsBackToHostShell`；`TestVbashConfigAcceptsNonPosixShell` 改名
`TestVbashIgnoresHostShell`（断言改为「不依赖宿主 shell」，去掉过时的 `/c/` 断言）。
`tools_test.go` 三条转发用例改走 `ShellOverride`。

验收：`gofmt -l` 干净、`go build`/`vet ./...`、`go test ./...` 全绿、`make cross` 通过、
基准 290/290。文档 `docs/shell-tool.md` §17 与 `docs/vbash-capability.md` §2 已改写开关口径。

## 2026-11-09 fix(vbash): 赋值前缀吞副作用、nounset 漏 `${}`、EPIPE 误诊断；tanya 接线围栏并默认 vbash

调用方（tanya）试用前的阻塞项修复，三处均为**静默错误**（不报错、返回错误结果），危害高于报错类缺口：

- **赋值前缀吞掉内建副作用（P0）**：`runner.go` 原先在派发前 `saved := r.Env.Clone()`、派发后
  `r.Env.vars = saved.vars` 整体回滚，内建自己写的变量一并被抹——`IFS= read -r line < f` 读出的
  `line` 恒为空、`FOO=bar export E=1` 的 `E` 丢失、`printf -v v` 的 `v` 丢失（**且静默返回 0**，
  agent 会当成「文件是空的」继续跑）。改为只回滚**前缀变量名**本身：新增
  `env.go` 的 `savePrefixVars`/`restorePrefixVars`，命中 bash 语义——前缀不持久（结束恢复原值，
  含内建写同名变量的情形 `V=prev; V=pre read V` → `prev`）、内建副作用保留，求值顺序左到右。
  函数分支同样保留函数体对全局变量的赋值（原先也一并丢失）。
- **`set -u` 对 `${VAR}` 完全不生效（P0）**：nounset 检查只挂在 `SegVar`（`$VAR`）上，
  `${nope}`/`${#nope}`/`${nope^^}`/`${nope:0:2}`/`${nope#x}`/`${nope/x/y}` 全部静默返回空。
  新增 `bracedUnboundName`（`nounset.go`）并在 `bracedAt()` 入口统一检查；带默认值的
  `:-`/`-`/`:=`/`=`/`:+`/`+`/`?`/`:?` 豁免（`bracedOpSuppliesDefault`）。报错名按 bash 口径区分：
  `$!` 报 `$!`、`${!}` 报 `!`、`$9` 报 `$9`、`${9}` 报 `9`（`expandUnboundName` 的白名单原先漏了 `!`，
  即基准里唯一失败的 `T1/054`）。`parameter()` 改为把 `seg.Pos` 透传给 `bracedAt`，行号不再丢失。
- **`printf -v var` 未实现**：原实现把 `-v` 当普通参数，输出 `-vv=` 之类的垃圾。补上后写变量、
  不输出、坏标识符报 `not a valid identifier`（退出码 1 且继续）、`-v` 缺参报 usage（退出码 2）。
- **内建写已关闭管道误报 `broken pipe`（P2，既有缺陷，非本轮回归）**：`echo hi | true` 会往 stderr
  打 `echo: write error: write |1: broken pipe`（bash 静默）。bash 由 SIGPIPE 默认动作吸收，vbash
  作为进程内解释器没有信号可依赖。收敛在唯一出口 `(*IO).WriteString`：命中 `syscall.EPIPE` /
  `io.ErrClosedPipe`（含文案兜底）返回 nil，其余错误照旧上报。这是基准 `T3c/128` 偶发失败的根因
  （并发 8 路 × 50 轮：修复前 23/400 命中、基线提交 `e1f0917` 同样 25/400，修复后 0/400）。

**tanya 侧**（另一仓库，随本轮一起验证）：`main.go` 在 `cfg.Vbash` 为真时启动
`vbash.EnableSubreaper()` + `NewFence()` 并 `defer fence.Reap()`，经 `tools.Options.Fence` 传入。
首轮只把 `Reap` 挂在 `exitNow` 上不够——`ask`/REPL 正常结束走 `main` 直接 `return`，围栏从不执行
（实测 `sleep 240 &` 在进程退出后仍在跑）；改为构造处 `defer` 后端到端确认无残留。配置项 `vbash`
默认值本就是 `true`（`config.Default()`），本轮确认加载链路无覆盖，`tanya config` 输出 `vbash: true`。

**测试**：基准新增 T2/058–065（赋值前缀 5 条 + `printf -v` + nounset 三条，golden 由真 bash 录制），
总数 282 → **290，七层 100%**；单测新增 13 条（赋值前缀 5、`printf -v` 2、nounset 4、EPIPE 2）。
`T1/054` 由失败转绿。另用 50 条 agent 日常写法在 bwrap 隔离沙箱内与真 bash 差分，
stdout+退出码 **50/50 一致**。

**验收**：`gofmt -l` 干净、`go build`/`vet ./...`、`go test ./...` 与 `vbash`/`tools/...` 的 `-race`
全绿、`make cross` 通过、基准连跑 5 次 290/290。

## 2026-11-07 fix(vbash): 围栏真落地——登记全集/管理 `&` 子集/sid 判放行，作业表回归纯语义

前一轮 review 定位的九项缺陷全部闭合（探针均有真 bash 对照），核心是把「作业表既当 `wait`/`$!`
语义载体、又当围栏收割载体」的双重身份拆开：

- **作业表（`wait`/`$!`）**：`reap` 改为只标记完成、不删条目，状态保留到被 `wait` 消费（修
  `wait $pid` 对已完成作业 20/20 必现的 127）；复合 `&` 分支登记合成作业（无 pid），`wait` 无参
  会等它（修 `( sleep 1 ) & wait` 0ms 返回）；`$!` 完成后保留最后 pid、从未起过作业时为空，
  `set -u` 下报 unbound（报文对齐 bash 的 `$!:` 形态）
- **围栏（`Fence`）**：改为按进程登记——只登记 `&` 分支起的进程（pid+starttime；前台命令不进围栏，
  其生命周期归 `CancelCmd`/ctx，避免无人消费的登记开销）；`Reap()` 依 `/proc` 快照收集子孙、按 `sid != 主 sid` 放行整棵逃逸
  会话树（**该判据此前只写在文档里，代码从未实现**——旧实现对登记作业无条件 `kill(-pid)`，
  非 fork 型 setsid daemon 照杀）、按进程组去重收割；根已退出也照杀其进程组（覆盖
  `sh -c 'x &'` 的同组孤儿）；starttime 防 pid 复用。旧实现的 `kill(-pid)` 一刀切与围栏作业表
  泄漏（50 轮 50 表）随之消除
- **`&` 位路由**：内建与已定义函数回落到虚拟子 shell 分支执行（修 `export X=1 &`/`f &` 报
  `command not found`）；异步路径的外部命令解析与前台共用 `resolveExternal`（删除重复的
  `lookupAsync`）
- **后台 `<` 重定向**：仅当 fd0 未重定向时才接 `/dev/null`（修 `cat < f &` 丢输入）
- **平台片重排**：`/proc` 读取移入 linux 片；`getsid` 判定走 POSIX `RawSyscall`（linux/darwin
  共享）；darwin 无 `/proc` 退化为根级判放行 + 杀根进程组（漏收方向，见 capability §4.1）
- **tools 接线**：`tools.Options.Fence` → `tools/shell.Config.Fence` → `vbash.Config.Fence`
  透传打通（此前围栏在真实路径上是死代码）；调用方职责（启动 `EnableSubreaper`、退出 `Reap`）
  写进文档

基准新增 `T1/050`–`056`（wait 时序/已完成作业状态/后台 `<`/无作业 `$!`/nounset `$!`/内建函数
`&`/`$!` 保留），`make vbash-check` 282/282；单测新增围栏覆盖复合 `&`、死根孤儿、wait 语义、
`$!` 三态、内建函数 `&` 共 8 条（`vbash/jobs_test.go`）与 tools 接线 1 条。

## 2026-11-07 feat(vbash): 后台 `&` 真异步 + 进程围栏（跨轮存活、退出收割、主动 setsid 放行）

原实现把 `&` 当**同步**处理（`sleep 2 & echo done` 等 2 秒），导致三条硬伤：后台进程活不过单轮
（`RunScript` 返回前被 `CancelCmd` 杀掉）、`set -e` 下 `false &` 吞掉后续全部命令、`$!` 恒空使
`kill $!` 变硬错。语料口径：tanya 1762 + ivan 3054 条历史 `run_shell`，命令位 `&` 占 1.0%，按 AST
递归（含 `()`/`{}`/循环/函数体）占 2.38% / 1.08%，`( cmd & )` 形态 24 / 8 处。

**真异步**

| 用例 | 修复前 | 修复后 | bash |
|---|---|---|---|
| `sleep 2 & echo done` | 2009ms | ~1ms | ~5ms |
| `sleep 1 & sleep 1 & sleep 1 & wait` | 3018ms | 1006ms | 1006ms |
| `set -e; false & echo after; echo end` | 无输出 rc=1 | `after`/`end` rc=0 | 同 |
| `echo $!` / `wait $!` | 空 / 空操作 | pid / 作业状态 | 同 |

命令位单命令直接 `Start()` 真进程；`( cmd & )`、管道内、循环内、函数内由 goroutine 跑隔离 env
（bash 是子 shell，`x=1 & echo $x` 有时序偶然性；库内并发必须隔离，属有意偏差，已入 `capability`）。
后台命令 stdin 接 `/dev/null`，赋值前缀与 `cd` 不回流父 shell。**作业控制不做**（`jobs`/`kill %N`/`fg`/`bg`/`disown`：
语料零使用，且需终端所有权），`jobs` 回到 `command not found`(127) 的既有口径。

**进程围栏**

目标改为"后台进程只跟随调用方主进程生命周期、退出时保底拦截防污染"，**不是**作业控制：

- `vbash.EnableSubreaper()`：POSIX `PR_SET_CHILD_SUBREAPER`，零权限，让孤儿重新挂到主进程下
- `vbash.NewFence()` + `Config.Fence`：登记本次运行起的作业表
- `Fence.Reap()`：主进程退出路径调用，按进程组 `SIGKILL` + 宽限 + `Wait4(WNOHANG)` 清僵尸
- **单轮 `RunScript` 结束不收割**（跨轮存活是需求）；主动 `setsid`/double-fork 视为有意逃逸，一律放行
- `vbash.NewSyncWriter(w)`：后台与前台并发写同一 writer 时串行化（Out/Err 各包一层互不干扰）

**验证**：`go test ./...` 与 `-race` 全绿；新单测 `vbash/jobs_test.go`（10 条，含跨轮存活、
围栏收割、setsid 放行、并发写）；基准新增 `T1/043`–`049`（golden 由真 bash 录制），
`make vbash-check` 275/275 全通过。文档：`capability` §4.1 进程围栏（含已知边界）、
`plan` §3.4、`open-issues` §3。

## 2026-11-06 fix(vbash): 修掉调用方真实试用暴露的 13 类缺陷（P0 9 项、P1/P2 4 项）

调用方（tanya 开启 vbash 后）做了一轮真 bash ↔ vbash 差分实测（约 180 条用例），暴露一批
**文档未记录**的缺陷。本轮全部修完，语料口径为 1764 条历史 `run_shell` 调用。

**P0（会硬报错、静默错或误伤宿主）**

| # | 缺陷 | 修复前 → 修复后 |
|---|---|---|
| 1 | heredoc 体内 `$( )` / `$(( ))` | 退出码 1 / 静默空 → 正常展开（含嵌套） |
| 2 | heredoc 体内反斜杠与续行 | `\$`/`\\`/`` \` `` 全错、续行不拼接 → 与 bash 一致 |
| 3 | `test`/`[` 不认负数、`010` 当八进制 | 报 `integer expression expected` → 负数/`+3` 可用，`010`=10（`0x10` 报错，与 bash 同） |
| 4 | `exit -1` 返回 0、`exit abc` 静默 | → 255 / 诊断 + 继续执行（`exit 1 2` 终止） |
| 5 | 循环外 `break`/`continue` | **吞掉后续命令** → 告警 + 继续；函数内的 `break` 不算循环内 |
| 6 | `for x; do`（省略 `in`） | 空迭代 → 遍历位置参数 |
| 7 | `echo -e` 的 `\c` 与 `\0NNN` | 不抑制换行、不解释八进制 → 均与 bash 一致（另补 `\xHH`/`\uHHHH`/`\UHHHHHHHH`） |
| 8 | 算术内嵌 `${…}` / `$( )` / `$(( ))` | 硬报错 / 静默 0 → 正常求值 |
| 9 | `$$` 恒 0（`kill $$` 杀穿宿主进程组） | → `$$` = 宿主 pid；新增内建 `kill` 安全阀：`pid<=0` 与宿主自身 pid 一律拒绝，`-0` 探测自身放行 |

**P1/P2（静默错或不一致）**

| # | 缺陷 | 修复后 |
|---|---|---|
| 10 | `$*` 双引号内不按 IFS 首字符 | `Env.JoinArgs()` 统一三处 |
| 11 | `set -o pipefail` / `set -u` 被忽略 | pipefail 取首个非零段；nounset 诊断 + 退出码 127；`set` 解析重写（选项簇、`-o`/`+o`、不再吞位置参数） |
| 12 | `${V:off:len}`、`${V^^}`、`${!V}`、`base#`、`n++`、`**` | 全部实现；后置 `n++` 要求在 `parseAssign` 之前识别（否则名字被当赋值吃掉） |
| 13 | `printf %q` 用单引号包裹 | 改为逐字符反斜杠转义，43 个字符与 bash 零差异 |

**结构变化**

- 新增 `vbash/numparse.go`（三种整数口径：算术/`test`/`exit`）、`arith_dollar.go`、`arith_extra.go`、
  `expand_extra.go`、`nounset.go`、`loopctl.go`、`kill.go`、`shellquote.go`
- `lexer.go` 新增 `lexHeredocSegments`：heredoc 正文走与双引号相同的词法路径，替换原手写 `$` 扫描
- `platformOps` 新增 `DeliverSignal`（posix 側 `syscall.Kill`，其余 stub）
- `toolDesc` 去掉已修项（`${V:off:len}`/`n++`/`**`/`pipefail`），保留仍不支持项
- 顺手清理 `tools/shell/vbash.go` 的 `classifyVbashError` 未使用形参

**验证**

- 基准 228 → **268**（`T3h/021`–`030`、`T3c/134`–`159`、`T2/054`–`057`），golden 由真 bash 录制，七层 100%
- 单测新增 `vbash/fixes_test.go` 共 37 条
- `go build ./... && go vet ./... && go test ./... && go test -race ./... && make cross && make vbash-check` 全绿
- 文档：`vbash-capability.md` §3.1/§3.3/§4/§5 与 `vbash-open-issues.md` §6 全面更新；新增 §3.3.1 记录不复刻的 bash 怪癖

## 2026-11-06 feat(tools): vbash 工具描述列出不支持的构造与替代写法

`run_shell` 启用 vbash 后，描述里原本只有一句「实验特性，不支持的构造直接报错」，
模型撞到 `case`/`[[ ]]` 等报错后只能猜替代方案（实测会反复试探 `[[ ]]` 是否可用）。

现将高频不支持项与替代写法直接写进描述：`case`/`[[ ]]`/反引号/`<<<`/`(( ))`/进程替换/数组/
函数内改全局变量/`${V:off:len}`/算术 `n++` 与 `**`/`set -o pipefail`/`$RANDOM` →
改用 `if`/`elif`、`[ ]`、`$( )`、heredoc、`$(( ))`。

描述在 POSIX 与 Windows 宿主上都前置同一句（Windows 另有路径写法提示）。
单测 `TestVbashDescriptionListsUnsupported` 守关键词。

## 2026-11-06 fix(vbash): 复合命令后接带 fd 前缀的重定向解析失败

`parseCommand` 的复合命令分支（`cmd.Group`/`cmd.Func`/`cmd.Compound` 已就位后）只识别 `TokOp` 形态的
重定向，没有走 `redirectFd`，于是紧跟 `done`/`fi`/`esac`/`}`/`)` 的数字前缀全部落到「未预期的记号」：

| 写法 | 修复前 | 修复后（与 bash 一致） |
|---|---|---|
| `for i in 1; do echo $i; done 2>/dev/null` | 语法错误、退出码 2 | 正常执行 |
| `while false; do :; done 2>/dev/null` | 同上 | 正常执行 |
| `if true; then :; fi 2>/dev/null` | 同上 | 正常执行 |
| `{ echo a; } 2>/dev/null` | 同上 | 正常执行 |
| `( echo a ) 2>/dev/null` | 同上 | 正常执行 |
| `for i in 1; do echo $i; done 2>&1 \| cat` | 同上 | 正常执行 |
| `for i in 1; do echo $i; done >f` | 正常 | 不变（无 fd 前缀本来就支持） |

`vbash-capability.md` §3.1 早已写「redirs 可挂在分组/复合命令/函数上」，是实现没跟上文档，不是设计取舍。
真实语料命中率 0.90%（11/1219 条历史 `run_shell` 调用），形态集中在「跑批时静音 stderr」——
一开 vbash 就会把原本能跑的命令变成硬报错，属接入前必须闭合的缺陷。

- **修法**：抽出 `(*parser).tryFdRedirect(tok)`，把「数字 fd + 紧跟重定向符」的识别与装配收成一处，
  普通命令路径与复合命令分支共用；行为对普通命令完全不变（原内联逻辑原样搬入）
- **基准**：T3c 新增 `129`–`133`（`/dev/null`、写文件、`2>&1 >file` 保序、子 shell、`if` 上挂 stdin），
  golden 由真 bash 录制；总数 **223 → 228**，七层全部 100%
- **单测**：`vbash/parser_test.go` 新增 `TestParseFdRedirectOnCompound`（6 条形态）与
  `TestParseFdRedirectAfterCompoundKeepsOrder`（复合命令上的顺序敏感性）
- **函数定义的重定向**（`f(){ :; } 2>/dev/null`）仍走 `tryFuncDef` 自己的路径，本轮未改动——
  实测 bash 与 vbash 均已正常（重定向作用于定义语句本身），只是不走新抽的 helper

## 2026-11-05 fix(vbash): 补三处递归/规模上限

查「变量展开是否有递归深度限制」时发现的三处缺口，全部按「加明确上限 + 明确报错」闭合。
变量本身不做递归展开（`a='${b}'; b='${a}'; echo "$a"` → 字面 `${b}`，与 bash 一致），不存在间接爆炸。

- **大括号展开加递归深度上限**（`vbash/brace.go`）：`braceAlternatesRaw`/`braceAlternateAt` 原本裸递归，
  `Parse` 拦不住（大括号在词段里是普通字面量，不进 `lexer.readBraced`），`{a,{a,{a,…}}}` 每层递归一次。
  改成有状态展开器，复用 `maxNestDepth = 64`，超限报「大括号展开嵌套过深（上限 64 层）」
- **大括号展开加字段数上限**：`{a,b}` 连写 n 次产生 2ⁿ 个字段（实测 n=20 → 104 万/1.4s、
  n=22 → 419 万/6.8s，66 字节命令就能吃掉几 GB）。新增 `maxBraceFields = 16384`：
  - 预算按**一次词列表**累积（`expandWords` / `wordListToItems` 各持一个 `braceLimits`），
    不是每词各自计——否则多个词拼起来照样绕过
  - `{1..N}` 序列在生成循环里就中断并报错，不会先造完再检查（`{1..999999999}` 现在瞬时失败）
  - 超限报「大括号展开字段过多（上限 16384 个）」
- **shell 函数调用加深度上限**：`f(){ f; }; f` 原本无上限（2 万层耗时 4.8s，深度取决于宿主栈大小）。
  用上一轮遗留的闲置字段 `saveDepth`（改为 `funcDepth`）+ `maxFuncDepth = 512`，超限打
  `f: maximum function nesting level exceeded (512)` 并让该次调用返回 1、**脚本继续执行**——
  这是 bash `FUNCNEST` 的语义（真 bash 处同一场景直接 SIGSEGV）
  - `branch()` 复制 `funcDepth`：子 shell 与管道分支是同 goroutine 同步递归，不继承就等于
    `f(){ ( f ); }` 能绕过上限
  - `dispatch` 现在把 `pos` 传给 `callFunc`，诊断带行号
- **顺带修掉一处 O(n²)**（本轮查上限时发现，非本轮引入）：`findBraceFrom` 对每个 `{` 都向后线性
  扫描到结尾，`{a` 重复 n 次就是 O(n²)——实测 n=1600 已要 1.36s，n=5000 直接卡住 15s 以上（一条几百字节
  的命令即可触发）。改为先做一次栈式配对，再按配对表定位，`{a`×20000 现在是 3ms。
  单测 `TestBraceUnmatchedOpenLinear` 守护
- **顺带修掉管道段/命令替换里看不到函数**（本轮查上限时发现，非本轮引入）：
  `f(){ echo x; }; f | cat` 报 `command not found`，`echo "[$(f)]"` 同样。根因是
  `runPipeline` 与 `commandSubstitution` 用 `branch()` 造子环境而 `branch` 不复制 `funcs`
  （`runSubshell` 显式补了那行，管道没补）
  - 修法不能照抄那行：管道段并发在 goroutine 里，共享 map 会让「段内定义函数」变成并发 map 写。
    改为 `branch` 里 `copyFuncs` 各拷一份（`*funcDef` 指针共享，函数体 AST 不可变），
    `runSubshell` 的手动赋值随之删除
  - 语义与 bash 逐项核对：管道各段可见、命令替换可见、段内重定义只在该段生效、
    子 shell 内定义不泄漏到外层
  - 基准新增 `T3c/127`、`T3c/128`（golden 由真 bash 录制）；单测 `funcdepth_test.go` 加 3 条
- **函数形态调整**（包内）：`expandBraceWord(w) []Word` 换成 `(*braceLimits).word(w) ([]Word, error)`，
  展开器自己持有深度与字段预算；`braceSplitChars`/`braceSequenceChars` 用第三返回值表示「超预算」而非
  error，避免为哨兵造错误类型
- **基准**：T2 新增 `052`/`053`、T3c 新增 `126`/`127`/`128`（golden 由真 bash 录制）；
  总数 **218 → 223**，七层全部 100%。超限行为由单测守护（`vbash/brace_test.go` 新增 5 条、
  `vbash/funcdepth_test.go` 新增 6 条）——上限本身是宿主相关的安全阀，不写进跨实现基准
- **文档**：`capability` §3.4 扩为「嵌套深度与规模上限」，补大括号深度、字段数与函数深度三行，
  说明为何是「安全阀」以及子 shell 为何要继承深度；删除 §3.3 差异表现已修好的「管道段内调用已定义函数」一行；
  `vbash-open-issues.md` 新增 §3.1（三处上限）与 §3.2（函数可见性）；基准数字 218 → 223
  同步到 `vbash-bench.md` / `vbash.md` / `plan` 状态行

## 2026-11-05 feat(vbash): 实现大括号展开、printf 完整格式化、$(<file)

承接上一轮的遗留清单，三条 P1 全部按「实现」路线闭合（不再报错/不再静默错结果）。

- **大括号展开（`vbash/brace.go` 新）**：`{a,b}` 支持任意层嵌套、前后缀拼接、连写笛卡尔积；
  `{1..3}`/`{3..1}`/`{01..03}`/`{1..10..3}`/`{a..e..2}`/`{-2..2}` 支持步长与前导零；引号内与
  `\{` 转义不展开，`{}`/`{a}` 按字面保留。接入命令参数与 `for … in` 列表，**赋值上下文不展开**
  （`x={a,b}` 保持字面，与 bash 一致）。
  - 实现要点：把词段摊平成「字符 + 是否引用」序列后找最外层括号对，因此 `{"a,b",c}`、
    `{a,"b,c"}`、`{a\,b,c}` 的引号/转义语义天然正确；`{a,{b,c}}`、`{{a,b}}` 这类嵌套按 bash
    的「外层优先、失败退到下一个 `{`」规则递归。
  - 空元素规则按 bash 区分：逗号列表的空项不产出字段（`{a,}`→`a`），序列里跨 `\` 的空项产出
    空字段（`{Z..b}` 中间那个），整词为空时才落一个空参数（`{,,}`）。
  - 花括号分组 `{ echo a; }` 在 AST 里是 `GroupKind = GroupBrace`、不进词段，天然不冲突
- **`printf` 完整格式化（`vbash/printf.go` 新，从 `builtin.go` 拆出）**：
  - 浮点 `f F e E g G`：精度、宽度、`-`/`0`/`+`/空格/`#`、`%g` 的 `1e+06` 与尾零裁剪、
    `%e` 指数补齐两位
  - 整数 `d i u o x X`：`%.Nd` 补零（负数把符号算进精度宽度）、`%u`/`%x` 对负数按 64 位无符号、
    `%#x`/`%#o` 前缀、`%+d`/`% d`、`%.Nd` 下 `0` 填充让位
  - 字符串 `s c b q`：`%.Ns` 按 rune 截断、宽度/左对齐；`%.0s` 为空
  - 数字解析：`0x` 十六进制、前导零八进制（`010`→8）、`'A` 取字符、溢出取 `MaxInt64`/`MinInt64`、
    `3.9`/`12abc` 取前缀值（诊断仍报错）
  - 诊断口径与 bash 逐字节对齐（`LC_ALL=C` 核对）：`abc: invalid number`、
    `08: invalid octal number`、`99999999999999999999: Numerical result out of range`，退出码 1；
    原先 `formatPrintf` 返回的 error 被 `builtinPrintf` 转成诊断，现在改为回传 warn 字符串
- **`$(<file)`（`vbash/runner.go`）**：等价 `$(cat file)` 但不起子进程；相对路径按当前 `Dir` 解析，
  读失败时打 `name: No such file or directory` 诊断（用**原样的相对名**，不打绝对路径）、输出空、
  不改变整体退出码——与 bash 一致
- **基准**：T2 新增 14 条（`T2/038`–`T2/051`），golden 由真 bash 录制；总数 **204 → 218**，
  七层全部 100%。单测新增 `vbash/brace_test.go`（3 条）、`vbash/printf_test.go`（4 条）、
  `vbash/subst_test.go`（2 条）
- **文档校正**：
  - `capability` §3.1 把大括号展开与 `$(<file)` 移入已支持，`printf` 行改写为完整能力；
    删掉错误的「整数精度生效，浮点精度不生效」；§3.3 移除已闭合的浮点 `printf` 与 `%d` 取整两行
  - `capability` §3.3/§4 与 `plan` §3.4 统一后台 `&` 口径（**同步执行**、`wait` 空操作返回 0、
    裸 `&` 同样等待），补实测耗时并说明「`nohup server &` 会在超时上限处被强杀」这一后果
  - `plan` §3.4 的 `eval`/`trap` 由「报错」校正为 `command not found`（127）
  - `vbash-bench.md` / `capability` §5 / `vbash.md` / `plan` 状态行的基准数字同步为 218
  - **`docs/vbash-open-issues.md`** 重写：三条 P1 标记为已闭合并记实现要点与口径修正，
    只留 P3 接线卫生
- **上一轮的口径修正**（本轮复核得出）：大括号展开真实命中 **0.091%（16/17516）**，
  既不是 1.81%（把引号内正则量词算进来了）也不是 0.027%（漏掉片段含 `/` 的形态）；
  shell 层 `printf` 浮点动词只有 **3 行**，原记的 18 条里多数是 `awk` 自己的 printf

## 2026-11-05 docs(vbash): 新增遗留问题清单

- **`docs/vbash-open-issues.md`**（新）：接调用方 `tanya` 的接入评估时发现的问题，按级别列出：
  - **P1 大括号展开静默字面化**：`{a,b}`/`{1..3}` 不展开且不报错，`mkdir -p d/{x,y}` 会真的
    创建名为 `d/{x,y}` 的目录并返回 0。语料命中 0.027%（6/22497），其中 4 条是 `mkdir -p`。
    §3.2 把它列在「明确不支持（报错，不回落）」表里，但该行实际是字面输出，表格语义与行内冲突
  - **P1 `printf` 浮点精度不生效**：`%.3f` 截成 `3.1`、`%e` 不转科学计数，退出码 0。
    语料命中 18 条（0.080%），形态是 `awk` 里做体积/耗时合计的 `printf "%.1f"`
  - **P2 后台 `&`/`wait` 文档自相矛盾**：`plan` §3.4 称「后台启动后不等待、`wait` 报错」，
    实测为**同步等待**（`sleep 2 &` 耗时 2010ms）且 `wait` 返回 0。以实现为准校正计划文档
- **口径修正**：早先一轮统计的「大括号展开命中 1.81%」有误——把**引号内的正则量词**
  （`sed "s/^.\{0,45\}\] //"`）算成了展开。重算口径为「引号状态机判定未引用 + 剥掉 heredoc 正文」，
  真实值 0.027%
- **复核留档**（非缺陷）：`eval`/`trap` 均按普通命令处理 → 127（`plan` 写「报错」需按实校正）；
  Windows 输出解码未受影响（`streamCapture.finish()` 走 `toUTF8` → `decodeStream`）
- **接入侧 P3**：`tools.Standard` 已能透传 `Vbash`/`Environ` 但无人使用；`MsgVbashPosixFmt`
  是 0 引用死符号；`describeVbash` 属 Windows 路径、未真机验证
- **未改动任何实现**：本条只加文档与索引，`vbash` 与 `tools/shell` 代码保持原样

## 2026-11-05 docs(vbash): 补记真实语料差分的数据来源与隔离纪律

- **背景**：为评估 vbash 接入可行度，曾抽取真实会话的历史 `run_shell` 命令做 vbash 与宿主 bash 的
  行为差分。事后复查发现该轮的过滤条件不足：只按首词白名单判只读（拦不住 `sed -i`/`sort -o`/
  `find -delete` 这类写标志）、把子进程 `cwd` 当成沙盒（命令内部的 `cd` 不受其约束）、
  未限制访问范围（只读遍历可触达可移动介质与整机路径）。复核确认**无实际损害**：
  工作树干净、时间窗内无新增文件、越界写入的目标父目录本就不存在、沙盒外无写入目标
- **`docs/vbash-capability.md` §5.2（新）**：记录该轮的三个方法缺陷、事后复核结论、
  以及**哪些结论仍然可用**（解析层通过率属纯静态统计不受影响；行为差异项另有 90 条自建沙盒用例独立复现）
- **`AGENTS.md`《构建与测试》**：新增硬约束——跨实现差分属执行未知代码，一律在容器 /
  mount namespace / 只读 bind mount 内跑，或只跑纯内建用例；**不得以「子进程 cwd 指向临时目录」
  代替隔离**
- **未改动**：基准用例、vbash 实现与既有文档结论均不变（本条只补方法学与纪律）

## 2026-11-05 feat(vbash): exec 内建与 fd 关闭语义（fd 支持补课）

- **背景**：检查 fd 支持时发现 `/dev/null` 本身是好的（`2>/dev/null`、`>f 2>&1`、顺序敏感语义、
  `test -e /dev/null` 全部与 bash 一致），真正的缺口在两处：**`exec` 内建完全没实现**
  （`exec >log 2>&1` 这种最常见的日志重定向直接 `command not found`），以及
  **`>&-`/`<&-` 被当成写文件**（静默给出错误结果，比报错更难发现）
- **`exec` 内建（`vbash/exec_builtin.go` 新）**：三种形态——
  `exec >f 2>&1`（无命令，重定向**持久生效**到脚本结束，后续命令含外部程序的 stderr 都进该文件）、
  `exec <cmd>`（执行后不再继续，退出码取该命令，等价 bash 的进程替换语义）、裸 `exec` 无副作用；
  `-c`/`-l`/`-a name` 前缀接受但语义简化
- **`>&-` / `<&-`（`vbash/io.go`、`redirect.go`）**：`IO` 新增 `Shut map[int]bool` 与 `shutFd`，
  关闭后 fd 1/2 换成 `errWriter`（写入返回 `Bad file descriptor`）、fd 0 换成空读；
  `withWriter` 会清掉关闭标记（`exec 3>&-; exec 3>f` 可重新打开）
- **写错误传播**：`IO.WriteString` 原先吞掉 `io.WriteString` 的错误，现返回 error；
  `echo`/`printf` 写入失败报 `write error: Bad file descriptor` 并记退出码 1（`read` 从已关闭 fd 0 读同样报错）
- **fd 3+ 对外部程序**：`exec.Cmd.ExtraFiles` 只支持从 3 开始连续的 `*os.File`，而 `IO.Extra` 是
  `map[int]io.Writer`（可能 `discard`/内存 writer），整体透传需要重构 IO 表示——本轮**不做**，
  改为首次遇到时打一条 `fd N: not passed to external commands (vbash limitation)`，
  把静默失败变成可诊断。已关闭 stdin 传给外部程序同理提示
- **基准**：新增 6 条（`T1/037`…`T1/042`，golden 由真 bash 录制），总数 198 → **204**；
  单测新增 `vbash/exec_fd_test.go` 10 条。已验证回退 `exec` 分派或 `>&-` 处理会分别挂 4 条 / 2 条
- **文档**：`docs/vbash-capability.md` §3.1 增 `exec` 条目、§3.2 把 `exec` 从不支持清单移出、
  §3.3 改写 fd 3+ 与已关闭 stdin 两行（说清边界与提示文案）、代码地图补 `exec_builtin.go`

## 2026-11-05 feat(tools): vbash 开关透传到公共装配层

- **背景**：S8 把开关做进了 `tools/shell.Config`（`Vbash`/`Environ`），但公共装配层 `tools.Options`
  没有对应字段，`tools.Standard`/`tools.Shell` 不透传——而这是文档推荐的标准入口，
  **等于两个前台都开不了**：tanya `main.go`、ivan `runner/pool.go` 均走 `tools.Standard`，
  两仓库内 `Vbash` 零命中（唯一能开的是 tanya 的集成测试，它直连 `shell.New`）
- **`tools/tools.go`**：`Options` 新增 `Vbash bool` 与 `Environ func() []string`，`Shell` 原样转发到
  `shell.Config`。**不加「`Vbash` 即自动用 `os.Environ`」的便捷开关**——环境来源属前台决定，
  库不预设（与「库不读环境变量」的既有约束一致），缺失时由 `shell.New` 装配期报错兜底
- **`tools/tools_test.go`**：新增三条转发用例（经 `Standard` 能开启并真的走解释器、缺 `Environ`
  装配期报错、默认关闭）。已验证「字段存在但不转发」时前两条失败，确认用例有效
- **`docs/shell-tool.md` §17**：开关表改为两层对照 + 前台接入示例，写明两侧都要置位
- **验证**：`go build`/`vet`/`test` 全绿，`make cross` OK，`make vbash-self` 198/198；
  tanya 与 ivan 均 `go build ./...` 通过（新字段可选，不改则行为不变）

## 2026-11-05 feat(vbash): 跨平台归一落地（Windows 分片实现）

- **背景**：规划 §5 与 D3 把「跨平台归一」列为核心价值，但 `vbash/` 下实际没有任何平台分片：
  路径全程 `path.Clean` + 直接 `os.Stat`（假设宿主就是 POSIX）、`/dev/null` 是字符串比较、
  信号表散在 `signal_unix.go`/`signal_other.go`、`tools/shell` 还**拒绝**在非 POSIX 宿主上启用 vbash
  ——即 Windows 上特性根本开不起来
- **`vbash/platform.go`（新）**：`platformOps` 差异表 + 归一原语
  （`posixPath`/`hostPath`/`hostDir`/`isPathLike`/`isDevNull`/`resolveSymlinks`）。
  内部只有一个表示：**POSIX `/`**，只在文件系统调用与 `exec`（`cmd.Dir`、`argv[0]`）两个边界转宿主形式
- **平台白名单分片（新）**：`platform_linux.go` / `platform_darwin.go` 取 `posixPlatform()`，
  `platform_windows.go` 自带实现，`platform_posix.go` 放 linux||darwin 共享，其余落 `platform_stub.go`；
  删除 `signal_unix.go`/`signal_other.go`（被表取代）
- **Windows 侧**：`C:\x` ↔ `/c/x` 盘符归一（Git-Bash 惯例；`d/x` 仅在 `D:\` 存在时当盘符）、UNC 保留、
  `PATH` 内部 `:` 分隔而宿主 `;`、`PATHEXT` 式后缀查找（已带后缀不重复追加）、
  glob 大小写不敏感、退出码取低 8 位且 `0xC000013A`（Ctrl-C）记 130
- **`vbash/env.go`**：`hostVar`/`internalVar` 让 `HOME`/`PWD`/`OLDPWD` 内部存 POSIX 形式、`Env.Environ`
  出口转回宿主形式；`Runner` 入口归一 `Config.Dir`/`Home`/`Env`，调用方仍传原生路径
- **`tools/shell`**：**去掉「启用 vbash 要求宿主 shell 是 POSIX」的装配期拒绝**——拒绝等于特性在 Windows
  上不可用，与 D1 直接矛盾；非 POSIX 宿主上的描述改走 `describeVbash`（提示 `/` 分隔、`/c/` 盘符、
  `\` 只作转义），诊断前缀取 `bash` 不再出现 `pwsh.exe`
- **顺带修既有缺陷**：路径型命令不存在时退出码原为 1，改为 127（不存在）/126（不可执行）并补诊断，对齐 bash
- **修本轮引入的回归**：`IsExec` 首版只查 `mode&0111`，而目录的 0755 也满足，`PATH` 中同名目录会被当成
  可执行文件选中（基准 `C/029` 报 `cp: Permission denied`），现要求 `!st.IsDir()`；
  新增 `TestLookPathSkipsDirectory`（已确认修复前失败、修复后通过）
- **验证**：`go build`/`vet`/`test`/`test -race`/`make cross` 全绿；
  `make vbash-check`（真 bash 录 golden）与 `make vbash-self`（跑 vbash 适配器）均 **198/198**
  （POSIX 侧归一是恒等变换，语义未变）。**Windows/macOS 真机基准未跑**，文档一律标「实现完成、待验证」
- **口径提醒**：`make vbash-check` 只校验用例与真 bash 一致，**不测 vbash 本体**；被测实现的回归只有
  `make vbash-self` 会发现——本轮那条 `PATH` 目录回归就是靠它才暴露
- **文档**：`docs/vbash-plan.md` §5.4 新增分片落点表、S6 标完成并加未验证说明；
  `docs/vbash-impl.md` 新增 S6 节（6 条偏差）；`docs/vbash-capability.md` 新增 §3.5 跨平台行为、
  更新 §2 约束表与代码地图

## 2026-11-04 fix(vbash): 空引号参数被丢弃，导致参数个数与位置错位

- **症状**：`f ""` 传入 0 个参数（bash 是 1 个）；`[ -n "" ]` 判为真（bash 为假）；
  `f a "" b` 位置参数整体前移成 `2:[a][b]`（bash 为 `3:[a][]`）；`grep ""` 退化成
  无模式匹配。属于**静默给出错误结果**，比报错更难发现
- **根因**：`""` 在 lexer 里产生一个 `SegLiteral{Text: ""}` 段，`compactSegments`
  按「空文本」把它丢弃，`Word` 因此丢失「曾被引用」的信息，`expand()` 的
  空词兜底分支 `if w.Quoted() { return []string{""} }` 永远走不到
- **`vbash/lexer.go`**：`compactSegments` 记住第一个被引用（`Mode != ModeUnquoted`）
  的空字面量段，整词无其它内容时保留它；仅在 `out` 为空时回填，避免影响
  `"$X"` 这类「引用 + 展开」的段数（既有 parser 用例依赖该形状）
- **基准**：`T2-expansion.case` 新增 4 条（空参数计数、中间空参数、`"$@"` 遍历、
  `[ -n "" ]`、空引号拼接），golden 由真 bash 录制。回退修复后这 4 条失败（50%），
  确认用例有效。总用例 193 → 198
- **`docs/vbash-capability.md`**：修正 `printf` 的矛盾表述——§3.1 原写「精度」支持，
  §3.3 又写「精度不生效」；实测确认**整数精度生效、浮点精度不生效**，据此改写

## 2026-11-04 perf(vbash): `$( )` 嵌套解析——实测确认瓶颈主体已由字面量累积修复解决

- **背景**：上一轮记录「`readCmdSub` 每层复制内层文本，大命令仍慢」作为遗留项。本轮逐项实测归因
- **实测结论**：同口径（50KB body）对比 `wordBuilder` 修复前后——
  `57eebc1` 单层 328ms / 8 层 374ms → 当前 1.77ms / 6.03ms，**约 185 倍**。
  该遗留项的主体已由 `d2d626b` 修掉，无需再动内部表示
- **尝试过并否决的方案**（避免后人重复摸索）：
  - 手写 ASCII 快速路径替代 `[]rune(src)`：比 runtime 汇编版 `stringtoslicerune` 更慢，已回退
  - `readCmdSub` 改用 `begin` 索引做零拷贝切片：分配降 35%（286MB→185MB）、
    allocs 降 51%（3536→1744），但**耗时反而 321ms→424ms**（扫描循环多维护一个游标），已回退
- **剩余开销性质**：`$( )` 嵌套耗时随层数线性增长（64 层 400KB body 约 296ms），
  属于「每层必须重新解析内层」的固有成本，已由深度上限封住；真实命令在微秒级
- **`docs/vbash-capability.md`**：新增「5.1 解析性能」，记录分层耗时表与这个复核口径

## 2026-11-04 fix(vbash): 管道读端由消费方关闭，消除 fd 数据竞争

- **症状**：`go test -race` 偶发 `TestRunPipeline` 数据竞争——主协程在 `wg.Wait()` 之前
  就批量 `pr.Close()`，而消费方协程可能仍在 `exec.StartProcess` 里读同一个 `*os.File` 的 fd。
  这不只是 race 报告：子进程可能拿到已关闭的 fd
- **`vbash/runner.go`**：改由每个管道的**消费方**关闭读端——`pending` 记录上一段的读端，
  取用时移交给当前段，消费方命令结束后（含协程 defer）关闭；主协程不再提前批量关闭
- 建管道失败时先关已持有的读端、再 `wg.Wait()`，避免协程持有失效读端
- **`vbash/pipeline_test.go`**（新）：100 轮重复管道（fd 泄漏）、三段管道、5 万行吞吐、
  消费方 `while read` 逐行读取
- 实测：全量竞态 10 轮零失败（改前约 1/5 概率复现）；200 次管道循环 fd 峰值仅 3

## 2026-11-04 fix(vbash): 大词解析从 O(n²) 降为 O(n)

- **症状**：`wordBuilder.literal` 对尾部字面量逐字符 `Text += s`，字符串每轮整段复制，
  单个大词呈二次增长——实测 200KB 字面量解析耗时 5.9s，400KB 需约 27s（改动前基线同样如此）
- **`vbash/lexer.go`**：`wordBuilder` 增加待定字面量缓冲（`lit`/`litMode`/`litPos`/`litOpen`），
  字面量累积进 `strings.Builder`，遇到非字面量片段（`segment`/`mark`）或收尾时一次性回填 `segs`；
  `literalRune` 改为直接 `WriteRune`，不再每次 `string(r)` 分配
- **效果**：200KB 字面量解析 5.9s → 4ms（约 1400 倍）；基准 193/193 与全部单测零变化
- **`vbash/nestdepth_test.go`**：新增 `TestParseLargeLiteralLinear`，用 25KB/200KB 两档耗时比值
  （阈值 40 倍，正常约 2 倍）守住线性复杂度，回归时会明确报出倍率

## 2026-11-04 fix(vbash): 嵌套深度上限，堵住深层括号 `$()`/`${}`/`$(( ))` 撑爆内存

- **症状**：深层嵌套 `$( $( $( ... ) ) )` 会让 lexer 逐层复制内层文本（O(n²)）且无任何深度上限，
  十万层输入需数十 GB 内存。真实事故中宿主进程被内核 OOM killer 直接收割，
  `recover()` 接不住（OOM 与栈溢出都不是 panic）
- **`vbash/lexer.go`**：`readCmdSub`/`readBraced`/`readArith` 扫描括号时即守 `maxNestDepth`（64 层），
  超限立刻返回 `SyntaxError`——在累积大块文本之前就中止，5 万层输入 0.00s 报错（改前 7.1s）
- **`vbash/parser.go`**：`Parse` 拆出 `parseAt(src, depth)`，`resolveWord` 递归解析 `$(...)` 内层时
  传递并检查深度；错误走 `Token: "$("` + 说明行，不再把中文消息塞进 `near unexpected token` 位
- **`vbash/expand.go`**：`expandArbitrary`（`${x:-...}` 等参数展开的递归）加同样的深度门槛
- **`vbash/arith.go`**：`parsePrimary` 的括号递归加深度门槛
- **`vbash/nestdepth_test.go`**（新）：四个用例守上限内可解析、超限报错、5 万层不得退化（含耗时上界）
- 深度上限取 64：远高于真实脚本（实测常见脚本 < 10 层），足以挡住病态输入

## 2026-11-04 docs(vbash): 新增能力清单文档

- **`docs/vbash-capability.md`**（新）：面向使用者的能力清单——定位、开关用法与装配校验、
  已支持项矩阵（列表/管道/复合命令/函数/分组/重定向/heredoc/展开/算术/内建/`test`/`printf`）、
  明确不支持的构造（报错不回落）、**与真 bash 的已知差异表**（函数作用域、`for x; do`、
  `${V:off:len}`、`base#num`、`pipefail`/`nounset`、浮点 `printf`、后台 `&` 等 25 条，逐条实测）、
  语义细节速查、基准成绩、代码地图、适用场景边界
- **`docs/vbash-plan.md`**：移除 S6/S8 阶段的「Windows 手动验证」表述（该项由用户自行安排，不落文档）

## 2026-11-04 feat(shell): S8 接入 vbash——`run_shell` 可走进程内 bash 解释器（默认关闭）

- **开关**：`shell.Config.Vbash`（默认 `false`）+ `Config.Environ`（启用时必填，`vbash` 不读 `os.Environ`）。非 POSIX shell（pwsh/cmd）下开启在 `New` 期报错；`interactive: true` 仍借终端、不经解释器
- **执行路径**（`tools/shell/vbash.go`）：进程内 `vbash.RunScript`，复用 `streamCapture` 截断、`waitBounded` 放弃等待、`Result` 字段语义；不支持的构造与语法错误**直接报错、不回落宿主 shell**（D9）
- **注入点**（`vbash.Config.ConfigureCmd`/`CancelCmd`）：进程组与树杀策略留在调用方（`platform.ConfigureGroup`/`platform.KillGroup`），库不预设平台策略，Windows `taskkill /T /F` 自动生效
- **取消**：`vbash` 的 `runList` 每项前检查 `Ctx`，纯内建死循环（`while true; do :; done`）也能在截止时间退出；外部命令走 `exec.CommandContext` + `CancelCmd`
- **执行器派生统一**：新增 `Runner.branch(env, io)`，管道/子 shell/命令替换三处克隆点收敛到它（钩子不再漏传）
- **幂等收尾**：`streamCapture.finish()` 加 `sealed`，超时放弃后迟到写入不再改动已交付结果
- **诊断**：`UnsupportedError`/`SyntaxError` 位置为零时省略「第 0 行第 0 列」
- **实测**：`go build`/`vet`/`test`/`test -race`/`make cross` 全绿；`make vbash-self` 仍 **193/193**；新增 `tools/shell/vbash_test.go` 18 条用例
- **文档**：`docs/vbash-impl.md` 新增 S8 节（产物、实现要点、偏差 S8-1…S8-5）、`docs/vbash-plan.md` 标注 S1–S8 完成、`docs/shell-tool.md` 新增 §17

## 2026-11-04 feat(vbash): S7 控制流——基准 193/193 全绿

- **复合命令**（`vbash/parser_control.go`、`runner_control.go`、AST 新增 `Compound`/`CondClause`）：`for … in …; do … done`、`while`/`until …; do … done`、`if`-`elif`-`else`-`fi` 完整解析与执行
- **终止字栈**（`parser.go` 的 `p.stop`）：`parseList` 靠栈决定在 `do`/`then`/`done`/`fi`/`elif`/`else` 处停止；没有它会把 `done` 当普通命令
- **子 shell 与分组**（`runner_group.go`）：`( )` 用 `Env` 克隆隔离（`cd`、变量都不回传），`{ ; }` 共享 `Env`——`T3c/119` 正是靠这条区分
- **函数**：定义登记到 `funcs` 表；调用时切克隆 `Env`（`inFunc`、位置参数换 `argv[1:]`、`arg0` 换函数名），`return` 以 `ReturnRequest` 穿透，新增内建 `local`（仅函数内合法）
- **`break`/`continue`**：`loopControl{depth}` 逐层递减，支持 `break 2` 跳出双层循环
- **`set -e`**（`errexit.go`）三条例外全部用真 bash 核对：条件上下文不触发（`inCondition` 递增 `condDepth`）、`||` 链尾失败不触发、`&&` 链**短路未执行到末段**不触发。判定放在 `runAndOr` 内（只有它知道是否短路），否则 `set -e; false && echo x` 会被误判退出
- **仍未支持**（按规划 D7 保持报错）：`case`/`esac`、`select`、`function` 关键字、`coproc`、`time`、`[[ ]]`
- **实测**：`make vbash-self` **193/193** —— C / T0 / T1 / T2 / T3c / T3h / X 七层全部 **100%**（S3 时为 172/193）。`go build`/`vet`/`test`/`test -race`/`make cross` 全绿
- **文档**：`docs/vbash-impl.md` 新增 S7 节（产物、基准矩阵、实现要点、偏差 S7-1…S7-5）

## 2026-11-04 feat(vbash): S3–S6 执行层、展开、重定向与跨平台诊断

- **执行器**（`vbash/runner.go`）：列表 / `&&`、`||` 短路 / 管道（各段独立 `Env` 克隆，`-race` 干净）/ 命令 / 重定向套用；`RunScript` + `Config` 自带入口，不依赖 `tools/shell`
- **变量与展开**（`vbash/env.go`、`expand.go`、`arith.go`）：位置参数与 `$? $# $@ $* $$ $0 $1`；`$VAR` 贪婪取名；`"$@"` 展开成多词；IFS 字段切分（`"$X"` 不切、`$X` 切）；`${#V} ${V:-x} ${V:=x} ${V:?x} ${V:+x} ${V##p} ${V%%p} ${V/p/r} ${V//p/r}`；`$(( ))` 完整求值（优先级/赋值/三元/进制）；`~/` 展开；glob（无匹配保字面）
- **重定向**（`vbash/redirect.go`）：`> >> < 2> 2>> 2>&1 &> &>> >| <>`，**保序**（`2>&1 >f` ≠ `>f 2>&1`），heredoc 体按「终止符是否引号化」决定是否展开，`/dev/null` 走内部汇
- **内建**（`vbash/builtin.go`、`test.go`）：`:` `true` `false` `echo` `printf` `cd` `pwd` `export` `unset` `set` `shift` `exit` `return` `read` `wait` `test`/`[`；`printf` 自实现格式化与转义（含 `\0NNN`、`\xHH`）；`cd` 维护 `PWD`/`OLDPWD` 与 `cd -`
- **外部透传与退出码**（`vbash/exec.go`）：`$PATH` 查找、126/127/128+signum、`argv[0]` 原样传递、信号退出复刻 `<Signal> <command>` 提示
- **诊断文案对齐 bash**（`vbash/diag.go`）：基准逐字节比对 stderr，故 `cd:` / `command not found` / `Is a directory` / `syntax error near unexpected token` / `unexpected EOF while looking for matching` / heredoc 未闭合告警全部复刻；shell 名前缀由 `Config.ShellName` 注入（基准取 `/bin/bash`）
- **失败语义**：`( )`、`{ }`、函数定义能解析但**报「暂不支持执行」**（用户决策，符合 D9 不回落），故 `T1/017` 在 S7 前不通过
- **基准适配器** `vbash/testdata/cmd/vbashrun` + `make vbash-self` / `vbash-self-detail` / `vbash-run`
- **实测**：`make vbash-self` **172/193** —— T0=100%、T1=97.2%、T2=100%、T3h=100%、X=100%、T3c=24%、C=96.7%；未达标的三条全属控制流/子 shell（S7 范围）。`go build`/`vet`/`test`/`test -race`/`make cross` 全绿
- **文档**：`docs/vbash-impl.md` 新增 S3 节（产物、基准矩阵、实现要点、偏差 S3-1…S3-6）

## 2026-11-04 feat(vbash): S2 解析层——列表/管道/重定向/heredoc 挂载与展开片段

- **`vbash/ast.go`**（新）：AST —— `List`/`ListItem`（分隔符 + 是否换行）/`AndOr`/`PipeItem`/`Pipeline`（含 `!`）/`Command`（赋值 / 词 / 重定向 / 分组 / 函数定义）/`Assign`/`Redirect`（`Fd` 默认 -1）/`Word`（`Segments` + 拼接后的 `Text`）/`Segment`
- **`vbash/parser.go`**（新）：递归下降解析器，四层语法 `List → AndOr → Pipeline → Command`，外加子 shell `( )`、花括号分组 `{ ; }`、函数定义 `name() { …; }`
- **fd 重定向识别**：只有「纯数字 Word」与重定向操作符**无空白相邻**且未被引号包裹时才作 fd（`2>f` 是 fd、`2 >f` 与 `"2">f` 是参数），三种写法均用真 bash 核对；`Redirs` 保序，`2>&1 >f` ≠ `>f 2>&1`
- **heredoc 关联**：词法层改为把 `<<` 分隔符作为普通 Word 返回并携带 `*Heredoc`，解析层把同一指针挂到 `Redirect.Heredoc`；体在脚本解析结束时统一补齐，故「终止符后接命令」「管道中的 heredoc」都正确
- **展开片段结构化**（把 S5 的语义前移出词法器）：`Segment` 分字面/`$VAR`/`${…}`/`$((…))`/`$(…)` 五类，带「未引用/双引号内/字面」三种引号模式；`$(…)` 内层文本递归解析为 `Segment.Sub`。`$Xdef` 贪婪取名、单引号内不展开、双引号内 `$'…'` 是字面——均按 bash 行为
- **报错口径**：`if`/`for`/`while`/`until`/`case`/`function`/`coproc`/`time` 在首词位置报 `UnsupportedError`（带规划文档提示），闭合词 `fi`/`then`/`done`/`esac` 与 `;`/`|`/`&&` 打头报 `SyntaxError`；`[[` 提示改用 `[ ]`
- **修词法层死循环隐患**：`; ;&`、`|&`、`<<<`、`<(`/`>(`、`((` 原先落到 `matchOp` 返回 `(OpNone, 0)` 而调用方不消费字符 → 原地死循环；现新增前缀表 `unsupportedOps` 直接报 `UnsupportedError`；`;;` 单独成为 `OpDSemi`
- **回归**：192 条基准命令 **168 条解析成功** / 18 条按设计不支持 / 6 条按预期语法错误，**与 `bash -n` 的语法错判定集合完全一致**；新增随机片段回归（不 panic、不死循环、AST 无空节点）；`go build`/`go vet`/`go test`/`go test -race`/`make cross` 全绿，`make vbash-check` 仍 193/193（未触碰 `tools/shell`）
- **文档**：`docs/vbash-impl.md` 新增 S2 节（产物、语法层次、实现要点、偏差 S2-1…S2-6、验收）

## 2026-11-04 docs(vbash): 特性范围与实施规划 + 基准分层改造

- **`docs/vbash-plan.md`**（新）：把语料分析落成决策。**目标定为跨平台用法一致性**，不定位为性能优化（实测 `bash -c` 基线 1.7–3.9 ms/次，全量 52 秒可忽略）；**边界是一阶段只虚拟 bash 解释器、外部程序一律透传**（不复刻 coreutils 方言，且 Windows 侧 coreutils 可用）
- **特性分档**：P0（引号状态机、`; && || |`、`2>&1` 顺序、heredoc 栈式配对、`cd`、变量/`$(...)`/`~/`、退出码、`$'...'`）/ P1（`${...}` 参数展开、`$(( ))`、glob、`export`、`for`、`if`、`( )`/`{ ; }`、`exit`/`set -e`、`read`）/ P2（`while`、函数、`case`）/ 明确不做（`[[ ]]`、`eval`、`trap`、反引号、`<<<`、`((...))` 作语句、数组、作业控制、PowerShell/cmd 语法）
- **内建清单只收 bash builtins**（`cd pwd echo printf test [ true false : exit return export unset set shift read`），其余首词 `exec` 透传
- **跨平台归一**（本项目核心价值，新增目标）：路径分隔符与盘符归一为 POSIX 形式、`\` 一律按 bash 转义规则、`~` 用注入的 `Home` 不读环境、heredoc 体走 UTF-8 字节流、命令文本 CRLF→LF、`/dev/null` 映射 `os.DevNull`、128+signum 在 Windows 模拟
- **决策记录 D1–D10**，其中 **D9 否决了初稿的「回落开关」**：本特性是实验性质、不追求完备，虚拟层失败直接报错，静默回落会掩盖缺陷并丢失可观测性；**D10** 配置默认关闭、需显式开启
- **`vbash-bench` 分层改造**：`T3` 原把 heredoc(20) 与控制流(25) 合算一层、门槛 90%，与「控制流一阶段可不做」矛盾（只做 heredoc 上限 44.4%）；现按用例名前缀拆为 **`T3h`/`T3c`** 两层分别统计，golden 同步改名
- **`optional: true` 机制**：语料 0 次但基准收录的构造（`[[ ]]`、反引号、`case`，共 4 条）只校验「不崩、不挂起、退出码合理」，不校验输出——实现可以报错而非支持
- **修 `run.py` 门槛 bug**：存在失败用例时 `bad` 已含「存在失败用例」，导致 `if bad and '存在失败用例' not in bad` 跳过整个 GATE 分支、门槛违规信息不打印；现拆出 `gates` 独立输出
- **门槛表**：`T0=100, T1=100, T2=95, T3h=90, T3c=0（逐步提到 80）, C=100, X=100`，`make vbash-check` 同步
- **复测**：真 bash **193/193**、门槛全过；`go build`/`go vet`/`go test ./...` 全绿

## 2026-11-04 docs(vbash): 虚拟 bash 方向的语料分析与回归基准

- **`docs/vbash.md`**：基于 505 会话 / 35062 条真实 `run_shell` 调用（160065 子命令、715 个程序）的语法层分析。核心结论：T0+T1（单命令 + 管道/串联/重定向）占 **67.5%**，只需"正确切分 + 执行 + 传退出码"；30.0% 的复杂度来自 heredoc（26.3% 命令含 heredoc，其中 90% 引号化、体内可当字面字节）；控制流关键字合计 **<6%**，`[[ ]]`/`eval`/`trap`/反引号/`((...))`/`case` 在语料中为 **0**。刚性要求只有一条：**引号状态机**——23.9% 的命令在引号内含 `;`/`|`/`&`，盲切必错。另记录 `cd` 会话态（58.4% 命令以 `cd` 开头）、外部程序边界（构建/系统/网络三类共 19% 不可虚拟，应透传）、真实语法错误率 <0.1%
- **`testdata/vbash-bench/`**：193 条用例（T0 20 / T1 36 / T2 32 / T3-heredoc 20 / T3-controlflow 25 / C 语料回归 30 / X 边界 30），golden 由真 bash 5.3 录制，逐层比对 stdout/stderr/退出码；`run.py` 支持 `--record`/`--target`/`--filter`/`--min-rates` 门槛判定
- **基准自校验**：真 bash **193/193**；反面参照 `tools/reference-naive.py`（故意盲切分隔符、忽略引号与 heredoc）**88/193（45.6%）**——确保基准能区分好坏实现，而非"什么都能过"
- **`docs/vbash-bench.md`**：用例格式、沙箱约定、golden 确定性要求（含路径/时间戳须归一化，不许用 `skip_golden` 绕过）、分层门槛与维护流程
- **语料抽取脱敏**：`tools/gen_corpus_cases.py` 改为接收目录参数（不再内置本机路径），按命令指纹取高频形态，抽取阶段过滤家目录、内网地址与外部依赖命令，产出用例不含环境信息
- **`Makefile`**：新增 `vbash-check`（自检 + 门槛）/ `vbash-record` / `vbash-naive`；`AGENTS.md` 的 docs 索引与构建段同步
- **沙箱收紧**：`run.py` 的 `setup` 块原先只传 `cwd`、继承调用者 `HOME`，带 `~` 或绝对路径的 setup 会写到真实家目录；现改为 `HOME`/`TMPDIR` 指向沙箱内（全量 193 条复测仍 193/193），`FIELDS` 补入已在用却被解析器丢弃的 `check_files`
- **审查结论**：全量 193 条 + 反面参照跑前后，真家目录、仓库工作树、`/tmp` 均无变化，无沙箱残留；用例中无 `..`、无沙箱外绝对路径（仅 `/dev/null` 与不存在的 `/no/such/dir/xyz`）、无提权/联网命令
- **范围**：分析完成、基准可用；**虚拟 bash 本身未实施**，本文档不含实现承诺，也不改动 `tools/shell` 的 `Console`/`Lease` 契约

## 2026-10-23 feat(agent): 请求失败自动重试（`max_retries`，调用方给默认值）

- **配置**：`agent.Config` 新增 `MaxRetries`（`yaml:"max_retries"`，`0` = 关闭）；库不预设默认值、不做值域校验，负值按 0 处理，默认值与 `TANYA_MAX_RETRIES` 覆盖归调用方（tanya 侧默认 2、上限 20 回落）
- **位置**（`Client.ChatStream`）：重试循环包在协议分派之外，chat 与 responses 共用，每次尝试 = 一次完整请求（建连 + 状态码 + 流读取）；`do()` 与 `ListModels` 不参与（避免两层重试相乘，`/model` 保持「失败不重试」口径）
- **判据**（`retryableErr`）：重试传输层错误、`MsgReadStream`、`MsgStreamIdle`、HTTP `408`/`425`/`429`/`5xx`；不重试 `ctx` 取消/超时、其余 4xx、上游语义失败（新增 `semanticError` 类型标记 responses 的 `response.failed` / `completed` 带 error / `error` 事件三处，`Unwrap` 保持原错误文本可读）。**一旦已向内层 sink emit 过 `EventContent`/`EventReasoning`/`EventToolCall` 就不再重试**（`retrySink` 跟踪）——已上屏内容无法回滚，重放会造成重复输出
- **退避**（`retryDelay`）：`retryBaseDelay` 500ms 起指数翻倍、`retryMaxDelay` 8s 封顶；HTTP 响应带 `Retry-After`（秒数或 HTTP-date，`parseRetryAfter`）时取它、`retryAfterCap` 60s 封顶；等待走 `sleepCtx`（`select ctx.Done() / timer`），`^C` 立即中断并返回 `ctx.Err()`
- **事件**：新增 `EventKind` `EventRetry` 与 `RetryInfo{Attempt, Max, Delay, Err}`（`Err` 为完整上游错误原文，不裁剪）；`EventRequestStart` 不重复发，故渲染侧 TTFT 把重试等待一并计入
- **测试**（`agent/llm_retry_test.go`，+11）：503→200 成功后正文只有一份且退避指数翻倍、用尽重试返回最后一次错误（默认 2 次重试 = 3 次请求）、`max_retries=0` 直连、400 不重试、**已上屏 content 后空闲超时不重试**、`Retry-After` 优先且等待期取消返回 `context.Canceled`、responses 协议重试与语义失败不重试、`retryDelay` 序列与封顶、`retryableErr` 判定表；`retryBaseDelay` 在用例里调到 1ms（包级 var，`t.Cleanup` 还原）
- **验收**：`gofmt -l` 干净、`go build/vet ./...`、`go test ./...`、`go test -race ./agent/`、`make cross` 全绿

## 2026-10-23 release: v0.1.0 —— 首个发布版本，调用方撤除本地 replace

- 发布前全量复跑：`go build/vet ./...`、`go test ./...`、`go test -race ./...`、`make cross` 全绿；无 TODO/stub 残留；公共仓信息泄露扫描无命中
- 打 tag `v0.1.0` 并推送；tanya、ivan 两侧改 `require github.com/LaoQi/battlelab v0.1.0`、删除 `replace ../battlelab`，各自构建测试通过
- AGENTS.md「调用方」行同步：改为依赖已发布版本，本地 replace 降级为开发期联调手段（README 的联调示例口径不变）

## 2026-10-22 docs(readme): README 扩为完整版——架构 / 职责 / 数据管线

- 在原「边界 + 构建」骨架上补齐：一核两前台架构图、包职责表（agent/tools/ctty 各自边界与平台分片口径）、六条数据管线的精简图（回合主流、事件扇出、会话存储、交接、终端借出、图像）、作为库使用的最小示例（必填 Config 字段与注入点齐全，端点/路径用文档保留段）、docs/ 八篇索引
- 示例 API 面经与 `config.go`/`tools.Options` 核对

## 2026-10-22 fix(agent,tools/shell,docs): 首轮 review 修复

- **归档删除守卫**（`agent/session_archive.go`）：归档卷写好并读回校验通过前不删原会话文件——校验失败或卷内缺条目时逐条记入 `ArchiveReport.Failed`（`MsgArchiveVerifyFailFmt`/`MsgArchiveVerifyMissFmt`）、原文件与卷均保留现场，下次归档对已在卷内的条目按既有规则跳过；此前读回失败仍会照删原文，数据只剩一个打不开的卷。测试 `TestArchiveVerifyFailKeepsOriginals` 覆盖两场景（注入点 `verifyVolume`，与 `decodeStream` 同款包级 var 风格）
- **Lease 契约**（`tools/shell`）：删去 `runFull` 启动失败路径的冗余 `Release()`（defer 已覆盖，不再依赖实现幂等）；`Lease.Release` 的幂等要求写进接口注释
- **文档**：`docs/shell-tool.md` 归属注补 tanya 时代路径 → 本仓结构的映射（`agent/shelltool.go`→`tools/shell/`、`agent/builtin.go`→`tools/builtin/` 等），撤回失实的「文中代码路径即本仓路径」；`docs/session-archive.md` 归属注注明历史变更记录段按 tanya 语境理解
- **依赖**：`golang.org/x/sys` 升至 v0.48.0（与调用方 ivan 对齐，摆脱 2022-03 旧 pin），`go` 指令随依赖要求升至 1.26.0；tanya 侧 `go mod tidy` 同步

## 2026-10-22 init: 从 tanya 抽出 agent / tools / ctty

- **来源**：tanya 的 `agent/`、`tools/`、`ctty/` 整体迁入，代码与测试同步搬移（不含 `agent/init.go` 工作区脚手架、`tools/shell/notify.go` 通知实现、`tools/shell/view` 终端视图——三者属前台，留在 tanya）
- **去品牌**：`agent.Config` 新增 `LocalDir`（本地会话目录名，`yaml:"-"`，空则 local 模式校验失败）；全局说明（AGENTS.md）默认路径取消，改由 `WithGlobalAgentsPath` 注入；删除 `agent.UserAgent*` 常量；`MsgAPIKey`/`MsgShellOverrideFmt` 去掉 `TANYA_*` 环境变量名；`agent.SessionDirFor` 公开（供调用方在构造 Agent 前预览会话落点）
- **通知边界**：`Notification` 载荷与外部程序通知不进库，`tools/shell` 不再依赖 `render/*`
- **文档**：7 篇随代码搬迁（`agent-control-tool.md`、`ctty.md`、`shell-tool.md`、`agent-event-seams.md`、`session-archive.md`、`session-handoff.md`、`cache-probe.md`），每篇加归属说明、指向 tanya 仓的引用改 `tanya 的 …` 前缀；`docs/extraction.md` 记录抽取判据与边界
- **验收**：`go build/vet ./...`、`go test ./...`、`make cross` 全绿；tanya、ivan 两侧改 import 后各自全绿
