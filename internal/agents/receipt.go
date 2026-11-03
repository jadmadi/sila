package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// StateStore is an interface for reading and writing key-value state,
// satisfied by store.Store.
type StateStore interface {
	GetState(key string) (string, error)
	SetState(key, value string) error
}

// LastSyncReceiptKey is the state table key for the global last sync receipt.
const LastSyncReceiptKey = "agents_sync:last"

// ProjectSyncReceiptPrefix is the state table key prefix for per-project sync receipts.
const ProjectSyncReceiptPrefix = "agents_sync:project:"

// ProjectSyncReceiptKey returns the state key for a specific project.
func ProjectSyncReceiptKey(projectID string) string {
	return ProjectSyncReceiptPrefix + projectID
}

// ProjectReceipt records the outcome of a single project's rule sync.
type ProjectReceipt struct {
	ProjectID       string   `json:"project_id"`
	Path            string   `json:"path"`
	FilesWritten    []string `json:"files_written,omitempty"`
	Symlinks        []string `json:"symlinks,omitempty"`
	Errors          []string `json:"errors,omitempty"`
	Success         bool     `json:"success"`
	ManagedHash     string   `json:"managed_hash,omitempty"`
	ManagedRevision int      `json:"managed_revision,omitempty"`
	HashSkips       int      `json:"hash_skips,omitempty"`
	SkippedFiles    []string `json:"skipped_files,omitempty"`
	DriftKind       string   `json:"drift_kind,omitempty"`
	Written         bool     `json:"written,omitempty"`
	Skipped         bool     `json:"skipped,omitempty"`
}

// SyncReceipt represents a structured provenance receipt for an agent rule synchronization run.
type SyncReceipt struct {
	Timestamp       string           `json:"timestamp"`      // RFC3339 timestamp
	HubCommit       string           `json:"hub_commit"`     // Git commit hash of the core hub rules (or sha256)
	Scope           string           `json:"scope"`          // "all" or specific project_id
	Initiator       string           `json:"initiator"`      // $SILA_AGENT or user/cli
	TotalProjects   int              `json:"total_projects"` // Total projects attempted
	Succeeded       int              `json:"succeeded"`      // Number of successfully synced projects
	Failed          int              `json:"failed"`         // Number of projects with errors
	FilesWritten    int              `json:"files_written"`  // Total files written across projects
	Symlinks        int              `json:"symlinks"`       // Total symlinks created
	ManagedRevision int              `json:"managed_revision,omitempty"`
	ManagedHash     string           `json:"managed_hash,omitempty"`
	Projects        []ProjectReceipt `json:"projects"` // Per-project summaries
}

// hashSkippedFiles lists the plan entries a managed-hash fingerprint skipped.
func hashSkippedFiles(res *SyncResult) []string {
	if res == nil {
		return nil
	}
	var skipped []string
	for _, p := range res.Plan {
		if p.Action == "skip" && p.HashMatch {
			skipped = append(skipped, p.Path)
		}
	}
	return skipped
}

// SaveSyncReceipt stores a SyncReceipt into the state store.
// It updates the global LastSyncReceiptKey and per-project keys.
func SaveSyncReceipt(s StateStore, rec *SyncReceipt) error {
	if s == nil || rec == nil {
		return nil
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal sync receipt: %w", err)
	}

	if err := s.SetState(LastSyncReceiptKey, string(data)); err != nil {
		return fmt.Errorf("save last sync receipt: %w", err)
	}

	for _, p := range rec.Projects {
		if p.ProjectID == "" {
			continue
		}
		pData, err := json.Marshal(p)
		if err != nil {
			continue
		}
		_ = s.SetState(ProjectSyncReceiptKey(p.ProjectID), string(pData))
	}

	return nil
}

// LoadLastSyncReceipt retrieves the most recent sync receipt from the state store.
func LoadLastSyncReceipt(s StateStore) (*SyncReceipt, error) {
	if s == nil {
		return nil, nil
	}
	raw, err := s.GetState(LastSyncReceiptKey)
	if err != nil {
		return nil, fmt.Errorf("load last sync receipt: %w", err)
	}
	if raw == "" {
		return nil, nil
	}

	var rec SyncReceipt
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, fmt.Errorf("unmarshal sync receipt: %w", err)
	}
	return &rec, nil
}

// LoadProjectSyncReceipt retrieves the sync receipt for a specific project.
func LoadProjectSyncReceipt(s StateStore, projectID string) (*ProjectReceipt, error) {
	if s == nil || projectID == "" {
		return nil, nil
	}
	raw, err := s.GetState(ProjectSyncReceiptKey(projectID))
	if err != nil {
		return nil, fmt.Errorf("load project sync receipt: %w", err)
	}
	if raw == "" {
		return nil, nil
	}

	var rec ProjectReceipt
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, fmt.Errorf("unmarshal project sync receipt: %w", err)
	}
	return &rec, nil
}

// ResolveHubCommit determines the git commit SHA or content hash of the Core Hub rules.
func ResolveHubCommit(agentsDir string) string {
	if agentsDir != "" {
		// Try git rev-parse in agentsDir
		cmd := exec.Command("git", "-C", agentsDir, "rev-parse", "--short", "HEAD")
		if out, err := cmd.Output(); err == nil {
			short := strings.TrimSpace(string(out))
			if short != "" {
				return short
			}
		}

		// Fallback to hashing AGENTS.latest.md if present
		latestPath := filepath.Join(agentsDir, "AGENTS.latest.md")
		if data, err := os.ReadFile(latestPath); err == nil && len(data) > 0 {
			h := sha256.Sum256(data)
			return "sha256:" + hex.EncodeToString(h[:])[:8]
		}
	}

	return "embedded"
}

// ResolveInitiator returns the active agent identity or user.
func ResolveInitiator() string {
	if agent := os.Getenv("SILA_AGENT"); agent != "" {
		return agent
	}
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	return "cli"
}

// NewSyncReceiptFromMulti constructs a SyncReceipt from a MultiSyncResult.
func NewSyncReceiptFromMulti(res *MultiSyncResult, hubCommit, initiator string) *SyncReceipt {
	if res == nil {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if initiator == "" {
		initiator = ResolveInitiator()
	}

	rec := &SyncReceipt{
		Timestamp:     now,
		HubCommit:     hubCommit,
		Scope:         "all",
		Initiator:     initiator,
		TotalProjects: res.Total,
		Succeeded:     res.Succeeded,
		Failed:        res.Failed,
		Projects:      make([]ProjectReceipt, 0, len(res.Projects)),
	}

	for _, p := range res.Projects {
		pr := ProjectReceipt{
			ProjectID: p.ProjectID,
			Path:      p.Path,
			Success:   p.Error == "",
		}
		if p.Error != "" {
			pr.Errors = []string{p.Error}
		}
		if p.Result != nil {
			pr.FilesWritten = p.Result.FilesWritten
			pr.Symlinks = p.Result.Symlinks
			pr.ManagedHash = p.Result.ManagedHash
			pr.ManagedRevision = p.Result.ManagedRevision
			pr.HashSkips = p.Result.HashSkips
			pr.SkippedFiles = hashSkippedFiles(p.Result)
			pr.Written = len(p.Result.FilesWritten) > 0 || len(p.Result.Symlinks) > 0
			pr.Skipped = p.Result.FilesSkipped > 0 && !pr.Written
			if len(p.Result.Errors) > 0 {
				pr.DriftKind = "error"
			} else {
				pr.DriftKind = "in_sync"
			}
			if rec.ManagedHash == "" && p.Result.ManagedHash != "" {
				rec.ManagedHash = p.Result.ManagedHash
				rec.ManagedRevision = p.Result.ManagedRevision
			}
			if len(p.Result.Errors) > 0 {
				pr.Errors = append(pr.Errors, p.Result.Errors...)
				pr.Success = false
			}
			rec.FilesWritten += len(p.Result.FilesWritten)
			rec.Symlinks += len(p.Result.Symlinks)
		}
		rec.Projects = append(rec.Projects, pr)
	}

	return rec
}

// NewSyncReceiptFromSingle constructs a SyncReceipt from a single project SyncResult.
func NewSyncReceiptFromSingle(res *SyncResult, projectID, hubCommit, initiator string) *SyncReceipt {
	if res == nil {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if initiator == "" {
		initiator = ResolveInitiator()
	}

	succeeded := 1
	failed := 0
	if len(res.Errors) > 0 {
		succeeded = 0
		failed = 1
	}

	pr := ProjectReceipt{
		ProjectID:       projectID,
		Path:            res.RepoRoot,
		FilesWritten:    res.FilesWritten,
		Symlinks:        res.Symlinks,
		Errors:          res.Errors,
		Success:         len(res.Errors) == 0,
		ManagedHash:     res.ManagedHash,
		ManagedRevision: res.ManagedRevision,
		HashSkips:       res.HashSkips,
		SkippedFiles:    hashSkippedFiles(res),
		DriftKind:       map[bool]string{true: "error", false: "in_sync"}[len(res.Errors) > 0],
		Written:         len(res.FilesWritten) > 0 || len(res.Symlinks) > 0,
		Skipped:         res.FilesSkipped > 0 && len(res.FilesWritten) == 0 && len(res.Symlinks) == 0,
	}

	return &SyncReceipt{
		Timestamp:       now,
		HubCommit:       hubCommit,
		Scope:           projectID,
		Initiator:       initiator,
		TotalProjects:   1,
		Succeeded:       succeeded,
		Failed:          failed,
		FilesWritten:    len(res.FilesWritten),
		Symlinks:        len(res.Symlinks),
		ManagedRevision: res.ManagedRevision,
		ManagedHash:     res.ManagedHash,
		Projects:        []ProjectReceipt{pr},
	}
}

// FormatRelativeAge formats an RFC3339 timestamp as a human-friendly relative age.
func FormatRelativeAge(rfc3339Time string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, rfc3339Time)
	if err != nil {
		return "unknown"
	}
	diff := now.Sub(t)
	if diff < 0 {
		return "just now"
	}
	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		mins := int(diff.Minutes())
		return fmt.Sprintf("%dm ago", mins)
	}
	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		return fmt.Sprintf("%dh ago", hours)
	}
	days := int(diff.Hours() / 24)
	return fmt.Sprintf("%dd ago", days)
}
