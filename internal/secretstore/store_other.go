//go:build (!linux && !windows) || android

package secretstore

func newPlatformSecretStore(_ string, _ string, _ string) Store {
	return nil
}
