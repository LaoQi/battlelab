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
