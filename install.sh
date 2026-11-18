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

APP_DIR="${OMATHUNDER_APP_DIR:-$HOME/Applications}"
APP_PATH="$APP_DIR/OmaThunder.app"
UI_PATH="$PREFIX/omathunder-ui"
VERSION="$(cat "$ROOT_DIR/VERSION" 2>/dev/null || echo "dev")"

# installs <src> to <dest>; never writes through an old symlink (earlier versions linked the
# CLI into the .app bundle, and cp would then overwrite the app binary).
install_bin() {
  rm -f "$2"
  cp "$1" "$2"
  chmod +x "$2"
}

# 1. Native Messaging host + CLI (small, no GUI dependencies).
PREBUILT="$ROOT_DIR/dist/omathunder-${OS}-${ARCH}"
if [[ -x "$PREBUILT" ]]; then
  echo "==> Installiere vorab gebautes Binary: $PREBUILT -> $BIN_PATH"
  install_bin "$PREBUILT" "$BIN_PATH"
elif command -v go >/dev/null 2>&1; then
  echo "==> Baue omathunder ($VERSION) lokal nach $BIN_PATH..."
  rm -f "$BIN_PATH"
  (cd "$ROOT_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o "$BIN_PATH" ./cmd/omathunder)
  chmod +x "$BIN_PATH"
else
  echo "Weder vorab gebautes Binary ($PREBUILT) noch Go gefunden." >&2
  exit 1
fi

# 2. Native desktop app (optional): OmaThunder.app on macOS, omathunder-ui elsewhere.
if [[ "$OS" == "darwin" ]]; then
  PREBUILT_APP=""
  if [[ -d "$ROOT_DIR/dist/OmaThunder.app" ]]; then
    PREBUILT_APP="$ROOT_DIR/dist/OmaThunder.app"
  elif command -v go >/dev/null 2>&1; then
    echo "==> Baue macOS App-Bundle (darwin/${ARCH})..."
    (cd "$ROOT_DIR" && MYGO_LDFLAGS="-X main.version=${VERSION}" \
      go run github.com/egoist/mygo/cmd/mygo@v0.7.4 build -platform "darwin/${ARCH}" -skip-dmg -o "$ROOT_DIR/dist")
    if [[ -d "$ROOT_DIR/dist/darwin-${ARCH}/OmaThunder.app" ]]; then
      PREBUILT_APP="$ROOT_DIR/dist/darwin-${ARCH}/OmaThunder.app"
    fi
  fi
  if [[ -n "$PREBUILT_APP" ]]; then
    echo "==> Installiere macOS App: $PREBUILT_APP -> $APP_PATH"
    mkdir -p "$APP_DIR"
    rm -rf "$APP_PATH"
    cp -R "$PREBUILT_APP" "$APP_PATH"
  else
    echo "  (macOS App übersprungen: weder dist/OmaThunder.app noch Go gefunden)"
  fi
else
  PREBUILT_UI="$ROOT_DIR/dist/omathunder-ui-${OS}-${ARCH}"
  if [[ -x "$PREBUILT_UI" ]]; then
    echo "==> Installiere App: $PREBUILT_UI -> $UI_PATH"
    install_bin "$PREBUILT_UI" "$UI_PATH"
  elif command -v go >/dev/null 2>&1; then
    echo "==> Baue omathunder-ui ($VERSION) lokal nach $UI_PATH..."
    rm -f "$UI_PATH"
    (cd "$ROOT_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o "$UI_PATH" ./cmd/omathunder-ui)
  else
    echo "  (App übersprungen: weder $PREBUILT_UI noch Go gefunden)"
  fi
fi

echo "==> Registriere Thunderbird Native Messaging Host (ohne Profil-Eingriff)..."
"$BIN_PATH" setup

echo ""
echo "Fertig! Nächste Schritte:"
echo "  1. Stelle sicher, dass '$PREFIX' in deinem PATH liegt."
echo "  2. Installiere das Add-on in Thunderbird:"
echo "     Add-ons & Themes -> Zahnrad -> Add-on aus Datei installieren -> $ROOT_DIR/dist/omathunder-extension.xpi"
if [[ "$OS" == "darwin" && -d "$APP_PATH" ]] || [[ -x "$UI_PATH" ]]; then
  echo "  3. Optional: native App starten mit 'omathunder ui'. Sie braucht die lokale Bridge,"
  echo "     die standardmäßig aus ist – einschalten in der App, in den Add-on-Einstellungen"
  echo "     oder mit 'omathunder bridge enable'."
  echo "  4. Zum rückstandsfreien Entfernen jederzeit: ./uninstall.sh"
else
  echo "  3. Zum rückstandsfreien Entfernen jederzeit: ./uninstall.sh"
fi
