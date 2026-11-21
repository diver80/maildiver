package ai

import (
	"errors"
	"fmt"
	"strings"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"
	"github.com/mozilla-ai/any-llm-go/providers/anthropic"
	"github.com/mozilla-ai/any-llm-go/providers/deepseek"
	"github.com/mozilla-ai/any-llm-go/providers/gemini"
	"github.com/mozilla-ai/any-llm-go/providers/groq"
	"github.com/mozilla-ai/any-llm-go/providers/llamacpp"
	"github.com/mozilla-ai/any-llm-go/providers/llamafile"
	"github.com/mozilla-ai/any-llm-go/providers/mistral"
	"github.com/mozilla-ai/any-llm-go/providers/ollama"
	"github.com/mozilla-ai/any-llm-go/providers/openai"
	"github.com/mozilla-ai/any-llm-go/providers/zai"

	"github.com/diver80/maildiver/internal/config"
)

const (
	kindAPI   = "api"
	kindLocal = "local"
	kindCLI   = "cli"

	// Deadlines for one non-streaming call (classify, analyze, one chat round), retries included.
	// The same backend gets the same deadline on every path.
	apiTimeout   = 150 * time.Second
	localTimeout = 300 * time.Second
	cliTimeout   = 300 * time.Second
)

var (
	// ErrUnknownBackend is returned (wrapped) for backend names that are not in the registry.
	// There is deliberately no fallback to another provider.
	ErrUnknownBackend = errors.New("unknown_ai_backend")
	// ErrNoBackend is returned when no backend is configured.
	ErrNoBackend = errors.New("no_ai_backend")
	// ErrEndpointRequired is returned for backends without an official URL (openaicompat) when
	// config.json sets no endpoint for them.
	ErrEndpointRequired = errors.New("ai_endpoint_required")
	// ErrKeyNotForEndpoint is returned when a backend's API key would go to an endpoint that is
	// neither the backend's official host nor configured in config.json.
	ErrKeyNotForEndpoint = errors.New("ai_key_not_for_endpoint")
)

// backendSpec is the single source of truth for one backend: every path (classify, analyze,
// chat, streaming, model listing, provider list) resolves backends through it.
type backendSpec struct {
	ID   string
	Kind string // kindAPI, kindLocal, kindCLI
	// DefaultModel is used when config sets no model; ClassifierModel optionally overrides it for
	// the classifier role (cheaper model for many small calls).
	DefaultModel    string
	ClassifierModel string
	// EnvKeys are the environment variables holding this backend's API key (first non-empty wins).
	// The key is only sent to OfficialURL or to an endpoint configured for this backend in config.json.
	EnvKeys []string
	// PlaceholderKey is sent when no key applies (servers that ignore auth).
	PlaceholderKey string
	// OfficialURL is the default base URL; empty = an endpoint must be configured.
	OfficialURL string
	Timeout     time.Duration
	Streaming   bool
	Tools       bool
	// RewriteHost: the SDK ignores WithBaseURL (any-llm's Gemini provider), so the transport
	// sends its requests to the configured endpoint (or official URL) instead.
	RewriteHost bool
	// newProvider builds the any-llm provider (nil for CLI backends).
	newProvider func(opts ...anyllm.Option) (anyllm.Provider, error)
}

func wrapProvider[T anyllm.Provider](f func(...anyllm.Option) (T, error)) func(...anyllm.Option) (anyllm.Provider, error) {
	return func(opts ...anyllm.Option) (anyllm.Provider, error) {
		p, err := f(opts...)
		if err != nil {
			return nil, err
		}
		return p, nil
	}
}

// backendRegistry is in display order (SupportedProviders) and must cover config.BackendNames().
var backendRegistry = []backendSpec{
	{ID: "gemini-api", Kind: kindAPI, DefaultModel: "gemini-2.5-flash", EnvKeys: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		OfficialURL: "https://generativelanguage.googleapis.com", Timeout: apiTimeout, Streaming: true, Tools: true,
		RewriteHost: true, newProvider: wrapProvider(gemini.New)},
	{ID: "claude-api", Kind: kindAPI, DefaultModel: "claude-sonnet-4-5", ClassifierModel: "claude-haiku-4-5",
		EnvKeys: []string{"ANTHROPIC_API_KEY"}, OfficialURL: "https://api.anthropic.com", Timeout: apiTimeout,
		Streaming: true, Tools: true, newProvider: wrapProvider(anthropic.New)},
	{ID: "openai", Kind: kindAPI, DefaultModel: "gpt-4o-mini", EnvKeys: []string{"OPENAI_API_KEY"},
		OfficialURL: "https://api.openai.com/v1", Timeout: apiTimeout, Streaming: true, Tools: true,
		newProvider: wrapProvider(openai.New)},
	// OpenAI-compatible servers get their own key variable: OPENAI_API_KEY never leaves for a non-OpenAI host.
	{ID: "openaicompat", Kind: kindAPI, EnvKeys: []string{"OPENAICOMPAT_API_KEY"}, PlaceholderKey: "omathunder-local",
		Timeout: apiTimeout, Streaming: true, Tools: true, newProvider: wrapProvider(openai.New)},
	{ID: "deepseek", Kind: kindAPI, DefaultModel: "deepseek-chat", EnvKeys: []string{"DEEPSEEK_API_KEY"},
		OfficialURL: "https://api.deepseek.com", Timeout: apiTimeout, Streaming: true, Tools: true,
		newProvider: wrapProvider(deepseek.New)},
	{ID: "groq", Kind: kindAPI, DefaultModel: "llama-3.3-70b-versatile", EnvKeys: []string{"GROQ_API_KEY"},
		OfficialURL: "https://api.groq.com/openai/v1", Timeout: apiTimeout, Streaming: true, Tools: true,
		newProvider: wrapProvider(groq.New)},
	{ID: "mistral", Kind: kindAPI, DefaultModel: "mistral-small-latest", EnvKeys: []string{"MISTRAL_API_KEY"},
		OfficialURL: "https://api.mistral.ai/v1", Timeout: apiTimeout, Streaming: true, Tools: true,
		newProvider: wrapProvider(mistral.New)},
	{ID: "zai", Kind: kindAPI, DefaultModel: "glm-4.5", EnvKeys: []string{"ZAI_API_KEY"},
		OfficialURL: "https://api.z.ai/api/paas/v4", Timeout: apiTimeout, Streaming: true, Tools: true,
		newProvider: wrapProvider(zai.New)},
	{ID: "ollama", Kind: kindLocal, DefaultModel: "qwen3:4b", OfficialURL: "http://127.0.0.1:11434",
		Timeout: localTimeout, Streaming: true, Tools: true, newProvider: wrapProvider(ollama.New)},
	{ID: "llamacpp", Kind: kindLocal, DefaultModel: "default", OfficialURL: "http://127.0.0.1:8080/v1",
		Timeout: localTimeout, Streaming: true, Tools: true, newProvider: wrapProvider(llamacpp.New)},
	{ID: "llamafile", Kind: kindLocal, DefaultModel: "default", OfficialURL: "http://127.0.0.1:8080/v1",
		Timeout: localTimeout, Streaming: true, Tools: true, newProvider: wrapProvider(llamafile.New)},
	{ID: "claude-cli", Kind: kindCLI, Timeout: cliTimeout},
	{ID: "gemini-cli", Kind: kindCLI, Timeout: cliTimeout},
	{ID: "opencode-cli", Kind: kindCLI, Timeout: cliTimeout},
}

var backendByID = func() map[string]*backendSpec {
	m := make(map[string]*backendSpec, len(backendRegistry))
	for i := range backendRegistry {
		m[backendRegistry[i].ID] = &backendRegistry[i]
	}
	return m
}()

// CanonicalBackend resolves a backend name case-insensitively including aliases
// ("Anthropic" -> "claude-api", "gemini" -> "gemini-api"). Unknown names come back lower-cased.
func CanonicalBackend(name string) string {
	return config.CanonicalBackend(name)
}

// KnownBackend reports whether name (or an alias) is a supported backend.
func KnownBackend(name string) bool {
	_, ok := backendByID[CanonicalBackend(name)]
	return ok
}

// IsCLIBackend reports whether the backend runs a local CLI (claude-cli, gemini-cli, opencode-cli).
func IsCLIBackend(name string) bool {
	spec, ok := backendByID[CanonicalBackend(name)]
	return ok && spec.Kind == kindCLI
}

// CLIModelHint provides curated model suggestions and naming guidance for local CLI backends
// that do not expose a programmatic model-listing API.
type CLIModelHint struct {
	Models        []string // clickable aliases / model IDs (empty string = CLI default)
	PlaceholderDE string
	PlaceholderEN string
	HintDE        string
	HintEN        string
}

// CLIModelHints returns model suggestions and naming hints for a CLI backend.
func CLIModelHints(backend string) (CLIModelHint, bool) {
	switch CanonicalBackend(backend) {
	case "claude-cli":
		return CLIModelHint{
			Models:        []string{"sonnet", "opus", "haiku", "claude-opus-4-6", "claude-sonnet-4-6"},
			PlaceholderDE: "sonnet / opus / haiku (Standard)",
			PlaceholderEN: "sonnet / opus / haiku (default)",
			HintDE:        "Claude-CLI: Aliasse (sonnet, opus, haiku) nutzen immer die neueste Version, oder volle Modell-ID (z. B. claude-opus-4-6). Leer = CLI-Standard.",
			HintEN:        "Claude CLI: aliases (sonnet, opus, haiku) always track the latest release, or enter a full model ID (e.g. claude-opus-4-6). Empty = CLI default.",
		}, true
	case "gemini-cli":
		return CLIModelHint{
			Models:        []string{"auto", "gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite"},
			PlaceholderDE: "gemini-2.5-pro / flash (Standard)",
			PlaceholderEN: "gemini-2.5-pro / flash (default)",
			HintDE:        "Gemini-CLI: z. B. auto, gemini-2.5-pro, gemini-2.5-flash oder Preview-ID eingeben. Leer = CLI-Standard.",
			HintEN:        "Gemini CLI: e.g. auto, gemini-2.5-pro, gemini-2.5-flash, or any preview model ID. Empty = CLI default.",
		}, true
	case "opencode-cli":
		return CLIModelHint{
			Models:        []string{"anthropic/claude-sonnet-4-6", "anthropic/claude-opus-4-6", "google/gemini-2.5-pro"},
			PlaceholderDE: "provider/model (Standard)",
			PlaceholderEN: "provider/model (default)",
			HintDE:        "OpenCode-CLI: Format provider/model (alle lokalen Modelle per „opencode models“). Leer = CLI-Standard.",
			HintEN:        "OpenCode CLI: provider/model format (run “opencode models” to list all). Empty = CLI default.",
		}, true
	default:
		return CLIModelHint{}, false
	}
}

// lookupBackend returns the spec for a backend name or a clear error; never a substitute backend.
func lookupBackend(name string) (*backendSpec, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: kein KI-Backend konfiguriert / no AI backend configured", ErrNoBackend)
	}
	if spec, ok := backendByID[CanonicalBackend(name)]; ok {
		return spec, nil
	}
	ids := make([]string, 0, len(backendRegistry))
	for _, s := range backendRegistry {
		ids = append(ids, s.ID)
	}
	return nil, fmt.Errorf("%w: %q – unbekanntes KI-Backend / unknown AI backend (supported: %s)",
		ErrUnknownBackend, strings.TrimSpace(name), strings.Join(ids, ", "))
}

func defaultModelForBackend(backend string) string {
	if spec, ok := backendByID[CanonicalBackend(backend)]; ok {
		return spec.DefaultModel
	}
	return ""
}

// classifierModel returns the model for the classifier role: the configured one, else the
// backend's cheaper classifier default, else its general default.
func classifierModel(p config.ProviderConfig) string {
	if m := strings.TrimSpace(p.Model); m != "" {
		return m
	}
	if spec, ok := backendByID[CanonicalBackend(p.Backend)]; ok && spec.ClassifierModel != "" {
		return spec.ClassifierModel
	}
	return defaultModelForBackend(p.Backend)
}

// envKey reads the backend's API key fresh from the env file or the environment, so a key
// saved in the app works without a restart.
func (s *backendSpec) envKey() string {
	return config.LookupAPIKey(s.EnvKeys...)
}

// APIKeyName is the variable an API backend reads its key from (the app's settings list them).
type APIKeyName struct {
	Backend string `json:"backend"`
	EnvKey  string `json:"envKey"`
}

// APIKeyNames lists the key variables of the API backends that need one.
func APIKeyNames() []APIKeyName {
	var out []APIKeyName
	for _, s := range backendRegistry {
		if s.Kind == kindAPI && len(s.EnvKeys) > 0 && s.PlaceholderKey == "" {
			out = append(out, APIKeyName{Backend: s.ID, EnvKey: s.EnvKeys[0]})
		}
	}
	return out
}

// ProviderDescriptor describes one backend for provider pickers.
type ProviderDescriptor struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"` // "api", "local", "cli"
	DefaultModel string `json:"defaultModel,omitempty"`
	Configured   bool   `json:"configured"`
	Streaming    bool   `json:"streaming"`
	Tools        bool   `json:"tools"`
}

// SupportedProviders returns all AI backends (canonical IDs, display order) from the registry.
func SupportedProviders() []ProviderDescriptor {
	out := make([]ProviderDescriptor, 0, len(backendRegistry))
	for i := range backendRegistry {
		s := &backendRegistry[i]
		configured := true
		if len(s.EnvKeys) > 0 && s.PlaceholderKey == "" {
			configured = s.envKey() != ""
		}
		out = append(out, ProviderDescriptor{
			ID: s.ID, Kind: s.Kind, DefaultModel: s.DefaultModel,
			Configured: configured, Streaming: s.Streaming, Tools: s.Tools,
		})
	}
	return out
}

// ProviderForBackend returns the provider settings for backend with endpoint and model taken only
// from config: the agent's settings when it uses the same canonical backend, else the
// classifier's, else ai.endpoints[backend] (or the official URL). An empty backend means the agent.
// The Backend field of the result is canonical. Callers may override Model; never Endpoint.
func ProviderForBackend(aiCfg config.AIConfig, backend string) config.ProviderConfig {
	want := CanonicalBackend(backend)
	agent := canonicalProvider(aiCfg.Agent)
	var p config.ProviderConfig
	switch {
	case want == "" || want == agent.Backend:
		p = agent
	case want == canonicalProvider(aiCfg.Classifier).Backend:
		p = canonicalProvider(aiCfg.Classifier)
	default:
		p = config.ProviderConfig{Backend: want}
	}
	return withConfiguredEndpoint(aiCfg, p)
}

func canonicalProvider(p config.ProviderConfig) config.ProviderConfig {
	if strings.TrimSpace(p.Backend) != "" {
		p.Backend = CanonicalBackend(p.Backend)
	}
	p.Model = strings.TrimSpace(p.Model)
	p.Endpoint = strings.TrimSpace(p.Endpoint)
	return p
}

// withConfiguredEndpoint fills an empty endpoint from ai.endpoints (config.json only).
func withConfiguredEndpoint(aiCfg config.AIConfig, p config.ProviderConfig) config.ProviderConfig {
	p = canonicalProvider(p)
	if p.Endpoint != "" || p.Backend == "" {
		return p
	}
	for k, v := range aiCfg.Endpoints {
		if CanonicalBackend(k) == p.Backend && strings.TrimSpace(v) != "" {
			p.Endpoint = strings.TrimSpace(v)
			break
		}
	}
	return p
}
