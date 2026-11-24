package mailengine

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/secretstore"
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
		DBPath:       filepath.Join(dir, "maildiver.db"),
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
