package copilot

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/diver80/omathunder/internal/bridge"
	"github.com/diver80/omathunder/internal/bridgeclient"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/native"
	"github.com/diver80/omathunder/internal/triage"
)

// encryptedRPC serves one encrypted mail: its text only when the call asks for decryption.
type encryptedRPC struct {
	mu       sync.Mutex
	decrypts []bool
}

func (*encryptedRPC) Connected() bool { return true }
func (e *encryptedRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method != "messages.get" {
		return json.RawMessage(`{}`), nil
	}
	p, _ := params.(map[string]any)
	decrypt := p["decrypt"] == true
	e.mu.Lock()
	e.decrypts = append(e.decrypts, decrypt)
	e.mu.Unlock()
	if decrypt {
		return json.RawMessage(`{"id":"7","subject":"Vertrag","bodyText":"GEHEIMER INHALT","security":{"encrypted":true,"decryption":"success"}}`), nil
	}
	return json.RawMessage(`{"id":"7","subject":"Vertrag","bodyText":"","security":{"encrypted":true,"decryption":"skipped"}}`), nil
}

// The model's own get_message never decrypts; only the mail the user has open is decrypted for
// the context snapshot.
func TestCopilotDecryptsOnlyTheOpenMail(t *testing.T) {
	tmp := t.TempDir()
	discPath := filepath.Join(tmp, "bridge.json")
	cfg := config.Default()
	rpc := &encryptedRPC{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	br, err := bridge.Start(ctx, bridge.Options{
		DiscoveryPath: discPath,
		RPC:           rpc,
		Events:        native.NewEventHub(),
		Store:         triage.NewStoreAt(tmp),
		ConfigLoader:  func() (config.Config, error) { return cfg, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer br.Close()
	bc, _, err := bridgeclient.Discover(discPath)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{ConfigLoader: func() (config.Config, error) { return cfg, nil }, Bridge: bc}

	out := r.executeTool(ctx, &runState{bc: bc, cfg: cfg, lang: "de"}, "get_message", `{"id":"7"}`)
	if strings.Contains(out.content, "GEHEIMER INHALT") || !strings.Contains(out.content, "encrypted email: content not available") {
		t.Fatalf("get_message must not decrypt: %s", out.content)
	}

	snap := r.collectContextSnapshot(ctx, bc, "de", "", "7")
	if !strings.Contains(snap, "GEHEIMER INHALT") {
		t.Fatalf("the open mail should be in the snapshot: %s", snap)
	}
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	if len(rpc.decrypts) != 2 || rpc.decrypts[0] || !rpc.decrypts[1] {
		t.Fatalf("decrypt flags = %v, want [false true]", rpc.decrypts)
	}
}
