package mailengine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/diver80/postdive/internal/config"
)

const (
	diagLogFileName     = "account-diag.log"
	diagLogMaxRingLines = 300
	diagLogMaxFileBytes = 256 * 1024
	diagLogKeepBytes    = 128 * 1024
)

var (
	diagMu        sync.Mutex
	diagRing      []string
	diagPathOvr   string // optional override for tests
	diagSecretRe  = regexp.MustCompile(`(?i)(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}|(password|passwd|secret|access_token|refresh_token)=[^\s&;]+`)
)

// DiagLogPath returns the on-disk path of the password-free account diagnostic log.
func DiagLogPath() string {
	diagMu.Lock()
	ovr := diagPathOvr
	diagMu.Unlock()
	if ovr != "" {
		return ovr
	}
	dir, err := config.DataDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, diagLogFileName)
}

// PasswordMeta returns safe, non-sensitive diagnostics about a password string
// (length, byte length, non-ASCII, leading/trailing spaces, control chars).
// It NEVER includes any character, prefix, or hash of the password.
func PasswordMeta(pw string) string {
	if pw == "" {
		return "empty"
	}
	runes := utf8.RuneCountInString(pw)
	bytesLen := len(pw)
	nonASCII := false
	hasCtrl := false
	for _, r := range pw {
		if r > 0x7f {
			nonASCII = true
		}
		if r < 0x20 || r == 0x7f {
			hasCtrl = true
		}
	}
	first, _ := utf8.DecodeRuneInString(pw)
	last, _ := utf8.DecodeLastRuneInString(pw)
	leadSpace := unicode.IsSpace(first)
	trailSpace := unicode.IsSpace(last)
	return fmt.Sprintf("len=%d bytes=%d non_ascii=%v lead_space=%v trail_space=%v ctrl=%v",
		runes, bytesLen, nonASCII, leadSpace, trailSpace, hasCtrl)
}

// SanitizeDiagErr formats an error onto a single line while scrubbing any accidental
// bearer/basic tokens or password query parameters.
func SanitizeDiagErr(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ReplaceAll(err.Error(), "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	s = diagSecretRe.ReplaceAllString(s, "[REDACTED]")
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

// logAccountDiag appends a single password-free diagnostic line to the in-memory ring
// buffer and (best-effort) to <DataDir>/account-diag.log (mode 0600, auto-trimmed).
func logAccountDiag(tag, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	msg = strings.ReplaceAll(msg, "\r", " ")
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = diagSecretRe.ReplaceAllString(msg, "[REDACTED]")
	line := fmt.Sprintf("%s [%s] %s", time.Now().Format(time.RFC3339), tag, msg)

	diagMu.Lock()
	defer diagMu.Unlock()
	diagRing = append(diagRing, line)
	if len(diagRing) > diagLogMaxRingLines {
		diagRing = append([]string(nil), diagRing[len(diagRing)-diagLogMaxRingLines:]...)
	}

	p := diagPathOvr
	if p == "" {
		if dir, err := config.DataDir(); err == nil && strings.TrimSpace(dir) != "" {
			p = filepath.Join(dir, diagLogFileName)
		}
	}
	if p == "" {
		return
	}
	_ = appendDiagFileLocked(p, line+"\n")
}

func appendDiagFileLocked(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if st, err := os.Stat(path); err == nil && st.Size() > diagLogMaxFileBytes {
		if raw, rerr := os.ReadFile(path); rerr == nil && len(raw) > diagLogKeepBytes {
			tail := raw[len(raw)-diagLogKeepBytes:]
			if nl := strings.IndexByte(string(tail), '\n'); nl >= 0 && nl+1 < len(tail) {
				tail = tail[nl+1:]
			}
			_ = os.WriteFile(path, tail, 0o600)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

// ReadAccountDiagLog returns the log file path and the sanitized diagnostic log text
// (safe to share for troubleshooting: contains zero passwords or OAuth tokens).
func ReadAccountDiagLog() (string, string) {
	p := DiagLogPath()

	diagMu.Lock()
	var body string
	if p != "" {
		if raw, err := os.ReadFile(p); err == nil && len(raw) > 0 {
			body = string(raw)
		}
	}
	if body == "" && len(diagRing) > 0 {
		body = strings.Join(diagRing, "\n") + "\n"
	}
	diagMu.Unlock()

	header := fmt.Sprintf("# PostDive Account Diagnostic Log (no passwords or tokens)\n# OS: %s/%s · Generated: %s\n",
		runtime.GOOS, runtime.GOARCH, time.Now().Format(time.RFC3339))
	if p != "" {
		header += fmt.Sprintf("# File: %s\n", p)
	}
	if strings.TrimSpace(body) == "" {
		return p, header + "# (no account connection events recorded yet — click Test Connection or Sync)\n"
	}
	return p, header + "\n" + body
}

// ClearAccountDiagLog empties both the in-memory ring buffer and the on-disk diagnostic log.
func ClearAccountDiagLog() error {
	p := DiagLogPath()
	diagMu.Lock()
	defer diagMu.Unlock()
	diagRing = nil
	if p == "" {
		return nil
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AccountDiagLog exposes the password-free account connection & sync log for UI copy/export.
func (e *Engine) AccountDiagLog() (string, string) {
	return ReadAccountDiagLog()
}
