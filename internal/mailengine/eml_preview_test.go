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

func TestParseCSVAttachmentRFC2231AndMalformedDisposition(t *testing.T) {
	// 1) Long RFC 2231 split CSV filename (as sent by Thunderbird / standard clients)
	raw1 := strings.Join([]string{
		"From: Frank <frank@example.de>",
		"To: Frank <frank@example.de>",
		"Subject: RE: UAT2",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=\"bnd-csv\"",
		"",
		"--bnd-csv",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Hi Romain, see attached CSV.",
		"--bnd-csv",
		"Content-Type: text/csv; charset=UTF-8; name=\"2026-11-22_helvetia_fr_deactivated_with_confluence_entitlement.csv\"",
		"Content-Disposition: attachment;",
		" filename*0=\"2026-11-22_helvetia_fr_deactivated_with_confluence_\";",
		" filename*1=\"entitlement.csv\"",
		"",
		"user,status",
		"alice@example.fr,deactivated",
		"--bnd-csv--",
		"",
	}, "\r\n")

	parsed, err := ParseRawMessage([]byte(raw1))
	if err != nil {
		t.Fatalf("ParseRawMessage(raw1): %v", err)
	}
	wantName := "2026-11-22_helvetia_fr_deactivated_with_confluence_entitlement.csv"
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Name != wantName {
		t.Fatalf("expected CSV attachment %q, got %+v", wantName, parsed.Attachments)
	}
	name, ct, data, err := ExtractAttachment([]byte(raw1), parsed.Attachments[0].PartName, "")
	if err != nil || name != wantName || ct != "text/csv" || !strings.Contains(string(data), "alice@example.fr") {
		t.Fatalf("ExtractAttachment(raw1): name=%q ct=%q data=%q err=%v", name, ct, string(data), err)
	}

	// 2) Quirky unquoted filename with spaces where strict mime.ParseMediaType fails
	raw2 := strings.Join([]string{
		"From: Sender <sender@example.de>",
		"To: Receiver <recv@example.de>",
		"Subject: Quirky CSV",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=\"bnd-q\"",
		"",
		"--bnd-q",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Body text",
		"--bnd-q",
		"Content-Type: text/csv; name=report final 2026.csv",
		"Content-Disposition: attachment; filename=report final 2026.csv",
		"",
		"a,b\r\n1,2",
		"--bnd-q--",
		"",
	}, "\r\n")
	parsed2, err := ParseRawMessage([]byte(raw2))
	if err != nil {
		t.Fatalf("ParseRawMessage(raw2): %v", err)
	}
	if len(parsed2.Attachments) != 1 || parsed2.Attachments[0].Name != "report final 2026.csv" {
		t.Fatalf("expected fallback-parsed attachment, got %+v", parsed2.Attachments)
	}
}
