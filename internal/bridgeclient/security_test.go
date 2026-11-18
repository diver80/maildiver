package bridgeclient

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/diver80/omathunder/internal/bridge"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/native"
	"github.com/diver80/omathunder/internal/triage"
)

// recordingRPC answers like the add-on and records the params of each call.
type recordingRPC struct {
	mu     sync.Mutex
	params map[string]map[string]any
}

func (r *recordingRPC) Connected() bool { return true }

func (r *recordingRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	r.mu.Lock()
	r.params[method] = p
	r.mu.Unlock()
	switch method {
	case "messages.get":
		if p["decrypt"] == true {
			return json.RawMessage(`{"id":"7","subject":"Geheim","bodyText":"Klartext","security":{"encrypted":true,"decryption":"success","signed":true}}`), nil
		}
		return json.RawMessage(`{"id":"7","subject":"Geheim","bodyText":"","security":{"encrypted":true,"decryption":"skipped","signed":false}}`), nil
	case "compose.draft":
		return json.RawMessage(`{"ok":true,"windowOpened":true,"reason":"encrypted_reply","security":{"technology":"OpenPGP","signed":true,"encrypted":true}}`), nil
	}
	return json.RawMessage(`{"opened":true,"messages":[]}`), nil
}

func (r *recordingRPC) got(method string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.params[method]
}

// Mails are decrypted only on request; the reading pane asks, everything else gets them encrypted.
func TestDecryptOnlyOnRequest(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	cfg.Mailbox.ReadOnly = false
	rpc := &recordingRPC{params: map[string]map[string]any{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	discPath := filepath.Join(tmp, "bridge.json")
	srv, err := bridge.Start(ctx, bridge.Options{
		DiscoveryPath: discPath,
		RPC:           rpc,
		Events:        native.NewEventHub(),
		Store:         triage.NewStoreAt(tmp),
		ConfigLoader:  func() (config.Config, error) { return cfg, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	client, _, err := Discover(discPath)
	if err != nil {
		t.Fatal(err)
	}

	d, err := client.Message(ctx, "7", "both")
	if err != nil {
		t.Fatal(err)
	}
	if got := rpc.got("messages.get"); got["decrypt"] != false || got["format"] != "both" {
		t.Errorf("Message must not decrypt: %v", got)
	}
	if !d.Security.Encrypted || d.Security.Readable() || d.BodyText != "" {
		t.Errorf("encrypted mail without decrypt: %+v", d)
	}

	d, err = client.MessageWithOptions(ctx, "7", MessageOptions{Format: "text", Decrypt: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := rpc.got("messages.get"); got["decrypt"] != true {
		t.Errorf("Decrypt: true must reach the add-on: %v", got)
	}
	if !d.Security.Readable() || !d.Security.Signed || d.BodyText != "Klartext" {
		t.Errorf("decrypted mail: %+v", d)
	}

	if _, err := client.MessageThread(ctx, "7", 5); err != nil {
		t.Fatal(err)
	}
	if got := rpc.got("messages.thread"); got["decrypt"] != false {
		t.Errorf("threads are never decrypted by the client: %v", got)
	}

	if err := client.OpenMessage(ctx, "7"); err != nil {
		t.Fatal(err)
	}
	if got := rpc.got("messages.open"); got["id"] != "7" {
		t.Errorf("messages.open params: %v", got)
	}

	raw, err := client.ComposeDraft(ctx, ComposeRequest{ReplyToMessageID: "7", Body: "x", Sign: "on"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rpc.got("compose.draft"); got["sign"] != "on" {
		t.Errorf("sign must reach the add-on: %v", got)
	}
	res := ParseComposeResult(raw)
	if !res.WindowOpened || res.Reason != "encrypted_reply" || res.Security == nil || !res.Security.Encrypted || res.Security.Technology != "OpenPGP" {
		t.Errorf("compose result: %+v", res)
	}
	if got := ParseComposeResult(nil); got.WindowOpened || got.Security != nil {
		t.Errorf("empty result: %+v", got)
	}
}
