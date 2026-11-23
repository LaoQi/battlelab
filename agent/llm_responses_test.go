package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func responsesLLM(t *testing.T, steps ...mockStep) (*mockLLM, *Config) {
	t.Helper()
	m := newMockLLM(t, steps...)
	cfg := m.config()
	cfg.ApiProtocol = "responses"
	return m, cfg
}

func TestResponsesContentAndUsage(t *testing.T) {
	m, cfg := responsesLLM(t, mockStep{
		content:   "你好，世界",
		reasoning: "enc-abc",
		usage: &Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
			ReasoningTokens:  30,
		},
	})
	cfg.ReasoningEffort = "high"
	var sb strings.Builder
	msg, err := NewClient(cfg, testToolDefs()).ChatStream(context.Background(),
		[]Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}},
		EventSink(func(e Event) {
			if e.Kind == EventContent {
				sb.WriteString(e.Text)
			}
		}))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "你好，世界" {
		t.Errorf("content: %q", msg.Content)
	}
	if sb.String() != "你好，世界" {
		t.Errorf("onDelta 累积: %q", sb.String())
	}
	if len(msg.ReasoningItems) != 1 {
		t.Fatalf("reasoning items 数: %d", len(msg.ReasoningItems))
	}
	if msg.ReasoningItems[0].ID != "rs_test" || msg.ReasoningItems[0].Content != "enc-abc" {
		t.Errorf("reasoning item: %+v", msg.ReasoningItems[0])
	}
	if msg.Usage == nil || msg.Usage.PromptTokens != 100 || msg.Usage.TotalTokens != 150 {
		t.Fatalf("usage: %+v", msg.Usage)
	}
	if msg.Usage.CacheHit() != 0 {
		t.Errorf("无缓存时 CacheHit 应为 0: %d", msg.Usage.CacheHit())
	}
	if msg.Usage.ReasoningTokens != 30 {
		t.Errorf("reasoning tokens: %d", msg.Usage.ReasoningTokens)
	}

	req := m.rawReqs[0]
	if v, ok := req["store"].(bool); !ok || v {
		t.Errorf("store 应为 false: %v", req["store"])
	}
	if v, ok := req["instructions"].(string); !ok || v != "sys" {
		t.Errorf("instructions: %v", req["instructions"])
	}
	if v, ok := req["reasoning"].(map[string]any); !ok || v["effort"] != "high" {
		t.Errorf("reasoning.effort: %v", req["reasoning"])
	}
	if _, ok := req["include"]; ok {
		t.Errorf("请求不应携带 include 字段: %v", req["include"])
	}
	input, _ := req["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input 数: %d", len(input))
	}
	first := input[0].(map[string]any)
	if first["type"] != "message" || first["role"] != "user" {
		t.Errorf("input[0]: %v", first)
	}
	tools, _ := req["tools"].([]any)
	if len(tools) != len(testToolDefs()) {
		t.Fatalf("工具数: %d", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "t1" {
		t.Errorf("扁平工具格式: %v", tool)
	}
	if _, nested := tool["function"]; nested {
		t.Error("工具不应有嵌套 function 字段")
	}
}

func TestResponsesToolLoopReasoningReplay(t *testing.T) {
	m, cfg := responsesLLM(t,
		mockStep{
			reasoning: "enc-1",
			toolCalls: []mockToolCall{{id: "call_1", name: "calc", args: `{"expression":"1+2"}`}},
		},
		mockStep{content: "结果是 3"},
	)
	c := NewClient(cfg, nil)
	ctx := context.Background()

	first, err := c.ChatStream(ctx, []Message{{Role: "user", Content: "算 1+2"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 {
		t.Fatalf("tool_calls 数: %d", len(first.ToolCalls))
	}
	if first.ToolCalls[0].ID != "call_1" || first.ToolCalls[0].Function.Arguments != `{"expression":"1+2"}` {
		t.Errorf("tool_call: %+v", first.ToolCalls[0])
	}

	history := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "算 1+2"},
		*first,
		{Role: "tool", ToolCallID: "call_1", Name: "calc", Content: "3"},
	}
	second, err := c.ChatStream(ctx, history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "结果是 3" {
		t.Errorf("second content: %q", second.Content)
	}

	if len(m.rawReqs) != 2 {
		t.Fatalf("请求数: %d", len(m.rawReqs))
	}
	input, _ := m.rawReqs[1]["input"].([]any)
	var kinds []string
	for _, it := range input {
		kinds = append(kinds, it.(map[string]any)["type"].(string))
	}
	want := []string{"message", "reasoning", "function_call", "function_call_output"}
	if len(kinds) != len(want) {
		t.Fatalf("input items: %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("input 顺序: got %v want %v", kinds, want)
		}
	}
	assertReplayReasoningItem(t, input[1].(map[string]any), "rs_test", "enc-1")
	fc := input[2].(map[string]any)
	if fc["call_id"] != "call_1" || fc["name"] != "calc" {
		t.Errorf("function_call 回传: %v", fc)
	}
	out := input[3].(map[string]any)
	if out["call_id"] != "call_1" || out["output"] != "3" {
		t.Errorf("function_call_output 回传: %v", out)
	}
}

func TestResponsesEffortOmitsTemperature(t *testing.T) {
	m, cfg := responsesLLM(t, mockStep{content: "ok"}, mockStep{content: "ok"})
	cfg.ReasoningEffort = "high"
	if _, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.rawReqs[0]["temperature"]; ok {
		t.Errorf("设置 reasoning_effort 时不应发送 temperature: %v", m.rawReqs[0])
	}

	cfg.ReasoningEffort = ""
	if _, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.rawReqs[1]["temperature"]; !ok {
		t.Errorf("未设置时应发送 temperature: %v", m.rawReqs[1])
	}
}

func TestResponsesToolCallOnlyRecordsDuration(t *testing.T) {
	_, cfg := responsesLLM(t, mockStep{
		toolCalls: []mockToolCall{{id: "call_1", name: "calc", args: `{"expression":"1+2"}`}},
	})
	var kinds []EventKind
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, func(e Event) { kinds = append(kinds, e.Kind) })
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "" || len(msg.ToolCalls) != 1 {
		t.Fatalf("应为纯 tool_call 响应: %+v", msg)
	}
	if msg.Duration <= 0 {
		t.Errorf("纯 tool_call 响应也应记录请求耗时: %v", msg.Duration)
	}
	if len(kinds) == 0 || kinds[0] != EventToolCall {
		t.Errorf("纯 tool_call 响应也应产出事件（渲染侧据此记 TTFT）: %v", kinds)
	}
}

func TestResponsesCompletedMultiMessageItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"甲"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"乙"}]}]}}`+"\n\n")
	}))
	defer srv.Close()
	cfg := defaultConfig()
	cfg.BaseURL = srv.URL
	cfg.APIKey = "test-key"
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "甲乙" {
		t.Errorf("多 message item 应拼接: %q", msg.Content)
	}
}

func TestResponsesCompletedSkipsMessageWhenDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"流式"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"终态"},{"type":"output_text","text":"拼接"}]}]}}`+"\n\n")
	}))
	defer srv.Close()
	cfg := defaultConfig()
	cfg.BaseURL = srv.URL
	cfg.APIKey = "test-key"
	var sb strings.Builder
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, EventSink(func(e Event) {
			if e.Kind == EventContent {
				sb.WriteString(e.Text)
			}
		}))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "流式" {
		t.Errorf("收到 delta 后应跳过 completed 的 message 拼接: %q", msg.Content)
	}
	if sb.String() != "流式" {
		t.Errorf("onDelta 累积: %q", sb.String())
	}
}

func TestResponsesNoEffortNoReasoningField(t *testing.T) {
	m, cfg := responsesLLM(t, mockStep{content: "ok"})
	cfg.ReasoningEffort = ""
	if _, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.rawReqs[0]["reasoning"]; ok {
		t.Error("未设置 effort 时不应携带 reasoning 字段")
	}
}

func TestResponsesNoReasoningInReplayWhenAbsent(t *testing.T) {
	m, cfg := responsesLLM(t, mockStep{content: "ok"})
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	isolatePromptEnv(t)
	a.history = []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "ok"},
	}
	if _, err := NewClient(cfg, nil).ChatStream(context.Background(), a.buildMessages(), nil); err != nil {
		t.Fatal(err)
	}
	input, _ := m.rawReqs[0]["input"].([]any)
	for _, it := range input {
		if it.(map[string]any)["type"] == "reasoning" {
			t.Fatal("无 reasoning 历史不应回传 reasoning item")
		}
	}
}

func TestResponsesSkipEmptyContentReasoningInReplay(t *testing.T) {
	m, cfg := responsesLLM(t, mockStep{content: "ok"})
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	isolatePromptEnv(t)
	a.history = []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "ok", ReasoningItems: []ReasoningItem{
			{ID: "rs_1", Content: ""},
			{ID: "rs_2", Content: "有的推理"},
		}},
	}
	if _, err := NewClient(cfg, nil).ChatStream(context.Background(), a.buildMessages(), nil); err != nil {
		t.Fatal(err)
	}
	input, _ := m.rawReqs[0]["input"].([]any)
	var reasoning []map[string]any
	for _, it := range input {
		if it.(map[string]any)["type"] == "reasoning" {
			reasoning = append(reasoning, it.(map[string]any))
		}
	}
	if len(reasoning) != 1 {
		t.Fatalf("应仅回传非空 content 的 reasoning: %d", len(reasoning))
	}
	if reasoning[0]["id"] != "rs_2" {
		t.Errorf("回传的应是 rs_2: %v", reasoning[0])
	}
}

func TestResponsesUsageCacheHit(t *testing.T) {
	u := usageFromResponses(&responsesUsage{
		InputTokens:  200,
		OutputTokens: 10,
		TotalTokens:  210,
		InputTokensDetails: &struct {
			CachedTokens int `json:"cached_tokens"`
		}{CachedTokens: 150},
	})
	if u.PromptTokens != 200 || u.CompletionTokens != 10 || u.TotalTokens != 210 {
		t.Errorf("usage 映射: %+v", u)
	}
	if u.CacheHit() != 150 {
		t.Errorf("cached_tokens 映射: %d", u.CacheHit())
	}
}

func TestResponsesFailedEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"x\",\"message\":\"boom\"}}}\n\n")
	}))
	defer srv.Close()
	cfg := defaultConfig()
	cfg.BaseURL = srv.URL
	cfg.APIKey = "test-key"
	_, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("应返回 failed 事件错误: %v", err)
	}
}

func TestResponsesErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"y\",\"message\":\"bad\"}}\n\n")
	}))
	defer srv.Close()
	cfg := defaultConfig()
	cfg.BaseURL = srv.URL
	cfg.APIKey = "test-key"
	_, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Errorf("应返回 error 事件: %v", err)
	}
}

func TestResponses404Hint(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	cfg := defaultConfig()
	cfg.BaseURL = srv.URL
	cfg.APIKey = "test-key"
	_, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), MsgRespHint404) {
		t.Errorf("404 应附带协议提示: %v", err)
	}
}

func TestResponsesAgentLoopSessionReasoning(t *testing.T) {
	m, cfg := responsesLLM(t,
		mockStep{
			reasoning: "enc-loop",
			toolCalls: []mockToolCall{{id: "call_9", name: "calc", args: `{"expression":"2*3"}`}},
		},
		mockStep{content: "6"},
	)
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	isolatePromptEnv(t)
	a.NewSession()
	if err := a.Ask(context.Background(), "算 2*3", nil); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range readLines(t, a.store.path()) {
		var msg Message
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		if msg.Role == "assistant" && len(msg.ReasoningItems) > 0 {
			found = true
			if msg.ReasoningItems[0].Content != "enc-loop" {
				t.Errorf("会话中 reasoning: %+v", msg.ReasoningItems[0])
			}
		}
	}
	if !found {
		t.Error("会话文件应保存 reasoning_items")
	}
	if len(m.rawReqs) != 2 {
		t.Fatalf("请求数: %d", len(m.rawReqs))
	}
	input, _ := m.rawReqs[1]["input"].([]any)
	last := input[len(input)-1].(map[string]any)
	if last["type"] != "function_call_output" {
		t.Errorf("末位 item: %v", last)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestResponsesReasoningDeltaEvents(t *testing.T) {
	_, cfg := responsesLLM(t, mockStep{reasoning: "推理过程", content: "结论"})
	var reasoning, content strings.Builder
	sink := EventSink(func(e Event) {
		switch e.Kind {
		case EventReasoning:
			reasoning.WriteString(e.Text)
		case EventContent:
			content.WriteString(e.Text)
		}
	})
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if reasoning.String() != "推理过程" {
		t.Errorf("思维链事件: %q", reasoning.String())
	}
	if content.String() != "结论" {
		t.Errorf("正文事件: %q", content.String())
	}
	if msg.Content != "结论" {
		t.Errorf("思维链不应污染正文: %q", msg.Content)
	}
	if len(msg.ReasoningItems) != 1 || msg.ReasoningItems[0].Content != "推理过程" {
		t.Errorf("completed 仍应捕获思维链: %+v", msg.ReasoningItems)
	}
	if msg.Duration <= 0 {
		t.Errorf("应记录请求耗时: %v", msg.Duration)
	}
}

func TestResponsesToolCallDeltaEvents(t *testing.T) {
	_, cfg := responsesLLM(t,
		mockStep{toolCalls: []mockToolCall{{id: "call_1", name: "run_shell", args: `{"command":"echo hi"}`}}},
		mockStep{content: "done"},
	)
	var args strings.Builder
	sink := EventSink(func(e Event) {
		if e.Kind == EventToolCall {
			args.WriteString(e.ToolArgs)
		}
	})
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if args.String() != `{"command":"echo hi"}` {
		t.Errorf("工具参数增量事件: %q", args.String())
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Arguments != `{"command":"echo hi"}` {
		t.Errorf("终态仍应从 completed 提取 tool_calls: %+v", msg.ToolCalls)
	}
}

func TestResponsesErrorKeepsPartialTurn(t *testing.T) {
	m, cfg := responsesLLM(t,
		mockStep{toolCalls: []mockToolCall{{id: "call_1", name: "calc", args: `{"expression":"2*3"}`}}},
		mockStep{status: 500},
		mockStep{content: "继续"},
	)
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Ask(context.Background(), "算 2*3", nil); err == nil {
		t.Fatal("第一轮应返回错误")
	}
	if len(a.history) != 4 {
		t.Fatalf("应保留 user+assistant+tool+错误提示 共4条: %d", len(a.history))
	}
	if last := a.history[3]; last.Role != "user" || !strings.HasPrefix(last.Content, "[本轮因错误中止") {
		t.Fatalf("末条应为错误提示: %+v", last)
	}
	if err := a.Ask(context.Background(), "继续", nil); err != nil {
		t.Fatal(err)
	}
	if len(m.rawReqs) != 3 {
		t.Fatalf("应有 3 次请求: %d", len(m.rawReqs))
	}
	input, _ := m.rawReqs[2]["input"].([]any)
	found := false
	for _, it := range input {
		item, _ := it.(map[string]any)
		if item["role"] != "user" {
			continue
		}
		parts, _ := item["content"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			if text, _ := part["text"].(string); strings.HasPrefix(text, "[本轮因错误中止") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("续接请求 input 应含错误提示: %+v", input)
	}
}

func TestResponsesOmitsEmptyReasoningID(t *testing.T) {
	m, cfg := responsesLLM(t, mockStep{content: "ok"})
	history := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "ok", ReasoningItems: []ReasoningItem{{Content: "chat 来的推理"}}},
	}
	if _, err := NewClient(cfg, nil).ChatStream(context.Background(), history, nil); err != nil {
		t.Fatal(err)
	}
	input, _ := m.rawReqs[0]["input"].([]any)
	var reasoning map[string]any
	for _, it := range input {
		if item, ok := it.(map[string]any); ok && item["type"] == "reasoning" {
			reasoning = item
		}
	}
	if reasoning == nil {
		t.Fatal("应回传 reasoning item")
	}
	if _, ok := reasoning["id"]; ok {
		t.Errorf("空 id 不应出现在请求里: %v", reasoning)
	}
}

func reasoningDelta(text string) string {
	return `event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","delta":"` + text + `"}

`
}

func TestResponsesStreamReasoningEndMultiItem(t *testing.T) {
	done := `event: response.reasoning_text.done
data: {"type":"response.reasoning_text.done"}

`
	itemDone := func(itemType string) string {
		return `event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"` + itemType + `","id":"rs_x"}}

`
	}
	cases := []struct {
		name string
		body []string
	}{
		{"text.done 分隔两段", []string{
			reasoningDelta("第一段"), done,
			reasoningDelta("第二段"), done,
			"data: [DONE]\n\n",
		}},
		{"item.done 分隔两段", []string{
			reasoningDelta("第一段"), itemDone("reasoning"),
			reasoningDelta("第二段"), itemDone("reasoning"),
			"data: [DONE]\n\n",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, cfg := responsesLLM(t, mockStep{rawSSE: tc.body})
			var kinds []EventKind
			if _, err := NewClient(cfg, nil).ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, EventSink(func(e Event) {
				kinds = append(kinds, e.Kind)
			})); err != nil {
				t.Fatal(err)
			}
			if n := kindCount(kinds, EventReasoningEnd); n != 2 {
				t.Fatalf("两段思维链应各发一次段结束（实发 %d）: %v", n, kinds)
			}
			first, last := -1, -1
			for i, k := range kinds {
				if k == EventReasoningEnd {
					if first < 0 {
						first = i
					}
					last = i
				}
			}
			if first == last {
				t.Fatalf("两段应各有一个段结束事件: %v", kinds)
			}
			if kinds[first-1] != EventReasoning || kinds[first+1] != EventReasoning || kinds[last-1] != EventReasoning {
				t.Errorf("段结束应夹在两段 delta 之间: %v", kinds)
			}
		})
	}
	t.Run("工具调用 delta 收尾", func(t *testing.T) {
		_, cfg := responsesLLM(t, mockStep{rawSSE: []string{
			reasoningDelta("先想一想"),
			`event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{}"}

`,
			"data: [DONE]\n\n",
		}})
		var kinds []EventKind
		if _, err := NewClient(cfg, nil).ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, EventSink(func(e Event) {
			kinds = append(kinds, e.Kind)
		})); err != nil {
			t.Fatal(err)
		}
		if n := kindCount(kinds, EventReasoningEnd); n != 1 {
			t.Fatalf("工具调用前应收尾思维链段（实发 %d）: %v", n, kinds)
		}
		end := kindIndex(kinds, EventReasoningEnd)
		if kinds[end-1] != EventReasoning || kinds[end+1] != EventToolCall {
			t.Errorf("段结束应落在工具调用之前: %v", kinds)
		}
	})
	t.Run("非 reasoning 的 item.done 不切段", func(t *testing.T) {
		_, cfg := responsesLLM(t, mockStep{rawSSE: []string{
			reasoningDelta("第一段"), itemDone("message"),
			reasoningDelta("第二段"), itemDone("reasoning"),
			"data: [DONE]\n\n",
		}})
		var kinds []EventKind
		if _, err := NewClient(cfg, nil).ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, EventSink(func(e Event) {
			kinds = append(kinds, e.Kind)
		})); err != nil {
			t.Fatal(err)
		}
		if n := kindCount(kinds, EventReasoningEnd); n != 1 {
			t.Fatalf("只有 reasoning item 结束才切段（实发 %d）: %v", n, kinds)
		}
	})
	t.Run("空段 done 不发", func(t *testing.T) {
		_, cfg := responsesLLM(t, mockStep{rawSSE: []string{done, "data: [DONE]\n\n"}})
		var kinds []EventKind
		if _, err := NewClient(cfg, nil).ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, EventSink(func(e Event) {
			kinds = append(kinds, e.Kind)
		})); err != nil {
			t.Fatal(err)
		}
		if n := kindCount(kinds, EventReasoningEnd); n != 0 {
			t.Errorf("无 delta 的段结束不应外发（实发 %d）: %v", n, kinds)
		}
	})
	t.Run("非 reasoning 的 item.done 不发", func(t *testing.T) {
		_, cfg := responsesLLM(t, mockStep{rawSSE: []string{itemDone("message"), "data: [DONE]\n\n"}})
		var kinds []EventKind
		if _, err := NewClient(cfg, nil).ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, EventSink(func(e Event) {
			kinds = append(kinds, e.Kind)
		})); err != nil {
			t.Fatal(err)
		}
		if n := kindCount(kinds, EventReasoningEnd); n != 0 {
			t.Errorf("非思维链 item 结束不应发段结束（实发 %d）: %v", n, kinds)
		}
	})
}

func TestResponsesStreamReasoningEndEvents(t *testing.T) {
	cases := []struct {
		name string
		step mockStep
	}{
		{"reasoning_text.done", mockStep{reasoning: "先想了想", content: "答案是 42", reasoningDone: "text"}},
		{"reasoning_summary_text.done", mockStep{reasoning: "先想了想", content: "答案是 42", reasoningDone: "summary"}},
		{"output_item.done", mockStep{reasoning: "先想了想", content: "答案是 42", reasoningDone: "item"}},
		{"无 done 事件靠正文边界", mockStep{reasoning: "先想了想", content: "答案是 42"}},
		{"仅思考靠流结束兜底", mockStep{reasoning: "只有思考"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, cfg := responsesLLM(t, tc.step)
			var kinds []EventKind
			var reasoning strings.Builder
			if _, err := NewClient(cfg, nil).ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, EventSink(func(e Event) {
				kinds = append(kinds, e.Kind)
				if e.Kind == EventReasoning {
					reasoning.WriteString(e.Text)
				}
			})); err != nil {
				t.Fatal(err)
			}
			if reasoning.String() != "先想了想" && reasoning.String() != "只有思考" {
				t.Errorf("思维链 delta 未按序投递: %q", reasoning.String())
			}
			if n := kindCount(kinds, EventReasoningEnd); n != 1 {
				t.Fatalf("应恰发一次段结束事件（实发 %d）: %v", n, kinds)
			}
			end := kindIndex(kinds, EventReasoningEnd)
			if end == 0 || kinds[end-1] != EventReasoning {
				t.Errorf("段结束应紧随最后一个思维链 delta: %v", kinds)
			}
			for _, k := range kinds[:end] {
				if k != EventReasoning {
					t.Errorf("段结束前只应有思维链 delta: %v", kinds)
				}
			}
		})
	}
	t.Run("无思维链不发", func(t *testing.T) {
		_, cfg := responsesLLM(t, mockStep{content: "答"})
		var kinds []EventKind
		if _, err := NewClient(cfg, nil).ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, EventSink(func(e Event) {
			kinds = append(kinds, e.Kind)
		})); err != nil {
			t.Fatal(err)
		}
		if n := kindCount(kinds, EventReasoningEnd); n != 0 {
			t.Errorf("空段不应发结束事件（实发 %d）: %v", n, kinds)
		}
	})
}

// 摘要形态（Kimi / 火山方舟 / opencode）：正文在 summary[].summary_text，
// 流式事件名是 response.reasoning_summary_text.*。曾经只认 content[].reasoning_text，
// 导致 Content 恒空、落盘后 reasoning_items 只剩 id（2026-11-23 实测确认）。
func TestResponsesSummaryFormCapturesContent(t *testing.T) {
	_, cfg := responsesLLM(t, mockStep{reasoning: "先想了想", reasoningForm: "summary", content: "答案是 42"})
	var streamed strings.Builder
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		EventSink(func(e Event) {
			if e.Kind == EventReasoning {
				streamed.WriteString(e.Text)
			}
		}))
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "先想了想" {
		t.Errorf("摘要形态思维链事件: %q", streamed.String())
	}
	if len(msg.ReasoningItems) != 1 {
		t.Fatalf("reasoning items 数: %d", len(msg.ReasoningItems))
	}
	got := msg.ReasoningItems[0]
	if got.Content != "先想了想" {
		t.Errorf("摘要形态正文应落进 Content: %+v", got)
	}
	if got.ID != "rs_test" {
		t.Errorf("摘要形态应保留 id: %+v", got)
	}
}

// 明文形态（GLM / DeepSeek / LongCat）：正文在 content[].reasoning_text。
// 与摘要形态是并存的两派，改动后必须仍然优先取明文。
func TestResponsesPlainFormPrefersContent(t *testing.T) {
	_, cfg := responsesLLM(t, mockStep{reasoning: "明文推理", content: "好"})
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ReasoningItems) != 1 || msg.ReasoningItems[0].Content != "明文推理" {
		t.Fatalf("明文形态正文: %+v", msg.ReasoningItems)
	}
}

// 同一 item 两侧都有内容时优先明文 reasoning_text（它比摘要完整）。
func TestResponsesReasoningPrefersPlainOverSummary(t *testing.T) {
	if text := reasoningItemText(
		[]responsesContentText{{Type: "reasoning_text", Text: "明文全文"}},
		[]responsesContentText{{Type: "summary_text", Text: "摘要"}},
	); text != "明文全文" {
		t.Errorf("应优先明文: %q", text)
	}
	// 明文侧为空数组时回落摘要（真实上游的空侧就是 []）。
	if text := reasoningItemText(
		[]responsesContentText{},
		[]responsesContentText{{Type: "summary_text", Text: "摘要"}},
	); text != "摘要" {
		t.Errorf("应回落摘要: %q", text)
	}
	// 两侧都空 → 空串，调用方按「无思维链」处理。
	if text := reasoningItemText(nil, nil); text != "" {
		t.Errorf("两侧均空应得空串: %q", text)
	}
}

// assertReplayReasoningItem 断言回传的 reasoning item 与目标形态逐键一致：
// type=reasoning、summary/content 各恰一段且同文（summary_text / reasoning_text）、
// wantID 非空时带原样 id、空 id 时无 id 键，且除以上四键外没有别的键
// （status / encrypted_content 等一律不带）。
func assertReplayReasoningItem(t *testing.T, item map[string]any, wantID, wantText string) {
	t.Helper()
	if item["type"] != "reasoning" {
		t.Errorf("type = %v, want reasoning", item["type"])
	}
	for side, typ := range map[string]string{"summary": "summary_text", "content": "reasoning_text"} {
		arr, _ := item[side].([]any)
		if len(arr) != 1 {
			t.Fatalf("%s 应恰有一段: %v", side, item[side])
		}
		if p, ok := arr[0].(map[string]any); !ok || p["type"] != typ || p["text"] != wantText {
			t.Errorf("%s[0] = %v, want {%s %q}", side, arr[0], typ, wantText)
		}
	}
	if wantID == "" {
		if _, ok := item["id"]; ok {
			t.Errorf("空 id 不应出现在请求里: %v", item)
		}
	} else if item["id"] != wantID {
		t.Errorf("id = %v, want %q", item["id"], wantID)
	}
	wantKeys := map[string]bool{"type": true, "summary": true, "content": true, "id": wantID != ""}
	for k := range item {
		if !wantKeys[k] {
			t.Errorf("reasoning item 出现未预期键 %q: %v", k, item)
		}
	}
}

// replayReasoningItem 取第 i 个请求 input 里唯一的 reasoning item。
func replayReasoningItem(t *testing.T, m *mockLLM, i int) map[string]any {
	t.Helper()
	input, _ := m.rawReqs[i]["input"].([]any)
	var rs []map[string]any
	for _, it := range input {
		if item, ok := it.(map[string]any); ok && item["type"] == "reasoning" {
			rs = append(rs, item)
		}
	}
	if len(rs) != 1 {
		t.Fatalf("请求 %d 应恰有一个 reasoning item: %v", i, rs)
	}
	return rs[0]
}

// 回传形态与供应商无关：正文双写到 summary[].summary_text 与 content[].reasoning_text
// （同一份正文），上游 id 原样带回，不带 status / encrypted_content。
// 各家只读其一（content 优先）、双写不双计费，依据与实测见 docs/reasoning-replay.md。
func TestResponsesReplayDoubleWrites(t *testing.T) {
	m, cfg := responsesLLM(t, mockStep{content: "ok"})
	history := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "ok", ReasoningItems: []ReasoningItem{
			{ID: "rs_sum", Content: "先想了想"},
		}},
	}
	if _, err := NewClient(cfg, nil).ChatStream(context.Background(), history, nil); err != nil {
		t.Fatal(err)
	}
	assertReplayReasoningItem(t, replayReasoningItem(t, m, 0), "rs_sum", "先想了想")
}

// 旧会话文件带已撤回的 from_summary 键（2026-11-23 引入、次日方案推翻）：
// encoding/json 忽略未知键，条目照常加载并按统一形态双写回传，无需迁移。
func TestResponsesReplayLegacySummaryKeyIgnored(t *testing.T) {
	var legacy ReasoningItem
	if err := json.Unmarshal([]byte(`{"id":"rs_old","content":"旧推理","from_summary":true}`), &legacy); err != nil {
		t.Fatal(err)
	}
	m, cfg := responsesLLM(t, mockStep{content: "ok"})
	history := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "ok", ReasoningItems: []ReasoningItem{legacy}},
	}
	if _, err := NewClient(cfg, nil).ChatStream(context.Background(), history, nil); err != nil {
		t.Fatal(err)
	}
	assertReplayReasoningItem(t, replayReasoningItem(t, m, 0), "rs_old", "旧推理")
}

// 兜底：个别上游只发 reasoning delta、终态不给 reasoning item。此时正文必须
// 靠本地累积补回来，否则这次思考既不上屏也不落盘（等于静默丢弃）。
func TestResponsesReasoningDeltaFallbackWithoutTerminalItem(t *testing.T) {
	_, cfg := responsesLLM(t, mockStep{rawSSE: []string{
		reasoningDelta("只有增量没有终态"),
		`event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"好"}

`,
		"data: [DONE]\n\n",
	}})
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ReasoningItems) != 1 {
		t.Fatalf("终态缺失时应由累积兜底补一条: %+v", msg.ReasoningItems)
	}
	if msg.ReasoningItems[0].Content != "只有增量没有终态" {
		t.Errorf("兜底正文: %q", msg.ReasoningItems[0].Content)
	}
}

// 有终态 item 时不得重复添加（累积只作兜底）。
func TestResponsesReasoningNoDuplicateWhenTerminalPresent(t *testing.T) {
	_, cfg := responsesLLM(t, mockStep{reasoning: "想过了", content: "好"})
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ReasoningItems) != 1 {
		t.Fatalf("有终态时不应重复添加: %+v", msg.ReasoningItems)
	}
	if msg.ReasoningItems[0].Content != "想过了" {
		t.Errorf("应以终态为准: %q", msg.ReasoningItems[0].Content)
	}
}

// 两派 delta 混发且终态缺 reasoning item 时的兜底：不能把明文与摘要拼成一段，
// 按终态解析同序取明文优先（它比摘要完整）。
func TestResponsesMixedDeltaFallbackPrefersPlain(t *testing.T) {
	_, cfg := responsesLLM(t, mockStep{rawSSE: []string{
		`event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","delta":"明文段"}

`,
		`event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","delta":"摘要段"}

`,
		`event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"好"}

`,
		"data: [DONE]\n\n",
	}})
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ReasoningItems) != 1 {
		t.Fatalf("兜底应只补一条 reasoning item: %+v", msg.ReasoningItems)
	}
	got := msg.ReasoningItems[0]
	if got.Content != "明文段" {
		t.Errorf("混发时应取明文段、不与摘要拼接: %q", got.Content)
	}
}

// 只有摘要 delta、终态缺 reasoning item：兜底取摘要桶（明文桶为空），
// 否则这次思考既不落盘也不回传。
func TestResponsesSummaryDeltaFallbackUsedWhenNoPlain(t *testing.T) {
	_, cfg := responsesLLM(t, mockStep{rawSSE: []string{
		`event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","delta":"摘要段"}

`,
		`event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"好"}

`,
		"data: [DONE]\n\n",
	}})
	msg, err := NewClient(cfg, nil).ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ReasoningItems) != 1 {
		t.Fatalf("兜底应补一条 reasoning item: %+v", msg.ReasoningItems)
	}
	if got := msg.ReasoningItems[0]; got.Content != "摘要段" {
		t.Errorf("兜底正文: %q", got.Content)
	}
}
