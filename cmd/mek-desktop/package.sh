#!/bin/sh
# Builds mek-desktop for this machine (the frontend, then the app with cgo)
# and packages it with the mek CLI it runs, into dist/desktop:
#   macOS  mek-desktop_<version>_darwin_<arch>.zip   — mek.app (unsigned)
#   Linux  mek-desktop_<version>_linux_<arch>.tar.gz — needs GTK 3 and WebKitGTK 4.1
# Usage: package.sh <version> [arch]   (arch: amd64 or arm64; default: this machine's)
set -eu

version=${1:?version, e.g. v0.21.0}
arch=${2:-$(go env GOARCH)}
os=$(go env GOOS)
root=$(cd "$(dirname "$0")/../.." && pwd)
out="$root/dist/desktop"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$out"

(cd "$root/cmd/mek-desktop/frontend" && npm ci --no-fund --no-audit && npm run build)

ldflags="-s -w -X github.com/prateep-r/mek/internal/version.Version=$version"
export CGO_ENABLED=1 GOOS="$os" GOARCH="$arch"
if [ "$os" = darwin ]; then
  # Go needs macOS 13; the cgo parts are built for the same.
  export MACOSX_DEPLOYMENT_TARGET=13.0 CGO_CFLAGS="-mmacosx-version-min=13.0" CGO_LDFLAGS="-mmacosx-version-min=13.0"
  [ "$arch" = amd64 ] && export CC="clang -arch x86_64"
  [ "$arch" = arm64 ] && export CC="clang -arch arm64"
fi
go build -C "$root" -trimpath -tags desktop -ldflags "$ldflags" -o "$work/mek-desktop" ./cmd/mek-desktop
CGO_ENABLED=0 go build -C "$root" -trimpath -ldflags "$ldflags" -o "$work/mek" ./cmd/mek

name="mek-desktop_${version#v}_${os}_${arch}"
if [ "$os" = darwin ]; then
  app="$work/mek.app/Contents"
  mkdir -p "$app/MacOS" "$app/Resources"
  mv "$work/mek-desktop" "$work/mek" "$app/MacOS/"
  iconset="$work/mek.iconset"
  mkdir "$iconset"
  for s in 16 32 128 256 512; do
    sips -z $s $s "$root/assets/logo/mek-icon-512.png" --out "$iconset/icon_${s}x${s}.png" >/dev/null
    d=$((s * 2)); [ $d -le 512 ] && sips -z $d $d "$root/assets/logo/mek-icon-512.png" --out "$iconset/icon_${s}x${s}@2x.png" >/dev/null
  done
  iconutil -c icns "$iconset" -o "$app/Resources/mek.icns"
  cat > "$app/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>mek</string>
  <key>CFBundleDisplayName</key><string>mek</string>
  <key>CFBundleIdentifier</key><string>io.github.prateep-r.mek</string>
  <key>CFBundleExecutable</key><string>mek-desktop</string>
  <key>CFBundleIconFile</key><string>mek</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${version#v}</string>
  <key>CFBundleVersion</key><string>${version#v}</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
  (cd "$work" && ditto -c -k --keepParent mek.app "$out/$name.zip")
  echo "$out/$name.zip"
else
  tar -C "$work" -czf "$out/$name.tar.gz" mek-desktop mek
  echo "$out/$name.tar.gz"
fi
