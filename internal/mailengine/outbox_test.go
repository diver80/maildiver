package mailengine

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/diver80/maildiver/internal/secretstore"
)

func TestOutboxEnqueueRetryAndTransientDetection(t *testing.T) {
	if !isTransientNetworkError(fmt.Errorf("dial tcp: lookup smtp.example.com: no such host")) {
		t.Fatal("expected DNS error to be classified as transient")
	}
	if isTransientNetworkError(fmt.Errorf("535 5.7.8 Authentication credentials invalid")) {
		t.Fatal("auth error must NOT be classified as transient")
	}

	eng, err := New(Options{
		DBPath:  filepath.Join(t.TempDir(), "ob.db"),
		Secrets: secretstore.NewMemoryStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	ctx := context.Background()
	err = eng.store.EnqueueOutbox(ctx, OutboxItem{
		ID:         "ob-1",
		AccountID:  "acc-1",
		FromEmail:  "frank@example.org",
		Recipients: []string{"bob@example.org"},
		Subject:    "Offline Mail",
		RawEML:     []byte("Subject: Offline Mail\r\n\r\nHi Bob"),
		LastError:  "no such host",
	})
	if err != nil {
		t.Fatalf("EnqueueOutbox: %v", err)
	}
	items, err := eng.store.ListOutbox(ctx)
	if err != nil || len(items) != 1 || items[0].Subject != "Offline Mail" {
		t.Fatalf("ListOutbox got %+v err=%v", items, err)
	}
	if err := eng.store.DeleteOutbox(ctx, "ob-1"); err != nil {
		t.Fatalf("DeleteOutbox: %v", err)
	}
	items, _ = eng.store.ListOutbox(ctx)
	if len(items) != 0 {
		t.Fatalf("expected empty outbox after delete, got %d", len(items))
	}
}
