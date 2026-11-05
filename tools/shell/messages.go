package shell

import (
	"errors"

	"github.com/LaoQi/battlelab/agent"
)

var ErrNoLend = errors.New(agent.MsgErrPrefix + "interactive 不支持（无法借出终端）")

const (
	MsgBadCwd              = agent.MsgErrPrefix + "cwd 不存在或不是目录: %s"
	MsgInterruptNotStarted = agent.MsgErrPrefix + "已中断（命令未执行）"
	MsgInterruptRunning    = agent.MsgErrPrefix + "已中断（进程已终止，输出可能不完整）"
	MsgTimedOut            = agent.MsgErrPrefix + "执行超时"
	MsgAbandoned           = agent.MsgErrPrefix + "超时后子进程仍未退出（可能处于不可中断状态），已放弃等待；其输出已截断"
	MsgErrLine             = agent.MsgErrPrefix + "%s"
	MsgNoShellFmt          = "未找到可用 shell（已尝试 %s），请安装或在配置中指定 shell:"
	MsgShellOverrideFmt    = "配置的 shell %q 不可执行，请检查 shell 配置"
	MsgNoOutput            = "(无输出，退出码 0)"
	MsgTruncMiddle         = "[%s 中间截断 %d 字节]"
	MsgTruncTail           = "[%s 截断 %d 字节]"
	MsgVbashDesc           = "命令由内置 bash 解释器执行（实验特性，不支持的构造直接报错、不回落系统 shell）。"
	MsgVbashEnvironFmt     = "启用内置 bash 解释器（vbash）需同时注入 Environ，否则外部命令拿不到环境变量"
)
