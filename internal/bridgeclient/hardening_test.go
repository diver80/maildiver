package bridgeclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/omathunder/internal/ai"
)

type capture struct {
	mu   sync.Mutex
	reqs []*http.Request
	body []string
}

func (c *capture) handler(status int, resp string, delay time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.reqs = append(c.reqs, r)
		c.body = append(c.body, string(b))
		c.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}
}

func (c *capture) last() (*http.Request, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reqs[len(c.reqs)-1], c.body[len(c.body)-1]
}

func TestNoGlobalTimeoutAndDefaultDeadlines(t *testing.T) {
	if New("http://x", "t").httpClient.Timeout != 0 {
		t.Fatal("http.Client must not carry a global timeout (it would cut AI calls)")
	}
	ctx, cancel := withDefaultDeadline(context.Background(), time.Minute)
	defer cancel()
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > time.Minute {
		t.Fatal("expected default deadline on a context without one")
	}
	parent, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pcancel()
	ctx2, cancel2 := withDefaultDeadline(parent, time.Hour)
	defer cancel2()
	if dl, _ := ctx2.Deadline(); time.Until(dl) > 6*time.Second {
		t.Fatal("caller deadline must win")
	}
	if AIRequestTimeout <= 110*time.Second {
		t.Fatalf("AI timeout must exceed the bridge's 110 s analyze budget, got %v", AIRequestTimeout)
	}
}

func TestErrorCodesAndOutcomeUnknown(t *testing.T) {
	var cap capture
	srv := httptest.NewServer(cap.handler(http.StatusConflict, `{"ok":false,"error":"message_changed: id 7 now points to a different mail"}`, 0))
	defer srv.Close()
	c := New(srv.URL, "tok")
	_, err := c.MessageActions(context.Background(), "7", MessageActionsRequest{ExpectedMessageID: "a@b"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "message_changed" || apiErr.Status != http.StatusConflict || !strings.Contains(apiErr.Detail, "different mail") {
		t.Fatalf("unexpected error %#v", err)
	}
	if _, body := cap.last(); !strings.Contains(body, `"expectedMessageId":"a@b"`) {
		t.Fatalf("expectedMessageId not sent: %s", body)
	}

	for msg, want := range map[string]string{
		"feature_disabled:calendar": "feature_disabled",
		"read_only":                 "read_only",
		"ambiguous: 2 copies":       "ambiguous",
		"something: else":           "",
	} {
		if code, _ := ParseErrorCode(msg); code != want {
			t.Errorf("ParseErrorCode(%q)=%q want %q", msg, code, want)
		}
	}

	// A mutating request that times out on the client has an unknown outcome.
	slow := httptest.NewServer(cap.handler(http.StatusOK, `{"ok":true,"result":{}}`, 2*time.Second))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = New(slow.URL, "tok").ComposeSend(ctx, ComposeRequest{To: []string{"a@b.de"}})
	if !IsOutcomeUnknown(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected outcome_unknown wrapping the deadline, got %v", err)
	}
	// A GET that times out is just a timeout.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	_, err = New(slow.URL, "tok").Accounts(ctx2)
	if err == nil || IsOutcomeUnknown(err) {
		t.Fatalf("GET timeout must not be outcome_unknown, got %v", err)
	}
	var nilClient *Client
	if err := nilClient.DoJSON(context.Background(), http.MethodGet, "/v1/health", nil, nil); !errors.Is(err, ErrOffline) {
		t.Fatalf("nil client must return ErrOffline, got %v", err)
	}
}

func TestComposeIdempotencyAndCalendarScope(t *testing.T) {
	var cap capture
	srv := httptest.NewServer(cap.handler(http.StatusOK, `{"ok":true,"result":{"ok":true}}`, 0))
	defer srv.Close()
	c := New(srv.URL, "tok")

	if _, err := c.ComposeDraft(context.Background(), ComposeRequest{Body: "x"}); err != nil {
		t.Fatal(err)
	}
	_, body := cap.last()
	var sent map[string]any
	_ = json.Unmarshal([]byte(body), &sent)
	if k, _ := sent["idempotencyKey"].(string); !strings.HasPrefix(k, "omt-") {
		t.Fatalf("expected generated idempotency key, got %v", sent)
	}
	if _, err := c.ComposeSend(context.Background(), ComposeRequest{Body: "x", IdempotencyKey: "fixed"}); err != nil {
		t.Fatal(err)
	}
	if _, body := cap.last(); !strings.Contains(body, `"idempotencyKey":"fixed"`) {
		t.Fatalf("caller key must be kept: %s", body)
	}
	if ContentIdempotencyKey("n", "a", "b") != ContentIdempotencyKey("n", "a", "b") || ContentIdempotencyKey("n", "a", "b") == ContentIdempotencyKey("n", "ab", "") {
		t.Fatal("content key must be stable and unambiguous")
	}

	if err := c.DeleteCalendarEvent(context.Background(), "cal 1", "ev/2", "20261118T090000Z", ScopeOccurrence); err != nil {
		t.Fatal(err)
	}
	r, _ := cap.last()
	if r.Method != http.MethodDelete || r.URL.Query().Get("recurrenceId") != "20261118T090000Z" || r.URL.Query().Get("scope") != "occurrence" {
		t.Fatalf("unexpected delete request %s %s", r.Method, r.URL.String())
	}
	if !strings.Contains(r.URL.EscapedPath(), "cal%201") || !strings.Contains(r.URL.EscapedPath(), "ev%2F2") {
		t.Fatalf("path segments must be escaped: %s", r.URL.EscapedPath())
	}
}

func TestAIRequestsNeverSendEndpoint(t *testing.T) {
	var cap capture
	srv := httptest.NewServer(cap.handler(http.StatusOK, `{"ok":true,"result":{"providers":[]}}`, 0))
	defer srv.Close()
	c := New(srv.URL, "tok")
	if _, err := c.AIProviders(context.Background(), true, "openai"); err != nil {
		t.Fatal(err)
	}
	if r, _ := cap.last(); r.URL.Query().Has("endpoint") || r.URL.Query().Get("backend") != "openai" {
		t.Fatalf("unexpected providers query %s", r.URL.RawQuery)
	}
	_, _ = c.AIChat(context.Background(), ai.ChatRequest{Endpoint: "https://evil.example", Messages: []anyllm.Message{{Role: "user", Content: "x"}}})
	if _, body := cap.last(); strings.Contains(body, "evil.example") {
		t.Fatalf("endpoint leaked into AI chat request: %s", body)
	}
}
