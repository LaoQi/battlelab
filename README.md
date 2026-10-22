# battlelab

tanya 与 ivan 共用的 agent 核心库。纯 Go、无 GUI、无终端表现层。

```
battlelab/
  agent/   回合核心：LLM 双协议、回合循环、工具注册、会话存储与归档、交接、统计、图像
  tools/   标准工具集：tools.Standard 装配 run_shell / read_image / get_time·get_env·calc
  ctty/    控制终端原语：/dev/tty、termios、模式与光标、探测器、信号面（零内部依赖）
```

## 边界

- 只装**可被多个前台复用**的东西；表现层（终端视图、通知行为、REPL、行编辑）留在调用方
- 库内**不预设品牌**：全局说明路径、本地会话目录名、User-Agent 均由调用方注入
- 库内**不提供具体通知实现**：通知载荷与外部程序调用属于前台，由调用方实现
- 依赖极简：仅 `golang.org/x/sys`，不读 yaml、不读环境变量、无配置加载机制

调用方：`tanya`（终端前台）、`ivan`（常驻后台）。开发期用 `replace ../battlelab` 本地联调。

## 构建与测试

```bash
go build ./... && go vet ./...          # 构建 + 静态检查
go test ./... && go test -race ./...    # 全量测试 + 竞态
make cross                              # darwin/windows 的 vet + 测试编译
```
