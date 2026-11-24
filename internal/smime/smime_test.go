package smime

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/diver80/postdive/internal/secretstore"
)

func generateTestCert(t *testing.T, email, name string, caCert *x509.Certificate, caKey *rsa.PrivateKey) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		EmailAddresses: func() []string {
			if email == "" {
				return nil
			}
			return []string{email}
		}(),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		BasicConstraintsValid: true,
	}
	signerCert := tmpl
	signerKey := key
	if caCert != nil && caKey != nil {
		signerCert = caCert
		signerKey = caKey
	} else {
		tmpl.IsCA = true
		tmpl.KeyUsage |= x509.KeyUsageCertSign
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signerCert, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func TestSMIMEImportSignVerifyEncryptDecryptRoundTrip(t *testing.T) {
	caCert, caKey := generateTestCert(t, "", "PostDive Test CA", nil, nil)
	aliceCert, aliceKey := generateTestCert(t, "alice@example.com", "Alice", caCert, caKey)
	bobCert, bobKey := generateTestCert(t, "bob@example.com", "Bob", caCert, caKey)

	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	// Encode PKCS#12 bundles for Alice and Bob using modern AES-256 encoder.
	aliceP12, err := pkcs12.Modern.Encode(aliceKey, aliceCert, []*x509.Certificate{caCert}, "alice-secret")
	if err != nil {
		t.Fatalf("encode alice p12: %v", err)
	}
	bobP12, err := pkcs12.Modern.Encode(bobKey, bobCert, []*x509.Certificate{caCert}, "bob-secret")
	if err != nil {
		t.Fatalf("encode bob p12: %v", err)
	}

	aliceMgr := NewManager(t.TempDir(), secretstore.NewMemoryStore())
	info, err := aliceMgr.ImportPKCS12(aliceP12, "alice-secret")
	if err != nil {
		t.Fatalf("ImportPKCS12 alice: %v", err)
	}
	if info.Email != "alice@example.com" || !info.HasPrivateKey {
		t.Fatalf("unexpected alice info: %+v", info)
	}

	// Import Bob's public cert into Alice's manager.
	bobPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: bobCert.Raw})
	if _, err := aliceMgr.ImportPublicCert(bobPEM); err != nil {
		t.Fatalf("ImportPublicCert bob: %v", err)
	}

	bobMgr := NewManager(t.TempDir(), secretstore.NewMemoryStore())
	if _, err := bobMgr.ImportPKCS12(bobP12, "bob-secret"); err != nil {
		t.Fatalf("ImportPKCS12 bob: %v", err)
	}

	rawMail := []byte("From: Alice <alice@example.com>\r\n" +
		"To: Bob <bob@example.com>\r\n" +
		"Subject: Geheimes Angebot\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Hallo Bob,\r\ndieser Text ist S/MIME signiert und verschlüsselt!\r\n")

	// 1. Alice signs the message.
	aliceID, err := aliceMgr.LoadIdentity("alice@example.com")
	if err != nil {
		t.Fatalf("LoadIdentity alice: %v", err)
	}
	signed, err := SignMIME(rawMail, aliceID)
	if err != nil {
		t.Fatalf("SignMIME: %v", err)
	}
	if !strings.Contains(string(signed), "multipart/signed") {
		t.Fatalf("expected multipart/signed header")
	}

	// 2. Verify signed message with trusted CA.
	vr, _, err := VerifyMIME(signed, roots)
	if err != nil || vr == nil || !vr.Valid || !vr.Trusted || vr.SignerEmail != "alice@example.com" {
		t.Fatalf("VerifyMIME failed: vr=%+v err=%v", vr, err)
	}

	// 3. Tampering with signed body invalidates the signature!
	tampered := []byte(strings.Replace(string(signed), "Geheimes Angebot", "Geheimes Angebot", 1))
	tampered = []byte(strings.Replace(string(tampered), "verschlüsselt!", "MANIPULIERT!", 1))
	vrBad, _, _ := VerifyMIME(tampered, roots)
	if vrBad != nil && vrBad.Valid {
		t.Fatal("tampered S/MIME message must fail signature verification")
	}

	// 4. Sign-then-encrypt for Bob (+ Alice so Alice can read her own Sent copy).
	rcptCerts, missing := aliceMgr.LookupRecipientCerts([]string{"bob@example.com", "alice@example.com"})
	if len(missing) != 0 || len(rcptCerts) != 2 {
		t.Fatalf("LookupRecipientCerts got %d certs, missing=%v", len(rcptCerts), missing)
	}
	encrypted, err := EncryptMIME(signed, rcptCerts)
	if err != nil {
		t.Fatalf("EncryptMIME: %v", err)
	}
	if strings.Contains(string(encrypted), "verschlüsselt!") {
		t.Fatal("plaintext leaked into S/MIME encrypted message")
	}

	// 5. Bob decrypts & automatically verifies Alice's inner signature.
	decrypted, innerVR, err := DecryptMIME(encrypted, bobMgr.AllIdentities(), roots)
	if err != nil {
		t.Fatalf("DecryptMIME: %v", err)
	}
	if !strings.Contains(string(decrypted), "dieser Text ist S/MIME signiert und verschlüsselt!") {
		t.Fatalf("decrypted body mismatch: %s", string(decrypted))
	}
	if innerVR == nil || !innerVR.Valid || !innerVR.Trusted || innerVR.SignerEmail != "alice@example.com" {
		t.Fatalf("expected valid inner signature after decrypt, got %+v", innerVR)
	}

	// Verify multi-cert PEM bundle import (with CA cert included and skipped cleanly).
	bundlePEM := append([]byte(encodeCertPEM(caCert)), '\n')
	bundlePEM = append(bundlePEM, []byte(encodeCertPEM(aliceCert))...)
	bundlePEM = append(bundlePEM, '\n')
	bundlePEM = append(bundlePEM, []byte(encodeCertPEM(bobCert))...)
	bulkMgr := NewManager(t.TempDir(), secretstore.NewMemoryStore())
	imported, err := bulkMgr.ImportPublicCerts(bundlePEM)
	if err != nil {
		t.Fatalf("ImportPublicCerts multi-PEM: %v", err)
	}
	if len(imported) != 2 {
		t.Fatalf("expected 2 end-entity certs imported (CA skipped), got %d: %+v", len(imported), imported)
	}

	// Verify AllIdentitiesWithErr surfaces secretstore load errors instead of silently returning empty.
	brokenSecrets := secretstore.NewMemoryStore()
	brokenMgr := NewManager(bobMgr.dir, brokenSecrets) // has bob's metadata on disk, but missing secret in brokenSecrets
	if ids, err := brokenMgr.AllIdentitiesWithErr(); err == nil || len(ids) != 0 {
		t.Fatalf("expected error from AllIdentitiesWithErr when keychain entry missing, got ids=%d err=%v", len(ids), err)
	}
}
