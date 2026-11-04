# 虚拟 bash：实施记录

> 配套 `docs/vbash-plan.md`（范围与决策）。本文只记**实施过程中的偏差与自定决策**，
> 按阶段（S1…S8）分节，每节记录：做了什么、与规划不一致处、为什么。

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
