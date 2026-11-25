package nativeui

import (
	"testing"

	"github.com/diver80/postdive/internal/mailengine"
)

func TestActivityRowsGroupOneBatchUntilExpanded(t *testing.T) {
	list := []mailengine.ActivityEntry{
		{ID: 5, Kind: mailengine.ActivityArchive, Batch: "b1"},
		{ID: 4, Kind: mailengine.ActivityArchive, Batch: "b1"},
		{ID: 3, Kind: mailengine.ActivityArchive, Batch: "b1"},
		{ID: 2, Kind: mailengine.ActivityTrash},
		{ID: 1, Kind: mailengine.ActivityTrash},
	}
	rows := activityRows(list, nil)
	if len(rows) != 3 || len(rows[0]) != 3 || len(rows[1]) != 1 || len(rows[2]) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	if rows := activityRows(list, map[string]bool{"b1": true}); len(rows) != 5 {
		t.Fatalf("expanded rows = %d", len(rows))
	}
}

func TestActivityUndoOnlyForMovesWithBothFolders(t *testing.T) {
	move := mailengine.ActivityEntry{Kind: mailengine.ActivityTrash, MessageID: "m", Folder: "INBOX", Target: "Trash"}
	if !activityUndoable(move) {
		t.Fatal("a trashed mail can be moved back")
	}
	for _, e := range []mailengine.ActivityEntry{
		{Kind: mailengine.ActivityDelete, MessageID: "m", Folder: "INBOX"},
		{Kind: mailengine.ActivityRead, MessageID: "m", Folder: "INBOX"},
		{Kind: mailengine.ActivityMove, Folder: "INBOX", Target: "Archive"},
	} {
		if activityUndoable(e) {
			t.Fatalf("%+v must not offer undo", e)
		}
	}
}

func TestActivityStartsWithoutTheReadEntries(t *testing.T) {
	a := &App{}
	for _, k := range a.activityKinds() {
		if k == mailengine.ActivityRead {
			t.Fatal("read entries are the busiest and start hidden")
		}
	}
	a.activity.off["read"] = false
	found := false
	for _, k := range a.activityKinds() {
		found = found || k == mailengine.ActivityRead
	}
	if !found || isMailNav("activity") || !isMailNav("folder:x") {
		t.Fatal("read chip or isMailNav wrong")
	}
}
