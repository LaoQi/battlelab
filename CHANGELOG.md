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
