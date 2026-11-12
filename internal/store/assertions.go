package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Assertion represents an explicit lineage link binding a high-level knowledge statement
// (fact, lesson, memo, decision) to resolvable evidence (file, message turn, doc, commit).
// Verification records source availability, not the semantic truth of the statement.
type Assertion struct {
	ID                string `json:"id"`
	ProjectID         string `json:"project_id"`
	StatementID       string `json:"statement_id"`           // Fact ID
	StatementType     string `json:"statement_type"`         // "fact", "memo", "lesson", "case", "decision"
	EvidenceType      string `json:"evidence_type"`          // "file", "message", "doc", "test_run", "git_commit"
	EvidenceID        string `json:"evidence_id"`            // path, message ID, doc ID, or commit SHA
	EvidenceRef       string `json:"evidence_ref,omitempty"` // optional bytes:start-end span
	Confidence        string `json:"confidence"`             // "ungrounded", "grounded", "corroborated"
	Verified          bool   `json:"verified"`               // project-scoped source availability
	VerifiedAt        string `json:"verified_at,omitempty"`
	CreatedAt         string `json:"created_at"`
	VerificationError string `json:"verification_error,omitempty"`
}

// FactWithLineage packages a Fact together with all supporting assertion-to-evidence links.
type FactWithLineage struct {
	Fact       Fact        `json:"fact"`
	Assertions []Assertion `json:"assertions"`
}

// AssertionID computes a deterministic ID for an assertion.
func AssertionID(projectID, statementID, evidenceType, evidenceID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s", projectID, statementID, evidenceType, evidenceID)))
	return "ass_" + hex.EncodeToString(sum[:6])
}

// InsertAssertion upserts an assertion into the database.
func (s *Store) InsertAssertion(a Assertion) error {
	return WithRetry(context.Background(), func() error {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		if err := insertAssertionTx(tx, a); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func insertAssertionTx(tx *sql.Tx, a Assertion) error {
	if a.ID == "" {
		a.ID = AssertionID(a.ProjectID, a.StatementID, a.EvidenceType, a.EvidenceID)
	}
	if a.CreatedAt == "" {
		a.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if a.Confidence == "" {
		a.Confidence = "grounded"
	}
	var existing Assertion
	err := tx.QueryRow(`SELECT project_id, statement_id, evidence_type, evidence_id, evidence_ref
		FROM assertions WHERE id = ?`, a.ID).Scan(&existing.ProjectID, &existing.StatementID,
		&existing.EvidenceType, &existing.EvidenceID, &existing.EvidenceRef)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if existing.ProjectID != a.ProjectID || existing.StatementID != a.StatementID ||
			existing.EvidenceType != a.EvidenceType || existing.EvidenceID != a.EvidenceID {
			return fmt.Errorf("assertion ID %q already identifies different evidence", a.ID)
		}
		if a.EvidenceRef == "" {
			a.EvidenceRef = existing.EvidenceRef
		}
	}
	if err := verifyAssertionTx(tx, &a, ""); err != nil {
		return err
	}

	verifiedInt := 0
	if a.Verified {
		verifiedInt = 1
	}

	_, err = tx.Exec(`
		INSERT INTO assertions(id, project_id, statement_id, statement_type, evidence_type, evidence_id, evidence_ref, confidence, verified, verified_at, created_at, verification_error)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			statement_type = excluded.statement_type,
			evidence_ref   = CASE WHEN excluded.evidence_ref != '' THEN excluded.evidence_ref ELSE assertions.evidence_ref END,
			confidence     = excluded.confidence,
			verified       = excluded.verified,
			verified_at    = excluded.verified_at,
			verification_error = excluded.verification_error;
	`, a.ID, a.ProjectID, a.StatementID, a.StatementType, a.EvidenceType, a.EvidenceID, a.EvidenceRef, a.Confidence, verifiedInt, a.VerifiedAt, a.CreatedAt, a.VerificationError)
	return err
}

// LinkFactEvidence generates and links assertion records for a fact's evidence.
func (s *Store) LinkFactEvidence(f Fact) error {
	return WithRetry(context.Background(), func() error {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		if err := linkFactEvidenceTx(tx, f); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func linkFactEvidenceTx(tx *sql.Tx, f Fact) error {
	statementType := f.Kind
	if statementType == "" {
		statementType = "fact"
	}
	// 1. Link file evidence
	for _, file := range f.RepoFiles {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		a := Assertion{
			ProjectID:     f.ProjectID,
			StatementID:   f.ID,
			StatementType: statementType,
			EvidenceType:  "file",
			EvidenceID:    file,
			Confidence:    f.Confidence,
			CreatedAt:     f.FirstSeen,
		}
		if err := insertAssertionTx(tx, a); err != nil {
			return fmt.Errorf("link file assertion %s: %w", file, err)
		}
	}

	// 2. Link source document / message / literal evidence
	if f.SourceRef != "" {
		evType := "doc"
		if strings.HasPrefix(f.SourceRef, "msg:") || strings.HasPrefix(f.SourceRef, "message:") {
			evType = "message"
		} else if !strings.HasPrefix(f.SourceRef, "doc:") && (strings.Contains(f.SourceRef, ".") || strings.Contains(f.SourceRef, "/")) {
			evType = "file"
		}

		ref := ""
		if f.ByteStart >= 0 && f.ByteEnd > f.ByteStart {
			ref = fmt.Sprintf("bytes:%d-%d", f.ByteStart, f.ByteEnd)
		}

		a := Assertion{
			ProjectID:     f.ProjectID,
			StatementID:   f.ID,
			StatementType: statementType,
			EvidenceType:  evType,
			EvidenceID:    f.SourceRef,
			EvidenceRef:   ref,
			Confidence:    f.Confidence,
			CreatedAt:     f.FirstSeen,
		}
		if err := insertAssertionTx(tx, a); err != nil {
			return fmt.Errorf("link source assertion %s: %w", f.SourceRef, err)
		}
	}

	// 3. Link session or commit evidence if applicable
	if strings.HasPrefix(f.SessionRef, "commit:") {
		commitSHA := strings.TrimPrefix(f.SessionRef, "commit:")
		a := Assertion{
			ProjectID:     f.ProjectID,
			StatementID:   f.ID,
			StatementType: statementType,
			EvidenceType:  "git_commit",
			EvidenceID:    commitSHA,
			Confidence:    f.Confidence,
			CreatedAt:     f.FirstSeen,
		}
		if err := insertAssertionTx(tx, a); err != nil {
			return fmt.Errorf("link commit assertion %s: %w", commitSHA, err)
		}
	}

	return nil
}

// AssertionsForStatement retrieves all assertion evidence rows for a statement.
func (s *Store) AssertionsForStatement(statementID string) ([]Assertion, error) {
	rows, err := s.DB.Query(`
		SELECT id, project_id, statement_id, statement_type, evidence_type, evidence_id, evidence_ref, confidence, verified, verified_at, created_at, verification_error
		FROM assertions
		WHERE statement_id = ?
		ORDER BY created_at ASC
	`, statementID)
	if err != nil {
		return nil, fmt.Errorf("query assertions for statement %s: %w", statementID, err)
	}
	defer rows.Close()

	return scanAssertions(rows)
}

// AssertionsForEvidence retrieves all assertions that cite the given evidence.
func (s *Store) AssertionsForEvidence(evidenceType, evidenceID string) ([]Assertion, error) {
	var rows *sql.Rows
	var err error
	if evidenceType != "" {
		rows, err = s.DB.Query(`
			SELECT id, project_id, statement_id, statement_type, evidence_type, evidence_id, evidence_ref, confidence, verified, verified_at, created_at, verification_error
			FROM assertions
			WHERE evidence_type = ? AND evidence_id = ?
			ORDER BY created_at ASC
		`, evidenceType, evidenceID)
	} else {
		rows, err = s.DB.Query(`
			SELECT id, project_id, statement_id, statement_type, evidence_type, evidence_id, evidence_ref, confidence, verified, verified_at, created_at, verification_error
			FROM assertions
			WHERE evidence_id = ?
			ORDER BY created_at ASC
		`, evidenceID)
	}
	if err != nil {
		return nil, fmt.Errorf("query assertions for evidence %s:%s: %w", evidenceType, evidenceID, err)
	}
	defer rows.Close()

	return scanAssertions(rows)
}

// AssertionsByProject retrieves assertions for a project, optionally filtered by verified status.
func (s *Store) AssertionsByProject(projectID string, verifiedOnly bool, limit int) ([]Assertion, error) {
	if limit <= 0 {
		limit = 100
	}
	where := "WHERE project_id = ?"
	args := []any{projectID}
	if verifiedOnly {
		where += " AND verified = 1"
	}
	query := fmt.Sprintf(`
		SELECT id, project_id, statement_id, statement_type, evidence_type, evidence_id, evidence_ref, confidence, verified, verified_at, created_at, verification_error
		FROM assertions
		%s
		ORDER BY created_at DESC
		LIMIT %d
	`, where, limit)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query assertions by project %s: %w", projectID, err)
	}
	defer rows.Close()

	return scanAssertions(rows)
}

func scanAssertions(rows *sql.Rows) ([]Assertion, error) {
	var list []Assertion
	for rows.Next() {
		var a Assertion
		var verifiedInt int
		if err := rows.Scan(
			&a.ID, &a.ProjectID, &a.StatementID, &a.StatementType,
			&a.EvidenceType, &a.EvidenceID, &a.EvidenceRef,
			&a.Confidence, &verifiedInt, &a.VerifiedAt, &a.CreatedAt, &a.VerificationError,
		); err != nil {
			return nil, fmt.Errorf("scan assertion: %w", err)
		}
		a.Verified = (verifiedInt != 0)
		list = append(list, a)
	}
	return list, rows.Err()
}

// SearchFactsWithLineage searches facts and joins each hit with all assertion lineage,
// including unresolved references and their verification errors.
func (s *Store) SearchFactsWithLineage(query string, projectSel string, refsExistOnly bool, limit int) ([]FactWithLineage, error) {
	facts, err := s.SearchFactsFTS(query, projectSel, refsExistOnly, limit)
	if err != nil {
		return nil, err
	}

	result := make([]FactWithLineage, 0, len(facts))
	for _, f := range facts {
		assertions, err := s.AssertionsForStatement(f.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, FactWithLineage{
			Fact:       f,
			Assertions: assertions,
		})
	}
	return result, nil
}

// ReconcileAssertionLineage re-verifies every evidence type in a project.
// Failed or unsupported references lose their timestamp and retain an explicit reason.
func (s *Store) ReconcileAssertionLineage(projectRoot, projectID string) (int, error) {
	result, err := s.reconcileAssertionLineage(projectRoot, projectID, false)
	return result.Verified, err
}

type assertionReconcileResult struct {
	Checked  int
	Verified int
}

func (s *Store) reconcileAssertionLineage(projectRoot, projectID string, dryRun bool) (assertionReconcileResult, error) {
	var result assertionReconcileResult
	if err := s.validateAssertionProjectRoot(projectRoot, projectID); err != nil {
		return result, err
	}

	err := WithRetry(context.Background(), func() error {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		rows, err := tx.Query(`SELECT id, project_id, statement_id, statement_type, evidence_type,
			evidence_id, evidence_ref, confidence, verified, verified_at, created_at, verification_error
			FROM assertions WHERE project_id = ? ORDER BY id`, projectID)
		if err != nil {
			return fmt.Errorf("read assertions for reconcile: %w", err)
		}
		assertions, err := scanAssertions(rows)
		rows.Close()
		if err != nil {
			return err
		}
		result = assertionReconcileResult{Checked: len(assertions)}
		var stmt *sql.Stmt
		if !dryRun {
			stmt, err = tx.Prepare(`UPDATE assertions SET verified = ?, verified_at = ?, verification_error = ? WHERE id = ?`)
			if err != nil {
				return err
			}
			defer stmt.Close()
		}

		for _, a := range assertions {
			if err := verifyAssertionTx(tx, &a, projectRoot); err != nil {
				return err
			}
			verInt := 0
			if a.Verified {
				verInt = 1
				result.Verified++
			}
			if !dryRun {
				if _, err := stmt.Exec(verInt, a.VerifiedAt, a.VerificationError, a.ID); err != nil {
					return fmt.Errorf("update assertion %s verification: %w", a.ID, err)
				}
			}
		}

		if dryRun {
			return nil
		}
		return tx.Commit()
	})

	if err != nil {
		return assertionReconcileResult{}, err
	}
	return result, nil
}

func (s *Store) validateAssertionProjectRoot(projectRoot, projectID string) error {
	var registeredRoot string
	err := s.DB.QueryRow(`SELECT path FROM projects WHERE id = ?`, projectID).Scan(&registeredRoot)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("resolve assertion project: %w", err)
	}
	// Unregistered legacy projects remain inspectable, but their evidence cannot
	// receive verified status. A known project's root must agree before any writes.
	if err == nil && canonicalAssertionRoot(projectRoot) != canonicalAssertionRoot(registeredRoot) {
		return fmt.Errorf("assertion project root does not match registered project %q", projectID)
	}
	return nil
}

func canonicalAssertionRoot(root string) string {
	root, _ = filepath.Abs(root)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(root)
}

func verifyAssertionTx(tx *sql.Tx, a *Assertion, selectedRoot string) error {
	a.Verified = false
	a.VerifiedAt = ""
	a.VerificationError = ""
	var root string
	err := tx.QueryRow(`SELECT path FROM projects WHERE id = ?`, a.ProjectID).Scan(&root)
	if err == sql.ErrNoRows {
		a.VerificationError = "project_missing"
		return nil
	}
	if err != nil {
		return err
	}
	var statementProject string
	err = tx.QueryRow(`SELECT project_id FROM facts WHERE id = ?`, a.StatementID).Scan(&statementProject)
	if err == sql.ErrNoRows {
		a.VerificationError = "statement_missing"
		return nil
	}
	if err != nil {
		return err
	}
	if statementProject != a.ProjectID {
		a.VerificationError = "statement_project_mismatch"
		return nil
	}
	var contentBytes int64
	switch a.EvidenceType {
	case "doc":
		id := strings.TrimPrefix(a.EvidenceID, "doc:")
		err = tx.QueryRow(`SELECT length(CAST(content AS BLOB)) FROM docs WHERE id = ? AND project_id = ?`, id, a.ProjectID).Scan(&contentBytes)
	case "message":
		id := strings.TrimPrefix(strings.TrimPrefix(a.EvidenceID, "message:"), "msg:")
		if _, parseErr := strconv.ParseInt(id, 10, 64); parseErr != nil {
			a.VerificationError = "malformed_message_id"
			return nil
		}
		err = tx.QueryRow(`SELECT length(CAST(content AS BLOB)) FROM messages WHERE id = ? AND project_id = ?`, id, a.ProjectID).Scan(&contentBytes)
	case "file", "git_commit":
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			a.VerificationError = "project_unavailable"
			return nil
		}
		if selectedRoot != "" {
			selected, resolveErr := filepath.EvalSymlinks(selectedRoot)
			if resolveErr != nil || filepath.Clean(selected) != filepath.Clean(root) {
				a.VerificationError = "project_root_mismatch"
				return nil
			}
		}
		if a.EvidenceType == "git_commit" {
			a.VerificationError = verifyAssertionCommit(root, a.EvidenceID)
		} else {
			path := a.EvidenceID
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			path, err = filepath.EvalSymlinks(path)
			if err != nil {
				a.VerificationError = "file_unavailable"
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				a.VerificationError = "file_outside_project"
				return nil
			}
			info, statErr := os.Stat(path)
			if statErr != nil || !info.Mode().IsRegular() {
				a.VerificationError = "file_unavailable"
				return nil
			}
			file, readErr := os.Open(path)
			if readErr != nil {
				a.VerificationError = "file_unavailable"
				return nil
			}
			info, statErr = file.Stat()
			file.Close()
			if statErr != nil || !info.Mode().IsRegular() {
				a.VerificationError = "file_unavailable"
				return nil
			}
			contentBytes = info.Size()
			if a.EvidenceType == "file" {
				digest, hashErr := fileSHA256(path)
				if hashErr != nil {
					a.VerificationError = "file_unavailable"
					return nil
				}
				stateKey := "assertion_file_sha256:" + a.ID + ":" + rel
				stamp := "sha256:" + digest
				var prior string
				stateErr := tx.QueryRow(`SELECT value FROM state WHERE key = ?`, stateKey).Scan(&prior)
				if stateErr == sql.ErrNoRows {
					_, _ = tx.Exec(`INSERT INTO state(key, value, updated_at) VALUES(?, ?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, stateKey, stamp, time.Now().UTC().Format(time.RFC3339))
				} else if stateErr != nil {
					return stateErr
				} else if prior != stamp {
					a.VerificationError = "file_content_drift"
					return nil
				}
			}
		}
	default:
		a.VerificationError = "unsupported_evidence_type"
		return nil
	}
	if err == sql.ErrNoRows {
		a.VerificationError = "evidence_missing_in_project"
		return nil
	}
	if err != nil {
		return err
	}
	if a.VerificationError != "" {
		return nil
	}
	if a.EvidenceRef != "" {
		span := strings.SplitN(strings.TrimPrefix(a.EvidenceRef, "bytes:"), "-", 2)
		if !strings.HasPrefix(a.EvidenceRef, "bytes:") || len(span) != 2 {
			a.VerificationError = "malformed_evidence_span"
			return nil
		}
		start, startErr := strconv.Atoi(span[0])
		end, endErr := strconv.Atoi(span[1])
		if startErr != nil || endErr != nil || start < 0 || end <= start || int64(end) > contentBytes {
			a.VerificationError = "invalid_evidence_span"
			return nil
		}
	}
	a.Verified = true
	a.VerifiedAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// Git evidence must be a commit in this exact repository, reachable from HEAD
// or one of its refs. Object presence alone does not establish reachability.
func verifyAssertionCommit(root, id string) string {
	if len(id) < 7 || len(id) > 64 || strings.Trim(id, "0") == "" {
		return "malformed_commit_id"
	}
	if _, err := hex.DecodeString(id + strings.Repeat("0", len(id)%2)); err != nil {
		return "malformed_commit_id"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		// Git environment overrides must not redirect evidence into another repo.
		cmd.Env = make([]string, 0, len(os.Environ()))
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GIT_") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		return cmd.Output()
	}
	top, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return "repository_unavailable"
	}
	canonicalTop, err := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
	if err != nil || canonicalTop != root {
		return "repository_root_mismatch"
	}
	resolved, err := run("rev-parse", "--verify", id+"^{commit}")
	if err != nil {
		return "commit_unresolved"
	}
	sha := strings.TrimSpace(string(resolved))
	if _, err := run("merge-base", "--is-ancestor", sha, "HEAD"); err == nil {
		return ""
	}
	refs, err := run("for-each-ref", "--contains="+sha, "--format=%(refname)")
	if err == nil && len(strings.TrimSpace(string(refs))) > 0 {
		return ""
	}
	return "commit_unreachable"
}
