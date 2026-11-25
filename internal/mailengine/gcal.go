package mailengine

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/diver80/postdive/internal/backend"
)

const (
	gcalDefaultAPIBase = "https://www.googleapis.com/calendar/v3"
	gcalScopeCalendar  = "https://www.googleapis.com/auth/calendar"
	gcalScopeContacts  = "https://www.googleapis.com/auth/contacts.readonly"
	secretGCalSecret   = "gcal:client_secret"
	secretGCalToken    = "gcal:token"
)

// GoogleCalStatus summarizes the Google Calendar connection state for the UI.
type GoogleCalStatus struct {
	Connected    bool
	ClientID     string
	AccountEmail string
	CalendarNum  int
	LastSync     string
	Error        string
}

func (e *Engine) gcalBaseURL() string {
	if e.gcalAPIBase != "" {
		return strings.TrimRight(e.gcalAPIBase, "/")
	}
	return gcalDefaultAPIBase
}

func (e *Engine) gcalOAuthConfig(clientID, clientSecret, redirectURL string) *oauth2.Config {
	ep := google.Endpoint
	if e.gcalEndpoint.AuthURL != "" || e.gcalEndpoint.TokenURL != "" {
		ep = e.gcalEndpoint
	}
	return &oauth2.Config{
		ClientID:     strings.TrimSpace(clientID),
		ClientSecret: strings.TrimSpace(clientSecret),
		Endpoint:     ep,
		RedirectURL:  redirectURL,
		Scopes:       []string{gcalScopeCalendar, gcalScopeContacts},
	}
}

func (e *Engine) GoogleCalStatus(ctx context.Context) GoogleCalStatus {
	cfg := e.loadConfig()
	_, tokErr := e.secrets.Get(secretGCalToken)
	cals, _ := e.store.ListCalendars(ctx)
	gcalCount := 0
	lastSync := ""
	for _, c := range cals {
		if c.Source == "google" {
			gcalCount++
			if c.LastSync > lastSync {
				lastSync = c.LastSync
			}
		}
	}
	e.mu.RLock()
	lastErr := e.gcalLastErr
	e.mu.RUnlock()
	return GoogleCalStatus{
		Connected:    tokErr == nil && cfg.Direct.GoogleCalendar.ClientID != "",
		ClientID:     cfg.Direct.GoogleCalendar.ClientID,
		AccountEmail: cfg.Direct.GoogleCalendar.AccountEmail,
		CalendarNum:  gcalCount,
		LastSync:     lastSync,
		Error:        lastErr,
	}
}

// StartGoogleCalAuth launches a temporary loopback OAuth2 receiver on 127.0.0.1,
// opens the Google consent URL in the user's browser, exchanges the code with PKCE,
// saves the token securely, and immediately syncs Google calendars.
func (e *Engine) StartGoogleCalAuth(ctx context.Context, clientID, clientSecret string) (string, error) {
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	if clientID == "" {
		return "", fmt.Errorf("Google OAuth Client-ID erforderlich")
	}
	if clientSecret != "" {
		if err := e.secrets.Set(secretGCalSecret, clientSecret); err != nil {
			return "", fmt.Errorf("Client-Secret speichern: %w", err)
		}
	} else if stored, err := e.secrets.Get(secretGCalSecret); err == nil {
		clientSecret = stored
	}

	// Persist ClientID in config right away.
	cfg := e.loadConfig()
	cfg.Direct.GoogleCalendar.ClientID = clientID
	cfg.Direct.GoogleCalendar.Enabled = true
	if err := e.saveConfig(cfg); err != nil {
		return "", fmt.Errorf("Konfiguration speichern: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("OAuth Loopback-Port öffnen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/oauth2/callback", port)
	oauthCfg := e.gcalOAuthConfig(clientID, clientSecret, redirectURL)

	state := randomHex(16)
	verifier := randomBase64URL(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authURL := oauthCfg.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	// Tie the loopback OAuth callback server to the Engine lifecycle (5 min window),
	// NOT the caller's short RPC/UI context which cancels as soon as StartGoogleCalAuth returns.
	authCtx, authCancel := context.WithTimeout(e.rootContext(), 5*time.Minute)
	e.mu.Lock()
	if e.gcalOAuthCancel != nil {
		e.gcalOAuthCancel()
	}
	e.gcalOAuthCancel = authCancel
	e.mu.Unlock()
	e.setGCalErr("")

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	done := make(chan struct{})

	mux.HandleFunc("/oauth2/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer func() {
			select {
			case <-done:
			default:
				close(done)
			}
		}()
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			e.setGCalErr("Google OAuth abgebrochen: " + errParam)
			e.emitCalendarChanged()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, "<html><body style='font-family:system-ui;padding:32px'><h2>Verbindung abgebrochen</h2><p>%s</p></body></html>", html.EscapeString(errParam))
			return
		}
		if q.Get("state") != state {
			e.setGCalErr("Google OAuth State ungültig")
			e.emitCalendarChanged()
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		oauthHTTP := &http.Client{Timeout: 20 * time.Second}
		exCtx := context.WithValue(r.Context(), oauth2.HTTPClient, oauthHTTP)
		tok, err := oauthCfg.Exchange(exCtx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
		if err != nil {
			e.setGCalErr("Token-Austausch fehlgeschlagen: " + err.Error())
			e.emitCalendarChanged()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, "<html><body style='font-family:system-ui;padding:32px'><h2>Fehler beim Token-Austausch</h2><p>%s</p></body></html>", html.EscapeString(err.Error()))
			return
		}
		if err := e.saveGCalToken(tok); err != nil {
			e.setGCalErr("Token speichern: " + err.Error())
		} else {
			e.setGCalErr("")
		}
		e.emitCalendarChanged()

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, "<html><body style='font-family:system-ui;padding:32px;background:#1a1b26;color:#c0caf5'><h2>PostDive × Google verbunden</h2><p>Die Anmeldung war erfolgreich. Du kannst dieses Fenster jetzt schließen.</p></body></html>")

		// Immediately trigger sync in background tied to Engine lifecycle.
		e.spawn(func(rootCtx context.Context) {
			syncCtx, cancel := context.WithTimeout(rootCtx, 45*time.Second)
			defer cancel()
			if err := e.SyncGoogleCalendar(syncCtx); err != nil {
				e.setGCalErr(err.Error())
				e.emitCalendarChanged()
			}
		})
	})

	go func() {
		_ = srv.Serve(ln)
	}()
	go func() {
		defer authCancel()
		select {
		case <-done:
			time.Sleep(1 * time.Second)
		case <-authCtx.Done():
		}
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if !e.disableBrowserOpen {
		_ = openURLInBrowser(authURL)
	}
	return authURL, nil
}

// DisconnectGoogleCal clears Google Calendar OAuth tokens, client secret, and cached Google calendars.
func (e *Engine) DisconnectGoogleCal(ctx context.Context) error {
	_ = e.secrets.Delete(secretGCalToken)
	_ = e.secrets.Delete(secretGCalSecret)
	_ = e.store.DeleteCalendarsBySource(ctx, "google")
	cfg := e.loadConfig()
	cfg.Direct.GoogleCalendar.Enabled = false
	cfg.Direct.GoogleCalendar.AccountEmail = ""
	_ = e.saveConfig(cfg)
	e.setGCalErr("")
	e.emitCalendarChanged()
	return nil
}

func (e *Engine) saveGCalToken(tok *oauth2.Token) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	return e.secrets.Set(secretGCalToken, string(b))
}

func (e *Engine) loadGCalHTTPClient(ctx context.Context) (*http.Client, error) {
	cfg := e.loadConfig()
	if cfg.Direct.GoogleCalendar.ClientID == "" {
		return nil, fmt.Errorf("Google Calendar nicht konfiguriert: Client-ID fehlt (Einstellungen → Konten → Kalender)")
	}
	rawTok, err := e.secrets.Get(secretGCalToken)
	if err != nil {
		return nil, fmt.Errorf("Google Calendar nicht angemeldet")
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(rawTok), &tok); err != nil {
		return nil, fmt.Errorf("Google Token beschädigt: %w", err)
	}
	secret, _ := e.secrets.Get(secretGCalSecret)
	oauthCfg := e.gcalOAuthConfig(cfg.Direct.GoogleCalendar.ClientID, secret, "http://127.0.0.1")
	baseHTTP := &http.Client{Timeout: 30 * time.Second}
	tokCtx := context.WithValue(ctx, oauth2.HTTPClient, baseHTTP)
	ts := oauthCfg.TokenSource(tokCtx, &tok)
	fresh, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("Google Token-Aktualisierung fehlgeschlagen: %w", err)
	}
	if fresh.AccessToken != tok.AccessToken || !fresh.Expiry.Equal(tok.Expiry) {
		_ = e.saveGCalToken(fresh)
	}
	hc := oauth2.NewClient(tokCtx, oauth2.StaticTokenSource(fresh))
	hc.Timeout = 30 * time.Second
	return hc, nil
}

// SyncGoogleCalendar synchronizes the user's Google calendars and events bidirectionally
// (using syncToken for fast incremental sync, falling back cleanly on 410 Gone).
func (e *Engine) SyncGoogleCalendar(ctx context.Context) error {
	hc, err := e.loadGCalHTTPClient(ctx)
	if err != nil {
		return err
	}
	hc.Timeout = 30 * time.Second

	// 1. Fetch CalendarList.
	var listResp struct {
		Items []struct {
			ID              string `json:"id"`
			Summary         string `json:"summary"`
			SummaryOverride string `json:"summaryOverride"`
			Primary         bool   `json:"primary"`
			AccessRole      string `json:"accessRole"`
			BackgroundColor string `json:"backgroundColor"`
			Selected        bool   `json:"selected"`
		} `json:"items"`
	}
	if err := doGCalJSON(ctx, hc, http.MethodGet, e.gcalBaseURL()+"/users/me/calendarList", nil, &listResp); err != nil {
		e.setGCalErr(err.Error())
		return err
	}

	existingCals, _ := e.store.ListCalendars(ctx)
	syncTokens := map[string]string{}
	for _, c := range existingCals {
		if c.Source == "google" {
			syncTokens[c.ID] = c.SyncToken
		}
	}

	var primaryEmail string
	for _, item := range listResp.Items {
		name := item.SummaryOverride
		if name == "" {
			name = item.Summary
		}
		if name == "" {
			name = item.ID
		}
		if item.Primary && strings.Contains(item.ID, "@") {
			primaryEmail = item.ID
		}
		ro := item.AccessRole != "owner" && item.AccessRole != "writer"
		color := item.BackgroundColor
		if color == "" {
			color = "#7aa2f7"
		}
		calID := "gcal:" + item.ID
		if err := e.store.UpsertCalendar(ctx, StoredCalendar{
			ID:         calID,
			Name:       name,
			Source:     "google",
			ReadOnly:   ro,
			Color:      color,
			ExternalID: item.ID,
			SyncToken:  syncTokens[calID],
		}); err != nil {
			continue
		}
		if err := e.syncSingleGoogleCalendar(ctx, hc, calID, name, item.ID, ro, syncTokens[calID]); err != nil {
			e.setGCalErr(err.Error())
			continue
		}
	}

	if primaryEmail != "" {
		cfg := e.loadConfig()
		if cfg.Direct.GoogleCalendar.AccountEmail != primaryEmail {
			cfg.Direct.GoogleCalendar.AccountEmail = primaryEmail
			_ = e.saveConfig(cfg)
		}
	}
	e.setGCalErr("")
	e.emitCalendarChanged()
	return nil
}

type gcalEventPayload struct {
	ID             string `json:"id,omitempty"`
	ICalUID        string `json:"iCalUID,omitempty"`
	Status         string `json:"status,omitempty"`
	Summary        string `json:"summary,omitempty"`
	Location       string `json:"location,omitempty"`
	Description    string `json:"description,omitempty"`
	HangoutLink    string `json:"hangoutLink,omitempty"`
	ConferenceData *struct {
		EntryPoints []struct {
			EntryPointType string `json:"entryPointType,omitempty"`
			URI            string `json:"uri,omitempty"`
			Label          string `json:"label,omitempty"`
		} `json:"entryPoints,omitempty"`
	} `json:"conferenceData,omitempty"`
	ETag             string   `json:"etag,omitempty"`
	RecurringEventID string   `json:"recurringEventId,omitempty"`
	Recurrence       []string `json:"recurrence,omitempty"`
	Start            struct {
		DateTime string `json:"dateTime,omitempty"`
		Date     string `json:"date,omitempty"`
		TimeZone string `json:"timeZone,omitempty"`
	} `json:"start"`
	End struct {
		DateTime string `json:"dateTime,omitempty"`
		Date     string `json:"date,omitempty"`
		TimeZone string `json:"timeZone,omitempty"`
	} `json:"end"`
	Attendees []struct {
		Email          string `json:"email,omitempty"`
		Self           bool   `json:"self,omitempty"`
		ResponseStatus string `json:"responseStatus,omitempty"`
	} `json:"attendees,omitempty"`
}

func (e *Engine) syncSingleGoogleCalendar(ctx context.Context, hc *http.Client, calID, calName, googleCalID string, readOnly bool, syncToken string) error {
	fullSync := syncToken == ""
	var collected []StoredEvent
	pageToken := ""
	nextSyncToken := ""

	for pageNum := 0; pageNum < 100; pageNum++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		vals := url.Values{}
		vals.Set("maxResults", "250")
		if fullSync {
			// Pull 60 days back through 1 year ahead; keep recurrence rules intact so rrule-go expands cleanly,
			// or singleEvents=true if we want Google's own DST/EXDATE expansion.
			// Note: Google API does NOT allow syncToken together with timeMin/timeMax/singleEvents=true,
			// so for syncToken support we sync master events + exceptions cleanly!
			vals.Set("showDeleted", "true")
			vals.Set("timeMin", time.Now().AddDate(0, -2, 0).UTC().Format(time.RFC3339))
		} else {
			vals.Set("syncToken", syncToken)
			vals.Set("showDeleted", "true")
		}
		if pageToken != "" {
			vals.Set("pageToken", pageToken)
		}

		endpoint := fmt.Sprintf("%s/calendars/%s/events?%s", e.gcalBaseURL(), url.PathEscape(googleCalID), vals.Encode())
		var page struct {
			Items         []gcalEventPayload `json:"items"`
			NextPageToken string             `json:"nextPageToken"`
			NextSyncToken string             `json:"nextSyncToken"`
		}
		err := doGCalJSON(ctx, hc, http.MethodGet, endpoint, nil, &page)
		if err != nil {
			// 410 Gone means syncToken expired — fall back to a clean full resync once.
			if !fullSync && strings.Contains(err.Error(), "HTTP 410") {
				fullSync = true
				syncToken = ""
				pageToken = ""
				collected = nil
				continue
			}
			return err
		}

		for _, it := range page.Items {
			se := gcalPayloadToStored(calID, calName, readOnly, it)
			if fullSync {
				if se.Status != "cancelled" {
					collected = append(collected, se)
				}
			} else {
				if se.Status == "cancelled" {
					_ = e.store.DeleteEvent(ctx, calID, se.ID)
				} else {
					_ = e.store.UpsertEvent(ctx, se)
				}
			}
		}

		if page.NextSyncToken != "" {
			nextSyncToken = page.NextSyncToken
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}

	if fullSync {
		if err := e.store.ReplaceCalendarEvents(ctx, calID, collected); err != nil {
			return err
		}
	}
	return e.store.SetCalendarSyncToken(ctx, calID, nextSyncToken)
}

func gcalPayloadToStored(calID, calName string, readOnly bool, it gcalEventPayload) StoredEvent {
	allDay := it.Start.Date != "" && it.Start.DateTime == ""
	startISO := it.Start.DateTime
	endISO := it.End.DateTime
	if allDay {
		startISO = it.Start.Date
		endISO = it.End.Date
	}
	st := parseFlexibleEventTime(startISO, 0)
	en := parseFlexibleEventTime(endISO, st.Add(time.Hour).Unix())

	rruleStr := ""
	for _, r := range it.Recurrence {
		if strings.HasPrefix(strings.ToUpper(r), "RRULE:") {
			rruleStr = strings.TrimSpace(r[len("RRULE:"):])
			break
		}
	}

	desc := it.Description
	confURL := strings.TrimSpace(it.HangoutLink)
	if confURL == "" && it.ConferenceData != nil {
		for _, ep := range it.ConferenceData.EntryPoints {
			if strings.EqualFold(ep.EntryPointType, "video") && strings.TrimSpace(ep.URI) != "" {
				confURL = strings.TrimSpace(ep.URI)
				break
			}
		}
	}
	if confURL != "" && !strings.Contains(desc, confURL) && !strings.Contains(it.Location, confURL) {
		if strings.TrimSpace(desc) == "" {
			desc = confURL
		} else {
			desc = confURL + "\n\n" + desc
		}
	}

	selfResp := ""
	for _, att := range it.Attendees {
		if att.Self {
			switch strings.ToLower(strings.TrimSpace(att.ResponseStatus)) {
			case "accepted":
				selfResp = "accepted"
			case "tentative":
				selfResp = "tentative"
			case "declined":
				selfResp = "declined"
			case "needsaction":
				selfResp = "needsAction"
			}
			break
		}
	}

	return StoredEvent{
		ID:           calID + "::" + it.ID,
		CalendarID:   calID,
		CalendarName: calName,
		ExternalID:   it.ID,
		UID:          it.ICalUID,
		Title:        it.Summary,
		StartISO:     startISO,
		EndISO:       endISO,
		StartUnix:    st.Unix(),
		EndUnix:      en.Unix(),
		AllDay:       allDay,
		Location:     it.Location,
		Description:  desc,
		RRule:        rruleStr,
		IsRecurring:  rruleStr != "" || it.RecurringEventID != "",
		ReadOnly:     readOnly,
		ETag:         it.ETag,
		Status:       it.Status,
		SelfResponse: selfResp,
	}
}

func (e *Engine) createGoogleEvent(ctx context.Context, cal StoredCalendar, req backend.CreateCalendarEventRequest) (StoredEvent, error) {
	hc, err := e.loadGCalHTTPClient(ctx)
	if err != nil {
		return StoredEvent{}, err
	}
	payload := buildGCalWritePayload(req.Title, req.Start, req.End, req.AllDay, req.Location, req.Description, req.RecurrenceRule)
	var created gcalEventPayload
	endpoint := fmt.Sprintf("%s/calendars/%s/events", e.gcalBaseURL(), url.PathEscape(cal.ExternalID))
	if err := doGCalJSON(ctx, hc, http.MethodPost, endpoint, payload, &created); err != nil {
		return StoredEvent{}, err
	}
	se := gcalPayloadToStored(cal.ID, cal.Name, cal.ReadOnly, created)
	if err := e.store.UpsertEvent(ctx, se); err != nil {
		return StoredEvent{}, err
	}
	return se, nil
}

// importGoogleEvent imports an RFC 5545 event into a Google Calendar via events.import
// (preserving iCalUID for deduplication without sending duplicate invite emails),
// falling back to events.insert if needed.
func (e *Engine) importGoogleEvent(ctx context.Context, cal StoredCalendar, ev StoredEvent) (StoredEvent, error) {
	hc, err := e.loadGCalHTTPClient(ctx)
	if err != nil {
		return StoredEvent{}, err
	}
	payload := buildGCalWritePayload(ev.Title, ev.StartISO, ev.EndISO, ev.AllDay, ev.Location, ev.Description, ev.RRule)
	var created gcalEventPayload
	uid := strings.TrimSpace(ev.UID)
	if uid != "" {
		payload["iCalUID"] = uid
		importEndpoint := fmt.Sprintf("%s/calendars/%s/events/import", e.gcalBaseURL(), url.PathEscape(cal.ExternalID))
		if err := doGCalJSON(ctx, hc, http.MethodPost, importEndpoint, payload, &created); err != nil {
			// Fallback to standard insert if Google rejects an edge-case iCalUID/recurrence payload.
			delete(payload, "iCalUID")
			insertEndpoint := fmt.Sprintf("%s/calendars/%s/events", e.gcalBaseURL(), url.PathEscape(cal.ExternalID))
			if err2 := doGCalJSON(ctx, hc, http.MethodPost, insertEndpoint, payload, &created); err2 != nil {
				return StoredEvent{}, err2
			}
		}
	} else {
		insertEndpoint := fmt.Sprintf("%s/calendars/%s/events", e.gcalBaseURL(), url.PathEscape(cal.ExternalID))
		if err := doGCalJSON(ctx, hc, http.MethodPost, insertEndpoint, payload, &created); err != nil {
			return StoredEvent{}, err
		}
	}
	se := gcalPayloadToStored(cal.ID, cal.Name, cal.ReadOnly, created)
	if ev.SelfResponse != "" && se.SelfResponse == "" {
		se.SelfResponse = ev.SelfResponse
	}
	if err := e.store.UpsertEvent(ctx, se); err != nil {
		return StoredEvent{}, err
	}
	return se, nil
}

func (e *Engine) updateGoogleEvent(ctx context.Context, cal StoredCalendar, existing StoredEvent) (StoredEvent, error) {
	hc, err := e.loadGCalHTTPClient(ctx)
	if err != nil {
		return StoredEvent{}, err
	}
	payload := buildGCalWritePayload(existing.Title, existing.StartISO, existing.EndISO, existing.AllDay, existing.Location, existing.Description, existing.RRule)
	var updated gcalEventPayload
	endpoint := fmt.Sprintf("%s/calendars/%s/events/%s", e.gcalBaseURL(), url.PathEscape(cal.ExternalID), url.PathEscape(existing.ExternalID))
	if err := doGCalJSON(ctx, hc, http.MethodPatch, endpoint, payload, &updated); err != nil {
		return StoredEvent{}, err
	}
	se := gcalPayloadToStored(cal.ID, cal.Name, cal.ReadOnly, updated)
	if err := e.store.UpsertEvent(ctx, se); err != nil {
		return StoredEvent{}, err
	}
	return se, nil
}

func (e *Engine) deleteGoogleEvent(ctx context.Context, cal StoredCalendar, existing StoredEvent) error {
	hc, err := e.loadGCalHTTPClient(ctx)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/calendars/%s/events/%s", e.gcalBaseURL(), url.PathEscape(cal.ExternalID), url.PathEscape(existing.ExternalID))
	if err := doGCalJSON(ctx, hc, http.MethodDelete, endpoint, nil, nil); err != nil {
		return err
	}
	return e.store.DeleteEvent(ctx, cal.ID, existing.ID)
}

// deleteGoogleEventOccurrence cancels a single instance of a recurring Google Calendar event
// without deleting the master series.
func (e *Engine) deleteGoogleEventOccurrence(ctx context.Context, cal StoredCalendar, existing StoredEvent, recurrenceID string) error {
	occTime := parseFlexibleEventTime(recurrenceID, 0)
	if occTime.IsZero() {
		return fmt.Errorf("ungültige Termininstanz %q – ganze Serie wird geschützt", recurrenceID)
	}
	hc, err := e.loadGCalHTTPClient(ctx)
	if err != nil {
		return err
	}
	vals := url.Values{}
	vals.Set("timeMin", occTime.Add(-2*time.Minute).UTC().Format(time.RFC3339))
	vals.Set("timeMax", occTime.Add(24*time.Hour+2*time.Minute).UTC().Format(time.RFC3339))
	vals.Set("maxResults", "20")
	instURL := fmt.Sprintf("%s/calendars/%s/events/%s/instances?%s",
		e.gcalBaseURL(), url.PathEscape(cal.ExternalID), url.PathEscape(existing.ExternalID), vals.Encode())

	var instPage struct {
		Items []struct {
			ID                string `json:"id"`
			Status            string `json:"status"`
			OriginalStartTime struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"originalStartTime"`
			Start struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"start"`
		} `json:"items"`
	}
	if err := doGCalJSON(ctx, hc, http.MethodGet, instURL, nil, &instPage); err != nil {
		return fmt.Errorf("Google Termininstanz nicht abrufbar: %w", err)
	}

	targetDay := occTime.UTC().Format("2006-01-02")
	var instanceID string
	for _, it := range instPage.Items {
		if it.ID == "" || it.Status == "cancelled" {
			continue
		}
		candISO := it.OriginalStartTime.DateTime
		if candISO == "" {
			candISO = it.OriginalStartTime.Date
		}
		if candISO == "" {
			candISO = it.Start.DateTime
		}
		if candISO == "" {
			candISO = it.Start.Date
		}
		if candISO == recurrenceID {
			instanceID = it.ID
			break
		}
		ct := parseFlexibleEventTime(candISO, 0)
		if existing.AllDay && !ct.IsZero() && ct.UTC().Format("2006-01-02") == targetDay {
			instanceID = it.ID
			break
		}
		if !ct.IsZero() && absDuration(ct.Sub(occTime)) <= 2*time.Minute {
			instanceID = it.ID
			break
		}
	}
	if instanceID == "" {
		return fmt.Errorf("einzelne Google-Termininstanz (%s) nicht gefunden – bitte in Google Kalender prüfen oder ganze Serie löschen", recurrenceID)
	}
	delURL := fmt.Sprintf("%s/calendars/%s/events/%s", e.gcalBaseURL(), url.PathEscape(cal.ExternalID), url.PathEscape(instanceID))
	if err := doGCalJSON(ctx, hc, http.MethodDelete, delURL, nil, nil); err != nil {
		return err
	}
	if !slices.Contains(existing.ExDates, recurrenceID) {
		existing.ExDates = append(existing.ExDates, recurrenceID)
	}
	return e.store.UpsertEvent(ctx, existing)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func buildGCalWritePayload(title, startISO, endISO string, allDay bool, location, desc, rruleStr string) map[string]any {
	st := parseFlexibleEventTime(startISO, time.Now().Unix())
	en := parseFlexibleEventTime(endISO, st.Add(time.Hour).Unix())
	if !en.After(st) {
		if allDay {
			en = st.AddDate(0, 0, 1)
		} else {
			en = st.Add(time.Hour)
		}
	}
	p := map[string]any{
		"summary":     title,
		"location":    location,
		"description": desc,
	}
	if allDay {
		p["start"] = map[string]string{"date": st.Format("2006-01-02")}
		p["end"] = map[string]string{"date": en.Format("2006-01-02")}
	} else {
		p["start"] = map[string]string{"dateTime": st.Format(time.RFC3339)}
		p["end"] = map[string]string{"dateTime": en.Format(time.RFC3339)}
	}
	if rr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rruleStr), "RRULE:")); rr != "" {
		p["recurrence"] = []string{"RRULE:" + rr}
	}
	return p
}

func doGCalJSON(ctx context.Context, hc *http.Client, method, endpoint string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Google Calendar API HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (e *Engine) setGCalErr(msg string) {
	e.mu.Lock()
	e.gcalLastErr = msg
	e.mu.Unlock()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomBase64URL(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// BrowserOpener, when set, opens a web page instead of open/xdg-open – the app sets it on
// phones, where only the system can.
var BrowserOpener func(url string) error

func openURLInBrowser(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return fmt.Errorf("ungültige Browser-URL")
	}
	clean := u.String()
	if BrowserOpener != nil {
		return BrowserOpener(clean)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", clean)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", clean)
	default:
		cmd = exec.Command("xdg-open", clean)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
