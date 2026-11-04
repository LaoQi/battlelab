# 虚拟 bash：实施记录

> 配套 `docs/vbash-plan.md`（范围与决策）。本文只记**实施过程中的偏差与自定决策**，
> 按阶段（S1…S8）分节，每节记录：做了什么、与规划不一致处、为什么。

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
