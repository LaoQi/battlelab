# 抽取边界（tanya / ivan → battlelab）

> 状态：**已实施**（2026-10-22）。本文记录判据、边界与遗留，供后续加包/回搬时对照。

## 1 判据

抽库只认三条，全部满足才搬：

1. **两个消费者**：tanya 与 ivan 都实际用到（"将来可能用"不算，先留在原仓）
2. **零品牌**：库内不预设产品名、机房路径、环境变量前缀；需要区分的项一律由调用方注入
3. **无具体实现泄漏**：库只提供能力与契约，不替调用方决定 UI / 通知 / 终端行为

## 2 结论

| tanya 包 | 消费者 | 结论 |
|---|---|---|
| `agent/` | tanya + ivan | **迁入** battlelab（回合核心） |
| `tools/`（`shell`/`image`/`builtin`） | tanya + ivan | **迁入** battlelab |
| `ctty/` | tanya + ivan | **迁入** battlelab（终端原语） |
| `agent/init.go` | 仅 tanya | 留 tanya → `workspace/` |
| `tools/shell/notify.go` | 仅 tanya | 留 tanya → `notify/`（载荷 `Notification` 也在此） |
| `tools/shell/view/` | 仅 tanya | 留 tanya → `views/shell/` |
| `tools/shell/tty_e2e_test.go` | 仅 tanya（依赖 `readline`） | 留 tanya → `integration/` |
| `attach/` | 仅 tanya | 留 tanya（终端实例接管；ivan 自带走 socket 通道，语义不同） |
| `config/` | 仅 tanya | 留 tanya（ivan 自实现 loader） |
| `readline/`、`repl/` | 仅 tanya | 留 tanya（终端前台） |
| `render/`（style·ir·theme·markdown·markup·present） | 仅 tanya | 留 tanya（表现层；`markdown` 是 ivan 的"后置按需"项，等第二消费者再议） |
| ivan 的 `bus`/`channel`/`qqbot`/`schedule`/`data`/`prompt` | 仅 ivan | 留 ivan（无第二消费者；`qqbot` 走独立 module 路线，与 agent 核心库不同批次） |

## 3 三个耦合点的处理

- **通知**：`Notification` 载荷与外部程序通知**都不进库**（用户裁定：库不提供具体实现），整体落 tanya 的 `notify` 包；`tools/shell` 因此不再依赖任何 `render/*`，库的依赖图收敛为 `tools → agent + ctty`
- **工作区脚手架**：`agent/init.go` 写的是 tanya 品牌目录与文案，不是 agent 核心能力 → 落 tanya 的 `workspace` 包；目录名读 `agent.Config.LocalDir`（tanya 填 `.tanya`），会话落点经新增的 `agent.SessionDirFor` 公开
- **品牌默认值**：
  - 全局说明路径：库内取消默认（空 = 不读），tanya 经 `agent.WithGlobalAgentsPath(config.GlobalAgentsPath())` 注入；**漏注入会让用户全局 AGENTS.md 静默失效**，本仓无默认值即是有意为之
  - 本地会话目录名：`Config.LocalDir`（`yaml:"-"`，不改变 `config.example.yaml` 与 `tanya config` 输出）；`session_mode=local` 且为空时 `Validate` 报错，避免静默落到 `data_dir`
  - User-Agent：`agent.UserAgent*` 常量删除，文本由调用方 `config.UserAgent(version)` 产出
  - 文案：`MsgAPIKey`/`MsgShellOverrideFmt` 去掉 `TANYA_*` 环境变量名（文档承担提示职责）

## 4 工序与验收

按叶子到根的次序搬，每步都跑两边全量测试，任一步不过就停：

1. `ctty`（零依赖叶子，当试验田）
2. `agent`（含上述去品牌改造）
3. `tools`（含通知/视图/集成用例归位）

每步验收：battlelab `go build/vet/test ./...` + `make cross`；tanya `go build/vet/test ./...` + `make cross`；ivan `go build/vet/test ./...`。

## 5 遗留

- ~~**文档位置**~~（2026-10-22 已处理）：`agent-control-tool.md`、`ctty.md`、`shell-tool.md`、`agent-event-seams.md`、`session-archive.md`、`session-handoff.md`、`cache-probe.md` 全部迁入本仓 `docs/`；迁移时给每篇加了归属说明，并把指向 tanya 仓文档/文件的引用改写成 `tanya 的 …` 前缀。tanya 侧对应引用改为 `battlelab 的 docs/…`，`design.md`、`multimodal.md` 两篇混合文档加归属注
- **未来候选**（出现第二消费者再说）：`render/markdown`（终端 markdown 渲染）、`render/term`+`present`（若库侧工具需要自带视图契约）、`attach`（若 ivan 需要多实例接管）、`bus`/`channel`/`schedule`（若 tanya 也走通道抽象）
