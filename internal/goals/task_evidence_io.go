package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TaskEvidenceReceiptPath returns the exact immutable filepath for an attempt's receipt.
func TaskEvidenceReceiptPath(rootDir, slug, taskID, attemptID string) string {
	cleanSlug := filepath.Clean(slug)
	cleanTaskID := filepath.Clean(taskID)
	cleanAttemptID := filepath.Clean(attemptID)
	return filepath.Join(rootDir, "goals", cleanSlug, "evidence", cleanTaskID, cleanAttemptID, "receipt.json")
}

// SaveTaskEvidence atomically writes a TaskEvidence receipt to disk under
// goals/<slug>/evidence/<task-id>/<attempt-id>/receipt.json.
// Collisions are strictly rejected to prevent historical evidence mutation.
func SaveTaskEvidence(rootDir, slug string, ev *TaskEvidence) error {
	if ev == nil {
		return fmt.Errorf("task evidence is nil")
	}
	if strings.TrimSpace(ev.TaskID) == "" {
		return fmt.Errorf("task evidence task_id is required")
	}
	if strings.TrimSpace(ev.AttemptID) == "" {
		return fmt.Errorf("task evidence attempt_id is required")
	}
	if strings.TrimSpace(slug) == "" {
		slug = ev.GoalSlug
	}
	if strings.TrimSpace(slug) == "" {
		return fmt.Errorf("goal slug is required")
	}

	targetPath := TaskEvidenceReceiptPath(rootDir, slug, ev.TaskID, ev.AttemptID)

	// AC-010 & AC-011: Reject attempt collisions rather than overwrite history
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("attempt collision: evidence receipt already exists at %s", targetPath)
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}

	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task evidence: %w", err)
	}

	// Atomic write via temp file
	tmpFile, err := os.CreateTemp(dir, "receipt-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp receipt file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp receipt: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp receipt: %w", err)
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("atomic rename receipt: %w", err)
	}

	return nil
}

// LoadTaskEvidence reads and strictly decodes a TaskEvidence receipt from disk.
func LoadTaskEvidence(rootDir, slug, taskID, attemptID string) (*TaskEvidence, error) {
	p := TaskEvidenceReceiptPath(rootDir, slug, taskID, attemptID)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read evidence receipt %s: %w", p, err)
	}
	return DecodeTaskEvidence(data)
}

// ListTaskEvidenceAttempts returns all recorded attempt IDs for taskID under goal slug,
// ordered chronologically / lexicographically.
func ListTaskEvidenceAttempts(rootDir, slug, taskID string) ([]string, error) {
	evidenceBase := filepath.Join(rootDir, "goals", filepath.Clean(slug), "evidence", filepath.Clean(taskID))
	entries, err := os.ReadDir(evidenceBase)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read task evidence dir: %w", err)
	}

	var attempts []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		receiptFile := filepath.Join(evidenceBase, e.Name(), "receipt.json")
		if _, err := os.Stat(receiptFile); err == nil {
			attempts = append(attempts, e.Name())
		}
	}

	sort.Strings(attempts)
	return attempts, nil
}
