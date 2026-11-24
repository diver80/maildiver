package mailengine

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/diver80/postdive/internal/secretstore"
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

func TestOutboxStatusPermanentErrorsAndPGPDetection(t *testing.T) {
	if !isPermanentSMTPError(fmt.Errorf("535 5.7.8 Authentication credentials invalid")) {
		t.Fatal("expected 535 error to be classified as permanent")
	}
	if isPermanentSMTPError(fmt.Errorf("dial tcp: i/o timeout")) {
		t.Fatal("dial timeout must not be classified as permanent")
	}

	eng, err := New(Options{
		DBPath:  filepath.Join(t.TempDir(), "ob2.db"),
		Secrets: secretstore.NewMemoryStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	ctx := context.Background()

	// Enqueue with DraftMessageID for a deleted/missing account -> FlushOutbox marks status="failed".
	if err := eng.store.EnqueueOutbox(ctx, OutboxItem{
		ID:             "ob-fail",
		AccountID:      "missing-acc",
		FromEmail:      "alex@example.org",
		Recipients:     []string{"sam@example.org"},
		Subject:        "Drafted Offline",
		DraftMessageID: "draft-123",
		RawEML:         []byte("Subject: Drafted Offline\r\n\r\nHello"),
	}); err != nil {
		t.Fatalf("EnqueueOutbox: %v", err)
	}
	sent, rem := eng.FlushOutbox(ctx)
	if sent != 0 || rem != 1 {
		t.Fatalf("FlushOutbox got sent=%d rem=%d, want 0/1", sent, rem)
	}
	items, err := eng.ListOutbox(ctx)
	if err != nil || len(items) != 1 || items[0].Status != "failed" || items[0].DraftMessageID != "draft-123" {
		t.Fatalf("expected status=failed and draftId kept, got %+v err=%v", items, err)
	}
	if _, err := eng.Call(ctx, "outbox.list", nil); err != nil {
		t.Fatalf("rpc outbox.list: %v", err)
	}
	if _, err := eng.Call(ctx, "outbox.delete", map[string]any{"id": "ob-fail"}); err != nil {
		t.Fatalf("rpc outbox.delete: %v", err)
	}

	// Verify PGP/MIME and PGP armor detection does not masquerade as S/MIME.
	pgpRaw := []byte("From: sender@example.org\r\nSubject: Encrypted\r\nContent-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=\"b1\"\r\n\r\n--b1\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n--b1--\r\n")
	p, err := ParseRawMessage(pgpRaw)
	if err != nil {
		t.Fatalf("ParseRawMessage PGP: %v", err)
	}
	if !p.IsPGP || !p.Encrypted {
		t.Fatalf("expected IsPGP=true Encrypted=true, got %+v", p)
	}
}

func TestProtectedSpecialFoldersGuard(t *testing.T) {
	if !isProtectedSpecialFolder(StoredFolder{Path: "INBOX"}) {
		t.Fatal("INBOX must be protected")
	}
	if !isProtectedSpecialFolder(StoredFolder{Path: "Gesendet", SpecialUse: []string{"sent"}}) {
		t.Fatal("SpecialUse sent must be protected")
	}
	if isProtectedSpecialFolder(StoredFolder{Path: "Projects/Alpha"}) {
		t.Fatal("custom subfolder must be manageable")
	}
}
