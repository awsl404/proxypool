#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
DIST="$ROOT/dist"
CACHE="$ROOT/.cache/go-build"
MAIN="$ROOT/cmd/proxypool"
mkdir -p "$DIST"
mkdir -p "$CACHE"

build_target() {
  goos="$1"
  goarch="$2"
  target="$goos-$goarch"
  package_dir="$DIST/proxypool-$target"
  bin_name="proxypool"
  archive="$DIST/proxypool-$target.tar.gz"
  if [ "$goos" = "windows" ]; then
    bin_name="proxypool.exe"
    archive="$DIST/proxypool-$target.zip"
  fi

  rm -rf "$package_dir"
  mkdir -p "$package_dir/configs"

  echo "Building $target -> $package_dir/$bin_name"
  GOCACHE="$CACHE" CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -buildvcs=false -ldflags "-s -w" -o "$package_dir/$bin_name" "$MAIN"

  cp "$ROOT/configs/config.example.json" "$package_dir/configs/config.example.json"
  cp "$ROOT/README.md" "$package_dir/README.md"

  rm -f "$archive"
  echo "Packing $target -> $archive"
  if [ "$goos" = "windows" ]; then
    (cd "$DIST" && zip -qr "$(basename "$archive")" "proxypool-$target")
  else
    tar -czf "$archive" -C "$DIST" "proxypool-$target"
  fi
}

build_target windows amd64
build_target windows arm64
build_target linux amd64
build_target linux arm64
build_target darwin amd64
build_target darwin arm64

checksum_file="$DIST/SHA256SUMS.txt"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$DIST" && sha256sum proxypool-*.zip proxypool-*.tar.gz) > "$checksum_file"
elif command -v shasum >/dev/null 2>&1; then
  (cd "$DIST" && shasum -a 256 proxypool-*.zip proxypool-*.tar.gz) > "$checksum_file"
else
  echo "sha256sum or shasum is required to write checksums" >&2
  exit 1
fi
echo "Checksums -> $checksum_file"
