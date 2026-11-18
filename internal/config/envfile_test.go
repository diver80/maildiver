package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keys saved from the app reach the running process at once, removed keys are gone at once, and
// the rest of the file (comments, other variables) stays.
func TestEnvFileKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "from-shell")
	path := EnvFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# my keys\nexport GEMINI_API_KEY='abc'\nOTHER=1 # note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	LoadEnvFile()
	if got := LookupAPIKey("GEMINI_API_KEY", "GOOGLE_API_KEY"); got != "abc" {
		t.Fatalf("lookup = %q", got)
	}
	if got := LookupAPIKey("ANTHROPIC_API_KEY"); got != "from-shell" {
		t.Fatalf("a key only in the environment must still count: %q", got)
	}

	if err := SetEnvFileKeys(map[string]string{"GEMINI_API_KEY": "new-key-1", "OPENAI_API_KEY": "sk-$a b#c"}); err != nil {
		t.Fatal(err)
	}
	if got := LookupAPIKey("GEMINI_API_KEY"); got != "new-key-1" {
		t.Fatalf("changed key not seen at once: %q", got)
	}
	if got := LookupAPIKey("OPENAI_API_KEY"); got != "sk-$a b#c" {
		t.Fatalf("quoted key = %q", got)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode %o, want 600", st.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# my keys") || !strings.Contains(string(data), "OTHER=1 # note") {
		t.Fatalf("other lines lost:\n%s", data)
	}

	// Removing a key wins over the copy LoadEnvFile put into the environment earlier.
	if err := SetEnvFileKeys(map[string]string{"GEMINI_API_KEY": ""}); err != nil {
		t.Fatal(err)
	}
	if got := LookupAPIKey("GEMINI_API_KEY"); got != "" {
		t.Fatalf("removed key still found: %q", got)
	}
	if HasAPIKeyInFile("GEMINI_API_KEY") || !HasAPIKeyInFile("OPENAI_API_KEY") {
		t.Fatal("HasAPIKeyInFile is wrong")
	}
	if err := SetEnvFileKeys(map[string]string{"BAD KEY": "x"}); err == nil {
		t.Fatal("invalid key names must be refused")
	}
	if err := SetEnvFileKeys(map[string]string{"OPENAI_API_KEY": "it's"}); err == nil {
		t.Fatal("values with quotes must be refused")
	}
}
