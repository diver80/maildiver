#!/usr/bin/env bash
set -euo pipefail

# Sauberes Entfernen von MailDiver (sowie eventueller alter OmaThunder-Reste).
# Standardmäßig bleiben Benutzerdaten (config.json, maildiver.db, Kalender, Kontakte, Regeln) erhalten.
# Mit --purge werden auch Konfiguration und lokale Datenbank gelöscht.

PURGE=false
for arg in "$@"; do
  case "$arg" in
    --purge) PURGE=true ;;
    -h|--help)
      echo "Usage: ./uninstall.sh [--purge]"
      echo "  (default)  Removes MailDiver.app and CLI binaries, keeping user data & config."
      echo "  --purge    Also deletes config.json, maildiver.db and local caches."
      exit 0
      ;;
    *)
      echo "Unknown option: $arg" >&2
      exit 1
      ;;
  esac
done

PREFIX="${MAILDIVER_PREFIX:-$HOME/.local/bin}"
APP_DIR="${MAILDIVER_APP_DIR:-$HOME/Applications}"

echo "==> Entferne MailDiver-Programme..."
rm -f "$PREFIX/maildiver" "$PREFIX/omathunder" "$PREFIX/omathunder-ui"
rm -rf "$APP_DIR/MailDiver.app" "$APP_DIR/OmaThunder.app"

if [[ "$PURGE" == "true" ]]; then
  echo "==> Entferne Konfiguration & lokale Datenbank (--purge)..."
  rm -rf \
    "$HOME/Library/Application Support/maildiver" \
    "$HOME/Library/Caches/maildiver" \
    "$HOME/.config/maildiver" \
    "$HOME/.local/share/maildiver" \
    "$HOME/.local/state/maildiver"
else
  echo "Hinweis: Benutzerdaten (config.json, maildiver.db) wurden behalten."
  echo "Zum vollständigen Löschen: ./uninstall.sh --purge"
fi

# Eventuelle alte Thunderbird NativeMessaging-Reste mit aufräumen
rm -f \
  "$HOME/Library/Mozilla/NativeMessagingHosts/dev.omathunder.host.json" \
  "$HOME/Library/Application Support/Mozilla/NativeMessagingHosts/dev.omathunder.host.json" \
  "$HOME/Library/Thunderbird/NativeMessagingHosts/dev.omathunder.host.json" \
  "$HOME/.mozilla/native-messaging-hosts/dev.omathunder.host.json" \
  "$HOME/.thunderbird/native-messaging-hosts/dev.omathunder.host.json"

echo "MailDiver wurde entfernt."
