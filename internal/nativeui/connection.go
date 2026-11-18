package nativeui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/diver80/omathunder/internal/bridgeclient"
)

const (
	sseBackoffMin   = time.Second
	sseBackoffMax   = 15 * time.Second
	reloadDebounce  = 500 * time.Millisecond
	reloadMaxDelay  = 2 * time.Second
	healthTimeout   = 3 * time.Second
	enablePollTries = 12
)

var errStreamEnded = errors.New("bridge event stream ended")

// connManager owns the bridge client. The bridge gets a new port and token on every host
// start, so the client is re-discovered from bridge.json whenever the connection drops and
// swapped when URL/token changed. All methods are safe from any goroutine.
type connManager struct {
	discoveryPath string
	fixed         *bridgeclient.Client // pre-wired client (tests): never re-discovered

	mu     sync.Mutex
	client *bridgeclient.Client
}

func newConnManager(discoveryPath string, fixed *bridgeclient.Client) *connManager {
	return &connManager{discoveryPath: discoveryPath, fixed: fixed, client: fixed}
}

// Client returns the current bridge client, or bridgeclient.ErrOffline when there is none.
func (m *connManager) Client() (*bridgeclient.Client, error) {
	if c := m.current(); c != nil {
		return c, nil
	}
	return nil, bridgeclient.ErrOffline
}

func (m *connManager) current() *bridgeclient.Client {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

// set installs c and reports whether that changed the endpoint (or ended an offline phase).
func (m *connManager) set(c *bridgeclient.Client) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := m.client == nil || !m.client.SameEndpoint(c)
	m.client = c
	return changed
}

// isCurrent reports whether c (or a client for the same endpoint) is the current client.
func (m *connManager) isCurrent(c *bridgeclient.Client) bool {
	cur := m.current()
	return c != nil && cur != nil && (cur == c || cur.SameEndpoint(c))
}

// invalidate drops c if it is still the current client (nil drops any client).
func (m *connManager) invalidate(c *bridgeclient.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c == nil || m.client == c || (m.client != nil && m.client.SameEndpoint(c)) {
		m.client = nil
	}
}

// discover reads bridge.json afresh. It reuses the current client object when the endpoint
// is unchanged (keeps its connection pool).
func (m *connManager) discover() (*bridgeclient.Client, error) {
	if m.fixed != nil {
		return m.fixed, nil
	}
	bc, _, err := bridgeclient.Discover(m.discoveryPath)
	if err != nil {
		return nil, err
	}
	if cur := m.current(); cur != nil && cur.SameEndpoint(bc) {
		return cur, nil
	}
	return bc, nil
}

// connect returns a healthy client: the current one if it answers, else a freshly discovered
// one (installed on success). On failure the client is dropped and the error returned.
func (m *connManager) connect(ctx context.Context) (*bridgeclient.Client, bridgeclient.HealthStatus, error) {
	health := func(c *bridgeclient.Client) (bridgeclient.HealthStatus, error) {
		hctx, cancel := context.WithTimeout(ctx, healthTimeout)
		defer cancel()
		return c.Health(hctx)
	}
	cur := m.current()
	var lastErr error
	if cur != nil {
		h, err := health(cur)
		if err == nil {
			return cur, h, nil
		}
		lastErr = err
	}
	fresh, err := m.discover()
	if err != nil {
		m.invalidate(cur)
		return nil, bridgeclient.HealthStatus{}, err
	}
	if fresh == cur && lastErr != nil {
		m.invalidate(cur)
		return nil, bridgeclient.HealthStatus{}, lastErr
	}
	h, err := health(fresh)
	if err != nil {
		m.invalidate(cur)
		return nil, bridgeclient.HealthStatus{}, err
	}
	m.set(fresh)
	return fresh, h, nil
}

// client returns the live bridge client or a typed offline error. Safe from any goroutine.
func (a *App) client() (*bridgeclient.Client, error) {
	return a.conn.Client()
}

// liveClient returns the current client or nil (for the copilot runner).
func (a *App) liveClient() *bridgeclient.Client {
	c, _ := a.conn.Client()
	return c
}

// runEventStream keeps the SSE connection to the bridge alive: discover → connect → read until
// EOF → mark offline → re-discover with exponential backoff (1 s … 15 s).
func (a *App) runEventStream(ctx context.Context) {
	backoff := sseBackoffMin
	offlineReported := false
	for ctx.Err() == nil {
		bc, err := a.conn.discover()
		if err != nil {
			a.conn.invalidate(nil)
		} else {
			var resp *http.Response
			resp, err = bc.OpenStream(ctx, http.MethodGet, "/v1/events", nil)
			if err == nil && resp.StatusCode != http.StatusOK {
				_ = resp.Body.Close()
				err = fmt.Errorf("bridge HTTP %d", resp.StatusCode)
			}
			if err == nil {
				sawConnected := a.readBridgeSSE(ctx, bc, resp.Body)
				_ = resp.Body.Close()
				if sawConnected {
					// The stream was live: its end is a new offline phase that must be reported.
					backoff = sseBackoffMin
					offlineReported = false
				}
				err = errStreamEnded
			}
			a.conn.invalidate(bc)
		}
		if ctx.Err() != nil {
			return
		}
		if !offlineReported {
			a.reportOffline(err)
			offlineReported = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, sseBackoffMax)
	}
}

// reportOffline marks the app offline and checks whether the bridge is disabled in config.
// Safe from any goroutine.
func (a *App) reportOffline(cause error) {
	disabled := false
	if cfg, err := a.opts.ConfigLoader(); err == nil {
		disabled = !cfg.Bridge.Enabled
	}
	a.emitUpdate(func() {
		a.bridgeOnline = false
		a.thunderbirdConnected = false
		a.bridgeDisabled = disabled
		if cause != nil && !errors.Is(cause, errStreamEnded) {
			a.lastError = a.errText(cause)
		}
		a.notifyTray()
	})
}

// readBridgeSSE parses the event stream and dispatches complete events. It returns whether the
// initial `connected` event was seen.
func (a *App) readBridgeSSE(ctx context.Context, bc *bridgeclient.Client, body io.Reader) bool {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	sawConnected := false
	var eventName string
	var data []string
	dispatch := func() {
		if eventName == "" && len(data) == 0 {
			return
		}
		name := eventName
		if name == "" {
			name = "message"
		}
		if name == "connected" {
			sawConnected = true
		}
		a.handleBridgeEvent(ctx, bc, name, strings.Join(data, "\n"))
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		switch {
		case line == "":
			dispatch()
			eventName, data = "", nil
		case strings.HasPrefix(line, ":"):
			// comment / keepalive
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return sawConnected
}

// unwrapSSEData returns the payload of a bridge event. Bridge events are sent as the wrapper
// {"event": "...", "data": <payload>, "timestamp": "..."} (contract §3); the initial
// `connected` event carries its payload directly.
func unwrapSSEData(raw string) json.RawMessage {
	var w struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &w); err == nil && w.Event != "" {
		if len(w.Data) == 0 {
			return json.RawMessage("null")
		}
		return w.Data
	}
	return json.RawMessage(raw)
}

type sseHello struct {
	ThunderbirdConnected bool   `json:"thunderbirdConnected"`
	Version              string `json:"version"`
}

// handleBridgeEvent reacts to one SSE event. Safe from any goroutine; bc is the client the
// stream belongs to (nil when called without a stream).
func (a *App) handleBridgeEvent(ctx context.Context, bc *bridgeclient.Client, name, raw string) {
	payload := unwrapSSEData(raw)
	switch name {
	case "connected":
		var hello sseHello
		_ = json.Unmarshal(payload, &hello)
		changed := false
		if bc != nil {
			changed = a.conn.set(bc)
		}
		a.emitUpdate(func() {
			wasLive := a.bridgeOnline && a.thunderbirdConnected
			a.bridgeOnline = true
			a.bridgeDisabled = false
			a.lastError = ""
			a.thunderbirdConnected = hello.ThunderbirdConnected
			a.notifyTray()
			if changed || !wasLive {
				a.startRefresh(ctx)
			}
		})
	case "mail.new":
		a.handleNewMailSSE(ctx, payload)
	case "mail.updated", "mail.moved", "mail.deleted":
		a.msgReload.trigger(func() { a.LoadMessages(ctx) })
	case "calendar.changed":
		a.calReload.trigger(func() { a.RefreshAll(ctx) })
	default:
		a.handleJobEvent(ctx, name, payload)
	}
}

type newMailPayload struct {
	AccountID string                       `json:"accountId"`
	FolderID  string                       `json:"folderId"`
	Folder    string                       `json:"folder"`
	Count     int                          `json:"count"`
	Messages  []bridgeclient.MessageHeader `json:"messages"`
}

// handleNewMailSSE takes the UNWRAPPED mail.new payload. Safe from any goroutine.
func (a *App) handleNewMailSSE(ctx context.Context, payload json.RawMessage) {
	var p newMailPayload
	_ = json.Unmarshal(payload, &p)
	if len(p.Messages) > 0 {
		a.emitUpdate(func() { a.notifyNewMail(p.Messages) })
	}
	a.msgReload.trigger(func() { a.LoadMessages(ctx) })
}

// notifyNewMail raises desktop alerts for unseen mails (UI thread).
func (a *App) notifyNewMail(msgs []bridgeclient.MessageHeader) {
	var fresh []bridgeclient.MessageHeader
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

// debouncer coalesces bursts of SSE events into one reload (trailing edge, max delay bound).
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

// EnableBridge sets bridge.enabled=true in config.json (keeping all other fields) and retries
// discovery while the host starts the bridge (contract §9). UI thread.
func (a *App) EnableBridge(ctx context.Context) {
	if a.enablingBridge {
		return
	}
	a.enablingBridge = true
	go func() {
		cfg, err := a.opts.ConfigLoader()
		if err == nil {
			cfg.Bridge.Enabled = true
			err = a.opts.ConfigSaver(cfg)
		}
		if err != nil {
			a.emitUpdate(func() {
				a.enablingBridge = false
				a.toast(a.tr("Bridge konnte nicht aktiviert werden: ", "Could not enable the bridge: ")+a.errText(err), "error")
			})
			return
		}
		a.emitUpdate(func() {
			a.cfg.Bridge.Enabled = true
			a.bridgeDisabled = false
			a.toast(a.tr("Bridge aktiviert – verbinde mit Thunderbird…", "Bridge enabled – connecting to Thunderbird…"), "info")
		})
		for i := 0; i < enablePollTries; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			if _, _, err := a.conn.connect(ctx); err == nil {
				break
			}
		}
		a.emitUpdate(func() {
			a.enablingBridge = false
			a.startRefresh(ctx)
		})
	}()
}
