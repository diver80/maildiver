#!/usr/bin/env bash
set -euo pipefail

# Standalone installer for MailDiver (macOS & Linux).

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="${MAILDIVER_PREFIX:-$HOME/.local/bin}"
BIN_PATH="$PREFIX/maildiver"

mkdir -p "$PREFIX"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Nicht unterstützte Architektur: $ARCH" >&2; exit 1 ;;
esac

APP_DIR="${MAILDIVER_APP_DIR:-$HOME/Applications}"
APP_PATH="$APP_DIR/MailDiver.app"
VERSION="$(cat "$ROOT_DIR/VERSION" 2>/dev/null || echo "dev")"

install_bin() {
  rm -f "$2"
  cp "$1" "$2"
  chmod +x "$2"
}

if [[ "$OS" == "darwin" ]]; then
  PREBUILT_APP=""
  if [[ -d "$ROOT_DIR/dist/MailDiver.app" ]]; then
    PREBUILT_APP="$ROOT_DIR/dist/MailDiver.app"
  elif command -v go >/dev/null 2>&1; then
    echo "==> Baue macOS App-Bundle (darwin/${ARCH})..."
    (cd "$ROOT_DIR" && MYGO_LDFLAGS="-X main.version=${VERSION}" \
      go run github.com/egoist/mygo/cmd/mygo@v0.10.0 build -platform "darwin/${ARCH}" -skip-dmg -o "$ROOT_DIR/dist")
    if [[ -d "$ROOT_DIR/dist/darwin-${ARCH}/MailDiver.app" ]]; then
      PREBUILT_APP="$ROOT_DIR/dist/darwin-${ARCH}/MailDiver.app"
    fi
  fi
  if [[ -n "$PREBUILT_APP" ]]; then
    echo "==> Installiere macOS App: $PREBUILT_APP -> $APP_PATH"
    mkdir -p "$APP_DIR"
    rm -rf "$APP_PATH"
    cp -R "$PREBUILT_APP" "$APP_PATH"
  else
    echo "Weder dist/MailDiver.app noch Go gefunden." >&2
    exit 1
  fi
else
  PREBUILT="$ROOT_DIR/dist/maildiver-${OS}-${ARCH}"
  if [[ -x "$PREBUILT" ]]; then
    echo "==> Installiere Binary: $PREBUILT -> $BIN_PATH"
    install_bin "$PREBUILT" "$BIN_PATH"
  elif command -v go >/dev/null 2>&1; then
    echo "==> Baue maildiver ($VERSION) lokal nach $BIN_PATH..."
    rm -f "$BIN_PATH"
    (cd "$ROOT_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o "$BIN_PATH" ./cmd/maildiver)
  else
    echo "Weder vorab gebautes Binary ($PREBUILT) noch Go gefunden." >&2
    exit 1
  fi
fi

echo ""
echo "Fertig! Starte MailDiver:"
if [[ "$OS" == "darwin" ]]; then
  echo "  open \"$APP_PATH\""
else
  echo "  $BIN_PATH"
fi
echo "Zum Entfernen jederzeit: ./uninstall.sh"
