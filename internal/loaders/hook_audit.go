package loaders

import (
	"bufio"
	"encoding/json"
	"github.com/jadmadi/sila/internal/config"
	"os"
	"path/filepath"
	"time"
)

// HookAuditEntry is a local-only lifecycle audit record. It never contains
// prompt, query, claim, or file content.
type HookAuditEntry struct {
	Event      string `json:"event"`
	Timestamp  string `json:"timestamp"`
	DurationMS int64  `json:"duration_ms"`
	ExitCode   int    `json:"exit_code"`
	SessionID  string `json:"session_id,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

func hookAuditPath(projectDir string) string {
	return filepath.Join(projectDir, ".sila", "state", "hooks", "audit.jsonl")
}

// AppendHookAudit appends one bounded JSONL entry. It is best-effort and never
// blocks the lifecycle event.
func AppendHookAudit(projectDir string, entry HookAuditEntry) error {
	if projectDir == "" || config.IsForbiddenLocalStateDir(projectDir) {
		return nil
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	path := hookAuditPath(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// ReadHookAudit returns the most recent entries, newest last.
func ReadHookAudit(projectDir string, limit int) ([]HookAuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	f, err := os.Open(hookAuditPath(projectDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []HookAuditEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry HookAuditEntry
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}
