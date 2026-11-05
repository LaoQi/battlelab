# 虚拟 bash：实施记录

> 配套 `docs/vbash-plan.md`（范围与决策）。本文只记**实施过程中的偏差与自定决策**，
> 按阶段（S1…S8）分节，每节记录：做了什么、与规划不一致处、为什么。

## S8 接入 tools/shell（2026-11-04）

### 产出

| 文件 | 内容 |
|---|---|
| `tools/shell/vbash.go` | `Tool.runVbash`：进程内跑 `vbash.RunScript`，复用 `streamCapture`/`waitBounded`/超时与中断语义 |
| `tools/shell/tool.go` | `Config.Vbash`（默认关闭）+ `Config.Environ`（外部命令的环境来源）；装配期校验 |
| `tools/shell/messages.go` | vbash 描述句与两条装配错误文案 |
| `tools/shell/shell.go` | `streamCapture.seal()`：`finish()` 幂等（放弃等待后迟到写入不再改动结果） |
| `tools/shell/vbash_test.go` | 18 条用例（内建/展开/外部透传/环境注入/cwd/不支持不回落/语法错误/超时/纯内建死循环/中断/截断/interactive 旁路/装配校验/描述） |
| `vbash/runner.go` | `Config.ConfigureCmd`/`CancelCmd` 注入点（进程组与树杀策略留在调用方）、`branch()` 统一派生执行器、`runList` 每项前检查 `Ctx` |
| `vbash/exec.go` | 外部命令套用 `ConfigureCmd`/`CancelCmd`（含 `WaitDelay`） |
| `vbash/errors.go` | 位置为零时省略「第 0 行第 0 列」 |

### 实现要点

- **默认关闭，显式开启**：`Config.Vbash` 为 `false` 时执行路径与旧版完全一致（`bash -c`），
  开关为实验特性背书（规划 §9 风险对策）。
- **不回落**（D9）：`vbash` 报 `UnsupportedError`/`SyntaxError` 时直接进 `Result.Err` 或 stderr，
  绝不改走宿主 shell；`TestVbashUnsupportedDoesNotFallBack` 断言 `case` 语句既不执行也无输出。
- **错误分流**：`SyntaxError` 已由解释器按 bash 文案写进 stderr（基准逐字节要求），不再重复写 `Err`；
  `ExitRequest`/`ReturnRequest` 是控制流信号不算错误；只有 `UnsupportedError` 与内部错误进 `Err` 行。
- **超时与中断**：`waitBounded` 包住 `RunScript`（放弃等待即 `MsgAbandoned`），解释器内部靠
  `runList` 的 `Ctx` 检查让纯内建死循环（`while true; do :; done`）也能在截止时间退出，
  外部命令则由 `exec.CommandContext` + `CancelCmd` 组杀。
- **进程组与树杀留在调用方**：`vbash` 只提供 `ConfigureCmd`/`CancelCmd` 两个钩子，
  具体策略由 `tools/shell` 传 `platform.ConfigureGroup`/`platform.KillGroup`
  ——库不预设平台策略（分层约束），Windows 的 `taskkill /T /F` 因此自动生效。
- **`Environ` 必须注入**：`vbash` 是纯库，不读 `os.Environ`；装配期缺 `Environ` 直接报错，
  避免外部命令拿到空环境这种静默故障。
- **interactive 旁路**：`interactive: true` 仍走 `runFull`（终端所有权属 `ctty`），不经解释器。

### 与规划的偏差与自定决策

| # | 情况 | 处理 | 理由 |
|---|---|---|---|
| S8-1 | 规划 §6 把 `ConfigureGroup`/`KillGroup` 视为现成能力直接沿用 | `vbash.Config` 新增 `ConfigureCmd`/`CancelCmd` 钩子，由 `tools/shell` 注入 | `vbash` 是纯库不能 import `tools/shell`；把策略做成钩子既守住分层，也让 Windows 树杀自动生效 |
| S8-2 | 规划未提解释器内部的取消检查 | `runList` 每项前检查 `Ctx`，并在展开/管道派生处补 `branch()` 传播钩子 | 纯内建死循环不开子进程，没有取消点就只能靠外层放弃等待（会白等满宽限期） |
| S8-3 | 规划要求「配置开关（默认关闭）」但未定开关失败语义 | 非 POSIX shell（pwsh/cmd）下开启 `Vbash` 在 `New` 期报错 | 解释器只实现 bash 语法，在 pwsh 上开启必然是错的；装配期拒绝比运行期每条命令报错清楚 |
| S8-4 | `vbash` 原先的 `UnsupportedError`/`SyntaxError` 文案总会带「第 0 行第 0 列」 | 位置为零时省略位置段 | 解析期错误多半没有精确位置；`Err` 行会直接进模型视野，不能带着假位置 |
| S8-5 | `streamCapture.finish()` 原先非幂等 | 加 `sealed` 标记，写入与收尾在放弃等待后失效 | 超时放弃后迟到的写入不该改动已交付结果（`-race` 下也是数据竞争源） |

### 验收

- `go build ./... && go vet ./... && go test ./... && go test -race ./...` 全绿；`make cross` OK
- `make vbash-self` 仍 **193/193**（接入未改动解释器语义）
- 新增 `tools/shell/vbash_test.go` 18 条用例全过（含 1 秒超时的三条慢用例）

## S7 控制流（2026-11-04）

### 产出

| 文件 | 内容 |
|---|---|
| `vbash/parser_control.go` | `for`/`while`/`until`/`if`-`elif`-`else` 的递归下降解析，`expect()` 终止字校验 |
| `vbash/runner_control.go` | 复合命令执行、`break`/`continue` 的 `loopControl` 信号与深度传播 |
| `vbash/runner_group.go` | 子 shell（`Env` 克隆隔离）、花括号分组（共享 `Env`）、函数表与调用栈 |
| `vbash/errexit.go` | `set -e` 触发规则与条件上下文例外 |
| `vbash/ast.go` | 新增 `Compound{Kind,Name,Words,Cond,Body,Elifs,Else}`、`CondClause` |
| `vbash/builtin.go` | 新增内建 `break` `continue` `local` |
| `vbash/env.go` | `inFunc` 标记、`locals` 记录 |
| `vbash/parser.go` | 解析器加 `stop` 词栈（`do`/`then`/`done`/`fi`/`elif`/`else`） |

### 基准结果（`make vbash-self`）

| 层 | 结果 |
|---|---|
| C / T0 / T1 / T2 / T3c / T3h / X | **全部 100%** |
| 合计 | **193/193** |

### 实现要点

- **终止字栈**：`for f in a b; do ... done` 里条件与循环体都调 `parseList`，靠 `p.stop` 栈
  决定在哪停下（`pushStop("do")` → 解析条件 → `popStop`）；`if` 在 `then`/`elif`/`else`/`fi`
  四个词上停。没有这个栈，`parseList` 会把 `done` 当成普通命令。
- **子 shell 与分组的区别**：`( )` 用 `r.Env.Clone()`（`cd`、变量都不回传）；`{ ; }` 直接共享
  `r.Env`。`T3c/119` 靠这条区分（`x=2` 回传、`( x=3 )` 不回传）。
- **函数调用**：切到克隆的 `Env`（`inFunc=true`、位置参数换成 `argv[1:]`、`arg0` 换成函数名），
  `return` 用 `ReturnRequest` 信号穿透，`local` 靠 `inFunc` 判定合法位置。
  函数体若是 `{ }` 分组则 `runList` 执行。
- **`set -e` 的三条例外**（全部用真 bash 核对）：
  1. 条件上下文（`if`/`while`/`until` 的条件）不触发 —— `inCondition()` 递增 `condDepth`
  2. `||` 链结尾失败不触发
  3. `&&` 链**短路未执行到末段**时不触发（`false && echo x` 后面照常执行）
  实现上由 `runAndOr` 自己判定「链尾段是否真的执行且失败」，而不是让 `runList` 事后检查
  ——后者拿不到短路信息，会把 `false && echo x` 误判成需要退出。
- **`break`/`continue` 带深度**：`loopControl{depth}` 逐层递减，`break 2` 能跳出双层循环。

### 与规划的偏差与自定决策

| # | 情况 | 处理 | 理由 |
|---|---|---|---|
| S7-1 | 规划 §3.2 列 `for`/`if` 为 P1，`while`/函数/`case` 为 P2 | `for`/`if`/`while`/`until`/函数/分组全部实现，`case`/`select`/`function`/`coproc`/`time` 仍报 `UnsupportedError` | 基准里 `while` 与函数有专门用例，做完能让 `T3c` 到 100%，成本很低 |
| S7-2 | 规划未提 `break`/`continue` | 作为内建实现（含 `break N` 深度） | 基准 `T3c/105`、`106` 需要；不实现这两条无法通过 |
| S7-3 | 规划把 `set -e` 放 P1 但未定义例外 | 实现三条例外并在 `TestRunSetMinusEChains` 覆盖 | 不做例外会让 `set -e; false && echo x` 行为与 bash 不符 |
| S7-4 | 规划 §3.4 说 `wait` 报错 | 维持 S3 的无害内建 | 已在 S3 决策，S7 无变化 |
| S7-5 | `local` 不在规划的内建清单里 | 加入（仅函数内合法） | `T3c/116` 需要；它是 bash builtin，符合 D5 |

### 验收

- `go build ./... && go vet ./... && go test ./... && go test -race ./...` 全绿；`make cross` OK
- `make vbash-self`：**193/193**，七层全部 100%
- 新增执行层用例：`for`/`while`/`if`-`elif`-`else`/`break`(含深度)/`continue`/子 shell/分组/
  函数(返回码、`local`、`$1`)/`set -e`(五种链式组合 + 条件上下文)/嵌套控制流/`while read`

## S6 跨平台归一（2026-11-05）

### 产出

| 文件 | 内容 |
|---|---|
| `vbash/platform.go`（新） | `platformOps` 差异表 + `fillPlatform` 缺省值；`absInternal`/`posixPath`/`hostPath`/`hostDir`/`isPathLike`/`isDevNull`/`resolveSymlinks` 六个归一原语 |
| `vbash/platform_posix.go`（新） | `posixPlatform()`：`/dev/null`、POSIX 默认 `PATH`、mode&0111、`WaitStatus` 信号退出码与信号名 |
| `vbash/platform_linux.go` / `_darwin.go`（新） | 白名单装配（`var plat = fillPlatform(posixPlatform())`） |
| `vbash/platform_windows.go`（新） | 盘符与 `\\server\share` 归一、`;` 分隔的 `PATH`、`PATHEXT` 式后缀查找、大小写不敏感匹配、退出码低 8 位与 `0xC000013A`→130 |
| `vbash/platform_stub.go`（新） | 其余平台落空表（`make cross` 拦漏标） |
| `vbash/signal_unix.go` / `signal_other.go`（删） | 被 `platformOps` 取代，信号面只留一份 |
| `vbash/env.go` | `hostVar`/`internalVar`：`HOME`/`PWD`/`OLDPWD` 存 POSIX 形式，`PATH` 内部一律 `:` 分隔，`Environ` 出口转回宿主形式 |
| `tools/shell/tool.go` | 去掉「启用 vbash 要求宿主 shell 是 POSIX」的装配期拒绝；非 POSIX 宿主上的描述改走 `describeVbash` |
| `tools/shell/vbash.go` | `Arg0`/`ShellName` 在非 POSIX 宿主上取 `bash`（诊断前缀，不再出现 `pwsh.exe`） |

### 实现要点

- **内部只用一个表示：POSIX `/`**。语法层、展开层、`glob`、`test`、重定向目标全程按 `/` 处理，
  只在两类边界转成宿主形式：文件系统调用（`os.Stat`/`Open`/`ReadDir`/`Readlink`）与 `exec`（`cmd.Dir`、`argv[0]`）。
- **盘符按 Git-Bash 惯例**：`C:\x` ↔ `/c/x`；UNC `\\server\share` 保留不动。
  `PATH` 里的条目同样归一，因此 `$PATH` 在三平台都是 `:` 分隔。
- **`/dev/null` 走映射而非字符串比较**：`isDevNull` 按表匹配（Windows 额外认 `nul`、不区分大小写）。
  重定向命中时写端走内部 `discard` 汇、读端走空 `Reader`，不产生文件句柄；若目标经展开后落到
  `/dev/null`（如 `test -e "$X"`），`hostPath` 再兜一层映射到 `os.DevNull`，避免拼出宿主上不存在的路径。
- **`-x`/可执行判定按平台**：POSIX 看 mode，Windows 看后缀是否在 `PATHEXT` 清单内；
  `lookPath` 在候选名已带后缀时不再追加，避免 `git.exe.exe`。
- **退出码统一在表里**：Windows 的 `ExitCode` 取低 8 位（与 bash 的 `$?` 截断一致），
  Ctrl-C 的 `0xC000013A` 记 130；POSIX 保留 `128+signum` 并继续输出 `<Signal> <command>` 诊断行。
- **`RunScript` 入口归一注入值**：`Config.Dir`/`Config.Home`/`Config.Env` 在 `NewRunner` 里就转成内部形式，
  调用方（`tools/shell`）继续传宿主原生路径即可，不需要知道内部表示。
- **装配期不再拒绝非 POSIX 宿主**：这正是本特性的目标场景（Windows 只有 pwsh/cmd）；
  描述文案改为提示 bash 写法（`/` 分隔、`/c/` 盘符、`\` 只作转义）。

### 与规划的偏差与自定决策

| # | 情况 | 处理 | 理由 |
|---|---|---|---|
| S6-1 | 规划 §6 要求 `vbash/platform_{linux,darwin,windows}.go` 三分片 | 增补 `platform_posix.go`（linux\|darwin 共享）与 `platform_stub.go`，共五文件 | 与库内 `tools/shell/platform*.go` 的既有约定一致（posix 共享实现 + 三装配 + stub） |
| S6-2 | 规划未定义宿主路径到内部表示的具体形式 | 采用 Git-Bash 的 `/c/x` 而非 `C:/x` | 与语料里实际出现的写法一致（模型在 Windows 上本来就照 Git-Bash 写）；`C:/x` 仍可作输入 |
| S6-3 | 规划 §5.2 要求「参数中遇 NUL 报错」 | 未实现，`$'a\0b'` 仍产出字面量 `a\0b` | 现实现的 `$'…'` 只认单字节转义，`\0` 不在其表内；`os/exec` 遇 NUL 由内核拒绝（`invalid argument`），已在 `spawn` 归为普通失败。留作后续 |
| S6-4 | 规划未提「非 POSIX 宿主能否启用 vbash」 | 放开 `New` 的装配期拒绝，改由描述文案指引写法 | 拒绝等于特性在 Windows 上不可用，与 D1 直接矛盾 |
| S6-5 | 规划未提大小写不敏感的文件名匹配 | Windows 上 `glob` 折小写比较 | 「保持宿主行为」（§5.1 末行）的落地方式 |
| S6-6 | 路径型命令不存在时的退出码原为 1 | 改为 127（不存在）/126（不可执行），并补 diag | bash 如此；原行为属既有缺陷，归一过程顺带修正 |
| S6-7 | 规划未提「`a/b` 是盘符还是相对目录」 | `a/b` 仅当 `A:\` 真实存在才当盘符 | 无条件当盘符会把 Windows 上的相对目录 `a/b` 解析到错误位置，且失败后难排查 |

### 验收

- `go build ./... && go vet ./... && go test ./... && go test -race ./...` 全绿
- `make cross`（darwin/windows 的 vet + 逐包测试编译）OK
- `make vbash-check` **198/198**（Linux 侧语义未变，跨平台归一对 POSIX 是恒等变换）
- **未验证项**：Windows/macOS 真机基准未跑（当前环境只有 Linux），故文档一律标「实现完成、待验证」

## S3 执行层（2026-11-04）

### 产出

| 文件 | 内容 |
|---|---|
| `vbash/env.go` | `Env` 变量表（值 + 导出标志）、位置参数、`$?`/`$#`/`$@`/`$$`/`PWD` 等特殊参数、`Clone` |
| `vbash/io.go` | `IO` 三流 + 额外 fd 表、克隆、`/dev/null` 汇 |
| `vbash/expand.go` | 展开：字面/变量/`${...}`/`$((...)`/`$(...)`、IFS 字段切分、`~` 展开、glob、`${...}` 全套算子 |
| `vbash/arith.go` | `$(( ))` 递归下降求值（优先级、赋值、三元、比较、位运算、进制） |
| `vbash/glob.go` / `match.go` | glob 展开与自有的模式匹配器（`*` 可跨 `/`，与 bash 的 `##`/`%%` 语义一致） |
| `vbash/redirect.go` | 重定向落地：fd 复制、`&>`、`>|`、`<>`、heredoc 体（含引号化判断）、`/dev/null` |
| `vbash/exec.go` | 外部程序透传：`$PATH` 查找、126/127/128+signum、`argv[0]` 原样、信号诊断 |
| `vbash/builtin.go` | 内建：`:` `true` `false` `echo` `printf` `cd` `pwd` `export` `unset` `set` `shift` `exit` `return` `read` `wait` |
| `vbash/test.go` | `test`/`[` 求值（`-e -f -d -s -r -w -x -L`、`=`/`!=`/`-eq` 系列、`-z`/`-n`） |
| `vbash/runner.go` | 执行器：列表/and-or/管道（各段独立 Env 副本，避免竞争）/命令/重定向套用 |
| `vbash/diag.go` | 诊断文案：`<shell>: line N: ...`、`syntax error near unexpected token`、EOF 与 heredoc 告警 |
| `vbash/runner_test.go` | 执行层单测 30 组 |
| `vbash/testdata/cmd/vbashrun` | 基准适配器：把 vbash 包装成 `bash -c` 兼容可执行文件 |
| `Makefile` | `vbash-self` / `vbash-self-detail` / `vbash-run` |

### 基准结果（`make vbash-self`）

| 层 | 通过率 | 门槛 | 结论 |
|---|---|---|---|
| T0 | 20/20 = 100% | 100% | ✅ |
| T1 | 35/36 = 97.2% | 100% | 差 1 条 = 子 shell（S7） |
| T2 | 32/32 = 100% | 95% | ✅ |
| T3h | 20/20 = 100% | 90% | ✅ |
| X | 30/30 = 100% | 100% | ✅ |
| T3c | 6/25 = 24% | 0（S7 前） | 符合预期 |
| C | 29/30 = 96.7% | 100% | 差 1 条 = `for` 打表（S7） |

合计 **172/193**。

### 实现要点

- **失败语义按用户决策**：`( )`、`{ }`、函数定义已能解析但**报「暂不支持执行」**（`诊断 + 退出码 1`），
  不静默执行错误语义。因此 `T1/017`（子 shell 隔离 `cd`）在 S7 之前不会通过。
- **诊断文案对齐 bash**：基准逐字节比对 stderr，故文案必须精确。已核对并复刻：
  `cd: X: No such file or directory` / `Not a directory`、`X: command not found`、
  `X: Is a directory` / `Permission denied`、`syntax error near unexpected token \`;'` +
  `` `;' ``、`unexpected EOF while looking for matching \`''`、
  `warning: here-document at line N delimited by end-of-file (wanted \`EOF')`、
  `<signal> <command>`（信号退出）。**shell 名前缀**由 `Config.ShellName` 注入，
  基准里取 `/bin/bash`（bash 用 `argv[0]` 作前缀）。
- **`cd` 的显示名**：诊断用用户**输入的**路径，不用拼接后的绝对路径（`cd nope` 而非 `cd /abs/nope`）。
- **管道并发安全**：每个管道段跑在自己的 `Env` 克隆里，否则 `-race` 会报数据竞争。
- **赋值右侧的展开**：不走字段切分（`X="a  b"` 保留空格），且 `$(...)` 的退出状态传递给 `$?`。
- **`printf`**：自实现格式化（`%s %d %i %u %o %x %X %c %b %q`、宽度/精度/`-`/`0`），
  格式串里的 `\n \t \r \a \b \f \v \e \\ \0NNN \xHH` 全部解释。
- **模式匹配**：`path.Match` 的 `*` 不跨 `/`，而 bash 的 `${F##*/}` 必须跨，故自写 `match.go`
  （`*` `?` `[abc]` `[a-z]` `[!...]` `\` 转义）。

### 与规划的偏差与自定决策

| # | 情况 | 处理 | 理由 |
|---|---|---|---|
| S3-1 | 规划 §6 把执行层放在 `tools/shell` 内驱动 | `vbash` 自带 `Runner`/`Config`/`RunScript`，`tools/shell` 只在 S8 接线 | 库分层要求 `vbash` 不依赖 `tools/shell`；自带执行器也让基准可直接跑（`vbashrun`） |
| S3-2 | 规划未定义子 shell/分组/函数在 S7 前的行为 | 报「暂不支持执行」，退出码 1 | 用户明确决策；符合 D9「不回落、不静默」 |
| S3-3 | `wait` 在规划 §3.4 写「报错」 | 实现为无害内建（返回 0） | 语料里 `& wait` 是常见写法（`T1/031`），报错会让整条命令失败且无收益 |
| S3-4 | `${...}` 里 `#` 既可能是长度也可能是前缀删除 | `parseBraced` 先判 `${#name}` 纯名字形式为长度，其余按 `#`/`##` 删前缀 | 两者语法重叠，靠「`#` 之后是否仍是一个纯名字」区分 |
| S3-5 | 重定向打开失败如何终止 | 打诊断 + 该命令退出码 1，**不中断脚本** | 与 bash 一致（`echo x > nodir/f; echo rc=$?` 后续仍执行） |
| S3-6 | 信号退出诊断 | 输出 `<Signal> <command>`（`%-26s`） | 复刻 bash 的作业控制提示行 |

### 验收

- `go build ./... && go vet ./... && go test ./... && go test -race ./...` 全绿；`make cross` OK
- `make vbash-self`：**172/193**（T0=100、T2=100、T3h=100、X=100、T1=97.2、C=96.7、T3c=24）
- 未达 100% 的三条全部是控制流/子 shell，属 S7 范围

## S2 解析层（2026-11-04）

### 产出

| 文件 | 内容 |
|---|---|
| `vbash/ast.go` | AST 类型与裸打印：`List`/`ListItem`/`AndOr`/`PipeItem`/`Pipeline`/`Command`/`Assign`/`Redirect`/`Word`/`Segment` |
| `vbash/parser.go` | 递归下降解析器：列表/and-or/管道/简单命令/分组/函数定义/重定向/heredoc 挂载 |
| `vbash/parser_test.go` | 解析单元用例（赋值、重定向与 fd、heredoc、分组、函数、展开片段、语法错误） |
| `vbash/fuzz_test.go` | 随机片段拼接回归：不 panic、不死循环、AST 结构完整 |
| `vbash/corpus_test.go` | 新增 `TestCorpusParses`（全量可解析或按预期报错）与 `TestCorpusTreeInvariants`（AST 无空节点） |

### 语法层次（自顶向下）

```
List        := AndOr ((';' | '&' | NEWLINE) AndOr)*      ListItem{AndOr, Sep, Newline}
AndOr       := Pipeline (('&&' | '||') NEWLINE* Pipeline)*
Pipeline    := ['!'] Command ('|' NEWLINE* Command)*
Command     := Prefix* (Group | FuncDef | Word+)? Redirect*
Prefix      := Assign | Redirect
Group       := '(' List ')' | '{' List '}'
FuncDef     := NAME '(' ')' Group
```

`Command` 同时记录 `GroupKind`（`GroupSubshell` / `GroupBrace`）与 `Func`，重定向一律挂在
`Command.Redirs` 上（`Redirect.Fd` 默认 `DefaultFd = -1`）。

### 实现要点

- **fd 识别规则**：只有当「纯数字 Word」与「重定向操作符」**无空白相邻**（`tok.End == opTok.Pos`）、
  且该 Word 未被引号包裹时，才把数字当作 fd。因此 `2>f` 的 `2` 是 fd，`2 >f`、`"2">f` 的 `2` 是参数——
  三种写法已用真 bash 逐条核对（`/tmp` 下的落地文件内容）。
- **重定向顺序保留**：`Redirs` 按出现顺序追加，`2>&1 >f` 与 `>f 2>&1` 得到不同序列。
- **heredoc 关联**：词法层把 `<<` 的分隔符作为普通 Word token 交出去（`Token.Heredocs` 携带
  `*Heredoc` 指针），解析层在构造 `Redirect` 时把同一指针挂到 `Redirect.Heredoc`。
  词法器在整段脚本解析结束时 `finishHeredocs()` 补齐尚未读取的体，因此「命令在终止符之后」的
  情况自然成立，AST 里 `Redirect.Heredoc.Body` 一定是填好的。
- **赋值只在命令前缀位置识别**（真 bash 亦然）：`X="a b"` 拆成 `Name=X` + 值 `Word`（保留片段模式，
  双引号值不会被当成多词）；`echo a=1` 里的 `a=1` 是普通参数。
- **保留字**：`if`/`for`/`while`/`until`/`case`/`function`/`coproc`/`time` 在命令首词位置直接
  `UnsupportedError`（带 `docs/vbash-plan.md §3.2` 提示）；`fi`/`then`/`done`/`esac` 等**闭合词**
  则报 `SyntaxError`（与 bash 的「未预期记号」一致）。
- **展开片段结构化**：`Word.Segments []Segment` 记录 `字面`/`变量`/`${...}`/`$((...))`/`$(...)` 五类
  片段，并带引号模式（未引用/双引号内/字面），`$(...)` 的内层文本在解析结束时递归解析为 `Segment.Sub`。
  S5 只需实现语义（展开值、词分割、glob），不必再动词法与解析层。
- **`[[` 提前报错**：`[[` 在词法层是 `[` 开头的普通词，解析层在命令参数位置检查前缀即报
  `UnsupportedError`（提示改用 `[ ]`）。

### 与规划的偏差与自定决策

| # | 情况 | 处理 | 理由 |
|---|---|---|---|
| S2-1 | 规划建议 AST 用 `Redirect{Target Word}`，未提展开片段 | `Word` 改为 `Segments` 承载展开结构，`Text` 保留拼接后的字面结果 | 把 S5 的展开语义前移到解析层之外，避免 S5 二次修改词法器；`Text` 让不需要展开的消费者（诊断、裸打印）直接用 |
| S2-2 | 词法层原先在 `<<` 位置就地登记 heredoc，把分隔符吞掉 | 改为「登记 + 把分隔符作为普通 Word 返回」，体的读取延后到脚本解析完成 | 否则解析器看不到分隔符 Word，且「终止符后接命令」需要体的读取时机可推迟 |
| S2-3 | 空的 `{}`、`( )` 在 bash 里都是语法错误 | `parseList` 在括号/花括号内允许空列表返回，由调用方检查；`{}` 空体报 `SyntaxError` | 与 bash 一致（`bash -n -c '{ }'` 报错） |
| S2-4 | `( )` 与 `{ }` 的分隔符在词法层分别是 `OpLParen`/`OpRParen` 与普通词 `{`/`}` | 解析层两套分别处理 | bash 中 `{` `}` 是保留字（需空格分隔），不是操作符；`(` `)` 才是操作符 |
| S2-5 | `;;` 词法层已拆为 `OpDSemi` | 解析层在 `case` 之外遇到 `OpDSemi` 报 `SyntaxError` | `case` 属 P2（明确不做一阶段），语法位置错误应按 bash 口径报错 |
| S2-6 | 反引号、`<<<`、`|&`、`;;&`、`;&`、`<(`/`>(`、`((` 原会落到 `matchOp` 返回 `OpNone,0` 的**原地死循环** | 词法层新增 `unsupportedOp` 前缀表，命中即 `UnsupportedError` | 这是 S1 遗留的死循环风险（`matchOp` 返回 0 表示"不消费字符"，调用方必须继续走别的分支）|

### 验收

- `go build ./... && go vet ./... && go test ./... && go test -race ./...` 全绿；`make cross` OK
- 解析层单测 33 个用例（含随机片段回归）、语料回归：192 条基准命令中 **168 条成功解析**、
  18 条按设计不支持（控制流、`[[ ]]`、反引号）、6 条按预期语法错误（未闭合引号、
  `;`、`|`、`&&` 开头、`fi`）
- **与 `bash -n` 交叉核对**：192 条里「bash 判语法错」与「vbash 报 SyntaxError」的集合完全一致
  （另有 18 条 vbash 主动报不支持，bash 侧是合法语法）
- `make vbash-check` 仍 193/193（本轮未触碰基准与 `tools/shell`）

## S1 词法层（2026-11-04）

### 产出

| 文件 | 内容 |
|---|---|
| `vbash/errors.go` | `UnsupportedError`（构造名 + 提示 + 位置）、`SyntaxError`（消息 + 位置） |
| `vbash/token.go` | `Pos`、`Token`、`OpKind`、`Heredoc` 类型 |
| `vbash/lexer.go` | 词法扫描器：引号状态机、操作符、注释、heredoc 配对 |
| `vbash/lexer_test.go` | 单元用例（引号/转义/续行/注释/操作符/heredoc/CRLF/畸形输入） |
| `vbash/corpus_test.go` | 全量语料回归：192 条基准命令逐条过词法，不得死循环/意外报错 |
| `vbash/testdata/bench_commands.json` | 从基准 `.case` 导出的命令清单（测试输入） |

### 实现要点

- **引号状态机**：单引号内完全字面；双引号内只有 `$` `` ` `` `"` `\` 四者转义；
  `$'...'` 按 ANSI-C 解释 `\n \t \r \a \b \f \v \\ \' \" \e`；词内 `\` 转义下一个字符。
- **词间续行与词内续行**：`abc \<newline> def`（换行后有空格）是两个词；
  `abc\<newline>def`（紧接）拼接成 `abcdef`。两者行为已用真 bash 核对。
- **heredoc**：`<<` / `<<-`；终止符可单/双引号或反斜杠引用（引用即禁用体内展开）；
  多个 heredoc 按出现顺序排队，栈式逐个读取；终止符后同一行的剩余命令继续正常扫描。
  未闭合时按 EOF 截断（与 bash 一致，不报错）。
- **CRLF**：输入先归一 `\r\n` → `\n`（规划 §5.2 要求）。

### 与规划的偏差与自定决策

| # | 情况 | 处理 | 理由 |
|---|---|---|---|
| S1-1 | `$((...))` 与 `$(...)` 在词法阶段就整体识别（跳过内部括号配对） | 词法层给出单个 `Word` token，内部结构留给 S5 展开阶段再解析 | 若词法阶段就切开，`$(printf 'a\nb')` 里的引号会污染外层状态机 |
| S1-2 | `(` `)` 原本在 `matchOp` 里返回 `OpNone, 0`，导致**原地死循环** | 新增 `OpLParen` / `OpRParen` 两种操作符 token | 死循环是实施中发现的真 bug；`( )` 是子 shell/函数定义必需 token |
| S1-3 | 反引号在词法层直接 `UnsupportedError` | 保持（规划 §3.4 明确不做） | 早报错比执行到一半失败更清晰 |
| S1-4 | 未闭合引号报 `SyntaxError`，未闭合 heredoc **不报错** | 两者区别对待 | 与真 bash 一致：引号未闭合退出码 2；heredoc 未闭合只警告并按 EOF 截断 |
| S1-5 | `;;` 暂按两个 `OpSemi` 处理 | 未单独建 `OpDSemi` | `case` 属 P2，到 S7 再补 |
| S1-6 | `2>&1` 词法形态是 `Word("2") OpGreatAmp Word("1")` | 不在词法层合并 | fd 重定向的识别属解析层（S2）职责 |

### 验收

- `go test ./vbash/` 全绿；192 条基准命令词法全部通过（1 条按设计不支持=反引号，2 条按预期报语法错误=未闭合引号）
- `go build ./... && go vet ./...` 全绿；`make vbash-check` 仍 193/193（本轮未触碰基准与 `tools/shell`）
