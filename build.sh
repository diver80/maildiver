#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="$ROOT_DIR/dist"
VERSION="${VERSION:-0.1.0-dev}"

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

echo "==> Cross-compiling omathunder binaries ($VERSION)..."
for target in "${TARGETS[@]}"; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  OUT_NAME="omathunder-${GOOS}-${GOARCH}"
  if [[ "$GOOS" == "windows" ]]; then
    OUT_NAME="${OUT_NAME}.exe"
  fi
  echo "  - building $OUT_NAME"
  (cd "$ROOT_DIR" && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o "$DIST_DIR/$OUT_NAME" ./cmd/omathunder)
done

echo "==> Packaging Thunderbird WebExtension (.xpi)..."
XPI_PATH="$DIST_DIR/omathunder-extension.xpi"
(cd "$ROOT_DIR/extension" && zip -q -r "$XPI_PATH" . -x "*.DS_Store")

echo "==> Writing SHA256 checksums..."
if command -v shasum >/dev/null 2>&1; then
  (cd "$DIST_DIR" && shasum -a 256 omathunder-* > SHA256SUMS.txt)
elif command -v sha256sum >/dev/null 2>&1; then
  (cd "$DIST_DIR" && sha256sum omathunder-* > SHA256SUMS.txt)
fi

echo "==> Build complete:"
ls -lh "$DIST_DIR"
