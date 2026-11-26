package agent

import (
	"context"
	"fmt"
)

// Hook 是生命周期钩子：任意实例，按它实现的切点接口登记，可同时实现多个切点。
// 未实现任何切点接口时 New 返回错误；nil 元素被忽略。
type Hook any

// TurnStart 是回合开始的只读快照。
type TurnStart struct {
	Continue  bool
	Input     string
	SessionID string
	History   func() []Message
	Stats     func() Stats
}

// RequestInfo 是一次请求发出前的只读快照。
type RequestInfo struct {
	Turn      int
	SessionID string
	History   func() []Message
	Stats     func() Stats
}

// SessionEventKind 标识会话边界切点的来源。
type SessionEventKind uint8

const (
	SessionNew SessionEventKind = iota + 1
	SessionLoad
	SessionAdopt
	SessionFork
	SessionSwitchWorkspace
	SessionHandoff
)

// SessionEvent 是一次会话边界的只读快照；Handoff 仅在 SessionHandoff 时非空。
type SessionEvent struct {
	Kind      SessionEventKind
	SessionID string
	Workspace string
	Handoff   *Handoff
}

// SaveInfo 是一次落盘尝试的结果。
type SaveInfo struct {
	Session  string
	Messages int
	Err      error
}

type BeforeTurnHook interface {
	BeforeTurn(ctx context.Context, in TurnStart)
}

type BeforeRequestHook interface {
	BeforeRequest(ctx context.Context, in RequestInfo)
}

type AfterResponseHook interface {
	AfterResponse(ctx context.Context, in RequestInfo, resp ResponseInfo)
}

type BeforeToolHook interface {
	BeforeTool(ctx context.Context, in ToolCall)
}

type AfterToolHook interface {
	AfterTool(ctx context.Context, in ToolCall, result ToolResult)
}

type AfterTurnHook interface {
	AfterTurn(ctx context.Context, in TurnInfo)
}

// SessionHook 在会话边界切换后调用。
type SessionHook interface {
	SessionEvent(ctx context.Context, in SessionEvent)
}

type AfterSaveHook interface {
	AfterSave(ctx context.Context, in SaveInfo)
}

type hookSet struct {
	beforeTurn    []BeforeTurnHook
	beforeRequest []BeforeRequestHook
	afterResponse []AfterResponseHook
	beforeTool    []BeforeToolHook
	afterTool     []AfterToolHook
	afterTurn     []AfterTurnHook
	session       []SessionHook
	afterSave     []AfterSaveHook
}

func WithHooks(hooks ...Hook) Option {
	return func(o *Options) { o.hooks = append(o.hooks, hooks...) }
}

func newHookSet(hooks []Hook) (*hookSet, error) {
	s := &hookSet{}
	for _, h := range hooks {
		if h == nil {
			continue
		}
		matched := false
		if v, ok := h.(BeforeTurnHook); ok {
			s.beforeTurn = append(s.beforeTurn, v)
			matched = true
		}
		if v, ok := h.(BeforeRequestHook); ok {
			s.beforeRequest = append(s.beforeRequest, v)
			matched = true
		}
		if v, ok := h.(AfterResponseHook); ok {
			s.afterResponse = append(s.afterResponse, v)
			matched = true
		}
		if v, ok := h.(BeforeToolHook); ok {
			s.beforeTool = append(s.beforeTool, v)
			matched = true
		}
		if v, ok := h.(AfterToolHook); ok {
			s.afterTool = append(s.afterTool, v)
			matched = true
		}
		if v, ok := h.(AfterTurnHook); ok {
			s.afterTurn = append(s.afterTurn, v)
			matched = true
		}
		if v, ok := h.(SessionHook); ok {
			s.session = append(s.session, v)
			matched = true
		}
		if v, ok := h.(AfterSaveHook); ok {
			s.afterSave = append(s.afterSave, v)
			matched = true
		}
		if !matched {
			return nil, fmt.Errorf(MsgUnknownHookFmt, h)
		}
	}
	return s, nil
}

func (s *hookSet) fireBeforeTurn(ctx context.Context, in TurnStart) {
	for _, h := range s.beforeTurn {
		h.BeforeTurn(ctx, in)
	}
}

func (s *hookSet) fireBeforeRequest(ctx context.Context, in RequestInfo) {
	for _, h := range s.beforeRequest {
		h.BeforeRequest(ctx, in)
	}
}

func (s *hookSet) fireAfterResponse(ctx context.Context, in RequestInfo, resp ResponseInfo) {
	for _, h := range s.afterResponse {
		h.AfterResponse(ctx, in, resp)
	}
}

func (s *hookSet) fireBeforeTool(ctx context.Context, in ToolCall) {
	for _, h := range s.beforeTool {
		h.BeforeTool(ctx, in)
	}
}

func (s *hookSet) fireAfterTool(ctx context.Context, in ToolCall, result ToolResult) {
	for _, h := range s.afterTool {
		h.AfterTool(ctx, in, result)
	}
}

func (s *hookSet) fireAfterTurn(ctx context.Context, in TurnInfo) {
	for _, h := range s.afterTurn {
		h.AfterTurn(ctx, in)
	}
}

func (s *hookSet) fireSession(ctx context.Context, in SessionEvent) {
	for _, h := range s.session {
		h.SessionEvent(ctx, in)
	}
}

func (s *hookSet) fireAfterSave(ctx context.Context, in SaveInfo) {
	for _, h := range s.afterSave {
		h.AfterSave(ctx, in)
	}
}

func (a *Agent) turnStart(cont bool, input string) TurnStart {
	return TurnStart{Continue: cont, Input: input, SessionID: a.SessionID(), History: a.History, Stats: a.Stats}
}

func (a *Agent) requestInfo(turn int) RequestInfo {
	return RequestInfo{Turn: turn, SessionID: a.SessionID(), History: a.History, Stats: a.Stats}
}

func (a *Agent) fireSession(kind SessionEventKind, h *Handoff) {
	a.hooks.fireSession(context.Background(), SessionEvent{
		Kind:      kind,
		SessionID: a.SessionID(),
		Workspace: a.workspace,
		Handoff:   h,
	})
}
