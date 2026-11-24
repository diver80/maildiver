package triage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/diver80/postdive/internal/config"
)

type Proposal struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"` // "draft_reply", "add_tags", "move_folder"
	MessageID  string   `json:"messageId"`
	ThreadID   string   `json:"threadId,omitempty"`
	AccountID  string   `json:"accountId,omitempty"`
	Space      string   `json:"space,omitempty"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Subject    string   `json:"subject,omitempty"`
	Body       string   `json:"body,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	FolderPath string   `json:"folderPath,omitempty"`
	Status     string   `json:"status"` // "pending", "applied", "dismissed"
	CreatedAt  string   `json:"createdAt"`
}

// State is the triage result the app shows. Older files with the Kanban board's proposals and
// tracked moves load fine; those parts are dropped on the next write.
type State struct {
	UpdatedAt       string                    `json:"updatedAt"`
	Classifications map[string]Classification `json:"classifications"`
}

type BarSummary struct {
	UpdatedAt    string `json:"updatedAt"`
	ActionCount  int    `json:"actionCount"`
	WaitingCount int    `json:"waitingCount"`
	InvoiceCount int    `json:"invoiceCount"`
	EventCount   int    `json:"eventCount"`
	TopHeadline  string `json:"topHeadline,omitempty"`
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
	st := State{Classifications: map[string]Classification{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return State{Classifications: map[string]Classification{}}, nil
	}
	if st.Classifications == nil {
		st.Classifications = map[string]Classification{}
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

// SaveScanBatch merges newly scanned classifications into the store. When isRead is provided,
// cards whose effective column is "done" and whose Thunderbird message is already read are omitted
// (and any previous entry for the same mail is removed) so Done stays clean.
func (s *Store) SaveScanBatch(items []Classification, isRead map[string]bool) (State, error) {
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
		if isRead != nil && c.KanbanColumn == "done" && isRead[c.MessageID] {
			delete(st.Classifications, c.MessageID)
			continue
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
const maxClassifications = 800

// pruneClassifications keeps the newest mails (by date) and drops the oldest ones beyond the limit.
// Open cards and cards the user placed manually are kept ahead of older done cards.
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
		aOpen := a.KanbanColumn != "done"
		bOpen := b.KanbanColumn != "done"
		if aOpen != bOpen {
			return aOpen
		}
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
		if !policy.TriageIncludes(c.AccountID) {
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

func (s *Store) writeLocked(st State) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := writeAtomicJSON(s.path, st); err != nil {
		return err
	}
	return writeAtomicJSON(s.bar, ComputeBarSummary(st))
}

func urgencyRank(u string) int {
	switch u {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func ComputeBarSummary(st State) BarSummary {
	b := BarSummary{UpdatedAt: st.UpdatedAt}
	items := make([]Classification, 0, len(st.Classifications))
	for _, c := range st.Classifications {
		items = append(items, c)
	}
	// Deterministic ordering so TopHeadline always picks the highest-priority / newest mail.
	sort.Slice(items, func(i, j int) bool {
		ri, rj := urgencyRank(items[i].Urgency), urgencyRank(items[j].Urgency)
		if ri != rj {
			return ri > rj
		}
		if items[i].Date != items[j].Date {
			return items[i].Date > items[j].Date
		}
		return items[i].MessageID < items[j].MessageID
	})
	for _, c := range items {
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
				if c.Bucket == BucketEvent {
					b.EventCount++
				} else {
					b.InvoiceCount++
				}
			}
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
