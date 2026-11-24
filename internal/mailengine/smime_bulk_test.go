package mailengine

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
	"github.com/diver80/postdive/internal/smime"
)

func makeSelfSignedEmailCert(t *testing.T, cn, email string) (derBytes []byte, pemBytes []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:   big.NewInt(time.Now().UnixNano()),
		Subject:        pkix.Name{CommonName: cn},
		EmailAddresses: []string{email},
		NotBefore:      time.Now().Add(-time.Hour),
		NotAfter:       time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestImportSMIMECertPathsBulkFolderAndZip(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(dir, "postdive.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	certsDir := filepath.Join(dir, "team-certs")
	if err := os.MkdirAll(certsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	der1, _ := makeSelfSignedEmailCert(t, "Alice", "alice@avono.de")
	_, pem2 := makeSelfSignedEmailCert(t, "Bob", "bob@avono.de")
	_, pem3 := makeSelfSignedEmailCert(t, "Carol", "carol@avono.de")
	_, pem4 := makeSelfSignedEmailCert(t, "Dave", "dave@avono.de")

	// 1. DER .cer file + multi-PEM .pem bundle + non-cert README + .zip containing another cert
	if err := os.WriteFile(filepath.Join(certsDir, "alice.cer"), der1, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certsDir, "bundle.pem"), append(pem2, pem3...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certsDir, "README.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, err := zw.Create("dave.crt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(pem4)
	_ = zw.Close()
	if err := os.WriteFile(filepath.Join(certsDir, "more.zip"), zipBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	// First bulk import: all 4 new
	res, err := eng.ImportSMIMECertPaths([]string{certsDir})
	if err != nil {
		t.Fatalf("ImportSMIMECertPaths(folder): %v", err)
	}
	if res.Imported != 4 || res.Updated != 0 || res.Failed != 0 || res.Skipped != 1 {
		t.Fatalf("unexpected first bulk import result: %+v", res)
	}
	if got := len(eng.ListSMIMEContacts()); got != 4 {
		t.Fatalf("expected 4 stored S/MIME contacts, got %d", got)
	}

	// Second bulk import of same folder: all 4 updated
	res2, err := eng.ImportSMIMECertPaths([]string{certsDir})
	if err != nil {
		t.Fatalf("second ImportSMIMECertPaths: %v", err)
	}
	if res2.Imported != 0 || res2.Updated != 4 {
		t.Fatalf("expected 0 imported / 4 updated on repeat, got %+v", res2)
	}
}

func TestEncryptedSMIMEInviteAndAttachmentExtract(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	eng, err := New(Options{
		DBPath:       filepath.Join(dir, "postdive.db"),
		Secrets:      secretstore.NewMemoryStore(),
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()

	// Create Frank's S/MIME identity and import .p12
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:   big.NewInt(12345),
		Subject:        pkix.Name{CommonName: "Frank Hess"},
		EmailAddresses: []string{"frank.hess@avono.de"},
		NotBefore:      time.Now().Add(-time.Hour),
		NotAfter:       time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	p12, err := pkcs12.Modern.Encode(key, cert, nil, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.smime.ImportPKCS12(p12, "pw"); err != nil {
		t.Fatal(err)
	}

	// Build an inner multipart email containing an .ics invitation, then S/MIME-encrypt it.
	inner := "From: Axel <axel@avono.de>\r\n" +
		"To: Frank <frank.hess@avono.de>\r\n" +
		"Subject: Fwd: Kickoff\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"bnd123\"\r\n\r\n" +
		"--bnd123\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nSiehe Einladung.\r\n" +
		"--bnd123\r\nContent-Type: text/calendar; charset=utf-8; method=REQUEST; name=\"invite.ics\"\r\n" +
		"Content-Disposition: attachment; filename=\"invite.ics\"\r\n\r\n" +
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\n" +
		"UID:enc-kickoff-1@avono.de\r\nDTSTART:20261125T100000Z\r\nDTEND:20261125T110000Z\r\n" +
		"SUMMARY:Kickoff Avono\r\nORGANIZER:mailto:oscar@example.ch\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n" +
		"--bnd123--\r\n"

	encEML, err := smime.EncryptMIME([]byte(inner), []*x509.Certificate{cert})
	if err != nil {
		t.Fatalf("EncryptMIME: %v", err)
	}

	ctx := context.Background()
	// Store encrypted message in SQLite with empty ICSBody (plaintext is never stored in SQLite).
	if err := eng.store.UpsertMessage(ctx, StoredMessage{
		ID:          "m-enc-ics",
		AccountID:   "acc-1",
		FolderID:    "f-inbox",
		FolderPath:  "INBOX",
		UID:         10,
		Subject:     "Fwd: Kickoff",
		From:        "Axel <axel@avono.de>",
		DateISO:     time.Now().UTC().Format(time.RFC3339),
		DateUnix:    time.Now().Unix(),
		BodyFetched: true,
		RawEML:      encEML,
	}); err != nil {
		t.Fatal(err)
	}

	// 1. RespondMessageICS must decrypt RawEML on the fly and import the invite.
	res, err := eng.RespondMessageICS(ctx, "m-enc-ics", LocalCalendarID, "accepted")
	if err != nil {
		t.Fatalf("RespondMessageICS on encrypted S/MIME invite: %v", err)
	}
	if res.ImportedCount != 1 || res.Status != "accepted" {
		t.Fatalf("unexpected encrypted RSVP result: %+v", res)
	}

	// 2. ReadAttachmentPreview must also extract invite.ics from the decrypted inner MIME.
	att, err := eng.ReadAttachmentPreview(ctx, "m-enc-ics", "", "invite.ics")
	if err != nil {
		t.Fatalf("ReadAttachmentPreview on encrypted S/MIME mail: %v", err)
	}
	if !strings.Contains(string(att.Data), "SUMMARY:Kickoff Avono") {
		t.Fatalf("unexpected decrypted attachment bytes: %q", string(att.Data))
	}
}
