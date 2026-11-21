//go:build linux && !android

package secretstore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// newPlatformSecretStore stores secrets encrypted with AES-256-GCM on Linux. When the desktop
// session provides libsecret (secret-tool), a random 32-byte master key is kept in GNOME Keyring /
// KWallet; otherwise a machine+user-bound AES-256 key wraps the secrets file so plaintext JSON
// never sits unencrypted on disk.
func newPlatformSecretStore(appName, sealedPath, plainPath string) Store {
	if _, err := exec.LookPath("secret-tool"); err == nil {
		return NewSealedFileStore(sealedPath, func() ([]byte, error) {
			if k, ok := loadOrCreateLibsecretKey(appName); ok {
				return k, nil
			}
			return deriveLinuxMachineKey(appName, sealedPath), nil
		}, plainPath)
	}
	return NewSealedFileStore(sealedPath, func() ([]byte, error) {
		return deriveLinuxMachineKey(appName, sealedPath), nil
	}, plainPath)
}

func loadOrCreateLibsecretKey(appName string) ([]byte, bool) {
	service := "maildiver-master"
	if appName != "" {
		service = strings.ToLower(strings.TrimSpace(appName)) + "-master"
	}
	out, err := exec.Command("secret-tool", "lookup", "service", service, "account", "master-key").Output()
	if err == nil {
		if raw, derr := hex.DecodeString(strings.TrimSpace(string(out))); derr == nil && len(raw) == 32 {
			return raw, true
		}
	}
	key := make([]byte, 32)
	if _, rerr := rand.Read(key); rerr != nil {
		return nil, false
	}
	cmd := exec.Command("secret-tool", "store", "--label=MailDiver Master Key", "service", service, "account", "master-key")
	cmd.Stdin = strings.NewReader(hex.EncodeToString(key))
	if serr := cmd.Run(); serr != nil {
		return nil, false
	}
	return key, true
}

func deriveLinuxMachineKey(appName, sealedPath string) []byte {
	mid, _ := os.ReadFile("/etc/machine-id")
	if len(mid) == 0 {
		mid, _ = os.ReadFile("/var/lib/dbus/machine-id")
	}
	uid := fmt.Sprintf("%d", os.Getuid())
	uname := ""
	if u, err := user.Current(); err == nil {
		uname = u.Username
	}
	sum := sha256.Sum256([]byte("maildiver-linux-seal|" + appName + "|" + string(mid) + "|" + uid + "|" + uname + "|" + filepath.Dir(sealedPath)))
	return sum[:]
}
