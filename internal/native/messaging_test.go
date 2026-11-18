package native

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diver80/omathunder/internal/triage"
)

func TestConnRoundTripAndHandlerPing(t *testing.T) {
	var wire bytes.Buffer
	client := NewConn(nil, &wire)
	if err := client.Write(Request{ID: "1", Method: "ping"}); err != nil {
		t.Fatal(err)
	}

	var rawLen [4]byte
	copy(rawLen[:], wire.Bytes()[:4])
	if gotLen := int(binary.LittleEndian.Uint32(rawLen[:])); gotLen != wire.Len()-4 {
		t.Fatalf("frame length mismatch: header=%d actual=%d", gotLen, wire.Len()-4)
	}

	var out bytes.Buffer
	if err := RunHost(context.Background(), &wire, &out); err != nil {
		t.Fatal(err)
	}

	respConn := NewConn(&out, nil)
	var resp Response
	if err := respConn.Read(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.ID != "1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHostAnswersEveryConcurrentRequest(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var wire bytes.Buffer
	client := NewConn(nil, &wire)
	const n = 25
	for i := 0; i < n; i++ {
		method := "ping"
		if i%2 == 1 {
			method = "get_config"
		}
		if err := client.Write(Request{ID: string(rune('A' + i)), Method: method}); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := RunHost(context.Background(), &wire, &syncWriter{w: &out}); err != nil {
		t.Fatal(err)
	}
	respConn := NewConn(&out, nil)
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		var resp Response
		if err := respConn.Read(&resp); err != nil {
			t.Fatalf("response %d: %v", i, err)
		}
		if !resp.OK {
			t.Fatalf("request %s failed: %s", resp.ID, resp.Error)
		}
		seen[resp.ID] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct responses, got %d", n, len(seen))
	}
}

type syncWriter struct{ w *bytes.Buffer }

func (s *syncWriter) Write(p []byte) (int, error) { return s.w.Write(p) }

// The host only keeps what the bridge-only add-on still calls; the board's methods are gone.
func TestProtocolAndRemovedMethods(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	h := NewHandler(triage.NewStoreAt(t.TempDir()))
	call := func(method string, params any) Response {
		raw, _ := json.Marshal(params)
		return h.Handle(context.Background(), Request{ID: method, Method: method, Params: raw})
	}
	ping := call("ping", nil).Result.(map[string]any)
	if ping["protocol"] != ProtocolVersion || ping["version"] != Version || ProtocolVersion != 4 {
		t.Fatalf("ping must report version and protocol 4: %v", ping)
	}
	for _, m := range []string{"classify_batch", "analyze_thread", "move_kanban", "record_moves", "undo_scan_moves", "save_rules", "archive_export", "get_state"} {
		if r := call(m, map[string]any{}); r.OK || !strings.HasPrefix(r.Error, "unknown method") {
			t.Errorf("%s must be gone: %+v", m, r)
		}
	}
	// New mail is a notification: answered without a job runner, too.
	if r := call("triage_new_mail", map[string]any{"accountId": "a"}); !r.OK {
		t.Errorf("triage_new_mail: %+v", r)
	}
}

func TestStableBinaryPathSurvivesBrewUpgrade(t *testing.T) {
	cases := map[string]string{
		"/opt/homebrew/Cellar/omathunder/0.2.0/bin/omathunder":              "/opt/homebrew/opt/omathunder/bin/omathunder",
		"/home/linuxbrew/.linuxbrew/Cellar/omathunder/1.0_1/bin/omathunder": "/home/linuxbrew/.linuxbrew/opt/omathunder/bin/omathunder",
		"/Users/me/.local/bin/omathunder":                                   "/Users/me/.local/bin/omathunder",
		"/opt/homebrew/Cellar/other/1.0/bin/omathunder":                     "/opt/homebrew/Cellar/other/1.0/bin/omathunder",
	}
	for in, want := range cases {
		if got := StableBinaryPath(in); got != want {
			t.Errorf("StableBinaryPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadEnvFileAndShellQuote(t *testing.T) {
	cfgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgRoot)
	t.Setenv("OMATHUNDER_TEST_KEY", "")
	t.Setenv("OMATHUNDER_TEST_PLAIN", "")
	t.Setenv("OMATHUNDER_TEST_EXISTING", "from-env")

	envPath := EnvFilePath()
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "# comment\nexport OMATHUNDER_TEST_KEY=\"secret-val\"\nOMATHUNDER_TEST_PLAIN=abc # inline comment\nOMATHUNDER_TEST_EXISTING=from-file\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	LoadEnvFile()
	if got := os.Getenv("OMATHUNDER_TEST_KEY"); got != "secret-val" {
		t.Fatalf("expected exported quoted key loaded, got %q", got)
	}
	if got := os.Getenv("OMATHUNDER_TEST_PLAIN"); got != "abc" {
		t.Fatalf("expected plain key without inline comment, got %q", got)
	}
	if got := os.Getenv("OMATHUNDER_TEST_EXISTING"); got != "from-env" {
		t.Fatalf("expected existing env var preserved, got %q", got)
	}

	if q := shellQuote("/Users/O'Brien/bin/omathunder"); q != `'/Users/O'"'"'Brien/bin/omathunder'` {
		t.Fatalf("unexpected shellQuote output: %s", q)
	}
}

func TestHandlerSetThemeValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := triage.NewStoreAt(t.TempDir())
	h := NewHandler(store)
	call := func(method string, params any) Response {
		raw, _ := json.Marshal(params)
		return h.Handle(context.Background(), Request{ID: method, Method: method, Params: raw})
	}

	if r := call("set_theme", map[string]any{"mode": "bogus"}); r.OK {
		t.Fatalf("expected invalid mode rejected, got %+v", r)
	}
	if r := call("set_theme", map[string]any{"mode": "preset", "preset": "non-existent-theme"}); r.OK {
		t.Fatalf("expected unknown preset rejected, got %+v", r)
	}
	if r := call("set_theme", map[string]any{"mode": "preset", "preset": "nord"}); !r.OK {
		t.Fatalf("expected valid preset nord to succeed, got %s", r.Error)
	}
}
