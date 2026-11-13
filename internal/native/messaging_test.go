package native

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/fhess/omathunder/internal/config"
	"github.com/fhess/omathunder/internal/triage"
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
		"mailbox":  map[string]any{"readOnly": false, "triageAccountIds": []string{"acc1"}},
	})
	if r := h.Handle(context.Background(), Request{ID: "e1", Method: "save_config", Params: enable}); !r.OK {
		t.Fatalf("enable triage failed: %s", r.Error)
	}

	rulesParams, _ := json.Marshal(map[string]any{
		"incomingFolder": "INBOX",
		"rules": []map[string]any{
			{
				"id":            "rule-acme",
				"customer":      "Acme GmbH",
				"folderPath":    "Kunden/Acme/Incoming",
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
			{"messageId": "m10", "accountId": "acc1", "ruleId": "rule-acme", "originalFolder": "INBOX", "targetFolder": "Kunden/Acme/Incoming"},
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
		"rules":          []map[string]any{{"id": "keep-me", "customer": "Keep GmbH", "folderPath": "Kunden/Keep", "domains": []string{"keep.de"}}},
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

	if r := call("save_config", map[string]any{"mailbox": map[string]any{"triageAccountIds": []string{"work"}}}); !r.OK {
		t.Fatal(r.Error)
	}
	if r := call("save_rules", map[string]any{"rules": []map[string]any{
		{"id": "acme", "customer": "Acme", "accountId": "work", "folderPath": "Kunden/Acme", "domains": []string{"acme.de"}, "autoMove": true},
	}}); !r.OK {
		t.Fatal(r.Error)
	}
	r := call("classify_batch", msgs)
	if !r.OK {
		t.Fatal(r.Error)
	}
	res := r.Result.(map[string]any)
	if got := len(res["results"].([]triage.Classification)); got != 1 {
		t.Fatalf("only the enabled account may be classified, got %d results", got)
	}
	if res["skippedAccounts"].(int) != 1 {
		t.Fatalf("expected 1 skipped mail, got %v", res["skippedAccounts"])
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

	// Writable mode still refuses moves for accounts that are not enabled.
	if r := call("save_config", map[string]any{"mailbox": map[string]any{"readOnly": false}}); !r.OK {
		t.Fatal(r.Error)
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
