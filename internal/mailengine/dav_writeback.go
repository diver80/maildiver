package mailengine

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
)

// discoverDAVCollectionURL resolves a base server URL or /.well-known/{caldav,carddav} endpoint
// into a concrete calendar/addressbook collection URL via PROPFIND when possible, falling back
// cleanly to rawURL when the server already points directly at a collection or refuses discovery.
func discoverDAVCollectionURL(ctx context.Context, hc *http.Client, rawURL, kind, username, password string, allowLoopback bool) string {
	clean, err := validateDAVURL(rawURL, allowLoopback)
	if err != nil {
		return rawURL
	}
	parsed, err := url.Parse(clean)
	if err != nil {
		return clean
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}

	// Probe whether the supplied URL already represents a concrete collection or principal/home.
	probeURL := clean
	if rt, _, homeHref := propfindResource(ctx, hc, probeURL, kind, username, password); rt == kind+"-collection" {
		return probeURL
	} else if homeHref != "" {
		if resolved := resolveDAVHref(probeURL, homeHref); resolved != "" {
			if col := findFirstChildCollection(ctx, hc, resolved, kind, username, password); col != "" {
				return col
			}
			return resolved
		}
	}

	// Try RFC 6764 /.well-known/{caldav,carddav} when user passed bare domain or short path.
	if strings.Trim(parsed.Path, "/") == "" || (!strings.Contains(strings.ToLower(parsed.Path), kind) && !strings.Contains(strings.ToLower(parsed.Path), "calendars") && !strings.Contains(strings.ToLower(parsed.Path), "addressbooks")) {
		wk := fmt.Sprintf("%s://%s/.well-known/%s", parsed.Scheme, parsed.Host, kind)
		if wkURL, verr := validateDAVURL(wk, allowLoopback); verr == nil {
			if _, principalHref, homeHref := propfindResource(ctx, hc, wkURL, kind, username, password); homeHref != "" {
				if resolved := resolveDAVHref(wkURL, homeHref); resolved != "" {
					if col := findFirstChildCollection(ctx, hc, resolved, kind, username, password); col != "" {
						return col
					}
					return resolved
				}
			} else if principalHref != "" {
				pURL := resolveDAVHref(wkURL, principalHref)
				if _, _, h2 := propfindResource(ctx, hc, pURL, kind, username, password); h2 != "" {
					resolved := resolveDAVHref(pURL, h2)
					if col := findFirstChildCollection(ctx, hc, resolved, kind, username, password); col != "" {
						return col
					}
					if resolved != "" {
						return resolved
					}
				}
			}
		}
	}
	return clean
}

func resolveDAVHref(baseRaw, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	base, err := url.Parse(baseRaw)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

func propfindResource(ctx context.Context, hc *http.Client, targetURL, kind, username, password string) (resType, principalHref, homeHref string) {
	homeTag := "C:calendar-home-set"
	homeNS := `xmlns:C="urn:ietf:params:xml:ns:caldav"`
	if kind == "carddav" {
		homeTag = "C:addressbook-home-set"
		homeNS = `xmlns:C="urn:ietf:params:xml:ns:carddav"`
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:" %s>
  <D:prop>
    <D:resourcetype/>
    <D:current-user-principal/>
    <%s/>
  </D:prop>
</D:propfind>`, homeNS, homeTag)
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", targetURL, strings.NewReader(body))
	if err != nil {
		return "", "", ""
	}
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	if username != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", ""
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	return parseDiscoveryPropXML(raw, kind)
}

func parseDiscoveryPropXML(raw []byte, kind string) (resType, principalHref, homeHref string) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var stack []string
	var inHome, inPrincipal, hasCollection, hasDAVKind bool
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			stack = append(stack, name)
			switch name {
			case "collection":
				for _, s := range stack {
					if s == "resourcetype" {
						hasCollection = true
					}
				}
			case "calendar":
				if kind == "caldav" {
					for _, s := range stack {
						if s == "resourcetype" {
							hasDAVKind = true
						}
					}
				}
			case "addressbook":
				if kind == "carddav" {
					for _, s := range stack {
						if s == "resourcetype" {
							hasDAVKind = true
						}
					}
				}
			case "calendar-home-set", "addressbook-home-set":
				inHome = true
			case "current-user-principal":
				inPrincipal = true
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if name == "calendar-home-set" || name == "addressbook-home-set" {
				inHome = false
			}
			if name == "current-user-principal" {
				inPrincipal = false
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 && stack[len(stack)-1] == "href" {
				val := strings.TrimSpace(string(t))
				if val != "" {
					if inHome && homeHref == "" {
						homeHref = val
					} else if inPrincipal && principalHref == "" {
						principalHref = val
					}
				}
			}
		}
	}
	if hasCollection && hasDAVKind {
		resType = kind + "-collection"
	}
	return resType, principalHref, homeHref
}

func findFirstChildCollection(ctx context.Context, hc *http.Client, homeURL, kind, username, password string) string {
	body := `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:CR="urn:ietf:params:xml:ns:carddav">
  <D:prop><D:resourcetype/></D:prop>
</D:propfind>`
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", homeURL, strings.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	if username != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var href string
	var isKind bool
	var stack []string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			stack = append(stack, name)
			if name == "response" {
				href, isKind = "", false
			}
			if (kind == "caldav" && name == "calendar") || (kind == "carddav" && name == "addressbook") {
				for _, s := range stack {
					if s == "resourcetype" {
						isKind = true
					}
				}
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if name == "response" && isKind && href != "" {
				return resolveDAVHref(homeURL, href)
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) >= 2 && stack[len(stack)-1] == "href" && stack[len(stack)-2] == "response" && href == "" {
				href = strings.TrimSpace(string(t))
			}
		}
	}
	return ""
}

func (e *Engine) findCalDAVForCalendar(cal StoredCalendar) (config.DAVAccountConfig, string, bool) {
	cfg := e.loadConfig()
	id := strings.TrimPrefix(cal.ID, "caldav-")
	for _, cd := range cfg.Direct.CalDAVAccounts {
		if cd.ID == cal.ID || cd.ID == id || cal.ExternalID == cd.URL {
			pass, _ := e.secrets.Get(davPasswordKey(cd.ID))
			return cd, pass, true
		}
	}
	return config.DAVAccountConfig{}, "", false
}

func (e *Engine) findCardDAVForBook(bookID string) (config.DAVAccountConfig, string, bool) {
	cfg := e.loadConfig()
	id := strings.TrimPrefix(bookID, "carddav:")
	for _, cd := range cfg.Direct.CardDAVAccounts {
		if cd.ID == bookID || cd.ID == id {
			pass, _ := e.secrets.Get(davPasswordKey(cd.ID))
			return cd, pass, true
		}
	}
	return config.DAVAccountConfig{}, "", false
}

func davResourceURL(collectionURL, itemID, ext string) string {
	base := strings.TrimRight(strings.TrimSpace(collectionURL), "/")
	safeID := strings.TrimSpace(itemID)
	if strings.HasPrefix(safeID, "http://") || strings.HasPrefix(safeID, "https://") {
		return safeID
	}
	if strings.HasPrefix(safeID, "/") {
		if u, err := url.Parse(base); err == nil {
			u.Path = safeID
			return u.String()
		}
	}
	safeID = path.Base(strings.ReplaceAll(safeID, "::", "-"))
	safeID = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '@' {
			return r
		}
		return '_'
	}, safeID)
	if safeID == "" || safeID == "." || safeID == ".." {
		safeID = fmt.Sprintf("item-%d", time.Now().UnixNano())
	}
	if !strings.HasSuffix(strings.ToLower(safeID), ext) {
		safeID += ext
	}
	return base + "/" + safeID
}

// pushCalDAVEvent writes or updates ev via HTTP PUT to the CalDAV server and persists it locally.
func (e *Engine) pushCalDAVEvent(ctx context.Context, cal StoredCalendar, ev StoredEvent) (StoredEvent, error) {
	cd, pass, ok := e.findCalDAVForCalendar(cal)
	if !ok {
		return StoredEvent{}, fmt.Errorf("CalDAV-Konto für %q nicht gefunden", cal.Name)
	}
	cleanCol, err := validateDAVURL(cd.URL, true)
	if err != nil {
		return StoredEvent{}, err
	}
	targetURL := davResourceURL(cleanCol, ev.UID, ".ics")
	if ev.ExternalID != "" && (strings.HasPrefix(ev.ExternalID, "http") || strings.Contains(ev.ExternalID, "/")) {
		targetURL = davResourceURL(cleanCol, ev.ExternalID, ".ics")
	}
	payload := ExportEventsICS(cal.Name, []StoredEvent{ev})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, strings.NewReader(payload))
	if err != nil {
		return StoredEvent{}, err
	}
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	if cd.Username != "" {
		req.SetBasicAuth(cd.Username, pass)
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return StoredEvent{}, fmt.Errorf("CalDAV PUT fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return StoredEvent{}, fmt.Errorf("CalDAV HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if et := strings.TrimSpace(resp.Header.Get("ETag")); et != "" {
		ev.ETag = et
	}
	ev.RawICS = payload
	if ev.ExternalID == "" {
		ev.ExternalID = targetURL
	}
	if err := e.store.UpsertEvent(ctx, ev); err != nil {
		return StoredEvent{}, err
	}
	return ev, nil
}

// deleteCalDAVEvent removes ev via HTTP DELETE from the CalDAV server and from SQLite.
func (e *Engine) deleteCalDAVEvent(ctx context.Context, cal StoredCalendar, ev StoredEvent) error {
	cd, pass, ok := e.findCalDAVForCalendar(cal)
	if !ok {
		return e.store.DeleteEvent(ctx, cal.ID, ev.ID)
	}
	cleanCol, err := validateDAVURL(cd.URL, true)
	if err != nil {
		return err
	}
	targetURL := davResourceURL(cleanCol, ev.UID, ".ics")
	if ev.ExternalID != "" && (strings.HasPrefix(ev.ExternalID, "http") || strings.Contains(ev.ExternalID, "/")) {
		targetURL = davResourceURL(cleanCol, ev.ExternalID, ".ics")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, targetURL, nil)
	if err != nil {
		return err
	}
	if cd.Username != "" {
		req.SetBasicAuth(cd.Username, pass)
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("CalDAV DELETE fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("CalDAV DELETE HTTP %d", resp.StatusCode)
	}
	return e.store.DeleteEvent(ctx, cal.ID, ev.ID)
}

// pushCardDAVContact uploads a contact vCard via HTTP PUT when its AddressBookID is a CardDAV book.
func (e *Engine) pushCardDAVContact(ctx context.Context, c backend.ContactRecord) error {
	if !strings.HasPrefix(c.AddressBookID, "carddav:") {
		return nil
	}
	cd, pass, ok := e.findCardDAVForBook(c.AddressBookID)
	if !ok {
		return fmt.Errorf("CardDAV-Adressbuch %q nicht gefunden", c.AddressBookID)
	}
	cleanCol, err := validateDAVURL(cd.URL, true)
	if err != nil {
		return err
	}
	uid := strings.TrimPrefix(c.ID, cd.ID+"::")
	if uid == "" {
		uid = c.Email
	}
	targetURL := davResourceURL(cleanCol, uid, ".vcf")
	vcf := FormatSingleVCardWithUID(c, uid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, strings.NewReader(vcf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/vcard; charset=utf-8")
	if cd.Username != "" {
		req.SetBasicAuth(cd.Username, pass)
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("CardDAV PUT fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("CardDAV HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// deleteCardDAVContact deletes a contact vCard via HTTP DELETE on the CardDAV server.
func (e *Engine) deleteCardDAVContact(ctx context.Context, c backend.ContactRecord) error {
	if !strings.HasPrefix(c.AddressBookID, "carddav:") {
		return nil
	}
	cd, pass, ok := e.findCardDAVForBook(c.AddressBookID)
	if !ok {
		return nil
	}
	cleanCol, err := validateDAVURL(cd.URL, true)
	if err != nil {
		return err
	}
	uid := strings.TrimPrefix(c.ID, cd.ID+"::")
	targetURL := davResourceURL(cleanCol, uid, ".vcf")
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, targetURL, nil)
	if err != nil {
		return err
	}
	if cd.Username != "" {
		req.SetBasicAuth(cd.Username, pass)
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("CardDAV DELETE fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("CardDAV DELETE HTTP %d", resp.StatusCode)
	}
	return nil
}

// FormatSingleVCardWithUID returns a vCard 3.0 payload including a stable UID line for CardDAV PUT.
func FormatSingleVCardWithUID(c backend.ContactRecord, uid string) string {
	raw := FormatVCF([]backend.ContactRecord{c})
	if uid == "" {
		return raw
	}
	return strings.Replace(raw, "VERSION:3.0\r\n", "VERSION:3.0\r\nUID:"+cleanVCardSingleLine(uid)+"\r\n", 1)
}
