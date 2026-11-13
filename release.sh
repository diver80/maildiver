#!/usr/bin/env bash
# Publishes the version in ./VERSION: builds and tests, tags, pushes, creates the GitHub release
# with binaries and the .xpi, and prints the url/sha256 for the Homebrew formula
# (diver80/homebrew-tap, Formula/omathunder.rb).
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"
VERSION="$(cat VERSION)"
TAG="v$VERSION"
REPO="diver80/omathunder"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "Working tree not clean – commit first." >&2
  exit 1
fi
if [[ "$(git rev-parse --abbrev-ref HEAD)" != "main" ]]; then
  echo "Releases are cut from main." >&2
  exit 1
fi
if git rev-parse "$TAG" >/dev/null 2>&1; then
  echo "Tag $TAG exists already – bump VERSION and extension/manifest.json." >&2
  exit 1
fi

VERSION="$VERSION" ./build.sh

git tag -a "$TAG" -m "OmaThunder $VERSION"
git push origin main "$TAG"

gh release create "$TAG" dist/* --repo "$REPO" --title "OmaThunder $VERSION" --generate-notes

URL="https://github.com/$REPO/archive/refs/tags/$TAG.tar.gz"
SHA="$(curl -fsSL "$URL" | shasum -a 256 | cut -d' ' -f1)"
echo
echo "Homebrew formula values:"
echo "  url \"$URL\""
echo "  sha256 \"$SHA\""
