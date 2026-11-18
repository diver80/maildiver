package nativeui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/omathunder/internal/ai"
	"github.com/diver80/omathunder/internal/bridgeclient"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/copilot"
)

// Pending action states.
const (
	actionPending   = "pending"
	actionRunning   = "running"
	actionDone      = "done"
	actionDiscarded = "discarded"
	actionFailed    = "error"
)

// pendingAction is a write action proposed by the assistant, shown as a confirmation card.
// It runs only when the user clicks "Ausführen / Execute".
type pendingAction struct {
	Action        copilot.PendingAction
	Status        string
	Result        string
	Err           string
	CalendarLabel string // calendar picked on the card (event proposals)
}

// copilotState holds the AI assistant panel and provider/model selection.
type copilotState struct {
	providers      []ai.ProviderDescriptor
	providerLabels []string
	backendByLabel map[string]string
	providerLabel  string
	agentBackend   string // canonical backend id; the endpoint always comes from config.json
	agentModel     string
	modelChoices   []string
	modelsLoading  bool
	modelsError    string
	modelsSeq      uint64

	chatEntries    []ChatEntry
	chatInput      string
	chatStreaming  bool
	chatCancel     context.CancelFunc
	chatSeq        int
	chatBusy       atomic.Bool // guards against a second prompt while a run is active
	pendingActions map[string]*pendingAction
}

func (a *App) initProvidersFromStatic() {
	a.providers = ai.SupportedProviders()
	a.backendByLabel = make(map[string]string, len(a.providers))
	a.providerLabels = make([]string, 0, len(a.providers))
	want := ai.CanonicalBackend(a.agentBackend)
	for _, p := range a.providers {
		lbl := p.ID
		if p.Kind != "" {
			lbl = p.ID + " (" + p.Kind + ")"
		}
		a.providerLabels = append(a.providerLabels, lbl)
		a.backendByLabel[lbl] = p.ID
		if p.ID == want {
			a.providerLabel = lbl
			a.agentBackend = p.ID
		}
	}
	if a.providerLabel == "" && len(a.providerLabels) > 0 {
		a.providerLabel = a.providerLabels[0]
		a.agentBackend = a.backendByLabel[a.providerLabel]
	}
}

// selectProvider switches the assistant backend (UI thread). Only the backend id changes;
// endpoint and credentials are resolved from config.json by the host (contract §8).
func (a *App) selectProvider(ctx context.Context, label string) {
	backend := a.backendByLabel[label]
	if backend == "" || backend == a.agentBackend {
		return
	}
	a.providerLabel = label
	a.agentBackend = backend
	a.agentModel = ""
	a.modelChoices = nil
	a.LoadModelsAsync(ctx)
}

// LoadModelsAsync reloads the model list for the selected backend (UI thread).
func (a *App) LoadModelsAsync(ctx context.Context) {
	a.startModelLoad(ctx)
}

// startModelLoad lists the models of a.agentBackend (UI thread). Errors are shown in the panel.
func (a *App) startModelLoad(ctx context.Context) {
	backend := a.agentBackend
	if backend == "" {
		return
	}
	a.modelsSeq++
	seq := a.modelsSeq
	a.modelsLoading = true
	a.modelsError = ""
	bc, _ := a.client()
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var models []anyllm.Model
		var err error
		if bc != nil {
			res, e := bc.AIProviders(c, true, backend)
			switch {
			case e != nil:
				err = e
			case res.ModelsError != "":
				err = errors.New(res.ModelsError)
			}
			models = res.Models
		} else {
			cfg, cerr := a.opts.ConfigLoader()
			if cerr != nil {
				cfg = config.Default()
			}
			models, err = ai.ListModels(c, ai.ProviderForBackend(cfg.AI, backend), a.opts.AnyLLMOpts...)
		}
		var names []string
		for _, m := range models {
			if m.ID != "" {
				names = append(names, m.ID)
			}
		}
		a.emitUpdate(func() {
			if seq != a.modelsSeq {
				return
			}
			a.modelsLoading = false
			a.modelChoices = names
			if err != nil {
				a.modelsError = a.errText(err)
			}
			if a.agentModel == "" && len(names) > 0 {
				a.agentModel = names[0]
			}
		})
	}()
}

// SendCopilotPrompt starts an assistant run (UI thread). A second prompt is ignored while a
// run is active.
func (a *App) SendCopilotPrompt(ctx context.Context, prompt string) {
	text := strings.TrimSpace(prompt)
	if text == "" || a.chatStreaming || !a.chatBusy.CompareAndSwap(false, true) {
		return
	}
	a.chatSeq++
	userID := fmt.Sprintf("u-%d", a.chatSeq)
	asstID := fmt.Sprintf("a-%d", a.chatSeq)
	lang := a.uiLang()

	runCtx, cancel := context.WithCancel(ctx)
	history := make([]copilot.Message, 0, len(a.chatEntries)+1)
	for _, e := range a.chatEntries {
		if strings.TrimSpace(e.Content) == "" {
			continue
		}
		history = append(history, copilot.Message{Role: e.Role, Content: e.Content})
	}
	history = append(history, copilot.Message{Role: "user", Content: text})

	a.showCopilot = true
	a.chatInput = ""
	a.chatStreaming = true
	a.chatCancel = cancel
	a.chatEntries = append(a.chatEntries,
		ChatEntry{ID: userID, Role: "user", Content: text},
		ChatEntry{ID: asstID, Role: "assistant", Status: trLang(lang, "Starte KI-Assistent…", "Starting AI Assistant…"), Streaming: true},
	)

	req := copilot.RunRequest{
		Backend:           a.agentBackend,
		Model:             a.agentModel,
		Language:          lang,
		AccountID:         a.selectedAccount,
		SelectedMessageID: a.selectedID,
		Messages:          history,
	}
	runner := a.copilotRunner
	go func() {
		defer cancel()
		_ = runner.Run(runCtx, req, func(ev copilot.Event) {
			a.emitUpdate(func() { a.applyCopilotEvent(asstID, lang, ev) })
		})
		a.emitUpdate(func() {
			a.chatStreaming = false
			a.chatCancel = nil
			if e := a.chatEntry(asstID); e != nil && e.Streaming {
				e.Streaming = false
				e.Status = ""
			}
			a.chatBusy.Store(false)
		})
	}()
}

func (a *App) chatEntry(id string) *ChatEntry {
	for i := len(a.chatEntries) - 1; i >= 0; i-- {
		if a.chatEntries[i].ID == id {
			return &a.chatEntries[i]
		}
	}
	return nil
}

// applyCopilotEvent folds one runner event into the chat (UI thread).
func (a *App) applyCopilotEvent(asstID, lang string, ev copilot.Event) {
	entry := a.chatEntry(asstID)
	if entry == nil {
		return // chat was cleared
	}
	switch ev.Kind {
	case copilot.EventStatus:
		entry.Status = ev.Text
	case copilot.EventToolCall:
		entry.Status = trLang(lang, "Werkzeug: ", "Tool: ") + ev.ToolName
		entry.Tools = append(entry.Tools, ToolTrace{ID: ev.ToolID, Name: ev.ToolName})
	case copilot.EventToolResult:
		for t := len(entry.Tools) - 1; t >= 0; t-- {
			if entry.Tools[t].ID == ev.ToolID || (entry.Tools[t].Name == ev.ToolName && !entry.Tools[t].Done) {
				entry.Tools[t].Summary = ev.Text
				entry.Tools[t].Done = true
				break
			}
		}
	case copilot.EventProposal:
		if ev.Proposal == nil || ev.Proposal.ID == "" {
			return
		}
		if a.pendingActions == nil {
			a.pendingActions = make(map[string]*pendingAction)
		}
		pa := &pendingAction{Action: *ev.Proposal, Status: actionPending}
		if ev.Proposal.Kind == copilot.ActionCalendarEvent {
			pa.CalendarLabel = a.eventCalendar
			if pa.CalendarLabel == "" && len(a.eventCalLabels) > 0 {
				pa.CalendarLabel = a.eventCalLabels[0]
			}
		}
		a.pendingActions[ev.Proposal.ID] = pa
		entry.ActionIDs = append(entry.ActionIDs, ev.Proposal.ID)
	case copilot.EventDelta:
		entry.Status = ""
		entry.Content += ev.Text
	case copilot.EventError:
		entry.Streaming = false
		entry.Status = ""
		entry.Error = a.errText(errors.New(ev.Error))
		if entry.Error == "" {
			entry.Error = ev.Error
		}
	case copilot.EventDone:
		entry.Streaming = false
		entry.Status = ""
		if strings.TrimSpace(entry.Content) == "" && entry.Error == "" {
			entry.Content = trLang(lang, "_Keine Textantwort erhalten._", "_No text response received._")
		}
	}
}

// ClearChat empties the assistant history (UI thread). Running actions finish silently.
func (a *App) ClearChat() {
	if a.chatStreaming {
		return
	}
	a.chatEntries = nil
	a.pendingActions = make(map[string]*pendingAction)
	a.widgets.mdChat = nil
}

// ExecuteCopilotAction runs a confirmed proposal (UI thread, explicit user click only).
func (a *App) ExecuteCopilotAction(ctx context.Context, id string) {
	pa := a.pendingActions[id]
	if pa == nil || (pa.Status != actionPending && pa.Status != actionFailed) {
		return
	}
	if a.readOnly {
		pa.Status = actionFailed
		pa.Err = a.tr("Read-Only-Modus aktiv — Aktion blockiert", "Read-only mode active — action blocked")
		return
	}
	bc, err := a.client()
	if err != nil {
		pa.Status = actionFailed
		pa.Err = a.errText(err)
		return
	}
	opts := copilot.ExecuteOptions{}
	if pa.Action.Kind == copilot.ActionCalendarEvent {
		opts.CalendarID = a.eventCalByLabel[pa.CalendarLabel]
		if opts.CalendarID == "" && len(a.eventCalLabels) > 0 {
			pa.Status = actionFailed
			pa.Err = a.tr("Bitte einen Kalender wählen.", "Please pick a calendar.")
			return
		}
	}
	pa.Status = actionRunning
	pa.Err = ""
	action := pa.Action
	go func() {
		c, cancel := context.WithTimeout(ctx, 90*time.Second)
		res, err := copilot.ExecuteAction(c, bc, action, opts)
		cancel()
		a.emitUpdate(func() {
			p := a.pendingActions[id]
			if p == nil {
				return
			}
			if err != nil && res.Applied == 0 {
				p.Status = actionFailed
				p.Err = a.errText(err)
				return
			}
			p.Status = actionDone
			switch action.Kind {
			case copilot.ActionDraft:
				p.Result = a.tr("Entwurf in Thunderbird gespeichert (nicht gesendet).", "Draft saved in Thunderbird (not sent).")
			case copilot.ActionCalendarEvent:
				p.Result = a.trf("Termin „%s“ angelegt.", "Created event \"%s\".", res.Title)
				a.startRefresh(ctx)
			case copilot.ActionMarkRead:
				if err != nil {
					p.Result = a.trf("%d von %d angewendet, %d Fehler: %s", "%d of %d applied, %d failed: %s",
						res.Applied, res.Total, res.Total-res.Applied, a.errText(err))
				} else {
					p.Result = a.trf("%d Mail(s) aktualisiert.", "%d email(s) updated.", res.Applied)
				}
				a.startMessageLoad(ctx)
			}
			kind := "success"
			if err != nil {
				kind = "warning"
			}
			a.toast(p.Result, kind)
		})
	}()
}

// DiscardCopilotAction drops a proposal without executing it (UI thread).
func (a *App) DiscardCopilotAction(id string) {
	if pa := a.pendingActions[id]; pa != nil && (pa.Status == actionPending || pa.Status == actionFailed) {
		pa.Status = actionDiscarded
	}
}

// OpenDayBriefing shows the main window and starts the daily briefing prompt. Safe from any
// goroutine (it posts itself to the UI thread).
func (a *App) OpenDayBriefing(ctx context.Context) {
	a.emitUpdate(func() {
		if a.onShowMain != nil {
			a.onShowMain()
		}
		prompt := a.tr(
			"Gib mir mein strukturiertes Tages-Briefing: (1) Wichtige ungelesene Mails & offene Action-Items, (2) Heutige & morgige Kalendereinträge mit Zeitkonflikten/Vorbereitungsbedarf, (3) Konkrete Empfehlung für meine nächsten 3 Schritte.",
			"Give me my structured daily briefing: (1) Important unread emails & open action items, (2) Today's & tomorrow's calendar events including schedule conflicts or prep needs, (3) Concrete recommendation for my next 3 steps.",
		)
		a.SendCopilotPrompt(ctx, prompt)
	})
}

// actionReadOnlyHint is shown on proposal cards when writes are impossible.
func (a *App) actionBlockedReason() string {
	if a.readOnly {
		return a.tr("Read-Only-Modus aktiv: Ausführen ist gesperrt.", "Read-only mode active: execution is disabled.")
	}
	if _, err := a.client(); err != nil {
		return localizeError(bridgeclient.ErrOffline, a.uiLang())
	}
	return ""
}
