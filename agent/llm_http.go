package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type httpError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *httpError) Error() string {
	return fmt.Sprintf(MsgAPIStatus, e.Status, e.Body)
}

func (c *Client) endpoint(path string) string {
	return strings.TrimSuffix(c.cfg.BaseURL, "/") + path
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	return req, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &httpError{
			Status:     resp.StatusCode,
			Body:       strings.TrimSpace(string(b)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	return resp, nil
}

var (
	streamIdleTimeout = 60 * time.Second
	retryBaseDelay    = 500 * time.Millisecond
	retryMaxDelay     = 8 * time.Second
	retryAfterCap     = 60 * time.Second
)

// semanticError 标记上游语义失败（HTTP 200 但响应体报错）：重试只会重复失败，直接上抛。
type semanticError struct{ err error }

func (e *semanticError) Error() string { return e.err.Error() }

func (e *semanticError) Unwrap() error { return e.err }

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return code >= 500
}

// retryableErr 给出这次失败是否值得重试；Retry-After 有值时用于取代退避。
func retryableErr(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	var se *semanticError
	if errors.As(err, &se) {
		return 0, false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, false
	}
	var he *httpError
	if errors.As(err, &he) {
		if retryableStatus(he.Status) {
			return he.RetryAfter, true
		}
		return 0, false
	}
	return 0, true
}

// retryDelay 给第 attempt 次失败（从 1 起）算出等待时长：上游给了 Retry-After 就用它（封顶），否则指数退避。
func retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > retryAfterCap {
			return retryAfterCap
		}
		return retryAfter
	}
	d := retryBaseDelay
	for i := 1; i < attempt && d < retryMaxDelay; i++ {
		d *= 2
	}
	if d > retryMaxDelay {
		d = retryMaxDelay
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type idleWatcher struct {
	feed   chan struct{}
	quit   chan struct{}
	cancel context.CancelFunc
	fired  atomic.Bool
}

func newIdleWatcher(ctx context.Context, d time.Duration) (*idleWatcher, context.Context) {
	ictx, cancel := context.WithCancel(ctx)
	w := &idleWatcher{feed: make(chan struct{}, 1), quit: make(chan struct{}), cancel: cancel}
	go func() {
		t := time.NewTimer(d)
		defer t.Stop()
		for {
			select {
			case <-w.quit:
				return
			case <-w.feed:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(d)
			case <-t.C:
				w.fired.Store(true)
				cancel()
				return
			}
		}
	}()
	return w, ictx
}

func (w *idleWatcher) Write(p []byte) (int, error) {
	select {
	case w.feed <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (w *idleWatcher) stop() {
	close(w.quit)
	w.cancel()
}

func (c *Client) streamSSE(ctx context.Context, path string, payload any, onData func(string) error) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	w, ictx := newIdleWatcher(ctx, streamIdleTimeout)
	defer w.stop()
	err = scanSSE(ictx, io.TeeReader(resp.Body, w), onData)
	if err != nil && w.fired.Load() {
		return fmt.Errorf(MsgStreamIdle, streamIdleTimeout)
	}
	return err
}

func scanSSE(ctx context.Context, r io.Reader, onData func(string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "[DONE]" {
			return nil
		}
		if err := onData(data); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf(MsgReadStream, err)
	}
	return nil
}
