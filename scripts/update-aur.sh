#!/usr/bin/env bash
# Regenerates packaging/aur/maildiver-bin/{PKGBUILD,.SRCINFO} from VERSION and dist/SHA256SUMS.txt.
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${1:-$(cat VERSION)}"
AUR_DIR="$ROOT_DIR/packaging/aur/maildiver-bin"
SUMS_FILE="$ROOT_DIR/dist/SHA256SUMS.txt"

if [[ ! -f "$SUMS_FILE" ]]; then
  echo "Missing $SUMS_FILE – run ./build.sh or ./release.sh first." >&2
  exit 1
fi

sha_of() { shasum -a 256 "$1" | awk '{print $1}'; }
dist_sha() { awk -v f="$1" '$2 == f {print $1}' "$SUMS_FILE"; }

SHA_AMD64="$(dist_sha maildiver-linux-amd64)"
SHA_ARM64="$(dist_sha maildiver-linux-arm64)"
SHA_DESKTOP="$(sha_of "$AUR_DIR/maildiver.desktop")"
SHA_ICON="$(sha_of "$ROOT_DIR/resources/icon.png")"
SHA_LICENSE="$(sha_of "$ROOT_DIR/LICENSE")"
SHA_WIDGET="$(sha_of "$ROOT_DIR/omarchy/BarWidget.qml")"

if [[ -z "$SHA_AMD64" || -z "$SHA_ARM64" ]]; then
  echo "Could not find Linux binary checksums in $SUMS_FILE" >&2
  exit 1
fi

cat > "$AUR_DIR/PKGBUILD" <<EOF
# Maintainer: Frank Hess <https://github.com/diver80>
pkgname=maildiver-bin
pkgver=${VERSION}
pkgrel=1
pkgdesc="Native Go + MyGo IMAP/SMTP email, calendar & contacts client with optional local/cloud AI and live Omarchy theme sync"
arch=('x86_64' 'aarch64')
url="https://diver80.github.io/maildiver/"
license=('MIT')
provides=('maildiver')
conflicts=('maildiver' 'maildiver-git')
depends=('gtk3' 'libsecret' 'libnotify')
optdepends=(
  'webkit2gtk-4.1: rich HTML compose editor and inline split-panel browser preview'
  'libayatana-appindicator: system tray status widget'
  'ollama: local private AI assistant'
)
options=('!strip' '!debug')

source=(
  "maildiver.desktop"
  "maildiver-icon-\${pkgver}.png::https://raw.githubusercontent.com/diver80/maildiver/v\${pkgver}/resources/icon.png"
  "maildiver-LICENSE-\${pkgver}::https://raw.githubusercontent.com/diver80/maildiver/v\${pkgver}/LICENSE"
  "maildiver-BarWidget-\${pkgver}.qml::https://raw.githubusercontent.com/diver80/maildiver/v\${pkgver}/omarchy/BarWidget.qml"
)
source_x86_64=(
  "maildiver-\${pkgver}-x86_64::https://github.com/diver80/maildiver/releases/download/v\${pkgver}/maildiver-linux-amd64"
)
source_aarch64=(
  "maildiver-\${pkgver}-aarch64::https://github.com/diver80/maildiver/releases/download/v\${pkgver}/maildiver-linux-arm64"
)

sha256sums=(
  '${SHA_DESKTOP}'
  '${SHA_ICON}'
  '${SHA_LICENSE}'
  '${SHA_WIDGET}'
)
sha256sums_x86_64=(
  '${SHA_AMD64}'
)
sha256sums_aarch64=(
  '${SHA_ARM64}'
)

package() {
  install -Dm755 "\${srcdir}/maildiver-\${pkgver}-\${CARCH}" "\${pkgdir}/usr/bin/maildiver"
  install -Dm644 "\${srcdir}/maildiver.desktop" "\${pkgdir}/usr/share/applications/maildiver.desktop"
  install -Dm644 "\${srcdir}/maildiver-icon-\${pkgver}.png" "\${pkgdir}/usr/share/icons/hicolor/256x256/apps/maildiver.png"
  install -Dm644 "\${srcdir}/maildiver-BarWidget-\${pkgver}.qml" "\${pkgdir}/usr/share/maildiver/omarchy/BarWidget.qml"
  install -Dm644 "\${srcdir}/maildiver-LICENSE-\${pkgver}" "\${pkgdir}/usr/share/licenses/\${pkgname}/LICENSE"
}
EOF

cat > "$AUR_DIR/.SRCINFO" <<EOF
pkgbase = maildiver-bin
	pkgdesc = Native Go + MyGo IMAP/SMTP email, calendar & contacts client with optional local/cloud AI and live Omarchy theme sync
	pkgver = ${VERSION}
	pkgrel = 1
	url = https://diver80.github.io/maildiver/
	arch = x86_64
	arch = aarch64
	license = MIT
	depends = gtk3
	depends = libsecret
	depends = libnotify
	optdepends = webkit2gtk-4.1: rich HTML compose editor and inline split-panel browser preview
	optdepends = libayatana-appindicator: system tray status widget
	optdepends = ollama: local private AI assistant
	provides = maildiver
	conflicts = maildiver
	conflicts = maildiver-git
	options = !strip
	options = !debug
	source = maildiver.desktop
	source = maildiver-icon-${VERSION}.png::https://raw.githubusercontent.com/diver80/maildiver/v${VERSION}/resources/icon.png
	source = maildiver-LICENSE-${VERSION}::https://raw.githubusercontent.com/diver80/maildiver/v${VERSION}/LICENSE
	source = maildiver-BarWidget-${VERSION}.qml::https://raw.githubusercontent.com/diver80/maildiver/v${VERSION}/omarchy/BarWidget.qml
	sha256sums = ${SHA_DESKTOP}
	sha256sums = ${SHA_ICON}
	sha256sums = ${SHA_LICENSE}
	sha256sums = ${SHA_WIDGET}
	source_x86_64 = maildiver-${VERSION}-x86_64::https://github.com/diver80/maildiver/releases/download/v${VERSION}/maildiver-linux-amd64
	sha256sums_x86_64 = ${SHA_AMD64}
	source_aarch64 = maildiver-${VERSION}-aarch64::https://github.com/diver80/maildiver/releases/download/v${VERSION}/maildiver-linux-arm64
	sha256sums_aarch64 = ${SHA_ARM64}

pkgname = maildiver-bin
EOF

echo "Updated $AUR_DIR/PKGBUILD and .SRCINFO for v${VERSION}"
