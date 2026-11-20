#!/usr/bin/env bash
set -euo pipefail

# Sauberes Entfernen von MailDiver (sowie eventueller alter OmaThunder-Reste).

PREFIX="${MAILDIVER_PREFIX:-$HOME/.local/bin}"
APP_DIR="${MAILDIVER_APP_DIR:-$HOME/Applications}"

echo "==> Entferne MailDiver..."
rm -f "$PREFIX/maildiver" "$PREFIX/omathunder" "$PREFIX/omathunder-ui"
rm -rf "$APP_DIR/MailDiver.app" "$APP_DIR/OmaThunder.app"

rm -rf \
  "$HOME/Library/Application Support/maildiver" \
  "$HOME/Library/Caches/maildiver" \
  "$HOME/.config/maildiver" \
  "$HOME/.local/state/maildiver"

# Eventuelle alte Thunderbird NativeMessaging-Reste mit aufräumen
rm -f \
  "$HOME/Library/Mozilla/NativeMessagingHosts/dev.omathunder.host.json" \
  "$HOME/Library/Application Support/Mozilla/NativeMessagingHosts/dev.omathunder.host.json" \
  "$HOME/Library/Thunderbird/NativeMessagingHosts/dev.omathunder.host.json" \
  "$HOME/.mozilla/native-messaging-hosts/dev.omathunder.host.json" \
  "$HOME/.thunderbird/native-messaging-hosts/dev.omathunder.host.json"

echo "MailDiver wurde entfernt."
