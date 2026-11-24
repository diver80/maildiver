package archive

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/diver80/postdive/internal/config"
)

const (
	indexFileName       = ".postdive-index.json"
	legacyIndexFileName = ".omathunder-index.json"
)

var legacyIndexFileNames = []string{".maildiver-index.json", legacyIndexFileName}

type ExportRequest struct {
	ArchiveDir      string `json:"archiveDir,omitempty"`
	AccountID       string `json:"accountId"`
	AccountName     string `json:"accountName,omitempty"`
	FolderPath      string `json:"folderPath"`
	DateISO         string `json:"date"`
	Subject         string `json:"subject"`
	MessageID       string `json:"messageId"`
	HeaderMessageID string `json:"headerMessageId,omitempty"`
	RawEMLBase64    string `json:"rawEmlBase64,omitempty"`
	DryRun          bool   `json:"dryRun"`
}

type ExportResult struct {
	TargetPath       string `json:"targetPath"`
	RelativePath     string `json:"relativePath"`
	BytesWritten     int64  `json:"bytesWritten"`
	SHA256           string `json:"sha256"`
	Verified         bool   `json:"verified"`
	SkippedDuplicate bool   `json:"skippedDuplicate"`
	DryRun           bool   `json:"dryRun"`
}

type ArchiveStats struct {
	ArchiveDir string `json:"archiveDir"`
	Exists     bool   `json:"exists"`
	FileCount  int    `json:"fileCount"`
	TotalBytes int64  `json:"totalBytes"`
}

type indexEntry struct {
	RelativePath    string `json:"relativePath"`
	Bytes           int64  `json:"bytes"`
	HeaderMessageID string `json:"headerMessageId,omitempty"`
	ExportedAt      string `json:"exportedAt"`
}

var indexMu sync.Mutex

// ResolveRoot expands ~ prefixes, normalizes the archive root and returns its absolute path.
func ResolveRoot(dir string) (string, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		trimmed = config.DefaultArchiveDir()
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("cannot resolve home directory: %w", err)
		}
		if trimmed == "~" {
			trimmed = home
		} else {
			trimmed = filepath.Join(home, trimmed[2:])
		}
	}
	abs, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", err
	}
	return abs, nil
}

// SanitizeSegment turns a folder, account or subject string into a safe single path component.
func SanitizeSegment(raw string, maxRunes int) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		if unicode.IsControl(r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(raw))

	cleaned = strings.Trim(cleaned, ". ")
	for strings.Contains(cleaned, "  ") {
		cleaned = strings.ReplaceAll(cleaned, "  ", " ")
	}
	for strings.Contains(cleaned, "__") {
		cleaned = strings.ReplaceAll(cleaned, "__", "_")
	}
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return ""
	}
	if maxRunes > 0 {
		runes := []rune(cleaned)
		if len(runes) > maxRunes {
			cleaned = strings.TrimRight(string(runes[:maxRunes]), ". ")
		}
	}
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return ""
	}
	return cleaned
}

func parseDate(raw string) time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Now().UTC()
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		time.RFC1123Z,
		time.RFC1123,
	}
	for _, layout := range layouts {
		if tm, err := time.Parse(layout, trimmed); err == nil {
			return tm
		}
	}
	return time.Now().UTC()
}

func folderSegments(folderPath string) []string {
	normalized := strings.ReplaceAll(folderPath, "\\", "/")
	parts := strings.Split(normalized, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." {
			continue
		}
		seg := SanitizeSegment(p, 64)
		if seg == "" {
			continue
		}
		if len(out) == 0 && strings.EqualFold(seg, "INBOX") {
			seg = "Incoming"
		}
		out = append(out, seg)
	}
	if len(out) == 0 {
		return []string{"Incoming"}
	}
	return out
}

// BuildRelativePath produces <Account>/<Incoming|FolderPath>/<YYYY>/<MM>/<YYYY-MM-DD>_<Subject>_<shortHash>.eml.
func BuildRelativePath(req ExportRequest, hashHex string) string {
	accountLabel := SanitizeSegment(req.AccountName, 64)
	if accountLabel == "" {
		accountLabel = SanitizeSegment(req.AccountID, 64)
	}
	if accountLabel == "" {
		accountLabel = "account"
	}

	folders := folderSegments(req.FolderPath)
	tm := parseDate(req.DateISO)
	year := tm.Format("2006")
	month := tm.Format("01")
	day := tm.Format("2006-01-02")

	subj := SanitizeSegment(req.Subject, 60)
	if subj == "" {
		subj = "mail"
	}
	short := hashHex
	if len(short) > 8 {
		short = short[:8]
	}
	if short == "" {
		short = "00000000"
	}

	parts := make([]string, 0, 4+len(folders))
	parts = append(parts, accountLabel)
	parts = append(parts, folders...)
	parts = append(parts, year, month, fmt.Sprintf("%s_%s_%s.eml", day, subj, short))
	return filepath.Join(parts...)
}

func decodeBase64(raw string) ([]byte, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return nil, errors.New("empty raw eml")
	}
	if b, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
		return b, nil
	}
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, cleaned)
	if b, err := base64.StdEncoding.DecodeString(stripped); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(stripped, "="))
}

func ensureWithinRoot(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("archive target escapes archive root")
	}
	return nil
}

func verifyFileSHA256(path string, expected [32]byte) (int64, bool) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil || n == 0 {
		return 0, false
	}
	var got [32]byte
	copy(got[:], h.Sum(nil))
	return n, got == expected
}

func loadIndex(indexPath string) map[string]indexEntry {
	out := map[string]indexEntry{}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		// Fall back seamlessly to legacy index filenames from earlier builds (MailDiver / OmaThunder).
		found := false
		for _, name := range legacyIndexFileNames {
			legacyPath := filepath.Join(filepath.Dir(indexPath), name)
			if data, err = os.ReadFile(legacyPath); err == nil {
				found = true
				break
			}
		}
		if !found {
			return out
		}
	}
	_ = json.Unmarshal(data, &out)
	if out == nil {
		return map[string]indexEntry{}
	}
	return out
}

func saveIndex(indexPath string, idx map[string]indexEntry) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp := indexPath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, indexPath)
}

// Export writes and SHA256-verifies an .eml backup (or computes its dry-run path without touching disk).
func Export(req ExportRequest) (ExportResult, error) {
	if strings.TrimSpace(req.AccountID) == "" && strings.TrimSpace(req.AccountName) == "" {
		return ExportResult{}, errors.New("accountId is required")
	}
	root, err := ResolveRoot(req.ArchiveDir)
	if err != nil {
		return ExportResult{}, err
	}

	if req.DryRun {
		var sum [32]byte
		var expectedBytes int64
		if strings.TrimSpace(req.RawEMLBase64) != "" {
			rawBytes, decErr := decodeBase64(req.RawEMLBase64)
			if decErr != nil {
				return ExportResult{}, decErr
			}
			sum = sha256.Sum256(rawBytes)
			expectedBytes = int64(len(rawBytes))
		} else {
			seed := fmt.Sprintf("%s|%s|%s|%s|%s|%s", req.AccountID, req.FolderPath, req.HeaderMessageID, req.MessageID, req.Subject, req.DateISO)
			sum = sha256.Sum256([]byte(seed))
		}
		hashHex := hex.EncodeToString(sum[:])
		relPath := BuildRelativePath(req, hashHex)
		targetPath := filepath.Join(root, relPath)
		if err := ensureWithinRoot(root, targetPath); err != nil {
			return ExportResult{}, err
		}
		_, statErr := os.Stat(targetPath)
		return ExportResult{
			TargetPath:       targetPath,
			RelativePath:     relPath,
			BytesWritten:     expectedBytes,
			SHA256:           hashHex,
			Verified:         false,
			SkippedDuplicate: statErr == nil,
			DryRun:           true,
		}, nil
	}

	rawBytes, err := decodeBase64(req.RawEMLBase64)
	if err != nil {
		return ExportResult{}, err
	}
	if len(rawBytes) == 0 {
		return ExportResult{}, errors.New("empty raw eml")
	}

	expectedSum := sha256.Sum256(rawBytes)
	hashHex := hex.EncodeToString(expectedSum[:])
	relPath := BuildRelativePath(req, hashHex)
	targetPath := filepath.Join(root, relPath)
	if err := ensureWithinRoot(root, targetPath); err != nil {
		return ExportResult{}, err
	}

	indexMu.Lock()
	defer indexMu.Unlock()

	if n, ok := verifyFileSHA256(targetPath, expectedSum); ok && n == int64(len(rawBytes)) {
		return ExportResult{
			TargetPath:       targetPath,
			RelativePath:     relPath,
			BytesWritten:     n,
			SHA256:           hashHex,
			Verified:         true,
			SkippedDuplicate: true,
			DryRun:           false,
		}, nil
	}

	indexPath := filepath.Join(root, indexFileName)
	idx := loadIndex(indexPath)
	if prev, ok := idx[hashHex]; ok && prev.RelativePath != "" {
		existingAbs := filepath.Join(root, prev.RelativePath)
		if err := ensureWithinRoot(root, existingAbs); err == nil {
			if n, verified := verifyFileSHA256(existingAbs, expectedSum); verified && n == int64(len(rawBytes)) {
				return ExportResult{
					TargetPath:       existingAbs,
					RelativePath:     prev.RelativePath,
					BytesWritten:     n,
					SHA256:           hashHex,
					Verified:         true,
					SkippedDuplicate: true,
					DryRun:           false,
				}, nil
			}
		}
	}

	parentDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(parentDir, 0o700); err != nil {
		return ExportResult{}, err
	}

	tmpFile, err := os.CreateTemp(parentDir, ".postdive-*.tmp")
	if err != nil {
		return ExportResult{}, err
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return ExportResult{}, err
	}
	if _, err := tmpFile.Write(rawBytes); err != nil {
		_ = tmpFile.Close()
		return ExportResult{}, err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return ExportResult{}, err
	}
	if err := tmpFile.Close(); err != nil {
		return ExportResult{}, err
	}

	writtenBytes, verified := verifyFileSHA256(tmpPath, expectedSum)
	if !verified || writtenBytes != int64(len(rawBytes)) {
		return ExportResult{}, errors.New("sha256 verification failed after writing temp file")
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		return ExportResult{}, err
	}
	cleanupTmp = false

	info, err := os.Stat(targetPath)
	if err != nil || info.Size() != int64(len(rawBytes)) {
		return ExportResult{}, errors.New("post-rename file verification failed")
	}

	idx[hashHex] = indexEntry{
		RelativePath:    relPath,
		Bytes:           writtenBytes,
		HeaderMessageID: req.HeaderMessageID,
		ExportedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveIndex(indexPath, idx); err != nil {
		return ExportResult{}, err
	}

	return ExportResult{
		TargetPath:       targetPath,
		RelativePath:     relPath,
		BytesWritten:     writtenBytes,
		SHA256:           hashHex,
		Verified:         true,
		SkippedDuplicate: false,
		DryRun:           false,
	}, nil
}

// Stats returns the number and total byte size of .eml files under the archive root.
func Stats(archiveDir string) (ArchiveStats, error) {
	root, err := ResolveRoot(archiveDir)
	if err != nil {
		return ArchiveStats{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ArchiveStats{ArchiveDir: root, Exists: false}, nil
		}
		return ArchiveStats{}, err
	}
	if !info.IsDir() {
		return ArchiveStats{}, fmt.Errorf("archive path is not a directory: %s", root)
	}
	stats := ArchiveStats{ArchiveDir: root, Exists: true}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".eml") {
			fi, err := d.Info()
			if err == nil {
				stats.FileCount++
				stats.TotalBytes += fi.Size()
			}
		}
		return nil
	})
	return stats, err
}
