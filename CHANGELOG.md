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
