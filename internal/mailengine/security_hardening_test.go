package mailengine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/triage"
)

func TestApplyOutgoingSecurityFailClosed(t *testing.T) {
	e := &Engine{} // neither pgp nor smime initialized
	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: Test\r\n\r\nSecret\r\n")

	// Encrypt requested with uninitialized PGP must fail closed (never send plaintext).
	if _, _, err := e.applyOutgoingSecurity("a@example.com", backend.ComposeRequest{
		To:         []string{"b@example.com"},
		CryptoTech: "pgp",
		Encrypt:    true,
	}, raw); err == nil {
		t.Fatalf("expected error when PGP encrypt requested with nil pgp manager")
	}

	// Sign requested with uninitialized S/MIME must fail closed.
	if _, _, err := e.applyOutgoingSecurity("a@example.com", backend.ComposeRequest{
		To:         []string{"b@example.com"},
		CryptoTech: "smime",
		Sign:       "on",
	}, raw); err == nil {
		t.Fatalf("expected error when S/MIME sign requested with nil smime manager")
	}

	// Plain send with no sign/encrypt/tech requested still passes through.
	out, sec, err := e.applyOutgoingSecurity("a@example.com", backend.ComposeRequest{
		To:   []string{"b@example.com"},
		Sign: "off",
	}, raw)
	if err != nil || sec != nil || len(out) == 0 {
		t.Fatalf("expected plain send passthrough, got err=%v sec=%+v", err, sec)
	}
}

func TestUpsertFilterAtomicRenamePreservesPositionAndBlocksDuplicates(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	cond := []triage.NativeCondition{{Field: "from", Op: "contains", Value: "jira"}}
	act := []backend.FilterActionSpec{{Type: "markRead"}}

	f1, err := st.UpsertFilter(ctx, backend.FilterUpsertRequest{
		AccountID: "acc1", Name: "Rule A", Enabled: true, MatchAll: true, Conditions: cond, Actions: act, Mode: "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertFilter(ctx, backend.FilterUpsertRequest{
		AccountID: "acc1", Name: "Rule B", Enabled: true, MatchAll: true, Conditions: cond, Actions: act, Mode: "create",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Renaming Rule A -> Rule A Renamed via Mode="update" preserves position 0.
	renamed, err := st.UpsertFilter(ctx, backend.FilterUpsertRequest{
		AccountID: "acc1", Name: "Rule A Renamed", ExistingName: "Rule A", Enabled: true, MatchAll: true, Conditions: cond, Actions: act, Mode: "update",
	})
	if err != nil {
		t.Fatalf("rename update failed: %v", err)
	}
	if renamed.ID != f1.ID || renamed.Position != 0 || renamed.Name != "Rule A Renamed" {
		t.Fatalf("expected in-place rename preserving ID and position 0, got %+v", renamed)
	}

	// Renaming Rule A Renamed -> Rule B must be rejected as collision.
	if _, err := st.UpsertFilter(ctx, backend.FilterUpsertRequest{
		AccountID: "acc1", Name: "Rule B", ExistingName: "Rule A Renamed", Enabled: true, MatchAll: true, Conditions: cond, Actions: act, Mode: "update",
	}); err == nil {
		t.Fatalf("expected duplicate name collision error on rename")
	}
}

func TestSanitizeDownloadFilenameUTF8Safe(t *testing.T) {
	long := strings.Repeat("ü", 120) + ".pdf" // 240+ bytes of 2-byte UTF-8 runes
	got := sanitizeDownloadFilename(long)
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeDownloadFilename produced invalid UTF-8: %q", got)
	}
	if len(got) > 180 || !strings.HasSuffix(got, ".pdf") {
		t.Fatalf("unexpected sanitized filename len=%d val=%q", len(got), got)
	}
}

func TestExtractAttachmentStrictPartName(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"From: a@example.com",
		"To: b@example.com",
		"Subject: Att",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="bnd"`,
		"",
		"--bnd",
		"Content-Type: text/plain",
		"",
		"Hello",
		"--bnd",
		`Content-Type: application/pdf; name="doc.pdf"`,
		`Content-Disposition: attachment; filename="doc.pdf"`,
		"",
		"PDFDATA",
		"--bnd--",
	}, "\r\n"))

	// Requesting wrong partName must NOT silently fall back to another part with matching filename.
	if _, _, _, err := ExtractAttachment(raw, "9.9", "doc.pdf"); err == nil {
		t.Fatalf("expected error when explicit partName does not exist")
	}
	name, _, data, err := ExtractAttachment(raw, "2", "doc.pdf")
	if err != nil || name != "doc.pdf" || string(data) != "PDFDATA" {
		t.Fatalf("expected exact partName 2 match, got name=%q data=%q err=%v", name, string(data), err)
	}
}
