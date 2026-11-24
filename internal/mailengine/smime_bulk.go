package mailengine

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SMIMEBulkImportResult summarizes a bulk import of public S/MIME recipient certificates.
type SMIMEBulkImportResult struct {
	Imported int      // newly added recipient email certificates
	Updated  int      // existing recipient email certificates updated
	Skipped  int      // non-certificate files or CA-only certificates skipped
	Failed   int      // certificate files that failed to parse
	Emails   []string // sorted unique emails imported/updated
	Errors   []string // up to 5 sample error messages
}

const (
	maxSMIMEBulkFiles      = 500
	maxSMIMECertFileBytes  = 4 * 1024 * 1024
	maxSMIMEZipBytes       = 32 * 1024 * 1024
	maxSMIMEZipEntryBytes  = 4 * 1024 * 1024
	maxSMIMEBulkSampleErrs = 5
)

func isSMIMECertExt(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".cer", ".crt", ".pem", ".der", ".p7b", ".p7c":
		return true
	}
	return false
}

// ImportSMIMECertPaths bulk-imports public S/MIME recipient certificates from one or more
// certificate files (.cer/.crt/.pem/.der/.p7b/.p7c), multi-cert bundles, .zip archives, or folders.
func (e *Engine) ImportSMIMECertPaths(paths []string) (SMIMEBulkImportResult, error) {
	if e.smime == nil {
		return SMIMEBulkImportResult{}, fmt.Errorf("S/MIME nicht initialisiert")
	}
	if len(paths) == 0 {
		return SMIMEBulkImportResult{}, fmt.Errorf("keine Dateien oder Ordner angegeben")
	}

	existing := make(map[string]bool)
	for _, ct := range e.smime.ListContactCerts() {
		existing[strings.ToLower(strings.TrimSpace(ct.Email))] = true
	}

	var res SMIMEBulkImportResult
	touched := make(map[string]bool)
	recordErr := func(label string, err error) {
		res.Failed++
		if len(res.Errors) < maxSMIMEBulkSampleErrs {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", label, err))
		}
	}
	importBytes := func(label string, raw []byte) {
		infos, err := e.smime.ImportPublicCerts(raw)
		if err != nil {
			if strings.Contains(err.Error(), "enthält keine E-Mail-Adresse") {
				res.Skipped++
				return
			}
			recordErr(label, err)
			return
		}
		for _, inf := range infos {
			em := strings.ToLower(strings.TrimSpace(inf.Email))
			if em == "" || touched[em] {
				continue
			}
			touched[em] = true
			if existing[em] {
				res.Updated++
			} else {
				res.Imported++
			}
			res.Emails = append(res.Emails, em)
		}
	}

	importZip := func(zipPath string) {
		rawZip, err := readBoundedRegularFile(zipPath, maxSMIMEZipBytes)
		if err != nil {
			recordErr(filepath.Base(zipPath), err)
			return
		}
		zr, err := zip.NewReader(bytes.NewReader(rawZip), int64(len(rawZip)))
		if err != nil {
			recordErr(filepath.Base(zipPath), fmt.Errorf("ungültiges ZIP-Archiv: %w", err))
			return
		}
		processed := 0
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			base := filepath.Base(f.Name)
			if strings.HasPrefix(base, ".") || strings.Contains(f.Name, "__MACOSX/") {
				continue
			}
			if !isSMIMECertExt(base) {
				res.Skipped++
				continue
			}
			if processed >= maxSMIMEBulkFiles {
				break
			}
			processed++
			rc, oerr := f.Open()
			if oerr != nil {
				recordErr(base, oerr)
				continue
			}
			data, rerr := io.ReadAll(io.LimitReader(rc, maxSMIMEZipEntryBytes+1))
			_ = rc.Close()
			if rerr != nil {
				recordErr(base, rerr)
				continue
			}
			if len(data) > maxSMIMEZipEntryBytes {
				recordErr(base, fmt.Errorf("Datei überschreitet Limit von 4 MB"))
				continue
			}
			importBytes(base, data)
		}
	}

	totalFiles := 0
	for _, rawPath := range paths {
		p := expandHomePath(strings.TrimSpace(rawPath))
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			recordErr(filepath.Base(p), err)
			continue
		}
		if info.IsDir() {
			_ = filepath.WalkDir(p, func(cur string, d fs.DirEntry, werr error) error {
				if werr != nil {
					return nil
				}
				name := d.Name()
				if d.IsDir() {
					if cur != p && (strings.HasPrefix(name, ".") || name == "__MACOSX") {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasPrefix(name, ".") {
					return nil
				}
				if totalFiles >= maxSMIMEBulkFiles {
					return fs.SkipAll
				}
				ext := strings.ToLower(filepath.Ext(name))
				if ext == ".zip" {
					totalFiles++
					importZip(cur)
					return nil
				}
				if !isSMIMECertExt(name) {
					res.Skipped++
					return nil
				}
				totalFiles++
				raw, rerr := readBoundedRegularFile(cur, maxSMIMECertFileBytes)
				if rerr != nil {
					recordErr(name, rerr)
					return nil
				}
				importBytes(name, raw)
				return nil
			})
			continue
		}
		if totalFiles >= maxSMIMEBulkFiles {
			break
		}
		totalFiles++
		if strings.EqualFold(filepath.Ext(p), ".zip") {
			importZip(p)
			continue
		}
		raw, rerr := readBoundedRegularFile(p, maxSMIMECertFileBytes)
		if rerr != nil {
			recordErr(filepath.Base(p), rerr)
			continue
		}
		importBytes(filepath.Base(p), raw)
	}

	sort.Strings(res.Emails)
	if res.Imported == 0 && res.Updated == 0 {
		if len(res.Errors) > 0 {
			return res, fmt.Errorf("%s", strings.Join(res.Errors, "; "))
		}
		return res, fmt.Errorf("keine gültigen Empfänger-Zertifikate gefunden")
	}
	return res, nil
}
