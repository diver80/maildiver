package nativeui

import (
	"context"
	"errors"
	"strings"

	"github.com/diver80/omathunder/internal/bridgeclient"
)

// errorCode classifies err into a contract error code (§2) or a client-side code.
func errorCode(err error) (code, detail string) {
	if err == nil {
		return "", ""
	}
	if errors.Is(err, bridgeclient.ErrOffline) {
		return "bridge_offline", ""
	}
	var apiErr *bridgeclient.APIError
	if errors.As(err, &apiErr) && apiErr.Code != "" {
		return apiErr.Code, apiErr.Detail
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", ""
	}
	if errors.Is(err, context.Canceled) {
		return "canceled", ""
	}
	msg := err.Error()
	if c, d := bridgeclient.ParseErrorCode(msg); c != "" {
		return c, d
	}
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "thunderbird") && (strings.Contains(low, "disconnected") || strings.Contains(low, "not connected")):
		return "thunderbird_disconnected", ""
	case strings.Contains(low, "connection refused") || strings.Contains(low, "no such file") || strings.Contains(low, "bridge discovery"):
		return "bridge_offline", ""
	case strings.Contains(low, "deadline exceeded") || strings.Contains(low, "timeout"):
		return "timeout", ""
	}
	return "", msg
}

// localizeError turns bridge/RPC error codes into a DE/EN user message.
func localizeError(err error, lang string) string {
	if err == nil {
		return ""
	}
	code, detail := errorCode(err)
	var base string
	switch code {
	case "read_only":
		base = trLang(lang, "Read-Only-Modus aktiv — Schreibzugriff blockiert", "Read-only mode active — write access blocked")
	case "account_not_enabled":
		base = trLang(lang, "Konto ist nicht für Änderungen freigegeben (Triage-/Bridge-Konten prüfen)", "Account is not enabled for changes (check triage/bridge accounts)")
	case "account_not_readable":
		base = trLang(lang, "Konto ist für die Bridge nicht freigegeben", "Account is not readable via the bridge")
	case "send_disabled":
		base = trLang(lang, "Direktes Senden ist deaktiviert (bridge.allowSend)", "Direct sending is disabled (bridge.allowSend)")
	case "feature_disabled":
		base = trLang(lang, "Funktion deaktiviert", "Feature disabled")
	case "cross_account_move":
		base = trLang(lang, "Verschieben zwischen verschiedenen Konten ist nicht erlaubt", "Moving between different accounts is not allowed")
	case "not_found":
		base = trLang(lang, "Nicht gefunden (evtl. bereits verschoben oder gelöscht)", "Not found (maybe already moved or deleted)")
	case "invalid_argument":
		base = trLang(lang, "Ungültige Eingabe", "Invalid input")
	case "ambiguous":
		base = trLang(lang, "Mehrdeutig: Die Mail liegt in mehreren Ordnern", "Ambiguous: the email exists in several folders")
	case "message_changed":
		base = trLang(lang, "Die Mail hat sich inzwischen geändert — bitte Liste aktualisieren", "The email changed in the meantime — please refresh")
	case "conflict":
		base = trLang(lang, "Konflikt — bitte erneut versuchen", "Conflict — please try again")
	case "outcome_unknown":
		base = trLang(lang, "Zeitüberschreitung — Ergebnis unbekannt, bitte in Thunderbird prüfen", "Timed out — outcome unknown, please check in Thunderbird")
		detail = ""
	case "bridge_offline":
		base = trLang(lang, "Thunderbird-Bridge offline", "Thunderbird bridge offline")
		detail = ""
	case "thunderbird_disconnected":
		base = trLang(lang, "Thunderbird ist nicht verbunden", "Thunderbird is not connected")
		detail = ""
	case "timeout":
		base = trLang(lang, "Zeitüberschreitung", "Timed out")
		detail = ""
	case "canceled":
		base = trLang(lang, "Abgebrochen", "Canceled")
		detail = ""
	default:
		return detail
	}
	if detail != "" {
		return base + ": " + detail
	}
	return base
}

func (a *App) errText(err error) string {
	return localizeError(err, a.uiLang())
}
