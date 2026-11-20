package secretstore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/diver80/maildiver/internal/config"
)

// ErrNotFound is returned when a secret key does not exist in the store.
var ErrNotFound = errors.New("secret not found")

const (
	keychainService       = "dev.maildiver.app"
	legacyKeychainService = "dev.omathunder.direct"
)

// Store persists sensitive credentials (IMAP/SMTP passwords, OAuth tokens)
// outside of config.json.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Default returns the platform credential store: macOS Keychain with automatic
// fallback to a 0600 file in the config directory on headless macOS or other OSes.
// Reads transparently fall back to legacy OmaThunder entries so existing passwords
// and OAuth tokens continue working seamlessly after rebranding to MailDiver.
func Default() Store {
	dir, err := config.ConfigDir()
	if err != nil {
		dir = "."
	}
	fb := NewFileStore(filepath.Join(dir, "secrets.json"))
	var legacyFB *FileStore
	if legacyDir, lerr := config.LegacyConfigDir(); lerr == nil && legacyDir != dir {
		legacyFB = NewFileStore(filepath.Join(legacyDir, "secrets.json"))
	}
	if runtime.GOOS == "darwin" {
		return &keychainStore{
			service:        keychainService,
			legacyService:  legacyKeychainService,
			fallback:       fb,
			legacyFallback: legacyFB,
		}
	}
	if legacyFB != nil {
		return &fallbackStore{primary: fb, legacy: legacyFB}
	}
	return fb
}

// fallbackStore reads from primary first, falling back to legacy on ErrNotFound.
type fallbackStore struct {
	primary Store
	legacy  Store
}

func (s *fallbackStore) Get(key string) (string, error) {
	if v, err := s.primary.Get(key); err == nil {
		return v, nil
	}
	if s.legacy != nil {
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
	if s.legacy != nil {
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
	if _, err := tmp.Write(append(b, '\n')); err != nil {
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
	keychainB64Prefix = "b64:"
	keychainTimeout   = 8 * time.Second
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

func decodeKeychainSecret(raw string) string {
	s := strings.TrimSuffix(raw, "\n")
	if strings.HasPrefix(s, keychainB64Prefix) {
		if b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, keychainB64Prefix)); err == nil {
			return string(b)
		}
	}
	return s
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
	service        string
	legacyService  string
	fallback       *FileStore
	legacyFallback *FileStore
	mu             sync.Mutex
}

func (k *keychainStore) Get(key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if key == "" {
		return "", ErrNotFound
	}
	if validKeychainKey(key) {
		ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
		out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", k.service, "-a", key, "-w").Output()
		cancel()
		if err == nil {
			return decodeKeychainSecret(string(out)), nil
		}
	}
	// Check MailDiver fallback before legacy service so newly saved fallback secrets are never shadowed by older legacy Keychain entries.
	if k.fallback != nil {
		if v, ferr := k.fallback.Get(key); ferr == nil {
			return v, nil
		}
	}
	if k.legacyService != "" && validKeychainKey(key) {
		ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
		out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", k.legacyService, "-a", key, "-w").Output()
		cancel()
		if err == nil {
			return decodeKeychainSecret(string(out)), nil
		}
	}
	if k.legacyFallback != nil {
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
	if validKeychainKey(key) {
		// Store a printable-ASCII b64:-prefixed payload via `security -i` stdin so:
		// 1) secrets never appear in argv (`ps`),
		// 2) `security find-generic-password -w` never switches to raw hex output for non-ASCII/umlaut/newline passwords,
		// 3) empty values are stored reliably.
		encoded := keychainB64Prefix + base64.StdEncoding.EncodeToString([]byte(value))
		script := fmt.Sprintf("add-generic-password -U -s %q -a %q -w %q\n", k.service, key, encoded)
		if out, err := runSecurityCmd(script, "-i"); err == nil && !bytes.Contains(bytes.ToLower(out), []byte("security:")) {
			if k.fallback != nil {
				_ = k.fallback.Delete(key)
			}
			return nil
		}
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
	if validKeychainKey(key) {
		_, _ = runSecurityCmd("", "delete-generic-password", "-s", k.service, "-a", key)
		if k.legacyService != "" {
			_, _ = runSecurityCmd("", "delete-generic-password", "-s", k.legacyService, "-a", key)
		}
	}
	if k.legacyFallback != nil {
		_ = k.legacyFallback.Delete(key)
	}
	if k.fallback != nil {
		return k.fallback.Delete(key)
	}
	return nil
}
