package nativeui

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/diver80/maildiver/internal/backend"
)

const (
	reloadDebounce = 500 * time.Millisecond
	reloadMaxDelay = 2 * time.Second
)

// client returns the in-process backend (mail engine, AI layer, jobs).
func (a *App) client() (*backend.Client, error) {
	if a.opts.Backend == nil {
		return nil, backend.ErrOffline
	}
	return a.opts.Backend, nil
}

// liveClient returns the backend or nil (for the copilot runner).
func (a *App) liveClient() *backend.Client {
	return a.opts.Backend
}

// runEventStream follows the engine's events until ctx ends.
func (a *App) runEventStream(ctx context.Context) {
	bc, err := a.client()
	if err != nil {
		return
	}
	id, ch := bc.Subscribe()
	if ch == nil {
		return
	}
	defer bc.Unsubscribe(id)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			a.handleEngineEvent(ctx, ev.Event, ev.Data)
		}
	}
}

// handleEngineEvent reacts to one engine event. Safe from any goroutine.
func (a *App) handleEngineEvent(ctx context.Context, name string, payload json.RawMessage) {
	switch name {
	case "mail.new":
		a.handleNewMail(ctx, payload)
	case "mail.sync":
		var p struct {
			AccountID string `json:"accountId"`
		}
		_ = json.Unmarshal(payload, &p)
		a.emitUpdate(func() { delete(a.syncErrors, p.AccountID) })
		a.msgReload.trigger(func() { a.RefreshAll(ctx) })
	case "mail.sync.error":
		var p struct {
			AccountID string `json:"accountId"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(payload, &p) == nil && p.AccountID != "" {
			a.emitUpdate(func() { a.syncErrors[p.AccountID] = p.Error })
		}
	case "mail.updated":
		a.msgReload.trigger(func() { a.LoadMessages(ctx) })
	case "calendar.changed":
		a.calReload.trigger(func() { a.RefreshAll(ctx) })
	case "outbox.changed":
		a.LoadOutbox(ctx)
	default:
		a.handleJobEvent(ctx, name, payload)
	}
}

type newMailPayload struct {
	AccountID string                  `json:"accountId"`
	FolderID  string                  `json:"folderId"`
	Folder    string                  `json:"folder"`
	Count     int                     `json:"count"`
	Messages  []backend.MessageHeader `json:"messages"`
}

// handleNewMail takes a mail.new payload. Safe from any goroutine.
func (a *App) handleNewMail(ctx context.Context, payload json.RawMessage) {
	var p newMailPayload
	_ = json.Unmarshal(payload, &p)
	if len(p.Messages) > 0 {
		a.emitUpdate(func() { a.notifyNewMail(p.Messages) })
	}
	a.msgReload.trigger(func() { a.LoadMessages(ctx) })
}

// notifyNewMail raises desktop alerts for unseen mails (UI thread).
func (a *App) notifyNewMail(msgs []backend.MessageHeader) {
	var fresh []backend.MessageHeader
	for _, m := range msgs {
		if m.ID != "" && !a.seenMails[m.ID] {
			a.seenMails[m.ID] = true
			fresh = append(fresh, m)
		}
	}
	if !a.notifyEnabled || len(fresh) == 0 {
		return
	}
	if len(fresh) == 1 {
		m := fresh[0]
		a.pushAlert(AlertItem{
			ID:        alertMailPrefix + m.ID,
			Kind:      "mail",
			Title:     shortSender(m.From),
			Subtitle:  a.tr("Neue E-Mail", "New Email"),
			Body:      m.Subject,
			MessageID: m.ID,
			CreatedAt: time.Now(),
		})
		return
	}
	a.pushAlert(AlertItem{
		ID:        fmt.Sprintf("%s%d", alertMailBatchPrefix, time.Now().UnixNano()),
		Kind:      "mail",
		Title:     a.trf("%d neue E-Mails", "%d new emails", len(fresh)),
		Subtitle:  shortSender(fresh[0].From),
		Body:      fresh[0].Subject,
		CreatedAt: time.Now(),
	})
}

// debouncer coalesces bursts of engine events into one reload (trailing edge, max delay bound).
type debouncer struct {
	delay, maxDelay time.Duration

	mu    sync.Mutex
	timer *time.Timer
	first time.Time
}

func (d *debouncer) trigger(fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	if d.timer != nil {
		if now.Sub(d.first) >= d.maxDelay {
			return // keep the already scheduled run so bursts cannot starve the reload
		}
		d.timer.Stop()
	} else {
		d.first = now
	}
	d.timer = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		d.timer = nil
		d.mu.Unlock()
		fn()
	})
}

func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}
