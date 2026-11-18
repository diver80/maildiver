package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/native"
	"github.com/diver80/omathunder/internal/triage"
)

type fakeRPC struct {
	connected bool
	calls     []string
	fn        func(method string, params any) (json.RawMessage, error)
}

func (f *fakeRPC) Connected() bool { return f.connected }

func (f *fakeRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.calls = append(f.calls, method)
	if f.fn != nil {
		return f.fn(method, params)
	}
	return json.RawMessage(`{}`), nil
}

func TestBridgeAuthDiscoveryAndRoutes(t *testing.T) {
	tmp := t.TempDir()
	discPath := filepath.Join(tmp, "bridge.json")
	events := native.NewEventHub()
	store := triage.NewStoreAt(tmp)

	cfg := config.Default()
	cfg.Mailbox.ReadOnly = false
	cfg.Bridge.AllowSend = false

	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, `data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"Hi!"}}]}`)
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, `data: [DONE]`)
			_, _ = fmt.Fprintln(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "c1",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{
				{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "Antwort"}},
			},
		})
	}))
	defer aiSrv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	cfg.AI.Agent = config.ProviderConfig{
		Backend:  "openai",
		Model:    "gpt-4o-mini",
		Endpoint: aiSrv.URL + "/v1",
	}

	rpc := &fakeRPC{
		connected: true,
		fn: func(method string, _ any) (json.RawMessage, error) {
			switch method {
			case "accounts.list":
				return json.RawMessage(`{"accounts":[{"id":"acc1","name":"Work"}]}`), nil
			case "calendar.events":
				return json.RawMessage(`{"supported":true,"events":[{"id":"ev1","title":"Standup"}]}`), nil
			case "contacts.search":
				return json.RawMessage(`{"supported":true,"addressBooks":[{"id":"ab1","name":"Persönlich","readOnly":false}],"contacts":[{"id":"ct1","displayName":"Alice Weber","email":"alice@acme.de"}]}`), nil
			case "contacts.upsert":
				return json.RawMessage(`{"ok":true,"action":"imported","contact":{"id":"ct2","displayName":"Bob"}}`), nil
			case "compose.send":
				return json.RawMessage(`{"ok":true}`), nil
			default:
				return json.RawMessage(`{"ok":true}`), nil
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := Start(ctx, Options{
		Port:          0,
		DiscoveryPath: discPath,
		RPC:           rpc,
		Events:        events,
		Store:         store,
		ConfigLoader:  func() (config.Config, error) { return cfg, nil },
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Close()

	// Discovery file must exist with 0600 permissions.
	st, err := os.Stat(discPath)
	if err != nil {
		t.Fatalf("missing bridge.json: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600 on bridge.json, got %o", perm)
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// Unauthenticated request -> 401.
	unauthResp, err := client.Get(srv.URL() + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", unauthResp.StatusCode)
	}

	// Authenticated /v1/accounts -> 200.
	req, _ := http.NewRequest(http.MethodGet, srv.URL()+"/v1/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+srv.Token())
	accResp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var accBody map[string]any
	_ = json.NewDecoder(accResp.Body).Decode(&accBody)
	_ = accResp.Body.Close()
	if accResp.StatusCode != http.StatusOK || accBody["ok"] != true {
		t.Fatalf("unexpected /v1/accounts response: %d %+v", accResp.StatusCode, accBody)
	}

	// Authenticated /v1/calendar/events -> 200.
	calReq, _ := http.NewRequest(http.MethodGet, srv.URL()+"/v1/calendar/events", nil)
	calReq.Header.Set("Authorization", "Bearer "+srv.Token())
	calResp, err := client.Do(calReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = calResp.Body.Close()
	if calResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /v1/calendar/events, got %d", calResp.StatusCode)
	}

	// Authenticated /v1/contacts & /v1/contacts/upsert -> 200.
	ctReq, _ := http.NewRequest(http.MethodGet, srv.URL()+"/v1/contacts?q=alice", nil)
	ctReq.Header.Set("Authorization", "Bearer "+srv.Token())
	ctResp, err := client.Do(ctReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = ctResp.Body.Close()
	if ctResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /v1/contacts, got %d", ctResp.StatusCode)
	}
	ctUpReq, _ := http.NewRequest(http.MethodPost, srv.URL()+"/v1/contacts/upsert", bytes.NewBufferString(`{"mode":"create","contact":{"displayName":"Bob","email":"bob@acme.de"}}`))
	ctUpReq.Header.Set("Authorization", "Bearer "+srv.Token())
	ctUpResp, err := client.Do(ctUpReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = ctUpResp.Body.Close()
	if ctUpResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /v1/contacts/upsert, got %d", ctUpResp.StatusCode)
	}

	// /v1/compose/send blocked by default -> 403.
	sendReq, _ := http.NewRequest(http.MethodPost, srv.URL()+"/v1/compose/send", bytes.NewBufferString(`{"to":["a@b.de"],"subject":"Hi"}`))
	sendReq.Header.Set("Authorization", "Bearer "+srv.Token())
	sendResp, err := client.Do(sendReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = sendResp.Body.Close()
	if sendResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when allowSend=false, got %d", sendResp.StatusCode)
	}

	// /v1/ai/chat JSON & SSE streaming -> 200.
	chatReq, _ := http.NewRequest(http.MethodPost, srv.URL()+"/v1/ai/chat", bytes.NewBufferString(`{"stream":true,"messages":[{"role":"user","content":"Hi"}]}`))
	chatReq.Header.Set("Authorization", "Bearer "+srv.Token())
	chatResp, err := client.Do(chatReq)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(chatResp.Body)
	var seenDone bool
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "[DONE]") {
			seenDone = true
			break
		}
	}
	_ = chatResp.Body.Close()
	if chatResp.StatusCode != http.StatusOK || !seenDone {
		t.Fatalf("expected SSE chat completion with [DONE], status=%d seenDone=%v", chatResp.StatusCode, seenDone)
	}

	// /v1/events SSE stream receives live events.
	sseReq, _ := http.NewRequest(http.MethodGet, srv.URL()+"/v1/events?token="+srv.Token(), nil)
	sseResp, err := client.Do(sseReq)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		events.Publish("calendar.changed", map[string]any{"kind": "add"})
	}()
	evScanner := bufio.NewScanner(sseResp.Body)
	var seenCalendar bool
	for evScanner.Scan() {
		if strings.Contains(evScanner.Text(), "event: calendar.changed") {
			seenCalendar = true
			break
		}
	}
	_ = sseResp.Body.Close()
	if !seenCalendar {
		t.Fatal("expected calendar.changed on SSE /v1/events")
	}

	// Close cleans up discovery file.
	client.CloseIdleConnections()
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(discPath); !os.IsNotExist(err) {
		t.Fatalf("expected bridge.json removed after Close, got err=%v", err)
	}
}
