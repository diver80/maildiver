package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/native"
)

func TestRPCErrorStatusMapsContractCodes(t *testing.T) {
	cases := map[string]int{
		"read_only":                          http.StatusForbidden,
		"account_not_enabled: acc2":          http.StatusForbidden,
		"account_not_readable":               http.StatusForbidden,
		"send_disabled":                      http.StatusForbidden,
		"feature_disabled:assistant":         http.StatusForbidden,
		"cross_account_move: target in acc3": http.StatusForbidden,
		"not_found: Event ev1":               http.StatusNotFound,
		"invalid_argument: end before start": http.StatusBadRequest,
		"ambiguous: 2 copies":                http.StatusConflict,
		"message_changed":                    http.StatusConflict,
		"conflict":                           http.StatusConflict,
		"something exploded":                 http.StatusBadGateway,
		// A detail that merely mentions a code must not change the status.
		"backend said read_only_mode is weird": http.StatusBadGateway,
	}
	for msg, want := range cases {
		if got := rpcErrorStatus(errors.New(msg)); got != want {
			t.Errorf("rpcErrorStatus(%q) = %d, want %d", msg, got, want)
		}
	}
	if got := rpcErrorStatus(native.ErrThunderbirdDisconnected); got != http.StatusServiceUnavailable {
		t.Errorf("disconnected = %d, want 503", got)
	}
}

type recordingRPC struct {
	mu     sync.Mutex
	params map[string]any
}

func (r *recordingRPC) Connected() bool { return true }

func (r *recordingRPC) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	r.mu.Lock()
	if m, ok := params.(map[string]any); ok {
		r.params = m
	}
	r.mu.Unlock()
	return json.RawMessage(`{"ok":true}`), nil
}

func TestCalendarDeletePassesOccurrenceScope(t *testing.T) {
	rpc := &recordingRPC{}
	cfg := config.Default()
	srv, err := Start(context.Background(), Options{
		DiscoveryPath: filepath.Join(t.TempDir(), "bridge.json"),
		RPC:           rpc,
		ConfigLoader:  func() (config.Config, error) { return cfg, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	do := func(url string) int {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL()+url, nil)
		req.Header.Set("Authorization", "Bearer "+srv.Token())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if code := do("/v1/calendar/events/cal1/ev1?recurrenceId=2026-11-23T09:00:00Z&scope=occurrence"); code != http.StatusOK {
		t.Fatalf("delete occurrence status %d", code)
	}
	rpc.mu.Lock()
	got := rpc.params
	rpc.mu.Unlock()
	if got["recurrenceId"] != "2026-11-23T09:00:00Z" || got["scope"] != "occurrence" || got["id"] != "ev1" {
		t.Fatalf("occurrence params not forwarded: %+v", got)
	}
	if code := do("/v1/calendar/events/cal1/ev1?scope=everything"); code != http.StatusBadRequest {
		t.Fatalf("invalid scope must be rejected, got %d", code)
	}
}

func TestMutatingTimeoutReportsUnknownOutcome(t *testing.T) {
	if !mutatingMethods["compose.send"] || !mutatingMethods["messages.actions"] || mutatingMethods["messages.list"] {
		t.Fatal("unexpected mutatingMethods table")
	}
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/1/actions", nil)
	timeout := fmt.Errorf("rpc: %w", context.DeadlineExceeded)

	rec := httptest.NewRecorder()
	s.writeRPCError(rec, req, "messages.actions", timeout)
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), "outcome_unknown") {
		t.Fatalf("expected 504 outcome_unknown, got %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.writeRPCError(rec, req, "messages.list", timeout)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("a read timeout is a plain gateway error, got %d", rec.Code)
	}

	// The client went away: no outcome_unknown, the client cannot read it anyway.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	rec = httptest.NewRecorder()
	s.writeRPCError(rec, req.WithContext(gone), "messages.actions", timeout)
	if rec.Code == http.StatusGatewayTimeout {
		t.Fatal("a cancelled request must not be reported as outcome_unknown")
	}
}

func TestAIEndpointsIgnoreRequestEndpointAndRespectAssistantFlag(t *testing.T) {
	var evilHits int
	var mu sync.Mutex
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		evilHits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer evil.Close()
	// The configured endpoint is a local fake, so the test never leaves the machine.
	legit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"m","data":[],"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer legit.Close()
	t.Setenv("OPENAI_API_KEY", "secret-key")

	cfg := config.Default()
	cfg.Features.Assistant = false
	cfg.AI.Agent = config.ProviderConfig{Backend: "openai", Model: "m", Endpoint: legit.URL + "/v1"}
	srv, err := Start(context.Background(), Options{
		DiscoveryPath: filepath.Join(t.TempDir(), "bridge.json"),
		RPC:           &recordingRPC{},
		ConfigLoader:  func() (config.Config, error) { return cfg, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	do := func(method, path, body string) int {
		req, _ := http.NewRequest(method, srv.URL()+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+srv.Token())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := http.DefaultClient.Do(req.WithContext(ctx))
		if err != nil {
			return 0
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if code := do(http.MethodPost, "/v1/ai/chat", `{"messages":[{"role":"user","content":"hi"}]}`); code != http.StatusForbidden {
		t.Fatalf("chat with assistant disabled: got %d, want 403", code)
	}
	cfg.Features.Assistant = true
	_ = do(http.MethodPost, "/v1/ai/chat", `{"backend":"openai","endpoint":"`+evil.URL+`","messages":[{"role":"user","content":"hi"}]}`)
	_ = do(http.MethodGet, "/v1/ai/providers?models=1&backend=openai&endpoint="+evil.URL, "")
	mu.Lock()
	hits := evilHits
	mu.Unlock()
	if hits != 0 {
		t.Fatalf("a request-supplied endpoint was contacted %d times", hits)
	}
}
