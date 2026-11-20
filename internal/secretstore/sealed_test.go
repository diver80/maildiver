package secretstore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSealedFileStore(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{7}, 32)
	calls := 0
	keyFn := func() ([]byte, error) { calls++; return key, nil }

	plain := NewFileStore(filepath.Join(dir, "secrets.json"))
	if err := plain.Set("imap:acc1", "Passwort-äöü"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "secrets.sealed")
	s := NewSealedFileStore(path, keyFn, filepath.Join(dir, "secrets.json"))
	if v, err := s.Get("imap:acc1"); err != nil || v != "Passwort-äöü" {
		t.Fatalf("migrated secret = %q, %v", v, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets.json")); !os.IsNotExist(err) {
		t.Fatal("the plain file must be removed after the migration")
	}
	big := string(bytes.Repeat([]byte("x"), 8000)) // an S/MIME bundle is larger than the keystore's limit
	if err := s.Set("smime:me", big); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("Passwort")) || bytes.Contains(raw, []byte("imap:acc1")) {
		t.Fatal("the file shows secrets in clear text")
	}

	s2 := NewSealedFileStore(path, keyFn, "")
	if v, err := s2.Get("smime:me"); err != nil || v != big {
		t.Fatalf("reopened: %d bytes, %v", len(v), err)
	}
	wrong := NewSealedFileStore(path, func() ([]byte, error) { return bytes.Repeat([]byte{8}, 32), nil }, "")
	if _, err := wrong.Get("imap:acc1"); err == nil {
		t.Fatal("another key must not open the file")
	}
	if calls > 2 {
		t.Fatalf("key asked %d times, once per store expected", calls)
	}
}

func TestSetDefault(t *testing.T) {
	m := NewMemoryStore()
	SetDefault(m)
	defer defaultOverride.Store(nil)
	if Default() != Store(m) {
		t.Fatal("Default must return the store set")
	}
}
