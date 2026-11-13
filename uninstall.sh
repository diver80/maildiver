#!/usr/bin/env bash
set -euo pipefail

# Sauberes, vollständiges Entfernen von omathunder.
# Lässt Thunderbird-Profile, Mails, Kalender und prefs.js zu 100 % unberührt.

PREFIX="${OMATHUNDER_PREFIX:-$HOME/.local/bin}"
BIN_PATH="$PREFIX/omathunder"

echo "==> Entferne omathunder Native Messaging Hosts & lokalen State..."
if [[ -x "$BIN_PATH" ]]; then
  "$BIN_PATH" uninstall || true
  rm -f "$BIN_PATH"
  echo "  - Binary entfernt: $BIN_PATH"
else
  # Fallback falls Binary bereits gelöscht wurde
  rm -f \
    "$HOME/Library/Mozilla/NativeMessagingHosts/dev.omathunder.host.json" \
    "$HOME/Library/Application Support/Mozilla/NativeMessagingHosts/dev.omathunder.host.json" \
    "$HOME/Library/Thunderbird/NativeMessagingHosts/dev.omathunder.host.json" \
    "$HOME/.mozilla/native-messaging-hosts/dev.omathunder.host.json" \
    "$HOME/.thunderbird/native-messaging-hosts/dev.omathunder.host.json"
  rm -rf \
    "$HOME/Library/Application Support/omathunder" \
    "$HOME/Library/Caches/omathunder" \
    "$HOME/.config/omathunder" \
    "$HOME/.local/state/omathunder"
  echo "  - Fallback-Cleanup durchgeführt."
fi

echo ""
echo "omathunder wurde vollständig entfernt. Thunderbird ist davon unberührt."
echo "(Falls du das Add-on in Thunderbird installiert hast, kannst du es dort im Add-on-Manager mit einem Klick entfernen.)"
