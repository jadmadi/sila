package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

var slugCleanRE = regexp.MustCompile(`[^a-z0-9]+`)

func toSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugCleanRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
		s = strings.Trim(s, "-")
	}
	if s == "" {
		s = "synthesized-case"
	}
	return s
}

// InitCaseTemplate scaffolds a fresh test case directory with starter templates.
func InitCaseTemplate(outDir, caseName string, bare bool) (string, error) {
	slug := toSlug(caseName)
	targetDir := filepath.Join(outDir, slug)
	if err := os.MkdirAll(filepath.Join(targetDir, "graders"), 0755); err != nil {
		return "", fmt.Errorf("create case directory %s: %w", targetDir, err)
	}

	promptContent := fmt.Sprintf(`---
name: %s
max_turns: 5
tags: ["general"]
model: claude-3-7-sonnet
---

Describe the task or question that tests agent capability here.
`, slug)

	if err := os.WriteFile(filepath.Join(targetDir, "prompt.md"), []byte(promptContent), 0644); err != nil {
		return "", err
	}

	if bare {
		return targetDir, nil
	}

	caseYamlContent := `context:
  # scaffold_script: "bash setup.sh"
  # history_file: "conversation.jsonl"
  # rules: ["agents/AGENTS.md"]
`
	_ = os.WriteFile(filepath.Join(targetDir, "case.yaml"), []byte(caseYamlContent), 0644)

	graderTool := `---
type: regex
target: message
match_type: contains
pattern: "expected_token"
weight: 1.0
---
`
	_ = os.WriteFile(filepath.Join(targetDir, "graders", "output_check.md"), []byte(graderTool), 0644)

	graderContract := `---
type: sila_contract
action: test_runner
command: "go test ./..."
weight: 2.0
---
`
	_ = os.WriteFile(filepath.Join(targetDir, "graders", "sila_contract.md"), []byte(graderContract), 0644)

	return targetDir, nil
}

// SynthesizeFromLesson converts a recorded lesson fact into an automated regression test case.
func SynthesizeFromLesson(lesson store.Fact, outDir string) (string, error) {
	symptom, cause, fix, tagsStr := store.ParseLesson(lesson.Claim)
	if symptom == "" {
		symptom = lesson.Claim
	}

	slug := toSlug("lesson-" + symptom)
	targetDir := filepath.Join(outDir, slug)
	if err := os.MkdirAll(filepath.Join(targetDir, "graders"), 0755); err != nil {
		return "", fmt.Errorf("create case dir %s: %w", targetDir, err)
	}

	var tags []string
	if tagsStr != "" {
		for _, t := range strings.Split(tagsStr, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, fmt.Sprintf("%q", t))
			}
		}
	}
	tags = append(tags, `"regression"`, `"lesson"`)

	promptContent := fmt.Sprintf(`---
name: %s
max_turns: 5
tags: [%s]
---

We encountered an operational regression or bug:
%s

Investigate the root cause, apply the required fix, and verify that the solution passes all tests.
`, slug, strings.Join(tags, ", "), symptom)

	if err := os.WriteFile(filepath.Join(targetDir, "prompt.md"), []byte(promptContent), 0644); err != nil {
		return "", err
	}

	// Grader 1: Applied Fix check
	if fix != "" {
		cleanFix := strings.TrimSpace(fix)
		if len(cleanFix) > 30 {
			cleanFix = cleanFix[:30]
		}
		fixGrader := fmt.Sprintf(`---
type: regex
target: message
match_type: contains
pattern: %q
weight: 2.0
---
`, cleanFix)
		_ = os.WriteFile(filepath.Join(targetDir, "graders", "fix_applied.md"), []byte(fixGrader), 0644)
	}

	// Grader 2: Avoided Cause anti-pattern check
	if cause != "" && len(cause) > 5 {
		cleanCause := strings.TrimSpace(cause)
		if len(cleanCause) > 30 {
			cleanCause = cleanCause[:30]
		}
		causeGrader := fmt.Sprintf(`---
type: regex
target: message
match_type: not_contains
pattern: %q
weight: 1.5
---
`, cleanCause)
		_ = os.WriteFile(filepath.Join(targetDir, "graders", "avoid_cause.md"), []byte(causeGrader), 0644)
	}

	// Grader 3: Sila verification contract
	contractGrader := `---
type: sila_contract
action: test_runner
command: "go test ./..."
weight: 2.0
---
`
	_ = os.WriteFile(filepath.Join(targetDir, "graders", "workspace_test.md"), []byte(contractGrader), 0644)

	return targetDir, nil
}

// SynthesizeFromFeedback converts a user feedback record into a regression test case.
func SynthesizeFromFeedback(fb store.Feedback, outDir string) (string, error) {
	slug := toSlug("feedback-" + fb.Category + "-" + fb.Content)
	targetDir := filepath.Join(outDir, slug)
	if err := os.MkdirAll(filepath.Join(targetDir, "graders"), 0755); err != nil {
		return "", fmt.Errorf("create case dir %s: %w", targetDir, err)
	}

	promptContent := fmt.Sprintf(`---
name: %s
max_turns: 4
tags: ["feedback", %q]
---

User feedback report (%s):
%s

Resolve this issue cleanly without regressions.
`, slug, fb.Category, fb.Category, fb.Content)

	if err := os.WriteFile(filepath.Join(targetDir, "prompt.md"), []byte(promptContent), 0644); err != nil {
		return "", err
	}

	contractGrader := `---
type: sila_contract
action: test_runner
command: "go test ./..."
weight: 2.0
---
`
	_ = os.WriteFile(filepath.Join(targetDir, "graders", "workspace_test.md"), []byte(contractGrader), 0644)

	return targetDir, nil
}
