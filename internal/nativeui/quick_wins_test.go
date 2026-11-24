package nativeui

import (
	"testing"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/triage"
)

func TestPersistentSenderImageAllowlist(t *testing.T) {
	saved := config.Default()
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return saved, nil },
		ConfigSaver: func(next config.Config) error {
			saved = next
			return nil
		},
	})

	msg := &backend.MessageDetail{
		ID:       "img-1",
		From:     "Newsletter Team <News@Example.ORG>",
		BodyHtml: `<p>Hi</p><img src="https://cdn.example.org/banner.png">`,
	}
	if app.readerRemoteImagesAllowedForMsg(msg) {
		t.Fatal("remote images must be blocked by default")
	}

	app.SetSenderRemoteImagesAllowed(msg.From, true)
	if len(saved.App.AllowedImageSenders) != 1 || saved.App.AllowedImageSenders[0] != "news@example.org" {
		t.Fatalf("expected normalized lowercase sender persisted, got %+v", saved.App.AllowedImageSenders)
	}
	if !app.readerRemoteImagesAllowedForMsg(msg) {
		t.Fatal("remote images must be allowed automatically for allowlisted sender")
	}

	// Revoking removes the sender cleanly.
	app.SetSenderRemoteImagesAllowed(msg.From, false)
	app.setReaderRemoteImagesAllowed(msg.ID, false)
	if len(saved.App.AllowedImageSenders) != 0 {
		t.Fatalf("expected allowlist emptied after revoke, got %+v", saved.App.AllowedImageSenders)
	}
	if app.readerRemoteImagesAllowedForMsg(msg) {
		t.Fatal("remote images must be blocked again after revoking sender")
	}
}

func TestRuleEditorBuildRequestAndEditExisting(t *testing.T) {
	app := NewApp(Options{})
	app.accounts = []backend.Account{{ID: "acc-1", Name: "Work"}}

	existing := &triage.NativeFilter{
		AccountID: "acc-1",
		Name:      "Atlassian Noise",
		Enabled:   true,
		MatchAll:  true,
		Conditions: []triage.NativeCondition{
			{Field: "from", Op: "contains", Value: "atlassian.net", BooleanAnd: true},
		},
		Actions: []triage.NativeAction{
			{Type: "markRead"},
			{Type: "moveToFolder", TargetFolderPath: "INBOX/atlassian/done"},
		},
	}

	app.openRuleEditor(existing)
	if !app.direct.ruleEditOpen || app.direct.ruleEditName != "Atlassian Noise" || !app.direct.ruleEditMove || app.direct.ruleEditMoveFolder != "INBOX/atlassian/done" {
		t.Fatalf("rule editor did not load existing rule: %+v", app.direct)
	}

	req, err := app.buildRuleEditorRequest()
	if err != nil {
		t.Fatalf("buildRuleEditorRequest failed: %v", err)
	}
	if req.Mode != "update" || req.AccountID != "acc-1" || len(req.Actions) != 2 {
		t.Fatalf("unexpected upsert request: %+v", req)
	}

	// Creating a brand new rule switches to mode "create".
	app.openRuleEditor(nil)
	app.direct.ruleEditName = "Invoices"
	app.direct.ruleEditConds[0].Value = "billing@vendor.com"
	app.direct.ruleEditMoveFolder = "INBOX/Invoices"
	req2, err := app.buildRuleEditorRequest()
	if err != nil {
		t.Fatalf("new rule request failed: %v", err)
	}
	if req2.Mode != "create" || req2.Name != "Invoices" {
		t.Fatalf("unexpected create request: %+v", req2)
	}
}
