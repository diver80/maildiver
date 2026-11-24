#!/usr/bin/env bash
# Publishes the version in ./VERSION: builds and tests, tags, pushes, creates the GitHub release
# with the binaries and app packages, and prints the url/sha256 for the Homebrew formula
# (diver80/homebrew-tap: Formula/postdive.rb takes the binaries, Casks/postdive.rb the .dmg).
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"
VERSION="$(cat VERSION)"
TAG="v$VERSION"
REPO="diver80/postdive"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "Working tree not clean – commit first." >&2
  exit 1
fi
if [[ "$(git rev-parse --abbrev-ref HEAD)" != "main" ]]; then
  echo "Releases are cut from main." >&2
  exit 1
fi
if git rev-parse "$TAG" >/dev/null 2>&1; then
  echo "Tag $TAG exists already – bump VERSION, mygo.json and manifest.json." >&2
  exit 1
fi

# The Android app comes along when the NDK is there (build.sh finds it in the SDK).
# --universal: the macOS app for Intel and Apple silicon, with the .dmg the Homebrew cask takes.
BUILD_FLAGS=(--universal)
if [[ -n "${ANDROID_NDK_HOME:-}" || -d "${ANDROID_HOME:-$HOME/Library/Android/sdk}/ndk" || -d "$HOME/Android/Sdk/ndk" ]]; then
  BUILD_FLAGS+=(--android)
fi
VERSION="$VERSION" ./build.sh ${BUILD_FLAGS[@]+"${BUILD_FLAGS[@]}"}

git tag -a "$TAG" -m "PostDive $VERSION"
git push origin main "$TAG"

# Upload files only: dist/ also holds the unpacked PostDive.app directory used by install.sh.
shopt -s nullglob
ASSETS=(dist/postdive-* dist/PostDive-*.zip dist/android/*.apk dist/android/*.aab dist/android/*/*.apk dist/android/*/*.aab dist/packages/*/* dist/SHA256SUMS.txt)
shopt -u nullglob
gh release create "$TAG" "${ASSETS[@]}" --repo "$REPO" --title "PostDive $VERSION" --generate-notes

URL="https://github.com/$REPO/archive/refs/tags/$TAG.tar.gz"
SHA="$(curl -fsSL "$URL" | shasum -a 256 | cut -d' ' -f1)"
echo
echo "Homebrew formula values:"
echo "  url \"$URL\""
echo "  sha256 \"$SHA\""
echo
echo "AUR update:"
echo "  ./scripts/update-aur.sh $VERSION"
