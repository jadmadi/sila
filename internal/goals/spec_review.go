package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SpecReviewSchemaVersion defines the canonical schema version for spec reviews.
const SpecReviewSchemaVersion = "sila/spec-review/v1"

// Spec review verdicts.
const (
	VerdictReady  = "ready"
	VerdictRevise = "revise"
)

// ShortcutCounter represents a plausible incorrect implementation and the clause that excludes it.
type ShortcutCounter struct {
	IncorrectImplementation string   `json:"incorrect_implementation"`
	ExcludedByIDs           []string `json:"excluded_by_ids"`
	Resolution              string   `json:"resolution"`
}

// SpecReview records planner or verifier review against the exact specification digest.
// Reviewer identities are attestations, not cryptographic guarantees of independent identity.
type SpecReview struct {
	SchemaVersion      string            `json:"schema_version"`
	SpecDigest         string            `json:"spec_digest"`
	Reviewer           string            `json:"reviewer"`
	ReviewedAt         string            `json:"reviewed_at"`
	Verdict            string            `json:"verdict"`
	Shortcuts          []ShortcutCounter `json:"shortcuts"`
	UnresolvedBlockers []string          `json:"unresolved_blockers"`
}

// LoadSpecReview reads and decodes spec-review.json for the specified goal slug.
func LoadSpecReview(rootDir, slug string) (*SpecReview, error) {
	reviewPath := filepath.Join(rootDir, "goals", slug, "spec-review.json")
	data, err := os.ReadFile(reviewPath)
	if err != nil {
		return nil, err
	}

	var review SpecReview
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&review); err != nil {
		return nil, fmt.Errorf("decode spec-review.json: %w", err)
	}
	return &review, nil
}

// SaveSpecReview atomically writes spec-review.json for the given goal, archiving any previous review.
func SaveSpecReview(rootDir, slug string, review SpecReview) error {
	goalDir := filepath.Join(rootDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		return fmt.Errorf("create goal directory: %w", err)
	}

	reviewPath := filepath.Join(goalDir, "spec-review.json")
	if fileExists(reviewPath) {
		// Archive previous review into amendments/ before overwriting
		amendmentsDir := filepath.Join(goalDir, "amendments")
		_ = os.MkdirAll(amendmentsDir, 0755)
		timestamp := time.Now().UTC().Format("20060102T150405Z")
		archivePath := filepath.Join(amendmentsDir, fmt.Sprintf("spec-review-%s.json", timestamp))
		if oldData, err := os.ReadFile(reviewPath); err == nil {
			_ = AtomicWriteFile(archivePath, oldData, 0644)
		}
	}

	data, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal spec review: %w", err)
	}
	data = append(data, '\n')

	if err := AtomicWriteFile(reviewPath, data, 0644); err != nil {
		return fmt.Errorf("write spec-review.json: %w", err)
	}
	return nil
}

// ValidateSpecReview checks a SpecReview against a WorkPackage.
// It verifies schema version, reviewer attestation, matching package digest,
// resolved blockers, verdict validity, and at least 3 concrete shortcut counterexamples.
func ValidateSpecReview(pkg *WorkPackage, review SpecReview) []Diagnostic {
	var diags []Diagnostic
	goalSlug := ""
	if pkg != nil {
		goalSlug = pkg.GoalSlug
	}

	// 1. Schema version
	if review.SchemaVersion != SpecReviewSchemaVersion {
		diags = append(diags, Diagnostic{
			Code:        DiagSpecReviewMissing,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        "schema_version",
			Message:     fmt.Sprintf("unsupported or missing spec review schema version %q (expected %q)", review.SchemaVersion, SpecReviewSchemaVersion),
			Remediation: fmt.Sprintf("set schema_version to %q", SpecReviewSchemaVersion),
		})
	}

	// 2. Reviewer attestation
	if strings.TrimSpace(review.Reviewer) == "" {
		diags = append(diags, Diagnostic{
			Code:        DiagSpecReviewMissing,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        "reviewer",
			Message:     "spec review reviewer attestation cannot be empty",
			Remediation: "specify reviewer identity or agent attestation",
		})
	}

	// 3. ReviewedAt timestamp
	if strings.TrimSpace(review.ReviewedAt) == "" {
		diags = append(diags, Diagnostic{
			Code:        DiagSpecReviewMissing,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        "reviewed_at",
			Message:     "spec review reviewed_at timestamp cannot be empty",
			Remediation: "provide RFC3339 review timestamp",
		})
	}

	// 4. Digest matching
	if pkg == nil {
		diags = append(diags, Diagnostic{
			Code:        DiagSpecNotReady,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        "spec_digest",
			Message:     "cannot validate spec review against nil work package",
			Remediation: "provide a valid work package",
		})
	} else {
		expectedDigest, err := WorkPackageDigest(pkg)
		if err != nil {
			diags = append(diags, Diagnostic{
				Code:        DiagSpecNotReady,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        "spec_digest",
				Message:     fmt.Sprintf("failed to compute work package digest: %v", err),
				Remediation: "resolve work package format or structure issues",
			})
		} else if strings.ToLower(strings.TrimSpace(review.SpecDigest)) != strings.ToLower(expectedDigest) {
			diags = append(diags, Diagnostic{
				Code:        DiagSpecReviewStale,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        "spec_digest",
				Message:     fmt.Sprintf("spec review digest %q does not match current package digest %q", review.SpecDigest, expectedDigest),
				Remediation: "re-run planner review against updated package digest",
			})
		}
	}

	// 5. Verdict
	switch review.Verdict {
	case VerdictReady:
		// Acceptable if all other criteria pass
	case VerdictRevise:
		diags = append(diags, Diagnostic{
			Code:        DiagSpecNotReady,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        "verdict",
			Message:     "spec review verdict is 'revise'; goal is blocked until requirements are revised and marked ready",
			Remediation: "address reviewer findings and record verdict 'ready'",
		})
	default:
		diags = append(diags, Diagnostic{
			Code:        DiagSpecNotReady,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        "verdict",
			Message:     fmt.Sprintf("invalid spec review verdict %q (must be 'ready' or 'revise')", review.Verdict),
			Remediation: "set verdict to 'ready' or 'revise'",
		})
	}

	// 6. Unresolved blockers
	for _, blocker := range review.UnresolvedBlockers {
		if strings.TrimSpace(blocker) != "" {
			diags = append(diags, Diagnostic{
				Code:        DiagSpecNotReady,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        "unresolved_blockers",
				Message:     fmt.Sprintf("unresolved reviewer blocker %q prevents readiness", blocker),
				Remediation: "resolve reviewer blocker before claiming implementation",
			})
		}
	}

	// 7. Shortcuts counterexamples
	if len(review.Shortcuts) < 3 {
		diags = append(diags, Diagnostic{
			Code:        DiagSpecNotReady,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        "shortcuts",
			Message:     fmt.Sprintf("spec review requires at least 3 concrete shortcut counterexamples, got %d", len(review.Shortcuts)),
			Remediation: "document at least 3 plausible incorrect implementations and their excluding clauses",
		})
	}

	for idx, sc := range review.Shortcuts {
		if strings.TrimSpace(sc.IncorrectImplementation) == "" {
			diags = append(diags, Diagnostic{
				Code:        DiagSpecNotReady,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        fmt.Sprintf("shortcuts[%d].incorrect_implementation", idx),
				Message:     fmt.Sprintf("shortcut %d missing incorrect_implementation description", idx),
				Remediation: "describe plausible shortcut or incorrect implementation",
			})
		}
		if len(sc.ExcludedByIDs) == 0 {
			diags = append(diags, Diagnostic{
				Code:        DiagSpecNotReady,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        fmt.Sprintf("shortcuts[%d].excluded_by_ids", idx),
				Message:     fmt.Sprintf("shortcut %d must cite at least one excluding task or acceptance ID", idx),
				Remediation: "cite specific task or acceptance case IDs excluding the shortcut",
			})
		}
		if strings.TrimSpace(sc.Resolution) == "" {
			diags = append(diags, Diagnostic{
				Code:        DiagSpecNotReady,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        fmt.Sprintf("shortcuts[%d].resolution", idx),
				Message:     fmt.Sprintf("shortcut %d missing resolution explanation", idx),
				Remediation: "explain how contract clauses prevent this shortcut",
			})
		}
	}

	// Deterministic sorting
	sort.SliceStable(diags, func(i, j int) bool {
		if diags[i].Path != diags[j].Path {
			return diags[i].Path < diags[j].Path
		}
		if diags[i].Code != diags[j].Code {
			return diags[i].Code < diags[j].Code
		}
		return diags[i].Message < diags[j].Message
	})

	return diags
}
