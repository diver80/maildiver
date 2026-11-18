package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// API keys live in a small shell file next to config.json ("env", mode 0600): the host's launcher
// sources it, the app edits it (☰ → Settings), and LookupAPIKey reads it fresh, so a key saved in
// the app works in the running host without restarting Thunderbird.

// EnvFilePath is the env file holding API keys ("" when the config dir is unknown).
func EnvFilePath() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "env")
}

// parseEnvFile reads `KEY=value` / `export KEY=value` lines; quotes and trailing comments go.
func parseEnvFile(data string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(data, "\n") {
		key, val, ok := parseEnvLine(raw)
		if ok && val != "" {
			out[key] = val
		}
	}
	return out
}

func parseEnvLine(raw string) (key, val string, ok bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	if strings.HasPrefix(line, "export") && len(line) > len("export") && (line[6] == ' ' || line[6] == '\t') {
		line = strings.TrimSpace(line[6:])
	}
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:eq])
	val = strings.TrimSpace(line[eq+1:])
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
		val = val[1 : len(val)-1]
	} else if idx := strings.Index(val, " #"); idx >= 0 {
		val = strings.TrimSpace(val[:idx])
	}
	return key, val, true
}

var envCache struct {
	sync.Mutex
	path    string
	mod     time.Time
	size    int64
	values  map[string]string
	fromEnv map[string]bool // keys LoadEnvFile copied into the process environment
}

// readEnvFile returns the file's keys, re-reading it only when it changed.
func readEnvFile() map[string]string {
	path := EnvFilePath()
	envCache.Lock()
	defer envCache.Unlock()
	st, err := os.Stat(path)
	if err != nil || path == "" {
		envCache.path, envCache.values = path, map[string]string{}
		return envCache.values
	}
	if envCache.values != nil && envCache.path == path && st.ModTime().Equal(envCache.mod) && st.Size() == envCache.size {
		return envCache.values
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	envCache.path, envCache.mod, envCache.size = path, st.ModTime(), st.Size()
	envCache.values = parseEnvFile(string(data))
	return envCache.values
}

// LoadEnvFile copies the env file into the process environment without overwriting variables
// already set, so CLIs started from here (and old code reading os.Getenv) see the keys too.
func LoadEnvFile() {
	vals := readEnvFile()
	envCache.Lock()
	defer envCache.Unlock()
	if envCache.fromEnv == nil {
		envCache.fromEnv = map[string]bool{}
	}
	for k, v := range vals {
		switch os.Getenv(k) {
		case "":
			_ = os.Setenv(k, v)
			envCache.fromEnv[k] = true
		case v:
			envCache.fromEnv[k] = true // the host wrapper sourced the same file
		}
	}
}

// LookupAPIKey returns the first non-empty key among names: the env file first (read fresh), then
// the process environment – except values that only came from an earlier state of the file.
func LookupAPIKey(names ...string) string {
	vals := readEnvFile()
	for _, n := range names {
		if v := strings.TrimSpace(vals[n]); v != "" {
			return v
		}
	}
	envCache.Lock()
	fromFile := envCache.fromEnv
	envCache.Unlock()
	for _, n := range names {
		if fromFile[n] {
			continue // removed from the file since: gone
		}
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// HasAPIKeyInFile reports whether the env file sets name.
func HasAPIKeyInFile(name string) bool {
	return strings.TrimSpace(readEnvFile()[name]) != ""
}

// SetEnvFileKeys sets or (with "") removes keys in the env file, keeping its other lines and
// comments. The file is written atomically with mode 0600.
func SetEnvFileKeys(updates map[string]string) error {
	path := EnvFilePath()
	if path == "" {
		return os.ErrNotExist
	}
	for k, v := range updates {
		// Keys are shell variable names; values are single-quoted, so they cannot hold a quote.
		if k == "" || strings.ContainsAny(k, " \t=\n'\"$`\\") || strings.ContainsAny(v, "'\n\r") {
			return os.ErrInvalid
		}
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	pending := map[string]string{}
	for k, v := range updates {
		pending[k] = v
	}
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	out := make([]string, 0, len(lines)+len(updates))
	for _, line := range lines {
		key, _, ok := parseEnvLine(line)
		if !ok {
			out = append(out, line)
			continue
		}
		v, change := pending[key]
		if !change {
			out = append(out, line)
			continue
		}
		delete(pending, key)
		if v != "" {
			out = append(out, "export "+key+"="+shellQuote(v))
		}
	}
	keys := make([]string, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := pending[k]; v != "" {
			out = append(out, "export "+k+"="+shellQuote(v))
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp) // a stale tmp file must not keep a wider mode
	body := strings.Join(out, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// shellQuote quotes a value (without single quotes) for `export KEY=...` in a POSIX shell.
func shellQuote(s string) string {
	return "'" + s + "'"
}
