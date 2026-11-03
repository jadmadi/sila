package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseWorkflowRehearsal exercises the documented prepare → inspect →
// publish sequence against a local bare remote. It reproduces the re-eval F12
// defect: the guide's second invocation ("run the same script with --push")
// exited with "Tag already exists; releases are immutable", so the documented
// sequence could not complete.
//
// The build is stubbed (SILA_RELEASE_SKIP_BUILD=1 with pre-seeded dist assets)
// because a cross-platform release build has no place in a unit test; the state
// machine — identity, tag handling, publishing, retry, and failure recovery —
// is the part that was broken.
func TestReleaseWorkflowRehearsal(t *testing.T) {
	script := findReleaseScript(t)
	repo, remote := newRehearsalRepo(t, script)

	// --- prepare: tag + verify, no network. ------------------------------
	out := runRelease(t, repo, nil, "v0.6.0")
	if !strings.Contains(out, "Creating annotated tag v0.6.0") {
		t.Fatalf("prepare did not create the tag:\n%s", out)
	}
	if !strings.Contains(out, "Verifying with the host-native executable") {
		t.Fatalf("prepare did not verify with the host asset:\n%s", out)
	}
	if tagType(t, repo, "v0.6.0") != "tag" {
		t.Fatal("prepare must create an annotated tag")
	}
	if remoteHasTag(t, repo, "v0.6.0") {
		t.Fatal("prepare must not push")
	}

	// --- publish the inspected artifacts (the sequence that used to fail) -
	out = runRelease(t, repo, nil, "v0.6.0", "--push")
	if strings.Contains(out, "already exists") {
		t.Fatalf("publishing an inspected release must not fail on the existing tag:\n%s", out)
	}
	if !remoteHasTag(t, repo, "v0.6.0") {
		t.Fatalf("publish did not push the tag:\n%s", out)
	}

	// --- retry is idempotent: the tag is already published ---------------
	out = runRelease(t, repo, nil, "v0.6.0", "--push")
	if !strings.Contains(out, "already published") {
		t.Fatalf("a repeated publish should be a no-op, got:\n%s", out)
	}
	if tagType(t, repo, "v0.6.0") != "tag" {
		t.Fatal("a repeated publish must not recreate the tag")
	}
	_ = remote
}

// TestReleaseRehearsalRejectsBadIdentity covers the identity rules the
// evaluation asked to be tested deliberately: lightweight tags, pseudo-versions,
// and a tag that does not point at HEAD.
func TestReleaseRehearsalRejectsBadIdentity(t *testing.T) {
	script := findReleaseScript(t)

	t.Run("pseudo-version", func(t *testing.T) {
		repo, _ := newRehearsalRepo(t, script)
		out, err := tryRelease(t, repo, nil, "v0.0.0-20260101120000-abcdef123456")
		if err == nil {
			t.Fatalf("a Go pseudo-version must not be accepted as a release:\n%s", out)
		}
		if !strings.Contains(out, "pseudo-version") {
			t.Fatalf("the error should name the reason, got:\n%s", out)
		}
	})

	t.Run("lightweight tag", func(t *testing.T) {
		repo, _ := newRehearsalRepo(t, script)
		gitRun(t, repo, "tag", "v0.6.0") // lightweight on purpose
		out, err := tryRelease(t, repo, nil, "v0.6.0")
		if err == nil {
			t.Fatalf("a lightweight tag must be rejected:\n%s", out)
		}
		if !strings.Contains(out, "lightweight") {
			t.Fatalf("the error should explain the annotation requirement, got:\n%s", out)
		}
	})

	t.Run("tag not at HEAD", func(t *testing.T) {
		repo, _ := newRehearsalRepo(t, script)
		gitRun(t, repo, "tag", "-a", "v0.6.0", "-m", "Release v0.6.0")
		// Advance HEAD so the tag no longer identifies the released commit.
		if err := os.WriteFile(filepath.Join(repo, "later.txt"), []byte("later\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, repo, "add", "-A")
		gitRun(t, repo, "commit", "-qm", "later work")
		out, err := tryRelease(t, repo, nil, "v0.6.0", "--push")
		if err == nil {
			t.Fatalf("a tag that does not point at HEAD must be rejected:\n%s", out)
		}
		if !strings.Contains(out, "immutable") {
			t.Fatalf("the error should state that releases are immutable, got:\n%s", out)
		}
	})

	t.Run("dirty worktree", func(t *testing.T) {
		repo, _ := newRehearsalRepo(t, script)
		if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := tryRelease(t, repo, nil, "v0.6.0")
		if err == nil {
			t.Fatalf("a dirty worktree must be rejected:\n%s", out)
		}
		if !strings.Contains(out, "clean worktree") {
			t.Fatalf("unexpected error:\n%s", out)
		}
	})
}

// TestReleaseRehearsalFailureAfterPublishKeepsTag pins the recovery contract:
// a failure after the tag was pushed must never delete the local tag, because
// the identity is already public.
func TestReleaseRehearsalFailureAfterPublishKeepsTag(t *testing.T) {
	script := findReleaseScript(t)
	repo, _ := newRehearsalRepo(t, script)

	if out := runRelease(t, repo, nil, "v0.6.0"); !strings.Contains(out, "Creating annotated tag") {
		t.Fatalf("prepare failed:\n%s", out)
	}

	// Publish with gh absent and --publish: the tag is pushed, then the GitHub
	// step fails because the CLI is unavailable.
	out, err := tryRelease(t, repo, nil, "v0.6.0", "--publish")
	if err == nil {
		t.Fatal("publishing must fail when gh is unavailable")
	}
	if !strings.Contains(out, "must not be moved") {
		t.Fatalf("a post-push failure must warn against moving the tag, got:\n%s", out)
	}
	if tagType(t, repo, "v0.6.0") != "tag" {
		t.Fatal("a published tag was deleted by failure cleanup")
	}
	if !remoteHasTag(t, repo, "v0.6.0") {
		t.Fatal("the tag should still be published on the remote")
	}
}

// --- rehearsal harness ------------------------------------------------------

// findReleaseScript locates scripts/release.sh from the package directory.
func findReleaseScript(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for dir := wd; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "scripts", "release.sh")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Skip("scripts/release.sh not found from this working directory")
	return ""
}

// newRehearsalRepo builds a disposable repository with a local bare remote, the
// release script, a changelog carrying the release section, and pre-seeded dist
// assets for the host platform.
func newRehearsalRepo(t *testing.T, script string) (repo, remote string) {
	t.Helper()
	root := t.TempDir()
	repo = filepath.Join(root, "work")
	remote = filepath.Join(root, "origin.git")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}

	// The script under test, byte for byte.
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(repo, "scripts", "release.sh")
	if err := os.WriteFile(scriptPath, data, 0o755); err != nil {
		t.Fatal(err)
	}

	// A changelog with the section the script requires, and a minimal Go module
	// so `go test ./...` has something to run. The rehearsal never needs the
	// real module graph.
	write(t, filepath.Join(repo, "CHANGELOG.md"), "# Changelog\n\n## [0.6.0]\n\n### Added\n- rehearsal\n")
	write(t, filepath.Join(repo, "go.mod"), "module rehearsal\n\ngo 1.21\n")
	// dist/ is ignored in the real repository; the workflow writes artifacts
	// there, so the rehearsal repo must ignore it too or the second invocation
	// would see a dirty worktree.
	write(t, filepath.Join(repo, ".gitignore"), "/dist/\n")

	// Pre-seeded host-native asset: a tiny program that answers the release
	// subcommands the script calls, so verification is exercised for real.
	dist := filepath.Join(repo, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(dist, "sila-"+hostGOOS(t)+"-"+hostGOARCH(t))
	write(t, asset, fakeAssetScript)
	if err := os.Chmod(asset, 0o755); err != nil {
		t.Fatal(err)
	}
	// A second platform asset, so checksums cover more than one file.
	write(t, filepath.Join(dist, "sila-linux-arm64"), "placeholder\n")

	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "config", "user.email", "release@example.com")
	gitRun(t, repo, "config", "user.name", "release rehearsal")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "rehearsal baseline")
	gitRun(t, remote, "init", "-q", "--bare")
	gitRun(t, repo, "remote", "add", "origin", remote)
	return repo, remote
}

// fakeAssetScript stands in for the release binary: it implements just the
// subcommands the workflow calls, writing the artifacts the script expects.
const fakeAssetScript = `#!/bin/sh
case "$1 $2" in
  "release check") echo "release preflight ok (rehearsal)"; exit 0 ;;
  "release snapshot"|"release metadata")
    out=""
    prev=""
    for a in "$@"; do
      if [ "$prev" = "--out" ]; then out="$a"; fi
      prev="$a"
    done
    [ -n "$out" ] && printf '{"rehearsal":true}\n' > "$out"
    echo "$2 written"
    exit 0 ;;
esac
echo "rehearsal asset: unsupported $*" >&2
exit 1
`

// releaseEnv is the environment the script runs with: the rehearsal hooks plus
// a stub `go` so the workflow's test step is cheap.
func releaseEnv(t *testing.T, repo string, extra []string) []string {
	t.Helper()
	bin := filepath.Join(filepath.Dir(repo), "stub-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	goStub := filepath.Join(bin, "go")
	write(t, goStub, `#!/bin/sh
# Rehearsal stub: the workflow only needs GOOS/GOARCH lookups and a passing test run.
if [ "$1" = "env" ]; then
  case "$2" in
    GOOS) echo "${REHEARSAL_GOOS:-linux}" ;;
    GOARCH) echo "${REHEARSAL_GOARCH:-amd64}" ;;
  esac
  exit 0
fi
if [ "$1" = "test" ]; then echo "ok (rehearsal)"; exit 0; fi
exec /usr/bin/env go "$@"
`)
	if err := os.Chmod(goStub, 0o755); err != nil {
		t.Fatal(err)
	}
	ghStub := filepath.Join(bin, "gh")
	write(t, ghStub, "#!/bin/sh\necho \"rehearsal: gh is disabled\" >&2\nexit 1\n")
	if err := os.Chmod(ghStub, 0o755); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(),
		"SILA_RELEASE_ROOT="+repo,
		"SILA_RELEASE_DIST=dist",
		"SILA_RELEASE_SKIP_BUILD=1",
		"REHEARSAL_GOOS="+hostGOOS(t),
		"REHEARSAL_GOARCH="+hostGOARCH(t),
		"PATH="+bin+":/usr/bin:/bin",
	)
	return append(env, extra...)
}

func runRelease(t *testing.T, repo string, extra []string, args ...string) string {
	t.Helper()
	out, err := tryRelease(t, repo, extra, args...)
	if err != nil {
		t.Fatalf("release.sh %v failed: %v\n%s", args, err, out)
	}
	return out
}

func tryRelease(t *testing.T, repo string, extra []string, args ...string) (string, error) {
	t.Helper()
	script := filepath.Join(repo, "scripts", "release.sh")
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Dir = repo
	cmd.Env = releaseEnv(t, repo, extra)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func tagType(t *testing.T, repo, tag string) string {
	t.Helper()
	cmd := exec.Command("git", "cat-file", "-t", tag)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return "missing"
	}
	return strings.TrimSpace(string(out))
}

func remoteHasTag(t *testing.T, repo, tag string) bool {
	t.Helper()
	cmd := exec.Command("git", "ls-remote", "--tags", "origin", "refs/tags/"+tag)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "refs/tags/"+tag)
}

func hostGOOS(t *testing.T) string {
	t.Helper()
	return hostValue(t, "GOOS")
}

func hostGOARCH(t *testing.T) string {
	t.Helper()
	return hostValue(t, "GOARCH")
}

func hostValue(t *testing.T, key string) string {
	t.Helper()
	cmd := exec.Command("go", "env", key)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go env %s: %v", key, err)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
