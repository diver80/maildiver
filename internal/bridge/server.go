package bridge

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/omathunder/internal/ai"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/native"
	"github.com/diver80/omathunder/internal/triage"
)

const BridgeProtocolVersion = 1

type RPCClient interface {
	Connected() bool
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

type DiscoveryInfo struct {
	Version   string    `json:"version"`
	Protocol  int       `json:"protocol"`
	URL       string    `json:"url"`
	Port      int       `json:"port"`
	Token     string    `json:"token"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
}

type Options struct {
	Port          int
	Token         string
	DiscoveryPath string
	RPC           RPCClient
	Events        *native.EventHub
	Store         *triage.Store
	ConfigLoader  func() (config.Config, error)
	AnyLLMOpts    []anyllm.Option
}

type Server struct {
	opts      Options
	token     string
	listener  net.Listener
	httpSrv   *http.Server
	info      DiscoveryInfo
	discPath  string
	srvCancel context.CancelFunc
	done      chan struct{}
	closeMu   sync.Mutex
	closed    bool
}

func Start(ctx context.Context, opts Options) (*Server, error) {
	if opts.ConfigLoader == nil {
		opts.ConfigLoader = config.Load
	}
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generate bridge token: %w", err)
		}
		token = hex.EncodeToString(buf)
	}

	discPath := opts.DiscoveryPath
	if discPath == "" {
		p, err := config.BridgeInfoPath()
		if err != nil {
			return nil, err
		}
		discPath = p
	}

	addr := fmt.Sprintf("127.0.0.1:%d", opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	tcpAddr := ln.Addr().(*net.TCPAddr)
	info := DiscoveryInfo{
		Version:   native.Version,
		Protocol:  BridgeProtocolVersion,
		URL:       fmt.Sprintf("http://127.0.0.1:%d", tcpAddr.Port),
		Port:      tcpAddr.Port,
		Token:     token,
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
	}

	srvCtx, srvCancel := context.WithCancel(context.Background())
	s := &Server{
		opts:      opts,
		token:     token,
		listener:  ln,
		info:      info,
		discPath:  discPath,
		srvCancel: srvCancel,
		done:      make(chan struct{}),
	}
	s.httpSrv = &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return srvCtx },
	}

	if err := writeDiscoveryFile(discPath, info); err != nil {
		srvCancel()
		_ = ln.Close()
		return nil, err
	}

	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Server exited unexpectedly; clean up our stale discovery file.
			removeOwnDiscoveryFile(discPath, info)
		}
	}()

	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	return s, nil
}

func (s *Server) Info() DiscoveryInfo { return s.info }
func (s *Server) URL() string         { return s.info.URL }
func (s *Server) Token() string       { return s.token }

func (s *Server) Close() error {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return nil
	}
	s.closed = true
	s.closeMu.Unlock()

	if s.done != nil {
		close(s.done)
	}
	if s.srvCancel != nil {
		s.srvCancel()
	}
	if s.discPath != "" {
		removeOwnDiscoveryFile(s.discPath, s.info)
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.httpSrv.Shutdown(shutCtx); err != nil {
		return s.httpSrv.Close()
	}
	return nil
}

func ReadDiscoveryFile(path string) (DiscoveryInfo, error) {
	if path == "" {
		p, err := config.BridgeInfoPath()
		if err != nil {
			return DiscoveryInfo{}, err
		}
		path = p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DiscoveryInfo{}, err
	}
	var info DiscoveryInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return DiscoveryInfo{}, err
	}
	return info, nil
}

func writeDiscoveryFile(path string, info DiscoveryInfo) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	// os.WriteFile keeps the mode of an existing file, so a stale tmp file must not survive.
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeOwnDiscoveryFile deletes bridge.json only while it still describes this server.
// A second host (e.g. another Thunderbird profile) may have replaced it in the meantime;
// its file must survive when this host shuts down.
func removeOwnDiscoveryFile(path string, own DiscoveryInfo) {
	cur, err := ReadDiscoveryFile(path)
	if err != nil {
		return
	}
	if cur.PID == own.PID && cur.Token == own.Token {
		_ = os.Remove(path)
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.auth(s.handleHealth))
	mux.HandleFunc("GET /v1/events", s.authSSE(s.handleEvents))

	mux.HandleFunc("GET /v1/accounts", s.auth(s.handleAccounts))
	mux.HandleFunc("GET /v1/folders", s.auth(s.handleFolders))
	mux.HandleFunc("GET /v1/messages", s.auth(s.handleMessages))
	mux.HandleFunc("GET /v1/messages/{id}", s.auth(s.handleMessageGet))
	mux.HandleFunc("GET /v1/messages/{id}/thread", s.auth(s.handleMessageThread))
	mux.HandleFunc("POST /v1/messages/{id}/actions", s.auth(s.handleMessageActions))

	mux.HandleFunc("POST /v1/compose/draft", s.auth(s.handleComposeDraft))
	mux.HandleFunc("POST /v1/compose/send", s.auth(s.handleComposeSend))

	mux.HandleFunc("GET /v1/calendars", s.auth(s.handleCalendars))
	mux.HandleFunc("GET /v1/calendar/events", s.auth(s.handleCalendarEvents))
	mux.HandleFunc("POST /v1/calendar/events", s.auth(s.handleCalendarCreate))
	mux.HandleFunc("PATCH /v1/calendar/events/{calendarId}/{eventId}", s.auth(s.handleCalendarUpdate))
	mux.HandleFunc("DELETE /v1/calendar/events/{calendarId}/{eventId}", s.auth(s.handleCalendarDelete))

	mux.HandleFunc("GET /v1/contacts", s.auth(s.handleContactsSearch))
	mux.HandleFunc("POST /v1/contacts/upsert", s.auth(s.handleContactUpsert))

	mux.HandleFunc("GET /v1/filters", s.auth(s.handleFiltersList))
	mux.HandleFunc("POST /v1/filters/upsert", s.auth(s.handleFiltersUpsert))
	mux.HandleFunc("POST /v1/filters/run", s.auth(s.handleFiltersRun))
	mux.HandleFunc("POST /v1/filters/open", s.auth(s.handleFiltersOpen))

	mux.HandleFunc("GET /v1/triage/state", s.auth(s.handleTriageState))
	mux.HandleFunc("GET /v1/ai/providers", s.auth(s.handleAIProviders))
	mux.HandleFunc("POST /v1/ai/chat", s.auth(s.handleAIChat))
	mux.HandleFunc("POST /v1/ai/analyze", s.auth(s.handleAIAnalyze))
	return mux
}

func (s *Server) checkToken(candidate string) bool {
	if candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(s.token)) == 1
}

func bearerFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) > len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
		return strings.TrimSpace(auth[len(prefix):])
	}
	return ""
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkToken(bearerFromRequest(r)) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) authSSE(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerFromRequest(r)
		if tok == "" {
			tok = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		if !s.checkToken(tok) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"ok":    false,
		"error": msg,
	})
}

func writeRPCResult(w http.ResponseWriter, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true,"result":`))
	if len(raw) == 0 {
		_, _ = w.Write([]byte(`null`))
	} else {
		_, _ = w.Write(raw)
	}
	_, _ = w.Write([]byte(`}` + "\n"))
}

func decodeJSONBody(r *http.Request, dst any) error {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// mutatingMethods change mailbox, calendar, contacts or filters. When such a call times out, the
// extension may still finish it, so the client gets "outcome_unknown" instead of a plain failure
// and must not blindly retry (compose calls carry an idempotency key for that reason).
var mutatingMethods = map[string]bool{
	"messages.actions": true,
	"compose.draft":    true,
	"compose.send":     true,
	"calendar.create":  true,
	"calendar.update":  true,
	"calendar.delete":  true,
	"contacts.upsert":  true,
	"filters.upsert":   true,
	"filters.run":      true,
}

// rpcTimeout returns the deadline for one reverse-RPC call into Thunderbird.
func rpcTimeout(method string) time.Duration {
	switch method {
	case "compose.send", "filters.run":
		return 90 * time.Second
	case "messages.list", "messages.thread", "calendar.events":
		return 45 * time.Second
	default:
		return 30 * time.Second
	}
}

// rpcErrorStatus maps an extension error ("code" or "code: detail", see docs/BRIDGE.md) to an HTTP status.
func rpcErrorStatus(err error) int {
	if errors.Is(err, native.ErrThunderbirdDisconnected) {
		return http.StatusServiceUnavailable
	}
	msg := strings.TrimSpace(err.Error())
	hasCode := func(codes ...string) bool {
		for _, c := range codes {
			if msg == c || strings.HasPrefix(msg, c+":") || strings.HasPrefix(msg, c+" ") {
				return true
			}
		}
		return false
	}
	switch {
	case hasCode("read_only", "account_not_enabled", "account_not_readable", "send_disabled", "cross_account_move") ||
		strings.HasPrefix(msg, "feature_disabled"):
		return http.StatusForbidden
	case hasCode("not_found"):
		return http.StatusNotFound
	case hasCode("invalid_argument"):
		return http.StatusBadRequest
	case hasCode("ambiguous", "message_changed", "conflict"):
		return http.StatusConflict
	}
	return http.StatusBadGateway
}

func (s *Server) callTB(w http.ResponseWriter, r *http.Request, method string, params any) {
	if s.opts.RPC == nil || !s.opts.RPC.Connected() {
		writeError(w, http.StatusServiceUnavailable, native.ErrThunderbirdDisconnected.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout(method))
	defer cancel()
	raw, err := s.opts.RPC.Call(ctx, method, params)
	if err != nil {
		s.writeRPCError(w, r, method, err)
		return
	}
	writeRPCResult(w, raw)
}

func (s *Server) writeRPCError(w http.ResponseWriter, r *http.Request, method string, err error) {
	if mutatingMethods[method] && errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil {
		writeError(w, http.StatusGatewayTimeout, "outcome_unknown: Thunderbird did not answer in time; the change may still have been applied")
		return
	}
	writeError(w, rpcErrorStatus(err), err.Error())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.opts.ConfigLoader()
	tbConnected := s.opts.RPC != nil && s.opts.RPC.Connected()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"result": map[string]any{
			"version":              s.info.Version,
			"protocol":             s.info.Protocol,
			"pid":                  s.info.PID,
			"url":                  s.info.URL,
			"startedAt":            s.info.StartedAt,
			"thunderbirdConnected": tbConnected,
			"bridge":               cfg.Bridge,
			"readOnly":             cfg.Mailbox.ReadOnly,
			"ai": map[string]any{
				"agent":      cfg.AI.Agent,
				"classifier": cfg.AI.Classifier,
			},
		},
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.opts.Events == nil {
		writeError(w, http.StatusServiceUnavailable, "event hub unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	subID, ch := s.opts.Events.Subscribe()
	defer s.opts.Events.Unsubscribe(subID)

	hello, _ := json.Marshal(map[string]any{
		"thunderbirdConnected": s.opts.RPC != nil && s.opts.RPC.Connected(),
		"version":              s.info.Version,
	})
	_, _ = fmt.Fprintf(w, "event: connected\ndata: %s\n\n", hello)
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		case <-keepalive.C:
			_, _ = w.Write([]byte(": keepalive\n\n"))
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			payload, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, payload)
			flusher.Flush()
		}
	}
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	s.callTB(w, r, "accounts.list", nil)
}

func (s *Server) handleFolders(w http.ResponseWriter, r *http.Request) {
	s.callTB(w, r, "folders.list", map[string]any{
		"accountId": strings.TrimSpace(r.URL.Query().Get("accountId")),
	})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := map[string]any{}
	if v := strings.TrimSpace(q.Get("accountId")); v != "" {
		params["accountId"] = v
	}
	if v := strings.TrimSpace(q.Get("folderId")); v != "" {
		params["folderId"] = v
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		params["q"] = v
	}
	if v := strings.TrimSpace(q.Get("unread")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			params["unread"] = b
		}
	}
	if v := strings.TrimSpace(q.Get("days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			params["days"] = n
		}
	}
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			params["limit"] = n
		}
	}
	if v := strings.TrimSpace(q.Get("cursor")); v != "" {
		params["cursor"] = v
	}
	s.callTB(w, r, "messages.list", params)
}

// messageRefParams copies the optional disambiguation hints (see docs/BRIDGE.md) from the query.
func messageRefParams(r *http.Request, params map[string]any) {
	q := r.URL.Query()
	for _, key := range []string{"expectedMessageId", "folderId", "accountId"} {
		if v := strings.TrimSpace(q.Get(key)); v != "" {
			params[key] = v
		}
	}
}

func (s *Server) handleMessageGet(w http.ResponseWriter, r *http.Request) {
	params := map[string]any{
		"id":     r.PathValue("id"),
		"format": strings.TrimSpace(r.URL.Query().Get("format")),
	}
	messageRefParams(r, params)
	s.callTB(w, r, "messages.get", params)
}

func (s *Server) handleMessageThread(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, 50)
		}
	}
	params := map[string]any{
		"id":    r.PathValue("id"),
		"limit": limit,
	}
	messageRefParams(r, params)
	s.callTB(w, r, "messages.thread", params)
}

func (s *Server) handleMessageActions(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	body["id"] = r.PathValue("id")
	s.callTB(w, r, "messages.actions", body)
}

func (s *Server) handleComposeDraft(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	s.callTB(w, r, "compose.draft", body)
}

func (s *Server) handleComposeSend(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.opts.ConfigLoader()
	if cfg.Mailbox.ReadOnly {
		writeError(w, http.StatusForbidden, native.ErrReadOnly)
		return
	}
	if !cfg.Bridge.AllowSend {
		writeError(w, http.StatusForbidden, "send_disabled")
		return
	}
	var body map[string]any
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	s.callTB(w, r, "compose.send", body)
}

func (s *Server) handleCalendars(w http.ResponseWriter, r *http.Request) {
	s.callTB(w, r, "calendar.list", nil)
}

func (s *Server) handleCalendarEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := map[string]any{}
	if v := strings.TrimSpace(q.Get("start")); v != "" {
		params["start"] = v
	}
	if v := strings.TrimSpace(q.Get("end")); v != "" {
		params["end"] = v
	}
	if ids := q["calendarId"]; len(ids) > 0 {
		params["calendarIds"] = ids
	}
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			params["limit"] = n
		}
	}
	s.callTB(w, r, "calendar.events", params)
}

func (s *Server) handleCalendarCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	s.callTB(w, r, "calendar.create", body)
}

func (s *Server) handleCalendarUpdate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	body["calendarId"] = r.PathValue("calendarId")
	body["id"] = r.PathValue("eventId")
	s.callTB(w, r, "calendar.update", body)
}

func (s *Server) handleCalendarDelete(w http.ResponseWriter, r *http.Request) {
	params := map[string]any{
		"calendarId": r.PathValue("calendarId"),
		"id":         r.PathValue("eventId"),
	}
	q := r.URL.Query()
	if v := strings.TrimSpace(q.Get("recurrenceId")); v != "" {
		params["recurrenceId"] = v
	}
	if v := strings.TrimSpace(q.Get("scope")); v != "" {
		if v != "occurrence" && v != "series" {
			writeError(w, http.StatusBadRequest, "invalid_argument: scope must be occurrence or series")
			return
		}
		params["scope"] = v
	}
	s.callTB(w, r, "calendar.delete", params)
}

func (s *Server) handleContactsSearch(w http.ResponseWriter, r *http.Request) {
	params := map[string]any{}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		params["q"] = q
	}
	s.callTB(w, r, "contacts.search", params)
}

func (s *Server) handleContactUpsert(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	s.callTB(w, r, "contacts.upsert", body)
}

func (s *Server) handleFiltersList(w http.ResponseWriter, r *http.Request) {
	params := map[string]any{}
	if acc := strings.TrimSpace(r.URL.Query().Get("accountId")); acc != "" {
		params["accountId"] = acc
	}
	s.callTB(w, r, "filters.list", params)
}

func (s *Server) handleFiltersUpsert(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.opts.ConfigLoader()
	if cfg.Mailbox.ReadOnly {
		writeError(w, http.StatusForbidden, native.ErrReadOnly)
		return
	}
	var body map[string]any
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	s.callTB(w, r, "filters.upsert", body)
}

func (s *Server) handleFiltersRun(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.opts.ConfigLoader()
	if cfg.Mailbox.ReadOnly {
		writeError(w, http.StatusForbidden, native.ErrReadOnly)
		return
	}
	var body map[string]any
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	s.callTB(w, r, "filters.run", body)
}

func (s *Server) handleFiltersOpen(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSONBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	s.callTB(w, r, "filters.openDialog", body)
}

func (s *Server) handleTriageState(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "triage store unavailable")
		return
	}
	st, err := s.opts.Store.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"result": map[string]any{
			"state":      st,
			"barSummary": triage.ComputeBarSummary(st),
		},
	})
}

func (s *Server) handleAIProviders(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.opts.ConfigLoader()
	out := map[string]any{
		"providers":  ai.SupportedProviders(),
		"agent":      cfg.AI.Agent,
		"classifier": cfg.AI.Classifier,
	}
	if r.URL.Query().Get("models") == "1" {
		// The endpoint always comes from config.json; a request can only pick the backend.
		backend := strings.TrimSpace(r.URL.Query().Get("backend"))
		if backend == "" {
			backend = cfg.AI.Agent.Backend
		}
		models, err := ai.ListModelsFor(r.Context(), cfg.AI, backend, s.opts.AnyLLMOpts...)
		if err != nil {
			out["modelsError"] = err.Error()
		} else {
			out["models"] = models
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"result": out,
	})
}

func (s *Server) handleAIChat(w http.ResponseWriter, r *http.Request) {
	var req ai.ChatRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_argument: messages are required")
		return
	}
	cfg, _ := s.opts.ConfigLoader()
	if !cfg.Features.Assistant {
		writeError(w, http.StatusForbidden, native.ErrAssistantDisabled)
		return
	}
	if !req.Stream {
		resp, err := ai.Chat(r.Context(), cfg.AI, req, s.opts.AnyLLMOpts...)
		if err != nil {
			writeError(w, aiErrorStatus(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"result": resp,
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	chunkCh, errCh := ai.ChatStream(r.Context(), cfg.AI, req, s.opts.AnyLLMOpts...)
	for chunkCh != nil || errCh != nil {
		select {
		case <-r.Context().Done():
			return
		case chunk, open := <-chunkCh:
			if !open {
				chunkCh = nil
				continue
			}
			b, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case err, open := <-errCh:
			if !open {
				errCh = nil
				continue
			}
			if err != nil {
				b, _ := json.Marshal(map[string]any{"error": err.Error()})
				_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", b)
				flusher.Flush()
				return
			}
		}
	}
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	flusher.Flush()
}

// aiErrorStatus separates configuration mistakes (400) from provider failures (502).
func aiErrorStatus(err error) int {
	switch {
	case errors.Is(err, ai.ErrUnknownBackend), errors.Is(err, ai.ErrNoBackend),
		errors.Is(err, ai.ErrEndpointRequired), errors.Is(err, ai.ErrKeyNotForEndpoint):
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

func (s *Server) handleAIAnalyze(w http.ResponseWriter, r *http.Request) {
	var req ai.AnalyzeRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, _ := s.opts.ConfigLoader()
	if !cfg.Features.Assistant {
		writeError(w, http.StatusForbidden, native.ErrAssistantDisabled)
		return
	}
	if req.Language == "" && (cfg.Language == "de" || cfg.Language == "en") {
		req.Language = cfg.Language
	}
	ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
	defer cancel()
	resp, err := ai.Analyze(ctx, cfg.AI, req)
	if err != nil {
		writeError(w, aiErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"result": resp,
	})
}
