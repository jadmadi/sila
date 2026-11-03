package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Sila Tester")
	run("config", "user.email", "tester@sila.local")

	// Create initial file
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "main.go")
	run("commit", "-m", "initial commit")

	return dir
}

func TestTaskCandidateIdentity(t *testing.T) {
	dir := initTestGitRepo(t)
	slug := "test-goal"

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	// 1. Initial candidate capture
	cand1, err := CaptureTaskCandidate(dir, slug)
	if err != nil {
		t.Fatalf("initial CaptureTaskCandidate: %v", err)
	}
	if cand1.Commit == "" || cand1.ContentDigest == "" {
		t.Errorf("expected non-empty commit and content digest, got: %+v", cand1)
	}

	// 2. AC-004: Same content across two commits has same content digest with different commit SHA
	run("commit", "--allow-empty", "-m", "second commit with same content")
	cand2, err := CaptureTaskCandidate(dir, slug)
	if err != nil {
		t.Fatalf("second CaptureTaskCandidate: %v", err)
	}
	if cand2.Commit == cand1.Commit {
		t.Errorf("expected different commit SHAs across two commits")
	}
	if cand2.ContentDigest != cand1.ContentDigest {
		t.Errorf("AC-004 failed: expected identical content digest across same-content commits, got %q vs %q", cand1.ContentDigest, cand2.ContentDigest)
	}

	// 3. AC-006: Writing receipt under allowed evidence root does not make candidate stale or dirty
	evidenceDir := filepath.Join(dir, "goals", slug, "evidence", "T-001")
	if err := os.MkdirAll(evidenceDir, 0755); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(evidenceDir, "receipt.txt")
	if err := os.WriteFile(receiptPath, []byte("run receipt output\n"), 0644); err != nil {
		t.Fatal(err)
	}

	candWithEvidence, err := CaptureTaskCandidate(dir, slug)
	if err != nil {
		t.Fatalf("AC-006 failed: CaptureTaskCandidate failed with evidence file present: %v", err)
	}
	if candWithEvidence.ContentDigest != cand1.ContentDigest {
		t.Errorf("AC-006 failed: evidence file changed content digest: %q vs %q", candWithEvidence.ContentDigest, cand1.ContentDigest)
	}

	// 4. AC-006: Dirty source outside evidence root is rejected
	dirtySrc := filepath.Join(dir, "feature.go")
	if err := os.WriteFile(dirtySrc, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, errDirty := CaptureTaskCandidate(dir, slug)
	if errDirty == nil {
		t.Fatalf("AC-006 failed: expected error for untracked dirty source file feature.go")
	}
	if !strings.Contains(errDirty.Error(), "dirty source detected") {
		t.Errorf("unexpected error message for dirty source: %v", errDirty)
	}

	// Clean dirty file
	_ = os.Remove(dirtySrc)

	// 5. AC-004: Source edits change the content digest
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() { println(\"updated\") }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "main.go")
	run("commit", "-m", "update main.go source")

	cand3, err := CaptureTaskCandidate(dir, slug)
	if err != nil {
		t.Fatalf("CaptureTaskCandidate after source edit: %v", err)
	}
	if cand3.ContentDigest == cand1.ContentDigest {
		t.Errorf("AC-004 failed: expected content digest to change after source modification")
	}
}

func TestEvidenceArtifactBoundary(t *testing.T) {
	dir := initTestGitRepo(t)
	slug := "artifact-goal"

	evidenceDir := filepath.Join(dir, "goals", slug, "evidence", "T-001")
	if err := os.MkdirAll(evidenceDir, 0755); err != nil {
		t.Fatal(err)
	}

	artFile := filepath.Join(evidenceDir, "out.log")
	artData := []byte("clean execution artifact data\n")
	if err := os.WriteFile(artFile, artData, 0644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(artData)
	validSHA := hex.EncodeToString(h[:])

	// 1. Valid artifact passes with 0 diagnostics
	evValid := &TaskEvidence{
		GoalSlug: slug,
		Artifacts: []EvidenceArtifact{
			{Path: "T-001/out.log", SHA256: validSHA},
		},
	}
	diags := ValidateEvidenceArtifacts(dir, slug, evValid)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics for valid artifact, got: %+v", diags)
	}

	// 2. AC-005: Missing artifact fails validation
	evMissing := &TaskEvidence{
		GoalSlug: slug,
		Artifacts: []EvidenceArtifact{
			{Path: "T-001/missing.log", SHA256: validSHA},
		},
	}
	diagsMissing := ValidateEvidenceArtifacts(dir, slug, evMissing)
	foundMissing := false
	for _, d := range diagsMissing {
		if d.Code == DiagEvidenceArtifactMissing {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Errorf("expected DiagEvidenceArtifactMissing for missing file, got: %+v", diagsMissing)
	}

	// 3. AC-005: Changed artifact bytes fail validation with digest mismatch
	evDigestMismatch := &TaskEvidence{
		GoalSlug: slug,
		Artifacts: []EvidenceArtifact{
			{Path: "T-001/out.log", SHA256: strings.Repeat("f", 64)},
		},
	}
	diagsMismatch := ValidateEvidenceArtifacts(dir, slug, evDigestMismatch)
	foundMismatch := false
	for _, d := range diagsMismatch {
		if d.Code == DiagEvidenceArtifactDigestMismatch {
			foundMismatch = true
			break
		}
	}
	if !foundMismatch {
		t.Errorf("expected DiagEvidenceArtifactDigestMismatch for changed bytes, got: %+v", diagsMismatch)
	}

	// 4. AC-005: Paths outside evidence root fail validation
	evPathEscape := &TaskEvidence{
		GoalSlug: slug,
		Artifacts: []EvidenceArtifact{
			{Path: "../../main.go", SHA256: validSHA},
		},
	}
	diagsEscape := ValidateEvidenceArtifacts(dir, slug, evPathEscape)
	foundEscape := false
	for _, d := range diagsEscape {
		if d.Code == DiagEvidenceArtifactBoundaryEscape {
			foundEscape = true
			break
		}
	}
	if !foundEscape {
		t.Errorf("expected DiagEvidenceArtifactBoundaryEscape for path traversal, got: %+v", diagsEscape)
	}

	// 5. AC-005: Symlink escape outside evidence directory fails validation
	symlinkPath := filepath.Join(evidenceDir, "escape.link")
	targetOutside := filepath.Join(dir, "main.go")
	if err := os.Symlink(targetOutside, symlinkPath); err == nil {
		evSymlinkEscape := &TaskEvidence{
			GoalSlug: slug,
			Artifacts: []EvidenceArtifact{
				{Path: "T-001/escape.link", SHA256: validSHA},
			},
		}
		diagsSymlink := ValidateEvidenceArtifacts(dir, slug, evSymlinkEscape)
		foundSymlinkEscape := false
		for _, d := range diagsSymlink {
			if d.Code == DiagEvidenceArtifactSymlinkEscape {
				foundSymlinkEscape = true
				break
			}
		}
		if !foundSymlinkEscape {
			t.Errorf("expected DiagEvidenceArtifactSymlinkEscape for symlink to outside file, got: %+v", diagsSymlink)
		}
	}
}
