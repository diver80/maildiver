package mailengine

import (
	"context"
	"testing"
)

func TestListSnippetStoredWithBodyNeverForEncrypted(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	plain := testMail("p1", "INBOX", "p1@x", "", "", "Angebot", 100)
	plain.BodyText = "Hallo Alex,\n\nanbei das Angebot.\n\n> alte Mail\n"
	html := testMail("h1", "INBOX", "h1@x", "", "", "Newsletter", 101)
	html.BodyText, html.BodyHTML = "", "<p>Neue <b>Funktionen</b> im Herbst</p>"
	enc := testMail("e1", "INBOX", "e1@x", "", "", "Geheim", 102)
	enc.BodyText = "-----BEGIN PGP MESSAGE-----\nhQEMA...\n-----END PGP MESSAGE-----"
	smime := testMail("s1", "INBOX", "s1@x", "", "", "S/MIME", 103)
	smime.BodyText = "Klartext nach Entschlüsselung darf nie in die Liste"
	smime.Headers = map[string]string{"content-type": "application/pkcs7-mime; smime-type=enveloped-data"}
	headerOnly := testMail("n1", "INBOX", "n1@x", "", "", "Nur Kopf", 104)
	headerOnly.BodyText, headerOnly.BodyFetched = "", false
	for _, m := range []StoredMessage{plain, html, enc, smime, headerOnly} {
		if err := s.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	list, _, err := s.ListMessages(ctx, MessageQuery{HeadersOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range list {
		got[m.ID] = m.Snippet
		if h := storedToHeader(m, false); h.Snippet != m.Snippet {
			t.Fatalf("header of %s lost the snippet", m.ID)
		}
	}
	want := map[string]string{
		"p1": "Hallo Alex, anbei das Angebot.",
		"h1": "Neue Funktionen im Herbst",
		"e1": "", "s1": "", "n1": "",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("snippet %s = %q, want %q", id, got[id], w)
		}
	}

	// A header-only refresh (flags) keeps the snippet the fetched body made.
	plain.BodyText, plain.BodyFetched, plain.Read = "", false, true
	if err := s.UpsertMessage(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMessage(ctx, "p1"); m.Snippet != want["p1"] {
		t.Fatalf("header refresh dropped the snippet: %q", m.Snippet)
	}

	// Mails cached before the column existed get theirs once.
	if _, err := s.db.Exec(`UPDATE messages SET snippet=''`); err != nil {
		t.Fatal(err)
	}
	if err := s.backfillSnippets(); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMessage(ctx, "h1"); m.Snippet != want["h1"] {
		t.Fatalf("backfill: %q", m.Snippet)
	}
	if m, _ := s.GetMessage(ctx, "e1"); m.Snippet != "" {
		t.Fatalf("backfill must skip encrypted mails: %q", m.Snippet)
	}
}
