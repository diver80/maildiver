package bridge

import (
	"context"
	"sync"
	"time"

	"github.com/diver80/omathunder/internal/config"
)

// Supervisor keeps the bridge in line with config.json while the native host runs:
// it starts the server when bridge.enabled turns true, stops it when it turns false and
// restarts it when bridge.port changes. The bridge is opt-in, so a host started with the
// bridge disabled opens no port until the user enables it (app button, CLI or settings page).
type Supervisor struct {
	base     Options
	load     func() (config.Config, error)
	interval time.Duration
	logf     func(format string, args ...any)

	mu      sync.Mutex
	srv     *Server
	port    int
	lastErr string

	cancel context.CancelFunc
	done   chan struct{}
	kick   chan struct{}
}

// StartSupervisor reconciles once synchronously and then every interval until ctx is done or Close is called.
func StartSupervisor(ctx context.Context, base Options, interval time.Duration, logf func(format string, args ...any)) *Supervisor {
	if base.ConfigLoader == nil {
		base.ConfigLoader = config.Load
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	runCtx, cancel := context.WithCancel(ctx)
	s := &Supervisor{
		base:     base,
		load:     base.ConfigLoader,
		interval: interval,
		logf:     logf,
		cancel:   cancel,
		done:     make(chan struct{}),
		kick:     make(chan struct{}, 1),
	}
	s.reconcile(runCtx)
	go s.loop(runCtx)
	return s
}

func (s *Supervisor) loop(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.stopServer()
			return
		case <-ticker.C:
			s.reconcile(ctx)
		case <-s.kick:
			s.reconcile(ctx)
		}
	}
}

// Reconcile asks the supervisor to re-read the config now (e.g. right after set_config).
func (s *Supervisor) Reconcile() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Current returns the running server or nil.
func (s *Supervisor) Current() *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv
}

// Close stops the loop and the server and waits until both are gone.
func (s *Supervisor) Close() {
	s.cancel()
	<-s.done
}

func (s *Supervisor) reconcile(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	cfg, err := s.load()
	if err != nil {
		// A config that cannot be read must not toggle the bridge either way.
		s.noteErr("bridge config unreadable, keeping current state: " + err.Error())
		return
	}
	want := cfg.Bridge.Enabled
	wantPort := cfg.Bridge.Port

	s.mu.Lock()
	running := s.srv != nil
	samePort := s.port == wantPort
	s.mu.Unlock()

	switch {
	case !want && running:
		s.stopServer()
		s.logf("omathunder bridge stopped (disabled in config)")
	case want && running && !samePort:
		s.stopServer()
		s.startServer(ctx, wantPort)
	case want && !running:
		s.startServer(ctx, wantPort)
	}
}

func (s *Supervisor) startServer(ctx context.Context, port int) {
	opts := s.base
	opts.Port = port
	srv, err := Start(ctx, opts)
	if err != nil && port > 0 {
		// Retry on an ephemeral port instead of leaving the bridge down.
		opts.Port = 0
		srv, err = Start(ctx, opts)
	}
	if err != nil {
		s.noteErr("omathunder bridge disabled: " + err.Error())
		return
	}
	s.mu.Lock()
	s.srv = srv
	s.port = port
	s.lastErr = ""
	s.mu.Unlock()
}

func (s *Supervisor) stopServer() {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

// noteErr logs an error once instead of every interval.
func (s *Supervisor) noteErr(msg string) {
	s.mu.Lock()
	dup := s.lastErr == msg
	s.lastErr = msg
	s.mu.Unlock()
	if !dup {
		s.logf("%s", msg)
	}
}
