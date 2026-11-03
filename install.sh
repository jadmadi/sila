#!/usr/bin/env bash
set -e

# Colors
CYAN='\033[0;36m'
GREEN='\033[0;32m'
DIM='\033[2m'
BOLD='\033[1m'
NC='\033[0m'

echo -e "${CYAN}${BOLD}sila (صِلَة)${NC} — one shared memory, rulebook, and plan for every AI coding agent"
echo -e "${DIM}Created by Jad Madi (@jadmadi on X.com)${NC}\n"

# Target installation directory
INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "${INSTALL_DIR}"

# OS & Architecture detection
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "${ARCH}" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

case "${OS}" in
    linux|darwin) ;;
    *) echo "Unsupported OS: ${OS}"; exit 1 ;;
esac

TARGET_BIN="${INSTALL_DIR}/sila"

# Check if Go is installed to build latest directly
if command -v go >/dev/null 2>&1; then
    echo -e "Installing via ${BOLD}go install${NC} (a source build from the module proxy, not the pinned release artifact)..."
    GOBIN="${INSTALL_DIR}" go install github.com/jadmadi/sila/cmd/sila@latest
else
    # Resolve ONE release identity, then fetch metadata, checksums, and the
    # binary from that pinned tag. Three independent releases/latest URLs could
    # each resolve to a different release while an install was in flight, which
    # would verify one release's checksum against another release's binary.
    LATEST_REDIRECT="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/jadmadi/sila/releases/latest)" || {
        echo "Could not resolve the latest release. Visit https://github.com/jadmadi/sila/releases"; exit 1;
    }
    RELEASE_TAG="${LATEST_REDIRECT##*/}"
    case "${RELEASE_TAG}" in
        v[0-9]*.[0-9]*.[0-9]*) ;;
        *) echo "Resolved release tag ${RELEASE_TAG} is not a release identity"; exit 1 ;;
    esac
    RELEASE_BASE="https://github.com/jadmadi/sila/releases/download/${RELEASE_TAG}"
    RELEASE_URL="${RELEASE_BASE}/sila-${OS}-${ARCH}"
    METADATA_URL="${RELEASE_BASE}/release-metadata.json"
    echo -e "Downloading ${BOLD}${RELEASE_TAG}${NC} from ${DIM}${RELEASE_URL}${NC}..."
    if [ "${SILA_NO_VERIFY:-0}" = "1" ]; then
        echo "Warning: SILA_NO_VERIFY=1 — skipping release metadata and checksum verification."
    else
        METADATA="$(curl -fsSL "${METADATA_URL}")" || { echo "Failed to download release metadata"; exit 1; }
        printf '%s' "${METADATA}" | grep -Eq '"version"[[:space:]]*:[[:space:]]*"v[0-9]+\.[0-9]+\.[0-9]+' || { echo "Invalid release metadata version"; exit 1; }
        printf '%s' "${METADATA}" | grep -Eq '"mcp_contract_sha256"[[:space:]]*:[[:space:]]*"[0-9a-f]{64}"' || { echo "Invalid release metadata contract"; exit 1; }
        printf '%s' "${METADATA}" | grep -Fq "\"${RELEASE_TAG#v}\"" || { echo "Release metadata does not describe ${RELEASE_TAG}"; exit 1; }
    fi
    TMP_BIN="$(mktemp "${INSTALL_DIR}/.sila.XXXXXX")"
    trap 'rm -f "${TMP_BIN}"' EXIT
    curl -fsSL -o "${TMP_BIN}" "${RELEASE_URL}" || {
        echo "Failed to download release binary. Please visit https://github.com/jadmadi/sila/releases";
        exit 1;
    }
    if [ "${SILA_NO_VERIFY:-0}" != "1" ]; then
        EXPECTED="$(curl -fsSL "${RELEASE_BASE}/checksums.txt" | awk -v asset="sila-${OS}-${ARCH}" '$2 == asset {print $1}')"
        [ -n "${EXPECTED}" ] || { echo "Release checksum missing"; exit 1; }
        if command -v sha256sum >/dev/null 2>&1; then ACTUAL="$(sha256sum "${TMP_BIN}" | awk '{print $1}')"; else ACTUAL="$(shasum -a 256 "${TMP_BIN}" | awk '{print $1}')"; fi
        [ "${ACTUAL}" = "${EXPECTED}" ] || { echo "Release checksum mismatch"; exit 1; }
    fi
    chmod +x "${TMP_BIN}"
    mv -f "${TMP_BIN}" "${TARGET_BIN}"
    trap - EXIT
fi

echo -e "\n${GREEN}✔ Installed successfully:${NC} ${BOLD}${TARGET_BIN}${NC}"

# Verify PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo -e "${DIM}Note: Add ${INSTALL_DIR} to your PATH in ~/.bashrc or ~/.zshrc:${NC}"
    echo -e "  export PATH=\"\$HOME/.local/bin:\$PATH\"\n"
fi

# Run quickstart check
"${TARGET_BIN}" --version 2>/dev/null || true
echo -e "\n${BOLD}Quickstart:${NC}"
echo -e "  1. Ingest past AI sessions:  ${CYAN}sila sync${NC}"
echo -e "  2. Get instant briefing:     ${CYAN}sila prime${NC}"
echo -e "  3. Start MCP server:         ${CYAN}sila mcp${NC}\n"
