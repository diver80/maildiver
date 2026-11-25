package mailengine

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordMetaNeverLeaksSecret(t *testing.T) {
	secret := "S€crätPassw0rd!123 "
	meta := PasswordMeta(secret)
	if strings.Contains(meta, "S€crät") || strings.Contains(meta, "Passw0rd") || strings.Contains(meta, "123") {
		t.Fatalf("PasswordMeta leaked secret: %q", meta)
	}
	for _, want := range []string{"len=19", "non_ascii=true", "lead_space=false", "trail_space=true", "ctrl=false"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("PasswordMeta(%q) = %q, missing %q", secret, meta, want)
		}
	}
	if got := PasswordMeta(""); got != "empty" {
		t.Fatalf("PasswordMeta empty = %q, want empty", got)
	}
	if got := PasswordMeta("abc\n"); !strings.Contains(got, "ctrl=true") {
		t.Fatalf("expected ctrl=true for newline, got %q", got)
	}
}

func TestDiagLogWriteReadAndRedaction(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "account-diag.log")
	diagMu.Lock()
	prevOvr := diagPathOvr
	diagPathOvr = tmp
	diagRing = nil
	diagMu.Unlock()
	defer func() {
		diagMu.Lock()
		diagPathOvr = prevOvr
		diagRing = nil
		diagMu.Unlock()
	}()

	logAccountDiag("IMAP", "auth failed user=lars@example.de err=%s", SanitizeDiagErr(errors.New("NO auth failed Bearer eyJhbGciOiJIUzI1NiJ9.secret password=MySecret123")))

	p, content := ReadAccountDiagLog()
	if p != tmp {
		t.Fatalf("path = %q, want %q", p, tmp)
	}
	if strings.Contains(content, "eyJhbGciOiJIUzI1NiJ9") || strings.Contains(content, "MySecret123") {
		t.Fatalf("diag log leaked sensitive token/password: %s", content)
	}
	if !strings.Contains(content, "[IMAP]") || !strings.Contains(content, "[REDACTED]") {
		t.Fatalf("expected [IMAP] and [REDACTED] in diag log, got:\n%s", content)
	}

	if err := ClearAccountDiagLog(); err != nil {
		t.Fatalf("ClearAccountDiagLog: %v", err)
	}
	_, after := ReadAccountDiagLog()
	if strings.Contains(after, "lars@example.de") {
		t.Fatalf("expected cleared diag log, still got:\n%s", after)
	}
}
