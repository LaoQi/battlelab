package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type hookRecorder struct {
	calls     []string
	turns     []int
	saves     []SaveInfo
	sess      []SessionEvent
	onRequest func(RequestInfo)
}

func (r *hookRecorder) BeforeTurn(ctx context.Context, in TurnStart) {
	r.calls = append(r.calls, "beforeTurn")
}

func (r *hookRecorder) BeforeRequest(ctx context.Context, in RequestInfo) {
	r.calls = append(r.calls, "beforeRequest")
	r.turns = append(r.turns, in.Turn)
	if r.onRequest != nil {
		r.onRequest(in)
	}
}

func (r *hookRecorder) AfterResponse(ctx context.Context, in RequestInfo, resp ResponseInfo) {
	r.calls = append(r.calls, "afterResponse")
	r.turns = append(r.turns, in.Turn)
}

func (r *hookRecorder) BeforeTool(ctx context.Context, in ToolCall) {
	r.calls = append(r.calls, "beforeTool")
}

func (r *hookRecorder) AfterTool(ctx context.Context, in ToolCall, result ToolResult) {
	r.calls = append(r.calls, "afterTool")
}

func (r *hookRecorder) AfterTurn(ctx context.Context, in TurnInfo) {
	r.calls = append(r.calls, "afterTurn")
}

func (r *hookRecorder) SessionEvent(ctx context.Context, in SessionEvent) {
	r.calls = append(r.calls, "session")
	r.sess = append(r.sess, in)
}

func (r *hookRecorder) AfterSave(ctx context.Context, in SaveInfo) {
	r.calls = append(r.calls, "afterSave")
	r.saves = append(r.saves, in)
}

type tagHook struct {
	tag string
	log *[]string
}

func (h tagHook) BeforeRequest(ctx context.Context, in RequestInfo) {
	*h.log = append(*h.log, h.tag)
}

func hookMessages(t *testing.T, req chatRequest) []any {
	t.Helper()
	if len(req.Messages) == 0 {
		t.Fatal("请求缺少 messages")
	}
	return req.Messages
}

func hookMessageText(t *testing.T, v any) string {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("消息形态异常: %#v", v)
	}
	s, _ := m["content"].(string)
	return s
}

func TestHooksFireAllPointsInOrder(t *testing.T) {
	m := newMockLLM(t,
		mockStep{toolCalls: []mockToolCall{{id: "call_1", name: "run_shell", args: `{"command":"echo hi"}`}}},
		mockStep{content: "done"},
	)
	rec := &hookRecorder{}
	a := newHandoffAgent(t, m, WithHooks(rec))
	if len(rec.calls) != 0 {
		t.Fatalf("构造期不应触发钩子: %v", rec.calls)
	}
	if err := a.Ask(context.Background(), "问题", nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"beforeTurn", "beforeRequest", "afterResponse", "beforeTool", "afterTool", "afterSave",
		"beforeRequest", "afterResponse", "afterSave", "afterTurn",
	}
	if got := strings.Join(rec.calls, ","); got != strings.Join(want, ",") {
		t.Errorf("调用序列 = %v\n期望 = %v", rec.calls, want)
	}
	if len(rec.turns) != 4 || rec.turns[0] != 0 || rec.turns[1] != 0 || rec.turns[2] != 1 || rec.turns[3] != 1 {
		t.Errorf("Turn 计数 = %v, 期望 [0 0 1 1]", rec.turns)
	}
	if len(rec.saves) == 0 || rec.saves[0].Session == "" || rec.saves[0].Err != nil || rec.saves[0].Messages == 0 {
		t.Errorf("SaveInfo 异常: %+v", rec.saves)
	}
}

func TestHooksOrderAcrossInstances(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "ok"})
	var log []string
	a := newHandoffAgent(t, m, WithHooks(tagHook{tag: "first", log: &log}, tagHook{tag: "second", log: &log}))
	if err := a.Ask(context.Background(), "问题", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(log, ",") != "first,second" {
		t.Errorf("注册顺序未保持: %v", log)
	}
}

func TestHooksUnknownTypeRejected(t *testing.T) {
	m := newMockLLM(t)
	cfg := m.config()
	_, err := New(cfg, WithSystemPrompt(testBasePrompt), WithHooks(struct{ Name string }{Name: "x"}))
	if err == nil {
		t.Fatal("未知钩子类型应当报错")
	}
	if !strings.Contains(err.Error(), "不支持的生命周期钩子类型") {
		t.Errorf("错误文案 = %v", err)
	}
}

func TestHooksNilIgnored(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "ok"})
	a := newHandoffAgent(t, m, WithHooks(nil, nil))
	if err := a.Ask(context.Background(), "问题", nil); err != nil {
		t.Fatal(err)
	}
}

func TestHooksBeforeRequestAppendsNotice(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "ok"})
	rec := &hookRecorder{}
	var a *Agent
	rec.onRequest = func(in RequestInfo) {
		if in.Turn != 0 {
			return
		}
		if err := a.AppendNotice("钩子注入的提醒"); err != nil {
			t.Errorf("注入失败: %v", err)
		}
	}
	a = newHandoffAgent(t, m, WithHooks(rec))
	if err := a.Ask(context.Background(), "问题", nil); err != nil {
		t.Fatal(err)
	}
	if len(m.reqs) != 1 {
		t.Fatalf("请求数 = %d", len(m.reqs))
	}
	msgs := hookMessages(t, m.reqs[0])
	last := hookMessageText(t, msgs[len(msgs)-1])
	if last != "钩子注入的提醒" {
		t.Errorf("注入行应在请求末尾, got %q", last)
	}
	if got := hookMessageText(t, msgs[len(msgs)-2]); got != "问题" {
		t.Errorf("注入行应排在用户输入之后, 前一条 = %q", got)
	}
}

func TestHooksNoticeLandsAfterToolBatch(t *testing.T) {
	m := newMockLLM(t,
		mockStep{toolCalls: []mockToolCall{{id: "call_1", name: "run_shell", args: `{"command":"echo hi"}`}}},
		mockStep{content: "done"},
	)
	rec := &hookRecorder{}
	var a *Agent
	rec.onRequest = func(in RequestInfo) {
		if in.Turn == 1 {
			_ = a.AppendNotice("第二轮提醒")
		}
	}
	a = newHandoffAgent(t, m, WithHooks(rec))
	if err := a.Ask(context.Background(), "问题", nil); err != nil {
		t.Fatal(err)
	}
	if len(m.reqs) != 2 {
		t.Fatalf("请求数 = %d", len(m.reqs))
	}
	msgs := hookMessages(t, m.reqs[1])
	if got := hookMessageText(t, msgs[len(msgs)-1]); got != "第二轮提醒" {
		t.Errorf("注入行应在末尾, got %q", got)
	}
	prev, ok := msgs[len(msgs)-2].(map[string]any)
	if !ok || prev["role"] != "tool" {
		t.Errorf("注入行之前应是 tool 消息, got %#v", msgs[len(msgs)-2])
	}
}

func TestHooksSessionEvents(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "ok"})
	rec := &hookRecorder{}
	a := newHandoffAgent(t, m, WithHooks(rec))

	a.NewSession()
	if len(rec.sess) != 1 || rec.sess[0].Kind != SessionNew {
		t.Fatalf("NewSession 事件 = %+v", rec.sess)
	}
	a.history = append(a.history, Message{Role: "user", Content: "旧会话内容"})
	if err := a.save(); err != nil {
		t.Fatal(err)
	}
	first := a.SessionID()
	if _, err := a.Fork(); err != nil {
		t.Fatal(err)
	}
	if len(rec.sess) != 2 || rec.sess[1].Kind != SessionFork {
		t.Fatalf("Fork 事件 = %+v", rec.sess)
	}
	if err := a.LoadSession(first); err != nil {
		t.Fatal(err)
	}
	if len(rec.sess) != 3 || rec.sess[2].Kind != SessionLoad {
		t.Fatalf("LoadSession 事件 = %+v", rec.sess)
	}
	if err := a.SwitchWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(rec.sess) != 4 || rec.sess[3].Kind != SessionSwitchWorkspace {
		t.Fatalf("SwitchWorkspace 事件 = %+v", rec.sess)
	}
	if rec.sess[3].Workspace != a.Workspace() {
		t.Errorf("工作区 = %q, 期望 %q", rec.sess[3].Workspace, a.Workspace())
	}
}

func TestHooksHandoffSessionEvent(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "收尾"})
	rec := &hookRecorder{}
	a := newHandoffAgent(t, m, WithHooks(rec))
	if _, err := a.RequestHandoff("摘要", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Ask(context.Background(), "问题", nil); err != nil {
		t.Fatal(err)
	}
	var got *SessionEvent
	for i := range rec.sess {
		if rec.sess[i].Kind == SessionHandoff {
			got = &rec.sess[i]
		}
	}
	if got == nil {
		t.Fatalf("未触发 SessionHandoff: %+v", rec.sess)
	}
	if got.Handoff == nil || got.Handoff.NewID == "" || got.Handoff.Summary != "摘要" {
		t.Errorf("交接事实 = %+v", got.Handoff)
	}
	if got.SessionID != got.Handoff.NewID {
		t.Errorf("SessionID = %q, 期望新会话 %q", got.SessionID, got.Handoff.NewID)
	}
}

func TestHooksFireInNoSaveMode(t *testing.T) {
	m := newMockLLM(t, mockStep{content: "ok"})
	rec := &hookRecorder{}
	a := newHandoffAgent(t, m, NoSave(true), WithHooks(rec))
	if err := a.Ask(context.Background(), "问题", nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.saves) == 0 {
		t.Fatal("不落盘模式下也应触发 AfterSave")
	}
	if rec.saves[0].Session != "" || rec.saves[0].Err != nil {
		t.Errorf("SaveInfo = %+v", rec.saves[0])
	}
	if rec.calls[len(rec.calls)-1] != "afterTurn" {
		t.Errorf("末次调用 = %q", rec.calls[len(rec.calls)-1])
	}
}

func TestHooksSkipRequestOnCanceledContext(t *testing.T) {
	m := newMockLLM(t)
	rec := &hookRecorder{}
	a := newHandoffAgent(t, m, WithHooks(rec))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.Ask(ctx, "问题", nil)
	var ie *InterruptError
	if !errors.As(err, &ie) {
		t.Fatalf("错误 = %v, 期望 InterruptError", err)
	}
	var sawTurn, sawRequest, sawEnd bool
	for _, c := range rec.calls {
		switch c {
		case "beforeTurn":
			sawTurn = true
		case "beforeRequest":
			sawRequest = true
		case "afterTurn":
			sawEnd = true
		}
	}
	if !sawTurn || !sawEnd {
		t.Errorf("回合开始/出口钩子应触发: %v", rec.calls)
	}
	if sawRequest {
		t.Errorf("ctx 已取消不应触发请求前钩子: %v", rec.calls)
	}
	if len(m.reqs) != 0 {
		t.Errorf("ctx 已取消不应发出请求, got %d", len(m.reqs))
	}
}
