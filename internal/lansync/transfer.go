package lansync

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

// Bundle is the encrypted JSON payload transferred once over the local LAN.
type Bundle struct {
	Version   int                       `json:"version"`
	CreatedAt string                    `json:"createdAt"`
	Accounts  []config.DirectAccount    `json:"accounts,omitempty"`
	Secrets   map[string]string         `json:"secrets,omitempty"` // secretstore keys -> values
	ICalFeeds []config.ICalFeedConfig   `json:"icalFeeds,omitempty"`
	CalDAV    []config.DAVAccountConfig `json:"caldavAccounts,omitempty"`
	CardDAV   []config.DAVAccountConfig `json:"carddavAccounts,omitempty"`
	App       config.AppConfig          `json:"app"`
	Language  string                    `json:"language,omitempty"`
	Theme     config.ThemeConfig        `json:"theme"`
}

// Session represents an active, ephemeral, one-shot AES-256-GCM LAN transfer server.
type Session struct {
	URI       string
	QRPNG     []byte
	ExpiresAt time.Time

	srv    *http.Server
	cancel context.CancelFunc
	mu     sync.Mutex
	done   bool
}

// Close shuts down the ephemeral LAN transfer server immediately.
func (s *Session) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

// Completed reports whether the receiving device has fetched the encrypted bundle.
func (s *Session) Completed() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// BuildBundle collects transferable accounts, credentials (from secretstore), and app settings.
func BuildBundle(cfg config.Config, secrets secretstore.Store) Bundle {
	secMap := make(map[string]string)
	if secrets != nil {
		for _, acc := range cfg.Direct.Accounts {
			for _, k := range []string{"account:" + acc.ID, "oauth:mail:" + acc.ID, "oauth:mail_secret:" + acc.ID} {
				if v, err := secrets.Get(k); err == nil && v != "" {
					secMap[k] = v
				}
			}
		}
		for _, dav := range append(append([]config.DAVAccountConfig(nil), cfg.Direct.CalDAVAccounts...), cfg.Direct.CardDAVAccounts...) {
			k := "dav:" + dav.ID
			if v, err := secrets.Get(k); err == nil && v != "" {
				secMap[k] = v
			}
		}
	}
	return Bundle{
		Version:   1,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Accounts:  cfg.Direct.Accounts,
		Secrets:   secMap,
		ICalFeeds: cfg.Direct.ICalFeeds,
		CalDAV:    cfg.Direct.CalDAVAccounts,
		CardDAV:   cfg.Direct.CardDAVAccounts,
		App:       cfg.App,
		Language:  cfg.Language,
		Theme:     cfg.Theme,
	}
}

// EncryptBundle encrypts bundle with a freshly generated 256-bit AES-GCM key.
func EncryptBundle(b Bundle) (ciphertext []byte, key []byte, err error) {
	plain, err := json.Marshal(b)
	if err != nil {
		return nil, nil, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, nil, err
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), key, nil
}

// DecryptBundle decrypts an AES-256-GCM payload produced by EncryptBundle.
func DecryptBundle(ciphertext, key []byte) (Bundle, error) {
	if len(key) != 32 {
		return Bundle{}, fmt.Errorf("ungültiger Transferschlüssel")
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return Bundle{}, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return Bundle{}, err
	}
	ns := gcm.NonceSize()
	if len(ciphertext) <= ns {
		return Bundle{}, fmt.Errorf("beschädigte Transferdaten")
	}
	plain, err := gcm.Open(nil, ciphertext[:ns], ciphertext[ns:], nil)
	if err != nil {
		return Bundle{}, fmt.Errorf("Entschlüsselung fehlgeschlagen (falscher QR-Code?): %w", err)
	}
	var out Bundle
	if err := json.Unmarshal(plain, &out); err != nil {
		return Bundle{}, err
	}
	return out, nil
}

func detectLANIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			v4 := ipNet.IP.To4()
			if v4 == nil {
				continue
			}
			if addr, ok := netip.AddrFromSlice(v4); ok && addr.IsPrivate() {
				return v4.String()
			}
		}
	}
	return "127.0.0.1"
}

func isPrivateOrLoopbackHost(host string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast()
}

// StartSession starts a one-shot, 3-minute, AES-256-GCM encrypted LAN transfer endpoint and
// generates a scannable QR code PNG. Passing listenHost="" binds 0.0.0.0 and advertises the local LAN IPv4.
func StartSession(parent context.Context, bundle Bundle, listenHost string, ttl time.Duration) (*Session, error) {
	if ttl <= 0 {
		ttl = 3 * time.Minute
	}
	ciphertext, key, err := EncryptBundle(bundle)
	if err != nil {
		return nil, err
	}
	tokBytes := make([]byte, 16)
	if _, err := rand.Read(tokBytes); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokBytes)

	bindAddr := "0.0.0.0:0"
	advHost := detectLANIPv4()
	if listenHost != "" {
		bindAddr = net.JoinHostPort(listenHost, "0")
		advHost = listenHost
	}
	ln, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("LAN-Transfer-Port nicht verfügbar: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	fetchURL := fmt.Sprintf("http://%s/transfer", net.JoinHostPort(advHost, fmt.Sprintf("%d", port)))

	q := url.Values{}
	q.Set("url", fetchURL)
	q.Set("t", token)
	q.Set("k", base64.RawURLEncoding.EncodeToString(key))
	pairURI := "postdive-pair://v1?" + q.Encode()

	qrPNG, err := qrcode.Encode(pairURI, qrcode.Medium, 256)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("QR-Code konnte nicht erzeugt werden: %w", err)
	}

	ctx, cancel := context.WithTimeout(parent, ttl)
	sess := &Session{
		URI:       pairURI,
		QRPNG:     qrPNG,
		ExpiresAt: time.Now().Add(ttl),
		cancel:    cancel,
	}

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	sess.srv = srv

	mux.HandleFunc("/transfer", func(w http.ResponseWriter, r *http.Request) {
		gotTok := r.URL.Query().Get("t")
		if subtle.ConstantTimeCompare([]byte(gotTok), []byte(token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		sess.mu.Lock()
		if sess.done {
			sess.mu.Unlock()
			http.Error(w, "already transferred", http.StatusGone)
			return
		}
		sess.done = true
		sess.mu.Unlock()

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(ciphertext)
		go func() {
			time.Sleep(250 * time.Millisecond)
			_ = srv.Close()
			cancel()
		}()
	})

	go func() {
		_ = srv.Serve(ln)
	}()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	return sess, nil
}

// ImportFromPairingURI fetches and decrypts a LAN QR pairing URI (only private LAN / loopback hosts allowed),
// and merges the transferred accounts, credentials, calendars, and app preferences into cfg and secrets.
func ImportFromPairingURI(ctx context.Context, pairURI string, curCfg config.Config, secrets secretstore.Store) (config.Config, int, error) {
	u, err := url.Parse(strings.TrimSpace(pairURI))
	if err != nil || !strings.EqualFold(u.Scheme, "postdive-pair") {
		return curCfg, 0, fmt.Errorf("ungültiger PostDive-Pairing-Code")
	}
	fetchRaw := u.Query().Get("url")
	token := u.Query().Get("t")
	keyEnc := u.Query().Get("k")
	if fetchRaw == "" || token == "" || keyEnc == "" {
		return curCfg, 0, fmt.Errorf("unvollständiger Pairing-Code")
	}
	fu, err := url.Parse(fetchRaw)
	if err != nil || (fu.Scheme != "http" && fu.Scheme != "https") || !isPrivateOrLoopbackHost(fu.Hostname()) {
		return curCfg, 0, fmt.Errorf("Pairing-URL muss im lokalen Netzwerk (LAN) liegen")
	}
	key, err := base64.RawURLEncoding.DecodeString(keyEnc)
	if err != nil || len(key) != 32 {
		return curCfg, 0, fmt.Errorf("ungültiger AES-Schlüssel im Pairing-Code")
	}

	fq := fu.Query()
	fq.Set("t", token)
	fu.RawQuery = fq.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fu.String(), nil)
	if err != nil {
		return curCfg, 0, err
	}
	hc := &http.Client{Timeout: 15 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return curCfg, 0, fmt.Errorf("LAN-Verbindung fehlgeschlagen (gleiches WLAN prüfen): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return curCfg, 0, fmt.Errorf("Transfer abgelehnt (HTTP %d — QR-Code abgelaufen oder bereits verwendet)", resp.StatusCode)
	}
	ct, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return curCfg, 0, err
	}
	bundle, err := DecryptBundle(ct, key)
	if err != nil {
		return curCfg, 0, err
	}

	merged := curCfg
	existingAcc := make(map[string]bool)
	for _, a := range merged.Direct.Accounts {
		existingAcc[strings.ToLower(strings.TrimSpace(a.Email))] = true
	}
	added := 0
	for _, a := range bundle.Accounts {
		em := strings.ToLower(strings.TrimSpace(a.Email))
		if em == "" || existingAcc[em] {
			continue
		}
		merged.Direct.Accounts = append(merged.Direct.Accounts, a)
		existingAcc[em] = true
		added++
		// As an account added in the accounts dialog: PostDive may act on it.
		if !merged.Mailbox.AccountEnabled(a.ID) {
			merged.Mailbox.TriageAccountIDs = append(merged.Mailbox.TriageAccountIDs, a.ID)
		}
	}
	if added > 0 {
		merged.Mailbox.ReadOnly = false
	}
	if secrets != nil {
		for k, v := range bundle.Secrets {
			if k != "" && v != "" {
				_ = secrets.Set(k, v)
			}
		}
	}
	if len(bundle.App.AllowedImageSenders) > 0 {
		merged.App.AllowedImageSenders = append(merged.App.AllowedImageSenders, bundle.App.AllowedImageSenders...)
	}
	if len(bundle.App.HiddenCalendars) > 0 && len(merged.App.HiddenCalendars) == 0 {
		merged.App.HiddenCalendars = bundle.App.HiddenCalendars
	}
	if bundle.Language != "" {
		merged.Language = bundle.Language
	}
	merged = config.Normalize(merged)
	return merged, added, nil
}
