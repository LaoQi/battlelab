# battlelab

agent 核心库：回合循环、LLM 双协议（chat / responses）、标准工具集、终端原语。纯 Go、无 GUI、无终端表现层。

两个前台共用：

- [`tanya`](https://github.com/LaoQi/tanya)——终端前台（REPL、行编辑、渲染、实例接管）
- [`ivan`](https://github.com/LaoQi/ivan)——常驻后台（IM 通道、定时作业、无人值守）

## 架构

一核两前台，依赖严格单向；库不预设任何前台的存在：

```
                ┌───────────────────────────────────┐
                │        battlelab（回合核心库）        │
                │  agent / tools / ctty              │
                └────────────┬──────────────┬───────┘
          注入 Console、UA、 │              │ 不注入 Console
          说明路径、目录名    │              │（无终端场景）
                ┌────────────┴────┐  ┌──────┴────────┐
                │  tanya 终端前台   │  │  ivan 常驻后台  │
                │  readline/render │  │  bus/channel   │
                │  repl/attach     │  │  runner/sched  │
                └─────────────────┘  └───────────────┘
```

## 包与职责

| 包 | 职责 | 边界 |
|---|---|---|
| `agent` | 回合循环（Ask/Continue）、SSE 流式、工具注册（注入在前 + `next_session`/`agent_custom` 恒末两位）、prompt 组装（基座 + 全局说明 + 项目说明）、会话存储与归档、交接、统计、图像（缩放/裁剪/EXIF） | 零内部依赖；不感知终端、不读配置、回合串行由前台保证 |
| `tools` | `tools.Standard` 装配标准集：`run_shell`（终端经注入的 `Console` 窄接口借出）、`read_image`、`get_time`/`get_env`/`calc` | 只依赖 `agent` + `ctty`；不含任何 `render` |
| `ctty` | /dev/tty、termios、模式复位与光标锚点、探测、信号面（SIGTERM/SIGHUP/SIGINT/SIGWINCH） | 零内部依赖叶子；无业务语义，任何层可直接依赖 |

平台分片一律白名单：`linux`/`darwin`/`windows` 各一个装配文件，posix 共享实现落 `linux || darwin`，其余平台 stub。

## 数据管线

### 回合主流

```
输入 → Ask: history += user
  → [system 快照(冻结)] + history          ← 快照服务 prompt cache
  → ChatStream ─┬─ chat:      wire 化 → POST /chat/completions (SSE)
                └─ responses: item 化 → POST /responses (SSE)
  → 流式事件（reasoning / content / tool_call）→ sink 扇出 + 消息累积
  → history += assistant
  → 有 tool_calls → 逐个执行 → history += tool（+ 图片注记）→ 继续
  → 无 → save()（append-only jsonl）→ 回合结算（TurnEnd + 交接应用）
```

失败是显式状态机：无产出即回滚 user；产出一半则补错误事实行保历史；中断单列 `InterruptError`。

### 事件扇出

`agent.Sinks` 装配期合成、同步串行、顺序即因果；事件只装事实（派生量由消费侧现算）。tanya 用单 goroutine 合流渲染，ivan 转成出站流式消息——同一事件源，两种消费范式，库零改动。

### 会话存储

内存 history → 增量 append（写前 stat、失败截断回滚、绑定文件丢失即报错）→ 首行 system 快照冻结 → 增量扫描列表 → 归档（zip 卷 tmp+rename → 读回校验通过才删原文）→ 归档会话只读，续写走 Fork。

### 会话交接

`next_session` 工具只置 pending；回合正常结束时应用：rotate 新会话 + 摘要作首条 user + 旧会话补一行事实，`Continue` 不追加 user 续跑。可跨工作区，目标不可用则整次交接作废。

### 终端借出（有终端的前台）

`run_shell` 实例级串行 → interactive ? 借出 pty 直通（`LendFull`）: stdin 接空设备（`LendStdin`）→ 进程组托管、超时击杀有宽限 → 归还时锚点跟随。借不出**明确报错，不静默降级**。无终端的前台不注入 `Console`，interactive 恒报不可用。

### 图像

`read_image` 读盘 → 校验、按 detail 缩放、EXIF 换向裁剪 → base64 `ImageRef` 入 history → 发送层转 chat parts / responses `input_image`。

## 设计边界

- 只装**可被多个前台复用**的东西；表现层（终端视图、通知行为、REPL、行编辑）留在调用方
- 库内**不预设品牌**：全局说明路径、本地会话目录名、User-Agent 均由调用方注入
- 库内**不提供具体通知实现**：`Bell`/`NotifyOSC` 只是写终端的原语，通知语义归前台
- 依赖极简：仅 `golang.org/x/sys`；不读 yaml、不读环境变量、无配置加载机制
- 会话 append-only 与 system 快照冻结是**优化手段**（prompt cache），非红线
- 测试包边界：需要上层包的集成用例放调用方仓库

## 作为库使用

```go
cfg := &agent.Config{
    BaseURL: "https://api.example.com/v1", // 文档保留段，勿用真实端点
    Model:   "demo-model",
    UserAgent:  "myapp/1.0",   // 由调用方产出，库无默认值
    ApiProtocol: "chat",        // chat / responses
    DataDir:     "/var/lib/myapp",
    ConfigPath:  "/etc/myapp/config.yaml",
    SessionMode: "global",      // auto / local / global
    ArchiveThreshold: 64,
    ArchiveKeep:      16,
}
list, err := tools.Standard(tools.Options{
    Home:      home,                          // ~ 展开
    Workspace: func() string { return cwd },  // run_shell 默认目录，活取
    // Console: 不注入 = 无终端模式
})
a, err := agent.New(cfg,
    agent.WithTools(list...),                          // 保序在前
    agent.WithGlobalAgentsPath(globalAgents),          // 空串 = 不读全局说明
)
err = a.Ask(ctx, "总结当前目录", func(e agent.Event) { /* 事件消费 */ })
```

开发期联调：调用方 `go.mod` 写 `replace github.com/LaoQi/battlelab => ../battlelab`。

## 构建与测试

```bash
go build ./... && go vet ./...          # 构建 + 静态检查
go test ./... && go test -race ./...    # 全量测试 + 竞态
make cross                              # darwin/windows 的 vet + 测试编译
```

## 文档

| 文档 | 内容 |
|---|---|
| [docs/extraction.md](docs/extraction.md) | 抽取边界与判据（什么进库、什么留前台） |
| [docs/shell-tool.md](docs/shell-tool.md) | run_shell 组件化设计与终端借出 |
| [docs/agent-control-tool.md](docs/agent-control-tool.md) | `agent_custom` 契约（key/action 表驱动） |
| [docs/agent-event-seams.md](docs/agent-event-seams.md) | 事件词汇表与消费者接缝 |
| [docs/session-archive.md](docs/session-archive.md) | 会话归档卷 |
| [docs/session-handoff.md](docs/session-handoff.md) | 会话交接（`/next` + `next_session`） |
| [docs/ctty.md](docs/ctty.md) | 控制终端统一抽象与平台收敛 |
| [docs/cache-probe.md](docs/cache-probe.md) | prompt cache 实测与台阶 |

变更记录见 [CHANGELOG.md](CHANGELOG.md)。
