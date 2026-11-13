package triage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/diver80/omathunder/internal/config"
)

type Proposal struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"` // "draft_reply", "add_tags", "move_folder"
	MessageID  string   `json:"messageId"`
	ThreadID   string   `json:"threadId,omitempty"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Subject    string   `json:"subject,omitempty"`
	Body       string   `json:"body,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	FolderPath string   `json:"folderPath,omitempty"`
	Status     string   `json:"status"` // "pending", "applied", "dismissed"
	CreatedAt  string   `json:"createdAt"`
}

// MovedMessage records an auto-moved email so deleting a routing rule can cleanly revert it to Incoming.
type MovedMessage struct {
	MessageID        string `json:"messageId"`
	OldMessageID     string `json:"oldMessageId,omitempty"`
	HeaderMessageID  string `json:"headerMessageId,omitempty"`
	AccountID        string `json:"accountId,omitempty"`
	RuleID           string `json:"ruleId"`
	ScanID           string `json:"scanId,omitempty"` // scan that moved the mail, for "undo last scan"
	OriginalFolder   string `json:"originalFolder"`
	OriginalFolderID string `json:"originalFolderId,omitempty"`
	TargetFolder     string `json:"targetFolder"`
	TargetFolderID   string `json:"targetFolderId,omitempty"`
	MovedAt          string `json:"movedAt"`
}

type State struct {
	UpdatedAt       string                    `json:"updatedAt"`
	Classifications map[string]Classification `json:"classifications"`
	Proposals       []Proposal                `json:"proposals"`
	MovedMessages   []MovedMessage            `json:"movedMessages"`
}

type BarSummary struct {
	UpdatedAt        string `json:"updatedAt"`
	ActionCount      int    `json:"actionCount"`
	WaitingCount     int    `json:"waitingCount"`
	InvoiceCount     int    `json:"invoiceCount"`
	EventCount       int    `json:"eventCount"`
	PendingProposals int    `json:"pendingProposals"`
	TopHeadline      string `json:"topHeadline,omitempty"`
}

type Store struct {
	mu   sync.Mutex
	dir  string
	path string
	bar  string
}

func OpenStore() (*Store, error) {
	dir, err := config.StateDir()
	if err != nil {
		return nil, err
	}
	return NewStoreAt(dir), nil
}

func NewStoreAt(dir string) *Store {
	return &Store{
		dir:  dir,
		path: filepath.Join(dir, "triage-state.json"),
		bar:  filepath.Join(dir, "bar-summary.json"),
	}
}

func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (State, error) {
	st := State{
		Classifications: map[string]Classification{},
		Proposals:       []Proposal{},
		MovedMessages:   []MovedMessage{},
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{
			Classifications: map[string]Classification{},
			Proposals:       []Proposal{},
			MovedMessages:   []MovedMessage{},
		}, nil
	}
	if st.Classifications == nil {
		st.Classifications = map[string]Classification{}
	}
	if st.Proposals == nil {
		st.Proposals = []Proposal{}
	}
	if st.MovedMessages == nil {
		st.MovedMessages = []MovedMessage{}
	}
	return st, nil
}

// findPrevClassificationLocked finds the stored card of the same mail. The numeric key only
// counts when its Message-ID matches: after a Thunderbird restart the number can belong to another mail.
func findPrevClassificationLocked(st State, c Classification) (string, Classification, bool) {
	if prev, exists := st.Classifications[c.MessageID]; exists && prev.ThreadID == c.ThreadID {
		return c.MessageID, prev, true
	}
	if c.ThreadID != "" {
		for k, prev := range st.Classifications {
			if prev.ThreadID == c.ThreadID {
				return k, prev, true
			}
		}
	}
	return "", Classification{}, false
}

func (s *Store) SaveClassifications(items []Classification) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return st, err
	}
	for _, c := range items {
		if c.MessageID == "" {
			continue
		}
		if oldKey, prev, exists := findPrevClassificationLocked(st, c); exists {
			if oldKey != c.MessageID {
				delete(st.Classifications, oldKey)
			}
			if prev.PinnedColumn && prev.KanbanColumn != "" {
				c.KanbanColumn = prev.KanbanColumn
				c.PinnedColumn = true
				c.Bucket = prev.Bucket
				c.NeedsAttention = prev.NeedsAttention
			} else if prev.KanbanColumn != "" && c.KanbanColumn == "" {
				c.KanbanColumn = prev.KanbanColumn
			}
		}
		if c.KanbanColumn == "" {
			c.KanbanColumn = BucketToKanban(c.Bucket, c.NeedsAttention)
		}
		st.Classifications[c.MessageID] = c
	}
	pruneClassifications(&st, maxClassifications)
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.writeLocked(st); err != nil {
		return st, err
	}
	return st, nil
}

// maxClassifications bounds the board so the state stays far below Thunderbird's 1 MB message limit.
const maxClassifications = 400

// pruneClassifications keeps the newest mails (by date) and drops the oldest ones beyond the limit.
// Cards the user placed manually are kept as long as possible.
func pruneClassifications(st *State, limit int) {
	if len(st.Classifications) <= limit {
		return
	}
	ids := make([]string, 0, len(st.Classifications))
	for id := range st.Classifications {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := st.Classifications[ids[i]], st.Classifications[ids[j]]
		if a.PinnedColumn != b.PinnedColumn {
			return a.PinnedColumn
		}
		return a.Date > b.Date
	})
	for _, id := range ids[limit:] {
		delete(st.Classifications, id)
	}
}

// RetainAccounts drops classifications of accounts that are no longer enabled for triage.
func (s *Store) RetainAccounts(policy config.MailboxPolicy) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return st, err
	}
	changed := false
	for id, c := range st.Classifications {
		if !policy.AccountEnabled(c.AccountID) {
			delete(st.Classifications, id)
			changed = true
		}
	}
	if !changed {
		return st, nil
	}
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return st, s.writeLocked(st)
}

func (s *Store) MoveKanban(messageID, column string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return st, err
	}
	if c, ok := st.Classifications[messageID]; ok {
		c.KanbanColumn = column
		c.PinnedColumn = true
		switch column {
		case "action":
			c.Bucket = BucketAction
			c.NeedsAttention = true
		case "waiting":
			c.Bucket = BucketWaiting
			c.NeedsAttention = false
		case "invoice":
			c.Bucket = BucketInvoice
			c.NeedsAttention = true
		case "done":
			c.Bucket = BucketDone
			c.NeedsAttention = false
		case "incoming":
			c.Bucket = BucketIncoming
			c.NeedsAttention = true
		}
		st.Classifications[messageID] = c
	}
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.writeLocked(st); err != nil {
		return st, err
	}
	return st, nil
}

func (s *Store) RecordMoves(entries []MovedMessage) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return st, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, e := range entries {
		if e.MovedAt == "" {
			e.MovedAt = now
		}
		st.MovedMessages = append(st.MovedMessages, e)

		// Migrate classification key if Thunderbird assigned a new MessageHeader.id after IMAP move
		if e.OldMessageID != "" && e.MessageID != "" {
			if c, ok := st.Classifications[e.OldMessageID]; ok {
				if e.OldMessageID != e.MessageID {
					delete(st.Classifications, e.OldMessageID)
				}
				c.MessageID = e.MessageID
				if e.TargetFolder != "" {
					c.Folder = e.TargetFolder
				}
				if e.TargetFolderID != "" {
					c.FolderID = e.TargetFolderID
				}
				st.Classifications[e.MessageID] = c
			}
		}
	}
	if len(st.MovedMessages) > 5000 {
		st.MovedMessages = st.MovedMessages[len(st.MovedMessages)-5000:]
	}
	st.UpdatedAt = now
	if err := s.writeLocked(st); err != nil {
		return st, err
	}
	return st, nil
}

// PopMovedByRule removes and returns all tracked moves associated with a ruleId (for clean rollback to Incoming).
func (s *Store) PopMovedByRule(ruleID string) ([]MovedMessage, State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return nil, st, err
	}
	var popped []MovedMessage
	kept := make([]MovedMessage, 0, len(st.MovedMessages))
	revertedByMsgID := map[string]MovedMessage{}
	revertedByHdrID := map[string]MovedMessage{}
	for _, m := range st.MovedMessages {
		if m.RuleID == ruleID {
			popped = append(popped, m)
			if m.MessageID != "" {
				revertedByMsgID[m.MessageID] = m
			}
			if m.HeaderMessageID != "" {
				revertedByHdrID[m.HeaderMessageID] = m
			}
		} else {
			kept = append(kept, m)
		}
	}
	for id, c := range st.Classifications {
		if m, ok := revertedByMsgID[id]; ok {
			c.Customer = ""
			c.MatchedRuleID = ""
			c.TargetFolder = ""
			c.TargetFolderID = ""
			if m.OriginalFolder != "" {
				c.Folder = m.OriginalFolder
			}
			if m.OriginalFolderID != "" {
				c.FolderID = m.OriginalFolderID
			}
			st.Classifications[id] = c
		} else if c.ThreadID != "" {
			if m, ok := revertedByHdrID[c.ThreadID]; ok {
				c.Customer = ""
				c.MatchedRuleID = ""
				c.TargetFolder = ""
				c.TargetFolderID = ""
				if m.OriginalFolder != "" {
					c.Folder = m.OriginalFolder
				}
				st.Classifications[id] = c
			}
		}
	}
	st.MovedMessages = kept
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.writeLocked(st); err != nil {
		return nil, st, err
	}
	return popped, st, nil
}

// PopMovedByScan removes and returns the moves one scan made, so they can be undone.
func (s *Store) PopMovedByScan(scanID string) ([]MovedMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	var popped []MovedMessage
	kept := make([]MovedMessage, 0, len(st.MovedMessages))
	for _, m := range st.MovedMessages {
		if scanID != "" && m.ScanID == scanID {
			popped = append(popped, m)
		} else {
			kept = append(kept, m)
		}
	}
	if len(popped) == 0 {
		return nil, nil
	}
	st.MovedMessages = kept
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return popped, s.writeLocked(st)
}

func (s *Store) AddProposal(p Proposal) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return st, err
	}
	if p.Status == "" {
		p.Status = "pending"
	}
	if p.CreatedAt == "" {
		p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	st.Proposals = append([]Proposal{p}, st.Proposals...)
	if len(st.Proposals) > 100 {
		st.Proposals = st.Proposals[:100]
	}
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.writeLocked(st); err != nil {
		return st, err
	}
	return st, nil
}

func (s *Store) UpdateProposalStatus(id, status string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return st, err
	}
	for i := range st.Proposals {
		if st.Proposals[i].ID == id {
			st.Proposals[i].Status = status
			break
		}
	}
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.writeLocked(st); err != nil {
		return st, err
	}
	return st, nil
}

func (s *Store) writeLocked(st State) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := writeAtomicJSON(s.path, st); err != nil {
		return err
	}
	return writeAtomicJSON(s.bar, ComputeBarSummary(st))
}

func ComputeBarSummary(st State) BarSummary {
	b := BarSummary{UpdatedAt: st.UpdatedAt}
	for _, c := range st.Classifications {
		switch c.KanbanColumn {
		case "action", "incoming":
			if c.NeedsAttention {
				b.ActionCount++
				if b.TopHeadline == "" {
					b.TopHeadline = c.Subject
				}
			}
		case "waiting":
			b.WaitingCount++
		case "invoice":
			if c.NeedsAttention {
				b.InvoiceCount++
			}
		}
	}
	for _, p := range st.Proposals {
		if p.Status == "pending" {
			b.PendingProposals++
		}
	}
	return b
}

func writeAtomicJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
