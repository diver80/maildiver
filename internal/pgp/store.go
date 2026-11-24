package pgp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/diver80/postdive/internal/secretstore"
)

const (
	maxPGPImportBytes      = 4 * 1024 * 1024
	maxPGPOnlineFetchBytes = 512 * 1024
)

// KeyInfo is non-secret metadata for a personal OpenPGP secret key or contact public key.
type KeyInfo struct {
	Email         string   `json:"email"`
	Emails        []string `json:"emails,omitempty"`
	UserID        string   `json:"userId"`
	Fingerprint   string   `json:"fingerprint"`
	KeyID         string   `json:"keyId"`
	Algorithm     string   `json:"algorithm"`
	CreatedAt     string   `json:"createdAt"`
	HasPrivateKey bool     `json:"hasPrivateKey"`
	AddedAt       string   `json:"addedAt"`
}

type storedKeyRecord struct {
	Info        KeyInfo `json:"info"`
	PublicArmor string  `json:"publicArmor"`
}

type pgpDiskState struct {
	Identities map[string]storedKeyRecord `json:"identities"`
	Contacts   map[string]storedKeyRecord `json:"contacts"`
}

// Manager stores OpenPGP secret identities in OS keychain / secretstore and contact public keys on disk.
type Manager struct {
	mu      sync.RWMutex
	dir     string
	secrets secretstore.Store
	state   pgpDiskState
}

// NewManager creates an OpenPGP keyring manager backed by dir and secretstore.
func NewManager(dir string, secrets secretstore.Store) *Manager {
	if secrets == nil {
		secrets = secretstore.NewMemoryStore()
	}
	m := &Manager{
		dir:     dir,
		secrets: secrets,
		state: pgpDiskState{
			Identities: make(map[string]storedKeyRecord),
			Contacts:   make(map[string]storedKeyRecord),
		},
	}
	m.loadDisk()
	return m
}

func (m *Manager) diskPath() string {
	if m.dir == "" {
		return ""
	}
	return filepath.Join(m.dir, "pgp-keys.json")
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
	var st pgpDiskState
	if err := json.Unmarshal(raw, &st); err != nil {
		return
	}
	if st.Identities == nil {
		st.Identities = make(map[string]storedKeyRecord)
	}
	if st.Contacts == nil {
		st.Contacts = make(map[string]storedKeyRecord)
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
	return "pgp:id:" + strings.ToLower(strings.TrimSpace(email))
}

func secretPassForIdentity(email string) string {
	return "pgp:pass:" + strings.ToLower(strings.TrimSpace(email))
}

func isEntityExpired(ent *openpgp.Entity, now time.Time) bool {
	if ent == nil || ent.PrimaryKey == nil {
		return true
	}
	sig, _ := ent.PrimarySelfSignature()
	if sig != nil && sig.KeyLifetimeSecs != nil && *sig.KeyLifetimeSecs > 0 {
		exp := ent.PrimaryKey.CreationTime.Add(time.Duration(*sig.KeyLifetimeSecs) * time.Second)
		if now.After(exp) {
			return true
		}
	}
	return false
}

func isEntityRevokedOrExpired(ent *openpgp.Entity, now time.Time) (bool, string) {
	if ent == nil || ent.PrimaryKey == nil {
		return true, "ungültiger OpenPGP-Schlüssel"
	}
	if ent.Revoked(now) {
		return true, "OpenPGP-Schlüssel wurde widerrufen (revoked)"
	}
	if isEntityExpired(ent, now) {
		return true, "OpenPGP-Schlüssel ist abgelaufen (expired)"
	}
	return false, ""
}

func entityEmails(ent *openpgp.Entity) (primaryEmail string, emails []string, userID string) {
	if ent == nil {
		return "", nil, ""
	}
	// Deterministic ordering: primary identity first, then sorted by identity name.
	var ordered []*openpgp.Identity
	prim := ent.PrimaryIdentity()
	if prim != nil {
		ordered = append(ordered, prim)
	}
	names := make([]string, 0, len(ent.Identities))
	for name := range ent.Identities {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		id := ent.Identities[name]
		if id == nil || id == prim {
			continue
		}
		ordered = append(ordered, id)
	}

	seen := map[string]bool{}
	for _, id := range ordered {
		if id == nil {
			continue
		}
		if len(id.Revocations) > 0 {
			continue
		}
		if userID == "" && id.Name != "" {
			userID = id.Name
		}
		if id.UserId == nil {
			continue
		}
		em := strings.ToLower(strings.TrimSpace(id.UserId.Email))
		if em == "" {
			if addr, err := mail.ParseAddress(id.Name); err == nil {
				em = strings.ToLower(strings.TrimSpace(addr.Address))
			}
		}
		if em != "" && !seen[em] {
			seen[em] = true
			if primaryEmail == "" {
				primaryEmail = em
			}
			emails = append(emails, em)
		}
	}
	return primaryEmail, emails, userID
}

func algoName(pk *packet.PublicKey) string {
	if pk == nil {
		return "OpenPGP"
	}
	switch pk.PubKeyAlgo {
	case packet.PubKeyAlgoRSA, packet.PubKeyAlgoRSASignOnly, packet.PubKeyAlgoRSAEncryptOnly:
		if bits, err := pk.BitLength(); err == nil && bits > 0 {
			return fmt.Sprintf("RSA-%d", bits)
		}
		return "RSA"
	case packet.PubKeyAlgoEdDSA:
		return "EdDSA"
	case packet.PubKeyAlgoECDSA:
		return "ECDSA"
	case packet.PubKeyAlgoECDH:
		return "ECDH"
	default:
		return "OpenPGP"
	}
}

func buildKeyInfo(ent *openpgp.Entity, hasPriv bool) KeyInfo {
	primaryEmail, emails, uid := entityEmails(ent)
	fp := strings.ToUpper(hex.EncodeToString(ent.PrimaryKey.Fingerprint[:]))
	keyID := strings.ToUpper(ent.PrimaryKey.KeyIdShortString())
	if primaryEmail == "" {
		primaryEmail = keyID
	}
	return KeyInfo{
		Email:         primaryEmail,
		Emails:        emails,
		UserID:        uid,
		Fingerprint:   fp,
		KeyID:         keyID,
		Algorithm:     algoName(ent.PrimaryKey),
		CreatedAt:     ent.PrimaryKey.CreationTime.UTC().Format(time.RFC3339),
		HasPrivateKey: hasPriv,
		AddedAt:       time.Now().UTC().Format(time.RFC3339),
	}
}

func serializeEntityPublicArmor(ent *openpgp.Entity) (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return "", err
	}
	if err := ent.Serialize(w); err != nil {
		_ = w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func serializeEntityPrivateArmor(ent *openpgp.Entity, passphrase string) (string, error) {
	if passphrase != "" {
		if err := ent.EncryptPrivateKeys([]byte(passphrase), nil); err != nil {
			return "", fmt.Errorf("Privatschlüssel verschlüsseln: %w", err)
		}
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return "", err
	}
	var serr error
	if passphrase != "" {
		serr = ent.SerializePrivateWithoutSigning(w, nil)
	} else {
		serr = ent.SerializePrivate(w, nil)
	}
	if serr != nil {
		_ = w.Close()
		return "", serr
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func decryptEntityPrivateKeys(ent *openpgp.Entity, passphrase string) error {
	needsDecrypt := ent.PrivateKey != nil && ent.PrivateKey.Encrypted
	if !needsDecrypt {
		for _, sub := range ent.Subkeys {
			if sub.PrivateKey != nil && sub.PrivateKey.Encrypted {
				needsDecrypt = true
				break
			}
		}
	}
	if !needsDecrypt {
		return nil
	}
	if passphrase == "" {
		return fmt.Errorf("Passwort für OpenPGP-Privatschlüssel erforderlich")
	}
	if err := ent.DecryptPrivateKeys([]byte(passphrase)); err != nil {
		return fmt.Errorf("falsches Passwort für OpenPGP-Privatschlüssel: %w", err)
	}
	return nil
}

func parseKeyRingAny(raw []byte) (openpgp.EntityList, error) {
	if len(raw) > maxPGPImportBytes {
		return nil, fmt.Errorf("OpenPGP-Schlüsseldaten überschreiten Limit von %d MB", maxPGPImportBytes/(1024*1024))
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("leerer OpenPGP-Schlüssel")
	}
	if bytes.Contains(trimmed, []byte("-----BEGIN PGP")) {
		el, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(trimmed))
		if err != nil {
			return nil, fmt.Errorf("ungültiger ASCII-Armored-PGP-Schlüssel: %w", err)
		}
		return el, nil
	}
	el, err := openpgp.ReadKeyRing(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("ungültiger binärer PGP-Schlüssel: %w", err)
	}
	return el, nil
}

// ImportKeys imports one or more ASCII-armored or binary OpenPGP public or private keys.
// Private keys are validated with passphrase (if encrypted) and saved into secretstore.
func (m *Manager) ImportKeys(raw []byte, passphrase string) ([]KeyInfo, error) {
	entities, err := parseKeyRingAny(raw)
	if err != nil {
		return nil, err
	}
	if len(entities) == 0 {
		return nil, fmt.Errorf("keine OpenPGP-Schlüssel gefunden")
	}
	var imported []KeyInfo
	for _, ent := range entities {
		if ent.PrivateKey != nil {
			if err := decryptEntityPrivateKeys(ent, passphrase); err != nil {
				return nil, err
			}
			pubArmor, err := serializeEntityPublicArmor(ent)
			if err != nil {
				return nil, fmt.Errorf("Public Key serialisieren: %w", err)
			}
			info := buildKeyInfo(ent, true)
			privArmor, err := serializeEntityPrivateArmor(ent, passphrase)
			if err != nil {
				return nil, fmt.Errorf("Privatschlüssel serialisieren: %w", err)
			}
			keys := info.Emails
			if len(keys) == 0 {
				keys = []string{strings.ToLower(info.Email)}
			}
			m.mu.Lock()
			var setErr error
			for _, em := range keys {
				if err := m.secrets.Set(secretKeyForIdentity(em), privArmor); err != nil {
					setErr = fmt.Errorf("Keychain speichern (%s): %w", em, err)
					break
				}
				if passphrase != "" {
					if err := m.secrets.Set(secretPassForIdentity(em), passphrase); err != nil {
						setErr = fmt.Errorf("Passwort speichern (%s): %w", em, err)
						break
					}
				} else {
					_ = m.secrets.Delete(secretPassForIdentity(em))
				}
				rec := info
				rec.Email = em
				m.state.Identities[em] = storedKeyRecord{Info: rec, PublicArmor: pubArmor}
			}
			if setErr == nil {
				setErr = m.saveDiskLocked()
			}
			m.mu.Unlock()
			if setErr != nil {
				return nil, setErr
			}
			imported = append(imported, info)
			continue
		}

		pubArmor, err := serializeEntityPublicArmor(ent)
		if err != nil {
			return nil, fmt.Errorf("Public Key serialisieren: %w", err)
		}
		info := buildKeyInfo(ent, false)
		m.mu.Lock()
		for _, em := range info.Emails {
			rec := info
			rec.Email = em
			m.state.Contacts[em] = storedKeyRecord{Info: rec, PublicArmor: pubArmor}
		}
		if len(info.Emails) == 0 {
			m.state.Contacts[strings.ToLower(info.KeyID)] = storedKeyRecord{Info: info, PublicArmor: pubArmor}
		}
		err = m.saveDiskLocked()
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		imported = append(imported, info)
	}
	return imported, nil
}

// GenerateIdentity creates a new 3072-bit RSA OpenPGP keypair for name/email and stores it in secretstore.
func (m *Manager) GenerateIdentity(name, email, passphrase string) (KeyInfo, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if email == "" || !strings.Contains(email, "@") {
		return KeyInfo{}, fmt.Errorf("gültige E-Mail-Adresse für OpenPGP-Schlüssel erforderlich")
	}
	if name == "" {
		name = email
	}
	for _, bad := range []string{"(", ")", "<", ">", "\x00"} {
		name = strings.ReplaceAll(name, bad, "")
		email = strings.ReplaceAll(email, bad, "")
	}
	cfg := &packet.Config{
		DefaultHash:   crypto.SHA256,
		DefaultCipher: packet.CipherAES256,
		RSABits:       3072,
	}
	ent, err := openpgp.NewEntity(name, "", email, cfg)
	if err != nil {
		return KeyInfo{}, fmt.Errorf("OpenPGP-Schlüssel erzeugen: %w", err)
	}
	pubArmor, err := serializeEntityPublicArmor(ent)
	if err != nil {
		return KeyInfo{}, err
	}
	info := buildKeyInfo(ent, true)
	privArmor, err := serializeEntityPrivateArmor(ent, passphrase)
	if err != nil {
		return KeyInfo{}, err
	}
	m.mu.Lock()
	if err := m.secrets.Set(secretKeyForIdentity(email), privArmor); err != nil {
		m.mu.Unlock()
		return KeyInfo{}, fmt.Errorf("Keychain speichern: %w", err)
	}
	if passphrase != "" {
		if err := m.secrets.Set(secretPassForIdentity(email), passphrase); err != nil {
			m.mu.Unlock()
			return KeyInfo{}, fmt.Errorf("Passwort speichern: %w", err)
		}
	} else {
		_ = m.secrets.Delete(secretPassForIdentity(email))
	}
	m.state.Identities[email] = storedKeyRecord{Info: info, PublicArmor: pubArmor}
	err = m.saveDiskLocked()
	m.mu.Unlock()
	if err != nil {
		return KeyInfo{}, err
	}
	return info, nil
}

// ExportPublicKeyArmor returns the ASCII-armored public key block for a stored identity or contact email.
func (m *Manager) ExportPublicKeyArmor(email string) (string, error) {
	em := strings.ToLower(strings.TrimSpace(email))
	m.mu.RLock()
	defer m.mu.RUnlock()
	if rec, ok := m.state.Identities[em]; ok && rec.PublicArmor != "" {
		return rec.PublicArmor, nil
	}
	if rec, ok := m.state.Contacts[em]; ok && rec.PublicArmor != "" {
		return rec.PublicArmor, nil
	}
	return "", fmt.Errorf("kein OpenPGP Public Key für %s vorhanden", em)
}

func (m *Manager) ListIdentities() []KeyInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]KeyInfo, 0, len(m.state.Identities))
	for _, rec := range m.state.Identities {
		out = append(out, rec.Info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out
}

func (m *Manager) ListContacts() []KeyInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]KeyInfo, 0, len(m.state.Contacts))
	for _, rec := range m.state.Contacts {
		out = append(out, rec.Info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out
}

func (m *Manager) DeleteIdentity(email string) error {
	em := strings.ToLower(strings.TrimSpace(email))
	m.mu.Lock()
	defer m.mu.Unlock()
	toRemove := map[string]bool{em: true}
	if rec, ok := m.state.Identities[em]; ok {
		fp := rec.Info.Fingerprint
		for _, alias := range rec.Info.Emails {
			toRemove[strings.ToLower(strings.TrimSpace(alias))] = true
		}
		if fp != "" {
			for k, other := range m.state.Identities {
				if other.Info.Fingerprint == fp {
					toRemove[k] = true
				}
			}
		}
	}
	for k := range toRemove {
		_ = m.secrets.Delete(secretKeyForIdentity(k))
		_ = m.secrets.Delete(secretPassForIdentity(k))
		delete(m.state.Identities, k)
	}
	return m.saveDiskLocked()
}

func (m *Manager) DeleteContact(email string) error {
	em := strings.ToLower(strings.TrimSpace(email))
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.state.Contacts, em)
	return m.saveDiskLocked()
}

func (m *Manager) HasIdentity(email string) bool {
	em := strings.ToLower(strings.TrimSpace(email))
	m.mu.RLock()
	_, ok := m.state.Identities[em]
	if !ok {
		m.mu.RUnlock()
		return false
	}
	raw, err := m.secrets.Get(secretKeyForIdentity(em))
	m.mu.RUnlock()
	return err == nil && strings.TrimSpace(raw) != ""
}

func (m *Manager) LoadIdentity(email string) (*openpgp.Entity, error) {
	em := strings.ToLower(strings.TrimSpace(email))
	m.mu.RLock()
	raw, err := m.secrets.Get(secretKeyForIdentity(em))
	pass, _ := m.secrets.Get(secretPassForIdentity(em))
	m.mu.RUnlock()
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("kein OpenPGP-Privatschlüssel für %s gespeichert", em)
	}
	el, err := openpgp.ReadArmoredKeyRing(strings.NewReader(raw))
	if err != nil || len(el) == 0 {
		return nil, fmt.Errorf("OpenPGP-Identität %s nicht lesbar: %w", em, err)
	}
	ent := el[0]
	if ent.PrivateKey == nil {
		return nil, fmt.Errorf("OpenPGP-Schlüssel für %s enthält keinen Privatschlüssel", em)
	}
	if err := decryptEntityPrivateKeys(ent, pass); err != nil {
		return nil, err
	}
	if bad, reason := isEntityRevokedOrExpired(ent, time.Now()); bad {
		return nil, fmt.Errorf("OpenPGP-Identität %s ungültig: %s", em, reason)
	}
	return ent, nil
}

// AllIdentities returns all unlocked personal OpenPGP entities from secretstore.
func (m *Manager) AllIdentities() []*openpgp.Entity {
	m.mu.RLock()
	emails := make([]string, 0, len(m.state.Identities))
	for em := range m.state.Identities {
		emails = append(emails, em)
	}
	m.mu.RUnlock()

	seenFP := map[string]bool{}
	var out []*openpgp.Entity
	for _, em := range emails {
		ent, err := m.LoadIdentity(em)
		if err != nil {
			continue
		}
		fp := hex.EncodeToString(ent.PrimaryKey.Fingerprint[:])
		if !seenFP[fp] {
			seenFP[fp] = true
			out = append(out, ent)
		}
	}
	return out
}

// AllEntities returns every personal secret entity plus every known contact public key for verification & decryption.
func (m *Manager) AllEntities() openpgp.EntityList {
	var all openpgp.EntityList
	seenFP := map[string]bool{}
	for _, ent := range m.AllIdentities() {
		fp := hex.EncodeToString(ent.PrimaryKey.Fingerprint[:])
		if !seenFP[fp] {
			seenFP[fp] = true
			all = append(all, ent)
		}
	}
	m.mu.RLock()
	armors := make([]string, 0, len(m.state.Contacts))
	for _, rec := range m.state.Contacts {
		if rec.PublicArmor != "" {
			armors = append(armors, rec.PublicArmor)
		}
	}
	m.mu.RUnlock()
	for _, arm := range armors {
		el, err := openpgp.ReadArmoredKeyRing(strings.NewReader(arm))
		if err != nil {
			continue
		}
		for _, ent := range el {
			fp := hex.EncodeToString(ent.PrimaryKey.Fingerprint[:])
			if !seenFP[fp] {
				seenFP[fp] = true
				all = append(all, ent)
			}
		}
	}
	return all
}

// LookupRecipientKeys finds valid, non-expired, non-revoked encryption-capable public OpenPGP entities
// for all recipient emails and lists missing or unusable ones.
func (m *Manager) LookupRecipientKeys(emails []string) (keys []*openpgp.Entity, missing []string) {
	seenFP := map[string]bool{}
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, rawEm := range emails {
		em := strings.ToLower(strings.TrimSpace(rawEm))
		if em == "" {
			continue
		}
		var pubArmor string
		if rec, ok := m.state.Contacts[em]; ok && rec.PublicArmor != "" {
			pubArmor = rec.PublicArmor
		} else if rec, ok := m.state.Identities[em]; ok && rec.PublicArmor != "" {
			pubArmor = rec.PublicArmor
		}
		if pubArmor == "" {
			missing = append(missing, em)
			continue
		}
		el, err := openpgp.ReadArmoredKeyRing(strings.NewReader(pubArmor))
		if err != nil || len(el) == 0 {
			missing = append(missing, em)
			continue
		}
		ent := el[0]
		if bad, _ := isEntityRevokedOrExpired(ent, now); bad {
			missing = append(missing, em)
			continue
		}
		if _, canEnc := ent.EncryptionKey(now); !canEnc {
			missing = append(missing, em)
			continue
		}
		fp := hex.EncodeToString(ent.PrimaryKey.Fingerprint[:])
		if !seenFP[fp] {
			seenFP[fp] = true
			keys = append(keys, ent)
		}
	}
	return keys, missing
}

var blockedOnlinePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func isBlockedOnlineIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	for _, pfx := range blockedOnlinePrefixes {
		if pfx.Contains(addr) {
			return true
		}
	}
	return false
}

func isSafePublicDomain(dom string) bool {
	dom = strings.ToLower(strings.TrimSpace(dom))
	if dom == "" || len(dom) > 253 || strings.ContainsAny(dom, ":/\\ \t\r\n?#@[]") {
		return false
	}
	if net.ParseIP(dom) != nil {
		return false
	}
	if dom == "localhost" || strings.HasSuffix(dom, ".localhost") || strings.HasSuffix(dom, ".local") || strings.HasSuffix(dom, ".internal") {
		return false
	}
	labels := strings.Split(dom, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			ch := l[i]
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return false
			}
		}
	}
	return true
}

func newSafePGPHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ipa := range ips {
				if isBlockedOnlineIP(ipa.IP) {
					return nil, fmt.Errorf("Keyserver-Ziel blockiert (private/lokale IP %s)", ipa.IP.String())
				}
			}
			for _, ipa := range ips {
				conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
				if derr == nil {
					return conn, nil
				}
				err = derr
			}
			return nil, err
		},
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("zu viele Weiterleitungen beim Keyserver-Abruf")
			}
			if !strings.EqualFold(req.URL.Scheme, "https") {
				return fmt.Errorf("unsichere Weiterleitung auf %s blockiert (nur HTTPS erlaubt)", req.URL.Scheme)
			}
			return nil
		},
	}
}

func (m *Manager) importValidatedOnlinePublicKey(raw []byte, expectedEmail string) ([]KeyInfo, error) {
	entities, err := parseKeyRingAny(raw)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var imported []KeyInfo
	for _, ent := range entities {
		// Never import private keys returned by an untrusted online keyserver.
		if ent.PrivateKey != nil {
			continue
		}
		if bad, _ := isEntityRevokedOrExpired(ent, now); bad {
			continue
		}
		_, emails, _ := entityEmails(ent)
		matched := false
		for _, em := range emails {
			if strings.EqualFold(em, expectedEmail) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		pubArmor, err := serializeEntityPublicArmor(ent)
		if err != nil {
			continue
		}
		info := buildKeyInfo(ent, false)
		info.Email = expectedEmail
		info.Emails = []string{expectedEmail}
		m.mu.Lock()
		m.state.Contacts[expectedEmail] = storedKeyRecord{Info: info, PublicArmor: pubArmor}
		err = m.saveDiskLocked()
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		imported = append(imported, info)
	}
	if len(imported) == 0 {
		return nil, fmt.Errorf("Online-Antwort enthielt keinen gültigen Public Key für %s", expectedEmail)
	}
	return imported, nil
}

// FetchOnlinePublicKey queries WKD first, then keys.openpgp.org VKS by email over HTTPS
// (only called on explicit user request) and imports the validated public key into Contacts.
func (m *Manager) FetchOnlinePublicKey(ctx context.Context, rawEmail string) ([]KeyInfo, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(rawEmail))
	if err != nil || addr == nil {
		return nil, fmt.Errorf("gültige E-Mail-Adresse erforderlich")
	}
	em := strings.ToLower(strings.TrimSpace(addr.Address))
	if strings.ContainsAny(em, " \t\r\n/\\?#") {
		return nil, fmt.Errorf("gültige E-Mail-Adresse erforderlich")
	}
	at := strings.IndexByte(em, '@')
	if at <= 0 || at == len(em)-1 {
		return nil, fmt.Errorf("gültige E-Mail-Adresse erforderlich")
	}
	local, dom := em[:at], em[at+1:]
	if !isSafePublicDomain(dom) {
		return nil, fmt.Errorf("ungültige oder nicht-öffentliche Domain %q für Online-Schlüsselsuche", dom)
	}

	client := newSafePGPHTTPClient()
	fetchBounded := func(u string) ([]byte, error) {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if rerr != nil {
			return nil, rerr
		}
		resp, derr := client.Do(req)
		if derr != nil {
			return nil, derr
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPGPOnlineFetchBytes+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > maxPGPOnlineFetchBytes {
			return nil, fmt.Errorf("Antwort vom Keyserver zu groß")
		}
		return raw, nil
	}

	// Try RFC/draft WKD direct URL first, then keys.openpgp.org VKS.
	h := sha1.Sum([]byte(local))
	zHash := zBase32Encode(h[:])
	wkdURL := fmt.Sprintf("https://%s/.well-known/openpgpkey/hu/%s?l=%s", dom, zHash, url.QueryEscape(local))
	if raw, werr := fetchBounded(wkdURL); werr == nil && len(raw) > 0 {
		if infos, ierr := m.importValidatedOnlinePublicKey(raw, em); ierr == nil && len(infos) > 0 {
			return infos, nil
		}
	}

	vksURL := "https://keys.openpgp.org/vks/v1/by-email/" + url.PathEscape(em)
	raw, verr := fetchBounded(vksURL)
	if verr != nil {
		return nil, fmt.Errorf("kein Schlüssel über WKD oder keys.openpgp.org gefunden (%v)", verr)
	}
	return m.importValidatedOnlinePublicKey(raw, em)
}

// zBase32Encode implements Phil Zimmermann's human-oriented base32 encoding used by OpenPGP WKD.
func zBase32Encode(data []byte) string {
	const alpha = "ybndrfg8ejkmcpqxot1uwisza345h769"
	var b strings.Builder
	var val, bits uint
	for _, c := range data {
		val = (val << 8) | uint(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			b.WriteByte(alpha[(val>>bits)&31])
		}
	}
	if bits > 0 {
		b.WriteByte(alpha[(val<<(5-bits))&31])
	}
	return b.String()
}
