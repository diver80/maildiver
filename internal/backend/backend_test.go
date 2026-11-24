package backend

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/diver80/maildiver/internal/ai"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/events"
	"github.com/diver80/maildiver/internal/jobs"
	"github.com/diver80/maildiver/internal/triage"
)

// fakeEngine records calls and answers like the mail engine.
type fakeEngine struct {
	mu     sync.Mutex
	params map[string]map[string]any
	err    error
	block  bool
}

func (f *fakeEngine) Connected() bool { return true }

func (f *fakeEngine) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	f.mu.Lock()
	if f.params == nil {
		f.params = map[string]map[string]any{}
	}
	f.params[method] = p
	err, block := f.err, f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	switch method {
	case "messages.get":
		if p["decrypt"] == true {
			return json.RawMessage(`{"id":"7","bodyText":"Klartext","security":{"encrypted":true,"decryption":"success"}}`), nil
		}
		return json.RawMessage(`{"id":"7","bodyText":"","security":{"encrypted":true,"decryption":"skipped"}}`), nil
	case "messages.thread":
		return json.RawMessage(`{"messages":[{"id":"1","subject":"Re: a"},{"id":"2","subject":"Re: Re: a"}]}`), nil
	}
	return json.RawMessage(`{"ok":true}`), nil
}

// CallHost lets the job runner use the same fake.
func (f *fakeEngine) CallHost(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if method == "host.triage.collect" {
		return json.RawMessage(`{"messages":[{"id":"1","threadId":"t@x","accountId":"acc1","subject":"Hallo","unread":true}],"scannedFolders":1}`), nil
	}
	return json.RawMessage(`{}`), nil
}

func (f *fakeEngine) got(method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.params[method]
}

func newClient(t *testing.T, eng *fakeEngine, cfg config.Config) (*Client, *events.Hub) {
	t.Helper()
	hub := events.NewHub()
	load := func() (config.Config, error) { return cfg, nil }
	store := triage.NewStoreAt(t.TempDir())
	return New(Options{
		Engine:     eng,
		Events:     hub,
		Store:      store,
		Jobs:       jobs.New(context.Background(), eng, hub, store, load),
		LoadConfig: load,
		Version:    "test",
	}), hub
}

func TestMessagesDecryptOnlyOnRequest(t *testing.T) {
	cfg := config.Default()
	cfg.Mailbox.ReadOnly = false
	eng := &fakeEngine{}
	c, _ := newClient(t, eng, cfg)
	ctx := context.Background()

	d, err := c.Message(ctx, "7", "both")
	if err != nil || d.Security.Readable() || eng.got("messages.get")["decrypt"] != false || eng.got("messages.get")["format"] != "both" {
		t.Fatalf("Message must not decrypt: %+v %v err=%v", d, eng.got("messages.get"), err)
	}
	d, err = c.MessageWithOptions(ctx, "7", MessageOptions{Decrypt: true})
	if err != nil || !d.Security.Readable() || d.BodyText != "Klartext" {
		t.Fatalf("decrypted mail: %+v err=%v", d, err)
	}
	msgs, err := c.MessageThread(ctx, "7", 500)
	if err != nil || len(msgs) != 2 || eng.got("messages.thread")["limit"] != float64(50) {
		t.Fatalf("thread: %+v %v err=%v", msgs, eng.got("messages.thread"), err)
	}
	read := true
	if _, err := c.MessageActions(ctx, "7", MessageActionsRequest{Read: &read, ExpectedMessageID: "m@x"}); err != nil {
		t.Fatal(err)
	}
	if p := eng.got("messages.actions"); p["id"] != "7" || p["read"] != true || p["expectedMessageId"] != "m@x" {
		t.Fatalf("actions params: %v", p)
	}
}

func TestErrorsKeepTheirCodes(t *testing.T) {
	cfg := config.Default()
	cfg.Mailbox.ReadOnly = false
	eng := &fakeEngine{err: errors.New("message_changed: the mail is a different one now")}
	c, _ := newClient(t, eng, cfg)
	_, err := c.MessageActions(context.Background(), "7", MessageActionsRequest{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "message_changed" || ErrorCode(err) != "message_changed" {
		t.Fatalf("error code: %v", err)
	}

	// A change that ran out of time may still have happened.
	eng = &fakeEngine{block: true}
	c, _ = newClient(t, eng, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.ComposeDraft(ctx, ComposeRequest{Body: "x"}); !IsOutcomeUnknown(err) {
		t.Fatalf("timed-out draft: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	if _, err := c.Accounts(ctx2); err == nil || IsOutcomeUnknown(err) {
		t.Fatalf("a timed-out read is no unknown outcome: %v", err)
	}
	if eng.got("compose.draft")["idempotencyKey"] == "" {
		t.Fatal("drafts carry an idempotency key")
	}
}

func TestReadOnlyAndAssistantGates(t *testing.T) {
	cfg := config.Default()
	cfg.Mailbox.ReadOnly = true
	cfg.Features.Assistant = false
	eng := &fakeEngine{}
	c, _ := newClient(t, eng, cfg)
	ctx := context.Background()
	if _, err := c.ComposeSend(ctx, ComposeRequest{To: []string{"a@b.de"}}); ErrorCode(err) != "read_only" || eng.got("compose.send") != nil {
		t.Fatalf("send in read-only: %v", err)
	}
	if _, err := c.MessageActions(ctx, "m1", MessageActionsRequest{Archive: true}); ErrorCode(err) != "read_only" || eng.got("messages.actions") != nil {
		t.Fatalf("archive in read-only: %v", err)
	}
	if _, err := c.ComposeDraft(ctx, ComposeRequest{To: []string{"a@b.de"}}); ErrorCode(err) != "read_only" || eng.got("compose.draft") != nil {
		t.Fatalf("draft in read-only: %v", err)
	}
	if _, err := c.FilterUpsert(ctx, FilterUpsertRequest{AccountID: "a"}); ErrorCode(err) != "read_only" {
		t.Fatalf("filter in read-only: %v", err)
	}
	if _, err := c.AIAnalyze(ctx, ai.AnalyzeRequest{Mode: "summarize"}); ErrorCode(err) != "feature_disabled" {
		t.Fatalf("analyze with the assistant off: %v", err)
	}
	h, err := c.Health(ctx)
	if err != nil || !h.Connected || !h.ReadOnly || h.Version != "test" {
		t.Fatalf("health: %+v err=%v", h, err)
	}
	if _, err := (*Client)(nil).Accounts(ctx); !errors.Is(err, ErrOffline) {
		t.Fatalf("nil client: %v", err)
	}
}

func TestJobsAndEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Triage = true
	cfg.Mailbox.TriageAccountIDs = []string{"acc1"}
	eng := &fakeEngine{}
	c, hub := newClient(t, eng, cfg)
	id, ch := c.Subscribe()
	defer c.Unsubscribe(id)

	scanID, err := c.StartScan(context.Background(), "")
	if err != nil || scanID == "" {
		t.Fatalf("StartScan = %q, %v", scanID, err)
	}
	deadline := time.After(3 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-ch:
			done = ev.Event == "triage.scan.finished"
		case <-deadline:
			t.Fatal("no scan event")
		}
	}
	if st, err := c.opts.Store.Load(); err != nil || len(st.Classifications) != 1 {
		t.Fatalf("state after scan: %+v err=%v", st, err)
	}
	if _, err := c.ArchivePreview(context.Background(), jobs.ArchiveRequest{}); ErrorCode(err) != "invalid_argument" {
		t.Fatalf("archive without account: %v", err)
	}
	hub.Publish("mail.new", map[string]any{"count": 1})
	select {
	case ev := <-ch:
		if ev.Event != "mail.new" {
			t.Fatalf("event = %s", ev.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("no mail.new event")
	}
}
