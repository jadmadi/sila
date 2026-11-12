package gate

import (
	"path"
	"strings"
)

// DefaultSensitivePatterns are the paths an untrusted commit may not touch.
var DefaultSensitivePatterns = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".sila/**",
	".github/workflows/**",
	".git/hooks/**",
	"scripts/**",
	"tools/gate/**",
	"Makefile",
	"go.mod",
	"go.sum",
}

// TrustCommit is one commit under review.
type TrustCommit struct {
	SHA    string
	Author string
	Email  string
	Paths  []string
}

// TrustRefusal is one commit that failed the trust check.
type TrustRefusal struct {
	SHA    string   `json:"sha"`
	Author string   `json:"author"`
	Email  string   `json:"email"`
	Paths  []string `json:"paths"`
}

// TrustDecision is the review outcome.
type TrustDecision struct {
	Checked        int            `json:"checked"`
	Refusals       []TrustRefusal `json:"refusals,omitempty"`
	SensitivePaths []string       `json:"sensitive_paths,omitempty"`
}

// Passed reports whether no refusal was recorded.
func (d TrustDecision) Passed() bool { return len(d.Refusals) == 0 }

// EvaluateTrust refuses any commit whose email differs from selfEmail and whose
// paths intersect the sensitive set, unless its full SHA is trusted. Paths are
// case-sensitive; emails compare case-insensitively.
func EvaluateTrust(commits []TrustCommit, selfEmail string, trusted map[string]bool, sensitive []string) TrustDecision {
	if len(sensitive) == 0 {
		sensitive = DefaultSensitivePatterns
	}
	decision := TrustDecision{Checked: len(commits)}
	seenPaths := map[string]bool{}
	for _, commit := range commits {
		hits := MatchSensitivePaths(commit.Paths, sensitive)
		if len(hits) == 0 {
			continue
		}
		for _, hit := range hits {
			if !seenPaths[hit] {
				seenPaths[hit] = true
				decision.SensitivePaths = append(decision.SensitivePaths, hit)
			}
		}
		if strings.EqualFold(strings.TrimSpace(commit.Email), strings.TrimSpace(selfEmail)) {
			continue
		}
		if trusted[commit.SHA] {
			continue
		}
		decision.Refusals = append(decision.Refusals, TrustRefusal{
			SHA: commit.SHA, Author: commit.Author, Email: commit.Email, Paths: hits,
		})
	}
	return decision
}

// MatchSensitivePaths returns the paths matching any pattern, case-sensitively.
func MatchSensitivePaths(paths, patterns []string) []string {
	var hits []string
	seen := map[string]bool{}
	for _, candidate := range paths {
		clean := path.Clean(strings.TrimSpace(candidate))
		if clean == "." || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") || clean == ".." {
			continue
		}
		for _, pattern := range patterns {
			if matchPathPattern(pattern, clean) {
				if !seen[clean] {
					seen[clean] = true
					hits = append(hits, clean)
				}
				break
			}
		}
	}
	return hits
}

// matchPathPattern supports ** across segments and path.Match within a segment.
func matchPathPattern(pattern, target string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	if pattern == "" || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "..") {
		return false
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(target, "/"))
}

func matchSegments(pattern, target []string) bool {
	if len(pattern) == 0 {
		return len(target) == 0
	}
	if pattern[0] == "**" {
		if matchSegments(pattern[1:], target) {
			return true
		}
		if len(target) > 0 {
			return matchSegments(pattern, target[1:])
		}
		return false
	}
	if len(target) == 0 {
		return false
	}
	matched, err := path.Match(pattern[0], target[0])
	if err != nil || !matched {
		return false
	}
	return matchSegments(pattern[1:], target[1:])
}
