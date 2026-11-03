#!/bin/sh
# Sila (صِلَة) installer for Linux and macOS.
#
# Default source: GitHub Releases for jadmadi/sila.
# Optional mirror: set SILA_URL_BASE to an R2-compatible base.
#
# Environment:
#   SILA_RELEASE_REPO  GitHub repository (default: jadmadi/sila)
#   SILA_VERSION       Release tag or "latest" (default: latest)
#   SILA_URL_BASE      Optional download base; when set, assets are expected at
#                      ${SILA_URL_BASE}/${SILA_VERSION}/${ASSET_NAME}
#   SILA_INSTALL_DIR   Target directory (default: ~/.local/bin, or /usr/local/bin for root)
#   SILA_NO_VERIFY     Set to 1 only for local debugging; skips checksum verification

set -eu

BOLD=""; GREEN=""; CYAN=""; YELLOW=""; RED=""; RESET=""
if [ -t 1 ]; then
  BOLD="\033[1m"; GREEN="\033[32m"; CYAN="\033[36m"; YELLOW="\033[33m"; RED="\033[31m"; RESET="\033[0m"
fi

SILA_RELEASE_REPO="${SILA_RELEASE_REPO:-jadmadi/sila}"
SILA_VERSION="${SILA_VERSION:-latest}"
SILA_URL_BASE="${SILA_URL_BASE:-}"
SILA_NO_VERIFY="${SILA_NO_VERIFY:-0}"

printf "${BOLD}==> Installing Sila (صِلَة) — local-first continuity for AI coding agents${RESET}\n"

OS="$(uname -s)"
case "$OS" in
  Linux*|linux*) OS="linux" ;;
  Darwin*|darwin*) OS="darwin" ;;
  *) printf "${RED}Error: unsupported operating system '%s'.${RESET}\n" "$OS" >&2; exit 1 ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) printf "${RED}Error: unsupported architecture '%s'.${RESET}\n" "$ARCH" >&2; exit 1 ;;
esac

ASSET_NAME="sila-${OS}-${ARCH}"

download() {
  url="$1"; dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
  else
    printf "${RED}Error: neither curl nor wget is available.${RESET}\n" >&2
    exit 1
  fi
}

if [ -z "$SILA_URL_BASE" ]; then
  SILA_URL_BASE="https://github.com/${SILA_RELEASE_REPO}/releases/download"
  if [ "$SILA_VERSION" = "latest" ]; then
    LATEST_URL="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${SILA_RELEASE_REPO}/releases/latest")" || {
      printf "${RED}Error: could not resolve the latest release.${RESET}\n" >&2
      printf "Visit https://github.com/%s/releases\n" "$SILA_RELEASE_REPO" >&2
      exit 1
    }
    SILA_VERSION="${LATEST_URL##*/}"
  fi
fi

case "$SILA_VERSION" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) printf "${RED}Error: '%s' is not a release tag.${RESET}\n" "$SILA_VERSION" >&2; exit 1 ;;
esac

BASE="${SILA_URL_BASE%/}/${SILA_VERSION}"
BIN_URL="${BASE}/${ASSET_NAME}"
CHECKSUM_URL="${BASE}/checksums.txt"

if [ -z "${SILA_INSTALL_DIR:-}" ]; then
  if [ "$(id -u 2>/dev/null || echo 1)" -eq 0 ]; then
    SILA_INSTALL_DIR="/usr/local/bin"
  else
    SILA_INSTALL_DIR="${HOME}/.local/bin"
  fi
fi

printf "  • Platform:     ${CYAN}%s/%s${RESET}\n" "$OS" "$ARCH"
printf "  • Version:      ${CYAN}%s${RESET}\n" "$SILA_VERSION"
printf "  • Install dir:  ${CYAN}%s${RESET}\n" "$SILA_INSTALL_DIR"

TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t 'sila-install.XXXXXX')"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT INT TERM

printf "${BOLD}==> Fetching %s...${RESET}\n" "$ASSET_NAME"
download "$BIN_URL" "$TMP_DIR/$ASSET_NAME"

if [ "$SILA_NO_VERIFY" != "1" ] && [ "$SILA_NO_VERIFY" != "true" ]; then
  if ! download "$CHECKSUM_URL" "$TMP_DIR/checksums.txt"; then
    printf "${RED}Error: checksum manifest is missing at %s.${RESET}\n" "$CHECKSUM_URL" >&2
    printf "Refusing to install an unverified binary. Set SILA_NO_VERIFY=1 only for local debugging.\n" >&2
    exit 1
  fi
  EXPECTED="$(awk -v asset="$ASSET_NAME" '($2 == asset || $2 == "./"asset || $2 == "dist/"asset) { print $1 }' "$TMP_DIR/checksums.txt" | head -n 1)"
  [ -n "$EXPECTED" ] || { printf "${RED}Error: checksum for %s is missing.${RESET}\n" "$ASSET_NAME" >&2; exit 1; }
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "$TMP_DIR/$ASSET_NAME" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    ACTUAL="$(shasum -a 256 "$TMP_DIR/$ASSET_NAME" | awk '{print $1}')"
  else
    printf "${RED}Error: no SHA-256 tool available.${RESET}\n" >&2
    exit 1
  fi
  [ "$EXPECTED" = "$ACTUAL" ] || { printf "${RED}Security error: checksum mismatch.${RESET}\n" >&2; exit 1; }
  printf "  ✔ SHA-256 verified: %s\n" "$ACTUAL"
fi

mkdir -p "$SILA_INSTALL_DIR"
TARGET="${SILA_INSTALL_DIR}/sila"
TMP_TARGET="${TARGET}.tmp.$$"
cp "$TMP_DIR/$ASSET_NAME" "$TMP_TARGET"
chmod 755 "$TMP_TARGET"
mv -f "$TMP_TARGET" "$TARGET"

printf "\n${GREEN}${BOLD}✔ Installed %s${RESET}\n" "$TARGET"
printf "  %s\n" "$("$TARGET" --version 2>/dev/null || echo 'run sila --version')"

case ":$PATH:" in
  *":$SILA_INSTALL_DIR:"*) ;;
  *) printf "\n${YELLOW}Add %s to your PATH:${RESET}\n  export PATH=\"%s:\$PATH\"\n" "$SILA_INSTALL_DIR" "$SILA_INSTALL_DIR" ;;
esac

printf "\n${BOLD}Quickstart:${RESET}\n"
printf "  sila sources    # probe local AI tool stores\n"
printf "  sila demo       # try the sandbox without indexing your data\n"
printf "  sila sync       # ingest sessions read-only\n"
printf "  sila prime      # brief the next agent session\n"
printf "  sila mcp        # start the MCP server\n"
