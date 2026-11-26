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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smallstep/pkcs7"
	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"github.com/diver80/postdive/internal/secretstore"
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
	for _, ca := range signingChain(cert, caCerts) {
		bundle.ChainPEM = append(bundle.ChainPEM, encodeCertPEM(ca))
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

// ImportPublicCert imports a contact's public X.509 certificate (PEM, DER, or PKCS#7) for encrypting emails to them.
func (m *Manager) ImportPublicCert(data []byte) (CertInfo, error) {
	infos, err := m.ImportPublicCerts(data)
	if err != nil {
		return CertInfo{}, err
	}
	return infos[0], nil
}

// ImportPublicCerts extracts and stores one or more public recipient X.509 certificates from
// a single PEM/DER cert, a multi-certificate PEM bundle, or a PKCS#7 (.p7b/.p7c) bundle.
// Pure CA certificates without email addresses in bundles are silently skipped.
func (m *Manager) ImportPublicCerts(data []byte) ([]CertInfo, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("leere Zertifikatsdatei")
	}
	var parsed []*x509.Certificate
	rest := data
	foundPEM := false
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		foundPEM = true
		rest = next
		switch strings.ToUpper(strings.TrimSpace(block.Type)) {
		case "CERTIFICATE", "X509 CERTIFICATE", "TRUSTED CERTIFICATE":
			if c, err := x509.ParseCertificate(block.Bytes); err == nil {
				parsed = append(parsed, c)
			}
		case "PKCS7", "CMS":
			if p7, err := pkcs7.Parse(block.Bytes); err == nil {
				parsed = append(parsed, p7.Certificates...)
			}
		}
	}
	if !foundPEM {
		if c, err := x509.ParseCertificate(data); err == nil {
			parsed = append(parsed, c)
		} else if p7, perr := pkcs7.Parse(data); perr == nil && len(p7.Certificates) > 0 {
			parsed = append(parsed, p7.Certificates...)
		} else {
			return nil, fmt.Errorf("ungültiges X.509-/PKCS#7-Zertifikat: %w", err)
		}
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("keine gültigen Zertifikate in Datei gefunden")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	var imported []CertInfo
	for _, cert := range parsed {
		if cert == nil {
			continue
		}
		emails := certEmails(cert)
		if len(emails) == 0 {
			// Skip CA/intermediate certificates bundled alongside end-entity certificates.
			continue
		}
		info := buildCertInfo(cert, false)
		certPEM := encodeCertPEM(cert)
		for _, em := range emails {
			recInfo := info
			recInfo.Email = em
			m.state.Contacts[em] = storedCertRecord{
				Info:    recInfo,
				CertPEM: certPEM,
			}
			imported = append(imported, recInfo)
		}
	}
	if len(imported) == 0 {
		return nil, fmt.Errorf("Zertifikat enthält keine E-Mail-Adresse (SAN/CN)")
	}
	if err := m.saveDiskLocked(); err != nil {
		return nil, err
	}
	return imported, nil
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

// CacheContactCert stores a verified sender's non-expired public certificate automatically,
// without overwriting an already-stored certificate that has a different fingerprint.
func (m *Manager) CacheContactCert(cert *x509.Certificate) {
	m.CacheContactCertFor(cert, "")
}

// CacheContactCertFor stores a verified, trusted sender's non-expired public certificate for
// fromAddr (or all matching cert emails when fromAddr is empty). It refuses to silently
// overwrite an existing stored contact certificate whose fingerprint differs, preventing
// certificate-replacement attacks on contact entries.
func (m *Manager) CacheContactCertFor(cert *x509.Certificate, fromAddr string) {
	if m == nil || cert == nil {
		return
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return
	}
	emails := certEmails(cert)
	if len(emails) == 0 {
		return
	}
	var targets []string
	if want := strings.ToLower(strings.TrimSpace(fromAddr)); want != "" {
		for _, em := range emails {
			if em == want {
				targets = append(targets, em)
				break
			}
		}
		if len(targets) == 0 {
			return
		}
	} else {
		targets = emails
	}
	info := buildCertInfo(cert, false)
	certPEM := encodeCertPEM(cert)

	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	for _, em := range targets {
		if existing, ok := m.state.Contacts[em]; ok && existing.Info.Fingerprint != "" && existing.Info.Fingerprint != info.Fingerprint {
			// Never silently replace an existing contact cert with a different key.
			continue
		}
		recInfo := info
		recInfo.Email = em
		m.state.Contacts[em] = storedCertRecord{
			Info:    recInfo,
			CertPEM: certPEM,
		}
		changed = true
	}
	if changed {
		_ = m.saveDiskLocked()
	}
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

// HasContactCert reports whether a public X.509 certificate exists for email (contact or own identity).
func (m *Manager) HasContactCert(email string) bool {
	if m == nil {
		return false
	}
	em := strings.ToLower(strings.TrimSpace(email))
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.state.Contacts[em]; ok {
		return true
	}
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
		Chain: signingChain(leaf, chain),
		Key:   privKey,
		Info:  rec.Info,
	}, nil
}

// AllIdentities loads all installed personal S/MIME identities (used for decrypting incoming mail).
func (m *Manager) AllIdentities() []*Identity {
	ids, _ := m.AllIdentitiesWithErr()
	return ids
}

// AllIdentitiesWithErr loads all installed personal S/MIME identities and surfaces any key-loading
// error when identities are registered on disk but none could be loaded from secretstore.
func (m *Manager) AllIdentitiesWithErr() ([]*Identity, error) {
	if m == nil {
		return nil, nil
	}
	infos := m.ListIdentities()
	out := make([]*Identity, 0, len(infos))
	var lastErr error
	for _, inf := range infos {
		id, err := m.LoadIdentity(inf.Email)
		if err == nil {
			out = append(out, id)
		} else {
			lastErr = err
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
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
				now := time.Now()
				if !now.Before(c.NotBefore) && !now.After(c.NotAfter) {
					certs = append(certs, c)
					continue
				}
			}
		}
		missing = append(missing, em)
	}
	return certs, missing
}

// signingChain puts the certificates of a .p12 in the order a signature needs them: the issuer of
// leaf first, then its issuer, up to the root. Some exports (SwissSign, for one) also list the
// leaf itself among the CA certificates; signing then fails with "parent certificate cannot sign
// this kind of certificate", so the leaf, duplicates, and non-CA certificates are dropped.
func signingChain(leaf *x509.Certificate, certs []*x509.Certificate) []*x509.Certificate {
	var pool []*x509.Certificate
	for _, c := range certs {
		if c == nil || !c.IsCA || (leaf != nil && c.Equal(leaf)) || slices.ContainsFunc(pool, c.Equal) {
			continue
		}
		pool = append(pool, c)
	}
	if leaf == nil {
		return pool
	}
	now := time.Now()
	isValidNow := func(c *x509.Certificate) bool {
		return !now.Before(c.NotBefore) && !now.After(c.NotAfter)
	}
	out := make([]*x509.Certificate, 0, len(pool))
	used := make([]bool, len(pool))
	for cur := leaf; ; {
		next := -1
		for i, c := range pool {
			if !used[i] && cur.CheckSignatureFrom(c) == nil {
				if isValidNow(c) {
					next = i
					break
				}
				if next < 0 {
					next = i
				}
			}
		}
		if next < 0 {
			break
		}
		used[next] = true
		out = append(out, pool[next])
		cur = pool[next]
		if cur.CheckSignatureFrom(cur) == nil {
			break // self-signed root
		}
	}
	for i, c := range pool {
		if !used[i] {
			out = append(out, c)
		}
	}
	return out
}
