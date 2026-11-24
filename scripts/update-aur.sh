#!/usr/bin/env bash
# Regenerates packaging/aur/postdive-bin/{PKGBUILD,.SRCINFO} from VERSION and dist/SHA256SUMS.txt.
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${1:-$(cat VERSION)}"
AUR_DIR="$ROOT_DIR/packaging/aur/postdive-bin"
SUMS_FILE="$ROOT_DIR/dist/SHA256SUMS.txt"

if [[ ! -f "$SUMS_FILE" ]]; then
  echo "Missing $SUMS_FILE – run ./build.sh or ./release.sh first." >&2
  exit 1
fi

sha_of() { shasum -a 256 "$1" | awk '{print $1}'; }
dist_sha() { awk -v f="$1" '$2 == f {print $1}' "$SUMS_FILE"; }

SHA_AMD64="$(dist_sha postdive-linux-amd64)"
SHA_ARM64="$(dist_sha postdive-linux-arm64)"
SHA_DESKTOP="$(sha_of "$AUR_DIR/postdive.desktop")"
SHA_ICON="$(sha_of "$ROOT_DIR/resources/icon.png")"
SHA_LICENSE="$(sha_of "$ROOT_DIR/LICENSE")"
SHA_WIDGET="$(sha_of "$ROOT_DIR/omarchy/BarWidget.qml")"

if [[ -z "$SHA_AMD64" || -z "$SHA_ARM64" ]]; then
  echo "Could not find Linux binary checksums in $SUMS_FILE" >&2
  exit 1
fi

cat > "$AUR_DIR/PKGBUILD" <<EOF
# Maintainer: Frank Hess <https://github.com/diver80>
pkgname=postdive-bin
pkgver=${VERSION}
pkgrel=1
pkgdesc="Native Go + MyGo IMAP/SMTP email, calendar & contacts client with optional local/cloud AI and live Omarchy theme sync"
arch=('x86_64' 'aarch64')
url="https://diver80.github.io/postdive/"
license=('MIT')
provides=('postdive')
conflicts=('postdive' 'postdive-git')
depends=('gtk3' 'libsecret' 'libnotify')
optdepends=(
  'webkit2gtk-4.1: rich HTML compose editor and inline split-panel browser preview'
  'libayatana-appindicator: system tray status widget'
  'ollama: local private AI assistant'
)
options=('!strip' '!debug')

source=(
  "postdive.desktop"
  "postdive-icon-\${pkgver}.png::https://raw.githubusercontent.com/diver80/postdive/v\${pkgver}/resources/icon.png"
  "postdive-LICENSE-\${pkgver}::https://raw.githubusercontent.com/diver80/postdive/v\${pkgver}/LICENSE"
  "postdive-BarWidget-\${pkgver}.qml::https://raw.githubusercontent.com/diver80/postdive/v\${pkgver}/omarchy/BarWidget.qml"
)
source_x86_64=(
  "postdive-\${pkgver}-x86_64::https://github.com/diver80/postdive/releases/download/v\${pkgver}/postdive-linux-amd64"
)
source_aarch64=(
  "postdive-\${pkgver}-aarch64::https://github.com/diver80/postdive/releases/download/v\${pkgver}/postdive-linux-arm64"
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
  install -Dm755 "\${srcdir}/postdive-\${pkgver}-\${CARCH}" "\${pkgdir}/usr/bin/postdive"
  install -Dm644 "\${srcdir}/postdive.desktop" "\${pkgdir}/usr/share/applications/postdive.desktop"
  install -Dm644 "\${srcdir}/postdive-icon-\${pkgver}.png" "\${pkgdir}/usr/share/icons/hicolor/256x256/apps/postdive.png"
  install -Dm644 "\${srcdir}/postdive-BarWidget-\${pkgver}.qml" "\${pkgdir}/usr/share/postdive/omarchy/BarWidget.qml"
  install -Dm644 "\${srcdir}/postdive-LICENSE-\${pkgver}" "\${pkgdir}/usr/share/licenses/\${pkgname}/LICENSE"
}
EOF

cat > "$AUR_DIR/.SRCINFO" <<EOF
pkgbase = postdive-bin
	pkgdesc = Native Go + MyGo IMAP/SMTP email, calendar & contacts client with optional local/cloud AI and live Omarchy theme sync
	pkgver = ${VERSION}
	pkgrel = 1
	url = https://diver80.github.io/postdive/
	arch = x86_64
	arch = aarch64
	license = MIT
	depends = gtk3
	depends = libsecret
	depends = libnotify
	optdepends = webkit2gtk-4.1: rich HTML compose editor and inline split-panel browser preview
	optdepends = libayatana-appindicator: system tray status widget
	optdepends = ollama: local private AI assistant
	provides = postdive
	conflicts = postdive
	conflicts = postdive-git
	options = !strip
	options = !debug
	source = postdive.desktop
	source = postdive-icon-${VERSION}.png::https://raw.githubusercontent.com/diver80/postdive/v${VERSION}/resources/icon.png
	source = postdive-LICENSE-${VERSION}::https://raw.githubusercontent.com/diver80/postdive/v${VERSION}/LICENSE
	source = postdive-BarWidget-${VERSION}.qml::https://raw.githubusercontent.com/diver80/postdive/v${VERSION}/omarchy/BarWidget.qml
	sha256sums = ${SHA_DESKTOP}
	sha256sums = ${SHA_ICON}
	sha256sums = ${SHA_LICENSE}
	sha256sums = ${SHA_WIDGET}
	source_x86_64 = postdive-${VERSION}-x86_64::https://github.com/diver80/postdive/releases/download/v${VERSION}/postdive-linux-amd64
	sha256sums_x86_64 = ${SHA_AMD64}
	source_aarch64 = postdive-${VERSION}-aarch64::https://github.com/diver80/postdive/releases/download/v${VERSION}/postdive-linux-arm64
	sha256sums_aarch64 = ${SHA_ARM64}

pkgname = postdive-bin
EOF

echo "Updated $AUR_DIR/PKGBUILD and .SRCINFO for v${VERSION}"
