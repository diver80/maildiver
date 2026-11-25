package mailengine

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/diver80/postdive/internal/config"
)

// An umlaut can reach the password field as "u" + combining diaeresis (some keyboards, IMEs
// and password managers). The server saved it as "ü", so the login is retried composed.
func TestIMAPLoginWithDecomposedUmlautPassword(t *testing.T) {
	composed := "Sommer-Grüße§2026"
	decomposed := norm.NFD.String(composed)
	if decomposed == composed {
		t.Fatal("test needs a password that changes under NFD")
	}
	addr, _, cleanup := startTestIMAPServer(t, "me@example.org", composed)
	defer cleanup()
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	acc := config.DirectAccount{Email: "me@example.org", IMAPHost: host, IMAPPort: port, IMAPTLS: "plain"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := TestIMAPConnection(ctx, acc, decomposed); err != nil {
		t.Fatalf("decomposed umlauts must still log in: %v", err)
	}
	if _, err := TestIMAPConnection(ctx, acc, "Sommer-Grusse§2026"); err == nil {
		t.Fatal("a wrong password must still fail")
	}
}

func TestPasswordMetaReportsDecomposedForm(t *testing.T) {
	if got := PasswordMeta(norm.NFD.String("Grüße")); !strings.Contains(got, "non_ascii=true nfc=false") {
		t.Errorf("decomposed: %s", got)
	}
	if got := PasswordMeta("Grüße"); !strings.Contains(got, "nfc=true") {
		t.Errorf("composed: %s", got)
	}
	if nfcPassword("plain-ascii!") != "" || nfcPassword("Grüße") != "" {
		t.Error("nothing to retry for passwords that are already composed")
	}
}
