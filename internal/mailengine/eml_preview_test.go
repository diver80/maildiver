package mailengine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveMessageEMLAndReadAttachmentPreview(t *testing.T) {
	rawEML := strings.Join([]string{
		"From: Alice <alice@example.com>",
		"To: Bob <bob@example.com>",
		"Subject: Quartalsbericht Q4 / Entwurf",
		"Date: Fri, 21 Nov 2026 10:00:00 +0100",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=\"bnd-outer\"",
		"",
		"--bnd-outer",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Hallo Bob, anbei die Notiz.",
		"--bnd-outer",
		"Content-Type: text/plain; charset=utf-8; name=\"notiz.txt\"",
		"Content-Disposition: attachment; filename=\"notiz.txt\"",
		"",
		"Interne Notiz Zeile 1",
		"--bnd-outer",
		"Content-Type: message/rfc822; name=\"weitergeleitet.eml\"",
		"Content-Disposition: attachment; filename=\"weitergeleitet.eml\"",
		"",
		"From: Carol <carol@example.com>",
		"To: Alice <alice@example.com>",
		"Subject: Ursprungs-Mail",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Inhalt der eingebetteten Mail",
		"--bnd-outer--",
		"",
	}, "\r\n")

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	_ = os.MkdirAll(filepath.Join(tmpHome, "Downloads"), 0o755)

	st, err := OpenStore(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()

	eng := &Engine{store: st}
	ctx := context.Background()
	sm := StoredMessage{
		ID:        "msg-eml-1",
		AccountID: "acc-1",
		Subject:   "Quartalsbericht Q4 / Entwurf",
		RawEML:    []byte(rawEML),
	}
	if err := st.UpsertMessage(ctx, sm); err != nil {
		t.Fatalf("UpsertMessage: %v", err)
	}

	savedPath, err := eng.SaveMessageEMLToDownloads(ctx, "msg-eml-1")
	if err != nil {
		t.Fatalf("SaveMessageEMLToDownloads: %v", err)
	}
	if filepath.Ext(savedPath) != ".eml" {
		t.Fatalf("expected .eml extension, got %s", savedPath)
	}
	if gotBytes, rerr := os.ReadFile(savedPath); rerr != nil || len(gotBytes) != len(rawEML) {
		t.Fatalf("exported .eml mismatch: err=%v len=%d/%d", rerr, len(gotBytes), len(rawEML))
	}

	txtPrev, err := eng.ReadAttachmentPreview(ctx, "msg-eml-1", "", "notiz.txt")
	if err != nil {
		t.Fatalf("ReadAttachmentPreview(txt): %v", err)
	}
	if string(txtPrev.Data) != "Interne Notiz Zeile 1" {
		t.Fatalf("unexpected text preview payload: %q", string(txtPrev.Data))
	}

	emlPrev, err := eng.ReadAttachmentPreview(ctx, "msg-eml-1", "", "weitergeleitet.eml")
	if err != nil {
		t.Fatalf("ReadAttachmentPreview(eml): %v", err)
	}
	if emlPrev.ParsedEML == nil || emlPrev.ParsedEML.Subject != "Ursprungs-Mail" || !strings.Contains(emlPrev.ParsedEML.BodyText, "eingebetteten Mail") {
		t.Fatalf("expected parsed nested .eml preview, got %+v", emlPrev.ParsedEML)
	}
}
