# vbash 能力清单

> 归属（2026-11-04）：本文面向**使用者**，回答「这个解释器现在能跑什么、跑不了什么、跑不了会怎样」。
> 范围与决策见 `docs/vbash-plan.md`，实施偏差见 `docs/vbash-impl.md`，回归基准见 `docs/vbash-bench.md`。
> 接入方式见 `docs/shell-tool.md` §17。

## 1 一句话定位

`vbash` 是一个**跨平台的 bash 子集解释器**：同一段命令在 Linux、macOS、Windows 上行为一致，
不支持的构造**明确报错、绝不静默回落**系统 shell。

> **验证状态（2026-11-05）**：三平台分片（`vbash/platform_{linux,darwin,windows}.go`）已实现，
> Windows 侧含盘符归一、`;` 分隔 `PATH`、`PATHEXT` 查找、大小写不敏感匹配与退出码映射。
> 但**只在 Linux 上跑过基准**（290/290）；darwin/windows 目前仅由 `make cross` 保证编译与 vet 通过。
> 跨平台一致性属「实现完成、待验证」，不要当作已验证结论引用。

一阶段只虚拟解释器本身，**外部程序一律透传**（`cat`/`grep`/`sed`/`git`/`go`… 跑的是用户系统上那个），
所以它不是 coreutils 的复刻，也不是性能优化项目（实测 200 次 `echo hi`：vbash 0.42s、`bash -c` 0.40s，
没有可感知差异）。

## 2 现在怎么用

开关就是 `shell` 配置项本身，`vbash` 与 `bash`/`zsh`/`pwsh` 同级（没有独立的布尔开关）：

```go
tool, err := shell.New(shell.Config{
	Override:  "vbash",       // 与 "bash"/"zsh" 同级的取值；空则按平台自动探测
	LookPath:  exec.LookPath,
	Home:      home,
	Workspace: func() string { return cwd },
	Console:   console,
	Environ:   os.Environ,    // 必填：解释器不读 os.Environ
})
```

| 约束 | 行为 |
|---|---|
| `Override` 为空 | 按平台探测真实 shell（`bash -c "$command"`），与历史一致 |
| `Override: "bash"` 等 | 仍起真实 shell 子进程；要让命令走系统 shell 就显式这么写 |
| `Override: "vbash"` + `Environ` 缺失 | `New` 期报错，不起解释器 |
| `Override: "vbash"` | 进程内解释器，**不查 `PATH`**、不要求磁盘上存在该文件 |
| 判断入口 | `shell.IsBuiltin(name)`：供调用方判断某取值是否指向内置解释器（大小写/首尾空白不敏感） |
| 宿主为 pwsh / cmd | 与宿主无关：写 `vbash` 即生效，命令仍按 bash 语法书写（Windows 上的目标场景） |
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
| **`set` 选项** | `set -e`（含 bash 的三条例外）、`set -u`（未绑定变量：`$VAR` 与 `${VAR}`、`${#V}`、`${V^^}`、`${V:off}`、`${V#p}`、`${V/p/r}` 同样触发；带默认值的 `:-`/`-`/`:=`/`=`/`:+`/`+`/`?`/`:?` 不受影响；诊断 + 退出码 127）、`set -o pipefail`（管道取首个非零段）、`set -x` 被接受（不打印）、`set --` 位置参数；支持选项簇 `-euo pipefail` 与 `-o`/`+o` 无参打印 |
| **重定向** | `>` `>>` `<` `2>` `2>>` `2>&1` `>&2` `&>` `&>>` `>\|` `<>` `<&`、`n<&m` 复制、`<&-`/`>&-` 关闭、`/dev/null` 内部映射；**严格保序**（`2>&1 >f` ≠ `>f 2>&1`）；redirs 可挂在分组/复合命令/函数上 |
| **heredoc** | `<<EOF`、`<<-EOF`（剥前导 tab）、`<<'EOF'`（禁展开）、多 heredoc 队列、未闭合按 EOF 截断并告警；**未引用体内**支持 `$VAR` `${…}` `$( )`（含嵌套）`$(( ))`、`$*`（按 IFS 首字符）`$@`、`\$ \\ \`` 去一层反斜杠、行尾 `\` 续行 |
| **参数展开** | `$VAR`、`${VAR}`、`${#VAR}`（字符数）、`${V:off}` `${V:off:len}`（负偏移/负长度）、`${V^^}` `${V,,}` `${V^}` `${V,}`（可带 `[pat]` 限定）、`${!V}`（间接展开）、`${V:-x}` `${V-x}` `${V:=x}` `${V= x}` `${V:?x}` `${V?x}` `${V:+x}` `${V+x}`、`${V##p}` `${V#p}` `${V%%p}` `${V%p}`、`${V/p/r}` `${V//p/r}` `${V/#p/r}`；模式里可用 `*?[...]` |
| **特殊变量** | `$? $# $@ $* $0 $1…$9`、`${10}`、`$PWD`（活取当前目录）、`$IFS`、`${#@}`、`$$`（宿主进程 pid，非 0） |
| **算术** | `$(( … ))`：`+ - * / %`、`**` 幂（右结合）、`<< >> & \| ^ ~ !`、比较与 `&&`/`\|\|`/三元 `?:`、赋值 `= += -= *= /= %= <<= >>= &= \|= ^=`、自增 `n++`/`n--`/`++n`/`--n`、`base#num`（2–64 进制）、逗号、括号、`0x` 十六进制、前导零八进制（`010`=8）、变量与 `$VAR` `${…}` `$( )` `$(( ))` 混写 |
| **命令替换** | `$( … )`，任意嵌套，末尾换行剥离，`$?` 传播子命令退出码；`$(<file)` 直接读文件（等价 `$(cat file)`，不启子进程） |
| **引号** | 单引号全字面、双引号（仅 `$ \` \" \\` 转义）、`\` 转义、`$'…'` ANSI-C（`\n \t \r \a \b \f \v \e \\ \' \"`）、行续 |
| **分词与 glob** | 未引用展开按 IFS 切分、双引号不切、`"$@"` 逐元素保形、空引号参数保留（`f ""` 传 1 个空串）、glob `* ? [...]`（无匹配保字面、不匹配隐藏文件） |
| **`~` 展开** | `~` → `$HOME`、`~/x` |
| **大括号展开** | `{a,b}`（可嵌套、可与前后缀拼接、可连写做笛卡尔积）、`{1..3}`/`{3..1}`/`{01..03}`/`{1..10..3}`、`{a..e..2}`、字母与负数范围；引号内与 `\{` 转义不展开 |
| **内建命令** | `: true false echo printf cd pwd export unset set shift exit return read wait local break continue test [ kill`（共 22 个，含 `[`） |
| **`exec`** | `exec >f 2>&1`（无命令，**重定向持久生效**到脚本结束）、`exec <cmd> [args]`（执行该命令后不再继续，退出码取该命令）、裸 `exec` 无副作用；`-c`/`-l`/`-a name` 前缀被接受但语义简化 |
| **`test`/`[`** | 一元 `-z -n -e -a -f -d -s -r -w -x -L -h`、二元 `= == != < > -eq -ne -lt -le -gt -ge`、`!` 取反；整数比较接受前导 `+`/`-`，**按十进制**解释（`010`=10，`0x10` 报错），非数值诊断 `[: abc: integer expected` |
| **`printf`** | `%s %c %b %q %d %i %u %o %x %X %f %F %e %E %g %G`、宽度/左对齐/零填充/`+`/` `(空格)/`#`、精度（字符串截断、整数补零、浮点小数位）、格式复用、`-v var`（写变量而非输出，坏标识符按 bash 报 `not a valid identifier`）、`\n \t \r \\ \" \a \b \f \v \e \0NNN \xHH`；无效数字按 bash 打诊断并置退出码 1 |
| **`echo`** | `-n`、`-e`/`-E`；`-e` 下 `\c` 立即截断并抑制换行，支持 `\0NNN` 八进制、`\xHH`、`\uHHHH`、`\UHHHHHHHH` |
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
| 别名 `alias`、`source`/`.`、`eval`、`declare`、`type`、`command`、`trap`、`ulimit`、`umask`、`getopts`、`mapfile`、`jobs`/`fg`/`bg`/`disown` | 按普通命令处理 → `command not found`（退出码 127） |

### 3.3 与真 bash 的已知差异（不报错，但结果不同）

这些是**当前实现的行为**，不在基准覆盖内，写命令时要避开：

| 差异 | vbash | bash |
|---|---|---|
| 函数内对全局变量赋值 | 不回传（函数环境是克隆的）：`x=1; f(){ x=2; }; f` → `1` | 回传 → `2` |
| 函数内 `$0` | 函数名 | shell 名 |
| `set -x` | 被接受但不打印追踪 | 打印 `+ cmd` |
| `${V:?msg}` 失败 | 退出码 1、无 stderr | 退出码 127、stderr 打 `V: msg` |
| `[ … ]` 的 `( … )` 分组、`-a`/`-o` 逻辑连接 | 报错（`binary operator expected` / `too many arguments`） | 支持 |
| `[ f1 -nt f2 ]` 等文件比较 | 报错 | 支持 |
| `$RANDOM $SECONDS $LINENO` | 空 | 有值 |
| `$_` | 上一个命令名（实现细节，值可能不同） | 上一命令的末参数 |
| `$!`（末后台进程 pid） | 命令位 `&` 有值且完成后保留；**复合形态（`( ) &`/`{ } &`/函数 `&`/管道 `&`）不置 `$!`**（虚拟外壳无 pid，置空而非留旧值，避免 `kill $!` 误杀上一作业）；从未起过后台作业时为空，`set -u` 下报 unbound（同 bash） | 任何 `&` 都置真 pid |
| 后台命令的 stdin | 无 `<` 重定向时接 `/dev/null`（非交互 bash 语义），`<` 重定向生效 | 同 |
| `wait` 已完成作业 | 状态保留到被 `wait` 消费，与 bash 一致 | 同 |
| 内建/函数在 `&` 位 | 正常执行（虚拟子 shell）；纯内建后台（`export X=1 &`）不占 `$!` | 执行于子 shell，占 `$!` |
| 函数/复合命令后台化 | 跑在独立 goroutine 的隔离 env 上（bash 是子 shell 共享，`x=1 & echo $x` 时有偶然性），**赋值与 `cd` 不回流**属有意偏差 | 部分共享 |
| 作业控制（`jobs`/`fg`/`bg`/`disown`/`kill %N`） | 不实现（语料零使用）；`jobs` → `command not found`(127)、`kill %1` → 报 `arguments must be process or job IDs` | 支持 |
| 主动 `setsid` 逃逸 | **允许逃逸**，围栏收割时放行（设计取舍，见 §4） | 无此概念 |
| 外部命令的 fd 3+ | 不支持：`3>f`/`3>&1` 只对**内建**生效，透传给外部程序时不成立（`sh -c 'echo x >&3' 3>f` 会失败）。首次遇到会打一条 `fd N: not passed to external commands` 提示，避免静默误判 | 支持 |
| 已关闭的 stdin 传给外部程序 | `exec 0<&-` 后外部程序读到的是空输入（EOF）而非 `Bad file descriptor` | 子进程继承到已关闭 fd 0，读即报错 |

### 3.3.1 heredoc 体内 `$*` 与 `$@` 同现

真 bash 有一条未文档化的怪癖：未引用的 heredoc 体内**只要出现 `$@`**，同一体内所有 `$*` 都改按空格连接
（`IFS=,` 时 `[$*][$@]` → `[a b c][a b c]`，单独 `[$*]` 才是 `[a,b,c]`）。

vbash **不复刻**这条：`$*` 一律按 IFS 首字符、`$@` 一律按空格。基准 `T3h/027` 按此口径录制
（把两种形态分到两个 heredoc 里回避歧义）。真实语料里同现次数为 0，收益不足以换来实现复杂度。

### 3.4 嵌套深度与规模上限

四处嵌套/递归受 `maxNestDepth = 64` 限制，超过即报错（不静默降级）：

| 构造 | 上限 | 超限表现 |
|---|---|---|
| 命令替换 `$( … )` | 64 层 | 解析期语法错误（`parser.go`） |
| 参数展开 `${x:-…}` | 64 层 | 展开期错误（`expand.go`） |
| 算术 `$(( … ))` | 64 层 | 词法期语法错误（`lexer.go`） |
| 大括号展开 `{a,{a,…}}` | 64 层 | 展开期错误（`vbash/brace.go`） |

```
$ vbashrun -c 'echo $($($(...)))'   # 超过 64 层
/bin/bash: -c: line 1: syntax error near unexpected token `$('
```

大括号展开另有**字段数**上限 `maxBraceFields = 16384`（按一次词列表展开累积，不是每词各自计）。
`{a,b}` 连写 n 组产生 2ⁿ 个字段、`{1..N}` 产生 N 个，是命令行里唯一的指数级放大点：
不加限制时 66 字节的 `{a,b}`×22 就能吃掉几 GB。超限报「大括号展开字段过多」，不再静默膨胀。

shell 函数调用深度上限 `maxFuncDepth = 512`，超限打
`f: maximum function nesting level exceeded (512)` 并让**该次调用**返回 1，
脚本继续执行（同 bash `FUNCNEST` 的语义，不终止整个脚本）。子 shell 与管道分支继承当前深度——
它们是同一个 goroutine 里的同步递归，不继承就等于绕过了上限。

这些上限都是**安全阀**，不是兼容性缺口：无上限时 lexer 会逐层复制内层文本（O(n²)）、
函数递归几十万层才栈溢出，深层/大输入能把宿主进程撑到内核 OOM killer 出手——`recover()`
拦不住 OOM，只能靠提前拒绝。真实脚本实测远低于 10 层嵌套、字段数在数十量级、
函数递归深度个位数，余量充足。

### 3.5 跨平台行为（Windows 侧未真机验证）

内部只用一个路径表示：**POSIX `/`**。同一条命令在三平台按同一套规则解析，差异都在边界吸收：

| 写法 | 行为 |
|---|---|
| `/c/Users/x` | Windows 上映射到 `C:\Users\x`；Linux/macOS 上就是 `/c/Users/x`（不特殊对待） |
| `C:/Users/x`、`C:\Users\x` | 仅 Windows 上识别为盘符并归一为 `/c/Users/x`；`\` 在 bash 里本是转义符，所以实际书写请用 `/` |
| `cd d/x`（相对盘符） | 仅 Windows、且 `D:\` 真实存在时归一为 `/d/x`；否则仍按相对目录处理 |
| `\\server\share` | 仅 Windows：UNC 路径，原样保留 |
| `$PATH` | 三平台都是 `:` 分隔（Windows 上 `;` 分隔在内部被归一）；外部程序拿到的仍是宿主形式 |
| `~/x` | 用注入的 `Home`（`tools/shell.Config.Home`），不读环境变量 |
| `/dev/null` | 三平台都识别；Windows 另认 `nul`（不区分大小写） |
| `-x` / 命令查找 | POSIX 看 mode 0111；Windows 看后缀是否在 `.exe .cmd .bat .com` 内（类似 `PATHEXT`） |
| glob 匹配 | POSIX 大小写敏感；Windows 折成小写（保持宿主行为） |
| 被信号杀 | POSIX `128+signum`；Windows 取退出码低 8 位，Ctrl-C（`0xC000013A`）记 130 |

宿主 shell 是 pwsh/cmd 时也**允许**开启 vbash——这正是 Windows 上的目标场景，命令仍按 bash 语法书写。
`interactive: true` 例外：它不经解释器，直接交宿主 shell。

## 4 语义细节速查

- **`set -e` 三条例外**（与 bash 一致，逐条核对过）：条件上下文（`if`/`while`/`until` 的条件）不触发；
  `||` 链尾失败不触发；`&&` 链因短路没执行到末段时不触发。函数内失败会传导到调用方。
- **重定向保序**：`cmd 2>&1 >f` 把 stderr 留在原处、stdout 进 `f`；`cmd >f 2>&1` 两者都进 `f`。
- **heredoc 展开**：终止符被引号/反斜杠引用则体内不展开（`$`、反引号保持字面）。
- **`( )` vs `{ ; }`**：子 shell 克隆环境（`cd`、变量都不回传），花括号共享——`{` 后必须跟空格、`}` 前必须有 `;` 或换行。
- **函数环境**：调用时切克隆环境（`local` 生效、位置参数换成实参），这也是 §3.3 第一条「全局赋值不回传」的来源。
- **退出码**：`exit 300` → 44、`exit -1` → 255、`exit -300` → 212（统一按 `& 0xff` 取模，与 bash 一致）；
  外部命令被信号杀 → `128+signum`。参数语义同 bash：`exit abc` 打 `numeric argument required` 并**继续执行**
  （状态 2），`exit 1 2` 打 `too many arguments` 并**终止**（状态 1）。
- **`set -u`**：未绑定变量打 `<shell>: line N: NAME: unbound variable` 并以 127 退出
  （`$VAR` 与 `${VAR}` 一致；特殊参数名按 bash 口径，`$!` 报 `$!`、`${!}` 报 `!`、`$9` 报 `$9`、`${9}` 报 `9`）；
  `${NAME:-x}`/`${NAME-x}` 等带默认值的形态不受影响。
- **`kill` 内建安全阀**：`$$` 取宿主进程 pid（非 0），但 `kill` 对 `pid <= 0`（含 `kill 0` 进程组、
  `kill -1` 全体）与宿主自身 pid **一律拒绝**（`Operation not permitted`，退出码 1），
  只有 `-0` 探测自身是允许的。`kill -l` 走内部信号表，不触碰任何进程。
- **后台 `&` 真异步**：`sleep 2 & echo done` 立即返回、`for i in 1 2 3; do sleep 1 & done; wait`
  并行约 1s（同步实现时代为 3s）。命令位 `&` 起真进程，`( cmd & )`、管道内、循环内、函数内同样生效。
  附带的 bash 语义全部对齐：`$!` 为最近后台 pid、`wait`/`wait PID` 返回作业状态、
  `set -e` 下后台失败不触发退出；后台命令的**赋值前缀与 `cd` 不回流**父 shell（`x=2 & echo $x` → 原值）。
- **后台作业只跟随调用方主进程生命周期**（见 §4 进程围栏）：单轮 `RunScript` 结束后后台进程继续存活，
  直到调用方显式 `Fence.Reap()`（主进程退出路径）才被统一收割；`CancelCmd`/ctx 取消不影响已起的后台作业。

### 4.1 进程围栏（禁止污染宿主）

目标：**后台进程只跟随调用方主进程生命周期，退出时保底拦截，防止污染宿主**。

| 要素 | 做法 |
|---|---|
| 收容 | 调用方在主进程启动时 `vbash.EnableSubreaper()`（Linux 置 `PR_SET_CHILD_SUBREAPER`，零权限；darwin 不支持，退化） |
| 登记 | **只登记 `&` 分支起的进程**（pid + starttime），跨轮保留；不靠扫描全表猜归属 |
| 管理范围 | 管理范围即登记范围（`&` 分支及其子孙）；前台命令不进围栏（生命周期归 `CancelCmd`/ctx 超时），其残留的子孙也不收 |
| 收割时机 | **只在调用方主进程退出时** `Fence.Reap()`；单轮 `RunScript` 结束**不收割**（跨轮存活是需求） |
| 放行判据 | 后台根的 `sid != 主进程 sid` → 整棵会话树放行（**主动 `setsid`/double-fork 视为有意逃逸，一律放行**，设计决策）；linux 上另以 starttime 防 pid 复用 |
| 收割方式 | 以登记的后台项为根：进程表收集子孙 → 过滤 `sid == 主 sid` → 按进程组去重后 `kill(-pgid, SIGKILL)`；根已退出的也照杀其进程组（覆盖 `sh -c 'x &'` 留下的同组孤儿）；宽限 2s 后清僵尸 |
| 僵尸回收 | subreaper 收来的子进程必须 `Wait4(-1, WNOHANG)` 循环回收，否则永久 `state=Z` |
| 接线 | 调用方构造 `Fence` 后经 `tools.Options.Fence`（或 `vbash.Config.Fence`）传入；`EnableSubreaper` 与退出路径的 `Reap` 由调用方主进程负责。**退出路径必须覆盖「函数返回」而非只有 `os.Exit` 分支**——tanya 首轮接线只挂在 `exitNow` 上，`ask`/REPL 正常结束走的是 `main` 直接 `return`，`Reap` 从未执行（实测 `sleep 240 &` 端口残留），改为构造 `Fence` 处 `defer fence.Reap()` 后收效 |

**已知边界**（除注明外都是"漏收"方向，不会误杀）：

- **darwin 没有 `/proc`**：子孙收集与 starttime 防复用不可用，退化为「根级 `getsid` 判放行 + 杀根进程组」；
  自立进程组且不 setsid 的孙进程可能漏收，根 pid 被复用的小概率窗口无法排除
- 进程在**退出收割之后**才 `setsid` 的，围栏覆盖不到（收割是快照式，不是持续监控）
- `env -i cmd` / 显式覆盖环境后起的进程，若改用 token 归属则不可见（当前实现用围栏登记表，不受此影响）
- `sudo` 等提权后的进程不属于本围栏职责

**并发写**：后台作业与前台命令可能同时写 `Stdout`/`Stderr`。库用 `vbash.NewSyncWriter(w)` 串行化
（锁按 writer 实例，Out/Err 各包一层即互不干扰）；不经包装时**调用方 writer 必须并发安全**。

## 5 基准成绩

`make vbash-self`（适配器 `vbash/testdata/cmd/vbashrun` 模拟 `/bin/bash -c`）：

| 层 | 内容 | 结果 | 门槛 |
|---|---|---|---|
| T0 | 单命令 + 参数（20 条） | 20/20 = 100% | 100% |
| T1 | 管道/串联/重定向/退出码/`cd`（56 条） | 56/56 = 100% | 100% |
| T2 | 变量/命令替换/参数展开/分词/大括号/printf/赋值前缀/nounset（65 条） | 65/65 = 100% | 95% |
| T3h | heredoc（30 条） | 30/30 = 100% | 90% |
| T3c | 控制流/函数/分组（59 条） | 59/59 = 100% | 0%（S7 后 100%） |
| C | 真实语料高频形态（30 条） | 30/30 = 100% | 100% |
| X | 边界鲁棒性（30 条） | 30/30 = 100% | 100% |
| **合计** | | **290/290 = 100%** | |

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

### 5.2 真实语料差分的数据来源与审计（2026-11-05 补记）

除 §5 的录制基准外，另有一轮**真实语料差分**：从调用方会话记录（含归档卷）抽取
约 2 万条历史 `run_shell` 命令，逐条交给 `vbash.Parse` 与真 `bash -n` 比对，
并对其中 400 条做 vbash 与 bash 的行为差分。**该轮的过滤审计不充分，结论可用、方法不可复用**：

| 问题 | 说明 |
|---|---|
| 只按首词白名单判只读 | `sed`/`sort`/`find`/`date`/`awk` 都可能带写标志（`-i`/`-o`/`-delete`/`-s`/`print >`），首词白名单拦不住 |
| 把子进程 `cwd` 当沙盒 | 命令内部可 `cd` 到沙盒之外，`exec.Cmd.Dir` 对脚本内的 `cd` 没有约束力 |
| 未限制访问范围 | 只读遍历可触达可移动介质与整机路径，既是挂起风险，也让无关的路径信息进入上下文 |

**事后复核结果（无实际损害）**：各仓库工作树 `git status` 干净；家目录与文件系统根在时间窗内无新增项；
越界写入的目标父目录本就不存在（写入失败、未创建）；沙盒目录之外无写入目标
（全部 `2>` 重定向指向 `/dev/null`，命令替换与嵌套 `sh -c` 内零写动作）。

**该轮结论的可用性**：解析层通过率与各构造命中数是**纯静态统计**，不受影响；
行为差异项（多段 glob 不展开、`$$`/`$!` 取值、函数内全局赋值不回传、`pipefail` 被忽略等）
另有一组 90 条自建沙盒用例独立复现，结论成立。

**复跑这类差分时必须遵守**（已写入 `AGENTS.md`）：在容器 / mount namespace / 只读 bind mount 内执行；
或退一步只跑不触达文件系统的纯内建用例。**不得以「子进程 cwd 指向临时目录」代替隔离**。

## 6 代码地图

| 文件 | 职责 |
|---|---|
| `lexer.go` `token.go` | 词法：引号状态机、操作符、heredoc 配对、`$'…'` |
| `parser.go` `parser_control.go` `ast.go` | 递归下降：list / and-or / pipeline / command / 复合命令 / 重定向挂载 |
| `env.go` `expand.go` `arith.go` `glob.go` `match.go` | 变量表与环境克隆、参数展开、算术求值、glob |
| `runner.go` `runner_control.go` `runner_group.go` `exec.go` `redirect.go` | 执行：列表/管道/复合命令/函数/分组/重定向/外部透传 |
| `builtin.go` `test.go` `strconv.go` `exec_builtin.go` | 21 个内建、`test` 运算、数字格式化、`exec` 持久重定向 |
| `errexit.go` | `set -e` 判定 |
| `diag.go` `errors.go` | bash 对齐的诊断文案、`UnsupportedError`/`SyntaxError`/`RuntimeError`/`ExitRequest` |
| `brace.go` | 大括号展开：字符级摊平后找括号对，嵌套/序列/步长/前导零 |
| `printf.go` | `printf` 格式化：浮点/整数/字符串动词、标志位、精度、无效数字诊断 |
| `platform.go` `platform_posix.go` `platform_{linux,darwin,windows}.go` `platform_stub.go` | 平台差异表：路径与盘符归一、`PATH` 分隔、可执行判定、`/dev/null`、信号名与退出码 |

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

**暂时不要依赖**：数组、`case`、`[[ ]]`、作业控制（`jobs`/`fg`/`bg`/`kill %N`）、`pipefail`/`nounset`、
函数内改写全局变量。这些要么报错（好排查），要么行为与 bash 不同（§3.3，需避开）。
