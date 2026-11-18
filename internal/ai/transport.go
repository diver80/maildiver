package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/omathunder/internal/config"
)

const (
	// Response size caps: a broken or hostile endpoint must not fill the host's memory.
	maxResponseBytes = 4 << 20  // non-streaming JSON responses
	maxStreamBytes   = 32 << 20 // SSE / NDJSON streams (per-token JSON overhead is large)
	// Default and upper bound for generated tokens when the caller sets none.
	defaultMaxTokens = 8192
	maxMaxTokens     = 32768
	// providerCacheSize bounds the provider cache (one entry per backend/endpoint/key).
	providerCacheSize = 32
)

// sharedTransport is reused by every provider so scans keep their connections alive.
// It has no overall timeout: non-streaming calls get a context deadline, streams an idle watchdog.
var sharedTransport http.RoundTripper = func() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = 15 * time.Second
	t.MaxIdleConnsPerHost = 8
	t.IdleConnTimeout = 90 * time.Second
	return t
}()

// guardTransport pins every request of one provider to its expected origin (official URL or the
// endpoint from config.json), so the API key cannot reach any other host – not via SDK
// environment overrides (OPENAI_BASE_URL, GOOGLE_GEMINI_BASE_URL, ...) and not via redirects.
// It also caps response sizes.
type guardTransport struct {
	base   http.RoundTripper
	target *url.URL
	// rewrite sends every request to target (SDKs that ignore base URLs, e.g. any-llm's Gemini
	// provider, or that read their own base-URL environment variables). Config wins.
	rewrite bool
	// stripOpenAIHeaders drops OpenAI account headers the SDK adds from the environment.
	stripOpenAIHeaders bool
}

func (g *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if g.rewrite && !sameOrigin(req.URL, g.target) {
		req = req.Clone(req.Context())
		prefix := strings.TrimRight(g.target.Path, "/")
		req.URL.Scheme = g.target.Scheme
		req.URL.Host = g.target.Host
		req.URL.Path = prefix + req.URL.Path
		if req.URL.RawPath != "" {
			req.URL.RawPath = prefix + req.URL.RawPath
		}
		req.Host = ""
	}
	if !sameOrigin(req.URL, g.target) {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf("%w: refusing AI request to %s://%s (expected %s://%s from config.json)",
			ErrKeyNotForEndpoint, req.URL.Scheme, req.URL.Host, g.target.Scheme, g.target.Host)
	}
	if g.stripOpenAIHeaders && (req.Header.Get("OpenAI-Organization") != "" || req.Header.Get("OpenAI-Project") != "") {
		req = req.Clone(req.Context())
		req.Header.Del("OpenAI-Organization")
		req.Header.Del("OpenAI-Project")
	}
	resp, err := g.base.RoundTrip(req)
	if err != nil || resp.Body == nil {
		return resp, err
	}
	limit := int64(maxResponseBytes)
	if isStreamContentType(resp.Header.Get("Content-Type")) {
		limit = maxStreamBytes
	}
	resp.Body = &cappedBody{rc: resp.Body, remaining: limit, limit: limit}
	return resp, nil
}

func isStreamContentType(ct string) bool {
	mt, _, _ := mime.ParseMediaType(ct)
	switch strings.ToLower(mt) {
	case "text/event-stream", "application/x-ndjson", "application/jsonl", "application/json-seq":
		return true
	}
	return false
}

// errResponseTooLarge is returned once a response exceeds its cap.
var errResponseTooLarge = errors.New("ai_response_too_large")

type cappedBody struct {
	rc        io.ReadCloser
	remaining int64
	limit     int64
}

func (c *cappedBody) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, fmt.Errorf("%w: more than %d MB", errResponseTooLarge, c.limit>>20)
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.rc.Read(p)
	c.remaining -= int64(n)
	return n, err
}

func (c *cappedBody) Close() error { return c.rc.Close() }

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(canonicalHost(a), canonicalHost(b))
}

// canonicalHost returns host:port with the scheme's default port made explicit.
func canonicalHost(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid AI endpoint %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid AI endpoint %q: need http(s)://host[:port][/path]", raw)
	}
	return u, nil
}

// resolveAPIKey applies the key-binding rule: a backend's env key is only used for its official
// host or for an endpoint taken from config.json (trusted). Other endpoints get the placeholder
// key or an error, never the secret.
func resolveAPIKey(spec *backendSpec, endpoint string, trusted bool) (string, error) {
	if len(spec.EnvKeys) == 0 {
		return spec.PlaceholderKey, nil
	}
	allowed := endpoint == "" || trusted
	if !allowed && spec.OfficialURL != "" {
		if eu, err := url.Parse(endpoint); err == nil {
			if ou, err := url.Parse(spec.OfficialURL); err == nil && sameOrigin(eu, ou) {
				allowed = true
			}
		}
	}
	if allowed {
		if k := spec.envKey(); k != "" {
			return k, nil
		}
		return spec.PlaceholderKey, nil
	}
	if spec.PlaceholderKey != "" {
		return spec.PlaceholderKey, nil
	}
	return "", fmt.Errorf("%w: %s is only sent to %s or an endpoint configured in config.json, not to %s",
		ErrKeyNotForEndpoint, strings.Join(spec.EnvKeys, "/"), spec.OfficialURL, endpoint)
}

type providerCacheKey struct {
	id, base, keyHash string
}

var (
	providerCacheMu sync.Mutex
	providerCache   = map[providerCacheKey]anyllm.Provider{}
)

// requireModel reports a clear error when neither config nor the registry names a model.
func requireModel(spec *backendSpec, model string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("%s: kein Modell konfiguriert / no model configured", spec.ID)
	}
	return nil
}

// buildProvider creates (or reuses) the any-llm provider for a config-derived ProviderConfig.
// The returned model is the configured one or the backend default (may be empty: requireModel).
// trusted=false means pc.Endpoint did not come from config.json: the API key is then withheld
// unless the endpoint is the backend's official host.
func buildProvider(pc config.ProviderConfig, trusted bool, extraOpts []anyllm.Option) (anyllm.Provider, *backendSpec, string, error) {
	spec, err := lookupBackend(pc.Backend)
	if err != nil {
		return nil, nil, "", err
	}
	if spec.Kind == kindCLI || spec.newProvider == nil {
		return nil, spec, "", fmt.Errorf("%s is a CLI backend without an HTTP API", spec.ID)
	}
	endpoint := strings.TrimSpace(pc.Endpoint)
	base := endpoint
	if base == "" {
		base = spec.OfficialURL
	}
	if base == "" {
		return nil, spec, "", fmt.Errorf("%w: %s braucht einen Endpoint in config.json / needs an endpoint in config.json (ai.agent.endpoint or ai.endpoints.%s)",
			ErrEndpointRequired, spec.ID, spec.ID)
	}
	target, err := parseBaseURL(base)
	if err != nil {
		return nil, spec, "", err
	}
	model := strings.TrimSpace(pc.Model)
	if model == "" {
		model = spec.DefaultModel
	}
	key, err := resolveAPIKey(spec, endpoint, trusted)
	if err != nil {
		return nil, spec, "", err
	}

	cacheable := len(extraOpts) == 0
	sum := sha256.Sum256([]byte(key))
	ck := providerCacheKey{id: spec.ID, base: target.String(), keyHash: hex.EncodeToString(sum[:])}
	if cacheable {
		providerCacheMu.Lock()
		p, ok := providerCache[ck]
		providerCacheMu.Unlock()
		if ok {
			return p, spec, model, nil
		}
	}

	guard := &guardTransport{base: sharedTransport, target: target, rewrite: spec.RewriteHost, stripOpenAIHeaders: spec.ID != "openai"}
	// Caller options first: base URL, key and HTTP client below always win.
	opts := make([]anyllm.Option, 0, len(extraOpts)+3)
	opts = append(opts, extraOpts...)
	opts = append(opts, anyllm.WithHTTPClient(&http.Client{Transport: guard}), anyllm.WithBaseURL(base))
	if key != "" {
		opts = append(opts, anyllm.WithAPIKey(key))
	}
	p, err := spec.newProvider(opts...)
	if err != nil {
		return nil, spec, "", err
	}
	if cacheable {
		providerCacheMu.Lock()
		if len(providerCache) >= providerCacheSize {
			clear(providerCache)
		}
		providerCache[ck] = p
		providerCacheMu.Unlock()
	}
	return p, spec, model, nil
}

func clampMaxTokens(v *int) *int {
	n := defaultMaxTokens
	if v != nil && *v > 0 {
		n = min(*v, maxMaxTokens)
	}
	return &n
}
