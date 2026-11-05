# vbash 能力清单

> 归属（2026-11-04）：本文面向**使用者**，回答「这个解释器现在能跑什么、跑不了什么、跑不了会怎样」。
> 范围与决策见 `docs/vbash-plan.md`，实施偏差见 `docs/vbash-impl.md`，回归基准见 `docs/vbash-bench.md`。
> 接入方式见 `docs/shell-tool.md` §17。

## 1 一句话定位

`vbash` 是一个**跨平台的 bash 子集解释器**：同一段命令在 Linux、macOS、Windows 上行为一致，
不支持的构造**明确报错、绝不静默回落**系统 shell。

一阶段只虚拟解释器本身，**外部程序一律透传**（`cat`/`grep`/`sed`/`git`/`go`… 跑的是用户系统上那个），
所以它不是 coreutils 的复刻，也不是性能优化项目（实测 200 次 `echo hi`：vbash 0.42s、`bash -c` 0.40s，
没有可感知差异）。

## 2 现在怎么用

默认关闭。开关在 `tools/shell` 的配置上：

```go
tool, err := shell.New(shell.Config{
	LookPath:  exec.LookPath,
	Home:      home,
	Workspace: func() string { return cwd },
	Console:   console,
	Vbash:     true,          // 启用内置解释器（实验特性）
	Environ:   os.Environ,    // 必填：解释器不读 os.Environ
})
```

| 约束 | 行为 |
|---|---|
| `Vbash: false`（默认） | 与历史完全一致：`bash -c "$command"` |
| `Vbash: true` + `Environ` 缺失 | `New` 期报错，不起解释器 |
| shell 解析为 pwsh / cmd | `New` 期报错（解释器只实现 bash 语法） |
| `interactive: true` | **不经解释器**，仍借终端直跑（终端所有权属 `ctty`） |

## 3 能力矩阵

### 3.1 已支持

| 类别 | 内容 |
|---|---|
| **列表与连接** | `;` `&&` `\|\|` 短路、`!` 取反、换行分隔、`&`（后台，见 §4） |
| **管道** | `cmd1 \| cmd2 \| cmd3`，各段独立 `Env` 克隆，内外建混用 |
| **复合命令** | `for x in …; do … done`（含 `for x; do`）、`while`/`until … do … done`、`if`-`elif`-`else`-`fi`、任意嵌套 |
| **函数** | `name() { …; }` 定义与调用、位置参数、`$#`、`shift`、`return N`、递归、`local`（仅函数内） |
| **分组** | `( … )` 子 shell（`cd`/变量不回传）、`{ …; }` 花括号分组（共享环境） |
| **循环控制** | `break`/`continue`，支持 `break 2` 深度 |
| **`set` 选项** | `set -e`（含 bash 的三条例外）、`set --` 位置参数、`set -x` 被接受（不打印） |
| **重定向** | `>` `>>` `<` `2>` `2>>` `2>&1` `>&2` `&>` `&>>` `>\|` `<>` `<&`、`n<&m` 复制、`<&-`/`>&-` 关闭、`/dev/null` 内部映射；**严格保序**（`2>&1 >f` ≠ `>f 2>&1`）；redirs 可挂在分组/复合命令/函数上 |
| **heredoc** | `<<EOF`、`<<-EOF`（剥前导 tab）、`<<'EOF'`（禁展开）、多 heredoc 队列、未闭合按 EOF 截断并告警 |
| **参数展开** | `$VAR`、`${VAR}`、`${#VAR}`（字符数）、`${V:-x}` `${V-x}` `${V:=x}` `${V= x}` `${V:?x}` `${V?x}` `${V:+x}` `${V+x}`、`${V##p}` `${V#p}` `${V%%p}` `${V%p}`、`${V/p/r}` `${V//p/r}` `${V/#p/r}`；模式里可用 `*?[...]` |
| **特殊变量** | `$? $# $@ $* $0 $1…$9`、`${10}`、`$PWD`（活取当前目录）、`$IFS`、`${#@}` |
| **算术** | `$(( … ))`：`+ - * / %`（`**` 见 §3.3）、`<< >> & \| ^ ~ !`、比较与 `&&`/`\|\|`/三元 `?:`、赋值 `= += -= *= /= %= <<= >>= &= \|= ^=`、逗号、括号、`0x` 十六进制、前导零八进制（`010`=8）、变量与 `$VAR` 混写 |
| **命令替换** | `$( … )`，任意嵌套，末尾换行剥离，`$?` 传播子命令退出码 |
| **引号** | 单引号全字面、双引号（仅 `$ \` \" \\` 转义）、`\` 转义、`$'…'` ANSI-C（`\n \t \r \a \b \f \v \e \\ \' \"`）、行续 |
| **分词与 glob** | 未引用展开按 IFS 切分、双引号不切、`"$@"` 逐元素保形、空引号参数保留（`f ""` 传 1 个空串）、glob `* ? [...]`（无匹配保字面、不匹配隐藏文件） |
| **`~` 展开** | `~` → `$HOME`、`~/x` |
| **内建命令** | `: true false echo printf cd pwd export unset set shift exit return read wait local break continue test [`（共 21 个，含 `[`） |
| **`test`/`[`** | 一元 `-z -n -e -a -f -d -s -r -w -x -L -h`、二元 `= == != < > -eq -ne -lt -le -gt -ge`、`!` 取反 |
| **`printf`** | `%s %d %i %u %o %x %X %c %f %e %g %b %q`、宽度/左对齐/零填充、格式复用、`\n \t \r \\ \" \a \b \f \v \e \0NNN \xHH`；**整数精度生效，浮点精度不生效**（见 §3.3） |
| **`echo`** | `-n`、`-e`/`-E`、`\c` 与转义序列 |
| **`cd`** | `cd`（回家）、`cd -`（并打印目标）、`-L`/`-P`、维护 `PWD`/`OLDPWD` |
| **`read`** | `-r`、多变量切分（末变量吃剩余）、缺省 `REPLY`、EOF 返回 1、`while read` 惯用法 |
| **外部程序** | `$PATH` 查找、`argv[0]` 原样传递、cwd 与导出环境继承、退出码透传、信号退出复刻 `Killed  cmd` 文案、126/127 语义 |
| **错误口径** | 语法错误/命令不存在/`Is a directory`/heredoc 告警等 stderr 逐字节对齐 bash；shell 名前缀可注入 |

### 3.2 明确不支持（报错，不回落）

| 构造 | 行为 |
|---|---|
| `case`/`esac` | `UnsupportedError`，退出码 2（P2 计划） |
| `[[ … ]]` | `UnsupportedError`，提示改用 `[ ]` |
| 反引号 `` `cmd` `` | `UnsupportedError`，提示改用 `$( … )` |
| `(( … ))` 算术命令 | `UnsupportedError`，提示改用 `$(( … ))` |
| `<<<` here-string | `UnsupportedError`，提示改用 heredoc |
| 进程替换 `<(...)` / `>(...)` | `UnsupportedError`，提示改用 `$( … )` + 临时文件 |
| `\|&` | `UnsupportedError`，提示改用 `2>&1 \|` |
| `;&` / `;;&` | `UnsupportedError`（`case` 分支符） |
| `select` / `coproc` / `time` / `function` 关键字 | `UnsupportedError` |
| 数组 `a=(1 2)`、`${a[1]}` | 语法错误 / bad substitution |
| 嵌套超过 64 层（`$()` / `${}` / `$(( ))`） | `SyntaxError`，退出码 2（保护宿主不被撑爆内存） |
| 大括号展开 `{a,b}`、`{1..3}` | **不展开，按字面输出** |
| 别名 `alias`、`source`/`.`、`eval`、`exec`、`declare`、`type`、`command`、`trap`、`ulimit`、`umask`、`getopts`、`mapfile`、`jobs`/`fg`/`bg`/`kill` | 按普通命令处理 → `command not found`（退出码 127） |

### 3.3 与真 bash 的已知差异（不报错，但结果不同）

这些是**当前实现的行为**，不在基准覆盖内，写命令时要避开：

| 差异 | vbash | bash |
|---|---|---|
| 函数内对全局变量赋值 | 不回传（函数环境是克隆的）：`x=1; f(){ x=2; }; f` → `1` | 回传 → `2` |
| 函数内 `$0` | 函数名 | shell 名 |
| `for x; do`（省略 `in`） | 不迭代（空） | 遍历位置参数 |
| `${V:off:len}` 子串切片 | 报 bad substitution | 支持 |
| `${V^^}` `${V,,}` 大小写、`${!V}` 间接展开 | 报 bad substitution | 支持 |
| 算术 `base#num`（`2#101`、`16#ff`） | 报错 | 支持 |
| 算术 `n++` / `n--` | 报错 | 支持 |
| 算术 `**` 幂 | 报错 | 支持 |
| `set -o pipefail` | 被忽略（管道状态取末段） | 生效 |
| `set -u` | 被忽略（未定义变量展开为空） | 报未绑定变量 |
| `set -x` | 被接受但不打印追踪 | 打印 `+ cmd` |
| `IFS=: read a b`（赋值前缀作用在 `read`） | 前缀变量不回写、也不影响本次切分 | 影响本次切分 |
| `$( … )` 出现在算术表达式里 | 不支持 | 支持 |
| `${V:?msg}` 失败 | 退出码 1、无 stderr | 退出码 127、stderr 打 `V: msg` |
| `printf '%.2f'` / `%e` | 精度不生效（按字面输出）、`%e` 不转科学计数 | 正确格式化浮点 |
| `printf '%d' 3.9` | 静默取 3 | 报「无效的数字」并输出 3 |
| `[ … ]` 的 `( … )` 分组、`-a`/`-o` 逻辑连接 | 报错（`binary operator expected` / `too many arguments`） | 支持 |
| `[ f1 -nt f2 ]` 等文件比较 | 报错 | 支持 |
| `echo -e 'x\c'` 的 `\c` | 不抑制换行 | 抑制换行并终止输出 |
| 函数外 `return` / 循环外 `break`/`continue` | 静默生效（`return` 退出整个脚本，`break` 无效果但无提示） | 告警并忽略 |
| `$RANDOM $SECONDS $LINENO` | 空 | 有值 |
| `$_` | 上一个命令名（实现细节，值可能不同） | 上一命令的末参数 |
| `$!`（末后台进程 pid） | 空 | 有值 |
| `$$` | `0` | 真实 pid |
| `$*` 在双引号内 | 按空格连接（不按 IFS 首字符） | 按 IFS 首字符 |
| 后台 `&` | 启动后**等待完成**（不是真后台） | 立即返回 |
| 外部命令的 fd 3+ | 不支持（`3>&1` 对子进程无效） | 支持 |
| `echo -e 'a\0101b'`（`\0NNN` 八进制） | 不解释，按字面输出 | → `aAb` |

### 3.4 嵌套深度上限

`$()`、`${…}`、`$(( … ))` 三处嵌套都受 `maxNestDepth = 64` 限制，超限直接报语法错误：

```
$ vbashrun -c 'echo $($($(...)))'   # 超过 64 层
/bin/bash: -c: line 1: syntax error near unexpected token `$('
```

这条上限是**安全阀**，不是兼容性缺口：无上限时 lexer 会逐层复制内层文本（O(n²)），
深层输入能把宿主进程撑到内核 OOM killer 出手——`recover()` 拦不住 OOM，只能靠提前拒绝。
真实脚本实测远低于 10 层，64 层留了充足余量。

## 4 语义细节速查

- **`set -e` 三条例外**（与 bash 一致，逐条核对过）：条件上下文（`if`/`while`/`until` 的条件）不触发；
  `||` 链尾失败不触发；`&&` 链因短路没执行到末段时不触发。函数内失败会传导到调用方。
- **重定向保序**：`cmd 2>&1 >f` 把 stderr 留在原处、stdout 进 `f`；`cmd >f 2>&1` 两者都进 `f`。
- **heredoc 展开**：终止符被引号/反斜杠引用则体内不展开（`$`、反引号保持字面）。
- **`( )` vs `{ ; }`**：子 shell 克隆环境（`cd`、变量都不回传），花括号共享——`{` 后必须跟空格、`}` 前必须有 `;` 或换行。
- **函数环境**：调用时切克隆环境（`local` 生效、位置参数换成实参），这也是 §3.3 第一条「全局赋值不回传」的来源。
- **退出码**：`exit 300` → 44（取模 256）；外部命令被信号杀 → `128+signum`；`exit -1` 不取模（与 bash 的 255 不同）。
- **后台 `&`**：解析与执行都接受（`Sep = OpAmp`），但行为是**同步执行**——`sleep 1 & echo done` 会等 1 秒。
  `wait` 是空操作返回 0。

## 5 基准成绩

`make vbash-self`（适配器 `vbash/testdata/cmd/vbashrun` 模拟 `/bin/bash -c`）：

| 层 | 内容 | 结果 | 门槛 |
|---|---|---|---|
| T0 | 单命令 + 参数（20 条） | 20/20 = 100% | 100% |
| T1 | 管道/串联/重定向/退出码/`cd`（36 条） | 36/36 = 100% | 100% |
| T2 | 变量/命令替换/参数展开/分词（32 条） | 32/32 = 100% | 95% |
| T3h | heredoc（20 条） | 20/20 = 100% | 90% |
| T3c | 控制流/函数/分组（25 条） | 25/25 = 100% | 0%（S7 后 100%） |
| C | 真实语料高频形态（30 条） | 30/30 = 100% | 100% |
| X | 边界鲁棒性（30 条） | 30/30 = 100% | 100% |
| **合计** | | **193/193 = 100%** | |

用例取自真实会话语料、golden 由真 bash 录制（`docs/vbash-bench.md`）。

### 5.1 解析性能

命令替换的解析开销随**嵌套层数**线性增长：每一层都要重新 lex 内层整段文本。
深度上限（64 层）把最坏情况封住了，实际表现（400KB body，取多次最快值）：

| 嵌套层数 | 解析耗时 |
|---|---|
| 1 | 12ms |
| 8 | 47ms |
| 16 | 83ms |
| 64（上限） | 296ms |

真实命令（无深层嵌套）在微秒级：`echo hi` 2µs、`ls -la \| grep go \| head -5` 5µs、
`for i in 1 2 3; do echo $i; done` 7µs。

`wordBuilder` 的字面量累积曾是 O(n²)（逐字符 `Text += s`），已改为 builder 缓冲，
50KB body 单层从 328ms 降至 1.8ms。**若后续改动内部表示，务必用这个口径复核**。

## 6 代码地图

| 文件 | 职责 |
|---|---|
| `lexer.go` `token.go` | 词法：引号状态机、操作符、heredoc 配对、`$'…'` |
| `parser.go` `parser_control.go` `ast.go` | 递归下降：list / and-or / pipeline / command / 复合命令 / 重定向挂载 |
| `env.go` `expand.go` `arith.go` `glob.go` `match.go` | 变量表与环境克隆、参数展开、算术求值、glob |
| `runner.go` `runner_control.go` `runner_group.go` `exec.go` `redirect.go` | 执行：列表/管道/复合命令/函数/分组/重定向/外部透传 |
| `builtin.go` `test.go` `strconv.go` | 21 个内建、`test` 运算、数字格式化 |
| `errexit.go` | `set -e` 判定 |
| `diag.go` `errors.go` | bash 对齐的诊断文案、`UnsupportedError`/`SyntaxError`/`RuntimeError`/`ExitRequest` |
| `signal_unix.go` `signal_other.go` | 信号名与退出码（平台白名单分片） |

入口三个：`Parse(src) (*List, error)`、`RunScript(ctx, src, cfg) (int, error)`、
`Run(ctx, src, cfg) (ExecResult, error)`。`Config` 里 `Dir`/`Home`/`Env`/`Args`/`Arg0`/
`Stdout`/`Stderr`/`Stdin`/`ShellName` 全部注入，另可注入 `Handler`（接管外部命令）与
`ConfigureCmd`/`CancelCmd`（进程组与树杀策略）。

配套文件（`vbash/` 约 7.5k 行）：`parser_control.go` 复合命令语法、`runner_control.go` 复合命令执行、
`runner_group.go` 子 shell/分组/函数、`heredoc_warn.go` 未闭合告警、`fuzz_test.go` 随机片段不 panic、
`corpus_test.go` 全量语料回归、`testdata/cmd/vbashrun` 基准适配器。

## 7 什么场景可以放心用

- **模型生成的一般 shell 命令**：读写文件、grep/sed/awk 管道、`for` 循环、`if` 判断、函数封装、
  heredoc 写文件、命令替换、变量与参数展开——这些是语料里 90% 以上的形态，全部覆盖且绑基准。
- **需要跨平台一致的脚本片段**：语法层三平台统一（外部程序本身的行为差异仍在）。
- **需要确定性诊断的场景**：错误文案与退出码对齐 bash，不支持的构造立即报错并带位置。

**暂时不要依赖**：数组、大括号展开、`case`、`[[ ]]`、真后台与作业控制、`pipefail`/`nounset`、
浮点 `printf`、函数内改写全局变量。这些要么报错（好排查），要么行为与 bash 不同（§3.3，需避开）。
