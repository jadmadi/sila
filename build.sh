#!/usr/bin/env bash
#
# build.sh - Build script for sila CLI
#
# Provides versatile build options across development, release, and ultra-compressed
# modes with automatic version tag injection and cross-compilation support.

set -euo pipefail

# Determine the canonical script directory and ensure CWD is anchored to the Go module root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Ensure internal/release/CHANGELOG.md is synced with root CHANGELOG.md for go:embed
if [[ -f "$SCRIPT_DIR/CHANGELOG.md" ]]; then
    cmp -s "$SCRIPT_DIR/CHANGELOG.md" "$SCRIPT_DIR/internal/release/CHANGELOG.md" 2>/dev/null || cp "$SCRIPT_DIR/CHANGELOG.md" "$SCRIPT_DIR/internal/release/CHANGELOG.md"
fi

# ANSI color codes for terminal output
BOLD=$'\033[1m'
RED=$'\033[31m'
GREEN=$'\033[32m'
YELLOW=$'\033[33m'
CYAN=$'\033[36m'
RESET=$'\033[0m'

show_help() {
    cat <<EOF
${BOLD}sila Build Script (${CYAN}build.sh${RESET}${BOLD})${RESET}

${BOLD}USAGE:${RESET}
    ./build.sh [MODE/OPTIONS] [TARGETS]
    ${CYAN}(Running ./build.sh without arguments defaults to: --release --install)${RESET}

${BOLD}BUILD MODES (Select one, defaults to --release):${RESET}

  ${CYAN}--upx${RESET}       ${BOLD}Ultra-Compressed Distribution Build (opt-in)${RESET}
              Command: Builds with --release flags, then compresses binary using upx --best --lzma
              • ${BOLD}Pros:${RESET}  Ultra-compact binary size (~75-80% reduction), minimum disk space.
              • ${BOLD}Cons:${RESET}  Requires 'upx' tool installed (sudo apt install upx-ucl).

  ${GREEN}--release${RESET}   ${BOLD}Production / Stripped Build without UPX${RESET}
              Command: CGO_ENABLED=0 go build -ldflags="-s -w -X ..." -trimpath -o sila ./cmd/sila
              • ${BOLD}Pros:${RESET}  Fast startup, fully self-contained (CGO_ENABLED=0), stripped symbols.

  ${YELLOW}--dev${RESET}       ${BOLD}Development / Debug Build${RESET}
              Command: go build -o sila ./cmd/sila
              • ${BOLD}Pros:${RESET}  Fastest compile time, retains full DWARF debug symbols for dlv/gdb.

${BOLD}OPTIONS & TARGETS:${RESET}
  ${BOLD}-o, --output <path>${RESET}   Specify output binary filepath or directory (default: ./sila)
  ${BOLD}--target <os/arch>${RESET}    Cross-compile for target (e.g., --target linux/amd64 or --target darwin/arm64)
  ${BOLD}--all${RESET}                 Cross-compile for all major platforms (linux, darwin, windows x amd64, arm64)
                        Outputs release assets and dist/checksums.txt.
  ${BOLD}--release-artifact${RESET}    Require HEAD to be an annotated vMAJOR.MINOR.PATCH tag; used by scripts/release.sh.
  ${BOLD}--check${RESET}               Run Go Report Card and test coverage preflight verification
  ${BOLD}--install[=<dir>]${RESET}     Copy the native single build into a bin dir (default: ~/.local/bin).
                                Pass --install=/usr/local/bin for system-wide sandbox visibility
                                (Antigravity, Cursor, DevContainers); may require sudo.
  ${BOLD}--no-install${RESET}          Disable automatic installation to ~/.local/bin
  ${BOLD}--no-upx${RESET}              Disable UPX compression (alias for --release)
  ${BOLD}-h, --help${RESET}            Display this help explanation and exit

${BOLD}EXAMPLES:${RESET}
    ./build.sh                                # Build uncompressed and install to ~/.local/bin (default)
    sudo ./build.sh --install=/usr/local/bin   # Install system-wide for container sandbox visibility
    ./build.sh --release                      # Build stripped binary without UPX, install to ~/.local/bin
    ./build.sh --dev --no-install             # Build local debug binary without installing
    ./build.sh --release --target darwin/arm64  # Cross-compile release binary for macOS Apple Silicon
    ./build.sh --all                          # Build release binaries for all primary OS/Arch targets
EOF
}

MODE="release"
OUTPUT=""
TARGET_OS_ARCH=""
BUILD_ALL=false
RELEASE_ARTIFACT=false
CHECK=false
if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    INSTALL_DIR="/usr/local/bin"
else
    INSTALL_DIR="${HOME}/.local/bin"
fi

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --release|--no-upx)
            MODE="release"
            shift
            ;;
        --dev)
            MODE="dev"
            shift
            ;;
        --upx)
            MODE="upx"
            shift
            ;;
        --check)
            CHECK=true
            shift
            ;;
        -o|--output)
            OUTPUT="$2"
            shift 2
            ;;
        --target)
            TARGET_OS_ARCH="$2"
            shift 2
            ;;
        --all)
            BUILD_ALL=true
            shift
            ;;
        --release-artifact)
            RELEASE_ARTIFACT=true
            shift
            ;;
        --install=*)
            INSTALL_DIR="${1#*=}"
            shift
            ;;
        --install)
            if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
                INSTALL_DIR="/usr/local/bin"
            else
                INSTALL_DIR="${HOME}/.local/bin"
            fi
            shift
            ;;
        --no-install)
            INSTALL_DIR=""
            shift
            ;;
        -h|--help|help)
            show_help
            exit 0
            ;;
        *)
            echo -e "${RED}Error: Unknown argument '$1'${RESET}" >&2
            echo "Run './build.sh --help' for usage information." >&2
            exit 1
            ;;
    esac
done

# Resolve a release identity from an exact SemVer tag. Untagged builds remain
# visibly developmental and must not be distributed as stable artifacts.
EXACT_TAG=$(git describe --tags --exact-match HEAD 2>/dev/null || true)
if [[ -n "${SILA_BUILD_VERSION:-}" ]]; then
    VERSION="$SILA_BUILD_VERSION"
elif [[ "$EXACT_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
    VERSION="$EXACT_TAG"
else
    VERSION="dev+$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    if [[ -n "$(git status --porcelain 2>/dev/null)" ]]; then VERSION+="-dirty"; fi
fi
if [[ "$RELEASE_ARTIFACT" == "true" && ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
    echo -e "${RED}Error: --release-artifact requires HEAD to be an exact SemVer Git tag.${RESET}" >&2
    exit 1
fi
if [[ "$RELEASE_ARTIFACT" == "true" && "$(git cat-file -t "$VERSION" 2>/dev/null || true)" != "tag" ]]; then
    echo -e "${RED}Error: --release-artifact requires an annotated Git tag.${RESET}" >&2
    exit 1
fi
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS="-X github.com/jadmadi/sila/internal/version.Version=${VERSION} \
-X github.com/jadmadi/sila/internal/version.Commit=${COMMIT} \
-X github.com/jadmadi/sila/internal/version.Date=${DATE}"

build_single() {
    local target_goos="${1:-$(go env GOOS)}"
    local target_goarch="${2:-$(go env GOARCH)}"
    local out_path="${3:-}"

    if [[ -z "$out_path" ]]; then
        if [[ "$target_goos" == "windows" ]]; then
            out_path="sila.exe"
        else
            out_path="sila"
        fi
    fi

    echo -e "${BOLD}Building sila (${CYAN}${MODE}${RESET}${BOLD} mode) for ${YELLOW}${target_goos}/${target_goarch}${RESET}${BOLD}...${RESET}"

    local go_flags=()
    local env_vars=("GOOS=${target_goos}" "GOARCH=${target_goarch}")

    case "$MODE" in
        release|upx)
            env_vars+=("CGO_ENABLED=0")
            go_flags+=("-trimpath" "-ldflags=-s -w ${LDFLAGS}")
            ;;
        dev)
            go_flags+=("-ldflags=${LDFLAGS}")
            ;;
    esac

    # Run go build
    env "${env_vars[@]}" go build "${go_flags[@]}" -o "$out_path" ./cmd/sila

    if [[ "$MODE" == "upx" ]]; then
        if ! command -v upx >/dev/null 2>&1; then
            echo -e "${RED}Warning: 'upx' command not found. Skipping LZMA compression.${RESET}" >&2
            echo -e "Install upx (e.g. 'sudo apt install upx-ucl') to enable ultra-compression." >&2
        else
            echo -e "Compressing ${CYAN}${out_path}${RESET} with UPX (--best --lzma) — this trades ~350ms of startup latency on every invocation for ~1.3MB of download size; see docs/issues-perf or docs/perf-baseline.md"
            local upx_flags=("--best" "--lzma")
            if [[ "$target_goos" == "darwin" ]]; then
                upx_flags+=("--force-macos")
            fi
            upx "${upx_flags[@]}" "$out_path" >/dev/null
        fi
    fi

    local file_size
    file_size=$(ls -lh "$out_path" | awk '{print $5}')

    echo -e "${GREEN}✔ Built successfully:${RESET} ${BOLD}${out_path}${RESET} (${CYAN}${file_size}${RESET})"
}

if [[ "$RELEASE_ARTIFACT" == "true" || "$CHECK" == "true" ]]; then
    echo -e "${BOLD}Running quality preflight (Go Report Card & Test Coverage)...${RESET}"
    if ! go run ./cmd/sila gate report-card; then
        echo -e "${RED}Error: Go Report Card preflight failed.${RESET}" >&2
        exit 1
    fi

    COVER_TMP=$(mktemp "${TMPDIR:-/tmp}/sila-build-cover.XXXXXX.out")
    TEST_LOG=$(mktemp "${TMPDIR:-/tmp}/sila-build-test.XXXXXX.log")
    cleanup_preflight() {
        rm -f "$COVER_TMP" "$TEST_LOG"
    }
    trap cleanup_preflight EXIT INT TERM

    if ! go test -coverprofile="$COVER_TMP" ./cmd/... ./internal/... >"$TEST_LOG" 2>&1; then
        echo -e "${RED}Error: Test suite failed during release quality preflight.${RESET}" >&2
        cat "$TEST_LOG" >&2
        exit 1
    fi
    rm -f "$TEST_LOG"

    if [[ ! -s "$COVER_TMP" ]]; then
        echo -e "${RED}Error: Coverage profile is missing or empty.${RESET}" >&2
        exit 1
    fi

    if ! go run ./cmd/sila gate coverage --profile="$COVER_TMP"; then
        echo -e "${RED}Error: Test coverage requirements rejected.${RESET}" >&2
        exit 1
    fi

    trap - EXIT INT TERM
    cleanup_preflight
    echo -e "${GREEN}✔ Quality preflight passed.${RESET}\n"
fi


if [[ "$BUILD_ALL" == "true" ]]; then
    mkdir -p dist
    targets=(
        "linux/amd64"
        "linux/arm64"
        "darwin/amd64"
        "darwin/arm64"
        "windows/amd64"
        "windows/arm64"
    )
    for t in "${targets[@]}"; do
        IFS='/' read -r goos goarch <<< "$t"
        ext=""
        if [[ "$goos" == "windows" ]]; then ext=".exe"; fi
        out_file="dist/sila-${goos}-${goarch}${ext}"
        build_single "$goos" "$goarch" "$out_file"
    done
    cat <<EOF > dist/version.json
{
  "version": "${VERSION}",
  "commit": "${COMMIT}",
  "built_at": "${DATE}",
  "platforms": [
    "linux-amd64",
    "linux-arm64",
    "darwin-amd64",
    "darwin-arm64",
    "windows-amd64",
    "windows-arm64"
  ]
}
EOF
    (
        cd dist
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum sila-* version.json > checksums.txt
        else
            shasum -a 256 sila-* version.json > checksums.txt
        fi
    )
    echo -e "\n${GREEN}${BOLD}✔ All target builds completed inside dist/ directory.${RESET}"
elif [[ -n "$TARGET_OS_ARCH" ]]; then
    IFS='/' read -r goos goarch <<< "$TARGET_OS_ARCH"
    build_single "$goos" "$goarch" "$OUTPUT"
else
    build_single "$(go env GOOS)" "$(go env GOARCH)" "$OUTPUT"
fi

# Install the native single binary into a PATH bin dir (opt-in via --install).
if [[ -n "$INSTALL_DIR" ]]; then
    if [[ "$BUILD_ALL" == "true" || -n "$TARGET_OS_ARCH" ]]; then
        echo -e "${YELLOW}install skipped: --all/--target produce cross-arch dist binaries. Run a native build to install.${RESET}" >&2
    else
        src="${OUTPUT:-sila}"
        if ! mkdir -p "$INSTALL_DIR" 2>/dev/null || [[ ! -w "$INSTALL_DIR" ]]; then
            echo -e "${RED}Error: Cannot write to install directory ${BOLD}${INSTALL_DIR}${RESET} (permission denied)." >&2
            echo -e "  For system-wide paths like /usr/local/bin, re-run with sudo:" >&2
            echo -e "    ${CYAN}sudo ./build.sh --install=${INSTALL_DIR}${RESET}" >&2
            echo -e "  Or install to your user bin directory without sudo:" >&2
            echo -e "    ${CYAN}./build.sh --install=\${HOME}/.local/bin${RESET}" >&2
            exit 1
        fi
        target="$INSTALL_DIR/$(basename "$src")"
        # Atomic install using temporary file to prevent text file busy (ETXTBSY) if running as daemon/MCP
        tmp_target="${target}.tmp.$$"
        cp "$src" "$tmp_target"
        chmod +x "$tmp_target"
        mv -f "$tmp_target" "$target"
        echo -e "${GREEN}✔ Installed:${RESET} ${BOLD}${target}${RESET}"

        # Workspace binary copy for direct repository access (./bin/sila)
        mkdir -p bin
        cp -f "$target" "bin/$(basename "$src")"
        chmod +x "bin/$(basename "$src")"
        echo -e "${GREEN}✔ Workspace binary:${RESET} ${BOLD}bin/$(basename "$src")${RESET}"

        # Subshell PATH Sentry: Warn if INSTALL_DIR is not present in $PATH
        case ":$PATH:" in
            *":$INSTALL_DIR:"*)
                ;;
            *)
                echo -e "\n${YELLOW}⚠ Subshell PATH Sentry Notice:${RESET} ${BOLD}${INSTALL_DIR}${RESET} is not in current \$PATH."
                echo -e "  To execute 'sila' across subshells and AI agents without full path, add to your shell profile:"
                echo -e "    ${CYAN}export PATH=\"${INSTALL_DIR}:\$PATH\"${RESET}\n"
                ;;
        esac

        # Hardened Path Linking: link into GOPATH/bin if distinct, writable, and present
        gopath_bin="$(go env GOPATH 2>/dev/null || true)"
        if [[ -n "$gopath_bin" && -d "$gopath_bin/bin" && "$gopath_bin/bin" != "$INSTALL_DIR" ]]; then
            if [[ -w "$gopath_bin/bin" ]]; then
                ln -sf "$target" "$gopath_bin/bin/$(basename "$src")" 2>/dev/null || true
                echo -e "${GREEN}✔ Linked to GOPATH/bin:${RESET} ${BOLD}${gopath_bin}/bin/$(basename "$src")${RESET}"
            fi
        fi
    fi
fi
