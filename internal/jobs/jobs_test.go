package jobs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/triage"
)

// fakeTB answers the add-on's host methods and records them.
type fakeTB struct {
	mu       sync.Mutex
	calls    []string
	params   []map[string]any
	messages []triage.MessageInput
	cands    []ArchiveCandidate
	rawErr   error
	finish   error
}

func (f *fakeTB) Connected() bool { return true }

func (f *fakeTB) CallHost(_ context.Context, method string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	f.mu.Lock()
	f.calls = append(f.calls, method)
	f.params = append(f.params, p)
	f.mu.Unlock()
	switch method {
	case "host.triage.collect":
		return json.Marshal(collected{Messages: f.messages, ScannedFolders: 1})
	case "host.archive.candidates":
		return json.Marshal(candidateList{Candidates: f.cands, TotalMatched: len(f.cands), ScannedFolders: 1, HasLocalAccount: true, LocalAccountName: "Lokal"})
	case "host.archive.raw":
		if f.rawErr != nil {
			return nil, f.rawErr
		}
		eml := "From: a@b.de\r\nSubject: " + p["subject"].(string) + "\r\nMessage-ID: <" + p["headerMessageId"].(string) + ">\r\n\r\nHallo\r\n"
		return json.Marshal(rawMessage{MessageID: p["messageId"].(string), Base64: base64.StdEncoding.EncodeToString([]byte(eml)), ByteLength: int64(len(eml))})
	case "host.archive.finish":
		if f.finish != nil {
			return nil, f.finish
		}
		return json.RawMessage(`{"ok":true}`), nil
	}
	return nil, errors.New("invalid_argument: unknown host method " + method)
}

func (f *fakeTB) called(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

type events struct {
	mu   sync.Mutex
	list []string
	data []any
}

func (e *events) Publish(name string, data any) {
	e.mu.Lock()
	e.list = append(e.list, name)
	e.data = append(e.data, data)
	e.mu.Unlock()
}

func (e *events) last(name string) (any, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.list) - 1; i >= 0; i-- {
		if e.list[i] == name {
			return e.data[i], true
		}
	}
	return nil, false
}

func testRunner(t *testing.T, cfg config.Config, tb *fakeTB) (*Runner, *events, *triage.Store) {
	t.Helper()
	ev := &events{}
	store := triage.NewStoreAt(t.TempDir())
	r := New(context.Background(), tb, ev, store, func() (config.Config, error) { return cfg, nil })
	r.classify = func(_ context.Context, _ config.AIConfig, m triage.MessageInput, base triage.Classification) (triage.Classification, error) {
		base.Stage = "stage1-llm"
		base.Summary = "model: " + m.BodyForAI()
		return base, nil
	}
	return r, ev, store
}

func triageConfig() config.Config {
	cfg := config.Default()
	cfg.Features.Triage = true
	cfg.Mailbox.TriageAccountIDs = []string{"acc1"}
	cfg.AI.ClassifyWithAI = true
	cfg.AI.MaxAICallsPerScan = 10
	return cfg
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestScanClassifiesWithoutDecrypting(t *testing.T) {
	tb := &fakeTB{messages: []triage.MessageInput{
		{ID: "1", ThreadID: "a@x", AccountID: "acc1", Subject: "Angebot", From: "kunde@firma.de", BodyText: "Bitte Angebot bis Freitag", Unread: true},
		{ID: "2", ThreadID: "b@x", AccountID: "acc1", Subject: "Vertrag", From: "anwalt@kanzlei.de", Encrypted: true, Unread: true},
		{ID: "3", ThreadID: "c@x", AccountID: "other", Subject: "Fremd", From: "x@y.de", BodyText: "nicht erlaubt"},
	}}
	r, ev, store := testRunner(t, triageConfig(), tb)

	id, err := r.StartScan("", "manual")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !r.ScanStatus().Running && r.ScanStatus().Last != nil })
	rep := r.ScanStatus().Last
	if rep.ScanID != id || rep.ScannedMessages != 3 || rep.Classified != 2 || rep.Encrypted != 1 || rep.AICalls != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if _, ok := ev.last("triage.scan.finished"); !ok {
		t.Fatal("no finished event")
	}
	if got := tb.params[0]; got["max"] != float64(maxScanMessages) {
		t.Errorf("collect params = %v", got)
	}
	st, _ := store.Load()
	if len(st.Classifications) != 2 || st.Classifications["3"].MessageID != "" {
		t.Fatalf("stored = %v", st.Classifications)
	}
	// The model saw the placeholder, never content, for the encrypted mail.
	if got := st.Classifications["2"].Summary; got != "model: "+triage.EncryptedPlaceholder {
		t.Errorf("encrypted summary = %q", got)
	}

	// A second scan reuses model results by Message-ID instead of calling the model again.
	rep2, err := r.Scan(context.Background(), "scan-2", "", "manual")
	if err != nil || rep2.AICalls != 0 || rep2.Classified != 2 {
		t.Fatalf("rescan = %+v err=%v", rep2, err)
	}
}

func TestScanRefusals(t *testing.T) {
	cfg := triageConfig()
	cfg.Features.Triage = false
	r, _, _ := testRunner(t, cfg, &fakeTB{})
	if _, err := r.StartScan("", "manual"); !errors.Is(err, ErrTriageDisabled) {
		t.Errorf("triage off: %v", err)
	}
	cfg = triageConfig()
	cfg.Mailbox.TriageAccountIDs = nil
	r, _, _ = testRunner(t, cfg, &fakeTB{})
	if _, err := r.StartScan("", "manual"); !errors.Is(err, ErrNoTriageAccounts) {
		t.Errorf("no accounts: %v", err)
	}
	r, _, _ = testRunner(t, triageConfig(), &fakeTB{})
	if _, err := r.StartScan("acc9", "manual"); !errors.Is(err, ErrAccountNotEnabled) {
		t.Errorf("foreign account: %v", err)
	}
	r.mu.Lock()
	r.scanning = true
	r.mu.Unlock()
	if _, err := r.StartScan("", "manual"); !errors.Is(err, ErrBusy) {
		t.Errorf("busy: %v", err)
	}
}

func TestNewMailScansOnlyWhenAsked(t *testing.T) {
	tb := &fakeTB{messages: []triage.MessageInput{{ID: "1", AccountID: "acc1", Subject: "Neu", Unread: true}}}
	cfg := triageConfig()
	cfg.Mailbox.ScanOnNewMail = false
	r, _, _ := testRunner(t, cfg, tb)
	r.NewMail()
	time.Sleep(50 * time.Millisecond)
	r.mu.Lock()
	scheduled := r.newMailTimer != nil
	r.mu.Unlock()
	if scheduled {
		t.Fatal("scanOnNewMail is off: nothing may be scheduled")
	}

	cfg.Mailbox.ScanOnNewMail = true
	r, ev, _ := testRunner(t, cfg, tb)
	r.scheduleNewMailScan(10 * time.Millisecond)
	r.scheduleNewMailScan(10 * time.Millisecond) // a burst gives one scan
	waitFor(t, func() bool { _, ok := ev.last("triage.scan.finished"); return ok })
	if rep := r.ScanStatus().Last; rep == nil || rep.Reason != "new_mail" {
		t.Fatalf("report = %+v", rep)
	}
	if n := tb.called("host.triage.collect"); n != 1 {
		t.Errorf("collect called %d times, want 1", n)
	}
}

func archiveSetup(t *testing.T, readOnly bool) (*Runner, *fakeTB, *events, string) {
	t.Helper()
	cfg := triageConfig()
	cfg.Mailbox.ReadOnly = readOnly
	cfg.Mailbox.ArchiveDir = filepath.Join(t.TempDir(), "Archiv")
	tb := &fakeTB{cands: []ArchiveCandidate{
		{MessageID: "11", HeaderMessageID: "old1@x", AccountID: "acc1", AccountName: "Work", FolderID: "f1", FolderPath: "INBOX", Subject: "Alte Rechnung", From: "a@b.de", Date: "2024-02-03T10:00:00Z", Size: 120},
		{MessageID: "12", HeaderMessageID: "old2@x", AccountID: "acc1", AccountName: "Work", FolderID: "f1", FolderPath: "INBOX", Subject: "Altes Angebot", From: "c@d.de", Date: "2024-03-04T10:00:00Z", Size: 80},
	}}
	r, ev, _ := testRunner(t, cfg, tb)
	return r, tb, ev, cfg.Mailbox.ArchiveDir
}

func TestArchiveSimulateTouchesNothing(t *testing.T) {
	r, tb, _, dir := archiveSetup(t, true)
	p, err := r.PreviewArchive(context.Background(), ArchiveRequest{AccountID: "acc1"})
	if err != nil || p.TotalCount != 2 || p.TotalBytes != 200 || !p.HasLocalAccount || !p.ReadOnly || p.ArchiveDir != dir {
		t.Fatalf("preview = %+v err=%v", p, err)
	}
	rep, err := r.RunArchive(context.Background(), "run-1", ArchiveRequest{AccountID: "acc1", Mode: ModeSimulate})
	if err != nil || !rep.DryRun || rep.Exported != 2 || len(rep.Failed) != 0 {
		t.Fatalf("simulate = %+v err=%v", rep, err)
	}
	if tb.called("host.archive.raw") != 0 || tb.called("host.archive.finish") != 0 {
		t.Fatal("simulate must not read or change mails")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("simulate must not write files")
	}
}

func TestArchiveDeletesOnlyAfterVerifiedBackup(t *testing.T) {
	// Read-only refuses every mode that changes the mailbox.
	r, _, _, _ := archiveSetup(t, true)
	for _, mode := range []string{ModeExportAndLocal, ModeExportAndDelete} {
		if _, err := r.StartArchive(ArchiveRequest{AccountID: "acc1", Mode: mode}); !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s in read-only: %v", mode, err)
		}
	}
	if _, err := r.StartArchive(ArchiveRequest{AccountID: "acc1", Mode: "wipe"}); err == nil || !strings.HasPrefix(err.Error(), "invalid_argument") {
		t.Errorf("unknown mode: %v", err)
	}

	r, tb, ev, dir := archiveSetup(t, false)
	runID, err := r.StartArchive(ArchiveRequest{AccountID: "acc1", Mode: ModeExportAndDelete})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, ok := ev.last("archive.finished"); return ok })
	rep := r.ArchiveStatus().Last
	if rep == nil || rep.RunID != runID || rep.Exported != 2 || rep.Deleted != 2 || len(rep.Failed) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if tb.called("host.archive.finish") != 2 {
		t.Fatalf("finish calls = %d", tb.called("host.archive.finish"))
	}
	for _, it := range rep.Items {
		if !strings.HasPrefix(it.TargetPath, dir) {
			t.Errorf("file outside the archive dir: %s", it.TargetPath)
		}
		if _, err := os.Stat(it.TargetPath); err != nil {
			t.Errorf("missing backup %s: %v", it.TargetPath, err)
		}
	}
	if prog, ok := ev.last("archive.progress"); !ok || prog.(map[string]any)["done"] != 2 {
		t.Errorf("progress = %v", prog)
	}

	// A mail that cannot be read is neither backed up nor deleted.
	r, tb, _, _ = archiveSetup(t, false)
	tb.rawErr = errors.New("message_moved_meanwhile")
	rep2, err := r.RunArchive(context.Background(), "run-2", ArchiveRequest{AccountID: "acc1", Mode: ModeExportAndDelete})
	if err != nil || rep2.Deleted != 0 || len(rep2.Failed) != 2 || tb.called("host.archive.finish") != 0 {
		t.Fatalf("unreadable mails: %+v err=%v finish=%d", rep2, err, tb.called("host.archive.finish"))
	}

	// A refused delete leaves a verified backup and says so.
	r, tb, _, _ = archiveSetup(t, false)
	tb.finish = errors.New("read_only")
	rep3, _ := r.RunArchive(context.Background(), "run-3", ArchiveRequest{AccountID: "acc1", Mode: ModeExportAndDelete})
	if rep3.Deleted != 0 || len(rep3.Failed) != 2 || !strings.Contains(rep3.Failed[0].Error, "backup verified, mailbox unchanged") {
		t.Fatalf("refused finish: %+v", rep3)
	}
}
