package pgp

import (
	"context"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/diver80/postdive/internal/secretstore"
)

func TestGenerateExportImportSignVerifyEncryptDecryptPGPMIME(t *testing.T) {
	sec := secretstore.NewMemoryStore()
	aliceMgr := NewManager(t.TempDir(), sec)
	bobMgr := NewManager(t.TempDir(), secretstore.NewMemoryStore())

	aliceInfo, err := aliceMgr.GenerateIdentity("Alice Sender", "alice@example.org", "")
	if err != nil {
		t.Fatalf("GenerateIdentity(alice): %v", err)
	}
	if !aliceInfo.HasPrivateKey || aliceInfo.Email != "alice@example.org" {
		t.Fatalf("unexpected alice info: %+v", aliceInfo)
	}
	bobInfo, err := bobMgr.GenerateIdentity("Bob Receiver", "bob@example.org", "")
	if err != nil {
		t.Fatalf("GenerateIdentity(bob): %v", err)
	}
	if !bobInfo.HasPrivateKey {
		t.Fatalf("expected bob private key")
	}

	alicePub, err := aliceMgr.ExportPublicKeyArmor("alice@example.org")
	if err != nil {
		t.Fatalf("ExportPublicKeyArmor(alice): %v", err)
	}
	bobPub, err := bobMgr.ExportPublicKeyArmor("bob@example.org")
	if err != nil {
		t.Fatalf("ExportPublicKeyArmor(bob): %v", err)
	}

	// Cross-import public keys cleanly
	if _, err := aliceMgr.ImportKeys([]byte(bobPub), ""); err != nil {
		t.Fatalf("alice import bob pub: %v", err)
	}
	if _, err := bobMgr.ImportKeys([]byte(alicePub), ""); err != nil {
		t.Fatalf("bob import alice pub: %v", err)
	}

	rawMIME := []byte(strings.Join([]string{
		"From: Alice Sender <alice@example.org>",
		"To: Bob Receiver <bob@example.org>",
		"Subject: Geheimer Test",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Hallo Bob, geheimer Text mit Umlauten äöüß.",
		"",
	}, "\r\n"))

	aliceEnt, err := aliceMgr.LoadIdentity("alice@example.org")
	if err != nil {
		t.Fatalf("LoadIdentity(alice): %v", err)
	}
	signedRaw, err := SignMIME(rawMIME, aliceEnt)
	if err != nil {
		t.Fatalf("SignMIME: %v", err)
	}
	vr, _, isSigned := VerifyMIME(signedRaw, "alice@example.org", bobMgr.AllEntities())
	if !isSigned || vr == nil || !vr.Valid || vr.SignerAddressMismatch {
		t.Fatalf("expected valid OpenPGP signature, got signed=%v vr=%+v", isSigned, vr)
	}

	// Sign + Encrypt to Bob + Alice self-copy
	rcpts, missing := aliceMgr.LookupRecipientKeys([]string{"bob@example.org", "alice@example.org"})
	if len(missing) != 0 || len(rcpts) != 2 {
		t.Fatalf("LookupRecipientKeys mismatch: keys=%d missing=%v", len(rcpts), missing)
	}
	encRaw, err := EncryptMIME(rawMIME, rcpts, aliceEnt)
	if err != nil {
		t.Fatalf("EncryptMIME: %v", err)
	}
	if !strings.Contains(string(encRaw), `multipart/encrypted; protocol="application/pgp-encrypted"`) {
		t.Fatalf("expected RFC 3156 multipart/encrypted header, got:\n%s", string(encRaw))
	}

	plain, decVR, isInline, isEnc, err := DecryptMIME(encRaw, bobMgr.AllEntities(), "alice@example.org")
	if err != nil || !isEnc || isInline || decVR == nil || !decVR.Valid {
		t.Fatalf("DecryptMIME failed: err=%v enc=%v inline=%v vr=%+v", err, isEnc, isInline, decVR)
	}
	if !strings.Contains(string(plain), "Hallo Bob, geheimer Text mit Umlauten äöüß.") {
		t.Fatalf("decrypted plaintext mismatch:\n%s", string(plain))
	}

	// Verify address mismatch flag sets Trusted=false
	vrMismatch, _, _ := VerifyMIME(signedRaw, "evil@attacker.example", openpgp.EntityList{aliceEnt})
	if vrMismatch == nil || !vrMismatch.Valid || !vrMismatch.SignerAddressMismatch || vrMismatch.Trusted {
		t.Fatalf("expected SignerAddressMismatch=true and Trusted=false when From != signer email, got %+v", vrMismatch)
	}
}

func TestPassphraseProtectedIdentityAndFetchOnlineSSRFGuards(t *testing.T) {
	sec := secretstore.NewMemoryStore()
	mgr := NewManager(t.TempDir(), sec)

	info, err := mgr.GenerateIdentity("Secret User", "sec@example.org", "super-secret-pass")
	if err != nil {
		t.Fatalf("GenerateIdentity with passphrase: %v", err)
	}
	if !info.HasPrivateKey {
		t.Fatalf("expected HasPrivateKey=true")
	}
	// Stored armor must actually be encrypted at rest.
	rawStored, err := sec.Get(secretKeyForIdentity("sec@example.org"))
	if err != nil || rawStored == "" {
		t.Fatalf("expected stored private key armor: %v", err)
	}
	el, err := openpgp.ReadArmoredKeyRing(strings.NewReader(rawStored))
	if err != nil || len(el) == 0 || !el[0].PrivateKey.Encrypted {
		t.Fatalf("expected private key stored in secretstore to be passphrase-encrypted")
	}
	// LoadIdentity must unlock it seamlessly using stored passphrase.
	loaded, err := mgr.LoadIdentity("sec@example.org")
	if err != nil || loaded.PrivateKey.Encrypted {
		t.Fatalf("expected LoadIdentity to unlock encrypted key, err=%v", err)
	}

	// SSRF / URL-injection guards on FetchOnlinePublicKey
	for _, bad := range []string{
		"user@127.0.0.1:8080",
		"user@169.254.169.254",
		"user@localhost",
		"user@internal.local",
		"bad/addr@example.com",
		"not-an-email",
	} {
		if _, err := mgr.FetchOnlinePublicKey(context.Background(), bad); err == nil {
			t.Fatalf("expected FetchOnlinePublicKey(%q) to be rejected", bad)
		}
	}
}

func TestSignThenEncryptInnerMultipartSignedVerified(t *testing.T) {
	aliceMgr := NewManager(t.TempDir(), secretstore.NewMemoryStore())
	bobMgr := NewManager(t.TempDir(), secretstore.NewMemoryStore())
	if _, err := aliceMgr.GenerateIdentity("Alice", "alice@example.org", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := bobMgr.GenerateIdentity("Bob", "bob@example.org", ""); err != nil {
		t.Fatal(err)
	}
	alicePub, _ := aliceMgr.ExportPublicKeyArmor("alice@example.org")
	bobPub, _ := bobMgr.ExportPublicKeyArmor("bob@example.org")
	_, _ = aliceMgr.ImportKeys([]byte(bobPub), "")
	_, _ = bobMgr.ImportKeys([]byte(alicePub), "")

	rawMIME := []byte("From: alice@example.org\r\nTo: bob@example.org\r\nSubject: Inner Sig\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nSecret signed content.\r\n")
	aliceEnt, _ := aliceMgr.LoadIdentity("alice@example.org")
	signedInner, err := SignMIME(rawMIME, aliceEnt)
	if err != nil {
		t.Fatalf("SignMIME: %v", err)
	}
	rcpts, _ := aliceMgr.LookupRecipientKeys([]string{"bob@example.org"})
	// Encrypt WITHOUT outer OpenPGP signature (Thunderbird-style Sign-then-Encrypt).
	encRaw, err := EncryptMIME(signedInner, rcpts, nil)
	if err != nil {
		t.Fatalf("EncryptMIME: %v", err)
	}
	plain, vr, isInline, isEnc, err := DecryptMIME(encRaw, bobMgr.AllEntities(), "alice@example.org")
	if err != nil || !isEnc || isInline {
		t.Fatalf("DecryptMIME failed: err=%v enc=%v inline=%v", err, isEnc, isInline)
	}
	if vr == nil || !vr.Valid || !vr.Trusted {
		t.Fatalf("expected inner multipart/signed to be verified on decrypt, got vr=%+v", vr)
	}
	if !strings.Contains(string(plain), "Secret signed content.") {
		t.Fatalf("unexpected decrypted payload: %s", string(plain))
	}
}
