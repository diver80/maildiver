package smime

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/diver80/postdive/internal/secretstore"
)

// Some CA exports list the user's own certificate among the CA certificates and put the root
// before the intermediate. Signing with such an identity must still work.
func TestSignWithP12ThatListsTheLeafAmongItsCAs(t *testing.T) {
	root, rootKey := generateTestCert(t, "", "Test Root", nil, nil)
	inter, interKey := generateTestCA(t, "Test Intermediate", root, rootKey)
	leaf, leafKey := generateTestCert(t, "carol@example.com", "Carol", inter, interKey)

	p12, err := pkcs12.Modern.Encode(leafKey, leaf, []*x509.Certificate{leaf, root, inter}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(t.TempDir(), secretstore.NewMemoryStore())
	if _, err := m.ImportPKCS12(p12, "pw"); err != nil {
		t.Fatal(err)
	}
	id, err := m.LoadIdentity("carol@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(id.Chain) != 2 || !id.Chain[0].Equal(inter) || !id.Chain[1].Equal(root) {
		t.Fatalf("chain not ordered issuer-first without the leaf: %v", id.Chain)
	}
	signed, err := SignMIME([]byte("From: carol@example.com\r\nSubject: x\r\nContent-Type: text/plain\r\n\r\nhi\r\n"), id)
	if err != nil {
		t.Fatalf("SignMIME: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	if vr, _, err := VerifyMIME(signed, roots); err != nil || vr == nil || !vr.Valid || !vr.Trusted {
		t.Fatalf("VerifyMIME: vr=%+v err=%v", vr, err)
	}
}

// Identities stored by earlier versions still carry the leaf in their chain; loading fixes it.
func TestSigningChainDropsTheLeafAndOrdersIssuerFirst(t *testing.T) {
	root, rootKey := generateTestCert(t, "", "Test Root", nil, nil)
	inter, interKey := generateTestCA(t, "Test Intermediate", root, rootKey)
	leaf, _ := generateTestCert(t, "dave@example.com", "Dave", inter, interKey)
	got := signingChain(leaf, []*x509.Certificate{leaf, root, inter, root})
	if len(got) != 2 || !got[0].Equal(inter) || !got[1].Equal(root) {
		t.Fatalf("signingChain = %v", got)
	}
}

func generateTestCA(t *testing.T, name string, parent *x509.Certificate, parentKey *rsa.PrivateKey) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}
