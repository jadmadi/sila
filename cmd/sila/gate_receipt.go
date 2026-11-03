package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	gateReceiptMaxAge            = 24 * time.Hour
	gateReceiptMaxUntrackedBytes = 10 << 20 // 10 MiB
	gateReceiptGitTimeout        = 5 * time.Second
)

// gateGreenReceipt records a verified test-suite pass bound to an exact
// working-tree Merkle state and command string.
type gateGreenReceipt struct {
	TreeHash  string `json:"tree_hash"`
	HeadSHA   string `json:"head_sha"`
	Command   string `json:"command"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

// checkGateGreenReceipt returns a passing gateCheck with a visible GATE_SKIPPED
// detail when an unexpired receipt matches the current working-tree state and
// command, and any required sidecar artifact (such as a coverage profile) still
// exists on disk. Any error or mismatch fails closed and returns (_, false).
func checkGateGreenReceipt(dir, cmdKey, requiredArtifact string) (gateCheck, bool) {
	if os.Getenv("SILA_NO_GATE_CACHE") == "1" || os.Getenv("SILA_GATE_NO_CACHE") == "1" {
		return gateCheck{}, false
	}
	if strings.TrimSpace(cmdKey) == "" {
		return gateCheck{}, false
	}
	if requiredArtifact != "" {
		fi, err := os.Stat(requiredArtifact)
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			return gateCheck{}, false
		}
	}

	treeHash, _, gitDir, err := gateTreeStateHash(dir, cmdKey)
	if err != nil || len(treeHash) < 16 || gitDir == "" {
		return gateCheck{}, false
	}

	receiptPath := filepath.Join(gitDir, "sila-gate-receipts", treeHash[:16]+".json")
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		return gateCheck{}, false
	}

	var rec gateGreenReceipt
	if err := json.Unmarshal(raw, &rec); err != nil {
		return gateCheck{}, false
	}
	if rec.TreeHash != treeHash || rec.Command != cmdKey || rec.Detail == "" {
		return gateCheck{}, false
	}
	createdAt, err := time.Parse(time.RFC3339, rec.CreatedAt)
	if err != nil || time.Since(createdAt) > gateReceiptMaxAge {
		return gateCheck{}, false
	}

	return gateCheck{
		Name:   "tests",
		Status: "pass",
		Detail: fmt.Sprintf("GATE_SKIPPED: %s (cached green receipt %s)", rec.Detail, rec.TreeHash[:12]),
	}, true
}

// saveGateGreenReceipt persists a verified test-suite receipt under
// <git-dir>/sila-gate-receipts/ with mode 0600. Errors are silently ignored so
// cache writes never fail a passing gate.
func saveGateGreenReceipt(dir, cmdKey, detail string) {
	if os.Getenv("SILA_NO_GATE_CACHE") == "1" || os.Getenv("SILA_GATE_NO_CACHE") == "1" {
		return
	}
	if strings.TrimSpace(cmdKey) == "" || strings.TrimSpace(detail) == "" {
		return
	}
	treeHash, headSHA, gitDir, err := gateTreeStateHash(dir, cmdKey)
	if err != nil || len(treeHash) < 16 || gitDir == "" {
		return
	}

	receiptDir := filepath.Join(gitDir, "sila-gate-receipts")
	if err := os.MkdirAll(receiptDir, 0o700); err != nil {
		return
	}

	rec := gateGreenReceipt{
		TreeHash:  treeHash,
		HeadSHA:   headSHA,
		Command:   cmdKey,
		Detail:    detail,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	payload, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}

	receiptPath := filepath.Join(receiptDir, treeHash[:16]+".json")
	_ = os.WriteFile(receiptPath, payload, 0o600)
}

// gateTreeStateHash computes a deterministic SHA-256 digest of the command,
// HEAD's root tree object, staged diff, unstaged diff, and all non-ignored
// untracked files in dir.
func gateTreeStateHash(dir, cmdKey string) (treeHash, headSHA, gitDir string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), gateReceiptGitTimeout)
	defer cancel()

	if dir == "" {
		dir, err = os.Getwd()
		if err != nil {
			return "", "", "", fmt.Errorf("getwd: %w", err)
		}
	}

	gitDirOut, err := runGitBytes(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", "", "", fmt.Errorf("resolve git dir: %w", err)
	}
	gitDir = strings.TrimSpace(string(gitDirOut))
	if gitDir == "" {
		return "", "", "", fmt.Errorf("empty git dir")
	}

	headOut, err := runGitBytes(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("resolve HEAD: %w", err)
	}
	headSHA = strings.TrimSpace(string(headOut))

	treeOut, err := runGitBytes(ctx, dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", "", "", fmt.Errorf("resolve HEAD tree: %w", err)
	}
	headTree := strings.TrimSpace(string(treeOut))

	stagedDiff, err := runGitBytes(ctx, dir, "diff", "--cached", "--binary", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("diff cached: %w", err)
	}

	workDiff, err := runGitBytes(ctx, dir, "diff", "--binary")
	if err != nil {
		return "", "", "", fmt.Errorf("diff worktree: %w", err)
	}

	untrackedOut, err := runGitBytes(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", "", "", fmt.Errorf("list untracked: %w", err)
	}

	h := sha256.New()
	_, _ = fmt.Fprintf(h, "cmd:%s\ntree:%s\n", cmdKey, headTree)
	_, _ = fmt.Fprintf(h, "staged:%d\n", len(stagedDiff))
	_, _ = h.Write(stagedDiff)
	_, _ = fmt.Fprintf(h, "\nwork:%d\n", len(workDiff))
	_, _ = h.Write(workDiff)

	if len(untrackedOut) > 0 {
		rawPaths := bytes.Split(untrackedOut, []byte{0})
		paths := make([]string, 0, len(rawPaths))
		for _, p := range rawPaths {
			if len(p) > 0 {
				paths = append(paths, string(p))
			}
		}
		sort.Strings(paths)
		for _, rel := range paths {
			full := filepath.Join(dir, rel)
			fi, statErr := os.Lstat(full)
			if statErr != nil {
				return "", "", "", fmt.Errorf("lstat untracked %s: %w", rel, statErr)
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				target, linkErr := os.Readlink(full)
				if linkErr != nil {
					return "", "", "", fmt.Errorf("readlink untracked %s: %w", rel, linkErr)
				}
				_, _ = fmt.Fprintf(h, "\nsymlink:%s->%s\n", rel, target)
				continue
			}
			if !fi.Mode().IsRegular() || fi.Size() > gateReceiptMaxUntrackedBytes {
				return "", "", "", fmt.Errorf("untracked file %s not cacheable", rel)
			}
			content, readErr := os.ReadFile(full)
			if readErr != nil {
				return "", "", "", fmt.Errorf("read untracked %s: %w", rel, readErr)
			}
			sum := sha256.Sum256(content)
			_, _ = fmt.Fprintf(h, "\nuntracked:%s:%x\n", rel, sum)
		}
	}

	return hex.EncodeToString(h.Sum(nil)), headSHA, gitDir, nil
}

func runGitBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Output()
}
