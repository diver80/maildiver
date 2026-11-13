package native

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/fhess/omathunder/internal/ai"
	"github.com/fhess/omathunder/internal/config"
	"github.com/fhess/omathunder/internal/theme"
	"github.com/fhess/omathunder/internal/triage"
)

type Request struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID     string `json:"id,omitempty"`
	Event  string `json:"event,omitempty"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Version is the host build version (set by main from the linker flag).
var Version = "dev"

// ProtocolVersion changes whenever the messages between extension and host change incompatibly.
// The extension compares it with its own value and asks the user to update the older side.
const ProtocolVersion = 3

// Stable error codes the extension translates for the UI.
const (
	ErrTriageDisabled    = "feature_disabled:triage"
	ErrAssistantDisabled = "feature_disabled:assistant"
	ErrReadOnly          = "read_only"
	ErrNoTriageAccounts  = "no_triage_accounts"
	ErrAccountNotEnabled = "account_not_enabled"
)

// Upper bounds for one model call, so a hanging CLI or API cannot block a scan forever.
// They stay below the extension's own request timeouts (5 mails per batch, 300 s; analyze 120 s).
const (
	classifyTimeout = 50 * time.Second
	analyzeTimeout  = 110 * time.Second
)

func fail(id, msg string) Response {
	return Response{ID: id, OK: false, Error: msg}
}

type DoctorReport struct {
	OS              string               `json:"os"`
	Arch            string               `json:"arch"`
	OmarchyDetected bool                 `json:"omarchyDetected"`
	OmarchyPath     string               `json:"omarchyPath"`
	ActiveTheme     string               `json:"activeTheme"`
	ThemeSource     string               `json:"themeSource"`
	CLIs            map[string]bool      `json:"clis"`
	EnvKeys         map[string]bool      `json:"envKeys"`
	ManifestPaths   []string             `json:"manifestPaths"`
	HostVersion     string               `json:"hostVersion"`
	Protocol        int                  `json:"protocol"`
	EnvFile         string               `json:"envFile"`
	Features        config.FeatureFlags  `json:"features"`
	Mailbox         config.MailboxPolicy `json:"mailbox"`
}

type Handler struct {
	store *triage.Store
	// cfgMu serializes load-modify-save cycles on the config file; requests run concurrently.
	cfgMu sync.Mutex
	// AI call budget of the scan in progress (a scan arrives as several classify_batch calls).
	scanMu    sync.Mutex
	scanID    string
	scanCalls int
}

// takeAICall reserves one classifier call for the scan, or reports that the budget is used up.
func (h *Handler) takeAICall(scanID string, limit int) bool {
	h.scanMu.Lock()
	defer h.scanMu.Unlock()
	if scanID == "" || scanID != h.scanID {
		h.scanID = scanID
		h.scanCalls = 0
	}
	if h.scanCalls >= limit {
		return false
	}
	h.scanCalls++
	return true
}

func NewHandler(store *triage.Store) *Handler {
	return &Handler{store: store}
}

func BuildDoctorReport() DoctorReport {
	cfg, _ := config.Load()
	pal, src := theme.ResolveActive(cfg.Theme)
	omPath := theme.OmarchyColorsPath()
	_, omErr := os.Stat(omPath)
	paths, _ := ManifestPaths()

	clis := map[string]bool{}
	for _, bin := range []string{"claude", "gemini", "opencode", "codex", "ollama"} {
		_, err := exec.LookPath(bin)
		clis[bin] = err == nil
	}
	envKeys := map[string]bool{
		"GEMINI_API_KEY":    os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_API_KEY") != "",
		"ANTHROPIC_API_KEY": os.Getenv("ANTHROPIC_API_KEY") != "",
	}
	return DoctorReport{
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		OmarchyDetected: omErr == nil,
		OmarchyPath:     omPath,
		ActiveTheme:     pal.Name,
		ThemeSource:     src,
		CLIs:            clis,
		EnvKeys:         envKeys,
		ManifestPaths:   paths,
		HostVersion:     Version,
		Protocol:        ProtocolVersion,
		EnvFile:         EnvFilePath(),
		Features:        cfg.Features,
		Mailbox:         cfg.Mailbox,
	}
}

// configWriters are the methods that change the config file.
var configWriters = map[string]bool{"save_config": true, "save_rules": true, "delete_rule": true, "set_theme": true}

func (h *Handler) Handle(ctx context.Context, req Request) Response {
	if configWriters[req.Method] {
		h.cfgMu.Lock()
		defer h.cfgMu.Unlock()
	}
	switch req.Method {
	case "ping":
		return Response{ID: req.ID, OK: true, Result: map[string]any{"status": "pong", "version": Version, "protocol": ProtocolVersion}}

	case "doctor":
		return Response{ID: req.ID, OK: true, Result: BuildDoctorReport()}

	case "get_config":
		cfg, err := config.Load()
		if err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		return Response{ID: req.ID, OK: true, Result: cfg}

	case "save_config":
		existing, err := config.Load()
		if err != nil {
			return fail(req.ID, "config unreadable, refusing to overwrite: "+err.Error())
		}
		next, err := config.ApplyPatch(existing, req.Params)
		if err != nil {
			return fail(req.ID, err.Error())
		}
		if err := config.Save(next); err != nil {
			return fail(req.ID, err.Error())
		}
		pal, src := theme.ResolveActive(next.Theme)
		return Response{ID: req.ID, OK: true, Result: map[string]any{
			"config": next,
			"theme":  theme.BuildPayload(pal, src),
		}}

	case "save_rules":
		var p struct {
			Rules []config.RoutingRule `json:"rules"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		cfg, _ := config.Load()
		if p.Rules != nil {
			cfg.Rules = p.Rules
		}
		if err := config.Save(cfg); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		return Response{ID: req.ID, OK: true, Result: cfg}

	case "delete_rule":
		var p struct {
			RuleID           string `json:"ruleId"`
			RevertToIncoming bool   `json:"revertToIncoming"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		cfg, _ := config.Load()
		if p.RevertToIncoming && cfg.Mailbox.ReadOnly {
			return fail(req.ID, ErrReadOnly)
		}
		var deletedRule *config.RoutingRule
		nextRules := make([]config.RoutingRule, 0, len(cfg.Rules))
		for _, r := range cfg.Rules {
			if r.ID == p.RuleID {
				cp := r
				deletedRule = &cp
				continue
			}
			nextRules = append(nextRules, r)
		}
		cfg.Rules = nextRules
		if err := config.Save(cfg); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		var revertMoves []triage.MovedMessage
		if p.RevertToIncoming {
			revertMoves, _, _ = h.store.PopMovedByRule(p.RuleID)
		}
		return Response{ID: req.ID, OK: true, Result: map[string]any{
			"config":      cfg,
			"deletedRule": deletedRule,
			"revertMoves": revertMoves,
		}}

	case "record_moves":
		var p struct {
			Moves []triage.MovedMessage `json:"moves"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		cfg, _ := config.Load()
		if cfg.Mailbox.ReadOnly {
			return fail(req.ID, ErrReadOnly)
		}
		for _, m := range p.Moves {
			if !cfg.Mailbox.AccountEnabled(m.AccountID) {
				return fail(req.ID, ErrAccountNotEnabled)
			}
		}
		st, err := h.store.RecordMoves(p.Moves)
		if err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		return Response{ID: req.ID, OK: true, Result: st}

	case "undo_scan_moves":
		var p struct {
			ScanID string `json:"scanId"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, err.Error())
		}
		if cfg, _ := config.Load(); cfg.Mailbox.ReadOnly {
			return fail(req.ID, ErrReadOnly)
		}
		moves, err := h.store.PopMovedByScan(p.ScanID)
		if err != nil {
			return fail(req.ID, err.Error())
		}
		return Response{ID: req.ID, OK: true, Result: map[string]any{"revertMoves": moves}}

	case "move_kanban":
		var p struct {
			MessageID string `json:"messageId"`
			Column    string `json:"column"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		if cfg, _ := config.Load(); !cfg.Features.Triage {
			return fail(req.ID, ErrTriageDisabled)
		}
		st, err := h.store.MoveKanban(p.MessageID, p.Column)
		if err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		return Response{ID: req.ID, OK: true, Result: map[string]any{
			"state":      st,
			"barSummary": triage.ComputeBarSummary(st),
		}}

	case "list_themes":
		return Response{ID: req.ID, OK: true, Result: theme.ListPresets()}

	case "get_theme":
		cfg, _ := config.Load()
		pal, src := theme.ResolveActive(cfg.Theme)
		return Response{ID: req.ID, OK: true, Result: theme.BuildPayload(pal, src)}

	case "set_theme":
		var p struct {
			Mode   string `json:"mode"`
			Preset string `json:"preset"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		cfg, _ := config.Load()
		if p.Mode != "" {
			cfg.Theme.Mode = p.Mode
		}
		if p.Preset != "" {
			cfg.Theme.Preset = p.Preset
			if p.Mode == "" {
				cfg.Theme.Mode = "preset"
			}
		}
		if err := config.Save(cfg); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		pal, src := theme.ResolveActive(cfg.Theme)
		return Response{ID: req.ID, OK: true, Result: theme.BuildPayload(pal, src)}

	case "get_state":
		st, err := h.store.Load()
		if err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		return Response{ID: req.ID, OK: true, Result: map[string]any{
			"state":      st,
			"barSummary": triage.ComputeBarSummary(st),
		}}

	case "classify_batch":
		var p struct {
			Force    bool                  `json:"force"`
			Language string                `json:"language"`
			ScanID   string                `json:"scanId"`
			Messages []triage.MessageInput `json:"messages"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		cfg, _ := config.Load()
		if !cfg.Features.Triage {
			return fail(req.ID, ErrTriageDisabled)
		}
		cfg.AI.Language = cfg.Language
		if p.Language == "de" || p.Language == "en" {
			cfg.AI.Language = p.Language
		}
		if len(cfg.Mailbox.TriageAccountIDs) == 0 {
			return fail(req.ID, ErrNoTriageAccounts)
		}
		// Defense in depth: never classify (or send to an LLM) mails of accounts not enabled for triage.
		allowed := make([]triage.MessageInput, 0, len(p.Messages))
		skipped := 0
		for _, m := range p.Messages {
			if cfg.Mailbox.AccountEnabled(m.AccountID) {
				allowed = append(allowed, m)
			} else {
				skipped++
			}
		}
		if _, err := h.store.RetainAccounts(cfg.Mailbox); err != nil {
			return fail(req.ID, err.Error())
		}
		prevState, _ := h.store.Load()
		byThread := map[string]triage.Classification{}
		for _, prev := range prevState.Classifications {
			if prev.ThreadID != "" {
				byThread[prev.ThreadID] = prev
			}
		}

		results := make([]triage.Classification, 0, len(allowed))
		classifyErrors := 0
		firstClassifyError := ""
		aiCalls, aiSkipped := 0, 0
		// Unread mails get the AI budget first.
		sort.SliceStable(allowed, func(i, j int) bool { return allowed[i].Unread && !allowed[j].Unread })
		for _, m := range allowed {
			if !p.Force {
				if base, matched := triage.EvaluateStage0(m, cfg.AI.SkipNewsletters, cfg.Rules); matched {
					results = append(results, base)
					continue
				} else {
					prev, ok := prevState.Classifications[m.ID]
					if !ok && m.ThreadID != "" {
						prev, ok = byThread[m.ThreadID]
					}
					if ok && prev.Stage == "stage1-llm" && prev.Subject == m.Subject {
						prev.MessageID = m.ID
						prev.AccountID = m.AccountID
						prev.Folder = m.Folder
						prev.FolderID = m.FolderID
						prev.Customer = base.Customer
						prev.MatchedRuleID = base.MatchedRuleID
						prev.TargetFolder = base.TargetFolder
						prev.TargetFolderID = base.TargetFolderID
						results = append(results, prev)
						continue
					}
				}
			}
			if !cfg.AI.ClassifyWithAI {
				results = append(results, ai.ClassifyLocal(cfg.AI, m, cfg.Rules, "sum_ai_off", "Lokale Schätzung (KI-Klassifikation ausgeschaltet)"))
				continue
			}
			if _, matched := triage.EvaluateStage0(m, cfg.AI.SkipNewsletters, cfg.Rules); !matched {
				if !h.takeAICall(p.ScanID, cfg.AI.MaxAICallsPerScan) {
					aiSkipped++
					results = append(results, ai.ClassifyLocal(cfg.AI, m, cfg.Rules, "sum_ai_limit", "Lokale Schätzung (KI-Limit pro Scan erreicht)"))
					continue
				}
				aiCalls++
			}
			callCtx, cancel := context.WithTimeout(ctx, classifyTimeout)
			c, err := ai.Classify(callCtx, cfg.AI, m, cfg.Rules)
			cancel()
			if err != nil {
				// The mail still gets a local heuristic result; report why the AI step failed.
				classifyErrors++
				if firstClassifyError == "" {
					firstClassifyError = err.Error()
				}
			}
			results = append(results, c)
		}
		st, err := h.store.SaveClassifications(results)
		if err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		return Response{ID: req.ID, OK: true, Result: map[string]any{
			"results":         results,
			"plannedMoves":    triage.PlanAutoMoves(allowed, cfg.Rules, cfg.Mailbox),
			"readOnly":        cfg.Mailbox.ReadOnly,
			"skippedAccounts": skipped,
			"aiCalls":         aiCalls,
			"aiSkipped":       aiSkipped,
			"aiEnabled":       cfg.AI.ClassifyWithAI,
			"classifyErrors":  classifyErrors,
			"classifyError":   firstClassifyError,
			"barSummary":      triage.ComputeBarSummary(st),
		}}

	case "analyze_thread":
		var analyzeReq ai.AnalyzeRequest
		if err := json.Unmarshal(req.Params, &analyzeReq); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		cfg, _ := config.Load()
		if !cfg.Features.Assistant {
			return fail(req.ID, ErrAssistantDisabled)
		}
		if analyzeReq.Language == "" && (cfg.Language == "de" || cfg.Language == "en") {
			analyzeReq.Language = cfg.Language
		}
		callCtx, cancel := context.WithTimeout(ctx, analyzeTimeout)
		resp, err := ai.Analyze(callCtx, cfg.AI, analyzeReq)
		cancel()
		if err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		if !analyzeReq.Ephemeral {
			for _, prop := range resp.Proposals {
				_, _ = h.store.AddProposal(prop)
			}
		}
		return Response{ID: req.ID, OK: true, Result: resp}

	case "update_proposal":
		var p struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		st, err := h.store.UpdateProposalStatus(p.ID, p.Status)
		if err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		return Response{ID: req.ID, OK: true, Result: map[string]any{
			"state":      st,
			"barSummary": triage.ComputeBarSummary(st),
		}}

	default:
		return Response{ID: req.ID, OK: false, Error: "unknown method: " + req.Method}
	}
}

// RunHost runs the stdio Native Messaging loop plus a lightweight Omarchy theme watcher.
// Every request is handled in its own goroutine, so a long AI call does not block the UI.
func RunHost(ctx context.Context, r io.Reader, w io.Writer) error {
	store, err := triage.OpenStore()
	if err != nil {
		return err
	}
	handler := NewHandler(store)
	conn := NewConn(r, w)

	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	go watchThemeChanges(watchCtx, conn)

	var pending sync.WaitGroup
	defer pending.Wait()
	for {
		var req Request
		if err := conn.Read(&req); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		pending.Add(1)
		go func(req Request) {
			defer pending.Done()
			resp := handler.Handle(ctx, req)
			if err := conn.Write(resp); err != nil {
				// e.g. a response above Thunderbird's 1 MB limit: answer with an error instead of dying.
				_ = conn.Write(fail(req.ID, "response could not be sent: "+err.Error()))
			}
		}(req)
	}
}

func watchThemeChanges(ctx context.Context, conn *Conn) {
	lastHash := currentThemeFingerprint()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next := currentThemeFingerprint()
			if next != lastHash {
				lastHash = next
				cfg, _ := config.Load()
				pal, src := theme.ResolveActive(cfg.Theme)
				_ = conn.Write(Response{
					Event:  "theme_updated",
					OK:     true,
					Result: theme.BuildPayload(pal, src),
				})
			}
		}
	}
}

func currentThemeFingerprint() [32]byte {
	cfg, _ := config.Load()
	pal, src := theme.ResolveActive(cfg.Theme)
	b, _ := json.Marshal(struct {
		Pal theme.Palette
		Src string
	}{pal, src})
	return sha256.Sum256(b)
}
