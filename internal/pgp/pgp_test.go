package pgp

import (
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/diver80/maildiver/internal/secretstore"
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

	// Verify address mismatch flag
	vrMismatch, _, _ := VerifyMIME(signedRaw, "evil@attacker.example", openpgp.EntityList{aliceEnt})
	if vrMismatch == nil || !vrMismatch.Valid || !vrMismatch.SignerAddressMismatch {
		t.Fatalf("expected SignerAddressMismatch when From != signer email, got %+v", vrMismatch)
	}
}
