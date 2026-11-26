package ai

import "testing"

func TestCLIModelHints(t *testing.T) {
	for _, id := range []string{"claude-cli", "agy", "opencode-cli"} {
		h, ok := CLIModelHints(id)
		if !ok {
			t.Fatalf("expected CLIModelHints(%q) to return ok=true", id)
		}
		if len(h.Models) == 0 || h.HintDE == "" || h.HintEN == "" || h.PlaceholderDE == "" || h.PlaceholderEN == "" {
			t.Fatalf("incomplete CLIModelHints(%q): %+v", id, h)
		}
	}
	if _, ok := CLIModelHints("openai"); ok {
		t.Fatal("non-CLI backend openai must not return CLIModelHints")
	}
}
