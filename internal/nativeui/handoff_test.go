package nativeui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/triage"
)

func TestConversationMarkdown(t *testing.T) {
	open := &backend.MessageDetail{MessageHeader: backend.MessageHeader{ID: "m2", Subject: "Re: Prozessnetz"}}
	thread := []triage.MessageInput{
		{From: "Erika <erika@example.org>", To: []string{"me@example.org"}, Date: "2026-11-20T17:25:00+01:00", BodyText: "Hallo Frank,\nkannst du das Prozessnetz prüfen?\nGruß Erika"},
		{From: "Max <me@example.org>", Date: "2026-11-21T09:00:00+01:00", BodyText: "Hallo Erika,\nist erledigt.\n\nAm 20.11.2026 um 17:25 schrieb Erika <erika@example.org>:\n> Hallo Frank,\n> kannst du das Prozessnetz prüfen?", Attachments: []string{"liste.pdf"}},
		{From: "Erika <erika@example.org>", Date: "2026-11-21T10:00:00+01:00", Encrypted: true},
	}
	md := conversationMarkdown(open, thread, "de")
	for _, want := range []string{
		"# Re: Prozessnetz", "3 Mail(s), älteste zuerst",
		"## 1. Erika <erika@example.org> · ", "An: me@example.org",
		"## 2. Max <me@example.org>", "Anhänge: liste.pdf", "ist erledigt.",
		"## 3.", "_[verschlüsselt – Inhalt nicht verfügbar]_",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
	if strings.Count(md, "kannst du das Prozessnetz prüfen?") != 1 {
		t.Errorf("the quote of mail 1 inside mail 2 must be left out:\n%s", md)
	}

	// A mail on its own takes the open mail's text and attachments.
	single := &backend.MessageDetail{MessageHeader: backend.MessageHeader{Subject: "", From: "a@b.de"}, BodyText: "Nur Text",
		Attachments: []backend.Attachment{{Name: "x.pdf", Size: 4096}}}
	md = conversationMarkdown(single, nil, "en")
	if !strings.Contains(md, "# (No subject)") || !strings.Contains(md, "Nur Text") || !strings.Contains(md, "x.pdf (4 KB)") {
		t.Fatalf("single mail:\n%s", md)
	}
}

func TestStripQuotedReply(t *testing.T) {
	cases := map[string]string{
		"Danke!\n\nVon: Erika\nGesendet: Freitag\nAn: Frank\n\nalt": "Danke!",
		"Ok.\n> a\n> b":                      "Ok.",
		"Kurz > lang, aber kein Zitat\nEnde": "Kurz > lang, aber kein Zitat\nEnde",
		"Von: Hamburg aus geschrieben\nGruß": "Von: Hamburg aus geschrieben\nGruß",
		"> nur ein Zitat\n> sonst nichts":    "> nur ein Zitat\n> sonst nichts",
	}
	for in, want := range cases {
		if got := stripQuotedReply(in); got != want {
			t.Errorf("stripQuotedReply(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandoffFilesArePrivateAndExpire(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir, file, err := writeHandoffFile("# Test", "Re: Anfrage zu BC Confluence – Größe/100ml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(dir), "-re-anfrage-zu-bc-confluence-groesse") {
		t.Errorf("folder name: %s", filepath.Base(dir))
	}
	st, err := os.Stat(file)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mail.md mode: %v %v", st.Mode(), err)
	}
	old := time.Now().Add(-2 * handoffKeep)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writeHandoffFile("# Neu", "Neu"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("a handoff older than a day must be removed")
	}
	if q := shellQuote("it's"); q != `'it'\''s'` {
		t.Fatalf("shellQuote = %s", q)
	}
}
