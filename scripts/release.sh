#!/usr/bin/env bash
#
# release.sh creates a verified, tag-derived Sila release: it tags HEAD, builds
# every platform asset from that tag, and verifies them. It never contacts the
# network unless --push or --publish is explicitly given. There is no CI release
# workflow; this script is the sole publisher.
#
# The workflow is re-enterable, so preparing and publishing can be separate
# commands over the same immutable tag:
#
#   scripts/release.sh v0.6.0            # tag + build + verify (no network)
#   <inspect dist/>
#   scripts/release.sh v0.6.0 --push     # publish the inspected artifacts
#
# Rehearsal hooks (used by internal/release/rehearsal_test.go; not for normal
# releases): SILA_RELEASE_ROOT overrides the repository root, SILA_RELEASE_DIST
# overrides the asset directory, and SILA_RELEASE_SKIP_BUILD=1 reuses existing
# assets instead of running build.sh, so the workflow's state machine can be
# exercised without a full cross-platform build.

set -euo pipefail

: "${SILA_RELEASE_ROOT:=}"
if [[ -n "$SILA_RELEASE_ROOT" ]]; then
  ROOT="$(cd "$SILA_RELEASE_ROOT" && pwd)"
else
  ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fi
cd "$ROOT"
SKIP_BUILD="${SILA_RELEASE_SKIP_BUILD:-0}"
DIST="${SILA_RELEASE_DIST:-dist}"

usage() {
  cat <<'EOF'
Usage: scripts/release.sh vMAJOR.MINOR.PATCH[-prerelease] [--dry-run] [--verify-only] [--push] [--publish]

Creates an annotated local tag, runs the complete release preflight, builds all
platform assets uncompressed (startup latency over download size — the numbers
are in docs/releases.md), and writes dist/checksums.txt,
dist/mcp-contract.json, and dist/release-metadata.json.

Re-running the same command with --push (or --publish) on an existing tag
publishes the already-built artifacts instead of tagging or rebuilding, so the
documented "prepare, inspect, publish" sequence completes.

Options:
  --dry-run      Validate version, changelog, clean tree, and tests; do not tag or build.
  --verify-only  Validate the release identity and report the plan; no tag, no build.
  --skip-tests   Skip the full test suite when tests were already verified prior to release.
  --push         Push the validated annotated tag to origin after assets are built.
  --publish      Push the tag and create a GitHub Release with gh. Implies --push.

The script does not commit working-tree changes. Commit and review the release
content first. A failed release removes only a tag that has not been pushed; a
published tag is never deleted or moved by this script.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi
VERSION="${1:-}"
[[ -n "$VERSION" ]] || { usage >&2; exit 2; }
shift

DRY_RUN=false
VERIFY_ONLY=false
PUSH=false
PUBLISH=false
SKIP_TESTS=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=true ;;
    --verify-only) VERIFY_ONLY=true ;;
    --skip-tests|--no-tests) SKIP_TESTS=true ;;
    --push) PUSH=true ;;
    --publish) PUSH=true; PUBLISH=true ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

# --- Release identity -------------------------------------------------------
# A release identity is an exact SemVer tag. Prereleases are accepted and
# labeled; Go pseudo-versions are rejected: they identify a source build, not a
# published release, so treating one as a release would invent a version.
if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "Version must be an exact SemVer tag, for example v0.6.0." >&2
  exit 2
fi
if [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{14}-[0-9a-f]{7,}$ ]]; then
  echo "$VERSION looks like a Go pseudo-version (a source build), not a release." >&2
  echo "Publish a real tag such as v0.6.0, or a prerelease such as v0.6.0-rc.1." >&2
  exit 2
fi
IS_PRERELEASE=false
[[ "$VERSION" == *-* ]] && IS_PRERELEASE=true

TAG_EXISTS=false
TAG_KIND="none"
if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
  TAG_EXISTS=true
  TAG_KIND="$(git cat-file -t "$VERSION" 2>/dev/null || echo unknown)"
fi

echo "==> Release identity: $VERSION ($([[ "$IS_PRERELEASE" == true ]] && echo prerelease || echo stable), tag: $TAG_KIND)"

if [[ "$VERIFY_ONLY" == true ]]; then
  echo "✔ Identity verified. Re-run without --verify-only to prepare the release."
  exit 0
fi

if [[ -n "$(git status --porcelain)" ]]; then
  echo "Release requires a clean worktree. Commit, stash, or discard changes first." >&2
  exit 1
fi
if ! grep -Fq "## [${VERSION#v}]" CHANGELOG.md; then
  echo "CHANGELOG.md needs a ## [${VERSION#v}] section before release." >&2
  exit 1
fi

# A tag that already exists must be annotated and point at the released commit;
# identity is immutable, so a mismatch is a hard error rather than a re-tag.
if [[ "$TAG_EXISTS" == true ]]; then
  if [[ "$TAG_KIND" != "tag" ]]; then
    echo "Tag $VERSION is lightweight; releases require an annotated tag." >&2
    echo "Delete it and re-run to create an annotated tag: git tag -d $VERSION" >&2
    exit 1
  fi
  tag_commit="$(git rev-parse "$VERSION^{commit}")"
  head_commit="$(git rev-parse HEAD)"
  if [[ "$tag_commit" != "$head_commit" ]]; then
    echo "Tag $VERSION points at $tag_commit, not HEAD ($head_commit)." >&2
    echo "Releases are immutable: check out the tagged commit or choose a new version." >&2
    exit 1
  fi
fi

HOST_ASSET="$DIST/sila-$(go env GOOS)-$(go env GOARCH)"

# --- Prepare (tag + build + verify) ----------------------------------------
prepare_release() {
  if [[ "$SKIP_TESTS" == true || "${SILA_RELEASE_SKIP_TESTS:-0}" == "1" ]]; then
    echo "==> Skipping full test check for $VERSION (--skip-tests)"
  else
    echo "==> Testing $VERSION"
    go test -race ./...
  fi

  echo "==> Creating annotated tag $VERSION"
  git tag -a "$VERSION" -m "Release $VERSION"
  created_tag=true

  if [[ "$SKIP_BUILD" == "1" ]]; then
    echo "==> Reusing existing $DIST/ assets (SILA_RELEASE_SKIP_BUILD=1)"
  else
    echo "==> Building release assets"
    ./build.sh --all --release-artifact --no-install
  fi

  # Verification runs the host's own executable: a foreign-platform binary
  # cannot be executed, so a hardcoded linux/amd64 path made this step fail on
  # every other maintainer host.
  echo "==> Verifying with the host-native executable ($HOST_ASSET)"
  if [[ ! -x "$HOST_ASSET" ]]; then
    echo "Host-native asset $HOST_ASSET is missing; cannot verify this release on this host." >&2
    echo "build.sh --all builds every advertised target; build the host target before releasing." >&2
    exit 1
  fi
  "$HOST_ASSET" release check
  "$HOST_ASSET" release snapshot --out "$DIST/mcp-contract.json"
  "$HOST_ASSET" release metadata --out "$DIST/release-metadata.json"
  (
    cd "$DIST"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum sila-* mcp-contract.json release-metadata.json > checksums.txt
    else
      shasum -a 256 sila-* mcp-contract.json release-metadata.json > checksums.txt
    fi
  )
  echo "✔ Built release assets in $DIST/ for $VERSION"
}

# --- Publish (push and/or create the GitHub release) ------------------------
publish_release() {
  if [[ "$PUSH" != true ]]; then
    return 0
  fi
  if git ls-remote --tags origin "refs/tags/$VERSION" 2>/dev/null | grep -q "refs/tags/$VERSION"; then
    echo "==> Tag $VERSION already published; not pushing again"
  else
    echo "==> Pushing annotated tag $VERSION"
    git push origin "$VERSION"
    pushed_tag=true
  fi
  if [[ "$PUBLISH" == true ]]; then
    command -v gh >/dev/null 2>&1 || { echo "--publish requires GitHub CLI (gh)." >&2; exit 1; }
    if gh release view "$VERSION" >/dev/null 2>&1; then
      echo "==> GitHub Release $VERSION already exists; leaving it in place"
    else
      echo "==> Publishing GitHub Release $VERSION"
      gh release create "$VERSION" "$DIST"/sila-* "$DIST"/checksums.txt "$DIST"/mcp-contract.json "$DIST"/release-metadata.json --generate-notes
    fi
  fi
}

if [[ "$DRY_RUN" == true ]]; then
  if [[ "$SKIP_TESTS" == true || "${SILA_RELEASE_SKIP_TESTS:-0}" == "1" ]]; then
    echo "==> Skipping full test check for $VERSION (--skip-tests)"
  else
    echo "==> Testing $VERSION"
    go test -race ./...
  fi
  echo "✔ Dry run passed. The next real invocation will create annotated tag $VERSION."
  exit 0
fi

created_tag=false
pushed_tag=false
cleanup() {
  status=$?
  if [[ $status -ne 0 ]]; then
    if [[ "$pushed_tag" == true ]]; then
      # Never undo published identity: a pushed tag stays even if a later step
      # (such as creating the GitHub release) failed. Recovery is a re-run.
      echo "Release failed after the tag was pushed. Tag $VERSION is published and must not be moved." >&2
      echo "Fix the cause and re-run: scripts/release.sh $VERSION --publish" >&2
    elif [[ "$created_tag" == true ]]; then
      echo "Release failed before publishing; removing local tag $VERSION." >&2
      git tag -d "$VERSION" >/dev/null 2>&1 || true
    fi
  fi
  exit "$status"
}
trap cleanup EXIT

if [[ "$TAG_EXISTS" == true ]]; then
  echo "==> Tag $VERSION is already prepared; publishing the existing artifacts"
  if [[ ! -d "$DIST" ]]; then
    echo "$DIST/ is missing; re-run without --push to rebuild the release assets." >&2
    exit 1
  fi
else
  prepare_release
fi

publish_release

created_tag=false
trap - EXIT
echo "✔ Release $VERSION completed."
