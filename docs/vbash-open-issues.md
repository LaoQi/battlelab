# vbash 遗留问题清单

> 归属（2026-11-05）：本文记录**接入调用方之前必须决策/修复**的问题。
> 不替代既有文档：能力口径见 `docs/vbash-capability.md`，范围决策见 `docs/vbash-plan.md`，
> 实施记录见 `docs/vbash-impl.md`。本文只收「已知但未闭合」的条目，修完一项删一项。
>
> 状态（2026-11-09）：**P0–P2 全部闭合**（§7 的 EPIPE 既有缺陷同轮修完）。上一轮由调用方（tanya 真实试用）实测暴露的 13 项缺陷
> 已全部修完，见 §6；遗留项只剩 §4 的 P3 接线卫生与本文末尾的已知取舍。

## 摘要

| # | 问题 | 级别 | 状态 |
|---|---|---|---|
| 1 | 大括号展开静默字面化 | ~~P1~~ | **已实现**（`vbash/brace.go`） |
| 2 | `printf` 精度/标志位不生效 | ~~P1~~ | **已实现**（`vbash/printf.go`） |
| 3 | 后台 `&` / `wait` 与文档自相矛盾 | ~~P2~~ | **已实现真异步**（见 §3，文档同步校正） |
| 4 | `$(<file)` 静默返回空 | ~~P1~~ | **已实现** |
| 5 | 大括号展开/函数递归无深度上限、大括号无字段数上限 | ~~P1~~ | **已实现**（见 §3.1） |
| 6 | 管道段/命令替换内看不到已定义的函数 | ~~P2~~ | **已实现**（见 §3.2） |
| 7 | 复合命令后接带 fd 前缀的重定向解析失败 | ~~P1~~ | **已实现**（见 §3.3） |
| 8 | 接入侧遗留（死符号、Windows 未真机验证） | P3 | 见 §4 |
| 21 | 内建写已关闭管道打 `write error: broken pipe` | ~~P2~~ | **已实现**（见 §7） |
| 9 | heredoc 体内 `$( )`/`$(( ))` 坏、反斜杠转义与续行错 | ~~P0~~ | **已实现**（见 §6.1） |
| 10 | `test`/`[` 不认负数、把 `010` 当八进制 | ~~P0~~ | **已实现**（见 §6.2） |
| 11 | `exit -1` 返回 0、`exit abc` 静默 | ~~P0~~ | **已实现**（见 §6.3） |
| 12 | 循环外 `break`/`continue` 吞掉后续命令 | ~~P0~~ | **已实现**（见 §6.4） |
| 13 | `for x; do`（省略 `in`）空迭代 | ~~P0~~ | **已实现**（见 §6.5） |
| 14 | `echo -e` 的 `\c` 与 `\0NNN` | ~~P0~~ | **已实现**（见 §6.6） |
| 15 | `$*` 双引号内不按 IFS 首字符 | ~~P1~~ | **已实现**（见 §6.7） |
| 16 | 算术内 `${…}`/`$( )`/`$(( ))` 展开失败 | ~~P0~~ | **已实现**（见 §6.8） |
| 17 | `set -o pipefail` / `set -u` 被忽略 | ~~P1~~ | **已实现**（见 §6.9） |
| 18 | `$$` 恒 0（`kill $$` 会误杀宿主进程组） | ~~P0~~ | **已实现**（见 §6.10） |
| 19 | `${V:off:len}`、`${V^^}`、`${!V}`、`base#`、`n++`、`**` | ~~P2~~ | **已实现**（见 §6.11） |
| 20 | `printf %q` 用单引号包裹而非逐字符转义 | ~~P2~~ | **已实现**（见 §6.12） |

## 1 已闭合：大括号展开（P1 → 已实现）

原问题是 `{a,b}`/`{1..3}` 不展开且静默按字面输出，`mkdir -p d/{x,y}` 会真的创建名为 `d/{x,y}` 的目录并返回 0。

现已实现（`vbash/brace.go`），覆盖：

- 逗号列表 `{a,b}`：任意层嵌套、可与前后缀拼接（`pre{a,b}post`）、可连写做笛卡尔积（`{a,b}{1,2}`）
- 序列 `{1..3}`、`{3..1}`、`{01..03}`（保前导零）、`{1..10..3}`（步长）、`{a..e..2}`、`{-2..2}`
- 引号内与 `\{`/`\}` 转义不展开；`{}`/`{a}`（无逗号非序列）按字面保留
- 已接入命令参数、`for … in` 列表；**赋值上下文不展开**（`x={a,b}` 保持字面，与 bash 一致）
- 字母范围跨越 `\` 时按 bash 产出空元素（`{Z..b}` 中间那个空字段）

基准新增 `T2/038`–`T2/044`，单测 `vbash/brace_test.go`。

### 1.1 与真 bash 的已验证一致项

`{,a}`→`a`、`{a,}`→`a`、`{,,}`→空字段、`{a,,b}`→`a b`、`{"a,b",c}`→`a,b c`、
`{a,"b,c"}`→`a b,c`、`{a,{b,c}}`→`a b c`、`{{a,b}}`→`{a} {b}`、`{a{b,c}}`→`{ab} {ac}`。

### 1.2 语料命中率（口径修正）

**0.091%（16 / 17516 条历史 `run_shell` 调用）**。早先的 1.81% 有误——把**引号内的正则量词**
（`sed "s/^.\{0,45\}\] //"`）算成了展开；0.027%（6 条）则漏掉了片段含 `/` 的形态。
重算口径为「引号状态机判定未引用 + 剥掉 heredoc 正文 + 排除命令起始位置的花括号分组」，
并按 bash 判定规则（含 `,` 或合法序列）过滤。

其中会造成文件系统副作用的有 14 条：`mkdir -p .../{a,b,c}` 4 条、`rm -f ...mock-{c,d,e}.db*` 2 条、
`wc -l internal/web/*.{html,js,css}` 1 条，其余为命令分组或变量赋值。

> **一处实现提示（已验证）**：花括号分组 `{ echo a; }` 在 AST 里是 `GroupKind = GroupBrace`，
> 不会进入词段的 `Segment`，因此检测/展开只看词段即可，不需要额外区分命令位置。

## 2 已闭合：`printf` 精度与标志位（P1 → 已实现）

原问题是浮点精度不生效（`%.3f` 截成 `3.1`）、`%e` 不转科学计数。**复核时发现范围更大**：
整数精度（`%.3d`→`5`）与标志位（`%+d`/`% d`/`%#x`/`%#o`）同样不生效，
而 `capability` §3.1 当时写的是「整数精度生效」，是错的。

现已实现（`vbash/printf.go`，独立文件）：

| 类别 | 覆盖 |
|---|---|
| 浮点 `f F e E g G` | 精度、宽度、`-`/`0`/`+`/空格/`#`、`%g` 的 `1e+06` 与尾零裁剪、`%e` 指数至少两位 |
| 整数 `d i u o x X` | `%.Nd` 补零（负数含符号位）、`%u`/`%x` 对负数按 64 位无符号、`%#x`/`%#o` 前缀、`%+d`/`% d`、`0` 填充在 `%.Nd` 下让位 |
| 字符串 `s c b q` | `%.Ns` 按 rune 截断、宽度/左对齐 |
| 数字解析 | `0x` 十六进制、前导零八进制、`'A` 取字符、溢出取边界值、`3.9`/`12abc` 取前缀值 |
| 诊断口径 | `abc: invalid number`、`08: invalid octal number`、`…: Numerical result out of range`，退出码 1 |

基准新增 `T2/045`–`T2/049`，单测 `vbash/printf_test.go`。

### 2.1 语料形态

口径修正后，走 vbash 内建的 shell 层 `printf` 带浮点动词的只有 **3 行**（0.017%）；
原文档的 18 条里大部分是 `awk '{printf "%.1f"}'`——**那是 awk 自己的 printf**，与 vbash 内建无关。
形态仍真实（`printf '%-42s %8.1f MiB\n' "$d" "$(echo "scale=2; $t/1048576" | bc)"`），
但它不构成「agent 统计汇报的常规写法」这种量级的论据。

## 3 已实现：后台 `&` 真异步与进程围栏（原 P2 的文档矛盾，现已改为实现对齐）

### 3.0 真异步（2026-11-07）

原状态为"同步执行"（`sleep 1 & echo done` 等 1010ms）。改为真异步后：

| 用例 | 同步（旧） | 真异步（现） | bash |
|---|---|---|---|
| `sleep 2 & echo done` | 2009ms | **~1ms** | ~5ms |
| `sleep 1 & sleep 1 & sleep 1 & wait` | 3018ms | **1006ms** | 1006ms |
| `set -e; false & echo after; echo end` | 无输出 rc=1 | `after`/`end` rc=0 | 同 |
| `$!` | 空 | pid | pid |

配套：`wait`（无参/PID）返回作业状态、`$!` 为最近后台 pid；**作业控制（`jobs`/`kill %N`/`fg`/`bg`）不做**；
后台命令 stdin 接 `/dev/null`、
赋值与 `cd` 不回流父 shell（库内 goroutine 需隔离 env，与 bash 子 shell 的偶然共享不同，属有意偏差）。

### 3.1 进程围栏（禁止污染宿主）

目标是"后台进程跟随调用方主进程生命周期 + 退出时保底拦截"，**作业号控制（`fg`/`bg`）不在范围内**。
`Fence` + `EnableSubreaper()` 提供：**只登记 `&` 分支起的进程**（前台命令归 `CancelCmd`/ctx 管，
不进围栏——避免每条前台命令一次无人消费的 /proc starttime 读）→
主进程退出时以登记项为根收集子孙 → 按 `sid != 主 sid` 放行整棵逃逸会话树 →
按进程组收割 → `Wait4` 清僵尸。2026-11-07 一轮 review 修掉的九项缺陷（围栏漏收 `( ) &` 分支、
`wait` 已完成作业失效、后台 `<` 丢失、内建/函数 `&` 报 not found、`wait` 无参不等复合分支、
无作业 `$!` 为 0、围栏表泄漏、死代码、文档失真）均已闭合并由 `vbash/jobs_test.go` 与基准
`T1/050`–`056` 守护。细节与已知边界（含 darwin 退化）见 `docs/vbash-capability.md` §4.1。

原第三节的文档校正内容仍有效（同步时代的口径），保留如下。

三处表述互不一致，以实现为准校正：

| 出处 | 原表述 | 现表述 |
|---|---|---|
| `plan` §3.4 | 「后台启动后不等待，`wait` 报错」 | 「**同步执行**（启动后等待完成，不是真后台），`wait` 为空操作返回 0」 |
| `capability` §3.3 | 「启动后等待完成」 | 补实测数据：`sleep 1 &` 耗时 1010ms、裸 `&` 同样等待、`$!` 恒空 |
| `capability` §4 | 「同步执行，`wait` 空操作」 | 补「`nohup server &` 会在超时上限处被强杀」这一后果 |

同步执行**不泄漏子进程**（`pgrep` 复查无残留），是确定性取舍而非缺陷。
`eval`/`trap` 在 `plan` 里写「报错」，实测为 `command not found`（127），已按 `capability` 口径校正。

## 3.1 已闭合：递归与规模上限（P1 → 已实现）

查「变量展开是否有递归深度限制」时发现三处缺口，全部按「加明确上限 + 明确报错」闭合：

| 缺口 | 实测后果（修复前） | 修复 |
|---|---|---|
| 大括号展开无递归深度上限 | `{a,{a,{a,…}}}` 每层递归一次，`Parse` 拦不住（大括号在词段里是普通字面量，不进 `lexer.readBraced`） | `vbash/brace.go` 的展开器带 `depth`，复用 `maxNestDepth = 64`，超限报「大括号展开嵌套过深（上限 64 层）」 |
| 大括号展开指数膨胀、无字段数上限 | `{a,b}` 连写 n 次产生 2ⁿ 个字段：n=20 → 104 万字段/1.4s，n=22 → 419 万/6.8s；66 字节命令可吃掉几 GB | 字段数上限 `maxBraceFields = 16384`，按**一次词列表**累积（不是每词各自计，否则多词拼起来照样能绕）；`{1..N}` 序列在生成循环里就中断，不会先生成再检查 |
| shell 函数自递归无深度上限 | `f(){ f; }; f` 2 万层耗时 4.8s、未栈溢出，但深度完全取决于宿主栈大小（真 bash 在此处直接 SIGSEGV） | 用上一轮遗留的闲置字段 `saveDepth`（改为 `funcDepth`）+ `maxFuncDepth = 512`；超限打 `f: maximum function nesting level exceeded (512)` 并让该次调用返回 1、脚本继续——这是 bash `FUNCNEST` 的语义（实测 `FUNCNEST=3 bash -c 'f(){ f; }; f; echo after'` 只输出诊断、`after` 不打印但脚本退出码为 1，不整体中止） |

同一轮还顺带修掉一处**既有**的 O(n²)：`findBraceFrom` 原先对每个 `{` 都向后线性扫描到结尾定位配对，
`{a` 重复 n 次即 O(n²)（实测 n=1600 → 1.36s，n=5000 → 卡住 15s 以上）。改为一次栈式配对后按表定位，
`{a`×20000 降到 3ms；`TestBraceUnmatchedOpenLinear` 守护。

**子 shell 与管道必须继承深度**：`branch()` 复制 `funcDepth`。二者都是同 goroutine 同步递归，
不继承就等于「`f(){ ( f ); }` 绕过上限」。

**变量本身不做递归展开**（`a='${b}'; b='${a}'; echo "$a"` → 字面 `${b}`，与 bash 一致），
所以不存在变量间接递归爆炸，无需处理。

基准新增 `T2/052`、`T2/053`、`T3c/126`（都是**上限内**的正常用法，golden 由真 bash 录制）；
超限行为由单测 `vbash/brace_test.go`、`vbash/funcdepth_test.go` 守护——
上限本身是宿主相关的安全阀，不写进跨实现基准。

## 3.2 已闭合：管道段/命令替换内看不到函数（P2 → 已实现）

原问题：`f(){ echo x; }; f | cat` 报 `f: command not found`（bash 输出 `x`），
`echo "[$(f)]"` 同样拿不到函数。子 shell 侧反而是对的（`( f )` 能调用、其中定义不泄漏）。

根因：`runner.go` 的 `runPipeline` 与 `commandSubstitution` 都用 `r.branch(...)` 造子环境，
而 `branch` 不复制 `funcs`——`runSubshell` 之所以没事，是因为它显式补了
`sub.funcs = r.funcs` 这一行（管道与命令替换没补）。

修法**不能照抄那一行**：管道各段并发跑在 goroutine 里，共享同一个 map 会让「段内定义函数」
变成并发 map 写（`-race` 会直接报，运行时可能崩）。改为 `branch` 里 `copyFuncs` 各拷一份
（`*funcDef` 指针共享——函数体 AST 不可变，没必要深拷），于是：

| 写法 | 修复前 | 修复后（与 bash 一致） |
|---|---|---|
| `f(){ echo x; }; f \| cat` | `f: command not found` | `x` |
| `f(){ cat; }; echo hi \| f` | `f: command not found` | `hi` |
| `f(){ echo F; }; echo "[$(f)]"` | `f: command not found` | `[F]` |
| `f(){ echo one; }; f \| ( f(){ echo two; }; f ); f` | 段内 `f` 找不到 | `two` 然后 `one`（段内重定义只在段内生效） |
| `( g(){ echo g; }; g ); g` | 外层 `g` 找不到 | 同上（子 shell 定义不泄漏，原本就正确） |

`runSubshell` 里那行手动赋值随之删除（`branch` 已经做了）。

基准新增 `T3c/127`、`T3c/128`。单测 `vbash/funcdepth_test.go` 覆盖「管道/命令替换可见」
「子 shell 定义不泄漏」「段内重定义隔离」三组共 3 条。

### 3.3 已闭合：复合命令后接带 fd 前缀的重定向（接入前阻塞项）

`parseCommand` 的复合命令分支只认 `TokOp` 形态的重定向，没走 `redirectFd`，
`done`/`fi`/`esac`/`}`/`)` 后紧跟的数字前缀一律「未预期的记号」、退出码 2：

`for i in 1; do echo $i; done 2>/dev/null`、`{ echo a; } 2>/dev/null`、`( echo a ) 2>/dev/null`
等全部解析失败，而 `... done >f`（无 fd 前缀）正常。`capability` §3.1 早写「redirs 可挂在
分组/复合命令/函数上」，属实现落后于文档。

真实语料命中 0.90%（11/1219 条历史 `run_shell`），形态集中在「跑批时静音 stderr」——
开着 vbash 就会把原本能跑的命令变成硬报错。已抽出 `(*parser).tryFdRedirect` 统一两处路径，
基准新增 `T3c/129`–`133`（总数 223 → 228，七层 100%），单测见 `TestParseFdRedirectOnCompound`。

## 4 遗留：接入侧（P3，给调用方）

| 项 | 说明 |
|---|---|
| ~~tanya 尚未接线~~ | **已接线**（2026-11-06）：tanya 侧新增 `vbash` 配置项（yaml/env `TANYA_VBASH`）并**默认开启**，`main` 把 `Vbash`/`Environ: os.Environ` 透传给 `tools.Standard`。同轮闭合的解析缺陷见下方 §3.3 |
| `describeVbash` 未真机验证 | 仅在非 POSIX 宿主（pwsh/cmd）生效，`make cross` 只保证编译。Windows 侧属「实现完成、待验证」 |

## 6 已闭合：调用方真实试用暴露的 13 项（2026-11-06）

来源：tanya 侧开启 vbash 后做了一轮真 bash ↔ vbash 差分实测（约 180 条用例），
暴露出一批**文档未记录**的缺陷。下表按实际影响排序，全部已修并绑基准。

语料命中率口径：tanya `.tanya/sessions/*.jsonl` 里 1764 条历史 `run_shell` 调用。

### 6.1 heredoc 体内展开（P0，最严重）

原实现 `expandHeredoc` 逐字符扫 `$`、只取第一个 Segment 交给 `parameter()`，而 `parameter()`
只处理 `SegVar`/`${…}`，遇到 `SegCmdSub`/`SegArith` 直接 `bad substitution`；反斜杠完全没有分支。四个缺陷：

| 写法（未引用终止符） | 修复前 | 修复后（= bash） |
|---|---|---|
| `$(echo inner)` | 退出码 1、`bad substitution` | `inner` |
| `$((2+3))` | 静默输出空 | `5` |
| `\$b \\c \`` | `\ \\c \`` | `$b \c `` |
| 行尾 `\` 续行 | 原样保留反斜杠与换行 | 两行接成一行 |

- **修法**：新增 `vbash/lexer.go` 的 `lexHeredocSegments`——把正文按「双引号模式」交给同一套词法
  （`"`/`'` 不特殊、`\$ \\ \`` 去一层反斜杠、行尾 `\` 吞换行），产出 Segment 序列后
  `expandHeredoc` 逐段走 `SegLiteral/SegVar/SegBraced/SegArith/SegCmdSub`。原来的手写 `$` 扫描删除。
- **基准**：`T3h/021`–`030`（命令替换、算术、反斜杠、续行、混合展开、体内引号字面、`$*` 按 IFS、
  单行混合、引用终止符不展开、展开后接管道）。
- **单测**：`vbash/fixes_test.go` 的 `TestHeredocBody*`（7 条）。

### 6.2 `test`/`[` 的整数语义（P0）

`test.go` 用算术的 `parseInt`，而它不认符号、且把前导零当八进制。`[ -2 -lt 0 ]` 直接报
`integer expression expected`（退出码 2）——**`while [ $x -gt 0 ]` 这类递减循环一旦跨过 0 就炸**。
真 bash 的口径与算术不同：

| 写法 | 算术 | `test` | `exit` |
|---|---|---|---|
| `010` | 8（八进制） | **10**（十进制） | 10 |
| `0x10` | 16 | **报错** `integer expected` | **报错** `numeric argument required` |
| `-2` / `+3` | 支持 | 支持 | 支持 |

- **修法**：新增 `vbash/numparse.go`，把三种口径拆成 `parseArithInt`（保留八/十六进制）、
  `parseDecimalInt`（十进制 + 符号），`test` 用后者。诊断文案对齐 bash：
  `[: abc: integer expected`（原是 `integer expression expected`）。
- **基准**：`T3c/148`（负数比较）、`T3c/149`（`010` 按十进制）。
- **单测**：`TestTestBuiltinNegativeIntegers`、`TestTestBuiltinLeadingZeroIsDecimal`、`TestTestBuiltinNonNumericDiagnostic`。

### 6.3 `exit` / `return` 的参数语义（P0）

`exit -1` 返回 0（`parseInt("-1")` 失败 → 保留上次状态）；`exit abc` 静默返回上次状态。三态区分：

| 写法 | 行为 |
|---|---|
| `exit -1` / `exit -300` | 255 / 212（`& 0xff`） |
| `exit +3` / `exit 010` | 3 / 10 |
| `exit abc` | stderr `exit: abc: numeric argument required`，状态 2，**继续执行** |
| `exit 1 2` | stderr `exit: too many arguments`，状态 1，**终止** |
| `f(){ return abc; }` | stderr + `$?`=2，**函数体继续执行到结尾** |
| `f(){ return 1 2; }` | stderr + **终止整个脚本**（状态 1） |

- **修法**：`statusArg` 返回 `statusKind` 三态（`statusOK`/`statusNumeric`/`statusTooMany`），
  `builtinExit`/`builtinReturn` 各自决定「继续」还是「终止」。`return` 在函数外告警并返回 2。
- **基准**：`T3c/140`–`142`。
- **单测**：`TestExitNegativeWraps`、`TestExitNonNumericContinues`、`TestExitTooManyArguments`。

### 6.4 循环外 `break`/`continue`（P0）

原实现无条件抛出 `loopControl` 错误，外层没人接就一路冒到顶层——`break; echo after` **把 after 吞了**
（输出为空、退出码 0）。真 bash 是「告警 + 忽略 + 继续」。

- **修法**：`Runner.loopNest` 计数（进入 `for`/`while`/`until` 体 +1），`loopNest == 0` 时打
  `break: only meaningful in a 'for', 'while', or 'until' loop` 并返回 0。`callFunc` 进入时**清零**并在返回时恢复——
  bash 里函数内的 `break` 不算循环内（`f(){ break; }; for i in 1 2; do f; echo $i; done` 输出 `1 2`）。
- **基准**：`T3c/137`–`139`。
- **单测**：`TestBreakOutsideLoopContinues`、`TestBreakInFunctionInsideLoop`。

### 6.5 `for x; do`（省略 `in`）（P0）

解析器只在看到 `in` 时收集词表，否则 `Words` 为空 → 空迭代（bash 遍历位置参数）。

- **修法**：`Compound.NoIn` 标记，`runFor` 在 `NoIn` 时取 `Env.Args()`。
- **基准**：`T3c/134`–`136`。**单测**：`TestForWithoutIn`。

### 6.6 `echo -e` 的 `\c` 与 `\0NNN`（P0）

`decodeEchoEscapes` 遇到 `\c` 只是 `return` 已有文本，但换行仍加 → `x\cy; echo tail` 输出两行
（bash 是 `xtail`）。`\0NNN` 完全不解释。

- **修法**：返回 `(text, stop)`，`stop` 时抑制换行；补 `\0NNN`（1–3 位八进制）、`\xHH`、`\uHHHH`、`\UHHHHHHHH`。
- **基准**：`T3c/143`、`T3c/144`。**单测**：`TestEchoBackslashC`、`TestEchoOctalEscape`。

### 6.7 `$*` 在双引号内（P1）

三处硬编码 `strings.Join(args, " ")`，`IFS=,` 时 `"$*"` 应得 `a,b,c`。

- **修法**：`Env.JoinArgs()` 取 IFS 首字符（IFS 为空则直接相连、未设置则空格），
  接入 `applySegment`（引用/未引用两种）、`expandToString`、`expandHeredoc`。
- **基准**：`T3c/156`、`T3h/027`、`T2/057`。**单测**：`TestStarJoinsWithIFSFirstChar`。

### 6.8 算术内嵌展开（P0）

`$(( ${v:-1} + 1 ))`、`$(( 1 + $(( 2 )) ))` 硬报错（`parameter()` 不认 `SegArith`、`braced()` 处理不了
嵌套算术），`$(( $((2)) ))` 静默得 0。

- **修法**：`parsePrimary` 的 `$` 分支收敛到 `dollarOperand()`——按定界符切出完整 `$…` 片段，
  交给 `expandArbitrary()` 展开成文本，再递归 `arith()` 求值。`$#`/`$?` 等特殊参数、`${…}`、
  `$( )`、`$(( ))` 全部覆盖。
- **基准**：`T3c/155`、`T2/054`、`T2/056`。**单测**：`TestArithEmbeddedExpansions`。

### 6.9 `set -o pipefail` / `set -u`（P1）

两个选项都被静默忽略。

- **pipefail**：`Runner.PipeFail`，`runPipeline` 在开启时取**首个非零段**的状态
  （`false|true`→1、`true|false|true`→1、`echo hi|false|echo x`→1 且 stdout 完整）。
- **nounset**：`Env.NounSet`；`$VAR`、`${VAR}`、赋值右侧、算术裸名、`$1` 未绑定时打
  `<shell>: line N: NAME: unbound variable` 并以 **127** 退出；`${NAME:-x}` 等带默认值的形态不受影响。
- **`set` 解析重写**：支持选项簇（`-euo pipefail`）、`-o`/`+o` 带参与无参、`set -- 位置参数`，
  且**不再把位置参数当选项吃掉**（原实现会导致 `set -- a b; set -o pipefail` 把 `$#` 变成 1）。
- **基准**：`T3c/145`–`147`。**单测**：`TestSetPipefail`、`TestSetNounset*`、`TestSetOptionClustersKeepPositional`。

### 6.10 `$$` 与内建 `kill` 安全阀（P0，安全问题）

`Env.pid` 从未赋值，`$$` 恒 `0`。危险在 `kill $$`：宿主侧 `kill 0` 等于向**整个进程组**发信号，
实测能杀穿调用方（上一轮把执行差分的 python 进程判死）。

- **修法**：`NewEnv` 设 `pid = os.Getpid()`；新增第 22 个内建 `kill`（POSIX special builtin，不越界）：
  `-s SIG`/`-n NUM`/`-SIG`/`--`/`-l`；对 `pid <= 0`（`kill 0` 进程组、`kill -1` 全体）与宿主自身 pid
  **一律拒绝**（`Operation not permitted`、退出码 1），**但 `-0` 探测自身放行**（不发信号，安全）；
  其余 pid 经 `platformOps.DeliverSignal` 透传 `syscall.Kill`。
- **基准**：`T3c/157`、`T3c/158`。**单测**：`TestDollarDollarIsHostPid`、`TestKillRefusesSelfAndGroup`、`TestKillSignalLookup`。
- **注意**：基准录制**不能出现 `kill 0`**——录制进程与它同组，会把录制自己杀掉（本轮踩过）。

### 6.11 参数/算术扩展语法补齐（P2）

| 语法 | 实现要点 |
|---|---|
| `${V:off}` `${V:off:len}` | 负偏移相对末尾；负长度相对**整个值**（不是剩余段）；越界（负长度 < -len）报 `substring expression < 0` |
| `${V^^}` `${V,,}` `${V^}` `${V,}` | 可带 `[pat]` 限定；只对 ASCII 字母转换 |
| `${!V}` | 把 V 的**值**当变量名；取不到报 `invalid indirect expansion` |
| `base#num`（2–64） | `2#101`→5、`16#ff`→255、`36#z`→35；`2#-101` 按 bash 报 `invalid integer constant` |
| `n++` `n--` `++n` `--n` | `parseAssign` 先识别自增（否则名字被当赋值吃掉）；后置表达式记录起始切片判断是否裸变量名 |
| `**` | 右结合（`2**3**2`→512），优先级高于 `* / %`，`1+2**3`→9 |

- **基准**：`T3c/150`–`155`。**单测**：`TestSubstring*`、`TestCaseConversionExpansion`、
  `TestIndirectExpansion`、`TestArithBaseConstant`、`TestArithIncrement`、`TestArithPower`。

### 6.12 `printf %q`（P2）

原实现用 `shellQuote`（单引号包裹），bash 是**逐字符反斜杠转义**：`a b` → `a\ b`，空串 → `''`，
不可打印字符 → `$'…'`。安全字符集按实测枚举（`% + - . / : = @ _` 与字母数字直接输出，其余加 `\`）。

- **修法**：新增 `vbash/shellquote.go` 的 `shellQuoteEscaped`，43 个字符逐一与 bash 比对零差异。
- **基准**：`T3c/159`。**单测**：`TestPrintfPercentQ`。

### 6.13 已知取舍（本轮明确不做）

- **heredoc 体内 `$*` 与 `$@` 同现**：bash 有条未文档化怪癖（体内出现 `$@` 时 `$*` 改按空格连接）。
  不复刻，见 `docs/vbash-capability.md` §3.3.1。
- **`-c` 与脚本文件的 `exit 1 2` 差异**：`bash -c` 终止、`bash file.sh` 继续。vbash 按 `-c` 口径
  （`run_shell` 的实际形态）实现。

## 5 复现环境说明

本文所有 `vbash` 侧实测均在 **Linux** 上完成，用的是仓库自带的基准适配器：

```bash
make vbash-run      # 构建 vbash/testdata/cmd/vbashrun
vbashrun -c '<命令>'
```

行为差分只跑了**纯内建或不触碰文件系统**的用例；`mkdir` 类副作用用例全部落在临时目录内并已清理。
跨实现差分若需扩到真实语料，必须先读 `AGENTS.md`《构建与测试》末尾的隔离纪律
（容器 / mount namespace / 只读 bind mount，不得以「子进程 cwd 指向临时目录」代替隔离）。

## 7 已闭合：内建写已关闭管道产生 `broken pipe` 诊断（P2，既有缺陷）

`echo hi | true` 在 vbash 下会往 stderr 打一条
`<shell>: line 1: echo: write error: write |1: broken pipe`（bash 静默），退出码两边都是 0。

- **成因**：`builtin.go` 的 `echo`/`printf` 对 `io.WriteString` 的返回错误一律当失败打印
  （`write error: %s`），而管道下游 `true`/`head -2` 提前退出后写端拿到的是 `EPIPE`——
  这在 bash 里由 SIGPIPE 默认动作（进程被信号终止、不产生诊断）吸收，vbash 作为进程内解释器
  没有 SIGPIPE 可依赖，必须显式识别 `EPIPE` 并**静默**处理（`syscall.EPIPE`/`io.ErrClosedPipe`）。
- **复现**：并发下才稳定（`f | ( … ) | cat` 形态 8 路并发 50 轮命中 ~23/400），单跑多数不复现；
  基线提交 `e1f0917` 同样命中 25/400，**不是本次修复引入的回归**。
- **影响面**：只污染 stderr，不改退出码；但会让 `2>` 捕获或断言 stderr 为空的脚本/基准抖动
  （基准 `T3c/128` 因此偶发失败）。
- **修法**：收敛在 `vbash/io.go` 的 `(*IO).WriteString`（所有内建写 stdout/stderr 的唯一出口）——
  命中 `syscall.EPIPE` / `io.ErrClosedPipe`（含 `broken pipe` 文案兜底）时返回 nil，其余错误照旧上报。
  修在出口而非 `echo`/`printf` 各自的分支，避免漏掉其它内建。
- **守护**：`TestBrokenPipeWriteIsSilent`（4 条形态：`| true`、`printf | true`、`yes | head`、循环管道）
  与 `TestBrokenPipeKeepsRealWriteErrors`（确认真实写错误不被一并吞掉）。并发压测 8 路 × 50 轮由
  修复前 23/400 命中降至 0/400。
