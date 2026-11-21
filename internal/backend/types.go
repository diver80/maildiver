// Package backend is the app's access to the mail engine, the AI layer and the jobs, all in
// the same process: typed calls instead of the former localhost HTTP bridge.
package backend

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"

	"github.com/diver80/maildiver/internal/ai"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/triage"
)

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
	"read_only": true, "account_not_enabled": true, "feature_disabled": true, "cross_account_move": true,
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
	UnreadCount int      `json:"unreadCount"`
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
	// ThreadCount is the number of mails in this mail's conversation (lists only; 0 = unknown).
	ThreadCount int `json:"threadCount,omitempty"`
}

// ConversationMessage is one mail of a conversation: its header and the start of its text.
type ConversationMessage struct {
	MessageHeader
	Snippet string `json:"snippet,omitempty"`
}

// Conversation is a mail's conversation, newest mail first.
type Conversation struct {
	ThreadID string                `json:"threadId"`
	Messages []ConversationMessage `json:"messages"`
}

type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	PartName    string `json:"partName"`
}

type MessageDetail struct {
	MessageHeader
	BCC         []string          `json:"bcc,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	BodyText    string            `json:"bodyText"`
	BodyHtml    string            `json:"bodyHtml,omitempty"`
	Security    MessageSecurity   `json:"security"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	HasICS      bool              `json:"hasIcs,omitempty"`
	ICSInvite   *CalendarEvent    `json:"icsInvite,omitempty"`
}

// MessageSecurity says whether a mail is encrypted and signed. Signatures are not verified:
// Signed only means the mail carries one.
type MessageSecurity struct {
	Encrypted bool `json:"encrypted"`
	// Decryption: "none" (not encrypted), "success", "fail" (no key, cancelled) or "skipped"
	// (read without decrypting).
	Decryption       string `json:"decryption,omitempty"`
	Signed           bool   `json:"signed"`
	SMIME            bool   `json:"smime,omitempty"`
	SignatureValid   bool   `json:"signatureValid,omitempty"`
	SignatureTrusted bool   `json:"signatureTrusted,omitempty"`
	SignerEmail      string `json:"signerEmail,omitempty"`
	SignerSubject    string `json:"signerSubject,omitempty"`
	SignerIssuer     string `json:"signerIssuer,omitempty"`
	SignatureError   string `json:"signatureError,omitempty"`
	Error            string `json:"error,omitempty"`
}

// Readable reports that the content is there: not encrypted, or decrypted.
func (s MessageSecurity) Readable() bool {
	return !s.Encrypted || s.Decryption == "success"
}

type MessagesFilter struct {
	AccountID       string
	FolderID        string
	Query           string
	Unread          *bool
	UnreadOrFlagged bool
	Days            int
	Limit           int
	Cursor          string
}

type MessagesPage struct {
	Messages   []MessageHeader `json:"messages"`
	Total      int             `json:"total"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type MessageOptions struct {
	Format  string // "text" (default), "html" or "both"
	Decrypt bool
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
	// Archive files the mail in the account's archive folder (the identity's archive folder and
	// yearly/monthly layout). Use at most one of Trash, Archive and MoveTo*.
	Archive bool `json:"archive,omitempty"`
}

// RemovesFromFolder reports whether the action takes the mail out of its current folder.
func (r MessageActionsRequest) RemovesFromFolder() bool {
	return r.Trash || r.Archive || r.MoveToPath != "" || r.MoveToFolderID != ""
}

// SourceAttachment references an attachment already stored in an existing message
// (e.g. when reopening a draft or forwarding a message with attachments).
type SourceAttachment struct {
	MessageID string `json:"messageId"`
	PartName  string `json:"partName"`
	Name      string `json:"name"`
}

// BatchActionItem identifies one target message for messages.batchActions.
type BatchActionItem struct {
	ID                string `json:"id"`
	ExpectedMessageID string `json:"expectedMessageId,omitempty"`
}

// BatchActionsRequest applies one MessageActionsRequest across many messages in batched IMAP calls.
type BatchActionsRequest struct {
	Items  []BatchActionItem     `json:"items"`
	Action MessageActionsRequest `json:"action"`
}

// BatchActionsResponse reports per-item outcomes and resolved target folder paths (for Undo).
type BatchActionsResponse struct {
	OK              bool              `json:"ok"`
	OKIDs           []string          `json:"okIds,omitempty"`
	Failed          int               `json:"failed,omitempty"`
	FirstError      string            `json:"firstError,omitempty"`
	MovedTargetPath map[string]string `json:"movedTargetPath,omitempty"` // msgID -> target IMAP folder path
}

// UndoMoveItem describes one moved/trashed/archived message to restore to its previous folder.
type UndoMoveItem struct {
	AccountID      string `json:"accountId"`
	MessageID      string `json:"messageId"` // RFC Message-ID (without <>)
	FromFolderPath string `json:"fromFolderPath"`
	ToFolderPath   string `json:"toFolderPath"`
}

// UndoMoveRequest moves messages back from ToFolderPath to FromFolderPath on IMAP.
type UndoMoveRequest struct {
	Items []UndoMoveItem `json:"items"`
}

// UndoMoveResponse reports how many messages were restored.
type UndoMoveResponse struct {
	OK       bool `json:"ok"`
	Restored int  `json:"restored"`
}

type ComposeRequest struct {
	AccountID         string             `json:"accountId,omitempty"`
	IdentityID        string             `json:"identityId,omitempty"`
	FromEmail         string             `json:"fromEmail,omitempty"`
	FromName          string             `json:"fromName,omitempty"`
	DraftMessageID    string             `json:"draftMessageId,omitempty"`
	To                []string           `json:"to,omitempty"`
	CC                []string           `json:"cc,omitempty"`
	BCC               []string           `json:"bcc,omitempty"`
	Subject           string             `json:"subject,omitempty"`
	Body              string             `json:"body,omitempty"`
	BodyHTML          string             `json:"bodyHtml,omitempty"`
	IsHTML            bool               `json:"isHtml,omitempty"`
	Attachments       []string           `json:"attachments,omitempty"`
	SourceAttachments []SourceAttachment `json:"sourceAttachments,omitempty"`
	ReplyToMessageID  string             `json:"replyToMessageId,omitempty"`
	ReplyType         string             `json:"replyType,omitempty"`
	InReplyTo         string             `json:"inReplyTo,omitempty"`
	References        string             `json:"references,omitempty"`
	OpenWindow        bool               `json:"openWindow,omitempty"`
	// Sign: "on", "off" or "" (the identity's default). A reply to an encrypted
	// mail is always encrypted; a silent draft of it opens its window instead (Reason
	// "encrypted_reply" in the result).
	Sign    string `json:"sign,omitempty"`
	Encrypt bool   `json:"encrypt,omitempty"`
	// IdempotencyKey deduplicates retries (contract §5). ComposeDraft/ComposeSend fill in a
	// random key when empty; callers that may retry should set a stable one.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// ComposeResult is the part of a compose.draft / compose.send result the app shows.
type ComposeResult struct {
	WindowOpened bool   `json:"windowOpened"`
	MessageID    string `json:"messageId,omitempty"`
	DraftID      string `json:"draftId,omitempty"`
	// Reason "encrypted_reply": a reply to an encrypted mail was opened for editing instead of
	// being saved silently, so that it stays encrypted.
	Reason   string           `json:"reason,omitempty"`
	Security *ComposeSecurity `json:"security,omitempty"`
	// SentCopyFailed: the mail went out, but its copy could not be stored in the Sent folder.
	SentCopyFailed bool `json:"sentCopyFailed,omitempty"`
}

// ComposeSecurity is what was applied: Technology "OpenPGP" or "S/MIME", "" without a key.
type ComposeSecurity struct {
	Technology string `json:"technology"`
	Signed     bool   `json:"signed"`
	Encrypted  bool   `json:"encrypted"`
}

// ParseComposeResult reads a compose result; unknown or empty input gives the zero value.
func ParseComposeResult(raw json.RawMessage) ComposeResult {
	var res ComposeResult
	_ = json.Unmarshal(raw, &res)
	return res
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
	RecurrenceID   string   `json:"recurrenceId,omitempty"`
	RecurrenceRule string   `json:"rrule,omitempty"`
	IsRecurring    bool     `json:"isRecurring,omitempty"`
	Organizer      string   `json:"organizer,omitempty"`
	Attendees      []string `json:"attendees,omitempty"`
}

type CalendarEventsResponse struct {
	Supported bool            `json:"supported"`
	Events    []CalendarEvent `json:"events"`
}

type CreateCalendarEventRequest struct {
	CalendarID     string `json:"calendarId,omitempty"`
	Title          string `json:"title"`
	Start          string `json:"start"`
	End            string `json:"end,omitempty"`
	AllDay         bool   `json:"allDay,omitempty"`
	Location       string `json:"location,omitempty"`
	Description    string `json:"description,omitempty"`
	RecurrenceRule string `json:"rrule,omitempty"`
}

// Calendar delete/update scopes (contract §7).
const (
	ScopeOccurrence = "occurrence"
	ScopeSeries     = "series"
)

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

type TriageStateResponse struct {
	State      triage.State      `json:"state"`
	BarSummary triage.BarSummary `json:"barSummary"`
}

type AIProvidersResponse struct {
	Providers   []ai.ProviderDescriptor `json:"providers"`
	Agent       config.ProviderConfig   `json:"agent"`
	Classifier  config.ProviderConfig   `json:"classifier"`
	Models      []anyllm.Model          `json:"models,omitempty"`
	ModelsError string                  `json:"modelsError,omitempty"`
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
