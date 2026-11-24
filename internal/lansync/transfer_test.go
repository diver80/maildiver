package lansync

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

func TestLANQREncryptedOneShotTransfer(t *testing.T) {
	srcSecrets := secretstore.NewMemoryStore()
	_ = srcSecrets.Set("account:acc-1", "super-secret-imap-pass")

	srcCfg := config.Default()
	srcCfg.Language = "de"
	srcCfg.App.AllowedImageSenders = []string{"trusted@example.org"}
	srcCfg.Direct.Accounts = []config.DirectAccount{{
		ID:       "acc-1",
		Name:     "Frank",
		Email:    "frank@example.org",
		IMAPHost: "imap.example.org",
		IMAPPort: 993,
		IMAPTLS:  "tls",
		SMTPHost: "smtp.example.org",
		SMTPPort: 465,
		SMTPTLS:  "tls",
		Enabled:  true,
	}}

	bundle := BuildBundle(srcCfg, srcSecrets)
	sess, err := StartSession(context.Background(), bundle, "127.0.0.1", 30*time.Second)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()

	if !strings.HasPrefix(sess.URI, "postdive-pair://v1?") {
		t.Fatalf("unexpected pairing URI: %q", sess.URI)
	}
	if len(sess.QRPNG) < 200 {
		t.Fatalf("expected valid PNG QR code bytes, got %d", len(sess.QRPNG))
	}

	// Receiver device imports from pairing URI.
	dstSecrets := secretstore.NewMemoryStore()
	dstCfg, added, err := ImportFromPairingURI(context.Background(), sess.URI, config.Default(), dstSecrets)
	if err != nil {
		t.Fatalf("ImportFromPairingURI: %v", err)
	}
	if added != 1 || len(dstCfg.Direct.Accounts) != 1 || dstCfg.Direct.Accounts[0].Email != "frank@example.org" {
		t.Fatalf("unexpected imported accounts: added=%d %+v", added, dstCfg.Direct.Accounts)
	}
	// A fresh phone starts read-only with no account enabled: the transferred one must be usable.
	if dstCfg.Mailbox.ReadOnly || !dstCfg.Mailbox.CanWrite("acc-1") {
		t.Fatalf("transferred account must be writable: readOnly=%v triage=%v", dstCfg.Mailbox.ReadOnly, dstCfg.Mailbox.TriageAccountIDs)
	}
	gotPass, err := dstSecrets.Get("account:acc-1")
	if err != nil || gotPass != "super-secret-imap-pass" {
		t.Fatalf("expected secret transferred securely, got %q err=%v", gotPass, err)
	}
	if !sess.Completed() {
		t.Fatal("session must be marked completed after one-shot fetch")
	}

	// Replay attempt must fail (one-shot transfer).
	if _, _, err := ImportFromPairingURI(context.Background(), sess.URI, config.Default(), secretstore.NewMemoryStore()); err == nil {
		t.Fatal("replay of one-shot LAN pairing URI must be rejected")
	}

	// Non-LAN public IP must be rejected.
	if _, _, err := ImportFromPairingURI(context.Background(), "postdive-pair://v1?url=https://8.8.8.8/transfer&t=abc&k=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", config.Default(), nil); err == nil {
		t.Fatal("public internet URL must be rejected for LAN transfer")
	}
}
