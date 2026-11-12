#!/bin/sh
#
# Sila (صِلَة) - Zero-repo universal installer
#
# Installs standalone Sila binary for Linux, macOS, and Windows.
# Usage:
#   curl -fsSL https://sila.jadmadi.net/install.sh | sh
#   wget -qO- https://sila.jadmadi.net/install.sh | sh
#
# Environment variables:
#   SILA_VERSION       Version to install (default: latest)
#   SILA_INSTALL_DIR   Target directory (default: ~/.local/bin or /usr/local/bin for root)
#   SILA_URL_BASE      Base distribution URL (default: https://sila.jadmadi.net)
#   SILA_NO_VERIFY     Set to 1 to bypass SHA-256 verification

set -eu

# Color formatting if terminal supports it
if [ -t 1 ]; then
  BOLD="\033[1m"
  GREEN="\033[32m"
  CYAN="\033[36m"
  YELLOW="\033[33m"
  RED="\033[31m"
  RESET="\033[0m"
else
  BOLD=""
  GREEN=""
  CYAN=""
  YELLOW=""
  RED=""
  RESET=""
fi

printf "${BOLD}==> Installing Sila (صِلَة) — Autonomous AI Agent Continuity & Governance Substrate${RESET}\n"

# 1. Detect Operating System
OS="$(uname -s)"
case "$OS" in
  Linux*|linux*)
    OS="linux"
    ;;
  Darwin*|darwin*)
    OS="darwin"
    ;;
  MINGW*|MSYS*|CYGWIN*)
    OS="windows"
    ;;
  *)
    printf "${RED}Error: Unsupported operating system '%s'.${RESET}\n" "$OS" >&2
    printf "Sila supports Linux, macOS, and Windows.\n" >&2
    exit 1
    ;;
esac

# 2. Detect Hardware Architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)
    ARCH="amd64"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    printf "${RED}Error: Unsupported architecture '%s'.${RESET}\n" "$ARCH" >&2
    printf "Sila provides precompiled binaries for amd64 and arm64.\n" >&2
    exit 1
    ;;
esac

EXT=""
if [ "$OS" = "windows" ]; then
  EXT=".exe"
fi

ASSET_NAME="sila-${OS}-${ARCH}${EXT}"
SILA_URL_BASE="${SILA_URL_BASE:-https://sila.jadmadi.net/sila}"
case "$SILA_URL_BASE" in
  */sila) ;;
  *) SILA_URL_BASE="${SILA_URL_BASE}/sila" ;;
esac
SILA_VERSION="${SILA_VERSION:-latest}"

# 3. Determine Installation Directory
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

# 4. Create temporary working directory
TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t 'sila-install.XXXXXX')"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

download_url() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
  else
    printf "${RED}Error: Neither 'curl' nor 'wget' was found on your system.${RESET}\n" >&2
    exit 1
  fi
}

# 5. Fetch Binary
printf "${BOLD}==> Fetching %s...${RESET}\n" "$ASSET_NAME"
PRIMARY_BIN_URL="${SILA_URL_BASE}/${SILA_VERSION}/${ASSET_NAME}"

if ! download_url "$PRIMARY_BIN_URL" "$TMP_DIR/$ASSET_NAME" 2>/dev/null; then
  printf "${RED}Error: Failed to download %s from %s.${RESET}\n" "$ASSET_NAME" "$PRIMARY_BIN_URL" >&2
  exit 1
fi

# 6. Verify Cryptographic Integrity (SHA-256)
NO_VERIFY="${SILA_NO_VERIFY:-0}"
if [ "$NO_VERIFY" != "1" ] && [ "$NO_VERIFY" != "true" ]; then
  PRIMARY_CHECKSUM_URL="${SILA_URL_BASE}/${SILA_VERSION}/checksums.txt"

  if download_url "$PRIMARY_CHECKSUM_URL" "$TMP_DIR/checksums.txt" 2>/dev/null; then

    EXPECTED_HASH="$(awk -v bin="$ASSET_NAME" '($2 == bin || $2 == "./"bin || $2 == "dist/"bin) { print $1 }' "$TMP_DIR/checksums.txt" | head -n 1)"

    if [ -n "$EXPECTED_HASH" ]; then
      ACTUAL_HASH=""
      if command -v sha256sum >/dev/null 2>&1; then
        ACTUAL_HASH="$(sha256sum "$TMP_DIR/$ASSET_NAME" | awk '{print $1}')"
      elif command -v shasum >/dev/null 2>&1; then
        ACTUAL_HASH="$(shasum -a 256 "$TMP_DIR/$ASSET_NAME" | awk '{print $1}')"
      elif command -v openssl >/dev/null 2>&1; then
        ACTUAL_HASH="$(openssl dgst -sha256 "$TMP_DIR/$ASSET_NAME" | awk '{print $NF}')"
      fi

      if [ -n "$ACTUAL_HASH" ]; then
        if [ "$EXPECTED_HASH" != "$ACTUAL_HASH" ]; then
          printf "${RED}Security Error: SHA-256 checksum mismatch!${RESET}\n" >&2
          printf "  Expected: %s\n" "$EXPECTED_HASH" >&2
          printf "  Actual:   %s\n" "$ACTUAL_HASH" >&2
          exit 1
        fi
        printf "  ✔ Cryptographic integrity verified: ${GREEN}%s${RESET}\n" "$ACTUAL_HASH"
      fi
    fi
  fi
fi

# 7. Install Binary Atomically
if ! mkdir -p "$SILA_INSTALL_DIR" 2>/dev/null || [ ! -w "$SILA_INSTALL_DIR" ]; then
  printf "${RED}Error: Cannot write to install directory '%s'.${RESET}\n" "$SILA_INSTALL_DIR" >&2
  printf "Please run with sudo or set SILA_INSTALL_DIR to a writable directory, e.g.:\n" >&2
  printf "  SILA_INSTALL_DIR=\"\$HOME/.local/bin\" curl -fsSL %s/install.sh | sh\n" "$SILA_URL_BASE" >&2
  exit 1
fi

DEST_BIN="${SILA_INSTALL_DIR}/sila${EXT}"
TMP_INSTALL_BIN="${DEST_BIN}.tmp.$$"

cp "$TMP_DIR/$ASSET_NAME" "$TMP_INSTALL_BIN"
chmod 755 "$TMP_INSTALL_BIN"
mv -f "$TMP_INSTALL_BIN" "$DEST_BIN"

# 8. Test Execution
VER_OUTPUT="$("$DEST_BIN" version 2>/dev/null || "$DEST_BIN" --version 2>/dev/null || echo "sila")"

printf "\n${GREEN}${BOLD}✔ Successfully installed Sila!${RESET}\n"
printf "  • Binary:       ${CYAN}%s${RESET}\n" "$DEST_BIN"
printf "  • Version Info: %s\n\n" "$VER_OUTPUT"

# 9. Verify PATH Visibility
case ":$PATH:" in
  *":$SILA_INSTALL_DIR:"*)
    ;;
  *)
    printf "${YELLOW}⚠ Notice: '%s' is not in your current \$PATH.${RESET}\n" "$SILA_INSTALL_DIR"
    printf "To execute 'sila' from any directory, add it to your shell configuration:\n\n"
    if [ -n "${ZSH_VERSION:-}" ] || [ -f "$HOME/.zshrc" ]; then
      printf "  ${CYAN}echo 'export PATH=\"%s:\$PATH\"' >> ~/.zshrc && source ~/.zshrc${RESET}\n\n" "$SILA_INSTALL_DIR"
    elif [ -f "$HOME/.bashrc" ]; then
      printf "  ${CYAN}echo 'export PATH=\"%s:\$PATH\"' >> ~/.bashrc && source ~/.bashrc${RESET}\n\n" "$SILA_INSTALL_DIR"
    else
      printf "  ${CYAN}echo 'export PATH=\"%s:\$PATH\"' >> ~/.profile && source ~/.profile${RESET}\n\n" "$SILA_INSTALL_DIR"
    fi
    ;;
esac

# 10. Quickstart Guidance
printf "${BOLD}Quickstart Commands:${RESET}\n"
printf "  ${CYAN}sila prime${RESET}        Prime agent context with project facts, rules, and goals\n"
printf "  ${CYAN}sila clean${RESET}        Universal disk hygiene across Go, Rust, pnpm, npm, uv, bun, zig\n"
printf "  ${CYAN}sila gate${RESET}         Execute pre-commit quality discipline checks\n"
printf "  ${CYAN}sila mcp${RESET}          Run 58-tool Model Context Protocol server\n"
printf "  ${CYAN}sila doctor${RESET}       Verify installation, database integrity, and data sovereignty\n\n"
printf "Documentation & Source: ${BOLD}https://github.com/jadmadi/sila${RESET}\n"
