package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fastRetry(t *testing.T) {
	t.Helper()
	old := retryBaseDelay
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = old })
}

func retryConfig(url, protocol string, max int) *Config {
	return &Config{BaseURL: url, APIKey: "k", ApiProtocol: protocol, MaxRetries: max, Model: "m", UserAgent: "test"}
}

func writeChatSSE(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func writeResponsesSSE(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", content)
	fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\ndata: [DONE]\n\n")
}

type retryRecorder struct {
	mu     sync.Mutex
	retry  []RetryInfo
	text   []string
	onRtry func(RetryInfo)
}

func (r *retryRecorder) sink(e Event) {
	r.mu.Lock()
	switch e.Kind {
	case EventRetry:
		r.retry = append(r.retry, e.Retry)
		hook := r.onRtry
		info := e.Retry
		r.mu.Unlock()
		if hook != nil {
			hook(info)
		}
		return
	case EventContent:
		r.text = append(r.text, e.Text)
	}
	r.mu.Unlock()
}

func (r *retryRecorder) retries() []RetryInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RetryInfo(nil), r.retry...)
}

func (r *retryRecorder) contents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.text...)
}

func TestChatStreamRetriesServerError(t *testing.T) {
	fastRetry(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		writeChatSSE(w, "ok")
	}))
	defer srv.Close()

	rec := &retryRecorder{}
	msg, err := NewClient(retryConfig(srv.URL, "chat", 2), nil).
		ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, rec.sink)
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if msg.Content != "ok" {
		t.Errorf("正文应只来自成功那次: %q", msg.Content)
	}
	if calls.Load() != 3 {
		t.Errorf("应共 3 次请求: %d", calls.Load())
	}
	got := rec.retries()
	if len(got) != 2 {
		t.Fatalf("应有 2 次重试事件: %+v", got)
	}
	for i, info := range got {
		if info.Attempt != i+1 || info.Max != 2 {
			t.Errorf("重试序号/上限不对: %+v", info)
		}
		if !strings.Contains(info.Err, "503") {
			t.Errorf("重试事件应带完整错误原文: %q", info.Err)
		}
	}
	if got[0].Delay != time.Millisecond || got[1].Delay != 2*time.Millisecond {
		t.Errorf("退避应指数翻倍: %+v", got)
	}
	if len(rec.contents()) != 1 {
		t.Errorf("已上屏正文不应重复: %v", rec.contents())
	}
}

func TestChatStreamRetryExhausted(t *testing.T) {
	fastRetry(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	rec := &retryRecorder{}
	_, err := NewClient(retryConfig(srv.URL, "chat", 2), nil).
		ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, rec.sink)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("用尽重试应返回最后一次错误: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("默认 2 次重试应共 3 次请求: %d", calls.Load())
	}
	if len(rec.retries()) != 2 {
		t.Errorf("应有 2 次重试事件: %+v", rec.retries())
	}
}

func TestChatStreamRetryDisabled(t *testing.T) {
	fastRetry(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	rec := &retryRecorder{}
	if _, err := NewClient(retryConfig(srv.URL, "chat", 0), nil).
		ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, rec.sink); err == nil {
		t.Fatal("max_retries=0 应直接失败")
	}
	if calls.Load() != 1 || len(rec.retries()) != 0 {
		t.Errorf("关闭重试后只应请求一次: calls=%d retries=%v", calls.Load(), rec.retries())
	}
}

func TestChatStreamNoRetryOnClientError(t *testing.T) {
	fastRetry(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	rec := &retryRecorder{}
	if _, err := NewClient(retryConfig(srv.URL, "chat", 5), nil).
		ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, rec.sink); err == nil {
		t.Fatal("400 应直接失败")
	}
	if calls.Load() != 1 || len(rec.retries()) != 0 {
		t.Errorf("4xx 不应重试: calls=%d retries=%v", calls.Load(), rec.retries())
	}
}

func TestChatStreamNoRetryAfterContent(t *testing.T) {
	fastRetry(t)
	old := streamIdleTimeout
	streamIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = old })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"half\"}}]}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	rec := &retryRecorder{}
	_, err := NewClient(retryConfig(srv.URL, "chat", 5), nil).
		ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, rec.sink)
	if err == nil || !strings.Contains(err.Error(), "空闲超时") {
		t.Fatalf("应报空闲超时: %v", err)
	}
	if calls.Load() != 1 || len(rec.retries()) != 0 {
		t.Errorf("已上屏内容后失败不应重试: calls=%d retries=%v", calls.Load(), rec.retries())
	}
	if got := rec.contents(); len(got) != 1 || got[0] != "half" {
		t.Errorf("已上屏内容应只出现一次: %v", got)
	}
}

func TestChatStreamRetryAfterHeader(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &retryRecorder{onRtry: func(RetryInfo) { cancel() }}
	_, err := NewClient(retryConfig(srv.URL, "chat", 5), nil).
		ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, rec.sink)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("等待期取消应返回 context.Canceled: %v", err)
	}
	got := rec.retries()
	if len(got) != 1 {
		t.Fatalf("应只有 1 次重试事件: %+v", got)
	}
	if got[0].Delay != 3*time.Second {
		t.Errorf("应尊重 Retry-After: %v", got[0].Delay)
	}
	if calls.Load() != 1 {
		t.Errorf("取消后不应再请求: %d", calls.Load())
	}
}

func TestResponsesStreamRetriesServerError(t *testing.T) {
	fastRetry(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "overloaded", http.StatusInternalServerError)
			return
		}
		writeResponsesSSE(w, "ok")
	}))
	defer srv.Close()

	rec := &retryRecorder{}
	msg, err := NewClient(retryConfig(srv.URL, "responses", 1), nil).
		ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, rec.sink)
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if msg.Content != "ok" || calls.Load() != 2 || len(rec.retries()) != 1 {
		t.Errorf("responses 协议也应重试: content=%q calls=%d retries=%v", msg.Content, calls.Load(), rec.retries())
	}
}

func TestResponsesStreamNoRetryOnSemanticError(t *testing.T) {
	fastRetry(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n")
	}))
	defer srv.Close()

	rec := &retryRecorder{}
	_, err := NewClient(retryConfig(srv.URL, "responses", 5), nil).
		ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, rec.sink)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("语义失败应原样上抛: %v", err)
	}
	if calls.Load() != 1 || len(rec.retries()) != 0 {
		t.Errorf("语义失败不应重试: calls=%d retries=%v", calls.Load(), rec.retries())
	}
}

func TestRetryDelay(t *testing.T) {
	old := retryBaseDelay
	retryBaseDelay = 500 * time.Millisecond
	t.Cleanup(func() { retryBaseDelay = old })

	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, w := range want {
		if got := retryDelay(i+1, 0); got != w {
			t.Errorf("第 %d 次退避应为 %v，实为 %v", i+1, w, got)
		}
	}
	if got := retryDelay(1, 5*time.Second); got != 5*time.Second {
		t.Errorf("Retry-After 应优先: %v", got)
	}
	if got := retryDelay(1, 5*time.Minute); got != retryAfterCap {
		t.Errorf("Retry-After 应封顶 %v: %v", retryAfterCap, got)
	}
}

func TestRetryableErr(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		retry bool
	}{
		{"503", &httpError{Status: http.StatusServiceUnavailable}, true},
		{"429", &httpError{Status: http.StatusTooManyRequests}, true},
		{"408", &httpError{Status: http.StatusRequestTimeout}, true},
		{"400", &httpError{Status: http.StatusBadRequest}, false},
		{"401", &httpError{Status: http.StatusUnauthorized}, false},
		{"404", &httpError{Status: http.StatusNotFound}, false},
		{"语义失败", &semanticError{errors.New("boom")}, false},
		{"已取消", context.Canceled, false},
		{"读流失败", fmt.Errorf(MsgReadStream, errors.New("unexpected EOF")), true},
		{"空闲超时", errors.New(MsgStreamIdle), true},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if _, ok := retryableErr(c.err); ok != c.retry {
			t.Errorf("%s: 期望 retry=%v 实为 %v", c.name, c.retry, ok)
		}
	}
	if after, ok := retryableErr(&httpError{Status: http.StatusTooManyRequests, RetryAfter: 7 * time.Second}); !ok || after != 7*time.Second {
		t.Errorf("应回传 Retry-After: %v %v", after, ok)
	}
}
