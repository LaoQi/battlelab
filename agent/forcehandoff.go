package agent

import (
	"context"
	"fmt"
	"strings"
)

// ForceHandoffOptions 是一次外部强制复位的参数。
type ForceHandoffOptions struct {
	// Instruction 是调用方给模型的摘要指令：写清要覆盖哪些内容、按什么结构写。
	// 必填——库不提供任何默认文案（复位是外部行为，摘要口径由调用方决定）。
	Instruction string
	// Continue 只作事实回填到 Handoff.Continue，库自己不续跑。
	Continue bool
}

// ForceHandoff 由外部调用方发起，对 Agent 的整个状态做一次功能复位：
//
//  1. 摘要请求：system 快照 + 当前会话历史（去掉工具轮）+ Instruction，
//     发一次不带工具定义的一次性请求。不写 history、不落盘、不计 Stats、
//     不触发任何请求切点钩子；失败则整次复位不成立、状态与文件零变化。
//  2. 旧会话落地：save 当前会话并追加一条移交事实行（append-only，不改写）。
//  3. 新会话成立：轮转到新会话，history = [摘要]，prompt/stats 复位，
//     上报 SessionEvent(SessionHandoff)，返回交接事实。
//
// 与模型自驱动的 next_session/RequestHandoff 不是一条路：那条在回合内置 pending、
// 由回合出口应用，本方法在回合外同步执行、调用即落地。
// 回合进行中（Ask/Continue 未返回）调用会被拒绝；归档只读会话同样拒绝。
func (a *Agent) ForceHandoff(ctx context.Context, opt ForceHandoffOptions) (Handoff, error) {
	if a.turning {
		return Handoff{}, fmt.Errorf("%s", MsgForceHandoffInTurn)
	}
	if _, ok := a.ArchiveReadOnly(); ok {
		return Handoff{}, ErrArchiveReadOnly
	}
	instr := strings.TrimSpace(opt.Instruction)
	if instr == "" {
		return Handoff{}, fmt.Errorf("%s", MsgForceHandoffNoInstr)
	}
	if len(a.history) == 0 {
		return Handoff{}, fmt.Errorf("%s", MsgForceHandoffEmptySession)
	}
	summary, err := a.summarizeForHandoff(ctx, instr)
	if err != nil {
		return Handoff{}, err
	}
	h, ok := a.settleHandoff(&pendingHandoff{summary: summary, cont: opt.Continue})
	if !ok {
		return h, fmt.Errorf("%s", h.Err)
	}
	return h, nil
}

// summarizeForHandoff 取一份交接摘要：一次性请求、零副作用。
func (a *Agent) summarizeForHandoff(ctx context.Context, instr string) (string, error) {
	msgs := make([]Message, 0, len(a.history)+2)
	msgs = append(msgs, Message{Role: "system", Content: a.prompt.system()})
	msgs = append(msgs, summaryMessages(a.history)...)
	msgs = append(msgs, Message{Role: "user", Content: instr})
	resp, err := a.client.Request(ctx, LLMRequest{Messages: msgs}, nil)
	if err != nil {
		return "", err
	}
	summary := normalizeHandoffSummary(resp.Content)
	if summary == "" {
		return "", fmt.Errorf("%s", MsgForceHandoffEmptySummary)
	}
	if err := checkSummaryLen(summary); err != nil {
		return "", err
	}
	return summary, nil
}

// summaryMessages 把会话历史整理成摘要请求可用的形态：
//
//   - 去掉工具轮：带 ToolCalls 的 assistant 与随后的 tool 消息整段省略，
//     换成一行占位（[已省略工具调用：run_shell ×2]），让摘要仍知道做过什么；
//   - 去掉图片：图片消息只留一张计数占位（[图片 2 张]）；
//   - 去掉思维链：一次性请求不带旧回合的推理内容。
//
// 不走预算截断：调用方语义是「全量历史」。
func summaryMessages(history []Message) []Message {
	out := make([]Message, 0, len(history))
	for i := 0; i < len(history); i++ {
		m := history[i]
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			names := toolRoundNames(m.ToolCalls)
			for i+1 < len(history) && history[i+1].Role == "tool" {
				i++
			}
			placeholder := fmt.Sprintf(MsgSummaryToolRoundFmt, names)
			if m.Content != "" {
				placeholder = m.Content + "\n\n" + placeholder
			}
			out = append(out, Message{Role: "assistant", Content: placeholder})
			continue
		}
		if m.Role == "tool" {
			continue // 无主的 tool 消息（异常中断的回合）一并省略
		}
		m.ToolCalls = nil
		m.ToolCallID = ""
		m.ReasoningContent = ""
		m.ReasoningItems = nil
		m.Usage = nil
		m.Duration = 0
		m.TS = 0
		if len(m.Images) > 0 {
			note := fmt.Sprintf(MsgSummaryHasImageFmt, len(m.Images))
			if m.Content != "" {
				m.Content += "\n" + note
			} else {
				m.Content = note
			}
			m.Images = nil
		}
		out = append(out, m)
	}
	return out
}

// toolRoundNames 汇总一轮工具调用的工具名与次数，如 "run_shell ×2, read_image ×1"。
func toolRoundNames(calls []ToolCall) string {
	order := make([]string, 0, len(calls))
	counts := make(map[string]int, len(calls))
	for _, tc := range calls {
		name := tc.Function.Name
		if name == "" {
			name = "unknown"
		}
		if _, ok := counts[name]; !ok {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, fmt.Sprintf("%s ×%d", name, counts[name]))
	}
	return strings.Join(parts, ", ")
}
