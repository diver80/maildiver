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
	"sync"
	"time"

	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/jobs"
	"github.com/diver80/omathunder/internal/theme"
	"github.com/diver80/omathunder/internal/triage"
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
// 4: the add-on is a bridge only; scans and archive runs moved into the host (host.* RPC).
const ProtocolVersion = 4

// Stable error codes the add-on and the app translate.
const (
	ErrReadOnly          = "read_only"
	ErrAssistantDisabled = "feature_disabled:assistant"
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
	BridgeFile      string               `json:"bridgeFile,omitempty"`
	Bridge          config.BridgeConfig  `json:"bridge"`
	Features        config.FeatureFlags  `json:"features"`
	Mailbox         config.MailboxPolicy `json:"mailbox"`
}

type Handler struct {
	store  *triage.Store
	rpc    *RPCBroker
	events *EventHub
	// jobs runs triage scans and archive runs in this process (set by RunHostWithOptions).
	jobs *jobs.Runner
	// configChanged (optional) runs after a config-writing method, e.g. so the bridge
	// supervisor reacts to bridge.enabled without waiting for its next poll.
	configChanged func()
	// cfgMu serializes load-modify-save cycles on the config file; requests run concurrently.
	cfgMu sync.Mutex
}

func NewHandler(store *triage.Store) *Handler {
	return &Handler{
		store:  store,
		rpc:    NewRPCBroker(),
		events: NewEventHub(),
	}
}

func (h *Handler) Store() *triage.Store { return h.store }
func (h *Handler) RPC() *RPCBroker      { return h.rpc }
func (h *Handler) Events() *EventHub    { return h.events }
func (h *Handler) Jobs() *jobs.Runner   { return h.jobs }

func BuildDoctorReport() DoctorReport {
	cfg, _ := config.Load()
	pal, src := theme.ResolveActive(cfg.Theme)
	omPath := theme.OmarchyColorsPath()
	_, omErr := os.Stat(omPath)
	paths, _ := ManifestPaths()
	bridgePath, _ := config.BridgeInfoPath()

	clis := map[string]bool{}
	for _, bin := range []string{"claude", "gemini", "opencode", "codex", "ollama"} {
		_, err := exec.LookPath(bin)
		clis[bin] = err == nil
	}
	envKeys := map[string]bool{
		"GEMINI_API_KEY":       config.LookupAPIKey("GEMINI_API_KEY", "GOOGLE_API_KEY") != "",
		"ANTHROPIC_API_KEY":    config.LookupAPIKey("ANTHROPIC_API_KEY") != "",
		"OPENAI_API_KEY":       config.LookupAPIKey("OPENAI_API_KEY") != "",
		"DEEPSEEK_API_KEY":     config.LookupAPIKey("DEEPSEEK_API_KEY") != "",
		"GROQ_API_KEY":         config.LookupAPIKey("GROQ_API_KEY") != "",
		"MISTRAL_API_KEY":      config.LookupAPIKey("MISTRAL_API_KEY") != "",
		"OPENAICOMPAT_API_KEY": config.LookupAPIKey("OPENAICOMPAT_API_KEY") != "",
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
		BridgeFile:      bridgePath,
		Bridge:          cfg.Bridge,
		Features:        cfg.Features,
		Mailbox:         cfg.Mailbox,
	}
}

// configWriters are the methods that change the config file.
var configWriters = map[string]bool{"save_config": true, "set_theme": true}

func (h *Handler) Handle(ctx context.Context, req Request) Response {
	if configWriters[req.Method] {
		h.cfgMu.Lock()
		defer h.cfgMu.Unlock()
		if h.configChanged != nil {
			defer h.configChanged()
		}
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
		if p.Mode != "" && p.Mode != "auto" && p.Mode != "omarchy" && p.Mode != "preset" {
			return fail(req.ID, "invalid theme mode: "+p.Mode)
		}
		if p.Preset != "" {
			if _, ok := theme.GetPreset(p.Preset); !ok {
				return fail(req.ID, "unknown theme preset: "+p.Preset)
			}
		}
		cfg, err := config.Load()
		if err != nil {
			return fail(req.ID, "config unreadable, refusing to overwrite: "+err.Error())
		}
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

	case "tb_rpc_result":
		var p struct {
			ID     string          `json:"id"`
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result,omitempty"`
			Error  string          `json:"error,omitempty"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, err.Error())
		}
		if h.rpc != nil {
			h.rpc.Resolve(p.ID, p.OK, p.Result, p.Error)
		}
		return Response{ID: req.ID, OK: true}

	case "triage_new_mail":
		// The add-on reports new mail; the scan runs here when the settings ask for it.
		if h.jobs != nil {
			h.jobs.NewMail()
		}
		return Response{ID: req.ID, OK: true}

	case "tb_event":
		var p struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data,omitempty"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, err.Error())
		}
		if h.events != nil && p.Event != "" {
			h.events.Publish(p.Event, p.Data)
		}
		return Response{ID: req.ID, OK: true}

	default:
		return Response{ID: req.ID, OK: false, Error: "unknown method: " + req.Method}
	}
}

// HostOptions customizes RunHostWithOptions without adding circular imports to internal/native.
type HostOptions struct {
	OnReady func(ctx context.Context, handler *Handler) (cleanup func(), err error)
}

// SetConfigChangedHook registers fn to run after every config-writing request. Call it from OnReady.
func (h *Handler) SetConfigChangedHook(fn func()) { h.configChanged = fn }

// RunHost runs the stdio Native Messaging loop plus a lightweight Omarchy theme watcher.
// Every request is handled in its own goroutine, so a long AI call does not block the UI.
func RunHost(ctx context.Context, r io.Reader, w io.Writer) error {
	return RunHostWithOptions(ctx, r, w, HostOptions{})
}

func RunHostWithOptions(ctx context.Context, r io.Reader, w io.Writer, opts HostOptions) error {
	store, err := triage.OpenStore()
	if err != nil {
		return err
	}
	handler := NewHandler(store)
	conn := NewConn(r, w)
	handler.rpc.Attach(conn)
	defer handler.rpc.Detach()

	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	handler.jobs = jobs.New(watchCtx, handler.rpc, handler.events, store, config.Load)
	go watchThemeChanges(watchCtx, conn, handler.events)

	if opts.OnReady != nil {
		cleanup, err := opts.OnReady(watchCtx, handler)
		if err != nil {
			return err
		}
		if cleanup != nil {
			defer cleanup()
		}
	}

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
			// One-way notifications (tb_rpc_result, tb_event) carry no ID and do not expect a response frame.
			if req.ID == "" && (req.Method == "tb_rpc_result" || req.Method == "tb_event" || req.Method == "triage_new_mail") {
				return
			}
			if err := conn.Write(resp); err != nil {
				// e.g. a response above Thunderbird's 1 MB limit: answer with an error instead of dying.
				_ = conn.Write(fail(req.ID, "response could not be sent: "+err.Error()))
			}
		}(req)
	}
}

func watchThemeChanges(ctx context.Context, conn *Conn, events *EventHub) {
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
				payload := theme.BuildPayload(pal, src)
				_ = conn.Write(Response{
					Event:  "theme_updated",
					OK:     true,
					Result: payload,
				})
				if events != nil {
					events.Publish("theme_updated", payload)
				}
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
