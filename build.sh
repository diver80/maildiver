#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="$ROOT_DIR/dist"
VERSION="${VERSION:-$(cat "$ROOT_DIR/VERSION")}"
EXT_VERSION="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$ROOT_DIR/extension/manifest.json" | head -1)"
if [[ "$EXT_VERSION" != "${VERSION%%-*}" ]]; then
  echo "Version mismatch: VERSION=$VERSION, extension/manifest.json=$EXT_VERSION" >&2
  exit 1
fi

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

echo "==> Running Go unit tests..."
(cd "$ROOT_DIR" && go test ./...)

echo "==> Running extension unit tests..."
JSC="/System/Library/Frameworks/JavaScriptCore.framework/Versions/A/Helpers/jsc"
if command -v node >/dev/null 2>&1; then
  (cd "$ROOT_DIR" && node tests/core.test.js)
elif [[ -x "$JSC" ]]; then
  (cd "$ROOT_DIR" && "$JSC" tests/core.test.js)
else
  echo "  (skipped: neither node nor jsc found)"
fi

TARGETS=(
  "darwin/arm64"
  "darwin/amd64"
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "windows/arm64"
)

# Two programs: the small Native Messaging host + CLI (omathunder) and the native desktop app
# (omathunder-ui; on macOS additionally bundled as OmaThunder.app). The host has no GUI dependencies.
echo "==> Cross-compiling omathunder (host/CLI) and omathunder-ui (app) binaries ($VERSION)..."
for target in "${TARGETS[@]}"; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  EXE=""
  if [[ "$GOOS" == "windows" ]]; then
    EXE=".exe"
  fi
  for prog in omathunder omathunder-ui; do
    OUT_NAME="${prog}-${GOOS}-${GOARCH}${EXE}"
    echo "  - building $OUT_NAME"
    (cd "$ROOT_DIR" && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
      go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
      -o "$DIST_DIR/$OUT_NAME" "./cmd/${prog}")
  done
done

echo "==> Packaging Thunderbird WebExtension (.xpi)..."
XPI_PATH="$DIST_DIR/omathunder-extension.xpi"
(cd "$ROOT_DIR/extension" && zip -q -r "$XPI_PATH" . -x "*.DS_Store")

if [[ "$(uname -s)" == "Darwin" ]]; then
  HOST_ARCH="$(uname -m)"
  case "$HOST_ARCH" in
    x86_64|amd64) HOST_ARCH="amd64" ;;
    arm64|aarch64) HOST_ARCH="arm64" ;;
  esac
  echo "==> Building macOS .app bundle (darwin/${HOST_ARCH})..."
  # MYGO_LDFLAGS is appended after mygo's own -ldflags, so the app reports the real version.
  (cd "$ROOT_DIR" && MYGO_LDFLAGS="-X main.version=${VERSION}" \
    go run github.com/egoist/mygo/cmd/mygo@v0.7.4 build -platform "darwin/${HOST_ARCH}" -skip-dmg -o "$DIST_DIR")
  rm -rf "$DIST_DIR/OmaThunder.app"
  cp -R "$DIST_DIR/darwin-${HOST_ARCH}/OmaThunder.app" "$DIST_DIR/OmaThunder.app"
  rm -rf "$DIST_DIR/darwin-${HOST_ARCH}"
  APP_VERSION="$("$DIST_DIR/OmaThunder.app/Contents/MacOS/OmaThunder" --version 2>/dev/null || true)"
  if [[ "$APP_VERSION" != "omathunder-ui ${VERSION}" ]]; then
    echo "OmaThunder.app reports '$APP_VERSION', expected 'omathunder-ui ${VERSION}'" >&2
    exit 1
  fi
  # Release asset: a zipped bundle keeps symlinks, permissions and extended attributes intact.
  (cd "$DIST_DIR" && ditto -c -k --keepParent OmaThunder.app "OmaThunder-darwin-${HOST_ARCH}.zip")
fi

echo "==> Writing SHA256 checksums..."
shopt -s nullglob
ASSETS=()
for f in "$DIST_DIR"/omathunder-* "$DIST_DIR"/OmaThunder-*.zip; do
  ASSETS+=("$(basename "$f")")
done
shopt -u nullglob
if command -v shasum >/dev/null 2>&1; then
  (cd "$DIST_DIR" && shasum -a 256 "${ASSETS[@]}" > SHA256SUMS.txt)
elif command -v sha256sum >/dev/null 2>&1; then
  (cd "$DIST_DIR" && sha256sum "${ASSETS[@]}" > SHA256SUMS.txt)
fi

echo "==> Build complete:"
ls -lh "$DIST_DIR"
