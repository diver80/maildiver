package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/triage"
)

type stubBridgeRPC struct{}

func (stubBridgeRPC) Connected() bool { return true }
func (stubBridgeRPC) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	switch method {
	case "messages.list":
		return json.RawMessage(`{"messages":[{"id":"m1","subject":"Angebot Q4","from":"alice@acme.de","date":"2026-11-18T08:00:00Z","unread":true}],"total":1}`), nil
	default:
		return json.RawMessage(`{}`), nil
	}
}

func TestRunnerExecutesToolLoopAgainstLiveBridge(t *testing.T) {
	tmp := t.TempDir()
	store := triage.NewStoreAt(tmp)

	var aiCalls atomic.Int32
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := aiCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "cmp-1",
				"object": "chat.completion",
				"model":  "gpt-4o-mini",
				"choices": []map[string]any{{
					"index":         0,
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []map[string]any{{
							"id":   "tc-1",
							"type": "function",
							"function": map[string]any{
								"name":      "search_messages",
								"arguments": `{"unread":true,"limit":5}`,
							},
						}},
					},
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "cmp-2",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "Du hast 1 ungelesene Mail: Angebot Q4.",
				},
			}},
		})
	}))
	defer aiSrv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	cfg := config.Default()
	cfg.AI.Agent = config.ProviderConfig{
		Backend:  "openai",
		Model:    "gpt-4o-mini",
		Endpoint: aiSrv.URL + "/v1",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bc := backend.New(backend.Options{
		Engine:     stubBridgeRPC{},
		Events:     events.NewHub(),
		Store:      store,
		LoadConfig: func() (config.Config, error) { return cfg, nil },
	})

	runner := &Runner{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		Backend:      bc,
		Store:        store,
	}

	var events []Event
	var answer string
	err := runner.Run(ctx, RunRequest{
		Messages: []Message{{Role: "user", Content: "Was ist ungelesen?"}},
	}, func(ev Event) {
		events = append(events, ev)
		if ev.Kind == EventDelta {
			answer += ev.Text
		}
	})
	if err != nil {
		t.Fatalf("Runner.Run failed: %v", err)
	}

	var sawToolCall, sawToolResult, sawDone bool
	for _, ev := range events {
		switch ev.Kind {
		case EventToolCall:
			if ev.ToolName == "search_messages" {
				sawToolCall = true
			}
		case EventToolResult:
			if ev.ToolName == "search_messages" {
				sawToolResult = true
			}
		case EventDone:
			sawDone = true
		}
	}
	if !sawToolCall || !sawToolResult || !sawDone {
		t.Fatalf("missing expected events: call=%v result=%v done=%v events=%+v", sawToolCall, sawToolResult, sawDone, events)
	}
	if answer == "" {
		t.Fatal("expected non-empty final answer")
	}
}

func TestBuildSystemPromptLocalization(t *testing.T) {
	cfg := config.Default()

	dePrompt := BuildSystemPrompt("de", cfg, true, "", "")
	if !strings.Contains(dePrompt, "MailDiver KI-Assistent") {
		t.Fatalf("expected German assistant title in DE prompt, got %q", dePrompt)
	}
	if strings.Contains(strings.ToLower(dePrompt), "co-pilot") {
		t.Fatalf("unexpected legacy Co-Pilot label in DE prompt: %q", dePrompt)
	}

	enPrompt := BuildSystemPrompt("en", cfg, false, "acc1", "m1")
	if !strings.Contains(enPrompt, "MailDiver AI Assistant") {
		t.Fatalf("expected English assistant title in EN prompt, got %q", enPrompt)
	}
	if strings.Contains(strings.ToLower(enPrompt), "co-pilot") {
		t.Fatalf("unexpected legacy Co-Pilot label in EN prompt: %q", enPrompt)
	}
	if !strings.Contains(enPrompt, "the mail engine is offline") {
		t.Fatalf("expected English offline notice in EN prompt, got %q", enPrompt)
	}
}
