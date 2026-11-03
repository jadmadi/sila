package dream

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// AutoHandoffResult reports whether the session-end hook wrote a handoff.
type AutoHandoffResult struct {
	HandoffID string `json:"handoff_id,omitempty"`
	Summary   string `json:"summary"`
	Written   bool   `json:"written"`
	Existing  bool   `json:"existing"`
	OpenItems int    `json:"open_items"`
	RepoFiles int    `json:"repo_files"`
	Reason    string `json:"reason,omitempty"`
}

// BuildAutoHandoff guarantees a handoff at session end. It is idempotent for
// the same session, and it refuses to supersede a pending handoff owned by a
// different agent.
func BuildAutoHandoff(st *store.Store, projectID, projectDir, sessionID, requestedSummary string, dryRun bool) (*AutoHandoffResult, error) {
	if st == nil {
		return nil, fmt.Errorf("knowledge store is required")
	}
	if projectDir == "" {
		projectDir = "."
	}
	gitCtx := DetectGitContext(projectDir)
	if projectID == "" {
		projectID = store.ProjectID(projectDir)
	}
	result := &AutoHandoffResult{Existing: true}

	latest, _ := st.LatestHandoff(projectID)
	if latest != nil {
		if created, err := time.Parse(time.RFC3339, latest.CreatedAt); err == nil && time.Since(created) < time.Hour {
			if (sessionID != "" && latest.SessionID == sessionID) || latest.Agent == "sila-hook" {
				result.HandoffID = latest.ID
				result.Summary = latest.Summary
				result.Reason = "handoff already recorded for this session"
				return result, nil
			}
		}
		if latest.IsPending() && latest.Agent != "sila-hook" {
			result.HandoffID = latest.ID
			result.Summary = latest.Summary
			result.Reason = "another agent has a pending handoff; not superseding it"
			return result, nil
		}
	}

	openItems := autoHandoffOpenItems(st, projectID, projectDir, latest)
	repoFiles := gitChangedFiles(projectDir, 50)
	summary := strings.TrimSpace(requestedSummary)
	if summary == "" {
		branch := gitCtx.Branch
		if branch == "" {
			branch = "no-branch"
		}
		head := gitCtx.Commit
		if head == "" {
			head = "no-head"
		}
		goal := strings.TrimSpace(os.Getenv("SILA_ACTIVE_GOAL"))
		if goal == "" {
			goal = "none"
		}
		summary = fmt.Sprintf("sila-hook session handoff · branch %s · HEAD %s · %d changed file(s) · goal %s",
			branch, head, len(repoFiles), goal)
	}

	createdAt := time.Now().UTC().Format(time.RFC3339)
	handoff := store.Handoff{
		ID:            store.HandoffID(projectID, summary, createdAt),
		ProjectID:     projectID,
		Agent:         "sila-hook",
		ToolName:      "sila-hook",
		Branch:        gitCtx.Branch,
		Summary:       summary,
		OpenItems:     openItems,
		RepoFiles:     repoFiles,
		FileRefsExist: true,
		CreatedAt:     createdAt,
		Status:        string(store.HandoffPending),
		SessionID:     sessionID,
	}

	result.HandoffID = handoff.ID
	result.Summary = summary
	result.Written = false
	result.Existing = false
	result.OpenItems = len(openItems)
	result.RepoFiles = len(repoFiles)
	if dryRun {
		result.Reason = "dry-run: no handoff written"
		return result, nil
	}
	if err := st.InsertHandoff(handoff); err != nil {
		return nil, fmt.Errorf("insert auto-handoff: %w", err)
	}
	result.Written = true
	return result, nil
}

func autoHandoffOpenItems(st *store.Store, projectID, projectDir string, latest *store.Handoff) []string {
	var items []string
	seen := map[string]bool{}
	add := func(item string) {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			return
		}
		seen[item] = true
		items = append(items, item)
	}
	if latest != nil {
		for _, item := range latest.OpenItems {
			if !strings.HasPrefix(item, "- [x]") && !strings.HasPrefix(item, "[x]") {
				add(item)
			}
		}
	}
	activeGoal := strings.TrimSpace(os.Getenv("SILA_ACTIVE_GOAL"))
	if activeGoal != "" {
		add("Active goal: " + activeGoal)
	}
	if audit, err := goals.ScanGoals(projectDir); err == nil && audit != nil && activeGoal != "" {
		count := 0
		for _, goal := range audit.Goals {
			if goal.Status != "pending" {
				continue
			}
			for _, dep := range goal.DependsOn {
				if dep == activeGoal {
					add("Unblocks: " + goal.Slug)
					count++
					break
				}
			}
			if count >= 10 {
				break
			}
		}
	}
	return items
}

func gitChangedFiles(projectDir string, limit int) []string {
	cmd := exec.Command("git", "-C", projectDir, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
		if len(files) >= limit {
			break
		}
	}
	sort.Strings(files)
	return files
}
