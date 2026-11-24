package mailengine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/diver80/postdive/internal/config"
)

func TestIsTransientNetworkErrorMsg(t *testing.T) {
	transient := []string{
		"IMAP Verbindung zu imap.1und1.de:993 fehlgeschlagen: dial tcp: lookup imap.1und1.de: no such host",
		"dial tcp: lookup imap.ionos.de: Temporary failure in name resolution",
		"read tcp: connection reset by peer",
		"dial tcp 1.2.3.4:993: connect: network is unreachable",
		"read tcp: i/o timeout",
	}
	for _, msg := range transient {
		if !IsTransientNetworkErrorMsg(msg) {
			t.Errorf("expected transient network error for %q", msg)
		}
	}

	permanent := []string{
		"IMAP Anmeldung fehlgeschlagen (alice@example.org): invalid credentials",
		"authentication failed",
		"login failed: bad password",
		"permission denied",
		"",
	}
	for _, msg := range permanent {
		if IsTransientNetworkErrorMsg(msg) {
			t.Errorf("expected non-transient error for %q", msg)
		}
	}
	if !IsTransientNetworkError(errors.New("dial tcp: lookup imap.1und1.de: no such host")) {
		t.Fatal("expected IsTransientNetworkError to match DNS no such host error")
	}
}

func TestSetWorkersPausedPreventsBackgroundIdleAndClearsTransientErrors(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	cfg.Direct.Accounts = []config.DirectAccount{
		{
			ID:       "acc-pause",
			Name:     "Test",
			Email:    "test@example.invalid",
			IMAPHost: "imap.invalid.test",
			IMAPPort: 993,
			IMAPTLS:  "tls",
			Enabled:  true,
		},
	}

	eng, err := New(Options{
		DBPath:       filepath.Join(tmp, "test.db"),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(c config.Config) error { cfg = c; return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = eng.Close() }()

	// Pause BEFORE Start (just like Android headless launch): no IDLE worker should spawn.
	eng.SetWorkersPaused(true)
	eng.Start(context.Background())

	eng.mu.RLock()
	workerCount := len(eng.workers)
	eng.mu.RUnlock()
	if workerCount != 0 {
		t.Fatalf("expected 0 IDLE workers while paused, got %d", workerCount)
	}

	// Seed one transient DNS error and one permanent auth error; resuming must clear only the transient one.
	eng.setAccountState("acc-pause", "error", "IMAP Verbindung zu imap.1und1.de:993 fehlgeschlagen: dial tcp: lookup imap.1und1.de: no such host")
	eng.setAccountState("acc-auth", "error", "IMAP Anmeldung fehlgeschlagen (bob@example.org): invalid credentials")

	eng.SetWorkersPaused(false)

	eng.mu.RLock()
	transientErr := eng.accLastErr["acc-pause"]
	authErr := eng.accLastErr["acc-auth"]
	eng.mu.RUnlock()
	if transientErr != "" {
		t.Fatalf("resuming must clear transient DNS error, still got %q", transientErr)
	}
	if authErr == "" {
		t.Fatalf("resuming must keep permanent auth error visible")
	}

	// Pausing again cancels active workers cleanly.
	eng.SetWorkersPaused(true)
	eng.mu.RLock()
	afterPause := len(eng.workers)
	eng.mu.RUnlock()
	if afterPause != 0 {
		t.Fatalf("expected 0 workers after SetWorkersPaused(true), got %d", afterPause)
	}
}
