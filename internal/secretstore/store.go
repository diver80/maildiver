package secretstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/diver80/postdive/internal/config"
)

// ErrNotFound is returned when a secret key does not exist in the store.
var ErrNotFound = errors.New("secret not found")

const (
	keychainService       = "dev.postdive.app"
	legacyKeychainService = "dev.maildiver.app"
)

var legacyKeychainServices = []string{"dev.maildiver.app", "dev.omathunder.direct"}

// Store persists sensitive credentials (IMAP/SMTP passwords, OAuth tokens)
// outside of config.json.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Default returns the platform credential store: macOS Keychain with automatic
// fallback to a 0600 file in the config directory on headless macOS or other OSes.
// Reads transparently fall back to legacy MailDiver and OmaThunder entries so existing passwords
// and OAuth tokens continue working seamlessly after rebranding to PostDive.
func Default() Store {
	if d := defaultOverride.Load(); d != nil {
		return *d
	}
	dir, err := config.ConfigDir()
	if err != nil {
		dir = "."
	}
	fb := NewFileStore(filepath.Join(dir, "secrets.json"))
	var legacyFBs []*FileStore
	for _, legacyDir := range config.LegacyConfigDirs() {
		if legacyDir != "" && legacyDir != dir {
			legacyFBs = append(legacyFBs, NewFileStore(filepath.Join(legacyDir, "secrets.json")))
		}
	}
	if runtime.GOOS == "darwin" {
		var legacyFB *FileStore
		if len(legacyFBs) > 0 {
			legacyFB = legacyFBs[0]
		}
		return &keychainStore{
			service:         keychainService,
			legacyService:   legacyKeychainService,
			legacyServices:  legacyKeychainServices,
			fallback:        fb,
			legacyFallback:  legacyFB,
			legacyFallbacks: legacyFBs,
		}
	}
	var primary Store = fb
	if ps := newPlatformSecretStore("PostDive", filepath.Join(dir, "secrets.seal"), filepath.Join(dir, "secrets.json")); ps != nil {
		primary = ps
	}
	if len(legacyFBs) > 0 {
		stores := make([]Store, 0, len(legacyFBs))
		for _, lfb := range legacyFBs {
			stores = append(stores, lfb)
		}
		return &fallbackStore{primary: primary, legacy: legacyFBs[0], legacies: stores}
	}
	return primary
}

// fallbackStore reads from primary first, falling back to legacy stores on ErrNotFound.
type fallbackStore struct {
	primary  Store
	legacy   Store
	legacies []Store
}

func (s *fallbackStore) Get(key string) (string, error) {
	if v, err := s.primary.Get(key); err == nil {
		return v, nil
	}
	for _, l := range s.legacies {
		if l != nil {
			if v, err := l.Get(key); err == nil {
				return v, nil
			}
		}
	}
	if len(s.legacies) == 0 && s.legacy != nil {
		if v, err := s.legacy.Get(key); err == nil {
			return v, nil
		}
	}
	return "", ErrNotFound
}

func (s *fallbackStore) Set(key, value string) error {
	return s.primary.Set(key, value)
}

func (s *fallbackStore) Delete(key string) error {
	for _, l := range s.legacies {
		if l != nil {
			_ = l.Delete(key)
		}
	}
	if len(s.legacies) == 0 && s.legacy != nil {
		_ = s.legacy.Delete(key)
	}
	return s.primary.Delete(key)
}

// MemoryStore is an in-memory Store for unit tests.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: map[string]string{}}
}

func (m *MemoryStore) Get(key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *MemoryStore) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string]string{}
	}
	m.data[key] = value
	return nil
}

func (m *MemoryStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

// FileStore stores secrets in a mode-0600 JSON file (atomic write).
type FileStore struct {
	path string
	mu   sync.Mutex
	// seal and unseal, when set, encrypt the file's content (SealedFileStore).
	seal, unseal func([]byte) ([]byte, error)
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (f *FileStore) loadLocked() (map[string]string, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := map[string]string{}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	if f.unseal != nil {
		if data, err = f.unseal(data); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *FileStore) saveLocked(m map[string]string) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if f.seal != nil {
		if b, err = f.seal(b); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".secrets-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, f.path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func (f *FileStore) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.loadLocked()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *FileStore) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.loadLocked()
	if err != nil {
		return err
	}
	m[key] = value
	return f.saveLocked(m)
}

func (f *FileStore) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.loadLocked()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil
	}
	delete(m, key)
	return f.saveLocked(m)
}

const (
	keychainB64Prefix   = "b64:"
	keychainChunkPrefix = "b64c:"
	keychainPartPrefix  = "b64p:"
	// macOS `/usr/bin/security -i` truncates interactive stdin lines at 4096 bytes.
	// Keeping each encoded chunk <= 2800 bytes leaves ample room for command/service/account framing.
	keychainMaxChunkB64 = 2800
	keychainMaxChunks   = 128
	keychainTimeout     = 8 * time.Second
)

func validKeychainKey(key string) bool {
	if key == "" || len(key) > 256 {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

func decodeKeychainSecret(raw string) (string, bool) {
	s := strings.TrimSuffix(raw, "\n")
	if strings.HasPrefix(s, keychainB64Prefix) {
		b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, keychainB64Prefix))
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	return s, true
}

func parseKeychainChunkManifest(s string) (count int, shaHex string, ok bool) {
	if !strings.HasPrefix(s, keychainChunkPrefix) {
		return 0, "", false
	}
	rest := strings.TrimPrefix(s, keychainChunkPrefix)
	nStr, sum, found := strings.Cut(rest, ":")
	if !found || len(sum) != 64 {
		return 0, "", false
	}
	n, err := strconv.Atoi(nStr)
	if err != nil || n < 1 || n > keychainMaxChunks {
		return 0, "", false
	}
	return n, sum, true
}

func runSecurityCmd(stdin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd.CombinedOutput()
}

// keychainStore uses macOS /usr/bin/security (pure Go, CGO_ENABLED=0 compatible)
// and falls back to FileStore when Keychain is locked or unavailable.
type keychainStore struct {
	service         string
	legacyService   string
	legacyServices  []string
	fallback        *FileStore
	legacyFallback  *FileStore
	legacyFallbacks []*FileStore
	mu              sync.Mutex
}

func findKeychainRaw(service, key string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", service, "-a", key, "-w").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

func (k *keychainStore) getFromServiceLocked(service, key string) (val string, ok bool, corrupted bool) {
	if service == "" || !validKeychainKey(key) {
		return "", false, false
	}
	raw, err := findKeychainRaw(service, key)
	if err != nil {
		return "", false, false
	}
	if strings.HasPrefix(raw, keychainChunkPrefix) {
		n, wantSum, mok := parseKeychainChunkManifest(raw)
		if !mok {
			return "", false, true
		}
		var b64Buf strings.Builder
		for i := 0; i < n; i++ {
			partKey := fmt.Sprintf("%s#%d", key, i)
			partRaw, perr := findKeychainRaw(service, partKey)
			if perr != nil || !strings.HasPrefix(partRaw, keychainPartPrefix) {
				return "", false, true
			}
			b64Buf.WriteString(strings.TrimPrefix(partRaw, keychainPartPrefix))
		}
		decoded, derr := base64.StdEncoding.DecodeString(b64Buf.String())
		if derr != nil {
			return "", false, true
		}
		gotSum := fmt.Sprintf("%x", sha256.Sum256(decoded))
		if gotSum != wantSum {
			return "", false, true
		}
		return string(decoded), true, false
	}
	dec, decOK := decodeKeychainSecret(raw)
	if !decOK {
		return "", false, true
	}
	return dec, true, false
}

func (k *keychainStore) setKeychainLocked(key, value string) bool {
	if !validKeychainKey(key) {
		return false
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(value))
	if len(keychainB64Prefix)+len(b64) <= keychainMaxChunkB64 {
		// Small secret: single interactive command line (well below 4096-byte buffer).
		script := fmt.Sprintf("add-generic-password -U -s %q -a %q -w %q\n", k.service, key, keychainB64Prefix+b64)
		out, err := runSecurityCmd(script, "-i")
		return err == nil && !bytes.Contains(bytes.ToLower(out), []byte("security:"))
	}

	// Large secret (e.g. S/MIME identity bundle or OpenPGP private key): split into chunks <= 2800 bytes
	// plus a SHA-256 manifest so `/usr/bin/security -i` never silently truncates at 4096 bytes.
	n := (len(b64) + keychainMaxChunkB64 - 1) / keychainMaxChunkB64
	if n > keychainMaxChunks {
		return false
	}
	var script strings.Builder
	for i := 0; i < n; i++ {
		start := i * keychainMaxChunkB64
		end := start + keychainMaxChunkB64
		if end > len(b64) {
			end = len(b64)
		}
		partKey := fmt.Sprintf("%s#%d", key, i)
		fmt.Fprintf(&script, "add-generic-password -U -s %q -a %q -w %q\n", k.service, partKey, keychainPartPrefix+b64[start:end])
	}
	sum := sha256.Sum256([]byte(value))
	manifest := fmt.Sprintf("%s%d:%x", keychainChunkPrefix, n, sum)
	fmt.Fprintf(&script, "add-generic-password -U -s %q -a %q -w %q\n", k.service, key, manifest)

	out, err := runSecurityCmd(script.String(), "-i")
	if err != nil || bytes.Contains(bytes.ToLower(out), []byte("security:")) {
		return false
	}
	// Verify integrity by reading back before deleting fallback.
	got, ok, _ := k.getFromServiceLocked(k.service, key)
	return ok && got == value
}

func (k *keychainStore) deleteKeychainChunksLocked(service, key string) {
	if service == "" || !validKeychainKey(key) {
		return
	}
	if raw, err := findKeychainRaw(service, key); err == nil {
		if n, _, ok := parseKeychainChunkManifest(raw); ok {
			for i := 0; i < n; i++ {
				_, _ = runSecurityCmd("", "delete-generic-password", "-s", service, "-a", fmt.Sprintf("%s#%d", key, i))
			}
		}
	}
	_, _ = runSecurityCmd("", "delete-generic-password", "-s", service, "-a", key)
}

func (k *keychainStore) Get(key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if key == "" {
		return "", ErrNotFound
	}
	v, ok, corrupted := k.getFromServiceLocked(k.service, key)
	if ok {
		return v, nil
	}
	// Check PostDive fallback before legacy service so newly saved fallback secrets are never shadowed
	// by older legacy Keychain entries, and self-heal any previously truncated Keychain entry.
	if k.fallback != nil {
		if fv, ferr := k.fallback.Get(key); ferr == nil {
			if corrupted && k.setKeychainLocked(key, fv) {
				_ = k.fallback.Delete(key)
			}
			return fv, nil
		}
	}
	for _, ls := range k.legacyServices {
		if lv, lok, _ := k.getFromServiceLocked(ls, key); lok {
			return lv, nil
		}
	}
	if len(k.legacyServices) == 0 {
		if lv, lok, _ := k.getFromServiceLocked(k.legacyService, key); lok {
			return lv, nil
		}
	}
	for _, lfb := range k.legacyFallbacks {
		if lfb != nil {
			if v, ferr := lfb.Get(key); ferr == nil {
				return v, nil
			}
		}
	}
	if len(k.legacyFallbacks) == 0 && k.legacyFallback != nil {
		if v, ferr := k.legacyFallback.Get(key); ferr == nil {
			return v, nil
		}
	}
	return "", ErrNotFound
}

func (k *keychainStore) Set(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if key == "" {
		return fmt.Errorf("empty secret key")
	}
	if k.setKeychainLocked(key, value) {
		if k.fallback != nil {
			_ = k.fallback.Delete(key)
		}
		return nil
	}
	if k.fallback != nil {
		return k.fallback.Set(key, value)
	}
	return fmt.Errorf("macOS keychain unavailable")
}

func (k *keychainStore) Delete(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if key == "" {
		return nil
	}
	k.deleteKeychainChunksLocked(k.service, key)
	for _, ls := range k.legacyServices {
		k.deleteKeychainChunksLocked(ls, key)
	}
	if len(k.legacyServices) == 0 && k.legacyService != "" {
		k.deleteKeychainChunksLocked(k.legacyService, key)
	}
	for _, lfb := range k.legacyFallbacks {
		if lfb != nil {
			_ = lfb.Delete(key)
		}
	}
	if len(k.legacyFallbacks) == 0 && k.legacyFallback != nil {
		_ = k.legacyFallback.Delete(key)
	}
	if k.fallback != nil {
		return k.fallback.Delete(key)
	}
	return nil
}
