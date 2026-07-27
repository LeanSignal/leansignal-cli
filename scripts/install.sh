#!/usr/bin/env sh
# leanctl installer for macOS and Linux.
#
# Quick start:
#   curl -fsSL https://raw.githubusercontent.com/LeanSignal/lean-cli/main/scripts/install.sh | sh
#
# While the repository is PRIVATE, both that URL and the release assets need a
# GitHub credential. Either export a token with repo read access:
#   export GH_TOKEN=ghp_…
#   curl -fsSL -H "Authorization: Bearer $GH_TOKEN" \
#     https://raw.githubusercontent.com/LeanSignal/lean-cli/main/scripts/install.sh | sh
# …or, if the GitHub CLI is installed and logged in, this script uses it
# automatically and no token is needed.
#
# Options (flags or environment):
#   --version vX.Y.Z   VERSION    release to install (default: latest)
#   --bin-dir DIR      BIN_DIR    install location (default: /usr/local/bin,
#                                 or ~/.local/bin when that is not writable)
#   --no-verify        NO_VERIFY  skip checksum verification
#
# Review this script before piping it to a shell.
set -eu

REPO="${LEANCTL_REPO:-LeanSignal/lean-cli}"
VERSION="${VERSION:-latest}"
BIN_DIR="${BIN_DIR:-}"
NO_VERIFY="${NO_VERIFY:-0}"

# GH_TOKEN / GITHUB_TOKEN are the conventional names; LEANCTL_INSTALL_TOKEN is
# there for a token scoped to this install and nothing else.
TOKEN="${LEANCTL_INSTALL_TOKEN:-${GH_TOKEN:-${GITHUB_TOKEN:-}}}"

info() { printf '\033[0;36m[leanctl]\033[0m %s\n' "$*"; }
warn() { printf '\033[0;33m[leanctl]\033[0m %s\n' "$*" >&2; }
err() {
  printf '\033[0;31m[leanctl]\033[0m %s\n' "$*" >&2
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      VERSION="${2:?--version needs a value}"
      shift 2
      ;;
    --bin-dir)
      BIN_DIR="${2:?--bin-dir needs a value}"
      shift 2
      ;;
    --no-verify)
      NO_VERIFY=1
      shift
      ;;
    -h | --help)
      sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) err "unknown option: $1 (try --help)" ;;
  esac
done

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar >/dev/null 2>&1 || err "tar is required"

# ── Platform ────────────────────────────────────────────────────────────────
os="$(uname -s)"
case "$os" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) err "unsupported operating system: $os (leanctl ships for macOS and Linux)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) err "unsupported architecture: $arch (leanctl ships for amd64 and arm64)" ;;
esac

# ── Release access ──────────────────────────────────────────────────────────
# Two ways in. `gh` is preferred when present because it already holds a
# credential and handles private-asset redirects; plain curl is the fallback and
# is all a public release needs.
use_gh=0
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
  use_gh=1
fi

api() { # api <path> -> stdout
  if [ -n "$TOKEN" ]; then
    curl -fsSL -H "Authorization: Bearer $TOKEN" \
      -H "X-GitHub-Api-Version: 2022-11-28" "https://api.github.com/repos/$REPO/$1"
  else
    curl -fsSL -H "X-GitHub-Api-Version: 2022-11-28" "https://api.github.com/repos/$REPO/$1"
  fi
}

if [ "$VERSION" = "latest" ]; then
  info "resolving the latest release…"

  if [ "$use_gh" = "1" ]; then
    VERSION="$(gh release view --repo "$REPO" --json tagName --jq .tagName 2>/dev/null || true)"
  else
    # Buffer the response before parsing: piping curl into `grep -m1` closes the
    # pipe early and curl exits 23.
    rel="$(api releases/latest 2>/dev/null || true)"
    VERSION="$(printf '%s' "$rel" | grep -o '"tag_name": *"[^"]*"' | head -n1 | cut -d'"' -f4)"
  fi

  [ -n "$VERSION" ] || err "could not resolve the latest release of $REPO.
  If the repository is private, authenticate first:
    export GH_TOKEN=…            (a token with repo read access)
  or install and log in to the GitHub CLI:
    gh auth login
  If no release exists yet, build from source instead:
    git clone git@github.com:$REPO.git && cd lean-cli && make install"
fi

# Release archives are named by goreleaser as
# leanctl_<version-without-v>_<os>_<arch>.tar.gz.
plain_version="${VERSION#v}"
archive="leanctl_${plain_version}_${os}_${arch}.tar.gz"

tmp="$(mktemp -d)"
# shellcheck disable=SC2064 # expand tmp now, not at trap time
trap "rm -rf '$tmp'" EXIT INT TERM

info "downloading $archive ($VERSION)…"

if [ "$use_gh" = "1" ]; then
  gh release download "$VERSION" --repo "$REPO" --pattern "$archive" --dir "$tmp" ||
    err "download failed: $archive not found in release $VERSION"

  if [ "$NO_VERIFY" != "1" ]; then
    gh release download "$VERSION" --repo "$REPO" --pattern checksums.txt --dir "$tmp" 2>/dev/null || true
  fi
else
  url="https://github.com/$REPO/releases/download/$VERSION/$archive"

  if [ -n "$TOKEN" ]; then
    # A private release asset redirects to a signed storage URL, and curl drops
    # the Authorization header across that hop by design — which is what we
    # want, since forwarding it makes the storage host reject the request.
    curl -fsSL -H "Authorization: Bearer $TOKEN" -o "$tmp/$archive" "$url" ||
      err "download failed: $url
  For a private repository, install the GitHub CLI and run 'gh auth login' —
  it handles private release assets correctly."
  else
    curl -fsSL -o "$tmp/$archive" "$url" || err "download failed: $url"
  fi

  if [ "$NO_VERIFY" != "1" ]; then
    curl -fsSL -o "$tmp/checksums.txt" \
      "https://github.com/$REPO/releases/download/$VERSION/checksums.txt" 2>/dev/null || true
  fi
fi

# ── Verify ──────────────────────────────────────────────────────────────────
if [ "$NO_VERIFY" = "1" ]; then
  warn "skipping checksum verification (--no-verify)"
elif [ -f "$tmp/checksums.txt" ]; then
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmp" && sha256sum -c checksums.txt --ignore-missing >/dev/null) ||
      err "checksum verification FAILED for $archive — do not use this download"
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$tmp" && shasum -a 256 -c checksums.txt --ignore-missing >/dev/null) ||
      err "checksum verification FAILED for $archive — do not use this download"
  else
    warn "no sha256sum or shasum available; skipping checksum verification"
  fi
  info "checksum verified"
else
  warn "checksums.txt not available; skipping verification"
fi

tar -xzf "$tmp/$archive" -C "$tmp" || err "could not extract $archive"
[ -f "$tmp/leanctl" ] || err "the archive did not contain a leanctl binary"
chmod +x "$tmp/leanctl"

# ── Install ─────────────────────────────────────────────────────────────────
# Prefer /usr/local/bin, but never demand sudo: falling back to ~/.local/bin
# keeps the one-liner working for a user who cannot escalate.
if [ -z "$BIN_DIR" ]; then
  if [ -w /usr/local/bin ] 2>/dev/null; then
    BIN_DIR=/usr/local/bin
  elif [ "$(id -u)" = "0" ]; then
    BIN_DIR=/usr/local/bin
  else
    BIN_DIR="$HOME/.local/bin"
  fi
fi

mkdir -p "$BIN_DIR" || err "could not create $BIN_DIR"
install -m 0755 "$tmp/leanctl" "$BIN_DIR/leanctl" 2>/dev/null ||
  cp "$tmp/leanctl" "$BIN_DIR/leanctl" ||
  err "could not write to $BIN_DIR — re-run with --bin-dir DIR, or with sudo"

chmod 0755 "$BIN_DIR/leanctl"

# macOS quarantines anything carrying the com.apple.quarantine attribute. curl
# does not set it, but clear it defensively so a browser-fetched archive that
# was extracted by hand does not trip Gatekeeper.
if [ "$os" = "darwin" ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "$BIN_DIR/leanctl" 2>/dev/null || true
fi

info "installed $BIN_DIR/leanctl ($VERSION)"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    warn "$BIN_DIR is not on your PATH. Add it:"
    warn "  echo 'export PATH=\"\$PATH:$BIN_DIR\"' >> ~/.zshrc   # ~/.bashrc on Linux"
    ;;
esac

info "next: leanctl auth login --api-url https://<tenant>-api.eu11.leansignal.io"
