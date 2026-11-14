#!/usr/bin/env bash
set -euo pipefail

# Non-destructive installer for macOS & Linux (Omarchy).
# Never modifies Thunderbird's prefs.js, user.js, or mail profile folders.

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="${OMATHUNDER_PREFIX:-$HOME/.local/bin}"
BIN_PATH="$PREFIX/omathunder"

mkdir -p "$PREFIX"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Nicht unterstützte Architektur: $ARCH" >&2; exit 1 ;;
esac

PREBUILT="$ROOT_DIR/dist/omathunder-${OS}-${ARCH}"
if [[ -x "$PREBUILT" ]]; then
  echo "==> Installiere vorab gebautes Binary: $PREBUILT -> $BIN_PATH"
  cp "$PREBUILT" "$BIN_PATH"
elif command -v go >/dev/null 2>&1; then
  VERSION="$(cat "$ROOT_DIR/VERSION" 2>/dev/null || echo "dev")"
  echo "==> Baue omathunder ($VERSION) lokal nach $BIN_PATH..."
  (cd "$ROOT_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o "$BIN_PATH" ./cmd/omathunder)
else
  echo "Weder vorab gebautes Binary ($PREBUILT) noch Go gefunden." >&2
  exit 1
fi

chmod +x "$BIN_PATH"

echo "==> Registriere Thunderbird Native Messaging Host (ohne Profil-Eingriff)..."
"$BIN_PATH" setup

echo ""
echo "Fertig! Nächste Schritte:"
echo "  1. Stelle sicher, dass '$PREFIX' in deinem PATH liegt."
echo "  2. Installiere das Add-on in Thunderbird:"
echo "     Add-ons & Themes -> Zahnrad -> Add-on aus Datei installieren -> $ROOT_DIR/dist/omathunder-extension.xpi"
echo "  3. Zum rückstandsfreien Entfernen jederzeit: ./uninstall.sh"
