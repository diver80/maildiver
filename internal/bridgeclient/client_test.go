package bridgeclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/omathunder/internal/ai"
	"github.com/diver80/omathunder/internal/bridge"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/native"
	"github.com/diver80/omathunder/internal/triage"
)

type stubRPC struct{}

func (stubRPC) Connected() bool { return true }
func (stubRPC) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	switch method {
	case "accounts.list":
		return json.RawMessage(`{"accounts":[{"id":"acc1","name":"Frank","type":"imap"}]}`), nil
	case "calendar.events":
		return json.RawMessage(`{"supported":true,"events":[{"id":"e1","calendarId":"c1","title":"Standup","start":"2026-11-18T09:00:00Z","end":"2026-11-18T09:30:00Z"}]}`), nil
	default:
		return json.RawMessage(`{}`), nil
	}
}

func TestDiscoverAndTypedCalls(t *testing.T) {
	tmp := t.TempDir()
	discPath := filepath.Join(tmp, "bridge.json")

	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "c1",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{
				{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "Alles klar."}},
			},
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

	srv, err := bridge.Start(ctx, bridge.Options{
		Port:          0,
		DiscoveryPath: discPath,
		RPC:           stubRPC{},
		Events:        native.NewEventHub(),
		Store:         triage.NewStoreAt(tmp),
		ConfigLoader:  func() (config.Config, error) { return cfg, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	client, info, err := Discover(discPath)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if info.URL != srv.URL() {
		t.Fatalf("unexpected url %s vs %s", info.URL, srv.URL())
	}

	health, err := client.Health(ctx)
	if err != nil || !health.ThunderbirdConnected {
		t.Fatalf("Health failed: %+v err=%v", health, err)
	}
	accs, err := client.Accounts(ctx)
	if err != nil || len(accs) != 1 || accs[0].ID != "acc1" {
		t.Fatalf("Accounts failed: %+v err=%v", accs, err)
	}
	cal, err := client.CalendarEvents(ctx, "", "", nil, 10)
	if err != nil || !cal.Supported || len(cal.Events) != 1 || cal.Events[0].Title != "Standup" {
		t.Fatalf("CalendarEvents failed: %+v err=%v", cal, err)
	}
	chat, err := client.AIChat(ctx, ai.ChatRequest{
		Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: "Status?"}},
	})
	if err != nil || len(chat.Choices) == 0 || chat.Choices[0].Message.ContentString() != "Alles klar." {
		t.Fatalf("AIChat failed: %+v err=%v", chat, err)
	}
}
