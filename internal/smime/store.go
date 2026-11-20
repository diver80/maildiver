package smime

import (
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/diver80/maildiver/internal/secretstore"
)

// CertInfo is non-secret metadata for an S/MIME personal identity or contact public certificate.
type CertInfo struct {
	Email         string   `json:"email"`
	Emails        []string `json:"emails,omitempty"`
	Subject       string   `json:"subject"`
	Issuer        string   `json:"issuer"`
	Serial        string   `json:"serial"`
	Fingerprint   string   `json:"fingerprint"`
	NotBefore     string   `json:"notBefore"`
	NotAfter      string   `json:"notAfter"`
	HasPrivateKey bool     `json:"hasPrivateKey"`
	AddedAt       string   `json:"addedAt"`
}

// Identity holds a loaded personal S/MIME certificate, CA chain, and private key.
type Identity struct {
	Cert  *x509.Certificate
	Chain []*x509.Certificate
	Key   crypto.PrivateKey
	Info  CertInfo
}

type storedCertRecord struct {
	Info    CertInfo `json:"info"`
	CertPEM string   `json:"certPem"`
}

type certsDiskState struct {
	Identities map[string]storedCertRecord `json:"identities"`
	Contacts   map[string]storedCertRecord `json:"contacts"`
}

type secretIdentityBundle struct {
	KeyPEM   string   `json:"keyPem"`
	CertPEM  string   `json:"certPem"`
	ChainPEM []string `json:"chainPem,omitempty"`
}

// Manager stores S/MIME personal identities (private keys in OS Keychain / secretstore)
// and contact public certificates (on disk in certs.json).
type Manager struct {
	mu      sync.RWMutex
	dir     string
	secrets secretstore.Store
	state   certsDiskState
}

// NewManager creates an S/MIME certificate manager backed by dir and secretstore.
func NewManager(dir string, secrets secretstore.Store) *Manager {
	if secrets == nil {
		secrets = secretstore.NewMemoryStore()
	}
	m := &Manager{
		dir:     dir,
		secrets: secrets,
		state: certsDiskState{
			Identities: make(map[string]storedCertRecord),
			Contacts:   make(map[string]storedCertRecord),
		},
	}
	m.loadDisk()
	return m
}

func (m *Manager) diskPath() string {
	if m.dir == "" {
		return ""
	}
	return filepath.Join(m.dir, "certs.json")
}

func (m *Manager) loadDisk() {
	p := m.diskPath()
	if p == "" {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var st certsDiskState
	if err := json.Unmarshal(raw, &st); err != nil {
		return
	}
	if st.Identities == nil {
		st.Identities = make(map[string]storedCertRecord)
	}
	if st.Contacts == nil {
		st.Contacts = make(map[string]storedCertRecord)
	}
	m.state = st
}

func (m *Manager) saveDiskLocked() error {
	p := m.diskPath()
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func secretKeyForIdentity(email string) string {
	return "smime:id:" + strings.ToLower(strings.TrimSpace(email))
}

func certEmails(cert *x509.Certificate) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || !strings.Contains(s, "@") || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, em := range cert.EmailAddresses {
		add(em)
	}
	if strings.Contains(cert.Subject.CommonName, "@") {
		add(cert.Subject.CommonName)
	}
	return out
}

// CertFingerprint returns the SHA-256 fingerprint of cert as uppercase hex pairs.
func CertFingerprint(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func buildCertInfo(cert *x509.Certificate, hasKey bool) CertInfo {
	emails := certEmails(cert)
	primary := ""
	if len(emails) > 0 {
		primary = emails[0]
	}
	subj := strings.TrimSpace(cert.Subject.CommonName)
	if subj == "" {
		subj = cert.Subject.String()
	}
	iss := strings.TrimSpace(cert.Issuer.CommonName)
	if iss == "" {
		iss = cert.Issuer.String()
	}
	return CertInfo{
		Email:         primary,
		Emails:        emails,
		Subject:       subj,
		Issuer:        iss,
		Serial:        cert.SerialNumber.Text(16),
		Fingerprint:   CertFingerprint(cert),
		NotBefore:     cert.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:      cert.NotAfter.UTC().Format(time.RFC3339),
		HasPrivateKey: hasKey,
		AddedAt:       time.Now().UTC().Format(time.RFC3339),
	}
}

func encodeCertPEM(cert *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

// ImportPKCS12 decodes a .p12/.pfx bundle, stores the private key + certificate chain in
// secretstore (OS Keychain), and persists public metadata in certs.json.
func (m *Manager) ImportPKCS12(p12Data []byte, password string) (CertInfo, error) {
	if len(p12Data) == 0 {
		return CertInfo{}, fmt.Errorf("leere PKCS#12-Datei")
	}
	privKey, cert, caCerts, err := pkcs12.DecodeChain(p12Data, password)
	if err != nil {
		return CertInfo{}, fmt.Errorf("PKCS#12 konnte nicht entschlüsselt werden (Passwort prüfen): %w", err)
	}
	if cert == nil || privKey == nil {
		return CertInfo{}, fmt.Errorf("PKCS#12 enthält kein vollständiges Zertifikat mit privatem Schlüssel")
	}
	emails := certEmails(cert)
	if len(emails) == 0 {
		return CertInfo{}, fmt.Errorf("Zertifikat enthält keine E-Mail-Adresse (SAN/CN)")
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return CertInfo{}, fmt.Errorf("privater Schlüssel konnte nicht serialisiert werden: %w", err)
	}
	bundle := secretIdentityBundle{
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})),
		CertPEM: encodeCertPEM(cert),
	}
	for _, ca := range caCerts {
		if ca != nil {
			bundle.ChainPEM = append(bundle.ChainPEM, encodeCertPEM(ca))
		}
	}
	bundleRaw, err := json.Marshal(bundle)
	if err != nil {
		return CertInfo{}, err
	}
	info := buildCertInfo(cert, true)

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, em := range emails {
		if err := m.secrets.Set(secretKeyForIdentity(em), string(bundleRaw)); err != nil {
			return CertInfo{}, fmt.Errorf("Schlüsselbund-Fehler: %w", err)
		}
		recInfo := info
		recInfo.Email = em
		m.state.Identities[em] = storedCertRecord{
			Info:    recInfo,
			CertPEM: bundle.CertPEM,
		}
	}
	if err := m.saveDiskLocked(); err != nil {
		return CertInfo{}, err
	}
	return info, nil
}

// ImportPublicCert imports a contact's public X.509 certificate (PEM or DER) for encrypting emails to them.
func (m *Manager) ImportPublicCert(data []byte) (CertInfo, error) {
	if len(data) == 0 {
		return CertInfo{}, fmt.Errorf("leere Zertifikatsdatei")
	}
	var cert *x509.Certificate
	if block, _ := pem.Decode(data); block != nil {
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return CertInfo{}, fmt.Errorf("ungültiges PEM-Zertifikat: %w", err)
		}
		cert = c
	} else {
		c, err := x509.ParseCertificate(data)
		if err != nil {
			return CertInfo{}, fmt.Errorf("ungültiges X.509-Zertifikat: %w", err)
		}
		cert = c
	}
	return m.saveContactCert(cert)
}

func (m *Manager) saveContactCert(cert *x509.Certificate) (CertInfo, error) {
	if cert == nil {
		return CertInfo{}, fmt.Errorf("fehlendes Zertifikat")
	}
	emails := certEmails(cert)
	if len(emails) == 0 {
		return CertInfo{}, fmt.Errorf("Zertifikat enthält keine E-Mail-Adresse")
	}
	info := buildCertInfo(cert, false)
	certPEM := encodeCertPEM(cert)

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, em := range emails {
		recInfo := info
		recInfo.Email = em
		m.state.Contacts[em] = storedCertRecord{
			Info:    recInfo,
			CertPEM: certPEM,
		}
	}
	if err := m.saveDiskLocked(); err != nil {
		return CertInfo{}, err
	}
	return info, nil
}

// CacheContactCert stores a verified sender's non-expired public certificate automatically.
func (m *Manager) CacheContactCert(cert *x509.Certificate) {
	if m == nil || cert == nil {
		return
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return
	}
	if len(certEmails(cert)) == 0 {
		return
	}
	_, _ = m.saveContactCert(cert)
}

// ListIdentities returns all imported personal S/MIME identities sorted by email.
func (m *Manager) ListIdentities() []CertInfo {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CertInfo, 0, len(m.state.Identities))
	for _, rec := range m.state.Identities {
		out = append(out, rec.Info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out
}

// ListContactCerts returns all stored contact public certificates sorted by email.
func (m *Manager) ListContactCerts() []CertInfo {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CertInfo, 0, len(m.state.Contacts))
	for _, rec := range m.state.Contacts {
		out = append(out, rec.Info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out
}

// DeleteIdentity removes a personal S/MIME identity from secretstore and metadata.
func (m *Manager) DeleteIdentity(email string) error {
	em := strings.ToLower(strings.TrimSpace(email))
	if em == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.secrets.Delete(secretKeyForIdentity(em))
	delete(m.state.Identities, em)
	return m.saveDiskLocked()
}

// DeleteContactCert removes a contact's public certificate.
func (m *Manager) DeleteContactCert(email string) error {
	em := strings.ToLower(strings.TrimSpace(email))
	if em == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.state.Contacts, em)
	return m.saveDiskLocked()
}

// HasIdentity reports whether a personal S/MIME identity exists for email.
func (m *Manager) HasIdentity(email string) bool {
	if m == nil {
		return false
	}
	em := strings.ToLower(strings.TrimSpace(email))
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.state.Identities[em]
	return ok
}

// LoadIdentity loads the private key + leaf cert + chain for email from secretstore.
func (m *Manager) LoadIdentity(email string) (*Identity, error) {
	if m == nil {
		return nil, fmt.Errorf("S/MIME nicht initialisiert")
	}
	em := strings.ToLower(strings.TrimSpace(email))
	m.mu.RLock()
	rec, ok := m.state.Identities[em]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("kein S/MIME-Zertifikat für %s installiert", em)
	}
	raw, err := m.secrets.Get(secretKeyForIdentity(em))
	if err != nil {
		return nil, fmt.Errorf("S/MIME-Schlüssel für %s nicht im Schlüsselbund gefunden: %w", em, err)
	}
	var bundle secretIdentityBundle
	if err := json.Unmarshal([]byte(raw), &bundle); err != nil {
		return nil, fmt.Errorf("S/MIME-Schlüsselpaket beschädigt: %w", err)
	}
	keyBlock, _ := pem.Decode([]byte(bundle.KeyPEM))
	if keyBlock == nil {
		return nil, fmt.Errorf("ungültiger privater Schlüssel")
	}
	privKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("privater Schlüssel nicht lesbar: %w", err)
	}
	certBlock, _ := pem.Decode([]byte(bundle.CertPEM))
	if certBlock == nil {
		return nil, fmt.Errorf("ungültiges Zertifikat")
	}
	leaf, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("Zertifikat nicht lesbar: %w", err)
	}
	var chain []*x509.Certificate
	for _, cp := range bundle.ChainPEM {
		if b, _ := pem.Decode([]byte(cp)); b != nil {
			if ca, cerr := x509.ParseCertificate(b.Bytes); cerr == nil {
				chain = append(chain, ca)
			}
		}
	}
	return &Identity{
		Cert:  leaf,
		Chain: chain,
		Key:   privKey,
		Info:  rec.Info,
	}, nil
}

// AllIdentities loads all installed personal S/MIME identities (used for decrypting incoming mail).
func (m *Manager) AllIdentities() []*Identity {
	if m == nil {
		return nil
	}
	infos := m.ListIdentities()
	out := make([]*Identity, 0, len(infos))
	for _, inf := range infos {
		if id, err := m.LoadIdentity(inf.Email); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// LookupRecipientCerts resolves X.509 certificates for each recipient email (searching
// contact certs and personal identities) and returns any emails missing a certificate.
func (m *Manager) LookupRecipientCerts(emails []string) ([]*x509.Certificate, []string) {
	if m == nil {
		return nil, emails
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[string]bool)
	var certs []*x509.Certificate
	var missing []string
	for _, raw := range emails {
		em := strings.ToLower(strings.TrimSpace(raw))
		if em == "" || seen[em] {
			continue
		}
		seen[em] = true
		var certPEM string
		if rec, ok := m.state.Contacts[em]; ok {
			certPEM = rec.CertPEM
		} else if rec, ok := m.state.Identities[em]; ok {
			certPEM = rec.CertPEM
		}
		if certPEM == "" {
			missing = append(missing, em)
			continue
		}
		if b, _ := pem.Decode([]byte(certPEM)); b != nil {
			if c, err := x509.ParseCertificate(b.Bytes); err == nil {
				certs = append(certs, c)
				continue
			}
		}
		missing = append(missing, em)
	}
	return certs, missing
}
