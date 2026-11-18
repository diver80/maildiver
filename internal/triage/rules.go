package triage

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/diver80/omathunder/internal/config"
)

type Bucket string

const (
	BucketIncoming   Bucket = "incoming"
	BucketAction     Bucket = "action"
	BucketWaiting    Bucket = "waiting"
	BucketInvoice    Bucket = "invoice"
	BucketEvent      Bucket = "event"
	BucketFYI        Bucket = "fyi"
	BucketNewsletter Bucket = "newsletter"
	BucketDone       Bucket = "done"
)

type MessageInput struct {
	ID          string            `json:"id"`
	ThreadID    string            `json:"threadId,omitempty"`
	AccountID   string            `json:"accountId,omitempty"`
	Folder      string            `json:"folder,omitempty"`
	FolderID    string            `json:"folderId,omitempty"`
	InInbox     bool              `json:"inInbox,omitempty"` // true when Folder is the account's INBOX
	Subject     string            `json:"subject"`
	From        string            `json:"from"`
	To          []string          `json:"to,omitempty"`
	Date        string            `json:"date"`
	Headers     map[string]string `json:"headers,omitempty"`
	BodyText    string            `json:"bodyText"`
	Snippet     string            `json:"snippet,omitempty"`
	Unread      bool              `json:"unread"`
	Attachments []string          `json:"attachments,omitempty"`
	// Encrypted: the mail is encrypted. Unless it was decrypted on an explicit request, BodyText
	// and Attachments are empty.
	Encrypted bool `json:"encrypted,omitempty"`
}

// EncryptedPlaceholder stands in for the body of an encrypted mail that was not decrypted, so a
// model knows why it sees no content.
const EncryptedPlaceholder = "[encrypted email: content not available]"

// BodyForAI returns the body to hand to a model.
func (m MessageInput) BodyForAI() string {
	if m.Encrypted && strings.TrimSpace(m.BodyText) == "" {
		return EncryptedPlaceholder
	}
	return m.BodyText
}

type Classification struct {
	MessageID      string   `json:"messageId"`
	ThreadID       string   `json:"threadId,omitempty"`
	AccountID      string   `json:"accountId,omitempty"`
	Subject        string   `json:"subject"`
	From           string   `json:"from"`
	Date           string   `json:"date"`
	Folder         string   `json:"folder,omitempty"`
	FolderID       string   `json:"folderId,omitempty"`
	Space          string   `json:"space,omitempty"`
	MatchedRuleID  string   `json:"matchedRuleId,omitempty"`
	TargetFolder   string   `json:"targetFolder,omitempty"`
	TargetFolderID string   `json:"targetFolderId,omitempty"`
	KanbanColumn   string   `json:"kanbanColumn"` // "incoming", "action", "waiting", "invoice", "done"
	PinnedColumn   bool     `json:"pinnedColumn,omitempty"`
	Bucket         Bucket   `json:"bucket"`
	Urgency        string   `json:"urgency"` // "high", "medium", "low"
	Summary        string   `json:"summary"`
	SummaryKey     string   `json:"summaryKey,omitempty"` // UI text key for locally generated summaries
	ActionHint     string   `json:"actionHint,omitempty"`
	Deadline       string   `json:"deadline,omitempty"`
	Amount         string   `json:"amount,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Stage          string   `json:"stage"` // "stage0-rule", "stage1-llm", "stage2-agent"
	Model          string   `json:"model,omitempty"`
	NeedsAttention bool     `json:"needsAttention"`
}

var (
	whitespaceRE = regexp.MustCompile(`\s+`)
	urlRE        = regexp.MustCompile(`https?://\S{60,}`)
)

// ExtractSenderDomain extracts the lowercase domain from an RFC 5322 address or raw string.
func ExtractSenderDomain(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(from); err == nil && addr.Address != "" {
		from = addr.Address
	}
	if at := strings.LastIndexByte(from, '@'); at != -1 && at+1 < len(from) {
		dom := strings.TrimSpace(from[at+1:])
		dom = strings.TrimRight(dom, "> ")
		return strings.ToLower(dom)
	}
	return ""
}

func ruleMatchesAccount(rule config.RoutingRule, accountID string) bool {
	return rule.AccountID == "" || accountID == "" || rule.AccountID == accountID
}

// MatchDomainRule finds the first rule whose domains match the sender (exact or subdomain).
func MatchDomainRule(from string, rules []config.RoutingRule) (config.RoutingRule, bool) {
	return MatchDomainRuleForAccount(from, "", rules)
}

func containsAnyFold(haystack string, needles []string) bool {
	if len(needles) == 0 {
		return true
	}
	h := strings.ToLower(haystack)
	for _, n := range needles {
		c := strings.ToLower(strings.TrimSpace(n))
		if c != "" && strings.Contains(h, c) {
			return true
		}
	}
	return false
}

func ruleMatchesMessageSenderAndSubject(r config.RoutingRule, m MessageInput) bool {
	if !ruleMatchesAccount(r, m.AccountID) {
		return false
	}
	hasDomains := false
	for _, d := range r.Domains {
		if strings.TrimSpace(strings.TrimPrefix(d, "@")) != "" {
			hasDomains = true
			break
		}
	}
	if hasDomains {
		dom := ExtractSenderDomain(m.From)
		if dom == "" {
			return false
		}
		matchedDom := false
		for _, candidate := range r.Domains {
			c := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(candidate, "@")))
			if c != "" && (dom == c || strings.HasSuffix(dom, "."+c)) {
				matchedDom = true
				break
			}
		}
		if !matchedDom {
			return false
		}
	}
	if !containsAnyFold(m.From, r.FromContains) {
		return false
	}
	if !containsAnyFold(m.Subject, r.SubjectContains) {
		return false
	}
	return hasDomains || len(r.FromContains) > 0 || len(r.SubjectContains) > 0
}

// MatchMessageRuleForAccount finds the first rule whose domain, sender, and subject conditions match m.
func MatchMessageRuleForAccount(m MessageInput, rules []config.RoutingRule) (config.RoutingRule, bool) {
	for _, r := range rules {
		if ruleMatchesMessageSenderAndSubject(r, m) {
			return r, true
		}
	}
	return config.RoutingRule{}, false
}

// MatchDomainRuleForAccount finds the first rule matching both sender domain and account scope.
func MatchDomainRuleForAccount(from, accountID string, rules []config.RoutingRule) (config.RoutingRule, bool) {
	return MatchMessageRuleForAccount(MessageInput{From: from, AccountID: accountID}, rules)
}

// MatchRoutingRule finds the Space for a mail: first by sender/subject rule, then by the folder it lives in.
// It labels mails for the board only; auto-moves use PlanAutoMoves, which is stricter.
func MatchRoutingRule(from, currentFolder string, rules []config.RoutingRule) (config.RoutingRule, bool) {
	return MatchRoutingRuleForAccount(from, currentFolder, "", rules)
}

// MatchRoutingRuleForAccount finds the Space for a mail scoped to accountID.
func MatchRoutingRuleForAccount(from, currentFolder, accountID string, rules []config.RoutingRule) (config.RoutingRule, bool) {
	return MatchRoutingRuleForMessage(MessageInput{From: from, Folder: currentFolder, AccountID: accountID}, rules)
}

// MatchRoutingRuleForMessage finds the rule for a full message (matching rules first, then folder path).
func MatchRoutingRuleForMessage(msg MessageInput, rules []config.RoutingRule) (config.RoutingRule, bool) {
	if r, ok := MatchMessageRuleForAccount(msg, rules); ok {
		return r, true
	}
	if msg.Folder != "" {
		curLower := strings.ToLower(strings.Trim(strings.TrimSpace(msg.Folder), "/"))
		for _, r := range rules {
			if !ruleMatchesAccount(r, msg.AccountID) {
				continue
			}
			fp := strings.ToLower(strings.Trim(strings.TrimSpace(r.FolderPath), "/"))
			if fp != "" && (curLower == fp || strings.HasPrefix(curLower, fp+"/")) {
				return r, true
			}
		}
	}
	return config.RoutingRule{}, false
}

// BucketToKanban maps a classification bucket onto one of the 5 Kanban columns.
func BucketToKanban(b Bucket, unread bool) string {
	switch b {
	case BucketAction:
		return "action"
	case BucketWaiting:
		return "waiting"
	case BucketInvoice, BucketEvent:
		return "invoice"
	case BucketDone:
		return "done"
	case BucketIncoming:
		return "incoming"
	default:
		if unread {
			return "incoming"
		}
		return "done"
	}
}

// FolderKanbanColumn maps subfolder names like todo / waiting / done to a Kanban column.
// Other subfolders (e.g. "Reise", "Teams 26", "verification") return ("", false) so normal classification applies.
func FolderKanbanColumn(folderPath string) (string, bool) {
	clean := strings.Trim(strings.TrimSpace(folderPath), "/")
	if clean == "" {
		return "", false
	}
	parts := strings.Split(clean, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		seg := strings.ToLower(strings.TrimSpace(parts[i]))
		if i == 0 && seg == "inbox" {
			continue
		}
		switch seg {
		case "todo", "to-do", "to do", "action":
			return "action", true
		case "waiting", "wartet":
			return "waiting", true
		case "done", "erledigt":
			return "done", true
		}
	}
	return "", false
}

// ApplyFolderKanbanColumn overrides KanbanColumn when the mail sits in a todo/waiting/done folder.
func ApplyFolderKanbanColumn(c *Classification, folderPath string) bool {
	col, ok := FolderKanbanColumn(folderPath)
	if !ok {
		return false
	}
	c.KanbanColumn = col
	switch col {
	case "action":
		c.NeedsAttention = true
		if c.Bucket == "" || c.Bucket == BucketDone || c.Bucket == BucketWaiting || c.Bucket == BucketFYI || c.Bucket == BucketIncoming {
			c.Bucket = BucketAction
		}
	case "waiting":
		c.NeedsAttention = false
		if c.Bucket == "" || c.Bucket == BucketDone || c.Bucket == BucketAction || c.Bucket == BucketFYI || c.Bucket == BucketIncoming {
			c.Bucket = BucketWaiting
		}
	case "done":
		c.NeedsAttention = false
		if c.Bucket == "" || c.Bucket == BucketAction || c.Bucket == BucketWaiting || c.Bucket == BucketIncoming {
			c.Bucket = BucketDone
		}
	}
	return true
}

// EvaluateStage0 applies zero-token deterministic rules before invoking any LLM.
func EvaluateStage0(msg MessageInput, skipNewsletters bool, rules []config.RoutingRule) (Classification, bool) {
	base := Classification{
		MessageID: msg.ID,
		ThreadID:  msg.ThreadID,
		AccountID: msg.AccountID,
		Subject:   msg.Subject,
		From:      msg.From,
		Date:      msg.Date,
		Folder:    msg.Folder,
		FolderID:  msg.FolderID,
		Stage:     "stage0-rule",
		Model:     "deterministic",
	}

	if rule, ok := MatchRoutingRuleForMessage(msg, rules); ok {
		base.Space = rule.Space
		base.MatchedRuleID = rule.ID
		base.TargetFolder = rule.FolderPath
		base.TargetFolderID = rule.FolderID
	}

	h := normalizeHeaders(msg.Headers)
	fromLower := strings.ToLower(msg.From)
	subjLower := strings.ToLower(msg.Subject)

	// 1. Newsletters & mailing lists
	if skipNewsletters && (h["list-unsubscribe"] != "" || h["list-id"] != "" ||
		strings.Contains(h["precedence"], "bulk") || strings.Contains(h["precedence"], "list")) {
		base.Bucket = BucketNewsletter
		base.KanbanColumn = "done"
		base.Urgency = "low"
		base.Summary = "Mailingliste / Newsletter (Header-Regel)"
		base.SummaryKey = "sum_newsletter"
		base.Tags = []string{"oma-newsletter"}
		base.NeedsAttention = false
		ApplyFolderKanbanColumn(&base, msg.Folder)
		return base, true
	}

	// 2. Automated notifications (CI, bots, out-of-office). Security and transaction mails
	// (password resets, verification codes, orders, payments) are never downgraded here:
	// they go on to Stage 1 like any personal mail. A bare "noreply@" sender is not enough either.
	automated := strings.Contains(h["auto-submitted"], "auto-") || strings.Contains(fromLower, "notifications@github.com")
	if automated && !looksLikeInvoice(subjLower, msg.Attachments) && !looksTransactional(subjLower) {
		base.Bucket = BucketFYI
		base.KanbanColumn = BucketToKanban(BucketFYI, msg.Unread)
		base.Urgency = "low"
		base.Summary = "Automatische Benachrichtigung (Stage-0-Regel)"
		base.SummaryKey = "sum_automated"
		base.Tags = []string{"oma-fyi"}
		base.NeedsAttention = false
		ApplyFolderKanbanColumn(&base, msg.Folder)
		return base, true
	}

	// 3. Invoices / receipts detected cleanly by filename or subject keywords
	if looksLikeInvoice(subjLower, msg.Attachments) {
		base.Bucket = BucketInvoice
		base.KanbanColumn = "invoice"
		base.Urgency = "medium"
		base.Summary = "Rechnung / Beleg erkannt (zur Prüfung)"
		base.SummaryKey = "sum_invoice"
		base.ActionHint = "Prüfen & ablegen"
		base.Tags = []string{"oma-invoice"}
		base.NeedsAttention = msg.Unread
		ApplyFolderKanbanColumn(&base, msg.Folder)
		return base, true
	}

	ApplyFolderKanbanColumn(&base, msg.Folder)
	return base, false
}

// PrepareSnippet strips excessive whitespace, long tracking URLs, and quoted signatures.
func PrepareSnippet(body string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 1600
	}
	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		rawLine := strings.TrimRight(line, "\r")
		if rawLine == "-- " {
			break
		}
		trimmed := strings.TrimSpace(rawLine)
		if strings.HasPrefix(trimmed, ">") {
			continue
		}
		kept = append(kept, trimmed)
	}
	cleaned := strings.Join(kept, " ")
	cleaned = urlRE.ReplaceAllString(cleaned, "[link]")
	cleaned = whitespaceRE.ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)
	if utf8.RuneCountInString(cleaned) > maxChars {
		runes := []rune(cleaned)
		return string(runes[:maxChars]) + "…"
	}
	return cleaned
}

func normalizeHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[strings.ToLower(strings.TrimSpace(k))] = strings.ToLower(strings.TrimSpace(v))
	}
	return out
}

// looksTransactional reports subjects that usually need the user's attention even when sent by a machine.
func looksTransactional(subjLower string) bool {
	keywords := []string{
		"passwort", "password", "kennwort", "verifi", "bestätig", "confirm", "sicherheit", "security",
		"anmeldung", "login", "sign-in", "sign in", "code", "bestellung", "order", "zahlung", "payment",
		"mahnung", "kündigung", "frist", "deadline", "ablauf", "expir",
	}
	for _, kw := range keywords {
		if strings.Contains(subjLower, kw) {
			return true
		}
	}
	return false
}

func looksLikeInvoice(subjLower string, attachments []string) bool {
	keywords := []string{"rechnung", "invoice", "quittung", "receipt", "zahlungsaufforderung", "mahnung"}
	for _, kw := range keywords {
		if strings.Contains(subjLower, kw) {
			return true
		}
	}
	for _, att := range attachments {
		l := strings.ToLower(att)
		if strings.Contains(l, "rechnung") || strings.Contains(l, "invoice") {
			return true
		}
	}
	return false
}
