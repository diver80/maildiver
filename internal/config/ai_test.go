package config

import "testing"

func TestNormalizeCanonicalizesAIBackends(t *testing.T) {
	cfg := Default()
	cfg.AI.Agent = ProviderConfig{Backend: " Anthropic ", Model: " claude-sonnet-4-5 ", Endpoint: " https://proxy.example "}
	cfg.AI.Classifier = ProviderConfig{Backend: "GEMINI"}
	cfg.AI.Endpoints = map[string]string{"Ollama": " http://10.0.0.5:11434 ", "openai": "  "}
	got := Normalize(cfg).AI
	if got.Agent != (ProviderConfig{Backend: "claude-api", Model: "claude-sonnet-4-5", Endpoint: "https://proxy.example"}) {
		t.Fatalf("agent: %+v", got.Agent)
	}
	if got.Classifier.Backend != "gemini-api" {
		t.Fatalf("classifier: %+v", got.Classifier)
	}
	if len(got.Endpoints) != 1 || got.Endpoints["ollama"] != "http://10.0.0.5:11434" {
		t.Fatalf("endpoints: %+v", got.Endpoints)
	}

	// Unknown names are kept (lower-cased) so the AI layer can refuse them with a clear error.
	cfg.AI.Agent.Backend = "Claude"
	if got := Normalize(cfg).AI.Agent.Backend; got != "claude" || IsKnownBackend(got) {
		t.Fatalf("ambiguous backend must stay unknown, got %q", got)
	}
	if !IsKnownBackend("llama_cpp") || CanonicalBackend("OpenCode") != "opencode-cli" {
		t.Fatal("aliases")
	}
}

func TestApplyPatchReplacesAIEndpoints(t *testing.T) {
	base := Default()
	base.AI.Endpoints = map[string]string{"ollama": "http://a:1", "llamacpp": "http://b:2"}
	next, err := ApplyPatch(base, []byte(`{"ai":{"endpoints":{"openaicompat":"http://c:3"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(next.AI.Endpoints) != 1 || next.AI.Endpoints["openaicompat"] != "http://c:3" {
		t.Fatalf("patched endpoints must replace the map: %+v", next.AI.Endpoints)
	}
	if len(base.AI.Endpoints) != 2 {
		t.Fatalf("caller's map must not be mutated: %+v", base.AI.Endpoints)
	}
	kept, _ := ApplyPatch(base, []byte(`{"ai":{"agent":{"backend":"OpenAI","model":"gpt-4o-mini"}}}`))
	if len(kept.AI.Endpoints) != 2 || kept.AI.Agent.Backend != "openai" {
		t.Fatalf("unrelated patch must keep endpoints and canonicalize: %+v", kept.AI)
	}
}
