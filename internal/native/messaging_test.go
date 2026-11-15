package native

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/diver80/omathunder/internal/archive"
	"github.com/diver80/omathunder/internal/config"
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

func TestHandlerRulesKanbanAndRollback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	store := triage.NewStoreAt(t.TempDir())
	h := NewHandler(store)

	enable, _ := json.Marshal(map[string]any{
		"features": map[string]any{"triage": true},
		"mailbox":  map[string]any{"readOnly": false, "triageAccountIds": []string{"acc1"}, "kanbanAccountIds": []string{"acc1"}},
	})
	if r := h.Handle(context.Background(), Request{ID: "e1", Method: "save_config", Params: enable}); !r.OK {
		t.Fatalf("enable triage failed: %s", r.Error)
	}

	rulesParams, _ := json.Marshal(map[string]any{
		"incomingFolder": "INBOX",
		"rules": []map[string]any{
			{
				"id":            "rule-acme",
				"space":         "Acme GmbH",
				"folderPath":    "Spaces/Acme/Incoming",
				"domains":       []string{"acme.de"},
				"autoMove":      true,
				"includeInScan": true,
			},
		},
	})
	if r := h.Handle(context.Background(), Request{ID: "r1", Method: "save_rules", Params: rulesParams}); !r.OK {
		t.Fatalf("save_rules failed: %s", r.Error)
	}

	params, _ := json.Marshal(map[string]any{
		"messages": []triage.MessageInput{
			{
				ID:        "m10",
				AccountID: "acc1",
				Subject:   "Rechnung 2026",
				From:      "billing@acme.de",
				Unread:    true,
			},
		},
	})
	if r := h.Handle(context.Background(), Request{ID: "c1", Method: "classify_batch", Params: params}); !r.OK {
		t.Fatalf("classify_batch failed: %s", r.Error)
	}

	moveParams, _ := json.Marshal(map[string]any{"messageId": "m10", "column": "done"})
	if r := h.Handle(context.Background(), Request{ID: "k1", Method: "move_kanban", Params: moveParams}); !r.OK {
		t.Fatalf("move_kanban failed: %s", r.Error)
	}

	recParams, _ := json.Marshal(map[string]any{
		"moves": []map[string]any{
			{"messageId": "m10", "accountId": "acc1", "ruleId": "rule-acme", "originalFolder": "INBOX", "targetFolder": "Spaces/Acme/Incoming"},
		},
	})
	if r := h.Handle(context.Background(), Request{ID: "m1", Method: "record_moves", Params: recParams}); !r.OK {
		t.Fatalf("record_moves failed: %s", r.Error)
	}

	delParams, _ := json.Marshal(map[string]any{"ruleId": "rule-acme", "revertToIncoming": true})
	delResp := h.Handle(context.Background(), Request{ID: "d1", Method: "delete_rule", Params: delParams})
	if !delResp.OK {
		t.Fatalf("delete_rule failed: %s", delResp.Error)
	}
	resMap, _ := delResp.Result.(map[string]any)
	reverts, _ := resMap["revertMoves"].([]triage.MovedMessage)
	if len(reverts) != 1 || reverts[0].MessageID != "m10" {
		t.Fatalf("expected 1 revertMove for m10, got %+v", resMap["revertMoves"])
	}

	// Ensure partial save_config (e.g. from options.html) preserves rules & incomingFolder
	rulesParams2, _ := json.Marshal(map[string]any{
		"incomingFolder": "INBOX",
		"rules":          []map[string]any{{"id": "keep-me", "space": "Keep GmbH", "folderPath": "Spaces/Keep", "domains": []string{"keep.de"}}},
	})
	h.Handle(context.Background(), Request{ID: "r2", Method: "save_rules", Params: rulesParams2})

	partialCfg, _ := json.Marshal(map[string]any{
		"theme": map[string]any{"mode": "preset", "preset": "nord"},
	})
	saveResp := h.Handle(context.Background(), Request{ID: "s1", Method: "save_config", Params: partialCfg})
	if !saveResp.OK {
		t.Fatalf("save_config failed: %s", saveResp.Error)
	}
	getCfgResp := h.Handle(context.Background(), Request{ID: "g1", Method: "get_config"})
	if !getCfgResp.OK {
		t.Fatalf("get_config failed: %s", getCfgResp.Error)
	}
	rawCfg, _ := json.Marshal(getCfgResp.Result)
	if !bytes.Contains(rawCfg, []byte("keep-me")) {
		t.Fatalf("expected save_config to preserve existing rules, got %s", string(rawCfg))
	}
}

func TestHandlerEnforcesFeaturesReadOnlyAndAccounts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	h := NewHandler(triage.NewStoreAt(t.TempDir()))
	ctx := context.Background()
	call := func(method string, params any) Response {
		raw, _ := json.Marshal(params)
		return h.Handle(ctx, Request{ID: method, Method: method, Params: raw})
	}
	msgs := map[string]any{"messages": []triage.MessageInput{
		{ID: "1", ThreadID: "<1@acme.de>", AccountID: "work", Folder: "INBOX", InInbox: true, From: "a@acme.de", Subject: "Rechnung", Unread: true},
		{ID: "2", ThreadID: "<2@acme.de>", AccountID: "private", Folder: "INBOX", InInbox: true, From: "a@acme.de", Subject: "Rechnung", Unread: true},
		{ID: "3", ThreadID: "<3@acme.de>", AccountID: "personal", Folder: "INBOX", InInbox: true, From: "a@acme.de", Subject: "Rechnung", Unread: true},
	}}

	// Defaults: triage off, read-only on, assistant on.
	if r := call("classify_batch", msgs); r.OK || r.Error != ErrTriageDisabled {
		t.Fatalf("classify_batch must be refused while triage is off: %+v", r)
	}
	if r := call("move_kanban", map[string]any{"messageId": "1", "column": "done"}); r.OK || r.Error != ErrTriageDisabled {
		t.Fatalf("move_kanban must be refused while triage is off: %+v", r)
	}

	if r := call("save_config", map[string]any{"features": map[string]any{"triage": true}}); !r.OK {
		t.Fatal(r.Error)
	}
	if r := call("classify_batch", msgs); r.OK || r.Error != ErrNoTriageAccounts {
		t.Fatalf("classify_batch must require an enabled account: %+v", r)
	}

	// "work" has Spaces+Kanban; "personal" has Spaces/Rules without Kanban; "private" has neither.
	if r := call("save_config", map[string]any{"mailbox": map[string]any{"triageAccountIds": []string{"work", "personal"}, "kanbanAccountIds": []string{"work"}}}); !r.OK {
		t.Fatal(r.Error)
	}
	if r := call("save_rules", map[string]any{"rules": []map[string]any{
		{"id": "acme", "space": "Acme", "accountId": "work", "folderPath": "Spaces/Acme", "domains": []string{"acme.de"}, "autoMove": true},
	}}); !r.OK {
		t.Fatal(r.Error)
	}
	r := call("classify_batch", msgs)
	if !r.OK {
		t.Fatal(r.Error)
	}
	res := r.Result.(map[string]any)
	if got := len(res["results"].([]triage.Classification)); got != 1 {
		t.Fatalf("only the kanban-enabled account may be classified, got %d results", got)
	}
	if res["skippedAccounts"].(int) != 2 {
		t.Fatalf("expected 2 skipped mails (non-kanban + disabled), got %v", res["skippedAccounts"])
	}
	planned := res["plannedMoves"].([]triage.PlannedMove)
	if len(planned) != 1 || planned[0].MessageID != "1" || res["readOnly"] != true {
		t.Fatalf("expected a dry-run plan for mail 1 in read-only mode: %+v", res)
	}

	// Read-only blocks recording moves and reverting on rule deletion.
	if r := call("record_moves", map[string]any{"moves": []map[string]any{{"messageId": "1", "accountId": "work", "ruleId": "acme"}}}); r.OK || r.Error != ErrReadOnly {
		t.Fatalf("record_moves must be refused in read-only mode: %+v", r)
	}
	if r := call("delete_rule", map[string]any{"ruleId": "acme", "revertToIncoming": true}); r.OK || r.Error != ErrReadOnly {
		t.Fatalf("revert must be refused in read-only mode: %+v", r)
	}
	cfgResp := call("get_config", nil)
	if rules := cfgResp.Result.(config.Config).Rules; len(rules) != 1 {
		t.Fatalf("refused delete must keep the rule, got %+v", rules)
	}

	// Writable mode allows moves for triageAccountIds ("personal" included) while rejecting disabled accounts ("private").
	if r := call("save_config", map[string]any{"mailbox": map[string]any{"readOnly": false}}); !r.OK {
		t.Fatal(r.Error)
	}
	if r := call("record_moves", map[string]any{"moves": []map[string]any{{"messageId": "3", "accountId": "personal", "ruleId": "acme"}}}); !r.OK {
		t.Fatalf("record_moves should allow enabled non-kanban account: %+v", r)
	}
	if r := call("record_moves", map[string]any{"moves": []map[string]any{{"messageId": "2", "accountId": "private", "ruleId": "acme"}}}); r.OK || r.Error != ErrAccountNotEnabled {
		t.Fatalf("record_moves must reject other accounts: %+v", r)
	}

	// Assistant can be switched off independently.
	if r := call("save_config", map[string]any{"features": map[string]any{"assistant": false}}); !r.OK {
		t.Fatal(r.Error)
	}
	if r := call("analyze_thread", map[string]any{"mode": "summarize"}); r.OK || r.Error != ErrAssistantDisabled {
		t.Fatalf("analyze_thread must be refused while the assistant is off: %+v", r)
	}
	if cfg := call("get_config", nil).Result.(config.Config); !cfg.Features.Triage || cfg.Features.Assistant || !cfg.Features.Theme {
		t.Fatalf("feature patches must not reset other flags: %+v", cfg.Features)
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

func TestClassifyRespectsAISwitchAndBudget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	h := NewHandler(triage.NewStoreAt(t.TempDir()))
	call := func(method string, params any) Response {
		raw, _ := json.Marshal(params)
		return h.Handle(context.Background(), Request{ID: method, Method: method, Params: raw})
	}
	mail := func(id string, unread bool) triage.MessageInput {
		return triage.MessageInput{ID: id, ThreadID: "<" + id + "@x>", AccountID: "work", Folder: "INBOX", From: "a@kunde.de", Subject: "Frage " + id, Unread: unread}
	}
	if r := call("save_config", map[string]any{
		"features": map[string]any{"triage": true},
		"mailbox":  map[string]any{"triageAccountIds": []string{"work"}, "kanbanAccountIds": []string{"work"}},
		"ai":       map[string]any{"classifyWithAi": true, "maxAiCallsPerScan": 3},
	}); !r.OK {
		t.Fatal(r.Error)
	}

	// One scan in two batches: the budget of 3 spans both. (No API key: each attempt fails fast.)
	r1 := call("classify_batch", map[string]any{"scanId": "s1", "messages": []triage.MessageInput{mail("1", false), mail("2", true)}}).Result.(map[string]any)
	r2 := call("classify_batch", map[string]any{"scanId": "s1", "messages": []triage.MessageInput{mail("3", true), mail("4", true), mail("5", true)}}).Result.(map[string]any)
	if r1["aiCalls"] != 2 || r2["aiCalls"] != 1 || r2["aiSkipped"] != 2 {
		t.Fatalf("budget not shared across batches: %v/%v then %v/%v", r1["aiCalls"], r1["aiSkipped"], r2["aiCalls"], r2["aiSkipped"])
	}
	// A new scan starts with a fresh budget.
	if r := call("classify_batch", map[string]any{"scanId": "s2", "messages": []triage.MessageInput{mail("6", true)}}).Result.(map[string]any); r["aiCalls"] != 1 {
		t.Fatalf("new scan should get a fresh budget: %v", r)
	}

	if r := call("save_config", map[string]any{"ai": map[string]any{"classifyWithAi": false}}); !r.OK {
		t.Fatal(r.Error)
	}
	off := call("classify_batch", map[string]any{"scanId": "s3", "messages": []triage.MessageInput{mail("7", true)}}).Result.(map[string]any)
	if off["aiCalls"] != 0 || off["classifyErrors"] != 0 || off["aiEnabled"] != false {
		t.Fatalf("AI switch off must not call the classifier: %v", off)
	}
	if got := off["results"].([]triage.Classification); len(got) != 1 || got[0].Stage == "stage1-llm" {
		t.Fatalf("expected a local estimate, got %+v", got)
	}
}

func TestUndoScanMovesAndProtocol(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	h := NewHandler(triage.NewStoreAt(t.TempDir()))
	call := func(method string, params any) Response {
		raw, _ := json.Marshal(params)
		return h.Handle(context.Background(), Request{ID: method, Method: method, Params: raw})
	}
	ping := call("ping", nil).Result.(map[string]any)
	if ping["protocol"] != ProtocolVersion || ping["version"] != Version {
		t.Fatalf("ping must report version and protocol: %v", ping)
	}

	if r := call("undo_scan_moves", map[string]any{"scanId": "s1"}); r.OK || r.Error != ErrReadOnly {
		t.Fatalf("undo must be refused in read-only mode: %+v", r)
	}
	if r := call("save_config", map[string]any{"mailbox": map[string]any{"readOnly": false, "triageAccountIds": []string{"work"}}}); !r.OK {
		t.Fatal(r.Error)
	}
	moves := []map[string]any{
		{"messageId": "1", "accountId": "work", "ruleId": "r", "scanId": "s1"},
		{"messageId": "2", "accountId": "work", "ruleId": "r", "scanId": "s2"},
	}
	if r := call("record_moves", map[string]any{"moves": moves}); !r.OK {
		t.Fatal(r.Error)
	}
	got := call("undo_scan_moves", map[string]any{"scanId": "s1"}).Result.(map[string]any)["revertMoves"].([]triage.MovedMessage)
	if len(got) != 1 || got[0].MessageID != "1" {
		t.Fatalf("expected only the moves of scan s1, got %+v", got)
	}
	if again := call("undo_scan_moves", map[string]any{"scanId": "s1"}).Result.(map[string]any)["revertMoves"].([]triage.MovedMessage); len(again) != 0 {
		t.Fatalf("a scan can only be undone once, got %+v", again)
	}
	if empty := call("undo_scan_moves", map[string]any{"scanId": ""}).Result.(map[string]any)["revertMoves"].([]triage.MovedMessage); len(empty) != 0 {
		t.Fatalf("an empty scan id must never match older moves, got %+v", empty)
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

func TestHandlerSetThemeValidationAndSyncReverts(t *testing.T) {
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

	if _, err := store.SaveClassifications([]triage.Classification{
		{MessageID: "100", ThreadID: "<msg@acme.de>", AccountID: "work", Folder: "Spaces/Acme", FolderID: "f-acme"},
	}); err != nil {
		t.Fatal(err)
	}
	if r := call("sync_reverts", map[string]any{
		"moves": []map[string]any{
			{"messageId": "101", "oldMessageId": "100", "headerMessageId": "<msg@acme.de>", "originalFolder": "INBOX", "originalFolderId": "f-inbox"},
		},
	}); !r.OK {
		t.Fatalf("sync_reverts failed: %s", r.Error)
	}
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, oldExists := st.Classifications["100"]; oldExists {
		t.Fatalf("expected old numeric ID 100 migrated after revert")
	}
	if got := st.Classifications["101"]; got.Folder != "INBOX" || got.FolderID != "f-inbox" {
		t.Fatalf("expected reverted classification at ID 101 in INBOX, got %+v", got)
	}
}

func TestHandlerFilterDiagnosticsAndSpaceImports(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	h := NewHandler(triage.NewStoreAt(t.TempDir()))

	diagParams, _ := json.Marshal(map[string]any{
		"message": triage.MessageInput{
			AccountID: "acc3",
			Subject:   "Automated restore completed for Confluence in rewe",
			From:      "noreply@rewe.de",
		},
		"filters": []triage.NativeFilter{
			{
				AccountID: "acc3",
				Name:      "Inbox » REWE (Systemmails)",
				Enabled:   true,
				MatchAll:  true,
				Conditions: []triage.NativeCondition{
					{Field: "subject", Op: "contains", Value: "Automated restore completed for Confluence in rewe", BooleanAnd: true},
					{Field: "subject", Op: "contains", Value: "Automaterd restore started for Confluence in rewe", BooleanAnd: true},
				},
				Actions: []triage.NativeAction{{Type: "moveToFolder", TargetFolderPath: "INBOX/REWE"}},
			},
		},
	})
	diagResp := h.Handle(context.Background(), Request{ID: "df1", Method: "diagnose_filters", Params: diagParams})
	if !diagResp.OK {
		t.Fatalf("diagnose_filters failed: %s", diagResp.Error)
	}
	rep, ok := diagResp.Result.(triage.FilterDiagnosticReport)
	if !ok || len(rep.NearMisses) != 1 || rep.NearMisses[0].QuickFix != "set_or" {
		t.Fatalf("unexpected diagnose_filters result: %+v", diagResp.Result)
	}

	impParams, _ := json.Marshal(map[string]any{
		"accountId": "acc3",
		"filters": []triage.NativeFilter{
			{
				AccountID: "acc3",
				Name:      "Inbox » Börse",
				Enabled:   true,
				Conditions: []triage.NativeCondition{
					{Field: "from", Op: "endsWith", Value: "@boerse-stuttgart.de"},
				},
				Actions: []triage.NativeAction{{Type: "moveToFolder", TargetFolderPath: "INBOX/Boerse"}},
			},
		},
	})
	impResp := h.Handle(context.Background(), Request{ID: "ci1", Method: "suggest_space_imports", Params: impParams})
	if !impResp.OK {
		t.Fatalf("suggest_space_imports failed: %s", impResp.Error)
	}
	resMap := impResp.Result.(map[string]any)
	cands := resMap["candidates"].([]triage.SpaceImportCandidate)
	if len(cands) != 1 || cands[0].Space != "Boerse" {
		t.Fatalf("unexpected space import candidates: %+v", cands)
	}
}

func TestArchiveExportAndStatsRPC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)

	archiveRoot := filepath.Join(home, "my-archive")
	cfg := config.Default()
	cfg.Features.Triage = true
	cfg.Mailbox.ReadOnly = true
	cfg.Mailbox.TriageAccountIDs = []string{"personal"}
	cfg.Mailbox.KanbanAccountIDs = []string{} // non-Kanban enabled account may still use Archive/Cleanup
	cfg.Mailbox.ArchiveDir = archiveRoot
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	store, err := triage.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store)

	// Disabled account must be rejected.
	raw := []byte("From: a@b.de\r\nSubject: Old\r\n\r\nhi\r\n")
	b64 := base64.StdEncoding.EncodeToString(raw)
	deniedParams, _ := json.Marshal(archive.ExportRequest{
		AccountID:    "other",
		AccountName:  "other@example.com",
		FolderPath:   "INBOX",
		DateISO:      "2022-05-01T10:00:00Z",
		Subject:      "Old",
		MessageID:    "1",
		RawEMLBase64: b64,
	})
	if resp := h.Handle(context.Background(), Request{ID: "a1", Method: "archive_export", Params: deniedParams}); resp.OK || resp.Error != ErrAccountNotEnabled {
		t.Fatalf("expected account_not_enabled for unenabled account, got: %+v", resp)
	}

	// Enabled non-Kanban account succeeds even in ReadOnly mode (export-only never mutates mailbox).
	okParams, _ := json.Marshal(archive.ExportRequest{
		AccountID:    "personal",
		AccountName:  "mail@frankhess.de",
		FolderPath:   "INBOX",
		DateISO:      "2022-05-01T10:00:00Z",
		Subject:      "Old Invoice",
		MessageID:    "2",
		RawEMLBase64: b64,
	})
	resp := h.Handle(context.Background(), Request{ID: "a2", Method: "archive_export", Params: okParams})
	if !resp.OK {
		t.Fatalf("archive_export failed: %s", resp.Error)
	}
	res := resp.Result.(archive.ExportResult)
	if !res.Verified || res.BytesWritten != int64(len(raw)) {
		t.Fatalf("expected verified write: %+v", res)
	}

	statsResp := h.Handle(context.Background(), Request{ID: "a3", Method: "archive_stats"})
	if !statsResp.OK {
		t.Fatalf("archive_stats failed: %s", statsResp.Error)
	}
	stats := statsResp.Result.(archive.ArchiveStats)
	if !stats.Exists || stats.FileCount != 1 || stats.TotalBytes != int64(len(raw)) {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}


