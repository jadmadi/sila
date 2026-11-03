package dream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/store"
)

// SessionEndResult summarizes the actions taken by the session-end hook.
type SessionEndResult struct {
	ProjectID       string     `json:"project_id"`
	Git             GitContext `json:"git"`
	Timestamp       string     `json:"timestamp"`
	LatestHandoff   string     `json:"latest_handoff,omitempty"`
	UnclosedTasks   int        `json:"unclosed_tasks"`
	SyncTriggered   bool       `json:"sync_triggered"`
	Suggestions     []string   `json:"suggestions,omitempty"`
	Insights        []string   `json:"insights,omitempty"`
	SummaryMessage  string     `json:"summary_message"`
	HandoffID       string     `json:"handoff_id,omitempty"`
	HandoffWritten  bool       `json:"handoff_written"`
	HandoffExisting bool       `json:"handoff_existing,omitempty"`
	AutoHandoff     bool       `json:"auto_handoff"`
}

// RunSessionEndHook executes the end-of-session lifecycle hook.
func RunSessionEndHook(st *store.Store, projectID, projectDir string, autoSync bool) (*SessionEndResult, error) {
	if projectDir == "" {
		projectDir = "."
	}
	gitCtx := DetectGitContext(projectDir)
	if projectID == "" {
		if gitCtx.RootPath != "" {
			projectID = store.ProjectID(gitCtx.RootPath)
		} else {
			projectID = store.ProjectID(projectDir)
		}
	}

	res := &SessionEndResult{
		ProjectID:     projectID,
		Git:           gitCtx,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		SyncTriggered: autoSync,
	}

	// Check latest handoff
	handoff, _ := st.LatestHandoff(projectID)
	if handoff != nil {
		res.LatestHandoff = handoff.Summary
		for _, it := range handoff.OpenItems {
			if !strings.HasPrefix(it, "- [x]") && !strings.HasPrefix(it, "[x]") {
				res.UnclosedTasks++
			}
		}
	}

	// Extract candidate memo / skill suggestions from recent session messages
	candidates := ExtractProjectMemoCandidates(st, projectID)
	for _, cand := range candidates {
		res.Suggestions = append(res.Suggestions, fmt.Sprintf("Candidate memo: %q (run `sila memo %q` to save)", cand, cand))
	}

	// Extract candidate lessons from recent session messages
	lessonCandidates := ExtractProjectLessonCandidates(st, projectID, time.Time{})
	for _, cand := range lessonCandidates {
		res.Suggestions = append(res.Suggestions, fmt.Sprintf("Candidate lesson: %q (fix: %q) — run `sila lesson %q --cause=%q --fix=%q` or `sila lesson distill --save` to record", cand.Symptom, cand.Fix, cand.Symptom, cand.Cause, cand.Fix))
	}
	if len(candidates) == 0 && len(lessonCandidates) == 0 {
		res.Suggestions = append(res.Suggestions, "Tip: Solved a non-obvious bug or invariant? Record with `sila lesson \"<symptom>\" --cause=... --fix=...` or `sila memo \"...\"`")
	}

	// Optional semantic live assistance (fail-open: nil if disabled or unconfigured)
	if st != nil {
		if provider := assist.ActiveProviderFor("live", projectID); provider != nil {
			msgs, _ := st.RecentMessages(projectID, 15)
			if len(msgs) > 0 {
				fingerprint := computeEvidenceFingerprint(msgs)
				checkpointKey := "assist_live_checkpoint:" + projectID
				lastCheckpoint, err := st.GetState(checkpointKey)
				if err != nil {
					lastCheckpoint = ""
				}
				if fingerprint != lastCheckpoint {
					insights, evaluated, err := assist.EvaluateLiveDelta(context.Background(), provider, msgs)
					if err == nil && evaluated {
						res.Insights = insights
						// Advance checkpoint only on successful evaluation
						_ = st.SetState(checkpointKey, fingerprint)
					}
				}
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Session concluded for %s", projectID))
	if gitCtx.Branch != "" {
		sb.WriteString(fmt.Sprintf(" (branch: %s)", gitCtx.Branch))
	}
	if gitCtx.Commit != "" {
		sb.WriteString(fmt.Sprintf(" at commit %s", gitCtx.Commit))
	}
	sb.WriteString(".")
	if res.UnclosedTasks > 0 {
		sb.WriteString(fmt.Sprintf(" %d open task(s) recorded in handoff.", res.UnclosedTasks))
	}
	if len(res.Suggestions) > 0 {
		sb.WriteString(" " + strings.Join(res.Suggestions, " | "))
	}
	if len(res.Insights) > 0 {
		sb.WriteString(" Insights: " + strings.Join(res.Insights, " "))
	}

	res.SummaryMessage = sb.String()
	return res, nil
}

// ExtractProjectMemoCandidates extracts candidate memo rules from recent project messages.
func ExtractProjectMemoCandidates(st *store.Store, projectID string) []string {
	if st == nil || projectID == "" {
		return nil
	}
	existingFacts, _ := st.FactsByProject(projectID)
	recentMsgs, _ := st.RecentMessages(projectID, 20)
	if len(recentMsgs) == 0 {
		return nil
	}
	return ExtractSessionMemoCandidates(recentMsgs, existingFacts)
}

// ExtractSessionMemoCandidates scans recent session messages for high-value architectural decisions,
// constraints, or invariants that could be preserved as durable memos.
func ExtractSessionMemoCandidates(messages []store.StoredMessage, existingFacts []store.Fact) []string {
	var candidates []string
	seen := make(map[string]bool)

	// Build lookup of existing claims
	for _, f := range existingFacts {
		seen[strings.ToLower(strings.TrimSpace(f.Claim))] = true
		if f.Distilled != "" {
			seen[strings.ToLower(strings.TrimSpace(f.Distilled))] = true
		}
	}

	markers := []string{
		"decision:",
		"invariant:",
		"rule:",
		"constraint:",
		"we should always",
		"always use",
		"never use",
		"must always",
		"do not import",
		"do not use",
	}

	for _, m := range messages {
		if m.Role != "assistant" && m.Role != "user" {
			continue
		}
		lines := strings.SplitSeq(m.Content, "\n")
		for line := range lines {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimPrefix(line, "* ")
			line = strings.TrimPrefix(line, "> ")
			lower := strings.ToLower(line)
			for _, marker := range markers {
				if idx := strings.Index(lower, marker); idx >= 0 {
					candidate := strings.TrimSpace(line[idx:])
					if len(candidate) > 180 {
						candidate = candidate[:180]
					}
					candidate = strings.TrimRight(candidate, ".;,")
					candLower := strings.ToLower(candidate)
					if len(candidate) >= 15 && !seen[candLower] {
						seen[candLower] = true
						candidates = append(candidates, candidate)
						if len(candidates) >= 2 {
							return candidates
						}
					}
				}
			}
		}
	}
	return candidates
}

// computeEvidenceFingerprint computes a stable hash over recent project messages.
func computeEvidenceFingerprint(msgs []store.StoredMessage) string {
	h := sha256.New()
	for _, m := range msgs {
		tools := strings.Join(m.SourceTools, ",")
		fmt.Fprintf(h, "%d:%s:%s:%s:%s:%s:%s;", m.ID, m.ContentHash, m.TS, m.FirstSeen, m.Role, tools, m.ProjectID)
	}
	return hex.EncodeToString(h.Sum(nil))
}
