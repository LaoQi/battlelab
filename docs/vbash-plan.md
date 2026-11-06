# 虚拟 bash：特性范围与实施规划

> 归属（2026-11-04）：本文是 `docs/vbash.md`（语料分析）与 `docs/vbash-bench.md`（回归基准）
> 的决策落地。**语料分析回答「真实命令长什么样」，本文回答「我们虚拟到哪一层」。**
> 状态：**S1–S8 已实施**（2026-11-04）：`vbash` 解释器达标 268/268，已接入 `tools/shell`（默认关闭）。

## 1 目标与边界

### 1.1 核心目标：跨平台用法一致性

同一段命令在 Linux、macOS、Windows 上应当**行为一致**。今天的断点：

> **S6 跨平台归一的状态（2026-11-05）**：三平台分片 `vbash/platform_{linux,darwin,windows}.go`
> 与 stub 已落地，Windows 侧实现完整但**未在 Windows 真机验证**（`make cross` 只保证编译通过）。
> 下方 §5 的表格是设计与实现口径，验证状态见 §8 的 S6 行。

| 平台 | 现状 | 断在哪 |
|---|---|---|
| Linux/macOS | `bash -c`（`posixCandidates`） | 无 |
| Windows（pwsh） | `pwsh -NoProfile -NonInteractive -Command` | 管道传**对象**而非文本；`\` 是分隔符，转义规则不同；`$VAR` 语义不同；`&&` 需 PowerShell 7+ |
| Windows（cmd） | `cmd /d /s /c` | 无 `&&` 早期版本、无管道语义、`%VAR%` 展开 |

后果是**提示词必须教模型按平台写不同命令**（见 `platform_windows.go` 的 `Capabilities` 文案：让模型改用 `Get-ChildItem`/`Select-String`）。这本身就是承认失败。

虚拟 bash 要做的，是让 `run_shell` 在三平台都接受**同一套 bash 子集语法**：
模型只学一种写法，平台差异由解释器吸收。

### 1.2 边界：虚拟解释器，不虚拟外部程序

**一阶段只虚拟 bash 解释器本身。** 命令首词若不是内建，一律按 `exec` 透传给宿主
（Windows 上已有 coreutils 支持，`cat`/`grep`/`sed` 等可用）。

这条边界带来三个好处：

| 好处 | 说明 |
|---|---|
| 实现量可控 | 不必复刻 `sed` 的 BRE/ERE 方言、`grep` 的 `-P`、`ls` 的排序规则——这些是复刻陷阱，收益只有省进程 |
| 语义可信 | 外部程序跑的就是用户系统上那个，行为与直接敲一致 |
| 与现有契约兼容 | `tools/shell` 的 `Console`/`Lease`、超时、输出截断、信号处理全部保留，虚拟层是**前置解析层**，不是替代 |

推论：**性能不是本项目目标。** 实测 `bash -c` 基线开销约 1.7–3.9 ms/次，
35062 条命令全量约 52 秒，摊到几百个会话可忽略；纯内建命令（`echo`/`cd`）可省掉进程，
但外部程序仍要 `exec`，端到端提升有限。**若只有性能诉求，本项目不值得做。**

### 1.3 一阶段范围（明确）

| 做 | 不做 |
|---|---|
| bash 语法子集解析（见 §3） | 外部程序语义复刻（`sed`/`grep`/`awk` 内部行为） |
| bash 内建命令（见 §4） | 作业控制（`jobs`/`fg`/`bg`/`wait` 语义） |
| 外部程序透传与退出码 | 交互式程序语义变更（仍走 `Console`/`Lease`） |
| 路径与编码归一（见 §5） | PowerShell / cmd 语法（不解析、不翻译） |

## 2 决策记录

| # | 决策 | 理由 |
|---|---|---|
| D1 | 目标定为**跨平台一致性**，不定位为性能优化 | 见 §1.1；实测性能收益可忽略（§1.2） |
| D2 | **只虚拟解释器，外部程序透传** | 防止范围膨胀到复刻 coreutils；与现有 `Console`/`Lease` 契约兼容 |
| D3 | 平台分片沿用库的**白名单**约定：`vbash` 三平台各有装配文件 | 与 `tools/shell/platform*.go` 一致，`make cross` 可拦漏标 |
| D4 | 不支持时**报错而非猜**（`ErrUnsupported` 带构造名与位置） | 静默误解析比报错危险得多；语料语法错误率 <0.1%，报错不伤体验 |
| D5 | 内建清单**只收 bash builtins**，不收 coreutils | 内建 `cat` 等于走 D2 的反面；且 coreutils 各平台可用 |
| D6 | `&&`/`||`/`;`/`|`/重定向的语义由解释器实现，不委派给宿主 shell | 这正是跨平台不一致的来源 |
| D7 | 一阶段**不追求 bash 兼容**，以 `vbash-bench` 达标为准 | 语料是唯一事实来源；corner case 无收益 |
| D8 | 基准分层调整：`T3` 拆为 `T3h`（heredoc）与 `T3c`（控制流） | 见 §7「基准与文档的既存矛盾」 |
| D9 | **不设回落开关**：虚拟层失败直接报错 | 实验特性不追求完备；静默回落会掩盖缺陷、丢失可观测性（否决了初稿的回落方案） |
| D10 | 配置默认关闭，需显式开启 | 实验性质，不改变现有默认行为 |

## 3 语法特性矩阵

### 3.1 必做（P0）

| 特性 | 语料依据 | 说明 |
|---|---|---|
| 引号状态机：`'` `"` `$'...'` `\` 转义、行内 `#` 注释 | 引号内含 `;`/`\|`/`&` 占 **23.9%** | **唯一刚性要求**，盲切必错 |
| 单词切分与 IFS | 全部 | 引号内外分词差异（`$X` vs `"$X"`） |
| `;` `&&` `\|\|` `\|` 换行 | `&&` 59.6%、`;` 45.1%、`\|` 56.8% | 短路语义与退出码传递 |
| 重定向 `>` `>>` `<` `2>` `2>&1` `&>`、`/dev/null` | `2>&1` 一族 **32.8%** | **fd 复制顺序敏感**（`>f 2>&1` ≠ `2>&1 >f`）|
| heredoc：`<<`、`<<-`、引号化/未引号化、多终止符**栈式**配对、终止符后接命令 | 26.3% 命令含；90% 引号化；21.2% 终止符后还有命令 | 体内按字面字节传输；`<<-` 只剥制表符 |
| `cd`（含 `cd -`、失败退出码） | **58.4%** 命令以 `cd` 开头 | 单条命令内持久；跨调用不持久（与现状一致）|
| 变量 `$VAR` `${VAR}` 与引号内外展开差异 | 4.4% | 变量名贪婪匹配（`$Xdef`）是经典错误 |
| `~/` 展开 | **23.5%** | 高频刚需 |
| 命令替换 `$(...)`（含嵌套） | 2.7% | 剥尾部换行 |
| 退出码：127/126/128+signum | — | 基准 X 层门槛 100% |
| `$?` `$#` `$@` `$*` `$$` `$0` | 3.5% | — |
| 管道退出码取末段 | — | 易错点 |
| `$'...'` ANSI-C 引用 | 0.2% | 基准要求 |

### 3.2 应做（P1，门槛需要）

| 特性 | 语料依据 | 说明 |
|---|---|---|
| 参数展开 `${V:-x}` `${#V}` `${V##*/}` `${V%%.*}` `${V//-/}` | 0.11% | 量小但 `T2` 门槛 95% 需要 |
| `$((...))` 算术展开 | 33 次 | **只在 `$(( ))` 内**；`((...))` 作语句不做 |
| glob 展开与无匹配保字面 | 1.1% | `ls *.txt` 依赖；`nullglob` 未开时保字面 |
| `export` 子进程可见性 | 302 次 | — |
| `for ... do ... done` | 3.8% | 控制流里最高频，性价比最高 |
| `if/then/elif/else/fi` + `[ ]`/`test` | 0.3% | 需配 `-f` `-d` `-z` `-n` 等常用测试符 |
| `( )` 子 shell、`{ ; }` 分组 | 0.7% / 0.3% | 子 shell 的 `cd` 不得影响外层 |
| `exit` / `return` / `set -e` | 0.2% / 0.22% | `set -e` 出现就必须正确处理 |
| `read`（stdin 非 tty 立即 EOF） | — | 基准 X/021 |

### 3.3 可选（P2，不影响一阶段验收）

`while`/`until`（0.2%）、函数定义与 `local`（0.2%）、`case ... esac`（0.1%）、
字符串操作 `${V^}` `${V,}`、`shift`、`getopts`、`umask`/`ulimit`。

### 3.4 明确不做（语料 0 次，遇到即 `ErrUnsupported`）

| 构造 | 处理 |
|---|---|
| `[[ ]]`、`=~` | 报错并提示改用 `[ ]` |
| `eval`、`trap` | 按普通命令处理 → `command not found`（退出码 127），与 `capability` §3.2 口径一致 |
| 反引号 `` `...` `` | 报错并提示改用 `$(...)` |
| `<<<` here-string | 报错并提示改用 `<<` |
| `((...))` 作语句 | 报错（`$(( ))` 支持） |
| `;;&`、`;&`、`select`、`coproc`、进程替换 `<()`/`>()` | 报错 |
| 数组/关联数组、`set -o` 系列、`shopt` | 报错 |
| 作业控制 `jobs`/`fg`/`bg` | `&` 为**真异步**、`wait`/`$!` 可用；**作业控制一律不做**（`jobs` 127、`kill %N` 报错、`fg`/`bg`/`disown` 无）；进程围栏见 `capability` §4.1 |
| PowerShell / cmd 语法 | 不解析、不翻译 |

## 4 内建命令清单

**只收 bash builtins**（D5）。

| 组 | 命令 | 阶段 |
|---|---|---|
| 必有 | `cd` `pwd` `echo` `printf` `test` `[` `true` `false` `:` `exit` `return` `export` `unset` `set` `shift` `read` | P0/P1 |
| 视需要 | `local` `declare` `readonly` `alias` `type` `command` `builtin` `umask` `hash` `times` `getopts` | P2 |

`echo` 与 `printf` 的行为差异（`-n`/`-e`、`echo` 的平台相关行为）由内建统一实现，**这是跨平台一致性的直接收益**：`echo -e "a\tb"` 在三个平台产出相同字节。

其余一切首词 → `exec` 透传（D2）。

## 5 跨平台归一（本项目的核心价值）

除语法解析外，虚拟层还负责把三平台的**非语法差异**吸收掉。这部分不在语料分析覆盖内，是新增目标。

### 5.1 路径

| 问题 | 现状 | 虚拟层处理 |
|---|---|---|
| 分隔符 | Windows 用 `\`，bash 用 `/` | 解释器内部一律 POSIX `/`；仅在 `exec` 边界转成宿主形式 |
| 盘符 | `C:\` vs `/c/` | 接受两者，内部归一为 POSIX 形式；`cd /d/x` 与 `cd d/x` 等价 |
| `~` 展开 | 各平台 HOME 含义不同 | 统一用注入的 `Home`（`tools/shell.Config.Home`），不读环境 |
| 转义 | `\` 在 bash 是转义符，在 Windows 是分隔符 | **一律按 bash 规则**：`\` 只作转义；字面反斜杠必须 `\\` 或单引号 |
| `..`/`.` 归一 | — | `filepath.Clean` 语义，但按 POSIX 规则 |
| 大小写敏感 | Windows 不敏感 | **保持宿主行为**，不做统一（假装敏感会引入新 bug）|

### 5.2 编码

现有基础：`ctty/consolecp_windows.go` 已有 `EnsureUTF8`/`RestoreUTF8`/`DecodeCP`，
`tools/shell` 的 `decodeStream` 已接 `platform.DecodeOutput`。虚拟层需要补的是：

| 问题 | 处理 |
|---|---|
| 外部程序输出编码 | 已有 `DecodeOutput` 兜底；虚拟层保证透传时**不二次转码** |
| heredoc 体传输 | **一律 UTF-8 字节流**写入管道/文件，绕开控制台代码页 |
| 参数编码 | 内建命令内部按 UTF-8 字符串处理；`exec` 边界由 `golang.org/x/sys` / `os/exec` 处理 |
| `printf` 的 `%s` 字节宽度与字符宽度 | 按 UTF-8 字符（与 bash 一致），不按字节 |
| CRLF | 输入命令文本先归一 `\r\n` → `\n`；输出不添 `\r` |
| NUL 字节 | 参数中遇 NUL 报错（bash 行为），文件内容不限制 |

### 5.3 其它

| 问题 | 处理 |
|---|---|
| 退出码 | 统一映射：127 未找到、126 不可执行、128+signum 被信号杀（Windows 无信号概念，按 POSIX 模拟值报告） |
| `$?` 范围 | 0–255 |
| 区分大小写命令名 | Windows 不敏感，保持宿主行为 |
| `/dev/null` | 内部识别，映射到 `os.DevNull`（Windows = `NUL`） |
| stdin 非 tty | 与现状一致：普通命令 stdin 为空设备；`interactive: true` 才借终端 |

### 5.4 分片落点（S6 实施口径）

平台差异全部收敛进 `vbash/platform.go` 的一张表 `platformOps`，三个白名单分片填值：
`platform_linux.go` / `platform_darwin.go` 取 `posixPlatform()`，`platform_windows.go` 自带实现，
其余平台落 `platform_stub.go`（`make cross` 拦漏标）。

| 表项 | posix | windows |
|---|---|---|
| `FromHost` | 恒等 | `C:\x` → `/c/x`、`\\server\share` 保留、`\` → `/` |
| `DriveOf` | 未设（`a/b` 就是目录） | `a/b` 当盘符仅当 `A:\` 真实存在 |
| `ToHost` | 恒等 | `/c/x` → `C:\x`、`//server/share` → `\\server\share` |
| `PathSep` / `DefaultPath` | `:` / POSIX 默认 | `;` / `/c/Windows/system32:/c/Windows`（内部形式） |
| `ExecSuffixes` | `""` | `.exe .cmd .bat .com ""`（已带后缀则不追加） |
| `IsExec` | mode&0111 | 非目录且后缀在清单内 |
| `MatchName` | 大小写敏感 | 折成小写（宿主行为） |
| `Argv0` | 恒等 | 带 `/` 时转宿主形式 |
| `ExitCode` / `SignalName` | `WaitStatus` 的 signal | 退出码取低 8 位、`0xC000013A`（Ctrl-C）记 130 |
| `DevNull` | `/dev/null` | `/dev/null`、`nul`（不区分大小写） |

内部变量表也按此归一：`HOME`/`PWD`/`OLDPWD` 存 POSIX 形式、`PATH` 内部一律 `:` 分隔，
`exec` 边界再转回宿主形式（`Env.Environ`）。

## 6 架构与接缝

```
run_shell(command)
   │
   ├─ vbash.Parse(command) ──► AST（list/pipeline/redirect/heredoc/compound）
   │      └─ 语法不支持 ──► ErrUnsupported（不执行，直接报错）
   │
   ├─ vbash.Run(ctx, AST, Env)
   │      ├─ 内建命令（§4）──► 进程内执行
   │      └─ 外部命令 ──► exec.Command（透传）
   │             └─ Console/Lease、超时、输出截断、信号处理 **全部沿用现有实现**
   │
   └─ Result（沿用 tools/shell.Result）
```

**关键约束**：

- `vbash` 是**纯库**，不依赖 `tools/shell`（避免反向依赖）；`tools/shell` 依赖 `vbash`
- 虚拟层失败（`ErrUnsupported` / 内部错误）**直接报错，不回落真 shell**：本特性是实验性质，
  不追求完备支持；静默回落会掩盖解释器缺陷、让"跨平台一致"的目标失去可观测性
- `interactive: true` **不走虚拟层**，直接交宿主 shell（终端所有权是 `ctty` 的事）
- 平台分片：`vbash/platform_linux.go` / `_darwin.go` / `_windows.go` + stub（D3）；
  差异表与归一规则见 §5.4

## 7 基准与文档的既存矛盾（必须先解决）

`docs/vbash.md` §6.1/§10 声明 `[[ ]]`/反引号/`case`/`((...))` 语料 **0 次、不支持**，
但 `vbash-bench` 中这些用例的 golden **要求执行成功**：

| 用例 | golden | 文档说法 |
|---|---|---|
| `T3/122-double-bracket` | exit 0, `stdout: exists` | 不支持 |
| `T3/113-case-basic`、`T3/114` | exit 0, 要求通过 | 不支持 |
| `X/027-backtick-not-used` | exit 0, `stdout: hi` | 不做 |
| `T3/123-arith-in-condition` | exit 0（含 `$(( ))`，这个是支持的） | — |

更关键的是**门槛算不出来**：`T3` 层 = heredoc 20 条 + 控制流 25 条 = 45 条，
门槛 90%。若控制流不做，上限只有 `20/45 = 44.4%`。
「T3≥90」与「控制流可不做」两条不能同时成立。

**解决方案（D8）**：

1. `run.py` 分层键改为按用例名前缀：`T3h/*`（heredoc）与 `T3c/*`（控制流）分别统计
2. 门槛表改为：`T0=100, T1=100, T2=95, T3h=90, T3c=可选, C=100, X=100`
3. §3.4 的构造用例（`[[ ]]`/反引号/`case`）标 `optional: true`，**只比「报错不崩」**，
   不计入必过集——即 golden 改为"不校验 stdout，只校验不挂起且退出码合理"

## 8 分步实施与验收门槛

| 步 | 内容 | 验收门槛 | 预估 |
|---|---|---|---|
| **S1** | 词法层：引号状态机、单词切分、注释、行续 | 单元测试覆盖 §3.1 引号项 | 小 |
| **S2** | 解析层：list/pipeline/redirect/heredoc → AST | 解析用例全通过（含 X 层畸形输入不崩） | 中 |
| **S3** | 执行层：内建最小集 + 外部透传 + 退出码 + 管道 | `T0=100, T1=100` | 中 |
| **S4** | heredoc 精确配对（栈式、多终止符、`<<-`） | `T3h≥90`、`C=100` | 中 |
| **S5** | 展开：变量、`${...}`、`$(...)`、`~`、glob、`$(( ))` | `T2≥95` | 大 |
| **S6** ✅ | 跨平台归一：路径、编码、`/dev/null`、信号退出码 | `X=100` | 中 |
| **S7** | 控制流：`for`/`if`/`( )`/`{ ; }`/`exit`/`set -e` | `T3c` 从 0 提到 ≥80% | 大 |
| **S8** ✅ | 接入 `tools/shell`：`Config.Vbash`（默认关闭）+ `Config.Environ`，进程内执行 | 全量 193 条 + 现有 Go 测试全绿 | 小 |

**一阶段完成状态**：S1–S8 全部完成（偏差见 `docs/vbash-impl.md` 各节）。
**验证状态**：Linux 侧由 `make vbash-check`（268/268）守护；darwin/windows 仅由 `make cross`
保证编译与 vet 通过，**未在真机跑过基准**——跨平台一致性目前是**实现完成、待验证**，
不应当被引用为已证实结论。
原始完成定义：S1–S6 完成，`T0=100, T1=100, T2≥95, T3h≥90, C=100, X=100`，
且 Linux/macOS/Windows 三平台同一命令产出相同 stdout/退出码。

## 9 风险

| 风险 | 影响 | 对策 |
|---|---|---|
| 范围膨胀成"复刻 coreutils" | 项目失控 | D2/D5 硬边界；内建清单白名单化 |
| Windows 无法自动验证 | 跨平台目标落空 | 基准可跨平台跑（`run.py` 已只依赖 python3）；先在 Linux 达标，Windows 补回归 |
| 解释器缺陷导致命令完全跑不了 | 用户体验倒退 | 默认关闭、配置显式开启（实验特性）；`ErrUnsupported` 与内部错误区分并带位置 |
| coreutils 在 Windows 上的行为差异 | 一致性仍不完美 | 明确边界：**虚拟层只保证语法与内建一致**，外部程序行为归其自身 |
| 语料单一使用者偏差 | 覆盖不到其它场景 | 基准 C 层可重抽（`gen_corpus_cases.py`）；门槛按层设，不做全局百分比 |
