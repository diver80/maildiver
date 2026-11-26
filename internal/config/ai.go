package config

import "strings"

// backendNames lists the canonical AI backend identifiers in display order. The ai package
// implements exactly these (a test there keeps both lists in sync).
var backendNames = []string{
	"claude-cli", "agy", "ollama", "openaicompat", "claude-api", "gemini-api", "openai",
	// Legacy: still run from an existing config.json, but no longer offered in the settings.
	"deepseek", "groq", "mistral", "zai", "llamacpp", "llamafile", "opencode-cli",
}

// legacyBackends are kept for configs that already use them; the settings only offer them while
// selected.
var legacyBackends = map[string]bool{
	"deepseek": true, "groq": true, "mistral": true, "zai": true,
	"llamacpp": true, "llamafile": true, "opencode-cli": true,
}

// IsLegacyBackend reports whether backend still runs but is no longer offered for new setups.
func IsLegacyBackend(backend string) bool { return legacyBackends[CanonicalBackend(backend)] }

// backendAliasList lists the extra accepted spellings per canonical backend (compared lower case,
// with "_" and " " folded to "-"). "claude" alone is deliberately not an alias: it could mean the
// CLI or the API.
var backendAliasList = map[string][]string{
	"gemini-api":   {"gemini", "google", "google-gemini", "google-ai", "googleai"},
	"claude-api":   {"anthropic", "anthropic-api"},
	"openai":       {"openai-api"},
	"openaicompat": {"openai-compat", "openai-compatible", "openaicompatible", "mtplx", "lmstudio", "lm-studio"},
	"deepseek":     {"deepseek-api"},
	"groq":         {"groq-api"},
	"mistral":      {"mistral-api", "mistralai"},
	"zai":          {"z.ai", "z-ai", "zhipu"},
	"llamacpp":     {"llama.cpp", "llama-cpp"},
	"claude-cli":   {"claude-code", "claudecode"},
	// Google replaced the Gemini CLI with the Antigravity CLI (agy).
	"agy":          {"agy-cli", "antigravity", "antigravity-cli", "gemini-cli"},
	"opencode-cli": {"opencode"},
}

// backendAliases maps every accepted spelling to its canonical backend.
var backendAliases = func() map[string]string {
	m := make(map[string]string, 3*len(backendNames))
	for _, name := range backendNames {
		m[name] = name
		for _, alias := range backendAliasList[name] {
			m[alias] = name
		}
	}
	return m
}()

func backendKey(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	return strings.NewReplacer("_", "-", " ", "-").Replace(s)
}

// CanonicalBackend resolves a backend name case-insensitively, including aliases such as
// "anthropic" -> "claude-api" or "Gemini" -> "gemini-api". Unknown names come back lower-cased
// and trimmed so callers can report them; use IsKnownBackend to tell the two apart.
func CanonicalBackend(name string) string {
	k := backendKey(name)
	if c, ok := backendAliases[k]; ok {
		return c
	}
	return k
}

// IsKnownBackend reports whether name (or one of its aliases) is a supported AI backend.
func IsKnownBackend(name string) bool {
	_, ok := backendAliases[backendKey(name)]
	return ok
}

// BackendNames returns the canonical backend identifiers in display order.
func BackendNames() []string {
	return append([]string(nil), backendNames...)
}

func normalizeProvider(p ProviderConfig) ProviderConfig {
	p.Model = strings.TrimSpace(p.Model)
	if strings.TrimSpace(p.Backend) != "" {
		if backendKey(p.Backend) == "gemini-cli" {
			p.Model = "" // Gemini CLI model names mean nothing to agy: use its default
		}
		p.Backend = CanonicalBackend(p.Backend)
	} else {
		p.Backend = ""
	}
	p.Endpoint = strings.TrimSpace(p.Endpoint)
	return p
}

// normalizeAI canonicalizes backend names and per-backend endpoints.
func normalizeAI(a AIConfig) AIConfig {
	a.Classifier = normalizeProvider(a.Classifier)
	a.Agent = normalizeProvider(a.Agent)
	if len(a.Endpoints) > 0 {
		eps := make(map[string]string, len(a.Endpoints))
		for k, v := range a.Endpoints {
			k, v = CanonicalBackend(k), strings.TrimSpace(v)
			if k == "" || v == "" {
				continue
			}
			eps[k] = v
		}
		a.Endpoints = eps
	}
	if len(a.Endpoints) == 0 {
		a.Endpoints = nil
	}
	return a
}
