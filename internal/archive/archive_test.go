package archive

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportInboxAndSortedFolders(t *testing.T) {
	root := t.TempDir()
	raw := []byte("From: sender@example.com\r\nSubject: Test\r\nMessage-ID: <m1@example.com>\r\n\r\nHello World\r\n")
	b64 := base64.StdEncoding.EncodeToString(raw)

	resInbox, err := Export(ExportRequest{
		ArchiveDir:      root,
		AccountID:       "acc1",
		AccountName:     "user@example.org",
		FolderPath:      "INBOX",
		DateISO:         "2023-07-14T10:20:00Z",
		Subject:         "Stromrechnung Juli: Q3/2023?",
		MessageID:       "101",
		HeaderMessageID: "<m1@example.com>",
		RawEMLBase64:    b64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resInbox.Verified || resInbox.SkippedDuplicate || resInbox.BytesWritten != int64(len(raw)) {
		t.Fatalf("unexpected inbox export result: %+v", resInbox)
	}
	wantPrefix := filepath.Join("user@example.org", "Incoming", "2023", "07", "2023-07-14_")
	if !strings.HasPrefix(resInbox.RelativePath, wantPrefix) || !strings.HasSuffix(resInbox.RelativePath, ".eml") {
		t.Fatalf("unexpected relative path: %s (want prefix %s)", resInbox.RelativePath, wantPrefix)
	}
	disk, err := os.ReadFile(resInbox.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != string(raw) {
		t.Fatalf("disk content mismatch")
	}

	raw2 := []byte("From: billing@stadtwerke.de\r\nSubject: Abschlag\r\nMessage-ID: <m2@example.com>\r\n\r\nRechnung\r\n")
	resSorted, err := Export(ExportRequest{
		ArchiveDir:   root,
		AccountID:    "acc1",
		AccountName:  "user@example.org",
		FolderPath:   "Rechnungen/Strom",
		DateISO:      "2022-11-05T08:00:00Z",
		Subject:      "Abschlag November",
		MessageID:    "102",
		RawEMLBase64: base64.StdEncoding.EncodeToString(raw2),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSorted := filepath.Join("user@example.org", "Rechnungen", "Strom", "2022", "11", "2022-11-05_Abschlag November_")
	if !strings.HasPrefix(resSorted.RelativePath, wantSorted) {
		t.Fatalf("unexpected sorted folder path: %s", resSorted.RelativePath)
	}

	stats, err := Stats(root)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Exists || stats.FileCount != 2 || stats.TotalBytes != int64(len(raw)+len(raw2)) {
		t.Fatalf("unexpected archive stats: %+v", stats)
	}
}

func TestExportDryRunTouchesNoDisk(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")
	res, err := Export(ExportRequest{
		ArchiveDir:      root,
		AccountID:       "acc1",
		AccountName:     "Firma",
		FolderPath:      "INBOX",
		DateISO:         "2021-01-19T12:00:00Z",
		Subject:         "Alte Mail",
		MessageID:       "999",
		HeaderMessageID: "<old@example.com>",
		DryRun:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Verified {
		t.Fatalf("expected DryRun=true Verified=false: %+v", res)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("dry run must not create archive directory on disk")
	}
}

func TestExportNeutralizesPathTraversal(t *testing.T) {
	root := t.TempDir()
	raw := []byte("From: x@y.de\r\n\r\nhi\r\n")
	res, err := Export(ExportRequest{
		ArchiveDir:   root,
		AccountID:    "../evil",
		AccountName:  "../../etc",
		FolderPath:   "../../../passwd/..",
		DateISO:      "2024-02-01T00:00:00Z",
		Subject:      "../../secret",
		MessageID:    "1",
		RawEMLBase64: base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	absRoot, _ := filepath.Abs(root)
	if !strings.HasPrefix(res.TargetPath, absRoot+string(filepath.Separator)) {
		t.Fatalf("target escaped root: %s not inside %s", res.TargetPath, absRoot)
	}
}

func TestExportDeduplicationAndRecoveryIfDeleted(t *testing.T) {
	root := t.TempDir()
	raw := []byte("From: a@b.de\r\nMessage-ID: <dup@b.de>\r\n\r\nsame body\r\n")
	sum := sha256.Sum256(raw)
	wantHash := hex.EncodeToString(sum[:])
	req := ExportRequest{
		ArchiveDir:      root,
		AccountID:       "acc1",
		AccountName:     "user@example.org",
		FolderPath:      "INBOX",
		DateISO:         "2023-03-04T09:00:00Z",
		Subject:         "Dup",
		MessageID:       "1",
		HeaderMessageID: "<dup@b.de>",
		RawEMLBase64:    base64.StdEncoding.EncodeToString(raw),
	}

	first, err := Export(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != wantHash || first.SkippedDuplicate {
		t.Fatalf("unexpected first export: %+v", first)
	}

	second, err := Export(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Verified || !second.SkippedDuplicate {
		t.Fatalf("second export should skip duplicate safely: %+v", second)
	}

	// Same content exported from a different folder deduplicates via index if still intact.
	reqOtherFolder := req
	reqOtherFolder.FolderPath = "Spaces/Acme"
	third, err := Export(reqOtherFolder)
	if err != nil {
		t.Fatal(err)
	}
	if !third.Verified || !third.SkippedDuplicate || third.TargetPath != first.TargetPath {
		t.Fatalf("expected cross-folder dedup to intact file: %+v", third)
	}

	// If user removed the file on disk, re-exporting restores it instead of trusting stale index.
	if err := os.Remove(first.TargetPath); err != nil {
		t.Fatal(err)
	}
	fourth, err := Export(req)
	if err != nil {
		t.Fatal(err)
	}
	if !fourth.Verified || fourth.SkippedDuplicate {
		t.Fatalf("expected re-write after disk removal: %+v", fourth)
	}
	if _, err := os.Stat(fourth.TargetPath); err != nil {
		t.Fatalf("expected restored file on disk: %v", err)
	}
}
