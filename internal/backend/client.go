package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/postdive/internal/ai"
	"github.com/diver80/postdive/internal/archive"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/events"
	"github.com/diver80/postdive/internal/jobs"
	"github.com/diver80/postdive/internal/triage"
)

const (
	// DefaultRequestTimeout applies to an engine call whose context carries no deadline.
	DefaultRequestTimeout = 35 * time.Second
	// AIRequestTimeout bounds UI AI calls above per-backend timeouts (300 s for local/CLI models).
	AIRequestTimeout = 330 * time.Second
	// analyzeTimeout bounds one mail analysis when callers supply no deadline.
	analyzeTimeout = 320 * time.Second
)

// Engine is the mail engine (mailengine.Engine): calls by method name with JSON-shaped params.
type Engine interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	Connected() bool
}

// Options wire a Client to the parts of the running app.
type Options struct {
	Engine     Engine
	Events     *events.Hub
	Store      *triage.Store
	Jobs       *jobs.Runner
	LoadConfig func() (config.Config, error)
	Version    string
	// AnyLLMOpts are passed to the AI layer (tests point it at a fake server).
	AnyLLMOpts []anyllm.Option
}

// Client gives the app the engine, the AI layer and the jobs, all in-process.
type Client struct {
	opts Options
}

// ErrOffline is returned when no engine is wired.
var ErrOffline = errors.New("engine_offline")

// New returns a Client. A nil LoadConfig reads config.json.
func New(opts Options) *Client {
	if opts.LoadConfig == nil {
		opts.LoadConfig = config.Load
	}
	return &Client{opts: opts}
}

func (c *Client) config() config.Config {
	cfg, err := c.opts.LoadConfig()
	if err != nil {
		return config.Default()
	}
	return cfg
}

// Subscribe returns a subscription to engine events (nil channel without an event hub).
func (c *Client) Subscribe() (uint64, <-chan events.Event) {
	if c == nil || c.opts.Events == nil {
		return 0, nil
	}
	return c.opts.Events.Subscribe()
}

// Unsubscribe ends a subscription.
func (c *Client) Unsubscribe(id uint64) {
	if c != nil && c.opts.Events != nil && id != 0 {
		c.opts.Events.Unsubscribe(id)
	}
}

func withDefaultDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// mutatingMethods may already have changed the mailbox when they time out.
var mutatingMethods = map[string]bool{
	"messages.actions": true, "messages.batchActions": true, "messages.markAllRead": true, "messages.undoMove": true,
	"messages.snooze": true, "messages.wakeSnoozed": true,
	"compose.draft": true, "compose.send": true, "compose.discardDraft": true,
	"calendar.create": true, "calendar.update": true, "calendar.delete": true,
	"contacts.upsert": true, "filters.upsert": true, "filters.run": true,
}

// call runs one engine method and decodes its result into out (when not nil).
func (c *Client) call(ctx context.Context, timeout time.Duration, method string, params, out any) error {
	if c == nil || c.opts.Engine == nil {
		return ErrOffline
	}
	ctx, cancel := withDefaultDeadline(ctx, timeout)
	defer cancel()
	raw, err := c.opts.Engine.Call(ctx, method, params)
	if err != nil {
		return wrapError(ctx, method, err)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: unexpected result: %w", method, err)
	}
	return nil
}

// wrapError gives engine errors their stable code ("read_only", "not_found", …). A mutating
// call that ran out of time may still have been applied: "outcome_unknown".
func wrapError(ctx context.Context, method string, err error) error {
	if mutatingMethods[method] && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return &APIError{Code: "outcome_unknown", Message: "outcome_unknown: the change may still have been applied", Err: err}
	}
	code, detail := ParseErrorCode(err.Error())
	return &APIError{Code: code, Detail: detail, Message: err.Error(), Err: err}
}

// HealthStatus is the state the app shows in its footer.
type HealthStatus struct {
	Version   string `json:"version"`
	Connected bool   `json:"connected"`
	ReadOnly  bool   `json:"readOnly"`
	AI        struct {
		Agent      config.ProviderConfig `json:"agent"`
		Classifier config.ProviderConfig `json:"classifier"`
	} `json:"ai"`
}

func (c *Client) Health(ctx context.Context) (HealthStatus, error) {
	if c == nil || c.opts.Engine == nil {
		return HealthStatus{}, ErrOffline
	}
	cfg := c.config()
	h := HealthStatus{Version: c.opts.Version, Connected: c.opts.Engine.Connected(), ReadOnly: cfg.Mailbox.ReadOnly}
	h.AI.Agent, h.AI.Classifier = cfg.AI.Agent, cfg.AI.Classifier
	return h, nil
}

func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var res struct {
		Accounts []Account `json:"accounts"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "accounts.list", nil, &res)
	return res.Accounts, err
}

func (c *Client) Folders(ctx context.Context, accountID string) ([]Folder, error) {
	var res struct {
		Folders []Folder `json:"folders"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "folders.list", map[string]any{"accountId": strings.TrimSpace(accountID)}, &res)
	return res.Folders, err
}

func (c *Client) Messages(ctx context.Context, f MessagesFilter) (MessagesPage, error) {
	params := map[string]any{}
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			params[k] = v
		}
	}
	set("accountId", f.AccountID)
	set("folderId", f.FolderID)
	set("q", f.Query)
	set("cursor", f.Cursor)
	if f.Unread != nil {
		params["unread"] = *f.Unread
	}
	if f.UnreadOrFlagged {
		params["unreadOrFlagged"] = true
	}
	if f.SnoozedOnly {
		params["snoozedOnly"] = true
	}
	if f.ExcludeSnoozed {
		params["excludeSnoozed"] = true
	}
	if f.Days > 0 {
		params["days"] = f.Days
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}
	var res MessagesPage
	err := c.call(ctx, 45*time.Second, "messages.list", params, &res)
	return res, err
}

// Message returns a mail without decrypting it. Use MessageWithOptions{Decrypt: true} only for
// an explicit user action on that mail.
func (c *Client) Message(ctx context.Context, id, format string) (MessageDetail, error) {
	return c.MessageWithOptions(ctx, id, MessageOptions{Format: format})
}

func (c *Client) MessageWithOptions(ctx context.Context, id string, opts MessageOptions) (MessageDetail, error) {
	var res MessageDetail
	err := c.call(ctx, DefaultRequestTimeout, "messages.get", map[string]any{
		"id": id, "format": strings.TrimSpace(opts.Format), "decrypt": opts.Decrypt,
	}, &res)
	return res, err
}

// MessageThread returns the conversation of a mail, oldest first.
func (c *Client) MessageThread(ctx context.Context, id string, limit int) ([]triage.MessageInput, error) {
	if limit <= 0 {
		limit = 20
	}
	var res struct {
		Messages []triage.MessageInput `json:"messages"`
	}
	err := c.call(ctx, 45*time.Second, "messages.thread", map[string]any{"id": id, "limit": min(limit, 50)}, &res)
	return res.Messages, err
}

// Conversation returns a mail's conversation for the reading pane, newest mail first, without
// full bodies.
func (c *Client) Conversation(ctx context.Context, id string, limit int) (Conversation, error) {
	var res Conversation
	err := c.call(ctx, DefaultRequestTimeout, "messages.conversation", map[string]any{"id": id, "limit": limit}, &res)
	return res, err
}

// errReadOnly answers writes while the mailbox is read-only. The UI hides those actions too; this
// check also covers the copilot and anything else built on the client.
func (c *Client) errReadOnly() error {
	if c.config().Mailbox.ReadOnly {
		return &APIError{Code: "read_only", Message: "read_only"}
	}
	return nil
}

func (c *Client) MessageActions(ctx context.Context, id string, req MessageActionsRequest) (json.RawMessage, error) {
	if err := c.errReadOnly(); err != nil {
		return nil, err
	}
	params, err := withField(req, "id", id)
	if err != nil {
		return nil, err
	}
	var res json.RawMessage
	err = c.call(ctx, DefaultRequestTimeout, "messages.actions", params, &res)
	return res, err
}

// BatchMessageActions applies one action across many messages over batched IMAP connections.
func (c *Client) BatchMessageActions(ctx context.Context, req BatchActionsRequest) (BatchActionsResponse, error) {
	if err := c.errReadOnly(); err != nil {
		return BatchActionsResponse{}, err
	}
	var res BatchActionsResponse
	err := c.call(ctx, 90*time.Second, "messages.batchActions", req, &res)
	return res, err
}

// MarkAllRead marks all unread messages in a folder (or across accounts if folderID is empty) as read.
func (c *Client) MarkAllRead(ctx context.Context, accountID, folderID string) (int, error) {
	if err := c.errReadOnly(); err != nil {
		return 0, err
	}
	var res struct {
		OK      bool `json:"ok"`
		Updated int  `json:"updated"`
	}
	err := c.call(ctx, 60*time.Second, "messages.markAllRead", map[string]any{
		"accountId": strings.TrimSpace(accountID),
		"folderId":  strings.TrimSpace(folderID),
	}, &res)
	return res.Updated, err
}

// SearchServerMessages runs an IMAP server-side TEXT search (matching bodies not yet downloaded locally).
func (c *Client) SearchServerMessages(ctx context.Context, f MessagesFilter) (MessagesPage, error) {
	params := map[string]any{
		"accountId": strings.TrimSpace(f.AccountID),
		"folderId":  strings.TrimSpace(f.FolderID),
		"q":         strings.TrimSpace(f.Query),
	}
	if f.Limit > 0 {
		params["limit"] = f.Limit
	}
	var res MessagesPage
	err := c.call(ctx, 60*time.Second, "messages.searchServer", params, &res)
	return res, err
}

// UndoMove restores previously moved/trashed/archived messages back to their original folder.
func (c *Client) UndoMove(ctx context.Context, req UndoMoveRequest) (UndoMoveResponse, error) {
	if err := c.errReadOnly(); err != nil {
		return UndoMoveResponse{}, err
	}
	var res UndoMoveResponse
	err := c.call(ctx, 60*time.Second, "messages.undoMove", req, &res)
	return res, err
}

// withField returns v's JSON fields plus key=value.
func withField(v any, key string, value any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m[key] = value
	return m, nil
}

func (c *Client) ComposeDraft(ctx context.Context, req ComposeRequest) (json.RawMessage, error) {
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = NewIdempotencyKey()
	}
	if err := c.errReadOnly(); err != nil {
		return nil, err
	}
	var res json.RawMessage
	err := c.call(ctx, DefaultRequestTimeout, "compose.draft", req, &res)
	return res, err
}

// DiscardDraft deletes a draft that a discarded compose window auto-saved. The engine only
// deletes mails in a Drafts folder.
func (c *Client) DiscardDraft(ctx context.Context, accountID, draftID string) error {
	if err := c.errReadOnly(); err != nil {
		return err
	}
	return c.call(ctx, DefaultRequestTimeout, "compose.discardDraft", map[string]any{"accountId": accountID, "draftId": draftID}, nil)
}

// ComposeSendTimeout bounds sending, including slow SMTP servers.
const ComposeSendTimeout = 185 * time.Second

func (c *Client) ComposeSend(ctx context.Context, req ComposeRequest) (json.RawMessage, error) {
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = NewIdempotencyKey()
	}
	if err := c.errReadOnly(); err != nil {
		return nil, err
	}
	var res json.RawMessage
	err := c.call(ctx, ComposeSendTimeout, "compose.send", req, &res)
	return res, err
}

func (c *Client) CreateFolder(ctx context.Context, accountID, name string) error {
	if err := c.errReadOnly(); err != nil {
		return err
	}
	return c.call(ctx, DefaultRequestTimeout, "folders.create", map[string]any{"accountId": accountID, "name": name}, nil)
}

func (c *Client) RenameFolder(ctx context.Context, accountID, folderID, newName string) error {
	if err := c.errReadOnly(); err != nil {
		return err
	}
	return c.call(ctx, DefaultRequestTimeout, "folders.rename", map[string]any{"accountId": accountID, "folderId": folderID, "newName": newName}, nil)
}

func (c *Client) DeleteFolder(ctx context.Context, accountID, folderID string) error {
	if err := c.errReadOnly(); err != nil {
		return err
	}
	return c.call(ctx, DefaultRequestTimeout, "folders.delete", map[string]any{"accountId": accountID, "folderId": folderID}, nil)
}

func (c *Client) OutboxList(ctx context.Context) ([]OutboxEntry, error) {
	var res struct {
		Items []OutboxEntry `json:"items"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "outbox.list", nil, &res)
	return res.Items, err
}

func (c *Client) OutboxFlush(ctx context.Context) (int, int, error) {
	if err := c.errReadOnly(); err != nil {
		return 0, 0, err
	}
	var res struct {
		Sent      int `json:"sent"`
		Remaining int `json:"remaining"`
	}
	err := c.call(ctx, ComposeSendTimeout, "outbox.flush", nil, &res)
	return res.Sent, res.Remaining, err
}

// OutboxSendNow sends one outbox entry at once (a "send later" mail before its time, too).
func (c *Client) OutboxSendNow(ctx context.Context, id string) error {
	if err := c.errReadOnly(); err != nil {
		return err
	}
	return c.call(ctx, ComposeSendTimeout, "outbox.sendNow", map[string]any{"id": id}, nil)
}

// OutboxToDrafts takes an entry out of the outbox and saves it as a draft again.
func (c *Client) OutboxToDrafts(ctx context.Context, id string) error {
	if err := c.errReadOnly(); err != nil {
		return err
	}
	return c.call(ctx, ComposeSendTimeout, "outbox.toDrafts", map[string]any{"id": id}, nil)
}

func (c *Client) OutboxDelete(ctx context.Context, id string) error {
	if err := c.errReadOnly(); err != nil {
		return err
	}
	return c.call(ctx, DefaultRequestTimeout, "outbox.delete", map[string]any{"id": id}, nil)
}

func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	var res struct {
		Calendars []Calendar `json:"calendars"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "calendar.list", nil, &res)
	return res.Calendars, err
}

func (c *Client) CalendarEvents(ctx context.Context, start, end string, calendarIDs []string, limit int) (CalendarEventsResponse, error) {
	params := map[string]any{}
	if start != "" {
		params["start"] = start
	}
	if end != "" {
		params["end"] = end
	}
	var ids []string
	for _, id := range calendarIDs {
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		params["calendarIds"] = ids
	}
	if limit > 0 {
		params["limit"] = limit
	}
	var res CalendarEventsResponse
	err := c.call(ctx, 45*time.Second, "calendar.events", params, &res)
	return res, err
}

func (c *Client) CreateCalendarEvent(ctx context.Context, req CreateCalendarEventRequest) (CalendarEvent, error) {
	var res struct {
		Event CalendarEvent `json:"event"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "calendar.create", req, &res)
	return res.Event, err
}

func (c *Client) UpdateCalendarEvent(ctx context.Context, calendarID, eventID string, patch map[string]any) (CalendarEvent, error) {
	params := map[string]any{}
	for k, v := range patch {
		params[k] = v
	}
	params["calendarId"], params["id"] = calendarID, eventID
	var res struct {
		Event CalendarEvent `json:"event"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "calendar.update", params, &res)
	return res.Event, err
}

// DeleteCalendarEvent deletes an event; notify sends its attendees a cancellation. inviteErr
// says why that cancellation could not be sent (the event is deleted regardless).
func (c *Client) DeleteCalendarEvent(ctx context.Context, calendarID, eventID, recurrenceID, scope string, notify bool) (inviteErr string, err error) {
	if scope != "" && scope != ScopeOccurrence && scope != ScopeSeries {
		return "", &APIError{Code: "invalid_argument", Detail: "scope must be occurrence or series", Message: "invalid_argument: scope must be occurrence or series"}
	}
	params := map[string]any{"calendarId": calendarID, "id": eventID}
	if recurrenceID != "" {
		params["recurrenceId"] = recurrenceID
	}
	if scope != "" {
		params["scope"] = scope
	}
	if notify {
		params["notify"] = true
	}
	var res struct {
		InviteError string `json:"inviteError"`
	}
	err = c.call(ctx, DefaultRequestTimeout, "calendar.delete", params, &res)
	return res.InviteError, err
}

func (c *Client) ContactsSearch(ctx context.Context, query string) (ContactsSearchResponse, error) {
	params := map[string]any{}
	if q := strings.TrimSpace(query); q != "" {
		params["q"] = q
	}
	var res ContactsSearchResponse
	err := c.call(ctx, DefaultRequestTimeout, "contacts.search", params, &res)
	return res, err
}

func (c *Client) ContactUpsert(ctx context.Context, req ContactUpsertRequest) (ContactUpsertResponse, error) {
	var res ContactUpsertResponse
	err := c.call(ctx, DefaultRequestTimeout, "contacts.upsert", req, &res)
	return res, err
}

func (c *Client) FiltersList(ctx context.Context, accountID string) (FilterListResponse, error) {
	params := map[string]any{}
	if acc := strings.TrimSpace(accountID); acc != "" {
		params["accountId"] = acc
	}
	var res FilterListResponse
	err := c.call(ctx, DefaultRequestTimeout, "filters.list", params, &res)
	return res, err
}

func (c *Client) FilterUpsert(ctx context.Context, req FilterUpsertRequest) (FilterUpsertResponse, error) {
	if err := c.errReadOnly(); err != nil {
		return FilterUpsertResponse{}, err
	}
	var res FilterUpsertResponse
	err := c.call(ctx, DefaultRequestTimeout, "filters.upsert", req, &res)
	return res, err
}

func (c *Client) FilterRun(ctx context.Context, accountID string, filterNames []string) (FilterRunResponse, error) {
	if err := c.errReadOnly(); err != nil {
		return FilterRunResponse{}, err
	}
	var res FilterRunResponse
	err := c.call(ctx, 90*time.Second, "filters.run", map[string]any{"accountId": accountID, "filterNames": filterNames}, &res)
	return res, err
}

func (c *Client) jobs() (*jobs.Runner, error) {
	if c == nil || c.opts.Jobs == nil {
		return nil, errors.New("jobs unavailable")
	}
	return c.opts.Jobs, nil
}

// StartScan starts a triage scan; the result arrives as event "triage.scan.finished".
func (c *Client) StartScan(ctx context.Context, accountID string) (string, error) {
	r, err := c.jobs()
	if err != nil {
		return "", err
	}
	return r.StartScan(strings.TrimSpace(accountID), "manual")
}

func (c *Client) ArchiveStats(ctx context.Context) (archive.ArchiveStats, error) {
	r, err := c.jobs()
	if err != nil {
		return archive.ArchiveStats{}, err
	}
	return r.ArchiveStats()
}

// ArchivePreviewTimeout covers listing old mails across many folders.
const ArchivePreviewTimeout = 150 * time.Second

func (c *Client) ArchivePreview(ctx context.Context, req jobs.ArchiveRequest) (jobs.ArchivePreview, error) {
	r, err := c.jobs()
	if err != nil {
		return jobs.ArchivePreview{}, err
	}
	if strings.TrimSpace(req.AccountID) == "" {
		return jobs.ArchivePreview{}, &APIError{Code: "invalid_argument", Message: "invalid_argument: accountId is required"}
	}
	ctx, cancel := withDefaultDeadline(ctx, ArchivePreviewTimeout)
	defer cancel()
	return r.PreviewArchive(ctx, req)
}

// StartArchive starts an archive run; progress and the report arrive as events.
func (c *Client) StartArchive(ctx context.Context, req jobs.ArchiveRequest) (string, error) {
	r, err := c.jobs()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(req.AccountID) == "" {
		return "", &APIError{Code: "invalid_argument", Message: "invalid_argument: accountId is required"}
	}
	return r.StartArchive(req)
}

func (c *Client) AIProviders(ctx context.Context, includeModels bool, backend string) (AIProvidersResponse, error) {
	cfg := c.config()
	out := AIProvidersResponse{Providers: ai.SupportedProviders(cfg.AI.Agent.Backend, cfg.AI.Classifier.Backend), Agent: cfg.AI.Agent, Classifier: cfg.AI.Classifier}
	if includeModels {
		// The endpoint always comes from config.json; a request can only pick the backend.
		if strings.TrimSpace(backend) == "" {
			backend = cfg.AI.Agent.Backend
		}
		models, err := ai.ListModelsFor(ctx, cfg.AI, backend, c.opts.AnyLLMOpts...)
		if err != nil {
			out.ModelsError = err.Error()
		} else {
			out.Models = models
		}
	}
	return out, nil
}

// ErrAssistantDisabled is returned while the AI assistant is switched off.
const ErrAssistantDisabled = "feature_disabled:assistant"

func (c *Client) assistantAllowed() (config.Config, error) {
	cfg := c.config()
	if !cfg.Features.Assistant {
		return cfg, &APIError{Code: "feature_disabled", Detail: "assistant", Message: ErrAssistantDisabled}
	}
	return cfg, nil
}

func (c *Client) AIAnalyze(ctx context.Context, req ai.AnalyzeRequest) (ai.AnalyzeResponse, error) {
	cfg, err := c.assistantAllowed()
	if err != nil {
		return ai.AnalyzeResponse{}, err
	}
	if req.Language == "" && (cfg.Language == "de" || cfg.Language == "en") {
		req.Language = cfg.Language
	}
	ctx, cancel := withDefaultDeadline(ctx, analyzeTimeout)
	defer cancel()
	return ai.Analyze(ctx, cfg.AI, req)
}

func (c *Client) SnoozeMessage(ctx context.Context, msgID string, until time.Time) error {
	if err := c.errReadOnly(); err != nil {
		return err
	}
	var untilUnix int64
	if !until.IsZero() {
		untilUnix = until.Unix()
	}
	return c.call(ctx, DefaultRequestTimeout, "messages.snooze", map[string]any{
		"id":        strings.TrimSpace(msgID),
		"untilUnix": untilUnix,
	}, nil)
}

func (c *Client) WakeExpiredSnoozed(ctx context.Context) ([]MessageHeader, error) {
	if err := c.errReadOnly(); err != nil {
		return nil, err
	}
	var res struct {
		Messages []MessageHeader `json:"messages"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "messages.wakeSnoozed", nil, &res)
	return res.Messages, err
}

func (c *Client) CountSnoozed(ctx context.Context, accountID string) (int, error) {
	var res struct {
		Count int `json:"count"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "messages.snoozedCount", map[string]any{
		"accountId": strings.TrimSpace(accountID),
	}, &res)
	return res.Count, err
}

func (c *Client) ListWaitingReplyMessages(ctx context.Context, accountID string) ([]MessageHeader, error) {
	var res struct {
		Messages []MessageHeader `json:"messages"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "messages.waitingReply", map[string]any{
		"accountId": strings.TrimSpace(accountID),
	}, &res)
	return res.Messages, err
}

func (c *Client) ListSubscriptions(ctx context.Context, accountID string) ([]SubscriptionSummary, error) {
	var res struct {
		Subscriptions []SubscriptionSummary `json:"subscriptions"`
	}
	err := c.call(ctx, DefaultRequestTimeout, "messages.subscriptions", map[string]any{
		"accountId": strings.TrimSpace(accountID),
	}, &res)
	return res.Subscriptions, err
}
