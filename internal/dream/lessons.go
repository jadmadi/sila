package dream

import (
	"regexp"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// CandidateLesson represents a potential structured solution lesson distilled
// from recent session transcripts or commit discussions.
type CandidateLesson struct {
	Symptom string   `json:"symptom"`
	Cause   string   `json:"cause,omitempty"`
	Fix     string   `json:"fix"`
	Files   []string `json:"files,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// ExtractProjectLessonCandidates extracts candidate lessons from recent project messages.
func ExtractProjectLessonCandidates(st *store.Store, projectID string, since time.Time) []CandidateLesson {
	if st == nil || projectID == "" {
		return nil
	}
	existingLessons, _ := st.LessonFacts(projectID, 500)
	recentMsgs, _ := st.RecentMessages(projectID, 50)
	if len(recentMsgs) == 0 {
		return nil
	}

	var filtered []store.StoredMessage
	for _, m := range recentMsgs {
		if !since.IsZero() {
			ts, err := time.Parse(time.RFC3339, m.TS)
			if err == nil && ts.Before(since) {
				continue
			}
		}
		filtered = append(filtered, m)
	}

	return ExtractSessionLessonCandidates(filtered, existingLessons)
}

var (
	filePathRe = regexp.MustCompile(`\b([a-zA-Z0-9_\-\.\/]+\.(?:go|ts|tsx|js|jsx|py|rs|sql|json|yaml|yml|md|sh|toml))\b`)
	causeFixRe = regexp.MustCompile(`(?i)(?:the\s+)?(?:bug|issue|failure|problem|error)\s+(?:was\s+)?caused\s+by\s+([^.]+)\.\s*(?:fixed|resolved)\s+by\s+([^.]+)`)
	fixedByRe  = regexp.MustCompile(`(?i)(?:fixed|resolved)\s+(?:the\s+)?([^.]+?)\s+by\s+([^.]+)`)
)

// ExtractSessionLessonCandidates scans session messages for high-value problem-solution pairs.
func ExtractSessionLessonCandidates(messages []store.StoredMessage, existingLessons []store.Fact) []CandidateLesson {
	var candidates []CandidateLesson
	seen := make(map[string]bool)

	// Build lookup of existing lessons
	for _, f := range existingLessons {
		s, _, fx, _ := store.ParseLesson(f.Claim)
		if s != "" {
			seen[normalizeText(s)] = true
		}
		if fx != "" {
			seen[normalizeText(fx)] = true
		}
		seen[normalizeText(f.Claim)] = true
	}

	isDuplicate := func(cand CandidateLesson) bool {
		normSym := normalizeText(cand.Symptom)
		normFix := normalizeText(cand.Fix)
		if normSym == "" || normFix == "" {
			return true
		}
		if seen[normSym] || seen[normSym+"->"+normFix] {
			return true
		}
		candClaim := store.FormatLesson(cand.Symptom, cand.Cause, cand.Fix, "")
		for _, f := range existingLessons {
			if Similarity(f.Claim, candClaim) >= 0.6 {
				return true
			}
		}
		return false
	}

	for _, m := range messages {
		if m.Role != "assistant" && m.Role != "user" {
			continue
		}

		// Look for pattern matches in message text
		text := m.Content
		files := extractCandidateFiles(text)
		tags := extractCandidateTags(text)

		// 1. Check multi-line labeled patterns (Symptom/Problem/Error + Cause + Fix/Solution)
		labeled := extractLabeledLesson(text)
		for _, l := range labeled {
			if len(l.Files) == 0 {
				l.Files = files
			}
			if len(l.Tags) == 0 {
				l.Tags = tags
			}
			if !isDuplicate(l) {
				seen[normalizeText(l.Symptom)] = true
				seen[normalizeText(l.Symptom)+"->"+normalizeText(l.Fix)] = true
				candidates = append(candidates, l)
				if len(candidates) >= 5 {
					return candidates
				}
			}
		}

		// 2. Check regex sentence patterns
		if matches := causeFixRe.FindAllStringSubmatch(text, -1); len(matches) > 0 {
			for _, match := range matches {
				if len(match) >= 3 {
					cause := cleanClause(match[1], 120)
					fix := cleanClause(match[2], 120)
					sym := fmtSentenceSymptom(cause, fix)
					cand := CandidateLesson{
						Symptom: sym,
						Cause:   cause,
						Fix:     fix,
						Files:   files,
						Tags:    tags,
					}
					if !isDuplicate(cand) {
						seen[normalizeText(cand.Symptom)] = true
						seen[normalizeText(cand.Symptom)+"->"+normalizeText(cand.Fix)] = true
						candidates = append(candidates, cand)
						if len(candidates) >= 5 {
							return candidates
						}
					}
				}
			}
		}

		if matches := fixedByRe.FindAllStringSubmatch(text, -1); len(matches) > 0 {
			for _, match := range matches {
				if len(match) >= 3 {
					sym := cleanClause(match[1], 120)
					fix := cleanClause(match[2], 120)
					cand := CandidateLesson{
						Symptom: sym,
						Fix:     fix,
						Files:   files,
						Tags:    tags,
					}
					if !isDuplicate(cand) {
						seen[normalizeText(cand.Symptom)] = true
						seen[normalizeText(cand.Symptom)+"->"+normalizeText(cand.Fix)] = true
						candidates = append(candidates, cand)
						if len(candidates) >= 5 {
							return candidates
						}
					}
				}
			}
		}
	}

	return candidates
}

// extractLabeledLesson parses lines looking for Symptom/Problem/Issue/Error and Fix/Solution/Workaround markers.
func extractLabeledLesson(text string) []CandidateLesson {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\\n", "\n")
	lines := strings.Split(text, "\n")
	var symptom, cause, fix string
	var out []CandidateLesson

	flush := func() {
		if symptom != "" && fix != "" {
			out = append(out, CandidateLesson{
				Symptom: cleanClause(symptom, 140),
				Cause:   cleanClause(cause, 140),
				Fix:     cleanClause(fix, 140),
			})
		}
		symptom, cause, fix = "", "", ""
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "> ")
		line = strings.TrimPrefix(line, "# ")
		line = strings.TrimPrefix(line, "## ")
		line = strings.TrimPrefix(line, "### ")
		line = strings.TrimSpace(line)

		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "symptom:") || strings.HasPrefix(lower, "problem:") || strings.HasPrefix(lower, "issue:") || strings.HasPrefix(lower, "error:"):
			idx := strings.Index(line, ":")
			val := strings.TrimSpace(line[idx+1:])
			if val != "" {
				if symptom != "" && fix != "" {
					flush()
				}
				symptom = val
			}
		case strings.HasPrefix(lower, "cause:") || strings.HasPrefix(lower, "root cause:"):
			idx := strings.Index(line, ":")
			val := strings.TrimSpace(line[idx+1:])
			if val != "" {
				cause = val
			}
		case strings.HasPrefix(lower, "fix:") || strings.HasPrefix(lower, "solution:") || strings.HasPrefix(lower, "resolution:") || strings.HasPrefix(lower, "workaround:"):
			idx := strings.Index(line, ":")
			val := strings.TrimSpace(line[idx+1:])
			if val != "" {
				fix = val
				if symptom != "" {
					flush()
				}
			}
		}
	}
	flush()
	return out
}

func cleanClause(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`*\"'_.,;")
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return strings.TrimSpace(s)
}

func normalizeText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func fmtSentenceSymptom(cause, fix string) string {
	if cause != "" {
		return "failure due to " + cause
	}
	return "unresolved issue addressed by " + fix
}

func extractCandidateFiles(text string) []string {
	matches := filePathRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	var res []string
	seen := make(map[string]bool)
	for _, m := range matches {
		m = strings.Trim(m, "`\"'(),;:")
		if !seen[m] && len(m) > 3 {
			seen[m] = true
			res = append(res, m)
			if len(res) >= 4 {
				break
			}
		}
	}
	return res
}

func extractCandidateTags(text string) []string {
	lower := strings.ToLower(text)
	keywords := []string{"sqlite", "cgo", "race", "test", "gate", "mcp", "cache", "build", "cloud", "git", "hook", "network", "lock"}
	var tags []string
	seen := make(map[string]bool)
	for _, kw := range keywords {
		if strings.Contains(lower, kw) && !seen[kw] {
			seen[kw] = true
			tags = append(tags, kw)
			if len(tags) >= 3 {
				break
			}
		}
	}
	return tags
}

// DistillExperienceToLesson converts a non-confounded causal experience entry from contrastive
// harness evaluation into a structured Sila CandidateLesson.
func DistillExperienceToLesson(id, domain, cue, lesson, evidence string, files []string) CandidateLesson {
	tags := []string{"harness2"}
	if d := strings.TrimSpace(domain); d != "" {
		tags = append(tags, d)
	}
	if id = strings.TrimSpace(id); id != "" {
		tags = append(tags, id)
	}
	cleanFiles := make([]string, 0, len(files))
	for _, f := range files {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			cleanFiles = append(cleanFiles, trimmed)
		}
	}
	return CandidateLesson{
		Symptom: cleanClause(cue, 240),
		Cause:   cleanClause(evidence, 300),
		Fix:     cleanClause(lesson, 300),
		Files:   cleanFiles,
		Tags:    tags,
	}
}
