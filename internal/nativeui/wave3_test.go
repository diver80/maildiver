package nativeui

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
)

func TestUnsubscribeTargetsAndMailtoPrefill(t *testing.T) {
	msg := &backend.MessageDetail{
		MessageHeader: backend.MessageHeader{AccountID: "acc-1", Subject: "Weekly Digest"},
		Headers: map[string]string{
			"list-unsubscribe":      "<mailto:leave@lists.example.org?subject=Bye&body=Remove>, <https://lists.example.org/unsub?id=42>",
			"list-unsubscribe-post": "List-Unsubscribe=One-Click",
		},
	}
	httpsURL, mailtoURI, oneClick := parseUnsubscribeTargets(msg)
	if httpsURL != "https://lists.example.org/unsub?id=42" || mailtoURI == "" || !oneClick {
		t.Fatalf("unexpected unsubscribe parse: https=%q mailto=%q oneClick=%v", httpsURL, mailtoURI, oneClick)
	}

	// Mailto-only header opens prefilled compose draft.
	cfg := config.Default()
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	mailtoMsg := &backend.MessageDetail{
		MessageHeader: backend.MessageHeader{AccountID: "acc-1"},
		Headers: map[string]string{
			"list-unsubscribe": "<mailto:unsub@example.org?subject=Unsub&body=Please%20remove>",
		},
	}
	app.PerformUnsubscribe(context.Background(), mailtoMsg)
	if !app.composeOpen || app.composeTo != "unsub@example.org" || app.composeSubject != "Unsub" || app.composeBody != "Please remove" {
		t.Fatalf("unexpected mailto compose prefill: open=%v to=%q subj=%q body=%q", app.composeOpen, app.composeTo, app.composeSubject, app.composeBody)
	}
}

func TestLastRecipientTokenHandlesQuotedCommas(t *testing.T) {
	got := lastRecipientToken(`"Mustermann, Max" <max@example.org>`)
	if got != `"Mustermann, Max" <max@example.org>` {
		t.Fatalf("quoted comma must stay part of the single recipient token, got %q", got)
	}
	got2 := lastRecipientToken(`"Mustermann, Max" <max@example.org>, erik`)
	if got2 != "erik" {
		t.Fatalf("expected second token erik, got %q", got2)
	}
}

func TestCalendarRemindersSkipHiddenCalendars(t *testing.T) {
	cfg := config.Default()
	cfg.Language = "de"
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	soon := time.Now().Add(5 * time.Minute).Format(time.RFC3339)
	app.hiddenCalendars = map[string]bool{"cal-hidden": true}
	app.events = []backend.CalendarEvent{
		{ID: "ev-hidden", CalendarID: "cal-hidden", Title: "Muted Meeting", Start: soon},
		{ID: "ev-vis", CalendarID: "cal-vis", Title: "Visible Standup", Start: soon},
	}
	app.checkUpcomingMeetingReminders()
	if len(app.recentAlerts) != 1 || app.recentAlerts[0].Title != "Visible Standup" {
		t.Fatalf("expected only visible calendar alert, got %+v", app.recentAlerts)
	}
}

func TestDesktopLayoutPersistenceRoundTrip(t *testing.T) {
	var mu sync.Mutex
	cfg := config.Default()
	saved := make(chan config.Config, 1)
	app := NewApp(Options{
		ConfigLoader: func() (config.Config, error) {
			mu.Lock()
			defer mu.Unlock()
			return cfg, nil
		},
		ConfigSaver: func(next config.Config) error {
			mu.Lock()
			cfg = next
			mu.Unlock()
			select {
			case saved <- next:
			default:
			}
			return nil
		},
	})
	app.hideSidebar = true
	app.showCopilot = false
	app.notifyEnabled = false
	app.sidebarSplit = 275
	app.mailSplit = 420
	app.copilotSplit = 590
	app.persistLayoutPrefs()

	var loadedCfg config.Config
	select {
	case loadedCfg = <-saved:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ConfigSaver")
	}

	app2 := NewApp(Options{
		ConfigLoader: func() (config.Config, error) { return loadedCfg, nil },
	})
	if !app2.hideSidebar || app2.showCopilot || app2.notifyEnabled || app2.sidebarSplit != 275 || app2.mailSplit != 420 || app2.copilotSplit != 590 {
		t.Fatalf("restored layout mismatch: hideSidebar=%v showCopilot=%v notify=%v splits=(%.0f,%.0f,%.0f)",
			app2.hideSidebar, app2.showCopilot, app2.notifyEnabled, app2.sidebarSplit, app2.mailSplit, app2.copilotSplit)
	}
}
