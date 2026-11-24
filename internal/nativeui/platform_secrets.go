package nativeui

import (
	"crypto/rand"
	"errors"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"

	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

// secretsKeyName is the entry in the system's credential store holding the key of the sealed
// secrets file on phones.
const secretsKeyName = "postdive-secrets-key"

// SetupPlatformSecrets chooses where passwords and tokens go on platforms without a usable
// keychain command. On Android they go to a file in the app's storage, encrypted with a key that
// the Android keystore protects (mygo.Secrets); a plain secrets.json of earlier builds is taken
// over. Call it before the mail engine starts.
func SetupPlatformSecrets() {
	if runtime.GOOS != "android" {
		return
	}
	dir, err := config.ConfigDir()
	if err != nil {
		return
	}
	secretstore.SetDefault(secretstore.NewSealedFileStore(
		filepath.Join(dir, "secrets.sealed"), keystoreSecretsKey, filepath.Join(dir, "secrets.json")))
}

// keystoreSecretsKey returns the 32-byte key kept in the system's credential store, making one
// the first time. A key that cannot be read for another reason is an error rather than a new
// key: a new one would make the stored secrets unreadable.
func keystoreSecretsKey() ([]byte, error) {
	k, err := mygo.Secrets.Get(secretsKeyName)
	if err == nil && len(k) == 32 {
		return k, nil
	}
	if err != nil && !errors.Is(err, mygo.ErrSecretNotFound) {
		return nil, err
	}
	k = make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := mygo.Secrets.Set(secretsKeyName, k); err != nil {
		return nil, err
	}
	return k, nil
}

// RepairTransferredAccounts fixes configurations that a transfer from the desktop wrote before
// it enabled the accounts it added: on phones, accounts outside the mailbox policy were never
// enabled on purpose (there is no switch for it there), so they are enabled and read-only goes.
func RepairTransferredAccounts(load func() (config.Config, error), save func(config.Config) error) {
	if runtime.GOOS != "android" || load == nil || save == nil {
		return
	}
	cfg, err := load()
	if err != nil {
		return
	}
	changed := false
	for _, acc := range cfg.Direct.Accounts {
		if acc.ID != "" && !cfg.Mailbox.AccountEnabled(acc.ID) {
			cfg.Mailbox.TriageAccountIDs = append(cfg.Mailbox.TriageAccountIDs, acc.ID)
			changed = true
		}
	}
	if changed || (cfg.Mailbox.ReadOnly && len(cfg.Direct.Accounts) > 0) {
		cfg.Mailbox.ReadOnly = false
		_ = save(cfg)
	}
}
