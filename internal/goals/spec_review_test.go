package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validSpecReviewFixture(pkg *WorkPackage) SpecReview {
	digest, _ := WorkPackageDigest(pkg)
	return SpecReview{
		SchemaVersion: SpecReviewSchemaVersion,
		SpecDigest:    digest,
		Reviewer:      "strong-planner-agent",
		ReviewedAt:    time.Now().UTC().Format(time.RFC3339),
		Verdict:       VerdictReady,
		Shortcuts: []ShortcutCounter{
			{
				IncorrectImplementation: "implement retry by infinite loop",
				ExcludedByIDs:           []string{"T-001", "AC-001"},
				Resolution:              "T-001 step 3 caps retry attempts to 2 and exits with failure",
			},
			{
				IncorrectImplementation: "swallow disk I/O errors silently",
				ExcludedByIDs:           []string{"T-001"},
				Resolution:              "T-001 step 2 explicitly requires returning unwrapped os.PathError",
			},
			{
				IncorrectImplementation: "bypass boundary checks with path prefix matching",
				ExcludedByIDs:           []string{"AC-001"},
				Resolution:              "AC-001 specifies filepath.Rel check against repository root",
			},
		},
		UnresolvedBlockers: []string{},
	}
}

// TestSpecReviewValidation tests validation of spec reviews against work packages.
func TestSpecReviewValidation(t *testing.T) {
	_, pkg := setupValidReadinessFixture(t)
	review := validSpecReviewFixture(pkg)

	// AC-004: ready with matching digest and resolved counterexamples validates
	diags := ValidateSpecReview(pkg, review)
	if len(diags) != 0 {
		t.Fatalf("expected valid spec review to produce 0 diagnostics, got %d: %+v", len(diags), diags)
	}

	// AC-004: verdict 'revise' remains blocked
	reviseReview := review
	reviseReview.Verdict = VerdictRevise
	diags = ValidateSpecReview(pkg, reviseReview)
	foundRevise := false
	for _, d := range diags {
		if d.Code == DiagSpecNotReady && strings.Contains(d.Message, "revise") {
			foundRevise = true
			break
		}
	}
	if !foundRevise {
		t.Errorf("expected diagnostic for verdict revise, got: %+v", diags)
	}

	// AC-005: Empty reviewer fails
	emptyReviewer := review
	emptyReviewer.Reviewer = ""
	diags = ValidateSpecReview(pkg, emptyReviewer)
	foundReviewer := false
	for _, d := range diags {
		if d.Code == DiagSpecReviewMissing && strings.Contains(d.Path, "reviewer") {
			foundReviewer = true
			break
		}
	}
	if !foundReviewer {
		t.Errorf("expected SPEC_REVIEW_MISSING for empty reviewer, got: %+v", diags)
	}

	// AC-005: Fewer than 3 shortcuts fails
	fewShortcuts := review
	fewShortcuts.Shortcuts = fewShortcuts.Shortcuts[:2]
	diags = ValidateSpecReview(pkg, fewShortcuts)
	foundShortcuts := false
	for _, d := range diags {
		if d.Code == DiagSpecNotReady && strings.Contains(d.Path, "shortcuts") {
			foundShortcuts = true
			break
		}
	}
	if !foundShortcuts {
		t.Errorf("expected diagnostic for fewer than 3 shortcuts, got: %+v", diags)
	}

	// AC-005: Unresolved blockers prevent readiness
	blockedReview := review
	blockedReview.UnresolvedBlockers = []string{"Awaiting API contract finalization"}
	diags = ValidateSpecReview(pkg, blockedReview)
	foundBlocker := false
	for _, d := range diags {
		if d.Code == DiagSpecNotReady && strings.Contains(d.Path, "unresolved_blockers") {
			foundBlocker = true
			break
		}
	}
	if !foundBlocker {
		t.Errorf("expected diagnostic for unresolved blocker, got: %+v", diags)
	}

	// AC-006: Review records its attestation nature; empty reviewer cannot masquerade as valid
	blankAttestation := review
	blankAttestation.Reviewer = "   "
	diags = ValidateSpecReview(pkg, blankAttestation)
	if len(diags) == 0 {
		t.Errorf("expected validation failure for whitespace-only reviewer attestation")
	}
}

// TestSpecReviewStaleness tests digest invalidation and archiving during SaveSpecReview.
func TestSpecReviewStaleness(t *testing.T) {
	tmpDir, pkg := setupValidReadinessFixture(t)
	slug := "staleness-test-goal"
	pkg.GoalSlug = slug
	review := validSpecReviewFixture(pkg)

	// Save review
	if err := SaveSpecReview(tmpDir, slug, review); err != nil {
		t.Fatalf("SaveSpecReview: %v", err)
	}

	// Verify review was written
	loaded, err := LoadSpecReview(tmpDir, slug)
	if err != nil {
		t.Fatalf("LoadSpecReview: %v", err)
	}
	if loaded.SpecDigest != review.SpecDigest {
		t.Errorf("loaded digest mismatch: %q vs %q", loaded.SpecDigest, review.SpecDigest)
	}

	// Mutate work package: changing purpose alters package digest
	mutatedPkg := cloneWorkPackage(pkg)
	mutatedPkg.Purpose = "Changed purpose for testing staleness"
	diags := ValidateSpecReview(mutatedPkg, review)
	foundStale := false
	for _, d := range diags {
		if d.Code == DiagSpecReviewStale {
			foundStale = true
			break
		}
	}
	if !foundStale {
		t.Errorf("expected SPEC_REVIEW_STALE after mutating package purpose, got: %+v", diags)
	}

	// Overwriting review creates an archive in amendments/
	updatedReview := review
	updatedReview.ReviewedAt = time.Now().UTC().Add(time.Second).Format(time.RFC3339)
	if err := SaveSpecReview(tmpDir, slug, updatedReview); err != nil {
		t.Fatalf("second SaveSpecReview: %v", err)
	}

	amendmentsDir := filepath.Join(tmpDir, "goals", slug, "amendments")
	entries, err := os.ReadDir(amendmentsDir)
	if err != nil || len(entries) == 0 {
		t.Errorf("expected archived review in amendments directory, got entries=%v, err=%v", entries, err)
	}
}
