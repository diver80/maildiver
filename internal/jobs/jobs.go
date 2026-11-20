// Package jobs runs MailDiver's automatic work in the app process: triage scans (manually or
// when new mail arrives) and archive & cleanup runs. Mail access goes through the engine's job
// methods ("host.*"), which check the mailbox policy (enabled accounts, read-only). Encrypted
// mails are never decrypted for these jobs.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/diver80/maildiver/internal/ai"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/triage"
)

// Caller is the mail engine: the job methods ("host.*") read and change mails for the jobs.
type Caller interface {
	CallHost(ctx context.Context, method string, params any) (json.RawMessage, error)
	Connected() bool
}

// Publisher fans events out to bridge clients (the app).
type Publisher interface {
	Publish(event string, data any)
}

// Stable error codes (the add-on and the app use the same ones).
var (
	ErrBusy              = errors.New("busy")
	ErrTriageDisabled    = errors.New("feature_disabled:triage")
	ErrNoTriageAccounts  = errors.New("no_triage_accounts")
	ErrAccountNotEnabled = errors.New("account_not_enabled")
	ErrReadOnly          = errors.New("read_only")
	ErrDisconnected      = errors.New("engine_offline")
)

// newMailDelay coalesces a burst of new-mail notifications into one scan.
const newMailDelay = 3500 * time.Millisecond

// Runner owns the scan and archive jobs of one host process. One scan and one archive run at a time.
type Runner struct {
	ctx        context.Context
	rpc        Caller
	events     Publisher
	store      *triage.Store
	loadConfig func() (config.Config, error)
	// classify is the Stage-1 model call (ai.ClassifyWithBase); tests replace it.
	classify func(ctx context.Context, cfg config.AIConfig, m triage.MessageInput, base triage.Classification) (triage.Classification, error)

	mu           sync.Mutex
	scanning     bool
	archiving    bool
	lastScan     *ScanReport
	lastArchive  *ArchiveReport
	newMailTimer *time.Timer
}

// New returns a Runner whose jobs stop with ctx (the host's lifetime).
func New(ctx context.Context, rpc Caller, events Publisher, store *triage.Store, loadConfig func() (config.Config, error)) *Runner {
	if loadConfig == nil {
		loadConfig = config.Load
	}
	return &Runner{ctx: ctx, rpc: rpc, events: events, store: store, loadConfig: loadConfig, classify: ai.ClassifyWithBase}
}

func (r *Runner) publish(event string, data any) {
	if r.events != nil {
		r.events.Publish(event, data)
	}
}

func (r *Runner) config() config.Config {
	cfg, err := r.loadConfig()
	if err != nil {
		return config.Default()
	}
	return cfg
}

func (r *Runner) connected() bool {
	return r.rpc != nil && r.rpc.Connected()
}

// call sends a host RPC and decodes its result into out (when not nil).
func (r *Runner) call(ctx context.Context, timeout time.Duration, method string, params, out any) error {
	if !r.connected() {
		return ErrDisconnected
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := r.rpc.CallHost(c, method, params)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: unexpected answer: %w", method, err)
	}
	return nil
}

func nowISO() string { return time.Now().UTC().Format(time.RFC3339) }
