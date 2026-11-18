package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// recorder captures requests seen by a fake server (safe for the handler goroutine).
type recorder struct {
	mu       sync.Mutex
	bodies   []string
	headers  []http.Header
	paths    []string
	problems []string
}

func (r *recorder) add(req *http.Request) string {
	b, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, string(b))
	r.headers = append(r.headers, req.Header.Clone())
	r.paths = append(r.paths, req.URL.Path)
	return string(b)
}

// problem records an assertion failure from the handler goroutine (t.Fatal is not allowed there).
func (r *recorder) problem(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.problems = append(r.problems, fmt.Sprintf(format, args...))
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *recorder) body(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 {
		i = len(r.bodies) + i
	}
	if i < 0 || i >= len(r.bodies) {
		return ""
	}
	return r.bodies[i]
}

func (r *recorder) path(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.paths) {
		return ""
	}
	return r.paths[i]
}

func (r *recorder) header(i int) http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.headers) {
		return http.Header{}
	}
	return r.headers[i]
}

// check fails the test with every problem recorded by the handler.
func (r *recorder) check(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.problems {
		t.Error(p)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// fakeAnthropic serves the Messages API and answers with texts in order (the last one repeats).
// wantKey, if set, is checked against x-api-key.
func fakeAnthropic(t *testing.T, wantKey string, texts ...string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		if wantKey != "" && r.Header.Get("x-api-key") != wantKey {
			rec.problem("x-api-key = %q, want %q", r.Header.Get("x-api-key"), wantKey)
		}
		i := rec.count() - 1
		if i >= len(texts) {
			i = len(texts) - 1
		}
		writeJSON(w, map[string]any{
			"id": fmt.Sprintf("msg_%d", i), "type": "message", "role": "assistant", "model": "claude-test",
			"content":     []map[string]string{{"type": "text", "text": texts[i]}},
			"stop_reason": "end_turn",
			"usage":       map[string]int{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// fakeGemini serves generateContent with a fixed text answer.
func fakeGemini(t *testing.T, wantKey, text string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		if wantKey != "" && r.Header.Get("x-goog-api-key") != wantKey {
			rec.problem("x-goog-api-key = %q, want %q", r.Header.Get("x-goog-api-key"), wantKey)
		}
		writeJSON(w, map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"role": "model", "parts": []map[string]string{{"text": text}}},
				"finishReason": "STOP",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// fakeOpenAI serves /chat/completions and /models; stream requests get two SSE chunks.
func fakeOpenAI(t *testing.T, wantKey, text string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := rec.add(r)
		if got := r.Header.Get("Authorization"); wantKey != "" && got != "Bearer "+wantKey {
			rec.problem("Authorization = %q, want Bearer %s", got, wantKey)
		}
		switch {
		case r.URL.Path == "/v1/models" || r.URL.Path == "/models":
			writeJSON(w, map[string]any{"object": "list", "data": []map[string]any{{"id": "test-model", "object": "model", "owned_by": "test"}}})
		case len(body) > 0 && json.Valid([]byte(body)) && containsStreamTrue(body):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"id":"ch-1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Hallo "}}]}`)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"id":"ch-1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"content":"Welt"},"finish_reason":"stop"}]}`)
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			writeJSON(w, map[string]any{
				"id": "cmp-1", "object": "chat.completion", "model": "test-model",
				"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
					"message": map[string]any{"role": "assistant", "content": text}}},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func containsStreamTrue(body string) bool {
	var m map[string]any
	return json.Unmarshal([]byte(body), &m) == nil && m["stream"] == true
}
