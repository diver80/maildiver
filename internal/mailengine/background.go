package mailengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/diver80/maildiver/internal/backend"
)

// Mail while the app is not open (phones): the system wakes the app now and then, it syncs the
// inboxes and reports what arrived since the user last had the app open. "Arrived" is tracked
// per inbox by IMAP UID (a watermark in kv_meta): UIDs grow with every mail the server takes in,
// so older mails fetched later (backfill) never count as new, while mails moved into the inbox
// do.

// SyncInboxes syncs the INBOX of every enabled account in parallel.
func (e *Engine) SyncInboxes(ctx context.Context) error {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for _, a := range e.loadConfig().Direct.Accounts {
		if !a.Enabled {
			continue
		}
		accID := a.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.SyncAccount(ctx, accID, true); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// SyncBackgroundExtras flushes any queued Outbox emails and refreshes calendars during WorkManager wakes.
func (e *Engine) SyncBackgroundExtras(ctx context.Context) {
	_, _ = e.FlushOutbox(ctx)
	_ = e.SyncCalendarsNow(ctx)
}

// maxNewInboxMail bounds the mails one wake-up reports.
const maxNewInboxMail = 50

// TakeNewInboxMail returns the unread INBOX mails stored since the last call or MarkInboxSeen,
// newest first, and moves the watermark past them. An inbox without a watermark gets one and
// reports nothing, so a first run does not announce the whole mailbox.
func (e *Engine) TakeNewInboxMail(ctx context.Context) ([]StoredMessage, error) {
	return e.store.takeNewInboxMail(ctx, true)
}

// MarkInboxSeen moves every inbox's watermark past what is stored: the user has the app open
// and saw new mail there.
func (e *Engine) MarkInboxSeen(ctx context.Context) error {
	_, err := e.store.takeNewInboxMail(ctx, false)
	return err
}

func (s *Store) takeNewInboxMail(ctx context.Context, collect bool) ([]StoredMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, COALESCE(MAX(m.uid), 0) FROM folders f
		LEFT JOIN messages m ON m.folder_id = f.id WHERE f.type = 'inbox' GROUP BY f.id`)
	if err != nil {
		return nil, err
	}
	type inbox struct {
		id     string
		maxUID int64
	}
	var inboxes []inbox
	for rows.Next() {
		var in inbox
		if err := rows.Scan(&in.id, &in.maxUID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		inboxes = append(inboxes, in)
	}
	_ = rows.Close()

	var out []StoredMessage
	for _, in := range inboxes {
		key := "notified_uid:" + in.id
		var raw string
		err := s.db.QueryRowContext(ctx, `SELECT v FROM kv_meta WHERE k=?`, key).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		mark, _ := strconv.ParseInt(raw, 10, 64)
		if collect && raw != "" && in.maxUID > mark && len(out) < maxNewInboxMail {
			msgRows, err := s.db.QueryContext(ctx, `
				SELECT id, account_id, folder_id, folder_path, uid, message_id, thread_id, in_reply_to, references_hdr,
				       subject, from_addr, to_json, cc_json, date_iso, date_unix, read, flagged, size,
				       '', '', body_fetched, '[]', '{}', NULL, has_ics, '', snippet
				FROM messages WHERE folder_id=? AND uid>? AND read=0 ORDER BY uid DESC LIMIT ?`,
				in.id, mark, maxNewInboxMail-len(out))
			if err != nil {
				return nil, err
			}
			for msgRows.Next() {
				m, err := scanStoredMessage(msgRows)
				if err != nil {
					_ = msgRows.Close()
					return nil, err
				}
				out = append(out, m)
			}
			_ = msgRows.Close()
		}
		if raw == "" || in.maxUID > mark {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO kv_meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`,
				key, strconv.FormatInt(in.maxUID, 10)); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Calendar reminders while the app is not open: the same wake-up reports the events about to
// start. Which occurrences were reminded of – by the wake-up or by the open app – is kept in
// kv_meta, so neither reminds of one twice.

const remindedEventsKey = "reminded_events"

// reminderHorizon is how far ahead events are expanded for reminders: the longest reminder
// (cleanReminderMinutes) and a day.
const reminderHorizon = 8 * 24 * time.Hour

// EventReminderKey names one occurrence of an event in the reminded set.
func EventReminderKey(ev backend.CalendarEvent) string {
	return ev.ID + "|" + ev.RecurrenceID + "|" + ev.Start
}

// RemindedEvents returns the keys (EventReminderKey) of the occurrences reminded of already.
func (e *Engine) RemindedEvents(ctx context.Context) (map[string]bool, error) {
	e.store.remindMu.Lock()
	defer e.store.remindMu.Unlock()
	set, err := e.store.remindedEvents(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(set))
	for k := range set {
		out[k] = true
	}
	return out, nil
}

// MarkEventsReminded records occurrences the open app reminded of.
func (e *Engine) MarkEventsReminded(ctx context.Context, evs []backend.CalendarEvent) error {
	if len(evs) == 0 {
		return nil
	}
	e.store.remindMu.Lock()
	defer e.store.remindMu.Unlock()
	set, err := e.store.remindedEvents(ctx)
	if err != nil {
		return err
	}
	for _, ev := range evs {
		set[EventReminderKey(ev)] = parseFlexibleEventTime(ev.Start, 0).Unix()
	}
	return e.store.saveRemindedEvents(ctx, set, time.Now())
}

// TakeDueEventReminders returns the timed events starting after now whose reminder is due by
// now+within – within being the time to the next wake-up, so a reminder never falls between two
// – that were not reminded of yet, and records them. Events with the reminder off, all-day
// events and those of skipCalendars are left out.
func (e *Engine) TakeDueEventReminders(ctx context.Context, now time.Time, within time.Duration, skipCalendars []string) ([]backend.CalendarEvent, error) {
	raw, err := e.store.ListRawEvents(ctx, nil)
	if err != nil {
		return nil, err
	}
	skip := make(map[string]bool, len(skipCalendars))
	for _, id := range skipCalendars {
		skip[id] = true
	}
	e.store.remindMu.Lock()
	defer e.store.remindMu.Unlock()
	set, err := e.store.remindedEvents(ctx)
	if err != nil {
		return nil, err
	}
	var due []backend.CalendarEvent
	for _, ev := range ExpandEventsInWindow(raw, now, now.Add(reminderHorizon), 0) {
		if ev.AllDay || ev.ID == "" || skip[ev.CalendarID] {
			continue
		}
		lead, ok := ev.ReminderLead()
		if !ok {
			continue
		}
		start := parseFlexibleEventTime(ev.Start, 0)
		until := start.Sub(now)
		key := EventReminderKey(ev)
		if until <= 0 || until > max(lead, within) || set[key] != 0 {
			continue
		}
		set[key] = start.Unix()
		due = append(due, ev)
	}
	if len(due) == 0 {
		return nil, nil
	}
	if err := e.store.saveRemindedEvents(ctx, set, now); err != nil {
		return nil, err
	}
	return due, nil
}

// remindedEvents reads the reminded set: key → start (unix), for dropping past ones.
func (s *Store) remindedEvents(ctx context.Context) (map[string]int64, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM kv_meta WHERE k=?`, remindedEventsKey).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	set := map[string]int64{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &set)
	}
	return set, nil
}

// saveRemindedEvents stores the set without occurrences that started over two days ago.
func (s *Store) saveRemindedEvents(ctx context.Context, set map[string]int64, now time.Time) error {
	cutoff := now.Add(-48 * time.Hour).Unix()
	for k, start := range set {
		if start < cutoff {
			delete(set, k)
		}
	}
	b, err := json.Marshal(set)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO kv_meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, remindedEventsKey, string(b))
	return err
}
