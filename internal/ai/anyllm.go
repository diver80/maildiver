package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/maildiver/internal/config"
)

const (
	// streamIdleTimeout ends a stream when no chunk arrives for this long (time to first token
	// of thinking models included). A healthy long answer is never cut off.
	streamIdleTimeout = 120 * time.Second
	// streamMaxDuration is a last-resort bound for one stream.
	streamMaxDuration = 15 * time.Minute
	listModelsTimeout = 30 * time.Second
)

// NewAnyLLMProvider creates the any-llm-go provider for a ProviderConfig taken from config.json
// (use ProviderForBackend). Base URL, API key and HTTP client are pinned by the backend registry;
// extraOpts cannot override them. The returned model falls back to the backend default.
func NewAnyLLMProvider(cfg config.ProviderConfig, extraOpts ...anyllm.Option) (anyllm.Provider, string, error) {
	p, _, model, err := buildProvider(cfg, true, extraOpts)
	return p, model, err
}

// AnyLLMCompleter adapts any-llm-go's Provider to OmaThunder's TextCompleter interface.
type AnyLLMCompleter struct {
	cfg config.ProviderConfig
}

func NewAnyLLMCompleter(cfg config.ProviderConfig) *AnyLLMCompleter {
	cfg = canonicalProvider(cfg)
	return &AnyLLMCompleter{cfg: cfg}
}

func (a *AnyLLMCompleter) Name() string { return a.cfg.Backend }
func (a *AnyLLMCompleter) ModelName() string {
	if m := strings.TrimSpace(a.cfg.Model); m != "" {
		return m
	}
	return defaultModelForBackend(a.cfg.Backend)
}

func (a *AnyLLMCompleter) Complete(ctx context.Context, systemPrompt, userPrompt string, expectJSON bool) (string, error) {
	p, spec, model, err := buildProvider(a.cfg, true, nil)
	if err != nil {
		return "", err
	}
	if err := requireModel(spec, model); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	temp := 0.2
	params := anyllm.CompletionParams{
		Model:       model,
		Temperature: &temp,
		MaxTokens:   clampMaxTokens(nil),
		Messages: []anyllm.Message{
			{Role: anyllm.RoleSystem, Content: systemPrompt},
			{Role: anyllm.RoleUser, Content: userPrompt},
		},
	}
	if expectJSON {
		params.ResponseFormat = &anyllm.ResponseFormat{Type: "json_object"}
	}
	resp, err := p.Completion(ctx, params)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("%s returned empty choices", spec.ID)
	}
	return resp.Choices[0].Message.ContentString(), nil
}

// ChatRequest is a multi-turn chat request (bridge /v1/ai/chat, copilot).
type ChatRequest struct {
	// Backend selects a backend by name (aliases allowed); empty = the configured agent.
	Backend string `json:"backend,omitempty"`
	// Model overrides the configured/default model.
	Model string `json:"model,omitempty"`
	// Deprecated: ignored. Endpoints come only from config.json (see ProviderForBackend);
	// the field stays for JSON compatibility.
	Endpoint        string                 `json:"endpoint,omitempty"`
	Messages        []anyllm.Message       `json:"messages"`
	Tools           []anyllm.Tool          `json:"tools,omitempty"`
	ToolChoice      any                    `json:"toolChoice,omitempty"`
	ResponseFormat  *anyllm.ResponseFormat `json:"responseFormat,omitempty"`
	ReasoningEffort anyllm.ReasoningEffort `json:"reasoningEffort,omitempty"`
	Temperature     *float64               `json:"temperature,omitempty"`
	MaxTokens       *int                   `json:"maxTokens,omitempty"`
	Stream          bool                   `json:"stream,omitempty"`
}

// resolveChatProvider picks backend and model from the request, endpoint only from config.
func resolveChatProvider(aiCfg config.AIConfig, req ChatRequest) (config.ProviderConfig, *backendSpec, error) {
	p := ProviderForBackend(aiCfg, req.Backend)
	if m := strings.TrimSpace(req.Model); m != "" {
		p.Model = m
	}
	spec, err := lookupBackend(p.Backend)
	if err != nil {
		return p, nil, err
	}
	return p, spec, nil
}

func chatParams(model string, req ChatRequest, stream bool) anyllm.CompletionParams {
	return anyllm.CompletionParams{
		Model:           model,
		Messages:        req.Messages,
		Tools:           req.Tools,
		ToolChoice:      req.ToolChoice,
		ResponseFormat:  req.ResponseFormat,
		ReasoningEffort: req.ReasoningEffort,
		Temperature:     req.Temperature,
		MaxTokens:       clampMaxTokens(req.MaxTokens),
		Stream:          stream,
	}
}

// Chat executes a multi-turn chat completion (including tool definitions) via any-llm-go, or via
// the CLI completer for CLI backends. req.Endpoint is ignored.
func Chat(ctx context.Context, aiCfg config.AIConfig, req ChatRequest, extraOpts ...anyllm.Option) (*anyllm.ChatCompletion, error) {
	pCfg, spec, err := resolveChatProvider(aiCfg, req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	if spec.Kind == kindCLI {
		return chatViaCLI(ctx, aiCfg, pCfg, req)
	}
	p, _, model, err := buildProvider(pCfg, true, extraOpts)
	if err != nil {
		return nil, err
	}
	if err := requireModel(spec, model); err != nil {
		return nil, err
	}
	return p.Completion(ctx, chatParams(model, req, false))
}

func errorStream(err error) (<-chan anyllm.ChatCompletionChunk, <-chan error) {
	chunkCh := make(chan anyllm.ChatCompletionChunk)
	errCh := make(chan error, 1)
	close(chunkCh)
	errCh <- err
	close(errCh)
	return chunkCh, errCh
}

// ChatStream streams chat completion chunks via any-llm-go (or emits a single chunk for CLI
// backends). A stream ends with an error after streamIdleTimeout without a chunk.
// req.Endpoint is ignored.
func ChatStream(ctx context.Context, aiCfg config.AIConfig, req ChatRequest, extraOpts ...anyllm.Option) (<-chan anyllm.ChatCompletionChunk, <-chan error) {
	pCfg, spec, err := resolveChatProvider(aiCfg, req)
	if err != nil {
		return errorStream(err)
	}
	if spec.Kind == kindCLI {
		chunkCh := make(chan anyllm.ChatCompletionChunk, 1)
		errCh := make(chan error, 1)
		go func() {
			defer close(chunkCh)
			defer close(errCh)
			cctx, cancel := context.WithTimeout(ctx, spec.Timeout)
			defer cancel()
			resp, err := chatViaCLI(cctx, aiCfg, pCfg, req)
			if err != nil {
				errCh <- err
				return
			}
			var content string
			if len(resp.Choices) > 0 {
				content = resp.Choices[0].Message.ContentString()
			}
			chunkCh <- anyllm.ChatCompletionChunk{
				ID:    resp.ID,
				Model: resp.Model,
				Choices: []anyllm.ChunkChoice{{
					Index:        0,
					Delta:        anyllm.ChunkDelta{Role: anyllm.RoleAssistant, Content: content},
					FinishReason: anyllm.FinishReasonStop,
				}},
			}
		}()
		return chunkCh, errCh
	}

	p, _, model, err := buildProvider(pCfg, true, extraOpts)
	if err == nil {
		err = requireModel(spec, model)
	}
	if err != nil {
		return errorStream(err)
	}
	sctx, cancel := context.WithTimeout(ctx, streamMaxDuration)
	srcChunks, srcErrs := p.CompletionStream(sctx, chatParams(model, req, true))
	return watchStream(sctx, cancel, srcChunks, srcErrs, streamIdleTimeout)
}

// watchStream forwards a provider stream and ends it with an error when no chunk arrives within
// idle. cancel is called when the stream ends; the provider channels are drained afterwards.
func watchStream(ctx context.Context, cancel context.CancelFunc, src <-chan anyllm.ChatCompletionChunk, srcErr <-chan error, idle time.Duration) (<-chan anyllm.ChatCompletionChunk, <-chan error) {
	out := make(chan anyllm.ChatCompletionChunk)
	outErr := make(chan error, 1)
	go func() {
		defer func() {
			cancel()
			close(out)
			close(outErr)
			go drainStream(src, srcErr)
		}()
		timer := time.NewTimer(idle)
		defer timer.Stop()
		for src != nil || srcErr != nil {
			select {
			case c, ok := <-src:
				if !ok {
					src = nil
					continue
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idle)
				select {
				case out <- c:
				case <-ctx.Done():
					outErr <- ctx.Err()
					return
				}
			case err, ok := <-srcErr:
				if !ok {
					srcErr = nil
					continue
				}
				if err != nil {
					outErr <- err
					return
				}
			case <-timer.C:
				outErr <- fmt.Errorf("AI stream idle for %s – abgebrochen / aborted", idle)
				return
			case <-ctx.Done():
				outErr <- ctx.Err()
				return
			}
		}
	}()
	return out, outErr
}

func drainStream(src <-chan anyllm.ChatCompletionChunk, srcErr <-chan error) {
	for src != nil || srcErr != nil {
		select {
		case _, ok := <-src:
			if !ok {
				src = nil
			}
		case _, ok := <-srcErr:
			if !ok {
				srcErr = nil
			}
		}
	}
}

func chatViaCLI(ctx context.Context, aiCfg config.AIConfig, pCfg config.ProviderConfig, req ChatRequest) (*anyllm.ChatCompletion, error) {
	var sysParts []string
	var convParts []string
	for _, m := range req.Messages {
		text := m.ContentString()
		if text == "" {
			continue
		}
		if m.Role == anyllm.RoleSystem {
			sysParts = append(sysParts, text)
		} else {
			convParts = append(convParts, fmt.Sprintf("%s: %s", strings.ToUpper(m.Role), text))
		}
	}
	completer := CompleterFor(aiCfg, pCfg)
	// System messages go to stdin as instructions, never into process arguments (see ClaudeCLI).
	out, err := completer.Complete(ctx, strings.Join(sysParts, "\n\n"), strings.Join(convParts, "\n\n"), req.ResponseFormat != nil)
	if err != nil {
		return nil, err
	}
	return &anyllm.ChatCompletion{
		ID:    "cli-completion",
		Model: completer.ModelName(),
		Choices: []anyllm.Choice{{
			Index:        0,
			Message:      anyllm.Message{Role: anyllm.RoleAssistant, Content: out},
			FinishReason: anyllm.FinishReasonStop,
		}},
	}, nil
}

// ListModels queries available models when the provider implements anyllm.ModelLister.
// cfg.Endpoint is treated as untrusted here: the backend's API key is only attached for its
// official host. Prefer ListModelsFor, which takes the endpoint from config.json.
func ListModels(ctx context.Context, cfg config.ProviderConfig, extraOpts ...anyllm.Option) ([]anyllm.Model, error) {
	return listModels(ctx, canonicalProvider(cfg), false, extraOpts)
}

// ListModelsFor lists the models of backend (empty = agent) with endpoint and key from config.
func ListModelsFor(ctx context.Context, aiCfg config.AIConfig, backend string, extraOpts ...anyllm.Option) ([]anyllm.Model, error) {
	return listModels(ctx, ProviderForBackend(aiCfg, backend), true, extraOpts)
}

func listModels(ctx context.Context, cfg config.ProviderConfig, trusted bool, extraOpts []anyllm.Option) ([]anyllm.Model, error) {
	if IsCLIBackend(cfg.Backend) {
		return nil, fmt.Errorf("CLI backend %s does not expose model listing", CanonicalBackend(cfg.Backend))
	}
	p, spec, _, err := buildProvider(cfg, trusted, extraOpts)
	if err != nil {
		return nil, err
	}
	lister, ok := p.(anyllm.ModelLister)
	if !ok {
		return nil, fmt.Errorf("provider %s does not support listing models", spec.ID)
	}
	ctx, cancel := context.WithTimeout(ctx, listModelsTimeout)
	defer cancel()
	res, err := lister.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}
