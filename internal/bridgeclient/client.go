package bridgeclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/omathunder/internal/ai"
	"github.com/diver80/omathunder/internal/bridge"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/triage"
)

const (
	// DefaultRequestTimeout applies to a request whose context carries no deadline.
	DefaultRequestTimeout = 35 * time.Second
	// AIRequestTimeout applies to AI calls (analyze/chat) whose context carries no deadline.
	// The bridge allows ~110 s for an analysis, so the client must wait longer than that.
	AIRequestTimeout = 125 * time.Second
)

// Client talks to the authenticated OmaThunder localhost REST + SSE bridge.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New creates a Client for a known bridge URL and Bearer token.
//
// The underlying http.Client has no global timeout (it would also cut long AI calls and
// the SSE stream). Each request gets a deadline from its context, or DefaultRequestTimeout /
// AIRequestTimeout when the context has none.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:      strings.TrimSpace(token),
		httpClient: &http.Client{},
	}
}

// ErrOffline is returned when no bridge client is available (Thunderbird/host not running,
// bridge disabled, or the connection was lost).
var ErrOffline = errors.New("bridge_offline")

// SameEndpoint reports whether c and other talk to the same bridge URL with the same token.
func (c *Client) SameEndpoint(other *Client) bool {
	if c == nil || other == nil {
		return c == other
	}
	return c.baseURL == other.baseURL && c.token == other.token
}

// APIError is a failed bridge call. Code is the machine-readable error code the extension /
// bridge put in front of the message ("read_only", "message_changed", "outcome_unknown", …),
// or "" when the message carries none.
type APIError struct {
	Status  int
	Code    string
	Detail  string
	Message string
	Err     error // underlying transport error (e.g. context.DeadlineExceeded), if any
}

func (e *APIError) Error() string { return e.Message }
func (e *APIError) Unwrap() error { return e.Err }

// knownErrorCodes are the codes of the shared RPC contract (§2) plus client-side ones.
var knownErrorCodes = map[string]bool{
	"read_only": true, "account_not_enabled": true, "account_not_readable": true,
	"send_disabled": true, "feature_disabled": true, "cross_account_move": true,
	"not_found": true, "invalid_argument": true, "ambiguous": true,
	"message_changed": true, "conflict": true, "outcome_unknown": true,
}

// ParseErrorCode splits "code: detail" / "feature_disabled:name" into a known code and its detail.
// It returns ("", msg) when msg does not start with a known code.
func ParseErrorCode(msg string) (code, detail string) {
	m := strings.TrimSpace(msg)
	head, rest, found := strings.Cut(m, ":")
	head = strings.TrimSpace(head)
	if knownErrorCodes[head] {
		if found {
			return head, strings.TrimSpace(rest)
		}
		return head, ""
	}
	if !found && knownErrorCodes[m] {
		return m, ""
	}
	return "", m
}

// ErrorCode returns the bridge error code of err ("" if none). A client-side timeout of a
// mutating request reports "outcome_unknown".
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	code, _ := ParseErrorCode(err.Error())
	return code
}

// IsOutcomeUnknown reports whether a mutating request may or may not have been applied
// (server-side 504 "outcome_unknown" or a client-side timeout/cancel after sending).
func IsOutcomeUnknown(err error) bool {
	return ErrorCode(err) == "outcome_unknown"
}

// NewIdempotencyKey returns a random key for compose.draft / compose.send (contract §5).
func NewIdempotencyKey() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("omt-%d", time.Now().UnixNano())
	}
	return "omt-" + hex.EncodeToString(buf[:])
}

// ContentIdempotencyKey derives a stable key from a session nonce and the content, so a retry of
// the same content reuses the key while edited content gets a new one.
func ContentIdempotencyKey(nonce string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(nonce))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return "omt-" + hex.EncodeToString(h.Sum(nil)[:16])
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

// Discover reads bridge.json (or config.BridgeInfoPath when path is empty) and returns a configured Client.
func Discover(path string) (*Client, bridge.DiscoveryInfo, error) {
	info, err := bridge.ReadDiscoveryFile(path)
	if err != nil {
		return nil, bridge.DiscoveryInfo{}, err
	}
	if info.URL == "" || info.Token == "" {
		return nil, info, fmt.Errorf("bridge discovery file %q missing url or token", path)
	}
	return New(info.URL, info.Token), info, nil
}

func (c *Client) BaseURL() string { return c.baseURL }
func (c *Client) Token() string   { return c.token }

type apiEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// DoJSON performs an authenticated JSON request. When ctx has no deadline,
// DefaultRequestTimeout applies.
func (c *Client) DoJSON(ctx context.Context, method, path string, reqBody any, out any) error {
	return c.doJSON(ctx, DefaultRequestTimeout, method, path, reqBody, out)
}

func (c *Client) doJSON(ctx context.Context, timeout time.Duration, method, path string, reqBody any, out any) error {
	if c == nil {
		return ErrOffline
	}
	ctx, cancel := withDefaultDeadline(ctx, timeout)
	defer cancel()
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	mutating := method != http.MethodGet && method != http.MethodHead
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return transportError(ctx, mutating, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return transportError(ctx, mutating, err)
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		if resp.StatusCode >= 400 {
			msg := fmt.Sprintf("bridge HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
			code, detail := ParseErrorCode(strings.TrimSpace(string(raw)))
			return &APIError{Status: resp.StatusCode, Code: code, Detail: detail, Message: msg}
		}
		return err
	}
	if !env.OK || resp.StatusCode >= 400 {
		msg := env.Error
		if msg == "" {
			msg = fmt.Sprintf("bridge HTTP %d", resp.StatusCode)
		}
		code, detail := ParseErrorCode(msg)
		if code == "" && resp.StatusCode == http.StatusGatewayTimeout && mutating {
			code = "outcome_unknown"
		}
		return &APIError{Status: resp.StatusCode, Code: code, Detail: detail, Message: msg}
	}
	if out == nil || len(env.Result) == 0 {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// transportError classifies a failed round trip. A mutating request that hit its deadline (or
// was cancelled) after it may have reached the bridge has an unknown outcome.
func transportError(ctx context.Context, mutating bool, err error) error {
	if mutating && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil) {
		return &APIError{Code: "outcome_unknown", Message: "outcome_unknown: " + err.Error(), Err: err}
	}
	return err
}

// OpenStream opens an authenticated SSE / streaming response to the bridge (no deadline;
// the stream lives as long as ctx).
func (c *Client) OpenStream(ctx context.Context, method, path string, reqBody any) (*http.Response, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "text/event-stream")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	streamClient := &http.Client{}
	return streamClient.Do(req)
}

type HealthStatus struct {
	Version              string              `json:"version"`
	Protocol             int                 `json:"protocol"`
	PID                  int                 `json:"pid"`
	URL                  string              `json:"url"`
	StartedAt            time.Time           `json:"startedAt"`
	ThunderbirdConnected bool                `json:"thunderbirdConnected"`
	Bridge               config.BridgeConfig `json:"bridge"`
	ReadOnly             bool                `json:"readOnly"`
	AI                   struct {
		Agent      config.ProviderConfig `json:"agent"`
		Classifier config.ProviderConfig `json:"classifier"`
	} `json:"ai"`
}

func (c *Client) Health(ctx context.Context) (HealthStatus, error) {
	var res HealthStatus
	err := c.DoJSON(ctx, http.MethodGet, "/v1/health", nil, &res)
	return res, err
}

type AccountIdentity struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type Account struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Identities []AccountIdentity `json:"identities,omitempty"`
}

type Folder struct {
	ID          string   `json:"id"`
	AccountID   string   `json:"accountId"`
	AccountName string   `json:"accountName,omitempty"`
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Type        string   `json:"type,omitempty"`
	SpecialUse  []string `json:"specialUse,omitempty"`
}

type MessageHeader struct {
	ID string `json:"id"`
	// MessageID is the RFC Message-ID without <> (contract §4); used as expectedMessageId.
	MessageID string   `json:"messageId,omitempty"`
	ThreadID  string   `json:"threadId"`
	AccountID string   `json:"accountId"`
	FolderID  string   `json:"folderId"`
	Folder    string   `json:"folder"`
	InInbox   bool     `json:"inInbox"`
	Subject   string   `json:"subject"`
	From      string   `json:"from"`
	To        []string `json:"to"`
	CC        []string `json:"cc,omitempty"`
	Date      string   `json:"date"`
	Read      bool     `json:"read"`
	Unread    bool     `json:"unread"`
	Flagged   bool     `json:"flagged"`
	Tags      []string `json:"tags"`
	Size      int64    `json:"size"`
	External  bool     `json:"external,omitempty"`
}

type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	PartName    string `json:"partName"`
}

type MessageDetail struct {
	MessageHeader
	Headers     map[string]string `json:"headers,omitempty"`
	BodyText    string            `json:"bodyText"`
	BodyHtml    string            `json:"bodyHtml,omitempty"`
	Attachments []Attachment      `json:"attachments,omitempty"`
}

type MessagesFilter struct {
	AccountID string
	FolderID  string
	Query     string
	Unread    *bool
	Days      int
	Limit     int
	Cursor    string
}

type MessagesPage struct {
	Messages   []MessageHeader `json:"messages"`
	Total      int             `json:"total"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var res struct {
		Accounts []Account `json:"accounts"`
	}
	if err := c.DoJSON(ctx, http.MethodGet, "/v1/accounts", nil, &res); err != nil {
		return nil, err
	}
	return res.Accounts, nil
}

func (c *Client) Folders(ctx context.Context, accountID string) ([]Folder, error) {
	var res struct {
		Folders []Folder `json:"folders"`
	}
	q := ""
	if accountID != "" {
		q = "?accountId=" + url.QueryEscape(accountID)
	}
	if err := c.DoJSON(ctx, http.MethodGet, "/v1/folders"+q, nil, &res); err != nil {
		return nil, err
	}
	return res.Folders, nil
}

func (c *Client) Messages(ctx context.Context, f MessagesFilter) (MessagesPage, error) {
	vals := url.Values{}
	if f.AccountID != "" {
		vals.Set("accountId", f.AccountID)
	}
	if f.FolderID != "" {
		vals.Set("folderId", f.FolderID)
	}
	if f.Query != "" {
		vals.Set("q", f.Query)
	}
	if f.Unread != nil {
		vals.Set("unread", strconv.FormatBool(*f.Unread))
	}
	if f.Days > 0 {
		vals.Set("days", strconv.Itoa(f.Days))
	}
	if f.Limit > 0 {
		vals.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Cursor != "" {
		vals.Set("cursor", f.Cursor)
	}
	path := "/v1/messages"
	if enc := vals.Encode(); enc != "" {
		path += "?" + enc
	}
	var res MessagesPage
	err := c.DoJSON(ctx, http.MethodGet, path, nil, &res)
	return res, err
}

func (c *Client) Message(ctx context.Context, id, format string) (MessageDetail, error) {
	path := "/v1/messages/" + url.PathEscape(id)
	if format != "" {
		path += "?format=" + url.QueryEscape(format)
	}
	var res MessageDetail
	err := c.DoJSON(ctx, http.MethodGet, path, nil, &res)
	return res, err
}

func (c *Client) MessageThread(ctx context.Context, id string, limit int) ([]triage.MessageInput, error) {
	path := "/v1/messages/" + url.PathEscape(id) + "/thread"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var res struct {
		Messages []triage.MessageInput `json:"messages"`
	}
	if err := c.DoJSON(ctx, http.MethodGet, path, nil, &res); err != nil {
		return nil, err
	}
	return res.Messages, nil
}

type MessageActionsRequest struct {
	// ExpectedMessageID makes the extension reject the action with "message_changed" when the
	// mail found under the ID has a different Message-ID (contract §4).
	ExpectedMessageID string   `json:"expectedMessageId,omitempty"`
	Read              *bool    `json:"read,omitempty"`
	Flagged           *bool    `json:"flagged,omitempty"`
	AddTags           []string `json:"addTags,omitempty"`
	RemoveTags        []string `json:"removeTags,omitempty"`
	MoveToFolderID    string   `json:"moveToFolderId,omitempty"`
	MoveToPath        string   `json:"moveToPath,omitempty"`
	Trash             bool     `json:"trash,omitempty"`
	// Archive files the mail with Thunderbird's own archive (the identity's archive folder and
	// yearly/monthly layout). Use at most one of Trash, Archive and MoveTo*.
	Archive bool `json:"archive,omitempty"`
}

// RemovesFromFolder reports whether the action takes the mail out of its current folder.
func (r MessageActionsRequest) RemovesFromFolder() bool {
	return r.Trash || r.Archive || r.MoveToPath != "" || r.MoveToFolderID != ""
}

func (c *Client) MessageActions(ctx context.Context, id string, req MessageActionsRequest) (json.RawMessage, error) {
	var res json.RawMessage
	err := c.DoJSON(ctx, http.MethodPost, "/v1/messages/"+url.PathEscape(id)+"/actions", req, &res)
	return res, err
}

type ComposeRequest struct {
	AccountID        string   `json:"accountId,omitempty"`
	IdentityID       string   `json:"identityId,omitempty"`
	To               []string `json:"to,omitempty"`
	CC               []string `json:"cc,omitempty"`
	BCC              []string `json:"bcc,omitempty"`
	Subject          string   `json:"subject,omitempty"`
	Body             string   `json:"body,omitempty"`
	IsHTML           bool     `json:"isHtml,omitempty"`
	ReplyToMessageID string   `json:"replyToMessageId,omitempty"`
	ReplyType        string   `json:"replyType,omitempty"`
	OpenWindow       bool     `json:"openWindow,omitempty"`
	// IdempotencyKey deduplicates retries (contract §5). ComposeDraft/ComposeSend fill in a
	// random key when empty; callers that may retry should set a stable one.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

func (c *Client) ComposeDraft(ctx context.Context, req ComposeRequest) (json.RawMessage, error) {
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = NewIdempotencyKey()
	}
	var res json.RawMessage
	err := c.DoJSON(ctx, http.MethodPost, "/v1/compose/draft", req, &res)
	return res, err
}

func (c *Client) ComposeSend(ctx context.Context, req ComposeRequest) (json.RawMessage, error) {
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = NewIdempotencyKey()
	}
	var res json.RawMessage
	err := c.DoJSON(ctx, http.MethodPost, "/v1/compose/send", req, &res)
	return res, err
}

type Calendar struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	ReadOnly bool   `json:"readOnly"`
	Color    string `json:"color,omitempty"`
}

type CalendarEvent struct {
	ID          string `json:"id"`
	CalendarID  string `json:"calendarId"`
	Calendar    string `json:"calendarName,omitempty"`
	Title       string `json:"title"`
	Start       string `json:"start"`
	End         string `json:"end"`
	AllDay      bool   `json:"allDay"`
	Location    string `json:"location,omitempty"`
	Description string `json:"description,omitempty"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
	// RecurrenceID identifies one occurrence of a recurring series ("" for single events and
	// series masters); IsRecurring marks events that belong to a series (contract §7).
	RecurrenceID string `json:"recurrenceId,omitempty"`
	IsRecurring  bool   `json:"isRecurring,omitempty"`
}

type CalendarEventsResponse struct {
	Supported bool            `json:"supported"`
	Events    []CalendarEvent `json:"events"`
}

func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	var res struct {
		Supported bool       `json:"supported"`
		Calendars []Calendar `json:"calendars"`
	}
	if err := c.DoJSON(ctx, http.MethodGet, "/v1/calendars", nil, &res); err != nil {
		return nil, err
	}
	return res.Calendars, nil
}

func (c *Client) CalendarEvents(ctx context.Context, start, end string, calendarIDs []string, limit int) (CalendarEventsResponse, error) {
	vals := url.Values{}
	if start != "" {
		vals.Set("start", start)
	}
	if end != "" {
		vals.Set("end", end)
	}
	for _, id := range calendarIDs {
		if id != "" {
			vals.Add("calendarId", id)
		}
	}
	if limit > 0 {
		vals.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/calendar/events"
	if enc := vals.Encode(); enc != "" {
		path += "?" + enc
	}
	var res CalendarEventsResponse
	err := c.DoJSON(ctx, http.MethodGet, path, nil, &res)
	return res, err
}

type CreateCalendarEventRequest struct {
	CalendarID  string `json:"calendarId,omitempty"`
	Title       string `json:"title"`
	Start       string `json:"start"`
	End         string `json:"end,omitempty"`
	AllDay      bool   `json:"allDay,omitempty"`
	Location    string `json:"location,omitempty"`
	Description string `json:"description,omitempty"`
}

func (c *Client) CreateCalendarEvent(ctx context.Context, req CreateCalendarEventRequest) (CalendarEvent, error) {
	var res struct {
		Event CalendarEvent `json:"event"`
	}
	if err := c.DoJSON(ctx, http.MethodPost, "/v1/calendar/events", req, &res); err != nil {
		return CalendarEvent{}, err
	}
	return res.Event, nil
}

func (c *Client) UpdateCalendarEvent(ctx context.Context, calendarID, eventID string, patch map[string]any) (CalendarEvent, error) {
	var res struct {
		Event CalendarEvent `json:"event"`
	}
	path := "/v1/calendar/events/" + url.PathEscape(calendarID) + "/" + url.PathEscape(eventID)
	if err := c.DoJSON(ctx, http.MethodPatch, path, patch, &res); err != nil {
		return CalendarEvent{}, err
	}
	return res.Event, nil
}

// Calendar delete/update scopes (contract §7).
const (
	ScopeOccurrence = "occurrence"
	ScopeSeries     = "series"
)

// DeleteCalendarEvent deletes an event. For one occurrence of a recurring event pass its
// recurrenceID and scope ScopeOccurrence; ScopeSeries deletes the whole series. Empty values
// are omitted (the bridge then defaults to "occurrence" when recurrenceId is set, else "series").
func (c *Client) DeleteCalendarEvent(ctx context.Context, calendarID, eventID, recurrenceID, scope string) error {
	path := "/v1/calendar/events/" + url.PathEscape(calendarID) + "/" + url.PathEscape(eventID)
	vals := url.Values{}
	if recurrenceID != "" {
		vals.Set("recurrenceId", recurrenceID)
	}
	if scope != "" {
		vals.Set("scope", scope)
	}
	if enc := vals.Encode(); enc != "" {
		path += "?" + enc
	}
	return c.DoJSON(ctx, http.MethodDelete, path, nil, nil)
}

type AddressBook struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ReadOnly bool   `json:"readOnly"`
	Type     string `json:"type,omitempty"`
}

type ContactRecord struct {
	ID            string `json:"id,omitempty"`
	AddressBookID string `json:"addressBookId,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
	FirstName     string `json:"firstName,omitempty"`
	LastName      string `json:"lastName,omitempty"`
	Email         string `json:"email,omitempty"`
	SecondEmail   string `json:"secondEmail,omitempty"`
	Phone         string `json:"phone,omitempty"`
	Mobile        string `json:"mobile,omitempty"`
	Company       string `json:"company,omitempty"`
	JobTitle      string `json:"jobTitle,omitempty"`
	Website       string `json:"website,omitempty"`
	Address       string `json:"address,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

type ContactsSearchResponse struct {
	Supported    bool            `json:"supported"`
	AddressBooks []AddressBook   `json:"addressBooks"`
	Contacts     []ContactRecord `json:"contacts"`
}

type ContactUpsertRequest struct {
	ContactID     string        `json:"contactId,omitempty"`
	AddressBookID string        `json:"addressBookId,omitempty"`
	Mode          string        `json:"mode,omitempty"` // "create" | "enrich"
	Contact       ContactRecord `json:"contact"`
}

type ContactUpsertResponse struct {
	OK      bool          `json:"ok"`
	Action  string        `json:"action,omitempty"` // "imported" | "enriched"
	Contact ContactRecord `json:"contact"`
}

func (c *Client) ContactsSearch(ctx context.Context, query string) (ContactsSearchResponse, error) {
	path := "/v1/contacts"
	if q := strings.TrimSpace(query); q != "" {
		path += "?q=" + url.QueryEscape(q)
	}
	var res ContactsSearchResponse
	err := c.DoJSON(ctx, http.MethodGet, path, nil, &res)
	return res, err
}

func (c *Client) ContactUpsert(ctx context.Context, req ContactUpsertRequest) (ContactUpsertResponse, error) {
	var res ContactUpsertResponse
	err := c.DoJSON(ctx, http.MethodPost, "/v1/contacts/upsert", req, &res)
	return res, err
}

type TriageStateResponse struct {
	State      triage.State      `json:"state"`
	BarSummary triage.BarSummary `json:"barSummary"`
}

func (c *Client) TriageState(ctx context.Context) (TriageStateResponse, error) {
	var res TriageStateResponse
	err := c.DoJSON(ctx, http.MethodGet, "/v1/triage/state", nil, &res)
	return res, err
}

type AIProvidersResponse struct {
	Providers   []ai.ProviderDescriptor `json:"providers"`
	Agent       config.ProviderConfig   `json:"agent"`
	Classifier  config.ProviderConfig   `json:"classifier"`
	Models      []anyllm.Model          `json:"models,omitempty"`
	ModelsError string                  `json:"modelsError,omitempty"`
}

// AIProviders lists the AI providers (and optionally the models of backend). The endpoint is
// always taken from the host's config.json; clients never send one (contract §8).
func (c *Client) AIProviders(ctx context.Context, includeModels bool, backend string) (AIProvidersResponse, error) {
	vals := url.Values{}
	if includeModels {
		vals.Set("models", "1")
	}
	if backend != "" {
		vals.Set("backend", backend)
	}
	path := "/v1/ai/providers"
	if enc := vals.Encode(); enc != "" {
		path += "?" + enc
	}
	var res AIProvidersResponse
	err := c.DoJSON(ctx, http.MethodGet, path, nil, &res)
	return res, err
}

// AIChat runs a non-streaming chat completion on the bridge (AIRequestTimeout unless ctx has a
// deadline). The endpoint is never sent (contract §8).
func (c *Client) AIChat(ctx context.Context, req ai.ChatRequest) (*anyllm.ChatCompletion, error) {
	req.Stream = false
	req.Endpoint = ""
	var res anyllm.ChatCompletion
	if err := c.doJSON(ctx, AIRequestTimeout, http.MethodPost, "/v1/ai/chat", req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// AIAnalyze runs an AI analysis on the bridge (AIRequestTimeout unless ctx has a deadline).
func (c *Client) AIAnalyze(ctx context.Context, req ai.AnalyzeRequest) (ai.AnalyzeResponse, error) {
	var res ai.AnalyzeResponse
	err := c.doJSON(ctx, AIRequestTimeout, http.MethodPost, "/v1/ai/analyze", req, &res)
	return res, err
}

type FilterListResponse struct {
	Supported bool                  `json:"supported"`
	Filters   []triage.NativeFilter `json:"filters"`
}

type FilterActionSpec struct {
	Type             string `json:"type"` // "moveToFolder", "copyToFolder", "markRead", "delete", "stopExecution"
	TargetFolderPath string `json:"targetFolderPath,omitempty"`
	TargetFolderURI  string `json:"targetFolderUri,omitempty"`
}

type FilterUpsertRequest struct {
	Mode         string                   `json:"mode,omitempty"` // "create" (always new, name uniquified), "replace", "append_conditions", "toggle"
	AccountID    string                   `json:"accountId"`
	Name         string                   `json:"name"`
	ExistingName string                   `json:"existingName,omitempty"`
	Enabled      bool                     `json:"enabled"`
	MatchAll     bool                     `json:"matchAll"`
	CreateFolder bool                     `json:"createFolder,omitempty"`
	Conditions   []triage.NativeCondition `json:"conditions"`
	Actions      []FilterActionSpec       `json:"actions"`
}

type FilterUpsertResponse struct {
	OK     bool                `json:"ok"`
	Filter triage.NativeFilter `json:"filter"`
}

type FilterRunResponse struct {
	OK       bool `json:"ok"`
	Executed int  `json:"executed"`
}

func (c *Client) FiltersList(ctx context.Context, accountID string) (FilterListResponse, error) {
	path := "/v1/filters"
	if accountID != "" {
		path += "?accountId=" + url.QueryEscape(accountID)
	}
	var res FilterListResponse
	err := c.DoJSON(ctx, http.MethodGet, path, nil, &res)
	return res, err
}

func (c *Client) FilterUpsert(ctx context.Context, req FilterUpsertRequest) (FilterUpsertResponse, error) {
	var res FilterUpsertResponse
	err := c.DoJSON(ctx, http.MethodPost, "/v1/filters/upsert", req, &res)
	return res, err
}

func (c *Client) FilterRun(ctx context.Context, accountID string, filterNames []string) (FilterRunResponse, error) {
	var res FilterRunResponse
	err := c.DoJSON(ctx, http.MethodPost, "/v1/filters/run", map[string]any{
		"accountId":   accountID,
		"filterNames": filterNames,
	}, &res)
	return res, err
}

func (c *Client) FilterOpenDialog(ctx context.Context, accountID string) (bool, error) {
	var res struct {
		Opened bool `json:"opened"`
	}
	err := c.DoJSON(ctx, http.MethodPost, "/v1/filters/open", map[string]any{
		"accountId": accountID,
	}, &res)
	return res.Opened, err
}
