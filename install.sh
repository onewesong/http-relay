#!/usr/bin/env sh
set -eu

REPO="${HTTP_RELAY_REPO:-onewesong/http-relay}"
BINARY="${HTTP_RELAY_BINARY:-http-relay}"
VERSION="${VERSION:-latest}"
BINDIR="${BINDIR:-${HTTP_RELAY_INSTALL_DIR:-/usr/local/bin}}"
API_BASE="${GITHUB_API_URL:-https://api.github.com}"

info() {
  printf '%s\n' "http-relay: $*"
}

fail() {
  printf '%s\n' "http-relay: error: $*" >&2
  exit 1
}

has_cmd() {
  command -v "$1" >/dev/null 2>&1
}

http_get() {
  url="$1"

  if has_cmd curl; then
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" "$url"
    else
      curl -fsSL "$url"
    fi
    return
  fi

  if has_cmd wget; then
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      wget -qO- --header="Authorization: Bearer $GITHUB_TOKEN" "$url"
    else
      wget -qO- "$url"
    fi
    return
  fi

  fail "curl or wget is required"
}

download_file() {
  url="$1"
  output="$2"

  if has_cmd curl; then
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      curl -fL -H "Authorization: Bearer $GITHUB_TOKEN" -o "$output" "$url"
    else
      curl -fL -o "$output" "$url"
    fi
    return
  fi

  if has_cmd wget; then
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      wget -O "$output" --header="Authorization: Bearer $GITHUB_TOKEN" "$url"
    else
      wget -O "$output" "$url"
    fi
    return
  fi

  fail "curl or wget is required"
}

normalize_os() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    linux) printf '%s\n' "linux" ;;
    darwin) printf '%s\n' "darwin" ;;
    mingw* | msys* | cygwin*) printf '%s\n' "windows" ;;
    *) fail "unsupported OS: $os" ;;
  esac
}

normalize_arch() {
  arch="$(uname -m | tr '[:upper:]' '[:lower:]')"
  case "$arch" in
    x86_64 | amd64) printf '%s\n' "amd64" ;;
    arm64 | aarch64) printf '%s\n' "arm64" ;;
    *) fail "unsupported architecture: $arch" ;;
  esac
}

arch_matches() {
  name="$1"
  arch="$2"

  case "$arch" in
    amd64)
      case "$name" in
        *amd64* | *x86_64*) return 0 ;;
      esac
      ;;
    arm64)
      case "$name" in
        *arm64* | *aarch64*) return 0 ;;
      esac
      ;;
  esac

  return 1
}

select_asset_url() {
  urls="$1"
  os="$2"
  arch="$3"
  ext="$4"

  for url in $urls; do
    name="$(basename "$url")"
    lower_name="$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')"

    case "$lower_name" in
      *checksums* | *.txt | *.sig | *.pem) continue ;;
    esac

    case "$lower_name" in
      *"$os"*) ;;
      *) continue ;;
    esac

    arch_matches "$lower_name" "$arch" || continue

    case "$lower_name" in
      *"$ext") printf '%s\n' "$url"; return 0 ;;
    esac
  done

  return 1
}

select_checksum_url() {
  urls="$1"

  for url in $urls; do
    lower_name="$(basename "$url" | tr '[:upper:]' '[:lower:]')"
    case "$lower_name" in
      *checksums*.txt) printf '%s\n' "$url"; return 0 ;;
    esac
  done

  return 1
}

verify_checksum() {
  checksum_file="$1"
  archive_file="$2"
  archive_name="$3"

  expected="$(awk -v file="$archive_name" '$2 == file { print $1 }' "$checksum_file" | head -n 1)"
  [ -n "$expected" ] || fail "checksum for $archive_name was not found"

  if has_cmd sha256sum; then
    actual="$(sha256sum "$archive_file" | awk '{ print $1 }')"
  elif has_cmd shasum; then
    actual="$(shasum -a 256 "$archive_file" | awk '{ print $1 }')"
  else
    info "sha256sum or shasum not found; skipping checksum verification"
    return 0
  fi

  [ "$expected" = "$actual" ] || fail "checksum mismatch for $archive_name"
  info "verified checksum"
}

install_binary() {
  src="$1"
  dst="$BINDIR/$BINARY"

  if [ "$(basename "$src")" = "$BINARY.exe" ]; then
    dst="$dst.exe"
  fi

  if mkdir -p "$BINDIR" 2>/dev/null && [ -w "$BINDIR" ]; then
    cp "$src" "$dst"
    chmod 755 "$dst"
    return
  fi

  has_cmd sudo || fail "cannot write to $BINDIR; rerun with BINDIR set to a writable directory or install sudo"
  sudo mkdir -p "$BINDIR"
  sudo cp "$src" "$dst"
  sudo chmod 755 "$dst"
}

main() {
  os="$(normalize_os)"
  arch="$(normalize_arch)"
  ext="tar.gz"
  [ "$os" = "windows" ] && ext="zip"

  if [ "$VERSION" = "latest" ]; then
    release_api="$API_BASE/repos/$REPO/releases/latest"
  else
    release_api="$API_BASE/repos/$REPO/releases/tags/$VERSION"
  fi

  info "resolving $REPO $VERSION for $os/$arch"
  release_json="$(http_get "$release_api")"
  asset_urls="$(printf '%s\n' "$release_json" | sed -n 's/.*"browser_download_url":[[:space:]]*"\([^"]*\)".*/\1/p')"
  [ -n "$asset_urls" ] || fail "no downloadable assets found in release"

  asset_url="$(select_asset_url "$asset_urls" "$os" "$arch" "$ext")" || {
    printf '%s\n' "$asset_urls" >&2
    fail "no matching $os/$arch .$ext asset found"
  }

  tmpdir="$(mktemp -d 2>/dev/null || mktemp -d -t http-relay)"
  trap 'rm -rf "$tmpdir"' EXIT INT TERM

  archive_name="$(basename "$asset_url")"
  archive_file="$tmpdir/$archive_name"

  info "downloading $archive_name"
  download_file "$asset_url" "$archive_file"

  checksum_url="$(select_checksum_url "$asset_urls" || true)"
  if [ -n "$checksum_url" ]; then
    checksum_file="$tmpdir/checksums.txt"
    download_file "$checksum_url" "$checksum_file"
    verify_checksum "$checksum_file" "$archive_file" "$archive_name"
  else
    info "checksums.txt not found; skipping checksum verification"
  fi

  extract_dir="$tmpdir/extract"
  mkdir -p "$extract_dir"

  case "$ext" in
    zip)
      has_cmd unzip || fail "unzip is required to extract $archive_name"
      unzip -q "$archive_file" -d "$extract_dir"
      ;;
    tar.gz)
      tar -xzf "$archive_file" -C "$extract_dir"
      ;;
  esac

  binary_path="$(find "$extract_dir" -type f \( -name "$BINARY" -o -name "$BINARY.exe" \) | head -n 1)"
  [ -n "$binary_path" ] || fail "$BINARY binary was not found in $archive_name"

  install_binary "$binary_path"
  info "installed to $BINDIR"
  "$BINDIR/$BINARY" version 2>/dev/null || true
}

main "$@"
