package bridgeclient

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/diver80/omathunder/internal/bridge"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/jobs"
	"github.com/diver80/omathunder/internal/native"
	"github.com/diver80/omathunder/internal/triage"
)

// hostTB answers the add-on's host methods for the job runner behind the bridge.
type hostTB struct{}

func (hostTB) Connected() bool { return true }
func (hostTB) CallHost(_ context.Context, method string, _ any) (json.RawMessage, error) {
	switch method {
	case "host.triage.collect":
		return json.RawMessage(`{"messages":[{"id":"1","threadId":"t@x","accountId":"acc1","subject":"Hallo","from":"a@b.de","unread":true}],"scannedFolders":1}`), nil
	case "host.archive.candidates":
		return json.RawMessage(`{"candidates":[{"messageId":"9","accountId":"acc1","accountName":"Work","folderId":"f1","folderPath":"INBOX","subject":"Alt","date":"2024-01-01T00:00:00Z","size":10}],"totalMatched":1,"scannedFolders":1}`), nil
	}
	return json.RawMessage(`{}`), nil
}

// Scans and archive runs start through the bridge, run in the host and report through events;
// bridge.accountIds and read-only apply.
func TestJobsThroughBridge(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	cfg.Features.Triage = true
	cfg.AI.ClassifyWithAI = false
	cfg.Mailbox.ReadOnly = true
	cfg.Mailbox.TriageAccountIDs = []string{"acc1", "acc2"}
	cfg.Mailbox.ArchiveDir = filepath.Join(tmp, "Archiv")
	cfg.Bridge.AccountIDs = []string{"acc1"}
	load := func() (config.Config, error) { return cfg, nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := native.NewEventHub()
	store := triage.NewStoreAt(tmp)
	runner := jobs.New(ctx, hostTB{}, hub, store, load)
	discPath := filepath.Join(tmp, "bridge.json")
	srv, err := bridge.Start(ctx, bridge.Options{DiscoveryPath: discPath, RPC: stubRPC{}, Events: hub, Store: store, ConfigLoader: load, Jobs: runner})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	client, _, err := Discover(discPath)
	if err != nil {
		t.Fatal(err)
	}

	_, events := hub.Subscribe()
	scanID, err := client.StartScan(ctx, "")
	if err != nil || scanID == "" {
		t.Fatalf("StartScan = %q, %v", scanID, err)
	}
	waitEvent(t, events, "triage.scan.finished")
	st, err := client.ScanStatus(ctx)
	if err != nil || st.Running || st.Last == nil || st.Last.ScanID != scanID || st.Last.Classified != 1 {
		t.Fatalf("ScanStatus = %+v, %v", st, err)
	}
	if ts, err := client.TriageState(ctx); err != nil || len(ts.State.Classifications) != 1 {
		t.Fatalf("triage state after scan = %+v, %v", ts, err)
	}

	// bridge.accountIds: acc2 is enabled for triage but not readable through the bridge.
	var apiErr *APIError
	if _, err := client.StartScan(ctx, "acc2"); !errors.As(err, &apiErr) || apiErr.Code != "account_not_readable" {
		t.Errorf("scan of a non-readable account: %v", err)
	}

	p, err := client.ArchivePreview(ctx, jobs.ArchiveRequest{AccountID: "acc1"})
	if err != nil || p.TotalCount != 1 || p.ArchiveDir != cfg.Mailbox.ArchiveDir {
		t.Fatalf("ArchivePreview = %+v, %v", p, err)
	}
	if _, err := client.StartArchive(ctx, jobs.ArchiveRequest{AccountID: "acc1", Mode: jobs.ModeExportAndDelete}); !errors.As(err, &apiErr) || apiErr.Code != "read_only" || apiErr.Status != 403 {
		t.Errorf("delete in read-only: %v", err)
	}
	if _, err := client.StartArchive(ctx, jobs.ArchiveRequest{Mode: jobs.ModeSimulate}); !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("archive without account: %v", err)
	}
	runID, err := client.StartArchive(ctx, jobs.ArchiveRequest{AccountID: "acc1", Mode: jobs.ModeSimulate})
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events, "archive.finished")
	as, err := client.ArchiveStatus(ctx)
	if err != nil || as.Last == nil || as.Last.RunID != runID || as.Last.Exported != 1 || !as.Last.DryRun {
		t.Fatalf("ArchiveStatus = %+v, %v", as, err)
	}
	if stats, err := client.ArchiveStats(ctx); err != nil || stats.Exists {
		t.Errorf("simulate must leave the archive dir alone: %+v, %v", stats, err)
	}
}

func waitEvent(t *testing.T, ch <-chan native.BridgeEvent, name string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Event == name {
				return
			}
			if ev.Event == "triage.scan.failed" || ev.Event == "archive.failed" {
				t.Fatalf("%s: %s", ev.Event, ev.Data)
			}
		case <-timeout:
			t.Fatalf("no %s event", name)
		}
	}
}
