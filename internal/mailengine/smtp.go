package mailengine

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/diver80/postdive/internal/config"
)

func smtpDeadline(ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}
	return time.Now().Add(30 * time.Second)
}

func dialSMTP(ctx context.Context, acc config.DirectAccount, password string) (*smtp.Client, error) {
	started := time.Now()
	host := strings.TrimSpace(acc.SMTPHost)
	if host == "" {
		return nil, fmt.Errorf("SMTP-Host ist nicht konfiguriert")
	}
	port := acc.SMTPPort
	if port <= 0 {
		port = 465
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	deadline := smtpDeadline(ctx)
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	user := acc.EffectiveUsername()
	mode := strings.ToLower(strings.TrimSpace(acc.SMTPTLS))
	if mode == "" {
		mode = "tls"
	}
	authMode := strings.TrimSpace(acc.AuthType)
	if authMode == "" {
		authMode = "password"
	}
	logAccountDiag("SMTP", "dial host=%s tls=%s user=%s auth=%s pw=(%s)", addr, mode, user, authMode, PasswordMeta(password))

	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		logAccountDiag("SMTP", "dial_err host=%s dur=%s err=%s", addr, time.Since(started).Round(time.Millisecond), SanitizeDiagErr(err))
		return nil, fmt.Errorf("SMTP Verbindung zu %s fehlgeschlagen: %w", addr, err)
	}
	_ = rawConn.SetDeadline(deadline)

	var c *smtp.Client
	switch {
	case mode == "plain" && isLoopbackHost(host):
		c = smtp.NewClient(rawConn)
	case mode == "starttls":
		c, err = smtp.NewClientStartTLS(rawConn, tlsCfg)
		if err != nil {
			_ = rawConn.Close()
			logAccountDiag("SMTP", "starttls_err host=%s dur=%s err=%s", addr, time.Since(started).Round(time.Millisecond), SanitizeDiagErr(err))
			return nil, fmt.Errorf("SMTP STARTTLS zu %s fehlgeschlagen: %w", addr, err)
		}
	default:
		tlsConn := tls.Client(rawConn, tlsCfg)
		if err = tlsConn.HandshakeContext(ctx); err != nil {
			_ = rawConn.Close()
			logAccountDiag("SMTP", "tls_err host=%s dur=%s err=%s", addr, time.Since(started).Round(time.Millisecond), SanitizeDiagErr(err))
			return nil, fmt.Errorf("SMTP TLS zu %s fehlgeschlagen: %w", addr, err)
		}
		c = smtp.NewClient(tlsConn)
	}

	if password != "" {
		ok, mechs := c.Extension("AUTH")
		logAccountDiag("SMTP", "connected host=%s dur=%s auth_ext=%v mechs=%q", addr, time.Since(started).Round(time.Millisecond), ok, strings.TrimSpace(mechs))
		if !ok {
			_ = c.Close()
			return nil, fmt.Errorf("SMTP Server %s unterstützt kein AUTH", addr)
		}
		if strings.EqualFold(authMode, "xoauth2") {
			if err := c.Auth(NewXOAUTH2Client(user, password)); err != nil {
				_ = c.Close()
				logAccountDiag("SMTP", "auth_err host=%s user=%s mech=XOAUTH2 err=%s", addr, user, SanitizeDiagErr(err))
				return nil, fmt.Errorf("SMTP XOAUTH2 Anmeldung fehlgeschlagen (%s): %w", user, err)
			}
			logAccountDiag("SMTP", "auth_ok host=%s user=%s mech=XOAUTH2", addr, user)
			return c, nil
		}
		upperMechs := strings.ToUpper(mechs)
		useLoginFirst := strings.Contains(upperMechs, "LOGIN") && !strings.Contains(upperMechs, "PLAIN")
		firstMech := "PLAIN"
		var auth sasl.Client
		if useLoginFirst {
			firstMech = "LOGIN"
			auth = sasl.NewLoginClient(user, password)
		} else {
			auth = sasl.NewPlainClient("", user, password)
		}
		if err := c.Auth(auth); err != nil {
			logAccountDiag("SMTP", "auth_err host=%s user=%s mech=%s err=%s", addr, user, firstMech, SanitizeDiagErr(err))
			// Retry with LOGIN only when PLAIN was attempted first and LOGIN is advertised.
			if !useLoginFirst && strings.Contains(upperMechs, "LOGIN") {
				if lerr := c.Auth(sasl.NewLoginClient(user, password)); lerr == nil {
					logAccountDiag("SMTP", "auth_ok host=%s user=%s mech=LOGIN_FALLBACK", addr, user)
					return c, nil
				} else {
					logAccountDiag("SMTP", "auth_err host=%s user=%s mech=LOGIN_FALLBACK err=%s", addr, user, SanitizeDiagErr(lerr))
				}
			}
			_ = c.Close()
			return nil, fmt.Errorf("SMTP Anmeldung fehlgeschlagen (%s): %w", user, err)
		}
		logAccountDiag("SMTP", "auth_ok host=%s user=%s mech=%s", addr, user, firstMech)
	}
	return c, nil
}

// TestSMTPConnection verifies SMTP TLS + authentication without sending any email.
func TestSMTPConnection(ctx context.Context, acc config.DirectAccount, password string) error {
	c, err := dialSMTP(ctx, acc, password)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}

func validateSMTPEnvelopeAddr(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n\x00 \t") {
		return "", fmt.Errorf("ungültige E-Mail-Adresse: %q", raw)
	}
	a, err := mail.ParseAddress(raw)
	if err != nil || a.Address == "" || strings.ContainsAny(a.Address, "\r\n\x00 \t") {
		return "", fmt.Errorf("ungültige E-Mail-Adresse: %q", raw)
	}
	return a.Address, nil
}

// SendSMTP transmits a pre-built RFC 5322 email via the account's SMTP server.
func SendSMTP(ctx context.Context, acc config.DirectAccount, password string, fromEmail string, recipients []string, raw []byte) error {
	cleanFrom, err := validateSMTPEnvelopeAddr(fromEmail)
	if err != nil {
		return fmt.Errorf("invalid_argument: Absender %w", err)
	}
	cleanRcpts := make([]string, 0, len(recipients))
	for _, r := range recipients {
		addr, verr := validateSMTPEnvelopeAddr(r)
		if verr != nil {
			return fmt.Errorf("invalid_argument: Empfänger %w", verr)
		}
		cleanRcpts = append(cleanRcpts, addr)
	}
	if len(cleanRcpts) == 0 {
		return fmt.Errorf("invalid_argument: keine Empfänger angegeben")
	}
	c, err := dialSMTP(ctx, acc, password)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SendMail(cleanFrom, cleanRcpts, bytes.NewReader(raw)); err != nil {
		return fmt.Errorf("SMTP Versand fehlgeschlagen: %w", err)
	}
	_ = c.Quit()
	return nil
}
