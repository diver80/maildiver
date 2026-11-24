package secretstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.json")
	st := NewFileStore(p)

	if _, err := st.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	secret := "pässwörd!\"'\n123"
	if err := st.Set("acc:1", secret); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("acc:1")
	if err != nil || got != secret {
		t.Fatalf("got %q err=%v, want %q", got, err, secret)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 file mode, got %o", info.Mode().Perm())
	}
	if err := st.Delete("acc:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("acc:1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMemoryStoreRoundTrip(t *testing.T) {
	st := NewMemoryStore()
	if err := st.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("k")
	if err != nil || got != "v" {
		t.Fatalf("got %q err=%v", got, err)
	}
	_ = st.Delete("k")
	if _, err := st.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFallbackStoreLegacyReadAndDelete(t *testing.T) {
	dir := t.TempDir()
	primary := NewFileStore(filepath.Join(dir, "postdive", "secrets.json"))
	legacy := NewFileStore(filepath.Join(dir, "omathunder", "secrets.json"))
	if err := legacy.Set("account:ionos", "s3cret!"); err != nil {
		t.Fatal(err)
	}
	st := &fallbackStore{primary: primary, legacy: legacy}
	got, err := st.Get("account:ionos")
	if err != nil || got != "s3cret!" {
		t.Fatalf("fallback Get got %q err=%v", got, err)
	}
	if err := st.Set("account:ionos", "new-secret"); err != nil {
		t.Fatal(err)
	}
	got, err = st.Get("account:ionos")
	if err != nil || got != "new-secret" {
		t.Fatalf("primary override Get got %q err=%v", got, err)
	}
	if err := st.Delete("account:ionos"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("account:ionos"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestKeychainEncodingAndKeyValidation(t *testing.T) {
	// Valid keys
	for _, k := range []string{"account:acc-1", "oauth:google:user@example.com", "key_with-dots.1"} {
		if !validKeychainKey(k) {
			t.Fatalf("expected key %q to be valid", k)
		}
	}
	// Invalid keys (injection/control chars)
	for _, k := range []string{"", "bad\nkey", "bad\"quote", `bad\slash`, "umlaut:ä"} {
		if validKeychainKey(k) {
			t.Fatalf("expected key %q to be rejected", k)
		}
	}
	// Base64 round-trip for umlauts, quotes, and newlines
	secret := "Pässwört!\"'\nLine2"
	encoded := keychainB64Prefix + "UMOkc3N3w7ZydCEiJwpMaW5lMg=="
	if got, ok := decodeKeychainSecret(encoded + "\n"); !ok || got != secret {
		t.Fatalf("decodeKeychainSecret(b64) = %q (ok=%v), want %q", got, ok, secret)
	}
	// Truncated/corrupted b64: payload must be rejected so fallback/self-heal can recover it.
	if _, ok := decodeKeychainSecret(keychainB64Prefix + "abc"); ok {
		t.Fatalf("expected truncated b64: payload to return ok=false")
	}
	// Chunk manifest parser
	if n, sum, ok := parseKeychainChunkManifest(keychainChunkPrefix + "5:" + strings.Repeat("a", 64)); !ok || n != 5 || len(sum) != 64 {
		t.Fatalf("parseKeychainChunkManifest failed: n=%d sum=%q ok=%v", n, sum, ok)
	}
	if _, _, ok := parseKeychainChunkManifest(keychainChunkPrefix + "0:" + strings.Repeat("a", 64)); ok {
		t.Fatalf("expected 0-chunk manifest to be rejected")
	}
	// Legacy plaintext passthrough
	if got, ok := decodeKeychainSecret("plain-legacy-secret\n"); !ok || got != "plain-legacy-secret" {
		t.Fatalf("decodeKeychainSecret(plain) = %q (ok=%v)", got, ok)
	}
}
