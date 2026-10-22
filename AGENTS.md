# battlelab

tanya 与 ivan 共用的 agent 核心库（Go）。模块路径 `github.com/LaoQi/battlelab`。
本文件只记现行约束，细节见 `docs/`；变更记录一律写根目录 `CHANGELOG.md`（新条目置顶）。

## 设计约束

- **定位是库**：只收「多个前台都要用」的东西；表现层（REPL、行编辑、终端视图、通知行为）与终端所有权一律留在调用方
- **库内不预设品牌**：全局说明（AGENTS.md）路径、本地会话目录名（`Config.LocalDir`）、User-Agent 文本都由调用方注入；库不读 yaml、不读环境变量、无配置加载机制
- **库内不提供具体通知实现**：通知载荷与外部程序调用属于前台；库只在 `ctty` 提供终端原语（`Bell`/`NotifyOSC` 是写控制终端的原语，不是通知语义）
- 依赖极简：仅 `golang.org/x/sys`，新增依赖需先讨论
- `agent`/`tools`/`ctty` 之间：`agent` 零内部依赖，`tools → agent + ctty`，`ctty` 零内部依赖；`tools` 不依赖任何 `render/*`
- `agent` 的两条硬约束（沿用 tanya 口径）：事件扇出只有 `agent.Sinks`（装配期固定、同步串行、消费者不得阻塞/panic/反向调用）；会话文件 append-only + system 快照冻结是**优化手段**、非红线，不构成设计取舍依据
- 工具 = 调用方注入（`agent.WithTools` 保序在前）+ 自带 `next_session`/`agent_custom`（恒末两位）；装配期一次合成、运行期冻结，无运行期注册 API
- 平台分片一律白名单：`linux`/`darwin`/`windows` 各一个装配文件，posix 共享实现落 `linux || darwin`，其余平台 stub；`make cross` 逐包编译测试二进制拦漏标
- 测试包边界：需要上层包（如 `readline`）的集成用例放**调用方仓库**（tanya 的 `integration/`），不得在本库引入反向依赖
- 代码不添加注释，除非用户明确要求

## 结构

```
agent/   回合核心：Config 校验 / LLM 双协议（chat、responses）/ 回合循环 / prompt 组装（基座 + 全局说明 + 项目说明）/
         会话存储与归档 / 交接（next_session、Continue）/ 统计 / 工具注册与 agent_custom / 图像（缩放、裁剪、EXIF）
tools/   tools.Standard 装配标准集与顺序；shell/ = run_shell（借出终端经注入的 Console 窄接口）；image/ = read_image；
         builtin/ = get_time/get_env/calc
ctty/    控制终端原语：/dev/tty、termios、模式复位/光标锚点、探测器、信号面（SIGTERM/SIGHUP/SIGINT/SIGWINCH/SIGQUIT）
```

各模块行为细节见 `docs/`：

- `agent-control-tool.md` — `agent_custom` 契约（含 schema golden，测试守护）
- `ctty.md` — 控制终端统一抽象与平台收敛
- `shell-tool.md` — `run_shell` 组件化
- `agent-event-seams.md` — 事件词汇表与消费者接缝
- `session-archive.md` — 会话归档卷
- `session-handoff.md` — 会话交接（`/next` + `next_session`）
- `cache-probe.md` — prompt cache 实测与台阶
- `extraction.md` — 本文库的抽取边界与判据

这些文档 2026-10-22 随 `agent`/`tools`/`ctty` 从 tanya 迁入；文中 `tanya 的 …` 前缀指 tanya 仓库的同名文档。

## 构建与测试

```bash
go build ./... && go vet ./...          # 构建 + 静态检查
go test ./... && go test -race ./...    # 全量测试 + 竞态
make cross                              # darwin/windows 的 vet + 测试编译
```

调用方：`tanya`（终端前台）、`ivan`（常驻后台），均以 `replace ../battlelab` 本地联调。
