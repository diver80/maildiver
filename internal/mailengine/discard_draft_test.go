package mailengine

import (
	"context"
	"testing"

	"github.com/diver80/maildiver/internal/backend"
)

// A discarded compose window deletes the draft it auto-saved, on the server and in the cache.
func TestComposeDiscardDraftDeletesAutoSavedDraft(t *testing.T) {
	ctx := context.Background()
	eng, acc, _, _, _, cleanup := setupTestEngineWithIMAPAndSMTP(t, nil)
	defer cleanup()

	raw, err := eng.Call(ctx, "compose.draft", backend.ComposeRequest{
		AccountID: acc.ID,
		To:        []string{"max.muster@example.org"},
		Subject:   "Auto-Save",
		Body:      "Noch nicht fertig",
	})
	if err != nil {
		t.Fatalf("compose.draft: %v", err)
	}
	draftID := backend.ParseComposeResult(raw).DraftID
	if draftID == "" {
		t.Fatalf("no draft id in %s", raw)
	}
	if n := countFolder(t, eng, acc.ID, "Drafts"); n != 1 {
		t.Fatalf("want 1 draft, got %d", n)
	}
	if _, err := eng.Call(ctx, "compose.discardDraft", map[string]any{"accountId": acc.ID, "draftId": draftID}); err != nil {
		t.Fatalf("compose.discardDraft: %v", err)
	}
	if _, err := eng.SyncAccount(ctx, acc.ID, false); err != nil {
		t.Fatal(err)
	}
	if n := countFolder(t, eng, acc.ID, "Drafts"); n != 0 {
		t.Fatalf("discarded draft must be gone from Drafts (server and cache), got %d", n)
	}
	if _, err := eng.Call(ctx, "compose.discardDraft", map[string]any{"accountId": acc.ID}); err == nil {
		t.Fatal("a missing draftId must be rejected")
	}
}
