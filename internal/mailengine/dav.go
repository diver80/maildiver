package mailengine

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
)

type davMultistatus struct {
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href      string        `xml:"href"`
	PropStats []davPropStat `xml:"propstat"`
}

type davPropStat struct {
	Status string  `xml:"status"`
	Prop   davProp `xml:"prop"`
}

type davProp struct {
	ETag         string `xml:"getetag"`
	CalendarData string `xml:"calendar-data"`
	AddressData  string `xml:"address-data"`
}

func validateDAVURL(rawURL string, allowLoopback bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("ungültige DAV-URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && !(scheme == "http" && allowLoopback && isLoopbackHost(u.Hostname())) {
		return "", fmt.Errorf("DAV-URL muss https:// verwenden")
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("DAV-URL ohne Hostname")
	}
	return u.String(), nil
}

func davPasswordKey(id string) string {
	return "dav:" + strings.TrimSpace(id)
}

// FetchCalDAVEvents executes a CalDAV calendar-query REPORT against calendarURL and parses
// every returned VCALENDAR component into MailDiver StoredEvents.
func FetchCalDAVEvents(ctx context.Context, hc *http.Client, calID, calName, calendarURL, username, password string, allowLoopback bool) ([]StoredEvent, error) {
	safeURL, err := validateDAVURL(calendarURL, allowLoopback)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{Timeout: 25 * time.Second}
	}
	queryBody := `<?xml version="1.0" encoding="utf-8"?>
<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
    <d:getetag/>
    <c:calendar-data/>
  </d:prop>
  <c:filter>
    <c:comp-filter name="VCALENDAR">
      <c:comp-filter name="VEVENT"/>
    </c:comp-filter>
  </c:filter>
</c:calendar-query>`

	req, err := http.NewRequestWithContext(ctx, "REPORT", safeURL, strings.NewReader(queryBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "1")
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("CalDAV Verbindung fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("CalDAV HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, err
	}

	var ms davMultistatus
	if err := xml.Unmarshal(raw, &ms); err != nil {
		return nil, fmt.Errorf("CalDAV XML-Antwort nicht lesbar: %w", err)
	}
	var out []StoredEvent
	for _, r := range ms.Responses {
		for _, ps := range r.PropStats {
			ics := strings.TrimSpace(ps.Prop.CalendarData)
			if ics == "" {
				continue
			}
			evs, perr := ParseICS(calID, calName, false, strings.NewReader(ics))
			if perr != nil {
				continue
			}
			for i := range evs {
				evs[i].ETag = strings.Trim(ps.Prop.ETag, `"`)
				if evs[i].ExternalID == "" {
					evs[i].ExternalID = r.Href
				}
				out = append(out, evs[i])
			}
		}
	}
	return out, nil
}

// FetchCardDAVContacts executes a CardDAV addressbook-query REPORT against bookURL and returns
// the parsed contact records.
func FetchCardDAVContacts(ctx context.Context, hc *http.Client, bookURL, username, password string, allowLoopback bool) ([]backend.ContactRecord, error) {
	safeURL, err := validateDAVURL(bookURL, allowLoopback)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{Timeout: 25 * time.Second}
	}
	queryBody := `<?xml version="1.0" encoding="utf-8"?>
<card:addressbook-query xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">
  <d:prop>
    <d:getetag/>
    <card:address-data/>
  </d:prop>
</card:addressbook-query>`

	req, err := http.NewRequestWithContext(ctx, "REPORT", safeURL, strings.NewReader(queryBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "1")
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("CardDAV Verbindung fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("CardDAV HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, err
	}
	var ms davMultistatus
	if err := xml.Unmarshal(raw, &ms); err != nil {
		return nil, fmt.Errorf("CardDAV XML-Antwort nicht lesbar: %w", err)
	}
	var combined bytes.Buffer
	for _, r := range ms.Responses {
		for _, ps := range r.PropStats {
			if vd := strings.TrimSpace(ps.Prop.AddressData); vd != "" {
				combined.WriteString(vd)
				combined.WriteString("\n")
			}
		}
	}
	if combined.Len() == 0 {
		return nil, nil
	}
	return ParseVCF(bytes.NewReader(combined.Bytes()))
}

// AddCalDAVAccount saves a CalDAV calendar subscription (password stored in secretstore) and syncs it immediately.
func (e *Engine) AddCalDAVAccount(ctx context.Context, name, rawURL, username, password string) (int, error) {
	safeURL, err := validateDAVURL(rawURL, e.allowLoopbackICal)
	if err != nil {
		return 0, err
	}
	discovered := discoverDAVCollectionURL(ctx, nil, safeURL, "caldav", username, password, e.allowLoopbackICal)
	if discovered != "" {
		safeURL = discovered
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "CalDAV"
	}
	id := "caldav-" + randomHex(6)
	evs, err := FetchCalDAVEvents(ctx, nil, id, name, safeURL, username, password, e.allowLoopbackICal)
	if err != nil {
		return 0, err
	}
	if password != "" {
		if serr := e.secrets.Set(davPasswordKey(id), password); serr != nil {
			return 0, serr
		}
	}
	cfg, err := e.loadCfg()
	if err != nil {
		cfg = config.Default()
	}
	cfg.Direct.CalDAVAccounts = append(cfg.Direct.CalDAVAccounts, config.DAVAccountConfig{
		ID:       id,
		Name:     name,
		URL:      safeURL,
		Username: strings.TrimSpace(username),
		Color:    "#9d1140",
		Enabled:  true,
	})
	if err := e.saveCfg(config.Normalize(cfg)); err != nil {
		return 0, err
	}
	_ = e.store.UpsertCalendar(ctx, StoredCalendar{
		ID:       id,
		Name:     name,
		Source:   "caldav",
		Color:    "#9d1140",
		ReadOnly: false,
	})
	if err := e.store.ReplaceCalendarEvents(ctx, id, evs); err != nil {
		return 0, err
	}
	e.emitCalendarChanged()
	return len(evs), nil
}

// AddCardDAVAccount saves a CardDAV address book (password in secretstore) and imports all contacts.
func (e *Engine) AddCardDAVAccount(ctx context.Context, name, rawURL, username, password string) (int, error) {
	safeURL, err := validateDAVURL(rawURL, e.allowLoopbackICal)
	if err != nil {
		return 0, err
	}
	discovered := discoverDAVCollectionURL(ctx, nil, safeURL, "carddav", username, password, e.allowLoopbackICal)
	if discovered != "" {
		safeURL = discovered
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "CardDAV"
	}
	id := "carddav-" + randomHex(6)
	contacts, err := FetchCardDAVContacts(ctx, nil, safeURL, username, password, e.allowLoopbackICal)
	if err != nil {
		return 0, err
	}
	if password != "" {
		if serr := e.secrets.Set(davPasswordKey(id), password); serr != nil {
			return 0, serr
		}
	}
	cfg, err := e.loadCfg()
	if err != nil {
		cfg = config.Default()
	}
	cfg.Direct.CardDAVAccounts = append(cfg.Direct.CardDAVAccounts, config.DAVAccountConfig{
		ID:       id,
		Name:     name,
		URL:      safeURL,
		Username: strings.TrimSpace(username),
		Enabled:  true,
	})
	if err := e.saveCfg(config.Normalize(cfg)); err != nil {
		return 0, err
	}
	imported := 0
	for _, ct := range contacts {
		ct.AddressBookID = "carddav:" + id
		if _, _, serr := e.store.UpsertContact(ctx, backend.ContactUpsertRequest{Contact: ct, AddressBookID: ct.AddressBookID}); serr == nil {
			imported++
		}
	}
	return imported, nil
}

// DeleteCalDAVAccount removes a configured CalDAV calendar, its stored password, and cached events.
func (e *Engine) DeleteCalDAVAccount(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	e.cfgMu.Lock()
	cfg := e.loadConfig()
	kept := cfg.Direct.CalDAVAccounts[:0]
	for _, a := range cfg.Direct.CalDAVAccounts {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	cfg.Direct.CalDAVAccounts = kept
	err := e.saveConfig(cfg)
	e.cfgMu.Unlock()
	if err != nil {
		return err
	}
	_ = e.secrets.Delete(davPasswordKey(id))
	_ = e.store.DeleteCalendar(ctx, id)
	e.emitCalendarChanged()
	return nil
}

// DeleteCardDAVAccount removes a configured CardDAV address book and its stored password.
func (e *Engine) DeleteCardDAVAccount(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	_ = e.store.DeleteContactsByAddressBook(ctx, "carddav:"+id)
	e.cfgMu.Lock()
	cfg := e.loadConfig()
	kept := cfg.Direct.CardDAVAccounts[:0]
	for _, a := range cfg.Direct.CardDAVAccounts {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	cfg.Direct.CardDAVAccounts = kept
	err := e.saveConfig(cfg)
	e.cfgMu.Unlock()
	if err != nil {
		return err
	}
	_ = e.secrets.Delete(davPasswordKey(id))
	return nil
}

// SyncAllDAV synchronizes all configured CalDAV calendars and CardDAV address books.
func (e *Engine) SyncAllDAV(ctx context.Context) error {
	cfg := e.loadConfig()
	var firstErr error
	calChanged := false
	for _, acc := range cfg.Direct.CalDAVAccounts {
		if !acc.Enabled || acc.URL == "" {
			continue
		}
		pass, _ := e.secrets.Get(davPasswordKey(acc.ID))
		evs, ferr := FetchCalDAVEvents(ctx, nil, acc.ID, acc.Name, acc.URL, acc.Username, pass, e.allowLoopbackICal)
		if ferr != nil {
			if firstErr == nil {
				firstErr = ferr
			}
			continue
		}
		_ = e.store.ReplaceCalendarEvents(ctx, acc.ID, evs)
		calChanged = true
	}
	if calChanged {
		e.emitCalendarChanged()
	}
	for _, acc := range cfg.Direct.CardDAVAccounts {
		if !acc.Enabled || acc.URL == "" {
			continue
		}
		pass, _ := e.secrets.Get(davPasswordKey(acc.ID))
		cts, ferr := FetchCardDAVContacts(ctx, nil, acc.URL, acc.Username, pass, e.allowLoopbackICal)
		if ferr != nil {
			if firstErr == nil {
				firstErr = ferr
			}
			continue
		}
		bookID := "carddav:" + acc.ID
		keep := make(map[string]bool, len(cts))
		for _, ct := range cts {
			ct.AddressBookID = bookID
			if saved, _, serr := e.store.UpsertContact(ctx, backend.ContactUpsertRequest{Contact: ct, AddressBookID: bookID}); serr == nil {
				keep[saved.ID] = true
			}
		}
		_ = e.store.DeleteMissingAddressBookContacts(ctx, bookID, keep)
	}
	return firstErr
}
