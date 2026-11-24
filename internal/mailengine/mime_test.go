package mailengine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/diver80/postdive/internal/backend"
)

func TestParseLooseMIMEParamsRFC2231AndQuotes(t *testing.T) {
	// Quoted semicolon must not split the value, and extended RFC 2231 overrides plain ASCII fallback.
	raw := `filename="fallback;ascii.pdf"; filename*0*=utf-8'de'Bericht%20; filename*1*=2026%20%C3%9Cbersicht.pdf`
	got := parseLooseMIMEParams(raw)
	if got["filename"] != "Bericht 2026 Übersicht.pdf" {
		t.Fatalf("expected RFC 2231 decoded filename, got %q", got["filename"])
	}

	// Out-of-order RFC 2231 continuation segments should sort numerically.
	raw2 := `filename*2*=.csv; filename*0*=utf-8''report%20; filename*1*=Q4%20final`
	got2 := parseLooseMIMEParams(raw2)
	if got2["filename"] != "report Q4 final.csv" {
		t.Fatalf("expected numerically sorted RFC 2231 filename, got %q", got2["filename"])
	}

	// Quoted semicolon without extended parameter must stay intact.
	raw3 := `filename="invoice;2026.pdf"; size=1234`
	got3 := parseLooseMIMEParams(raw3)
	if got3["filename"] != "invoice;2026.pdf" {
		t.Fatalf("expected quoted semicolon preserved, got %q", got3["filename"])
	}
}

func TestListMessagesHeadersOnlySetsHasAttachments(t *testing.T) {
	ctx := context.Background()
	st, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()

	if err := st.UpsertFolder(ctx, StoredFolder{
		ID:        "acc1::INBOX",
		AccountID: "acc1",
		Path:      "INBOX",
		Name:      "INBOX",
		Type:      "inbox",
	}); err != nil {
		t.Fatalf("UpsertFolder: %v", err)
	}
	if err := st.UpsertMessage(ctx, StoredMessage{
		ID:         "acc1::INBOX::1",
		AccountID:  "acc1",
		FolderID:   "acc1::INBOX",
		FolderPath: "INBOX",
		UID:        1,
		Subject:    "With PDF",
		From:       "alice@example.com",
		DateISO:    "2026-11-22T10:00:00Z",
		DateUnix:   1795341600,
		Attachments: []backend.Attachment{
			{Name: "doc.pdf", ContentType: "application/pdf", Size: 2048, PartName: "2"},
		},
	}); err != nil {
		t.Fatalf("UpsertMessage 1: %v", err)
	}
	if err := st.UpsertMessage(ctx, StoredMessage{
		ID:         "acc1::INBOX::2",
		AccountID:  "acc1",
		FolderID:   "acc1::INBOX",
		FolderPath: "INBOX",
		UID:        2,
		Subject:    "Plain only",
		From:       "bob@example.com",
		DateISO:    "2026-11-22T11:00:00Z",
		DateUnix:   1795345200,
	}); err != nil {
		t.Fatalf("UpsertMessage 2: %v", err)
	}

	msgs, _, err := st.ListMessages(ctx, MessageQuery{HeadersOnly: true, Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	byID := map[string]backend.MessageHeader{}
	for _, sm := range msgs {
		byID[sm.ID] = storedToHeader(sm, false)
	}
	if !byID["acc1::INBOX::1"].HasAttachments {
		t.Fatalf("expected HasAttachments=true for message with attachment")
	}
	if byID["acc1::INBOX::2"].HasAttachments {
		t.Fatalf("expected HasAttachments=false for plain message")
	}
}
