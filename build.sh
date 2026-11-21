#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="$ROOT_DIR/dist"
VERSION="${VERSION:-$(cat "$ROOT_DIR/VERSION")}"
MYGO_VERSION="v0.10.0"

BUILD_PACKAGES=false
BUILD_UNIVERSAL=false
BUILD_ANDROID=false
SKIP_TESTS=false

usage() {
  cat <<EOF
MailDiver Multi-Platform Build Script (mygo ${MYGO_VERSION})

Usage: ./build.sh [options]
  --packages     Build platform installers/packages via mygo build
                 (macOS .dmg, Linux .tar.gz/.deb/.rpm/PKGBUILD, Windows .exe)
  --universal    Build macOS Universal binary (darwin/universal .app + .dmg)
  --android      Build Android APK/AAB (android/arm64) via mygo build (requires Android NDK)
  --skip-tests   Skip 'go test ./...'
  -h, --help     Show this help
EOF
}

for arg in "$@"; do
  case "$arg" in
    --packages)   BUILD_PACKAGES=true ;;
    --universal)  BUILD_UNIVERSAL=true ;;
    --android)    BUILD_ANDROID=true ;;
    --skip-tests) SKIP_TESTS=true ;;
    -h|--help)    usage; exit 0 ;;
    *)
      echo "Unknown option: $arg" >&2
      usage >&2
      exit 1
      ;;
  esac
done

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

if [[ "$SKIP_TESTS" != "true" ]]; then
  echo "==> Running Go unit tests..."
  (cd "$ROOT_DIR" && go test ./...)
fi

TARGETS=(
  "darwin/arm64"
  "darwin/amd64"
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "windows/arm64"
)

echo "==> Cross-compiling MailDiver ($VERSION) for Desktop platforms..."
for target in "${TARGETS[@]}"; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  EXE=""
  if [[ "$GOOS" == "windows" ]]; then
    EXE=".exe"
  fi
  OUT_NAME="maildiver-${GOOS}-${GOARCH}${EXE}"
  echo "  - building $OUT_NAME"
  (cd "$ROOT_DIR" && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o "$DIST_DIR/$OUT_NAME" ./cmd/maildiver)
done

if [[ "$(uname -s)" == "Darwin" ]]; then
  HOST_ARCH="$(uname -m)"
  case "$HOST_ARCH" in
    x86_64|amd64) HOST_ARCH="amd64" ;;
    arm64|aarch64) HOST_ARCH="arm64" ;;
  esac

  MAC_PLATFORM="darwin/${HOST_ARCH}"
  MAC_OUT_SUB="darwin-${HOST_ARCH}"
  DMG_FLAG="-skip-dmg"
  if [[ "$BUILD_UNIVERSAL" == "true" ]]; then
    MAC_PLATFORM="darwin/universal"
    MAC_OUT_SUB="darwin-universal"
    DMG_FLAG=""
  elif [[ "$BUILD_PACKAGES" == "true" ]]; then
    DMG_FLAG=""
  fi

  echo "==> Building macOS .app bundle (${MAC_PLATFORM}) via mygo@${MYGO_VERSION}..."
  (cd "$ROOT_DIR" && MYGO_LDFLAGS="-X main.version=${VERSION}" \
    go run "github.com/egoist/mygo/cmd/mygo@${MYGO_VERSION}" build \
      -platform "${MAC_PLATFORM}" ${DMG_FLAG} -o "$DIST_DIR")

  rm -rf "$DIST_DIR/MailDiver.app"
  cp -R "$DIST_DIR/${MAC_OUT_SUB}/MailDiver.app" "$DIST_DIR/MailDiver.app"
  if compgen -G "$DIST_DIR/${MAC_OUT_SUB}/*.dmg" >/dev/null 2>&1; then
    mv "$DIST_DIR/${MAC_OUT_SUB}"/*.dmg "$DIST_DIR/"
  fi
  rm -rf "$DIST_DIR/${MAC_OUT_SUB}"

  APP_VERSION="$("$DIST_DIR/MailDiver.app/Contents/MacOS/MailDiver" --version 2>/dev/null || true)"
  if [[ "$APP_VERSION" != "maildiver ${VERSION}" ]]; then
    echo "MailDiver.app reports '$APP_VERSION', expected 'maildiver ${VERSION}'" >&2
    exit 1
  fi
  # Release asset: a zipped bundle keeps symlinks, permissions and extended attributes intact.
  (cd "$DIST_DIR" && ditto -c -k --keepParent MailDiver.app "MailDiver-${MAC_OUT_SUB}.zip")
fi

if [[ "$BUILD_PACKAGES" == "true" ]]; then
  echo "==> Building Linux (.tar.gz/.deb/.rpm/PKGBUILD) & Windows packages via mygo@${MYGO_VERSION}..."
  (cd "$ROOT_DIR" && MYGO_LDFLAGS="-X main.version=${VERSION}" \
    go run "github.com/egoist/mygo/cmd/mygo@${MYGO_VERSION}" build \
      -platform "linux/amd64,linux/arm64,windows/amd64,windows/arm64" -skip-installer -o "$DIST_DIR/packages")
fi

if [[ "$BUILD_ANDROID" == "true" ]]; then
  echo "==> Checking Android toolchain (mygo@${MYGO_VERSION} doctor)..."
  DEFAULT_ANDROID_SDK="$HOME/Android/Sdk"
  if [[ "$(uname -s)" == "Darwin" ]]; then
    DEFAULT_ANDROID_SDK="$HOME/Library/Android/sdk"
  fi
  ANDROID_SDK="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$DEFAULT_ANDROID_SDK}}"
  NDK_DIR="${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-}}"
  if [[ -z "$NDK_DIR" && -d "$ANDROID_SDK/ndk" ]]; then
    NDK_DIR="$(ls -1d "$ANDROID_SDK"/ndk/* 2>/dev/null | sort -V | tail -n 1 || true)"
  fi
  if [[ -z "$NDK_DIR" || ! -d "$NDK_DIR" ]]; then
    echo "ERROR: Android NDK not found (SDK: ${ANDROID_SDK})." >&2
    echo "Install it via Android Studio -> SDK Manager -> SDK Tools -> NDK (Side by side)," >&2
    echo "or via: sdkmanager 'ndk;27.2.12479018' and export ANDROID_NDK_HOME." >&2
    exit 1
  fi
  echo "==> Building Android (${ANDROID_PLATFORMS:-android/arm64}) via mygo@${MYGO_VERSION}..."
  (cd "$ROOT_DIR" && ANDROID_NDK_HOME="$NDK_DIR" MYGO_LDFLAGS="-X main.version=${VERSION}" \
    go run "github.com/egoist/mygo/cmd/mygo@${MYGO_VERSION}" build \
      -platform "${ANDROID_PLATFORMS:-android/arm64}" -o "$DIST_DIR/android")
fi

echo "==> Writing SHA256 checksums..."
shopt -s nullglob
ASSETS=()
for f in "$DIST_DIR"/maildiver-* "$DIST_DIR"/MailDiver-*.zip; do
  [[ -f "$f" ]] && ASSETS+=("$(basename "$f")")
done
for sub in packages android; do
  if [[ -d "$DIST_DIR/$sub" ]]; then
    while IFS= read -r -d '' f; do
      ASSETS+=("${f#"$DIST_DIR"/}")
    done < <(find "$DIST_DIR/$sub" -type f -print0 | sort -z)
  fi
done
shopt -u nullglob
if [[ ${#ASSETS[@]} -gt 0 ]]; then
  if command -v shasum >/dev/null 2>&1; then
    (cd "$DIST_DIR" && shasum -a 256 "${ASSETS[@]}" > SHA256SUMS.txt)
  elif command -v sha256sum >/dev/null 2>&1; then
    (cd "$DIST_DIR" && sha256sum "${ASSETS[@]}" > SHA256SUMS.txt)
  fi
fi

echo "==> Build complete:"
ls -lh "$DIST_DIR"
