package secretstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// defaultOverride replaces what Default returns (SetDefault).
var defaultOverride atomic.Pointer[Store]

// SetDefault makes Default return s: the app sets it where the platform keeps secrets its own
// way, as on Android.
func SetDefault(s Store) { defaultOverride.Store(&s) }

// sealedMagic starts a sealed file, so a plain one is told apart.
const sealedMagic = "MDSEAL1\n"

// NewSealedFileStore keeps the secrets in a file at path encrypted with AES-256-GCM. The 32-byte
// key comes from key, called once when first needed – on Android a key kept by the system's
// keystore, so that the file alone (a backup, a copy) reveals nothing. A plain secrets file at
// plainPath, as earlier versions wrote, is taken over and removed.
func NewSealedFileStore(path string, key func() ([]byte, error), plainPath string) Store {
	var (
		once sync.Once
		aead cipher.AEAD
		kerr error
	)
	getAEAD := func() (cipher.AEAD, error) {
		once.Do(func() {
			k, err := key()
			if err != nil {
				kerr = fmt.Errorf("secret key: %w", err)
				return
			}
			if len(k) != 32 {
				kerr = errors.New("secret key: need 32 bytes")
				return
			}
			block, err := aes.NewCipher(k)
			if err != nil {
				kerr = err
				return
			}
			aead, kerr = cipher.NewGCM(block)
		})
		return aead, kerr
	}
	fs := &FileStore{path: path}
	fs.seal = func(plain []byte) ([]byte, error) {
		g, err := getAEAD()
		if err != nil {
			return nil, err
		}
		nonce := make([]byte, g.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
		out := append([]byte(sealedMagic), nonce...)
		return g.Seal(out, nonce, plain, []byte(sealedMagic)), nil
	}
	fs.unseal = func(data []byte) ([]byte, error) {
		if len(data) < len(sealedMagic) || string(data[:len(sealedMagic)]) != sealedMagic {
			return nil, errors.New("secrets file is not sealed")
		}
		g, err := getAEAD()
		if err != nil {
			return nil, err
		}
		data = data[len(sealedMagic):]
		if len(data) < g.NonceSize() {
			return nil, errors.New("secrets file is damaged")
		}
		plain, err := g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte(sealedMagic))
		if err != nil {
			return nil, errors.New("secrets file cannot be decrypted (key lost or file damaged)")
		}
		return plain, nil
	}
	if plainPath != "" && plainPath != path {
		migratePlain(fs, plainPath)
	}
	return fs
}

// migratePlain copies the secrets of a plain file into the sealed store and removes the plain
// file once they are stored.
func migratePlain(sealed *FileStore, plainPath string) {
	if _, err := os.Stat(plainPath); err != nil {
		return
	}
	plain := NewFileStore(plainPath)
	plain.mu.Lock()
	m, err := plain.loadLocked()
	plain.mu.Unlock()
	if err != nil || len(m) == 0 {
		return
	}
	sealed.mu.Lock()
	cur, err := sealed.loadLocked()
	if err == nil {
		for k, v := range m {
			if _, ok := cur[k]; !ok {
				cur[k] = v
			}
		}
		err = sealed.saveLocked(cur)
	}
	sealed.mu.Unlock()
	if err == nil {
		_ = os.Remove(plainPath)
	}
}
