//go:build windows

package secretstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modCrypt32             = syscall.NewLazyDLL("crypt32.dll")
	modKernel32            = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtectData   = modCrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = modCrypt32.NewProc("CryptUnprotectData")
	procLocalFree          = modKernel32.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func dpapiProtect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("empty plaintext")
	}
	in := dataBlob{cbData: uint32(len(plain)), pbData: &plain[0]}
	var out dataBlob
	r1, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0, 1, // CRYPTPROTECT_UI_FORBIDDEN = 0x1
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	res := make([]byte, out.cbData)
	copy(res, unsafe.Slice(out.pbData, out.cbData))
	return res, nil
}

func dpapiUnprotect(cipher []byte) ([]byte, error) {
	if len(cipher) == 0 {
		return nil, errors.New("empty ciphertext")
	}
	in := dataBlob{cbData: uint32(len(cipher)), pbData: &cipher[0]}
	var out dataBlob
	r1, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0, 1,
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	res := make([]byte, out.cbData)
	copy(res, unsafe.Slice(out.pbData, out.cbData))
	return res, nil
}

// newPlatformSecretStore keeps a DPAPI-protected random 32-byte master key alongside an
// AES-256-GCM sealed store so stored secrets are bound to the Windows user login.
func newPlatformSecretStore(_ string, sealedPath, plainPath string) Store {
	keyPath := filepath.Join(filepath.Dir(sealedPath), "master.dpapi")
	return NewSealedFileStore(sealedPath, func() ([]byte, error) {
		if raw, err := os.ReadFile(keyPath); err == nil {
			if plain, derr := dpapiUnprotect(raw); derr == nil {
				if k, herr := hex.DecodeString(strings.TrimSpace(string(plain))); herr == nil && len(k) == 32 {
					return k, nil
				}
			}
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		enc, err := dpapiProtect([]byte(hex.EncodeToString(key)))
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyPath, enc, 0o600); err != nil {
			return nil, err
		}
		return key, nil
	}, plainPath)
}
