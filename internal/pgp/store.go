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
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/diver80/maildiver/internal/secretstore"
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

func entityEmails(ent *openpgp.Entity) (primaryEmail string, emails []string, userID string) {
	if ent == nil {
		return "", nil, ""
	}
	seen := map[string]bool{}
	for _, id := range ent.Identities {
		if userID == "" && id != nil && id.Name != "" {
			userID = id.Name
		}
		if id == nil || id.UserId == nil {
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

func serializeEntityPrivateArmor(ent *openpgp.Entity) (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return "", err
	}
	if err := ent.SerializePrivate(w, nil); err != nil {
		_ = w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func decryptEntityPrivateKeys(ent *openpgp.Entity, passphrase string) error {
	pw := []byte(passphrase)
	if ent.PrivateKey != nil && ent.PrivateKey.Encrypted {
		if passphrase == "" {
			return fmt.Errorf("Passwort für OpenPGP-Privatschlüssel erforderlich")
		}
		if err := ent.PrivateKey.Decrypt(pw); err != nil {
			return fmt.Errorf("falsches Passwort für OpenPGP-Privatschlüssel: %w", err)
		}
	}
	for _, sub := range ent.Subkeys {
		if sub.PrivateKey != nil && sub.PrivateKey.Encrypted {
			if passphrase == "" {
				return fmt.Errorf("Passwort für OpenPGP-Subkey erforderlich")
			}
			if err := sub.PrivateKey.Decrypt(pw); err != nil {
				return fmt.Errorf("falsches Passwort für OpenPGP-Subkey: %w", err)
			}
		}
	}
	return nil
}

func parseKeyRingAny(raw []byte) (openpgp.EntityList, error) {
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
// Private keys are unlocked with passphrase (if encrypted) and saved into secretstore.
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
			privArmor, err := serializeEntityPrivateArmor(ent)
			if err != nil {
				return nil, fmt.Errorf("Privatschlüssel serialisieren: %w", err)
			}
			pubArmor, err := serializeEntityPublicArmor(ent)
			if err != nil {
				return nil, fmt.Errorf("Public Key serialisieren: %w", err)
			}
			info := buildKeyInfo(ent, true)
			m.mu.Lock()
			for _, em := range info.Emails {
				_ = m.secrets.Set(secretKeyForIdentity(em), privArmor)
				rec := info
				rec.Email = em
				m.state.Identities[em] = storedKeyRecord{Info: rec, PublicArmor: pubArmor}
			}
			if len(info.Emails) == 0 {
				key := strings.ToLower(info.Email)
				_ = m.secrets.Set(secretKeyForIdentity(key), privArmor)
				m.state.Identities[key] = storedKeyRecord{Info: info, PublicArmor: pubArmor}
			}
			err = m.saveDiskLocked()
			m.mu.Unlock()
			if err != nil {
				return nil, err
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
func (m *Manager) GenerateIdentity(name, email, _ string) (KeyInfo, error) {
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
	privArmor, err := serializeEntityPrivateArmor(ent)
	if err != nil {
		return KeyInfo{}, err
	}
	pubArmor, err := serializeEntityPublicArmor(ent)
	if err != nil {
		return KeyInfo{}, err
	}
	info := buildKeyInfo(ent, true)
	m.mu.Lock()
	if err := m.secrets.Set(secretKeyForIdentity(email), privArmor); err != nil {
		m.mu.Unlock()
		return KeyInfo{}, fmt.Errorf("Keychain speichern: %w", err)
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
	_ = m.secrets.Delete(secretKeyForIdentity(em))
	delete(m.state.Identities, em)
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
	m.mu.RUnlock()
	if !ok {
		return false
	}
	raw, err := m.secrets.Get(secretKeyForIdentity(em))
	return err == nil && strings.TrimSpace(raw) != ""
}

func (m *Manager) LoadIdentity(email string) (*openpgp.Entity, error) {
	em := strings.ToLower(strings.TrimSpace(email))
	raw, err := m.secrets.Get(secretKeyForIdentity(em))
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("kein OpenPGP-Privatschlüssel für %s gespeichert", em)
	}
	el, err := openpgp.ReadArmoredKeyRing(strings.NewReader(raw))
	if err != nil || len(el) == 0 {
		return nil, fmt.Errorf("OpenPGP-Identität %s nicht lesbar: %w", em, err)
	}
	if el[0].PrivateKey == nil {
		return nil, fmt.Errorf("OpenPGP-Schlüssel für %s enthält keinen Privatschlüssel", em)
	}
	return el[0], nil
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

// LookupRecipientKeys finds public OpenPGP entities for all recipient emails and lists missing ones.
func (m *Manager) LookupRecipientKeys(emails []string) (keys []*openpgp.Entity, missing []string) {
	seenFP := map[string]bool{}
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
		fp := hex.EncodeToString(el[0].PrimaryKey.Fingerprint[:])
		if !seenFP[fp] {
			seenFP[fp] = true
			keys = append(keys, el[0])
		}
	}
	return keys, missing
}

// FetchOnlinePublicKey queries WKD first, then keys.openpgp.org VKS by email over HTTPS
// (only called on explicit user request) and imports the retrieved public key into Contacts.
func (m *Manager) FetchOnlinePublicKey(ctx context.Context, rawEmail string) ([]KeyInfo, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(rawEmail))
	var em string
	if err == nil {
		em = strings.ToLower(strings.TrimSpace(addr.Address))
	} else {
		em = strings.ToLower(strings.TrimSpace(rawEmail))
	}
	at := strings.IndexByte(em, '@')
	if at <= 0 || at == len(em)-1 {
		return nil, fmt.Errorf("gültige E-Mail-Adresse erforderlich")
	}
	local, dom := em[:at], em[at+1:]

	client := &http.Client{Timeout: 10 * time.Second}
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
		return io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	}

	// Try RFC/draft WKD direct URL first, then keys.openpgp.org VKS.
	h := sha1.Sum([]byte(local))
	zHash := zBase32Encode(h[:])
	wkdURL := fmt.Sprintf("https://%s/.well-known/openpgpkey/hu/%s?l=%s", dom, zHash, local)
	if raw, werr := fetchBounded(wkdURL); werr == nil && len(raw) > 0 {
		if infos, ierr := m.ImportKeys(raw, ""); ierr == nil && len(infos) > 0 {
			return infos, nil
		}
	}

	vksURL := "https://keys.openpgp.org/vks/v1/by-email/" + em
	raw, verr := fetchBounded(vksURL)
	if verr != nil {
		return nil, fmt.Errorf("kein Schlüssel über WKD oder keys.openpgp.org gefunden (%v)", verr)
	}
	return m.ImportKeys(raw, "")
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
