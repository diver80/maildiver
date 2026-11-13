package native

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/diver80/omathunder/internal/config"
)

type HostManifest struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Path              string   `json:"path"`
	Type              string   `json:"type"`
	AllowedExtensions []string `json:"allowed_extensions"`
}

// ManifestPaths returns all per-user NativeMessagingHosts locations for the current OS.
func ManifestPaths() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	filename := config.NativeHostName + ".json"
	switch runtime.GOOS {
	case "darwin":
		// Thunderbird reads ~/Library/Mozilla/NativeMessagingHosts; the other two cover
		// Firefox-style lookups and older builds.
		return []string{
			filepath.Join(home, "Library", "Mozilla", "NativeMessagingHosts", filename),
			filepath.Join(home, "Library", "Application Support", "Mozilla", "NativeMessagingHosts", filename),
			filepath.Join(home, "Library", "Thunderbird", "NativeMessagingHosts", filename),
		}, nil
	case "linux":
		return []string{
			filepath.Join(home, ".mozilla", "native-messaging-hosts", filename),
			filepath.Join(home, ".thunderbird", "native-messaging-hosts", filename),
		}, nil
	case "windows":
		cfgDir, err := config.ConfigDir()
		if err != nil {
			return nil, err
		}
		return []string{filepath.Join(cfgDir, filename)}, nil
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// StableBinaryPath maps a Homebrew Cellar path (…/Cellar/omathunder/<version>/bin/omathunder),
// which disappears on `brew upgrade`, to the version-independent …/opt/omathunder/bin/omathunder.
// Other paths are returned unchanged.
func StableBinaryPath(p string) string {
	sep := string(filepath.Separator)
	marker := sep + "Cellar" + sep + "omathunder" + sep
	i := strings.Index(p, marker)
	if i < 0 {
		return p
	}
	rest := p[i+len(marker):] // <version>/bin/omathunder
	j := strings.Index(rest, sep)
	if j < 0 {
		return p
	}
	return filepath.Join(p[:i], "opt", "omathunder", rest[j+1:])
}

// ExtensionPackagePath returns the .xpi shipped next to the binary (Homebrew: share/omathunder), if present.
func ExtensionPackagePath(binaryPath string) string {
	candidate := filepath.Join(filepath.Dir(filepath.Dir(binaryPath)), "share", "omathunder", "omathunder-extension.xpi")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// InstallHost registers the Native Messaging Host manifest non-destructively (never touches prefs.js).
func InstallHost(binaryPath string) ([]string, error) {
	absBin, err := filepath.Abs(binaryPath)
	if err != nil {
		return nil, err
	}
	// On macOS/Linux Thunderbird invokes the binary without CLI args, so we install a tiny wrapper
	// or rely on argv/mode detection; a dedicated wrapper ensures `omathunder host` runs cleanly.
	wrapperPath, err := writeHostWrapper(absBin)
	if err != nil {
		return nil, err
	}

	manifest := HostManifest{
		Name:              config.NativeHostName,
		Description:       "omathunder live Omarchy theme & multi-stage AI bridge for Thunderbird",
		Path:              wrapperPath,
		Type:              "stdio",
		AllowedExtensions: []string{config.ExtensionID},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	paths, err := ManifestPaths()
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return nil, err
		}
	}
	if runtime.GOOS == "windows" && len(paths) > 0 {
		regKey := `HKCU\Software\Mozilla\NativeMessagingHosts\` + config.NativeHostName
		_ = exec.Command("reg", "add", regKey, "/ve", "/t", "REG_SZ", "/d", paths[0], "/f").Run()
	}
	return paths, nil
}

// UninstallHost removes only omathunder's Native Messaging Host manifests and wrapper.
func UninstallHost() ([]string, error) {
	var removed []string
	paths, err := ManifestPaths()
	if err == nil {
		for _, p := range paths {
			if err := os.Remove(p); err == nil {
				removed = append(removed, p)
			}
		}
	}
	if wrapper, err := wrapperLocation(); err == nil {
		if err := os.Remove(wrapper); err == nil {
			removed = append(removed, wrapper)
		}
	}
	if runtime.GOOS == "windows" {
		regKey := `HKCU\Software\Mozilla\NativeMessagingHosts\` + config.NativeHostName
		_ = exec.Command("reg", "delete", regKey, "/f").Run()
	}
	return removed, nil
}

// EnvFilePath is the optional shell file the host wrapper loads API keys from (macOS/Linux).
func EnvFilePath() string {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cfgDir, "env")
}

func wrapperLocation() (string, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(cfgDir, "omathunder-host.cmd"), nil
	}
	return filepath.Join(cfgDir, "omathunder-host.sh"), nil
}

func writeHostWrapper(absBin string) (string, error) {
	wrapper, err := wrapperLocation()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		return "", err
	}
	var content string
	var perm os.FileMode = 0o755
	if runtime.GOOS == "windows" {
		content = fmt.Sprintf("@echo off\r\n\"%s\" host %%*\r\n", absBin)
	} else {
		cfgDir := filepath.Dir(wrapper)
		envFile := filepath.Join(cfgDir, "env")
		content = fmt.Sprintf("#!/bin/sh\nexport PATH=\"/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$PATH\"\n[ -f %q ] && . %q\nexec %q host \"$@\"\n", envFile, envFile, absBin)
	}
	if err := os.WriteFile(wrapper, []byte(content), perm); err != nil {
		return "", err
	}
	return wrapper, nil
}
