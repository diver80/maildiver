package mailengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/diver80/postdive/internal/config"
)

// XOAUTH2Mechanism is the SASL mechanism name used by Gmail and Microsoft 365 IMAP/SMTP.
const XOAUTH2Mechanism = "XOAUTH2"

type xoauth2Client struct {
	username string
	token    string
}

// NewXOAUTH2Client implements the XOAUTH2 SASL mechanism used by Gmail and Microsoft 365.
func NewXOAUTH2Client(username, accessToken string) sasl.Client {
	return &xoauth2Client{username: username, token: accessToken}
}

func (c *xoauth2Client) Start() (mech string, ir []byte, err error) {
	ir = []byte("user=" + c.username + "\x01auth=Bearer " + c.token + "\x01\x01")
	return XOAUTH2Mechanism, ir, nil
}

func (c *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	// Per Gmail / Microsoft XOAUTH2 spec, an intermediate JSON challenge indicates an error;
	// replying with an empty response completes the SASL exchange cleanly so the server returns the error.
	if len(challenge) > 0 {
		return []byte{}, nil
	}
	return nil, nil
}

func mailOAuthConfig(provider, clientID, clientSecret, tenant, redirectURL string) (*oauth2.Config, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "google", "gmail":
		return &oauth2.Config{
			ClientID:     strings.TrimSpace(clientID),
			ClientSecret: strings.TrimSpace(clientSecret),
			RedirectURL:  redirectURL,
			Endpoint:     google.Endpoint,
			Scopes:       []string{"https://mail.google.com/"},
		}, nil
	case "microsoft", "office365", "m365", "outlook":
		t := strings.TrimSpace(tenant)
		if t == "" {
			t = "common"
		}
		return &oauth2.Config{
			ClientID:     strings.TrimSpace(clientID),
			ClientSecret: strings.TrimSpace(clientSecret),
			RedirectURL:  redirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:  fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", t),
				TokenURL: fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", t),
			},
			Scopes: []string{
				"https://outlook.office.com/IMAP.AccessAsUser.All",
				"https://outlook.office.com/SMTP.Send",
				"offline_access",
			},
		}, nil
	default:
		return nil, fmt.Errorf("unbekannter OAuth2-Anbieter %q (erwartet: google oder microsoft)", provider)
	}
}

func mailOAuthTokenKey(accountID string) string {
	return "oauth:mail:" + strings.TrimSpace(accountID)
}

func mailOAuthSecretKey(accountID string) string {
	return "oauth:mail_secret:" + strings.TrimSpace(accountID)
}

// StartMailOAuth2 launches a browser PKCE loopback OAuth2 flow for Gmail or Microsoft 365 XOAUTH2
// and stores the refreshable token in the OS Keychain / secretstore.
func (e *Engine) StartMailOAuth2(ctx context.Context, acc config.DirectAccount, clientSecret string) (string, error) {
	cid := strings.TrimSpace(acc.OAuthClientID)
	if cid == "" {
		return "", fmt.Errorf("OAuth2 Client-ID fehlt")
	}
	if acc.ID == "" {
		return "", fmt.Errorf("Konto-ID fehlt")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("lokaler OAuth-Callback-Port konnte nicht geöffnet werden: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/oauth2/mail/callback", port)

	ocfg, err := mailOAuthConfig(acc.OAuthProvider, cid, clientSecret, acc.OAuthTenant, redirectURL)
	if err != nil {
		_ = ln.Close()
		return "", err
	}
	verifier := oauth2.GenerateVerifier()
	state := randomHex(16)
	authURL := ocfg.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.S256ChallengeOption(verifier),
	)

	if strings.TrimSpace(clientSecret) != "" {
		_ = e.secrets.Set(mailOAuthSecretKey(acc.ID), strings.TrimSpace(clientSecret))
	}

	// Detached context so the loopback server survives short UI RPC timeouts until the user finishes sign-in.
	oauthCtx, cancel := context.WithTimeout(e.rootCtx, 5*time.Minute)
	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	mux.HandleFunc("/oauth2/mail/callback", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			go func() {
				time.Sleep(400 * time.Millisecond)
				_ = srv.Close()
				cancel()
			}()
		}()
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			return
		}
		if errCode := r.URL.Query().Get("error"); errCode != "" {
			http.Error(w, "OAuth denied: "+errCode, http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			return
		}
		tok, exErr := ocfg.Exchange(oauthCtx, code, oauth2.VerifierOption(verifier))
		if exErr != nil {
			http.Error(w, "Token exchange failed: "+exErr.Error(), http.StatusBadGateway)
			return
		}
		if raw, merr := json.Marshal(tok); merr == nil {
			_ = e.secrets.Set(mailOAuthTokenKey(acc.ID), string(raw))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><body style='font-family:system-ui;padding:32px;background:#121212;color:#eee'><h2>PostDive OAuth2 verbunden ✓</h2><p>Du kannst dieses Browserfenster jetzt schließen.</p></body></html>"))
		go func() {
			// Signing in is the user connecting the account: a paused one (imported from
			// Thunderbird without credentials) starts syncing, as with a first password.
			e.resumeAccount(acc.ID)
			_ = e.SyncNow(e.rootCtx, acc.ID)
		}()
	})

	go func() {
		defer cancel()
		_ = srv.Serve(ln)
	}()
	go func() {
		<-oauthCtx.Done()
		_ = srv.Close()
	}()

	if !e.disableBrowserOpen {
		_ = openURLInBrowser(authURL)
	}
	return authURL, nil
}

// resolveAccountSecret returns the password or fresh XOAUTH2 access token for acc.
func (e *Engine) resolveAccountSecret(ctx context.Context, acc config.DirectAccount) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(acc.AuthType), "xoauth2") {
		return e.secrets.Get(e.accountSecretKey(acc.ID))
	}
	rawTok, err := e.secrets.Get(mailOAuthTokenKey(acc.ID))
	if err != nil || strings.TrimSpace(rawTok) == "" {
		return "", fmt.Errorf("kein OAuth2-Token für %s gespeichert — bitte per OAuth2 anmelden", acc.Email)
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(rawTok), &tok); err != nil {
		return "", fmt.Errorf("OAuth2-Token beschädigt: %w", err)
	}
	// If token is still valid for >2 minutes, use it directly without a network round-trip.
	if tok.AccessToken != "" && (tok.Expiry.IsZero() || time.Until(tok.Expiry) > 2*time.Minute) {
		return tok.AccessToken, nil
	}
	sec, _ := e.secrets.Get(mailOAuthSecretKey(acc.ID))
	ocfg, err := mailOAuthConfig(acc.OAuthProvider, acc.OAuthClientID, sec, acc.OAuthTenant, "")
	if err != nil {
		return "", err
	}
	fresh, err := ocfg.TokenSource(ctx, &tok).Token()
	if err != nil {
		return "", fmt.Errorf("OAuth2-Token-Refresh fehlgeschlagen (%s): %w", acc.Email, err)
	}
	if fresh.AccessToken != tok.AccessToken || fresh.RefreshToken != tok.RefreshToken {
		if b, merr := json.Marshal(fresh); merr == nil {
			_ = e.secrets.Set(mailOAuthTokenKey(acc.ID), string(b))
		}
	}
	return fresh.AccessToken, nil
}
