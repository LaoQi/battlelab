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
