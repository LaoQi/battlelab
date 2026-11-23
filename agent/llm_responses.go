package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type responsesRequest struct {
	Model        string              `json:"model"`
	Instructions string              `json:"instructions,omitempty"`
	Input        []any               `json:"input"`
	Temperature  *float64            `json:"temperature,omitempty"`
	Reasoning    *responsesReasoning `json:"reasoning,omitempty"`
	Tools        []responsesTool     `json:"tools,omitempty"`
	Stream       bool                `json:"stream"`
	Store        bool                `json:"store"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type responsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesImagePart struct {
	Type     string `json:"type"`
	ImageURL string `json:"image_url"`
	Detail   string `json:"detail,omitempty"`
}

func responsesUserContent(m Message, detail string) []any {
	if !hasImages(m.Images) {
		return []any{responsesContentPart{Type: "input_text", Text: m.Content}}
	}
	parts := make([]any, 0, len(m.Images)+1)
	if m.Content != "" {
		parts = append(parts, responsesContentPart{Type: "input_text", Text: m.Content})
	}
	for _, img := range m.Images {
		url := img.URLValue()
		if url == "" {
			continue
		}
		parts = append(parts, responsesImagePart{Type: "input_image", ImageURL: url, Detail: img.DetailValue(detail)})
	}
	return parts
}

func buildResponsesInput(messages []Message, detail string) (string, []any) {
	var instructions string
	rest := messages
	if len(messages) > 0 && messages[0].Role == "system" {
		instructions = messages[0].Content
		rest = messages[1:]
	}
	items := make([]any, 0, len(rest))
	for _, m := range rest {
		switch m.Role {
		case "user":
			items = append(items, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": responsesUserContent(m, detail),
			})
		case "assistant":
			for _, r := range m.ReasoningItems {
				if r.Content == "" {
					continue
				}
				items = append(items, responsesReasoningItem(r))
			}
			if m.Content != "" {
				items = append(items, map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []responsesContentPart{
						{Type: "output_text", Text: m.Content},
					},
				})
			}
			for _, tc := range m.ToolCalls {
				items = append(items, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}
		case "tool":
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		}
	}
	return instructions, items
}

// responsesReasoningItem 构造回传用的 reasoning item，形态与供应商无关：正文双写
// （content[].reasoning_text 与 summary[].summary_text 各一份同文）、上游 id 原样带回、
// 不带 status。依据与实测见 docs/reasoning-replay.md：智谱 InputReasoning 只认 content，
// opencode 线要求 summary 键恒在且为数组，Kimi 两侧都收且 content 优先；各家只读其一，
// 双写不双计费。id 实测全被忽略且零成本，带着以备接要求原样回放的线；status 只是
// 输出 item 的生成状态位，回传没有信息量。
func responsesReasoningItem(r ReasoningItem) map[string]any {
	item := map[string]any{
		"type": "reasoning",
		"summary": []responsesContentPart{
			{Type: "summary_text", Text: r.Content},
		},
		"content": []responsesContentPart{
			{Type: "reasoning_text", Text: r.Content},
		},
	}
	if r.ID != "" {
		item["id"] = r.ID
	}
	return item
}

func responsesTools(defs []ToolDef) []responsesTool {
	tools := make([]responsesTool, 0, len(defs))
	for _, d := range defs {
		tools = append(tools, responsesTool{
			Type:        "function",
			Name:        d.Function.Name,
			Description: d.Function.Description,
			Parameters:  d.Function.Parameters,
		})
	}
	return tools
}

type responsesEvent struct {
	Type string `json:"type"`
}

type responsesTextDelta struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

type responsesContentText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesOutputItem struct {
	Type      string                 `json:"type"`
	ID        string                 `json:"id"`
	CallID    string                 `json:"call_id"`
	Name      string                 `json:"name"`
	Arguments string                 `json:"arguments"`
	Content   []responsesContentText `json:"content"`
	// Summary 是 reasoning item 的摘要形态正文。真实上游分成两派：GLM / DeepSeek /
	// LongCat 把正文放在 content[].reasoning_text，Kimi / 火山方舟 / opencode 放在
	// summary[].summary_text。两者可能同时出现（另一侧为空数组），故都要解析。
	Summary []responsesContentText `json:"summary"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type responsesCompleted struct {
	Type     string `json:"type"`
	Response struct {
		Output []responsesOutputItem `json:"output"`
		Usage  *responsesUsage       `json:"usage"`
		Error  *responsesError       `json:"error"`
		Status string                `json:"status"`
	} `json:"response"`
}

type responsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (c *Client) responsesStream(ctx context.Context, messages []Message, sink EventSink) (*Message, error) {
	instructions, input := buildResponsesInput(messages, c.cfg.ImageDetail)
	msg := &Message{Role: "assistant"}
	var usage *Usage
	var hasDelta bool
	start := time.Now()
	// 思维链 delta 的本地累积，仅作终态缺失时的兜底（见流结束处）。正常情况下
	// 正文以 response.completed 的终态 reasoning item 为准，这里不参与构造。
	// 两形态分桶：同一家上游通常只发一派，但混发时按「明文优先」择用，与终态解析
	// （reasoningItemText）同序——合并成一桶会把两派正文拼成一段、形态也标错。
	var reasonPlain, reasonSummary strings.Builder

	reasonOpen := false
	endReason := func() {
		if reasonOpen {
			reasonOpen = false
			sink.Emit(Event{Kind: EventReasoningEnd})
		}
	}

	err := c.streamSSE(ctx, "/responses", responsesRequest{
		Model:        c.cfg.Model,
		Instructions: instructions,
		Input:        input,
		Temperature:  temperatureParam(c.cfg),
		Reasoning:    reasoningParam(c.cfg.ReasoningEffort),
		Tools:        responsesTools(c.tools),
		Stream:       true,
	}, func(data string) error {
		var ev responsesEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			var d responsesTextDelta
			if json.Unmarshal([]byte(data), &d) != nil || d.Delta == "" {
				return nil
			}
			reasonOpen = true
			if strings.Contains(ev.Type, "summary") {
				reasonSummary.WriteString(d.Delta)
			} else {
				reasonPlain.WriteString(d.Delta)
			}
			sink.Emit(Event{Kind: EventReasoning, Text: d.Delta})
		case "response.reasoning_text.done", "response.reasoning_summary_text.done":
			endReason()
		case "response.output_item.done":
			var d struct {
				Item struct {
					Type string `json:"type"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(data), &d) == nil && d.Item.Type == "reasoning" {
				endReason()
			}
		case "response.function_call_arguments.delta":
			var d struct {
				ItemID string `json:"item_id"`
				Delta  string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &d) != nil || d.Delta == "" {
				return nil
			}
			endReason()
			sink.Emit(Event{Kind: EventToolCall, ToolID: d.ItemID, ToolArgs: d.Delta})
		case "response.output_text.delta":
			var d responsesTextDelta
			if json.Unmarshal([]byte(data), &d) != nil || d.Delta == "" {
				return nil
			}
			endReason()
			hasDelta = true
			msg.Content += d.Delta
			sink.Emit(Event{Kind: EventContent, Text: d.Delta})
		case "response.completed", "response.incomplete":
			var cc responsesCompleted
			if json.Unmarshal([]byte(data), &cc) != nil {
				return nil
			}
			applyCompletedOutput(msg, &cc.Response.Output, hasDelta)
			if cc.Response.Usage != nil {
				usage = usageFromResponses(cc.Response.Usage)
			}
			if ev.Type == "response.completed" && cc.Response.Error != nil {
				return &semanticError{fmt.Errorf(MsgRespFailed, cc.Response.Error.Message)}
			}
		case "response.failed":
			var cc responsesCompleted
			if json.Unmarshal([]byte(data), &cc) == nil && cc.Response.Error != nil {
				return &semanticError{fmt.Errorf(MsgRespFailed, cc.Response.Error.Message)}
			}
			return &semanticError{fmt.Errorf(MsgRespFailed, "response.failed")}
		case "error":
			var ee struct {
				Error *responsesError `json:"error"`
			}
			if json.Unmarshal([]byte(data), &ee) == nil && ee.Error != nil {
				return &semanticError{fmt.Errorf(MsgRespFailed, ee.Error.Message)}
			}
		}
		return nil
	})
	if err != nil {
		var he *httpError
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			return nil, fmt.Errorf(MsgAPIStatus+"（%s）", he.Status, he.Body, MsgRespHint404)
		}
		return nil, err
	}
	endReason()
	// 兜底：终态没给 reasoning item（个别上游只发 delta 不发终态）时，用累积文本补一条，
	// 否则这次思考既不上屏也没落盘。已有终态时以终态为准，不重复添加。两派 delta 混发
	// 时取明文桶（比摘要完整），只有摘要桶非空才用摘要。
	if len(msg.ReasoningItems) == 0 {
		switch {
		case reasonPlain.Len() > 0:
			msg.ReasoningItems = append(msg.ReasoningItems, ReasoningItem{Content: reasonPlain.String()})
		case reasonSummary.Len() > 0:
			msg.ReasoningItems = append(msg.ReasoningItems, ReasoningItem{Content: reasonSummary.String()})
		}
	}
	msg.Usage = usage
	msg.Duration = time.Since(start)
	return msg, nil
}

func applyCompletedOutput(msg *Message, output *[]responsesOutputItem, hasDelta bool) {
	for _, item := range *output {
		switch item.Type {
		case "message":
			if hasDelta {
				continue
			}
			for _, part := range item.Content {
				if part.Type == "output_text" {
					msg.Content += part.Text
				}
			}
		case "function_call":
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:   item.CallID,
				Type: "function",
			})
			tc := &msg.ToolCalls[len(msg.ToolCalls)-1]
			tc.Function.Name = item.Name
			tc.Function.Arguments = item.Arguments
		case "reasoning":
			msg.ReasoningItems = append(msg.ReasoningItems, ReasoningItem{
				ID:      item.ID,
				Content: reasoningItemText(item.Content, item.Summary),
			})
		}
	}
}

// reasoningItemText 从 reasoning item 的两种下发形态里取正文。
// 明文 reasoning_text 优先：它内容更完整（摘要只是模型的自我总结）。某条流的
// reasoning_text 缺失或为空时回落 summary_text——反过来不成立，摘要派上游
// 根本不下发明文。两个都没内容时返回空串，调用方按「无思维链」处理。
func reasoningItemText(content, summary []responsesContentText) string {
	if text := joinReasoningParts(content, "reasoning_text"); text != "" {
		return text
	}
	return joinReasoningParts(summary, "summary_text")
}

func joinReasoningParts(parts []responsesContentText, want string) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == want {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func usageFromResponses(u *responsesUsage) *Usage {
	out := &Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.InputTokensDetails != nil {
		out.PromptTokensDetails = &promptTokensDetails{CachedTokens: u.InputTokensDetails.CachedTokens}
	}
	if u.OutputTokensDetails != nil {
		out.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	return out
}

func reasoningParam(effort string) *responsesReasoning {
	if effort == "" {
		return nil
	}
	return &responsesReasoning{Effort: effort}
}
