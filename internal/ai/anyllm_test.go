package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/postdive/internal/config"
)

func TestSupportedProvidersComeFromRegistry(t *testing.T) {
	var all []string
	for _, s := range backendRegistry {
		all = append(all, s.ID)
	}
	if !slices.Equal(all, config.BackendNames()) {
		t.Fatalf("registry %v and config.BackendNames %v must match (same order)", all, config.BackendNames())
	}
	var ids []string
	for _, d := range SupportedProviders() {
		ids = append(ids, d.ID)
	}
	want := []string{"claude-cli", "agy", "ollama", "openaicompat", "claude-api", "gemini-api", "openai"}
	if !slices.Equal(ids, want) {
		t.Fatalf("offered providers %v, want %v", ids, want)
	}
	// A legacy backend stays in the list while it is selected.
	ids = nil
	for _, d := range SupportedProviders("DeepSeek", "") {
		ids = append(ids, d.ID)
	}
	if !slices.Equal(ids, append(slices.Clone(want), "deepseek")) {
		t.Fatalf("selected legacy backend must stay listed: %v", ids)
	}
	for _, d := range SupportedProviders(config.BackendNames()...) {
		if d.Kind == kindCLI && (d.Streaming || d.Tools) {
			t.Fatalf("CLI backend %s must not claim streaming/tools", d.ID)
		}
		if got := defaultModelForBackend(d.ID); got != d.DefaultModel {
			t.Fatalf("%s: default model %q vs descriptor %q", d.ID, got, d.DefaultModel)
		}
	}
}

func TestLocalOpenAICompatGetsTheLocalTimeout(t *testing.T) {
	spec := backendByID["openaicompat"]
	for ep, want := range map[string]time.Duration{
		"http://127.0.0.1:8000/v1": localTimeout, "http://localhost:1234/v1": localTimeout, "http://[::1]:8000/v1": localTimeout,
		"https://llm.example.com/v1": apiTimeout, "": apiTimeout,
	} {
		if got := spec.timeoutFor(ep); got != want {
			t.Errorf("timeoutFor(%q) = %v, want %v", ep, got, want)
		}
	}
	if got := backendByID["openai"].timeoutFor("http://127.0.0.1:8000/v1"); got != apiTimeout {
		t.Errorf("only openaicompat is stretched, openai got %v", got)
	}
}

func TestBackendNamesAreCaseInsensitiveAndUnknownIsRefused(t *testing.T) {
	for in, want := range map[string]string{
		"OpenAI": "openai", " Gemini ": "gemini-api", "Anthropic": "claude-api", "CLAUDE-CLI": "claude-cli",
		"gemini_api": "gemini-api", "OpenAI-Compatible": "openaicompat", "Llama.cpp": "llamacpp",
	} {
		if got := CanonicalBackend(in); got != want {
			t.Errorf("CanonicalBackend(%q) = %q, want %q", in, got, want)
		}
	}
	if !IsCLIBackend("Claude-CLI") || IsCLIBackend("OpenAI") || IsCLIBackend("nope") {
		t.Fatal("IsCLIBackend must use canonical names")
	}
	if c, ok := NewCompleter(config.ProviderConfig{Backend: "OpenAI"}).(*AnyLLMCompleter); !ok || c.Name() != "openai" || c.ModelName() != "gpt-4o-mini" {
		t.Fatalf("\"OpenAI\" must map to the openai completer, got %#v", NewCompleter(config.ProviderConfig{Backend: "OpenAI"}))
	}
	if _, ok := NewCompleter(config.ProviderConfig{Backend: "Claude-CLI"}).(*ClaudeCLI); !ok {
		t.Fatal("\"Claude-CLI\" must map to the Claude CLI")
	}

	// An unknown backend must never fall back to another provider (formerly: Gemini).
	srv, rec := fakeGemini(t, "", `{"bucket":"fyi"}`)
	t.Setenv("GEMINI_API_KEY", "gem-key")
	cfg := config.Default().AI
	cfg.Classifier = config.ProviderConfig{Backend: "OpenAl", Endpoint: srv.URL}
	cfg.Agent = config.ProviderConfig{Backend: "OpenAl", Endpoint: srv.URL}
	_, err := NewCompleter(cfg.Classifier).Complete(context.Background(), "s", "mail", true)
	if !errors.Is(err, ErrUnknownBackend) || !strings.Contains(strings.ToLower(err.Error()), "openal") {
		t.Fatalf("unknown backend must be refused clearly, got %v", err)
	}
	if _, err := Chat(context.Background(), cfg, ChatRequest{Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: "hi"}}}); !errors.Is(err, ErrUnknownBackend) {
		t.Fatalf("Chat with unknown backend: %v", err)
	}
	if _, err := Chat(context.Background(), config.AIConfig{}, ChatRequest{Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: "hi"}}}); !errors.Is(err, ErrNoBackend) {
		t.Fatalf("Chat without backend: %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("no request may reach any provider, got %d", rec.count())
	}
	// Opt-in check also works case-insensitively and for the old Gemini CLI name.
	for _, b := range []string{"Gemini-CLI", "AGY"} {
		if _, err := CompleterFor(config.Default().AI, config.ProviderConfig{Backend: b}).Complete(context.Background(), "s", "u", false); err == nil || err.Error() != ErrUnrestrictedCLI {
			t.Fatalf("%s must need the opt-in, got %v", b, err)
		}
	}
}

func TestAnyLLMCompleterOpenAICompatAndTools(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := rec.add(r)
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			rec.problem("unexpected path: %s", r.URL.Path)
		}
		if containsStreamTrue(body) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: "+`{"id":"ch-1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Hallo "}}]}`+"\n\n")
			_, _ = fmt.Fprint(w, "data: "+`{"id":"ch-1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"content":"Welt"},"finish_reason":"stop"}]}`+"\n\n")
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		writeJSON(w, map[string]any{
			"id": "cmp-1", "object": "chat.completion", "model": "test-model",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "tool_calls",
				"message": map[string]any{
					"role": "assistant", "content": "",
					"tool_calls": []map[string]any{{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "search_mail", "arguments": `{"q":"rechnung"}`},
					}},
				},
			}},
		})
	}))
	defer srv.Close()
	defer rec.check(t)

	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "openai", Model: "gpt-4o-mini", Endpoint: srv.URL + "/v1"}

	resp, err := Chat(context.Background(), cfg, ChatRequest{
		Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: "Suche nach Rechnungen"}},
		Tools: []anyllm.Tool{{
			Type:     "function",
			Function: anyllm.Function{Name: "search_mail", Description: "Search mailbox"},
		}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if len(resp.Choices) != 1 || len(resp.Choices[0].Message.ToolCalls) != 1 || resp.Choices[0].Message.ToolCalls[0].Function.Name != "search_mail" {
		t.Fatalf("expected 1 search_mail tool call, got %+v", resp)
	}
	if b := rec.body(0); !strings.Contains(b, "8192") {
		t.Fatalf("expected default max tokens in request, got %s", b)
	}

	chunkCh, errCh := ChatStream(context.Background(), cfg, ChatRequest{
		Stream:   true,
		Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: "Sag hallo"}},
	})
	if got := collectStream(t, chunkCh, errCh); got != "Hallo Welt" {
		t.Fatalf("expected streamed 'Hallo Welt', got %q", got)
	}
}

func collectStream(t *testing.T, chunkCh <-chan anyllm.ChatCompletionChunk, errCh <-chan error) string {
	t.Helper()
	var got strings.Builder
	for chunkCh != nil || errCh != nil {
		select {
		case c, open := <-chunkCh:
			if !open {
				chunkCh = nil
				continue
			}
			if len(c.Choices) > 0 {
				got.WriteString(c.Choices[0].Delta.Content)
			}
		case err, open := <-errCh:
			if !open {
				errCh = nil
				continue
			}
			if err != nil {
				t.Fatalf("ChatStream error: %v", err)
			}
		}
	}
	return got.String()
}

func TestChatIgnoresRequestEndpoint(t *testing.T) {
	good, goodRec := fakeOpenAI(t, "sk-real", "from config")
	evil, evilRec := fakeOpenAI(t, "", "stolen")
	t.Setenv("OPENAI_API_KEY", "sk-real")
	msgs := []anyllm.Message{{Role: anyllm.RoleUser, Content: "hi"}}

	// 1. Same backend as the agent: the configured endpoint wins over the request.
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "openai", Model: "gpt-4o-mini", Endpoint: good.URL + "/v1"}
	for _, backend := range []string{"", "OpenAI"} {
		resp, err := Chat(context.Background(), cfg, ChatRequest{Backend: backend, Endpoint: evil.URL + "/v1", Messages: msgs})
		if err != nil || resp.Choices[0].Message.ContentString() != "from config" {
			t.Fatalf("backend %q: resp=%+v err=%v", backend, resp, err)
		}
	}

	// 2. Another backend than the agent: only ai.endpoints from config counts.
	cfg = config.Default().AI // agent: claude-cli
	cfg.Endpoints = map[string]string{"openai": good.URL + "/v1"}
	if _, err := Chat(context.Background(), cfg, ChatRequest{Backend: "openai", Endpoint: evil.URL + "/v1", Messages: msgs}); err != nil {
		t.Fatalf("Chat via ai.endpoints: %v", err)
	}
	chunkCh, errCh := ChatStream(context.Background(), cfg, ChatRequest{Backend: "openai", Endpoint: evil.URL + "/v1", Messages: msgs, Stream: true})
	if got := collectStream(t, chunkCh, errCh); got != "Hallo Welt" {
		t.Fatalf("stream via config endpoint: %q", got)
	}
	if p := ProviderForBackend(cfg, "OPENAI"); p.Backend != "openai" || p.Endpoint != good.URL+"/v1" {
		t.Fatalf("ProviderForBackend: %+v", p)
	}
	if p := ProviderForBackend(config.Default().AI, "groq"); p.Endpoint != "" || p.Backend != "groq" {
		t.Fatalf("unconfigured backend must use its official URL: %+v", p)
	}

	goodRec.check(t)
	if evilRec.count() != 0 {
		t.Fatalf("request endpoint must be ignored, evil server got %d requests", evilRec.count())
	}
	if goodRec.count() != 4 {
		t.Fatalf("configured endpoint should get 4 requests, got %d", goodRec.count())
	}
}

func TestOpenAICompatNeverGetsOpenAIKey(t *testing.T) {
	srv, rec := fakeOpenAI(t, "", "ok")
	t.Setenv("OPENAI_API_KEY", "sk-real-openai")
	t.Setenv("OPENAI_ORG_ID", "org-secret")
	t.Setenv("OPENAICOMPAT_API_KEY", "")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "openaicompat", Model: "local-model", Endpoint: srv.URL + "/v1"}

	if _, err := NewCompleter(cfg.Agent).Complete(context.Background(), "sys", "mail", false); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	t.Setenv("OPENAICOMPAT_API_KEY", "compat-key")
	if _, err := Chat(context.Background(), cfg, ChatRequest{Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := rec.header(0).Get("Authorization"); got != "Bearer postdive-local" {
		t.Fatalf("without OPENAICOMPAT_API_KEY a placeholder must be sent, got %q", got)
	}
	if got := rec.header(1).Get("Authorization"); got != "Bearer compat-key" {
		t.Fatalf("OPENAICOMPAT_API_KEY must be used, got %q", got)
	}
	for i := 0; i < rec.count(); i++ {
		h := rec.header(i)
		if strings.Contains(fmt.Sprint(h), "sk-real-openai") || h.Get("OpenAI-Organization") != "" {
			t.Fatalf("OpenAI credentials leaked to a compatible server: %v", h)
		}
	}

	// Without an endpoint there is no official host: clear error, no request anywhere.
	cfg.Agent.Endpoint = ""
	if _, err := Chat(context.Background(), cfg, ChatRequest{Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: "hi"}}}); !errors.Is(err, ErrEndpointRequired) {
		t.Fatalf("openaicompat without endpoint: %v", err)
	}
}

func TestListModelsKeyBinding(t *testing.T) {
	srv, rec := fakeOpenAI(t, "sk-real", "")
	t.Setenv("OPENAI_API_KEY", "sk-real")

	// A caller-supplied (untrusted) endpoint never receives the key.
	if _, err := ListModels(context.Background(), config.ProviderConfig{Backend: "openai", Endpoint: srv.URL + "/v1"}); !errors.Is(err, ErrKeyNotForEndpoint) {
		t.Fatalf("ListModels with foreign endpoint must refuse the key, got %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("no request expected, got %d", rec.count())
	}

	// The same endpoint from config.json is trusted.
	cfg := config.Default().AI
	cfg.Endpoints = map[string]string{"OpenAI": srv.URL + "/v1"}
	models, err := ListModelsFor(context.Background(), cfg, "openai")
	rec.check(t)
	if err != nil || len(models) != 1 || models[0].ID != "test-model" {
		t.Fatalf("ListModelsFor: models=%+v err=%v", models, err)
	}
	if _, err := ListModelsFor(context.Background(), cfg, "claude-cli"); err == nil {
		t.Fatal("CLI backends cannot list models")
	}
}

func TestGeminiEndpointHonouredInChatAndClassify(t *testing.T) {
	srv, rec := fakeGemini(t, "gem-key", "Antwort vom konfigurierten Endpoint")
	t.Setenv("GEMINI_API_KEY", "gem-key")
	// An SDK-level env override must not win over config.json either.
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "http://127.0.0.1:1")
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "gemini", Endpoint: srv.URL}

	resp, err := Chat(context.Background(), cfg, ChatRequest{Messages: []anyllm.Message{
		{Role: anyllm.RoleSystem, Content: "sys"}, {Role: anyllm.RoleUser, Content: "hi"},
	}})
	rec.check(t)
	if err != nil {
		t.Fatalf("Chat via gemini endpoint: %v", err)
	}
	if got := resp.Choices[0].Message.ContentString(); got != "Antwort vom konfigurierten Endpoint" {
		t.Fatalf("unexpected answer %q", got)
	}
	if rec.count() != 1 || !strings.Contains(rec.path(0), "gemini-2.5-flash") {
		t.Fatalf("configured gemini endpoint must receive the chat request, got %q", rec.path(0))
	}
}

func TestGuardTransportPinsHostAndCapsBody(t *testing.T) {
	other, otherRec := fakeOpenAI(t, "", "x")
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, other.URL+"/v1/models", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		chunk := strings.Repeat("a", 1<<20)
		for i := 0; i < 6; i++ {
			_, _ = fmt.Fprint(w, chunk)
		}
	}))
	defer big.Close()
	target, _ := url.Parse(big.URL)
	client := &http.Client{Transport: &guardTransport{base: sharedTransport, target: target}}

	if _, err := client.Get(other.URL + "/v1/models"); !errors.Is(err, ErrKeyNotForEndpoint) {
		t.Fatalf("foreign host must be refused, got %v", err)
	}
	if _, err := client.Get(big.URL + "/redirect"); !errors.Is(err, ErrKeyNotForEndpoint) {
		t.Fatalf("redirect to a foreign host must be refused, got %v", err)
	}
	if otherRec.count() != 0 {
		t.Fatalf("foreign host got %d requests", otherRec.count())
	}
	resp, err := client.Get(big.URL + "/big")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64<<10)
	var readErr error
	for readErr == nil {
		_, readErr = resp.Body.Read(buf)
	}
	if !errors.Is(readErr, errResponseTooLarge) {
		t.Fatalf("expected size cap error, got %v", readErr)
	}
}

func TestProvidersAreReused(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-a")
	pc := config.ProviderConfig{Backend: "openai"}
	p1, _, _, err := buildProvider(pc, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	p2, _, _, _ := buildProvider(config.ProviderConfig{Backend: "OpenAI"}, true, nil)
	if p1 != p2 {
		t.Fatal("same backend/endpoint/key must reuse the provider")
	}
	t.Setenv("OPENAI_API_KEY", "sk-b")
	if p3, _, _, _ := buildProvider(pc, true, nil); p3 == p1 {
		t.Fatal("a changed key must create a new provider")
	}
}

func TestWatchStreamIdleTimeout(t *testing.T) {
	src := make(chan anyllm.ChatCompletionChunk)
	srcErr := make(chan error)
	ctx, cancel := context.WithCancel(context.Background())
	out, outErr := watchStream(ctx, cancel, src, srcErr, 50*time.Millisecond)
	go func() {
		src <- anyllm.ChatCompletionChunk{Choices: []anyllm.ChunkChoice{{Delta: anyllm.ChunkDelta{Content: "a"}}}}
		<-ctx.Done() // provider stops once the watchdog cancels
		close(src)
		close(srcErr)
	}()
	var got string
	var err error
	for out != nil || outErr != nil {
		select {
		case c, ok := <-out:
			if !ok {
				out = nil
				continue
			}
			got += c.Choices[0].Delta.Content
		case e, ok := <-outErr:
			if !ok {
				outErr = nil
				continue
			}
			err = e
		}
	}
	if got != "a" || err == nil || !strings.Contains(err.Error(), "idle") {
		t.Fatalf("expected one chunk then idle error, got %q / %v", got, err)
	}
}

// fakeCLI installs a fake binary named name on PATH that records its arguments and stdin.
func fakeCLI(t *testing.T, name, output string) (argsFile, stdinFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")
	stdinFile = filepath.Join(dir, "stdin.txt")
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\n' \"$a\" >> %q; done\ncat > %q\nprintf '%%s' %q\n",
		argsFile, stdinFile, output)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile, stdinFile
}

func TestCLIChatKeepsContentOutOfArgv(t *testing.T) {
	argsFile, stdinFile := fakeCLI(t, "claude", `{"result":"Antwort aus der CLI","is_error":false}`)
	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "Claude-CLI"}
	msgs := []anyllm.Message{
		{Role: anyllm.RoleSystem, Content: "Kontext: SECRET-MAIL-BODY von alice@example.com"},
		{Role: anyllm.RoleUser, Content: "SECRET-QUESTION"},
	}
	resp, err := Chat(context.Background(), cfg, ChatRequest{Messages: msgs})
	if err != nil {
		t.Fatalf("Chat via CLI: %v", err)
	}
	if got := resp.Choices[0].Message.ContentString(); got != "Antwort aus der CLI" {
		t.Fatalf("unexpected answer %q", got)
	}
	chunkCh, errCh := ChatStream(context.Background(), cfg, ChatRequest{Messages: msgs, Stream: true})
	if got := collectStream(t, chunkCh, errCh); got != "Antwort aus der CLI" {
		t.Fatalf("unexpected streamed answer %q", got)
	}

	args, _ := os.ReadFile(argsFile)
	stdin, _ := os.ReadFile(stdinFile)
	for _, secret := range []string{"SECRET", "alice@example.com"} {
		if strings.Contains(string(args), secret) {
			t.Fatalf("mail content leaked into argv: %s", args)
		}
	}
	if !strings.Contains(string(stdin), "SECRET-MAIL-BODY") || !strings.Contains(string(stdin), "SECRET-QUESTION") ||
		!strings.Contains(string(stdin), "<postdive_instructions>") {
		t.Fatalf("system and user content must arrive via stdin, got %q", stdin)
	}
	for _, flag := range []string{"--tools", "--strict-mcp-config", "mcp__*", "--no-session-persistence"} {
		if !strings.Contains(string(args), flag) {
			t.Fatalf("safety flag %s missing from %s", flag, args)
		}
	}
}

func TestUnrestrictedCLIUsesStdin(t *testing.T) {
	argsFile, stdinFile := fakeCLI(t, "opencode", "ok")
	cfg := config.Default().AI
	cfg.AllowUnrestrictedCLI = true
	out, err := CompleterFor(cfg, config.ProviderConfig{Backend: "opencode-cli"}).Complete(context.Background(), "SYS-SECRET", "MAIL-SECRET", false)
	if err != nil || out != "ok" {
		t.Fatalf("opencode-cli: %q %v", out, err)
	}
	args, _ := os.ReadFile(argsFile)
	stdin, _ := os.ReadFile(stdinFile)
	if strings.Contains(string(args), "SECRET") || !strings.Contains(string(stdin), "MAIL-SECRET") {
		t.Fatalf("argv=%q stdin=%q", args, stdin)
	}
}

func TestAgyRunsWithToolsDeniedAndContentOnStdin(t *testing.T) {
	result := `{"event":"result","result":{"status":"SUCCESS","response":"Antwort von agy\n"}}`
	argsFile, stdinFile := fakeCLI(t, "agy", result)
	cfg := config.Default().AI
	cfg.AllowUnrestrictedCLI = true
	out, err := CompleterFor(cfg, config.ProviderConfig{Backend: "gemini-cli", Model: "gemini-3.8-flash-low"}).Complete(context.Background(), "SYS-SECRET", "MAIL-SECRET", false)
	if err != nil || out != "Antwort von agy" {
		t.Fatalf("agy: %q %v", out, err)
	}
	args, _ := os.ReadFile(argsFile)
	stdin, _ := os.ReadFile(stdinFile)
	if strings.Contains(string(args), "SECRET") {
		t.Fatalf("mail content leaked into argv: %s", args)
	}
	for _, want := range []string{"read_file(*)", "read_url_content(*)", "run_command(*)", "command(*)", "mcp(*)",
		"--disable-slash-commands", "stream-json", "gemini-3.8-flash-low", "-p="} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("agy argument %s missing from %s", want, args)
		}
	}
	var msg struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(stdin, &msg); err != nil || msg.Event != "user" ||
		!strings.Contains(msg.Message.Content, "MAIL-SECRET") || !strings.Contains(msg.Message.Content, "<postdive_instructions>") {
		t.Fatalf("stdin must be one stream-json user message with the content: %q (%v)", stdin, err)
	}

	if _, err := parseAgyResult([]byte(`{"event":"result","result":{"status":"ERROR","error":"quota"}}`)); err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("agy errors must surface: %v", err)
	}
}

func TestJSONObjectResponseFormatFallback(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "json_object") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"response_format type 'json_object' cannot be enforced without llguidance","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"mtplx","choices":[{"index":0,"message":{"role":"assistant","content":"{\"summary\":\"OK\"}"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "llamacpp", Model: "mtplx-inkling-2-mini-optimized-speed", Endpoint: srv.URL + "/v1"}
	got, err := CompleterFor(cfg, cfg.Agent).Complete(context.Background(), "Return JSON", "Summarize this", true)
	if err != nil {
		t.Fatalf("Complete should fall back without response_format, got err=%v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 requests (initial + fallback), got %d", calls)
	}
	if got != `{"summary":"OK"}` {
		t.Fatalf("unexpected fallback content %q", got)
	}
}

func TestChatStreamJSONFallback(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "json_object") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"guided decoding not enabled: llguidance missing","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"mtplx\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "llamacpp", Model: "mtplx-inkling-2-mini", Endpoint: srv.URL + "/v1"}
	chunks, errs := ChatStream(context.Background(), cfg, ChatRequest{
		Messages:       []anyllm.Message{{Role: anyllm.RoleUser, Content: "Hi"}},
		ResponseFormat: &anyllm.ResponseFormat{Type: "json_object"},
		Stream:         true,
	})
	var sb strings.Builder
	for ch := range chunks {
		if len(ch.Choices) > 0 {
			sb.WriteString(ch.Choices[0].Delta.Content)
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls (initial + stream fallback), got %d", calls)
	}
	if sb.String() != "hello" {
		t.Fatalf("expected streamed fallback text 'hello', got %q", sb.String())
	}
}

func TestAnalyzeHonorsRequestBackendAndModel(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-2","object":"chat.completion","model":"custom-model","choices":[{"index":0,"message":{"role":"assistant","content":"{\"answer\":\"Kurzfassung\"}"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	cfg := config.Default().AI
	cfg.Agent = config.ProviderConfig{Backend: "ollama", Model: "other-model"}
	cfg.Endpoints = map[string]string{"llamacpp": srv.URL + "/v1"}

	res, err := Analyze(context.Background(), cfg, AnalyzeRequest{
		Backend: "llamacpp",
		Model:   "custom-model",
		Mode:    "summarize",
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if gotModel != "custom-model" || res.Provider != "llamacpp" {
		t.Fatalf("expected llamacpp/custom-model, got provider=%q model=%q", res.Provider, gotModel)
	}
}
