package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const fhInstruction = "写一份交接摘要：已完成、关键结论、当前状态、下一步。"

type fhRecorder struct {
	mu       sync.Mutex
	requests int
	sessions []SessionEvent
}

func (r *fhRecorder) BeforeRequest(context.Context, RequestInfo) {
	r.mu.Lock()
	r.requests++
	r.mu.Unlock()
}

func (r *fhRecorder) SessionEvent(_ context.Context, e SessionEvent) {
	r.mu.Lock()
	r.sessions = append(r.sessions, e)
	r.mu.Unlock()
}

func (r *fhRecorder) reqs() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests
}

func (r *fhRecorder) handoffs() []SessionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SessionEvent, 0, len(r.sessions))
	for _, e := range r.sessions {
		if e.Kind == SessionHandoff {
			out = append(out, e)
		}
	}
	return out
}

// fhForcer 在 BeforeTurn 里尝试强制复位：用于验证回合内被拒绝。
type fhForcer struct {
	ag  *Agent
	err error
}

func (f *fhForcer) BeforeTurn(ctx context.Context, _ TurnStart) {
	_, f.err = f.ag.ForceHandoff(ctx, ForceHandoffOptions{Instruction: fhInstruction})
}

func TestForceHandoffResetsState(t *testing.T) {
	rec := &fhRecorder{}
	m := newMockLLM(t,
		mockStep{
			content:   "先跑一下",
			toolCalls: []mockToolCall{{id: "c1", name: "run_shell", args: `{"command":"ls"}`}},
			usage:     &Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		},
		mockStep{content: "目录列完了", usage: &Usage{PromptTokens: 150, CompletionTokens: 5, TotalTokens: 155}},
		mockStep{content: "已完成：A\n下一步：B", usage: &Usage{PromptTokens: 200, CompletionTokens: 30, TotalTokens: 230}},
	)
	a := newHandoffAgent(t, m, WithHooks(rec))
	if err := a.Ask(context.Background(), "看看目录", nil); err != nil {
		t.Fatal(err)
	}
	oldID, oldFile := a.SessionID(), a.SessionFile()
	oldMsgs := len(a.History())
	oldLines := len(readSessionMsgs(t, oldFile))
	because := rec.reqs()
	if because != 2 {
		t.Fatalf("前置回合应发 2 次请求，实际 %d", because)
	}
	if st := a.Stats(); st.TotalTokens == 0 || !st.HasContext {
		t.Fatalf("前置回合应记用量: %+v", st)
	}

	h, err := a.ForceHandoff(context.Background(), ForceHandoffOptions{Instruction: fhInstruction, Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	if h.Summary != "已完成：A\n下一步：B" {
		t.Errorf("摘要取自一次性请求: %q", h.Summary)
	}
	if h.OldID != oldID || h.OldFile != oldFile || h.OldMsgs != oldMsgs {
		t.Errorf("旧会话快照不对: %+v", h)
	}
	if h.NewID == "" || h.NewID == oldID {
		t.Errorf("新会话 id 应不同: %q → %q", oldID, h.NewID)
	}
	if a.SessionID() != h.NewID {
		t.Errorf("Agent 应已轮转到新会话: %q vs %q", a.SessionID(), h.NewID)
	}
	if !h.Continue || h.Failed {
		t.Errorf("交接事实字段不对: %+v", h)
	}

	// 指标三：新会话状态——history 只剩摘要消息，prompt/stats 复位。
	hist := a.History()
	if len(hist) != 1 || hist[0].Role != "user" || !strings.Contains(hist[0].Content, "已完成：A") {
		t.Fatalf("新会话 history 应只有摘要: %+v", hist)
	}
	if !strings.Contains(hist[0].Content, oldID) {
		t.Errorf("摘要消息应带旧会话 id: %q", hist[0].Content)
	}
	if st := a.Stats(); st.TotalTokens != 0 || st.HasContext || st.Messages != 1 {
		t.Errorf("新会话统计应复位: %+v", st)
	}

	// 指标一：旧会话安稳落地——末尾一条移交事实行，其余内容不动。
	old := readSessionMsgs(t, oldFile)
	if len(old) != oldLines+1 {
		t.Fatalf("旧会话应只多一条事实行: %d → %d", oldLines, len(old))
	}
	if want := strings.Replace(MsgHandoffNoticeFmt, "%s", h.NewID, 1); old[len(old)-1].Content != want {
		t.Errorf("移交事实行: %q", old[len(old)-1].Content)
	}

	// 一次性请求不算回合：不触发请求切点钩子，SessionHandoff 只上报一次。
	if got := rec.reqs(); got != because {
		t.Errorf("摘要请求不应触发 BeforeRequest: %d → %d", because, got)
	}
	handoffs := rec.handoffs()
	if len(handoffs) != 1 || handoffs[0].Handoff == nil || handoffs[0].Handoff.NewID != h.NewID {
		t.Errorf("应上报一次 SessionHandoff: %+v", handoffs)
	}
	// 外部路径直接返回事实，不走 lastHandoff。
	if _, ok := a.TakeHandoff(); ok {
		t.Error("ForceHandoff 不应写 lastHandoff")
	}
}

func TestForceHandoffSummaryRequestShape(t *testing.T) {
	m := newMockLLM(t,
		mockStep{
			content:   "先跑一下",
			toolCalls: []mockToolCall{{id: "c1", name: "run_shell", args: `{"command":"ls"}`}, {id: "c2", name: "run_shell", args: `{"command":"pwd"}`}},
		},
		mockStep{content: "跑完了"},
		mockStep{content: "摘要正文"},
	)
	a := newHandoffAgent(t, m)
	if err := a.Ask(context.Background(), "把 A 做完", nil); err != nil {
		t.Fatal(err)
	}
	a.history = append(a.history, Message{Role: "user", Content: "看看这张图", Images: []ImageRef{{MIME: "image/png", Data: "AAAA", Bytes: 4}}})

	if _, err := a.ForceHandoff(context.Background(), ForceHandoffOptions{Instruction: fhInstruction}); err != nil {
		t.Fatal(err)
	}
	if len(m.reqs) != 3 {
		t.Fatalf("应发出 3 次请求（2 回合 + 1 摘要），实际 %d", len(m.reqs))
	}
	req := m.reqs[2]
	if len(req.Tools) != 0 {
		t.Errorf("摘要请求不应带工具定义: %+v", req.Tools)
	}
	var wire []Message
	b, err := json.Marshal(req.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) < 2 || wire[0].Role != "system" || wire[0].Content == "" {
		t.Fatalf("首条应是 system 快照: %+v", wire[:2])
	}
	if wire[len(wire)-1].Role != "user" || wire[len(wire)-1].Content != fhInstruction {
		t.Errorf("指令应是最后一条 user: %+v", wire[len(wire)-1])
	}

	var placeholder string
	var imageMsg *Message
	for i := range wire {
		if wire[i].Role == "tool" {
			t.Errorf("工具消息应被省略: %+v", wire[i])
		}
		if strings.Contains(wire[i].Content, "已省略工具调用") {
			placeholder = wire[i].Content
		}
		if strings.Contains(wire[i].Content, "看看这张图") {
			imageMsg = &wire[i]
		}
	}
	if !strings.Contains(placeholder, "run_shell ×2") {
		t.Errorf("工具轮应换成一占位行: %q", placeholder)
	}
	if !strings.HasPrefix(placeholder, "先跑一下") {
		t.Errorf("占位行应保留 assistant 正文: %q", placeholder)
	}
	if imageMsg == nil {
		t.Fatal("图片那条 user 应保留文本")
	}
	if !strings.Contains(imageMsg.Content, "[图片 1 张]") || len(imageMsg.Images) != 0 {
		t.Errorf("图片应换成计数占位且不上送: %+v", imageMsg)
	}
	for _, msg := range wire {
		if msg.ReasoningContent != "" || len(msg.ReasoningItems) > 0 {
			t.Errorf("摘要请求不应带旧回合思维链: %+v", msg)
		}
	}
}

func TestForceHandoffRequiresInstruction(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "摘要"})
	a := newHandoffAgent(t, m)
	a.history = []Message{{Role: "user", Content: "干活"}}
	for _, instr := range []string{"", "   ", "\n\t"} {
		if _, err := a.ForceHandoff(context.Background(), ForceHandoffOptions{Instruction: instr}); err == nil {
			t.Errorf("空指令应报错: %q", instr)
		}
	}
	if len(m.reqs) != 0 {
		t.Errorf("空指令不应发请求: %d", len(m.reqs))
	}
	if len(a.History()) != 1 {
		t.Error("被拒的复位不应动 history")
	}
}

func TestForceHandoffEmptyHistory(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "摘要"})
	a := newHandoffAgent(t, m)
	if _, err := a.ForceHandoff(context.Background(), ForceHandoffOptions{Instruction: fhInstruction}); err == nil {
		t.Fatal("空会话应报错")
	}
	if len(m.reqs) != 0 {
		t.Errorf("空会话不应发请求: %d", len(m.reqs))
	}
}

func TestForceHandoffEmptySummaryKeepsState(t *testing.T) {
	rec := &fhRecorder{}
	m := newMockLLM(t, mockStep{content: ""})
	a := newHandoffAgent(t, m, WithHooks(rec))
	a.history = []Message{{Role: "user", Content: "干活"}}
	oldID := a.SessionID()
	oldFile := a.store.path()
	before, beforeErr := os.ReadFile(oldFile)

	if _, err := a.ForceHandoff(context.Background(), ForceHandoffOptions{Instruction: fhInstruction}); err == nil {
		t.Fatal("空摘要应报错")
	}
	if a.SessionID() != oldID {
		t.Errorf("失败不应轮转会话: %q → %q", oldID, a.SessionID())
	}
	if len(a.History()) != 1 || a.History()[0].Content != "干活" {
		t.Errorf("失败不应动 history: %+v", a.History())
	}
	after, afterErr := os.ReadFile(oldFile)
	if (beforeErr == nil) != (afterErr == nil) || string(after) != string(before) {
		t.Error("失败不应写旧会话文件")
	}
	if got := rec.handoffs(); len(got) != 0 {
		t.Errorf("失败不应上报 SessionHandoff: %+v", got)
	}
	if _, ok := a.TakeHandoff(); ok {
		t.Error("失败不应留下交接事实")
	}
}

func TestForceHandoffSummaryLenBoundary(t *testing.T) {
	if err := checkSummaryLen(strings.Repeat("字", handoffSummaryLimit)); err != nil {
		t.Errorf("上限边界应通过: %v", err)
	}
	if err := checkSummaryLen(strings.Repeat("字", handoffSummaryLimit+1)); err == nil {
		t.Error("超一个字应报错")
	}
}

func TestForceHandoffRejectsDuringTurn(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "回复"}, mockStep{content: "摘要"})
	f := &fhForcer{}
	a := newHandoffAgent(t, m, WithHooks(f))
	f.ag = a
	if err := a.Ask(context.Background(), "你好", nil); err != nil {
		t.Fatal(err)
	}
	if f.err == nil || f.err.Error() != MsgForceHandoffInTurn {
		t.Fatalf("回合内强制复位应被拒绝: %v", f.err)
	}
	if len(m.reqs) != 1 {
		t.Errorf("被拒的复位不应发请求: %d", len(m.reqs))
	}
	// 回合外同一实例可正常复位。
	if _, err := a.ForceHandoff(context.Background(), ForceHandoffOptions{Instruction: fhInstruction}); err != nil {
		t.Errorf("回合外应可复位: %v", err)
	}
}

func TestForceHandoffRejectsArchiveReadOnly(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "摘要"})
	cfg := m.config()
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(2 * time.Second)
	seedSession(t, a.store.dir, "20260101-010000", sampleSession, now.Add(-48*time.Hour))
	if _, err := a.ArchiveSessions(ArchiveOptions{Exclude: a.SessionID(), Now: now}); err != nil {
		t.Fatal(err)
	}
	if err := a.LoadSession("20260101-010000"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ForceHandoff(context.Background(), ForceHandoffOptions{Instruction: fhInstruction}); err != ErrArchiveReadOnly {
		t.Fatalf("归档只读会话应拒绝: %v", err)
	}
	if len(m.reqs) != 0 {
		t.Errorf("被拒的复位不应发请求: %d", len(m.reqs))
	}
}

func TestForceHandoffNoSave(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "回复"}, mockStep{content: "摘要"})
	a := newHandoffAgent(t, m, NoSave(true))
	if err := a.Ask(context.Background(), "你好", nil); err != nil {
		t.Fatal(err)
	}
	h, err := a.ForceHandoff(context.Background(), ForceHandoffOptions{Instruction: fhInstruction})
	if err != nil {
		t.Fatal(err)
	}
	if !h.NoSave {
		t.Error("不落盘模式应体现在交接事实上")
	}
	if a.SessionFile() != "" {
		t.Errorf("不落盘模式不应有会话文件: %q", a.SessionFile())
	}
	if len(a.History()) != 1 {
		t.Errorf("新会话 history 应只有摘要: %+v", a.History())
	}
	if h.Continue {
		t.Error("Continue 缺省应为 false")
	}
}
