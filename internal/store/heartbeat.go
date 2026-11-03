package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EpistemicHeartbeat represents an explicit periodic assertion emitted by an agent or harness
// proving active evaluation and compliance with a core architectural invariant.
type EpistemicHeartbeat struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	SessionID     string `json:"session_id"`
	Agent         string `json:"agent"`
	InvariantRule string `json:"invariant_rule"`
	EvidenceType  string `json:"evidence_type"` // "file", "commit", "test_run", "gate"
	EvidenceRef   string `json:"evidence_ref"`  // file path, commit SHA, test name, or command
	Status        string `json:"status"`        // "pass" or "fail"
	CreatedAt     string `json:"created_at"`
}

// HeartbeatID computes a deterministic identifier for an epistemic heartbeat.
func HeartbeatID(projectID, sessionID, invariantRule, evidenceRef string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s", projectID, sessionID, invariantRule, evidenceRef)))
	return "hb_" + hex.EncodeToString(sum[:6])
}

// RecordHeartbeat records an epistemic heartbeat proving active invariant evaluation.
func (s *Store) RecordHeartbeat(hb EpistemicHeartbeat) error {
	if hb.ProjectID == "" {
		return fmt.Errorf("project_id is required for heartbeat")
	}
	if hb.InvariantRule == "" {
		return fmt.Errorf("invariant_rule is required for heartbeat")
	}
	hb.EvidenceRef = strings.TrimSpace(hb.EvidenceRef)
	if hb.EvidenceRef == "" {
		return fmt.Errorf("evidence_ref is required for epistemic heartbeat")
	}
	if hb.EvidenceType == "" {
		hb.EvidenceType = "file"
	}
	switch hb.EvidenceType {
	case "file", "commit", "tool", "test_run", "gate":
		// valid
	default:
		return fmt.Errorf("invalid evidence_type %q: must be one of file, commit, tool, test_run, gate", hb.EvidenceType)
	}
	if hb.Status != "pass" && hb.Status != "fail" {
		return fmt.Errorf("invalid heartbeat status %q: must be strictly 'pass' or 'fail'", hb.Status)
	}
	if hb.EvidenceType == "file" && hb.Status == "pass" {
		ref := hb.EvidenceRef
		exists := false
		if _, err := os.Stat(ref); err == nil {
			exists = true
		} else if p, err := s.ResolveProject(hb.ProjectID); err == nil && p.Path != "" {
			if _, err := os.Stat(filepath.Join(p.Path, ref)); err == nil {
				exists = true
			}
		}
		if !exists {
			return fmt.Errorf("evidence file does not exist: %s", ref)
		}
	}
	if hb.SessionID == "" {
		hb.SessionID = "default"
	}
	if hb.Agent == "" {
		hb.Agent = "agent"
	}
	if hb.CreatedAt == "" {
		hb.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if hb.ID == "" {
		hb.ID = HeartbeatID(hb.ProjectID, hb.SessionID, hb.InvariantRule, hb.EvidenceRef)
	}

	return WithRetry(context.Background(), func() error {
		_, err := s.DB.Exec(`
			INSERT INTO epistemic_heartbeats (id, project_id, session_id, agent, invariant_rule, evidence_type, evidence_ref, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				status = excluded.status,
				created_at = excluded.created_at;
		`, hb.ID, hb.ProjectID, hb.SessionID, hb.Agent, hb.InvariantRule, hb.EvidenceType, hb.EvidenceRef, hb.Status, hb.CreatedAt)
		return err
	})
}

// ListRecentHeartbeats returns the newest epistemic heartbeats for a project.
func (s *Store) ListRecentHeartbeats(projectID string, limit int) ([]EpistemicHeartbeat, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(`
		SELECT id, project_id, session_id, agent, invariant_rule, evidence_type, evidence_ref, status, created_at
		FROM epistemic_heartbeats
		WHERE project_id = ?
		ORDER BY created_at DESC
		LIMIT ?;
	`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("list heartbeats: %w", err)
	}
	defer rows.Close()

	var hbs []EpistemicHeartbeat
	for rows.Next() {
		var hb EpistemicHeartbeat
		if err := rows.Scan(&hb.ID, &hb.ProjectID, &hb.SessionID, &hb.Agent, &hb.InvariantRule, &hb.EvidenceType, &hb.EvidenceRef, &hb.Status, &hb.CreatedAt); err != nil {
			return nil, err
		}
		hbs = append(hbs, hb)
	}
	return hbs, rows.Err()
}
