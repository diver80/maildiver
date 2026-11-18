package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestFindUIApp(t *testing.T) {
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }
	exe := filepath.Join("/opt", "bin", "omathunder")

	// A sibling omathunder-ui binary wins (Linux, Homebrew, ./install.sh).
	sib := filepath.Join("/opt", "bin", "omathunder-ui")
	got, viaOpen := findUIApp(exe, "linux", func(p string) bool { return p == sib }, notOnPath)
	if got != sib || viaOpen {
		t.Fatalf("sibling: got %q viaOpen=%v", got, viaOpen)
	}

	// macOS: the .app bundle is started through `open`.
	got, viaOpen = findUIApp(exe, "darwin", func(p string) bool { return p == "/Applications/OmaThunder.app" }, notOnPath)
	if got != "/Applications/OmaThunder.app" || !viaOpen {
		t.Fatalf("app bundle: got %q viaOpen=%v", got, viaOpen)
	}

	// PATH fallback, and .exe on Windows.
	got, _ = findUIApp(exe, "windows", func(string) bool { return false }, func(name string) (string, error) {
		if name != "omathunder-ui.exe" {
			t.Fatalf("unexpected lookup %q", name)
		}
		return `C:\\bin\\omathunder-ui.exe`, nil
	})
	if got == "" {
		t.Fatal("expected PATH fallback")
	}

	if got, _ := findUIApp(exe, "linux", func(string) bool { return false }, notOnPath); got != "" {
		t.Fatalf("expected no app, got %q", got)
	}
}
