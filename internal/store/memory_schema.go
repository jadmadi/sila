package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func init() {
	migrations = append(migrations, migration{
		version: 41,
		name:    "typed_memory_envelope",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS memory_records (
					id              TEXT PRIMARY KEY,
					project_id      TEXT NOT NULL,
					fact_id         TEXT NOT NULL,
					kind            TEXT NOT NULL,
					title           TEXT NOT NULL,
					summary         TEXT NOT NULL,
					digest          TEXT NOT NULL,
					idempotency_key TEXT,
					envelope_json   TEXT NOT NULL,
					state           TEXT NOT NULL DEFAULT 'current',
					created_at      TEXT NOT NULL,
					updated_at      TEXT NOT NULL
				);`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_project_digest
					ON memory_records(project_id, digest);`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_idempotency
					ON memory_records(project_id, idempotency_key)
					WHERE idempotency_key IS NOT NULL;`,
				`CREATE INDEX IF NOT EXISTS idx_memory_project_kind
					ON memory_records(project_id, kind, state);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_fact
					ON memory_records(fact_id);`,
				`CREATE TABLE IF NOT EXISTS memory_assertions (
					memory_id       TEXT NOT NULL,
					assertion_id    TEXT NOT NULL,
					assertion_type  TEXT NOT NULL,
					predicate       TEXT NOT NULL,
					statement       TEXT NOT NULL,
					subject_json    TEXT NOT NULL,
					object_json     TEXT NOT NULL,
					confidence      REAL NOT NULL,
					importance      INTEGER NOT NULL,
					PRIMARY KEY (memory_id, assertion_id)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_assertions_predicate
					ON memory_assertions(predicate);`,
				`CREATE TABLE IF NOT EXISTS memory_evidence (
					memory_id    TEXT NOT NULL,
					evidence_id  TEXT NOT NULL,
					kind         TEXT NOT NULL,
					locator_json TEXT NOT NULL,
					summary      TEXT NOT NULL,
					sha256       TEXT,
					grounded     INTEGER NOT NULL DEFAULT 1,
					PRIMARY KEY (memory_id, evidence_id)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_evidence_kind
					ON memory_evidence(kind);`,
				`CREATE TABLE IF NOT EXISTS memory_relations (
					memory_id        TEXT NOT NULL,
					relation_type    TEXT NOT NULL,
					target_memory_id TEXT NOT NULL,
					evidence_json    TEXT NOT NULL DEFAULT '[]',
					PRIMARY KEY (memory_id, relation_type, target_memory_id)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_relations_target
					ON memory_relations(target_memory_id);`,
				`CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
					memory_id UNINDEXED,
					project_id UNINDEXED,
					title,
					summary,
					statements,
					tokenize='unicode61'
				);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

// MemoryRecord is a stored envelope plus derived query metadata.
type MemoryRecord struct {
	ID             string             `json:"id"`
	ProjectID      string             `json:"project_id"`
	FactID         string             `json:"fact_id"`
	Kind           string             `json:"kind"`
	Title          string             `json:"title"`
	Summary        string             `json:"summary"`
	Digest         string             `json:"digest"`
	IdempotencyKey string             `json:"idempotency_key,omitempty"`
	EnvelopeJSON   string             `json:"envelope_json"`
	State          string             `json:"state"`
	CreatedAt      string             `json:"created_at"`
	UpdatedAt      string             `json:"updated_at"`
	Authority      string             `json:"authority"`
	Grounded       bool               `json:"grounded"`
	Superseded     bool               `json:"superseded"`
	MissingAtWrite bool               `json:"missing_at_write"`
	Rank           float64            `json:"rank,omitempty"`
	Envelope       *MemoryEnvelope    `json:"envelope,omitempty"`
	Diagnostics    []MemoryDiagnostic `json:"diagnostics,omitempty"`
}

const memoryRecordSelect = `SELECT r.id, r.project_id, r.fact_id, r.kind, r.title, r.summary,
	r.digest, COALESCE(r.idempotency_key, ''), r.envelope_json, r.state, r.created_at, r.updated_at,
	EXISTS(SELECT 1 FROM memory_evidence e WHERE e.memory_id = r.id AND e.kind = 'user_attestation'),
	NOT EXISTS(SELECT 1 FROM memory_evidence e WHERE e.memory_id = r.id AND e.grounded = 0),
	EXISTS(SELECT 1 FROM memory_relations x WHERE x.target_memory_id = r.id AND x.relation_type = 'supersedes'),
	EXISTS(SELECT 1 FROM memory_evidence e WHERE e.memory_id = r.id AND e.grounded = 0)
	FROM memory_records r`

func scanMemoryRecord(scanner interface{ Scan(...any) error }) (*MemoryRecord, error) {
	var rec MemoryRecord
	var userAttested, grounded, superseded, missing int
	if err := scanner.Scan(
		&rec.ID, &rec.ProjectID, &rec.FactID, &rec.Kind, &rec.Title, &rec.Summary,
		&rec.Digest, &rec.IdempotencyKey, &rec.EnvelopeJSON, &rec.State,
		&rec.CreatedAt, &rec.UpdatedAt, &userAttested, &grounded, &superseded, &missing,
	); err != nil {
		return nil, err
	}
	rec.Authority = "agent_save"
	if userAttested != 0 {
		rec.Authority = "user_attested"
	}
	rec.Grounded = grounded != 0
	rec.Superseded = superseded != 0
	rec.MissingAtWrite = missing != 0
	if rec.EnvelopeJSON != "" {
		if env, err := DecodeMemoryEnvelope([]byte(rec.EnvelopeJSON)); err == nil {
			rec.Envelope = env
		} else {
			rec.Diagnostics = append(rec.Diagnostics, MemoryDiagnostic{
				Code: MemoryCodeJSONInvalid, Field: "envelope_json", Message: "stored envelope cannot be decoded",
			})
		}
	}
	return &rec, nil
}

// SaveMemoryEnvelope validates and persists one envelope atomically with its
// bridge fact row, assertions, evidence, relations and FTS entry.
func (s *Store) SaveMemoryEnvelope(ctx context.Context, projectID string, env *MemoryEnvelope, prov Provenance) (*MemoryRecord, bool, error) {
	if s == nil || s.DB == nil {
		return nil, false, errors.New("memory save: store is nil")
	}
	if s.ReadOnly {
		return nil, false, errors.New("memory save: store is read-only")
	}
	if env == nil {
		return nil, false, memoryErr(MemoryCodeJSONInvalid, "", "nil envelope")
	}
	if env.SchemaVersion == "" {
		env.SchemaVersion = MemorySchemaVersion
	}
	env.ProjectID = strings.TrimSpace(env.ProjectID)
	if env.ProjectID == "" {
		env.ProjectID = strings.TrimSpace(projectID)
	}
	if env.ProjectID == "" {
		return nil, false, memoryErr(MemoryCodeProjectUnresolved, "project_id", "no project id resolved; pass --project")
	}
	if normalized, ok := normalizeMemoryKind(env.Kind); ok {
		env.Kind = normalized
	}
	if env.MemoryID == "" {
		env.MemoryID = "mem_" + NewV7UUID()
	}
	if env.CreatedAt == "" {
		env.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	env.UpdatedAt = env.CreatedAt
	projectDir := s.memoryProjectDir(env.ProjectID)
	if diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{ProjectDir: projectDir}); len(diags) > 0 {
		return nil, false, &MemoryValidationError{Diagnostics: diags}
	}
	digest, err := MemoryDigest(env)
	if err != nil {
		return nil, false, err
	}
	contentDigest, err := MemoryContentDigest(env)
	if err != nil {
		return nil, false, err
	}

	factMu.Lock()
	defer factMu.Unlock()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin memory save tx: %w", err)
	}
	defer tx.Rollback()

	existingID, existingDigest, err := memoryIdempotentLookupTx(tx, env)
	if err != nil {
		return nil, false, err
	}
	if existingID != "" {
		if existingDigest == contentDigest {
			rec, err := memoryRecordByIDTx(tx, existingID)
			if err != nil {
				return nil, false, err
			}
			if err := tx.Commit(); err != nil {
				return nil, false, err
			}
			return rec, false, nil
		}
		return nil, false, memoryErr(MemoryCodeIdempotency, "idempotency_key", "key %q is already bound to a different memory", env.IdempotencyKey)
	}

	duplicateID, diagnostics, err := memoryContentMatchesTx(tx, env, contentDigest)
	if err != nil {
		return nil, false, err
	}
	if duplicateID != "" {
		return nil, false, memoryErr(MemoryCodeDuplicate, "digest", "an identical memory already exists: %s", duplicateID)
	}
	if err := validateMemoryRelationsTx(tx, env); err != nil {
		return nil, false, err
	}

	bridge := memoryBridgeFact(env, prov)
	factID, err := upsertFactInTx(tx, bridge)
	if err != nil {
		return nil, false, err
	}
	if err := insertMemoryRowsTx(tx, env, factID, digest); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	rec, err := s.MemoryRecordByID(env.MemoryID)
	if err != nil {
		return nil, false, err
	}
	rec.Diagnostics = diagnostics
	return rec, true, nil
}

// saveFactWithMemoryEnvelope persists a caller-built bridge fact together with
// a synthesized envelope in one transaction. Legacy memo/lesson/case writers
// use it so they fail closed rather than leaving an untyped fact behind.
func (s *Store) saveFactWithMemoryEnvelope(bridge Fact, env *MemoryEnvelope) (*MemoryRecord, bool, error) {
	if s == nil || s.DB == nil {
		return nil, false, errors.New("memory save: store is nil")
	}
	if s.ReadOnly {
		return nil, false, errors.New("memory save: store is read-only")
	}
	if env == nil {
		return nil, false, memoryErr(MemoryCodeJSONInvalid, "", "nil envelope")
	}
	if env.SchemaVersion == "" {
		env.SchemaVersion = MemorySchemaVersion
	}
	if env.ProjectID == "" {
		env.ProjectID = bridge.ProjectID
	}
	if normalized, ok := normalizeMemoryKind(env.Kind); ok {
		env.Kind = normalized
	}
	if env.MemoryID == "" {
		env.MemoryID = "mem_" + NewV7UUID()
	}
	if env.CreatedAt == "" {
		env.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	env.UpdatedAt = env.CreatedAt
	if diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{ProjectDir: s.memoryProjectDir(env.ProjectID)}); len(diags) > 0 {
		return nil, false, &MemoryValidationError{Diagnostics: diags}
	}
	digest, err := MemoryDigest(env)
	if err != nil {
		return nil, false, err
	}
	contentDigest, err := MemoryContentDigest(env)
	if err != nil {
		return nil, false, err
	}

	factMu.Lock()
	defer factMu.Unlock()

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, false, fmt.Errorf("begin legacy memory save tx: %w", err)
	}
	defer tx.Rollback()

	existingID, existingDigest, err := memoryIdempotentLookupTx(tx, env)
	if err != nil {
		return nil, false, err
	}
	if existingID != "" {
		if existingDigest != contentDigest {
			return nil, false, memoryErr(MemoryCodeIdempotency, "idempotency_key", "key %q is already bound to a different memory", env.IdempotencyKey)
		}
		if _, err := upsertFactInTx(tx, bridge); err != nil {
			return nil, false, err
		}
		rec, err := memoryRecordByIDTx(tx, existingID)
		if err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return rec, false, nil
	}

	duplicateID, _, err := memoryContentMatchesTx(tx, env, contentDigest)
	if err != nil {
		return nil, false, err
	}
	if duplicateID != "" {
		return nil, false, memoryErr(MemoryCodeDuplicate, "digest", "an identical memory already exists: %s", duplicateID)
	}
	if err := validateMemoryRelationsTx(tx, env); err != nil {
		return nil, false, err
	}
	factID, err := upsertFactInTx(tx, bridge)
	if err != nil {
		return nil, false, err
	}
	if err := insertMemoryRowsTx(tx, env, factID, digest); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	rec, err := s.MemoryRecordByID(env.MemoryID)
	if err != nil {
		return nil, false, err
	}
	return rec, true, nil
}

func (s *Store) memoryProjectDir(projectID string) string {
	projects, err := s.Projects()
	if err != nil {
		return ""
	}
	for _, p := range projects {
		if p.ID == projectID {
			return p.Path
		}
	}
	return ""
}

func memoryIdempotentLookupTx(tx *sql.Tx, env *MemoryEnvelope) (string, string, error) {
	if env.IdempotencyKey == "" {
		return "", "", nil
	}
	var id, raw string
	err := tx.QueryRow(`SELECT id, envelope_json FROM memory_records WHERE project_id = ? AND idempotency_key = ?`,
		env.ProjectID, env.IdempotencyKey).Scan(&id, &raw)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	existing, decodeErr := DecodeMemoryEnvelope([]byte(raw))
	if decodeErr != nil {
		return id, "corrupt", nil
	}
	contentDigest, err := MemoryContentDigest(existing)
	if err != nil {
		return "", "", err
	}
	return id, contentDigest, nil
}

// memoryContentMatchesTx finds an exact content duplicate and reports near
// duplicates without failing the write.
func memoryContentMatchesTx(tx *sql.Tx, env *MemoryEnvelope, contentDigest string) (string, []MemoryDiagnostic, error) {
	rows, err := tx.Query(`SELECT id, envelope_json FROM memory_records WHERE project_id = ? ORDER BY created_at DESC LIMIT 200`, env.ProjectID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	candidate := env.Title + " " + env.Summary
	var diagnostics []MemoryDiagnostic
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return "", nil, err
		}
		existing, decodeErr := DecodeMemoryEnvelope([]byte(raw))
		if decodeErr != nil {
			continue
		}
		existingDigest, err := MemoryContentDigest(existing)
		if err != nil {
			continue
		}
		if existingDigest == contentDigest {
			return id, diagnostics, nil
		}
		if score := memorySimilarity(candidate, existing.Title+" "+existing.Summary); score >= 0.6 {
			diagnostics = append(diagnostics, MemoryDiagnostic{
				Code:    MemoryCodeSimilar,
				Field:   "summary",
				Message: fmt.Sprintf("similar to memory %s (score %.2f); write proceeds", id, score),
			})
			if len(diagnostics) >= 3 {
				break
			}
		}
	}
	return "", diagnostics, rows.Err()
}

func validateMemoryRelationsTx(tx *sql.Tx, env *MemoryEnvelope) error {
	for _, rel := range env.Relations {
		var factID string
		err := tx.QueryRow(`SELECT fact_id FROM memory_records WHERE id = ? AND project_id = ?`,
			rel.TargetMemoryID, env.ProjectID).Scan(&factID)
		if err == sql.ErrNoRows {
			return memoryErr(MemoryCodeRelationTarget, "relations.target_memory_id", "target %q does not exist in project %q", rel.TargetMemoryID, env.ProjectID)
		}
		if err != nil {
			return err
		}
		if rel.Type != MemoryRelationSupersedes {
			continue
		}
		seen := map[string]bool{rel.TargetMemoryID: true}
		cursor := rel.TargetMemoryID
		for cursor != "" {
			if cursor == env.MemoryID {
				return memoryErr(MemoryCodeRelationCycle, "relations.target_memory_id", "supersedes relation forms a cycle")
			}
			var next string
			err := tx.QueryRow(`SELECT target_memory_id FROM memory_relations WHERE memory_id = ? AND relation_type = 'supersedes'`, cursor).Scan(&next)
			if err == sql.ErrNoRows {
				break
			}
			if err != nil {
				return err
			}
			if seen[next] {
				break
			}
			seen[next] = true
			cursor = next
		}
	}
	return nil
}

// memoryFactKind maps an envelope kind onto the legacy facts kind vocabulary.
func memoryFactKind(kind string) string {
	if kind == MemoryKindArchitectureDecision {
		return "decision"
	}
	return kind
}

func memoryBridgeFact(env *MemoryEnvelope, prov Provenance) Fact {
	now := time.Now().UTC().Format(time.RFC3339)
	claim := env.Summary
	repoFiles := memoryRepoFiles(env)
	confidence := "grounded"
	if MemoryAuthority(env) == "user_attested" {
		confidence = "corroborated"
	}
	sessionRef := prov.SessionRef
	if sessionRef == "" {
		sessionRef = "memory_envelope"
	}
	originRole := prov.OriginRole
	if originRole == "" {
		originRole = "user"
	}
	supersedesFact := ""
	if env.MemoryID != "" {
		// Filled by insertMemoryRowsTx through SupersedesID when a relation
		// exists; the zero value keeps ordinary saves unchanged.
		supersedesFact = ""
	}
	fact := Fact{
		ProjectID:     env.ProjectID,
		Claim:         claim,
		Kind:          memoryFactKind(env.Kind),
		Confidence:    confidence,
		Corroboration: confidence,
		Verified:      MemoryGrounded(env),
		FileRefsExist: MemoryGrounded(env),
		RepoFiles:     repoFiles,
		Tools:         []string{firstNonEmptyMemoryString(prov.ToolName, envProvenanceAgent(env), "user")},
		SessionRef:    sessionRef,
		FirstSeen:     now,
		PromotedAt:    now,
		RefsCheckedAt: now,
		Distilled:     env.Summary,
		DistillStatus: "distilled",
		ReviewStatus:  ReviewApproved,
		OriginRole:    originRole,
		ToolName:      prov.ToolName,
		ClientKind:    prov.ClientKind,
		OriginEntity:  prov.OriginEntity,
		SupersedesID:  supersedesFact,
		Tier:          string(TierSemantic),
	}
	return fact
}

func envProvenanceAgent(env *MemoryEnvelope) string {
	if env != nil && env.Provenance != nil {
		return env.Provenance.Agent
	}
	return ""
}

func firstNonEmptyMemoryString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func memoryRepoFiles(env *MemoryEnvelope) []string {
	seen := map[string]bool{}
	var out []string
	for _, ev := range env.Evidence {
		if ev.Locator.Path == "" || seen[ev.Locator.Path] {
			continue
		}
		seen[ev.Locator.Path] = true
		out = append(out, ev.Locator.Path)
		if len(out) >= 32 {
			break
		}
	}
	sort.Strings(out)
	return out
}

func insertMemoryRowsTx(tx *sql.Tx, env *MemoryEnvelope, factID, digest string) error {
	envelopeJSON, err := EncodeMemoryEnvelope(env)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.Exec(`INSERT INTO memory_records(id, project_id, fact_id, kind, title, summary, digest, idempotency_key, envelope_json, state, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		env.MemoryID, env.ProjectID, factID, env.Kind, env.Title, env.Summary, digest,
		memoryNullString(env.IdempotencyKey), string(envelopeJSON), "current", now, now)
	if err != nil {
		return fmt.Errorf("insert memory record: %w", err)
	}

	var statements []string
	for _, a := range env.Assertions {
		subjectJSON, _ := json.Marshal(a.Subject)
		objectJSON, _ := json.Marshal(a.Object)
		if _, err := tx.Exec(`INSERT INTO memory_assertions(memory_id, assertion_id, assertion_type, predicate, statement, subject_json, object_json, confidence, importance)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			env.MemoryID, a.ID, a.AssertionType, a.Predicate, a.Statement, string(subjectJSON), string(objectJSON), a.Confidence, a.Importance); err != nil {
			return fmt.Errorf("insert memory assertion %s: %w", a.ID, err)
		}
		statements = append(statements, a.Statement)
	}
	for _, ev := range env.Evidence {
		locatorJSON, _ := json.Marshal(ev.Locator)
		grounded := 1
		if ev.MissingAtWrite {
			grounded = 0
		}
		if _, err := tx.Exec(`INSERT INTO memory_evidence(memory_id, evidence_id, kind, locator_json, summary, sha256, grounded)
			VALUES(?,?,?,?,?,?,?)`,
			env.MemoryID, ev.ID, ev.Kind, string(locatorJSON), ev.Summary, memoryNullString(ev.SHA256), grounded); err != nil {
			return fmt.Errorf("insert memory evidence %s: %w", ev.ID, err)
		}
	}
	for _, rel := range env.Relations {
		refsJSON, _ := json.Marshal(rel.EvidenceRefs)
		if _, err := tx.Exec(`INSERT INTO memory_relations(memory_id, relation_type, target_memory_id, evidence_json)
			VALUES(?,?,?,?)`,
			env.MemoryID, rel.Type, rel.TargetMemoryID, string(refsJSON)); err != nil {
			return fmt.Errorf("insert memory relation %s: %w", rel.Type, err)
		}
		if rel.Type == MemoryRelationSupersedes {
			if _, err := tx.Exec(`UPDATE facts SET supersedes_id = (SELECT fact_id FROM memory_records WHERE id = ?) WHERE id = ?`,
				rel.TargetMemoryID, factID); err != nil {
				return fmt.Errorf("link supersedes fact: %w", err)
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO memory_fts(memory_id, project_id, title, summary, statements) VALUES(?,?,?,?,?)`,
		env.MemoryID, env.ProjectID, env.Title, env.Summary, strings.Join(statements, "\n")); err != nil {
		return fmt.Errorf("insert memory fts: %w", err)
	}
	return nil
}

func memoryNullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// MemoryRecordByID loads one record and its envelope.
func (s *Store) MemoryRecordByID(id string) (*MemoryRecord, error) {
	row := s.DB.QueryRow(memoryRecordSelect+` WHERE r.id = ?`, id)
	rec, err := scanMemoryRecord(row)
	if err == sql.ErrNoRows {
		return nil, memoryErr(MemoryCodeBound, "memory_id", "memory %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

func memoryRecordByIDTx(tx *sql.Tx, id string) (*MemoryRecord, error) {
	row := tx.QueryRow(memoryRecordSelect+` WHERE r.id = ?`, id)
	return scanMemoryRecord(row)
}

// MemoryListOptions filters memory list queries.
type MemoryListOptions struct {
	ProjectID         string
	Kind              string
	Since             time.Time
	Limit             int
	IncludeSuperseded bool
	IncludeArchived   bool
}

// ListMemoryRecords returns typed records newest first.
func (s *Store) ListMemoryRecords(opts MemoryListOptions) ([]MemoryRecord, error) {
	query := memoryRecordSelect
	var where []string
	var args []any
	if opts.ProjectID != "" && opts.ProjectID != "*" {
		where = append(where, "r.project_id = ?")
		args = append(args, opts.ProjectID)
	}
	if opts.Kind != "" {
		where = append(where, "r.kind = ?")
		args = append(args, opts.Kind)
	}
	if !opts.Since.IsZero() {
		where = append(where, "r.created_at >= ?")
		args = append(args, opts.Since.UTC().Format(time.RFC3339))
	}
	if !opts.IncludeArchived {
		if opts.IncludeSuperseded {
			where = append(where, "r.state != 'archived'")
		} else {
			where = append(where, "r.state = 'current'")
			where = append(where, `NOT EXISTS(SELECT 1 FROM memory_relations x WHERE x.target_memory_id = r.id AND x.relation_type = 'supersedes')`)
		}
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY r.created_at DESC, r.id DESC"
	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	query += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryRecord
	for rows.Next() {
		rec, err := scanMemoryRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// SearchMemoryRecords runs FTS5 over title, summary and assertion statements.
// Archived and superseded records are excluded by default.
func (s *Store) SearchMemoryRecords(query, projectID string, limit int) ([]MemoryRecord, error) {
	return s.searchMemoryRecords(query, projectID, limit, false)
}

// SearchMemoryRecordsIncludingArchived is SearchMemoryRecords with an explicit
// opt-in for archived and superseded records.
func (s *Store) SearchMemoryRecordsIncludingArchived(query, projectID string, limit int, includeArchived bool) ([]MemoryRecord, error) {
	return s.searchMemoryRecords(query, projectID, limit, includeArchived)
}

func (s *Store) searchMemoryRecords(query, projectID string, limit int, includeArchived bool) ([]MemoryRecord, error) {
	ftsQuery := FormatFTS5Query(query)
	if ftsQuery == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 10
	}
	sqlQuery := `SELECT r.id, r.project_id, r.fact_id, r.kind, r.title, r.summary,
		r.digest, COALESCE(r.idempotency_key, ''), r.envelope_json, r.state, r.created_at, r.updated_at,
		EXISTS(SELECT 1 FROM memory_evidence e WHERE e.memory_id = r.id AND e.kind = 'user_attestation'),
		NOT EXISTS(SELECT 1 FROM memory_evidence e WHERE e.memory_id = r.id AND e.grounded = 0),
		EXISTS(SELECT 1 FROM memory_relations x WHERE x.target_memory_id = r.id AND x.relation_type = 'supersedes'),
		EXISTS(SELECT 1 FROM memory_evidence e WHERE e.memory_id = r.id AND e.grounded = 0),
		bm25(memory_fts)
		FROM memory_fts f JOIN memory_records r ON r.id = f.memory_id
		WHERE (? = '' OR r.project_id = ?) AND memory_fts MATCH ?`
	args := []any{projectID, projectID, ftsQuery}
	if !includeArchived {
		sqlQuery += ` AND r.state = 'current'
			AND NOT EXISTS(SELECT 1 FROM memory_relations x WHERE x.target_memory_id = r.id AND x.relation_type = 'supersedes')`
	}
	sqlQuery += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryRecord
	for rows.Next() {
		var rec MemoryRecord
		var userAttested, grounded, superseded, missing int
		if err := rows.Scan(
			&rec.ID, &rec.ProjectID, &rec.FactID, &rec.Kind, &rec.Title, &rec.Summary,
			&rec.Digest, &rec.IdempotencyKey, &rec.EnvelopeJSON, &rec.State,
			&rec.CreatedAt, &rec.UpdatedAt, &userAttested, &grounded, &superseded, &missing, &rec.Rank,
		); err != nil {
			return nil, err
		}
		rec.Authority = "agent_save"
		if userAttested != 0 {
			rec.Authority = "user_attested"
		}
		rec.Grounded = grounded != 0
		rec.Superseded = superseded != 0
		rec.MissingAtWrite = missing != 0
		if env, err := DecodeMemoryEnvelope([]byte(rec.EnvelopeJSON)); err == nil {
			rec.Envelope = env
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// MemoryStats summarizes a project's typed memory for list --json.
type MemoryStats struct {
	Total          int            `json:"total"`
	ByKind         map[string]int `json:"by_kind"`
	Grounded       int            `json:"grounded"`
	Ungrounded     int            `json:"ungrounded"`
	AgentSave      int            `json:"agent_save"`
	UserAttested   int            `json:"user_attested"`
	Superseded     int            `json:"superseded"`
	MissingAtWrite int            `json:"missing_at_write"`
}

// ComputeMemoryStats aggregates records without further store reads.
func ComputeMemoryStats(records []MemoryRecord) MemoryStats {
	stats := MemoryStats{ByKind: map[string]int{}}
	for _, rec := range records {
		stats.Total++
		stats.ByKind[rec.Kind]++
		if rec.Grounded {
			stats.Grounded++
		} else {
			stats.Ungrounded++
		}
		if rec.Authority == "user_attested" {
			stats.UserAttested++
		} else {
			stats.AgentSave++
		}
		if rec.Superseded {
			stats.Superseded++
		}
		if rec.MissingAtWrite {
			stats.MissingAtWrite++
		}
	}
	return stats
}

// MemoryDiagnostics is a content-free health report for migration 41 tables.
type MemoryDiagnostics struct {
	SchemaVersion         int  `json:"schema_version"`
	Records               int  `json:"records"`
	OrphanedAssertions    int  `json:"orphaned_assertions"`
	OrphanedEvidence      int  `json:"orphaned_evidence"`
	OrphanedRelations     int  `json:"orphaned_relations"`
	BrokenRelationTargets int  `json:"broken_relation_targets"`
	CorruptEnvelopes      int  `json:"corrupt_envelopes"`
	FTSMissing            int  `json:"fts_missing"`
	FTSHealthy            bool `json:"fts_healthy"`
	LifecycleEvents       int  `json:"lifecycle_events"`
	OrphanLifecycleEvents int  `json:"orphan_lifecycle_events"`
	BrokenLifecycleChains int  `json:"broken_lifecycle_chains"`
	StateIndexMismatches  int  `json:"state_index_mismatches"`
}

// RebuildMemoryFTS repopulates memory_fts from stored envelopes. It only
// touches the index, never the records.
func (s *Store) RebuildMemoryFTS() error {
	if s == nil || s.DB == nil || s.ReadOnly {
		return nil
	}
	rows, err := s.DB.Query(`SELECT id, project_id, envelope_json FROM memory_records`)
	if err != nil {
		return err
	}
	type ftsRow struct {
		id, project, title, summary, statements string
	}
	var rowsToIndex []ftsRow
	for rows.Next() {
		var id, project, raw string
		if err := rows.Scan(&id, &project, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		env, err := DecodeMemoryEnvelope([]byte(raw))
		if err != nil {
			continue
		}
		var statements []string
		for _, a := range env.Assertions {
			statements = append(statements, a.Statement)
		}
		rowsToIndex = append(rowsToIndex, ftsRow{id, project, env.Title, env.Summary, strings.Join(statements, "\n")})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM memory_fts`); err != nil {
		return err
	}
	for _, row := range rowsToIndex {
		if _, err := tx.Exec(`INSERT INTO memory_fts(memory_id, project_id, title, summary, statements) VALUES(?,?,?,?,?)`,
			row.id, row.project, row.title, row.summary, row.statements); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RepairMemoryIndexes removes unreachable child rows and rebuilds memory_fts.
// It never deletes a memory record, assertion statement, evidence locator or
// relation that is still reachable from a record.
func (s *Store) RepairMemoryIndexes() ([]string, error) {
	if s == nil || s.DB == nil || s.ReadOnly {
		return nil, nil
	}
	var repairs []string
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, orphan := range []struct {
		label string
		query string
	}{
		{"assertions", `DELETE FROM memory_assertions WHERE memory_id NOT IN (SELECT id FROM memory_records)`},
		{"evidence", `DELETE FROM memory_evidence WHERE memory_id NOT IN (SELECT id FROM memory_records)`},
		{"relations", `DELETE FROM memory_relations WHERE memory_id NOT IN (SELECT id FROM memory_records)`},
	} {
		res, err := tx.Exec(orphan.query)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			repairs = append(repairs, fmt.Sprintf("Pruned %d orphaned memory %s row(s)", n, orphan.label))
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.RebuildMemoryFTS(); err != nil {
		return repairs, err
	}
	if report, err := s.MemoryDiagnostics(); err == nil && report.FTSMissing == 0 && report.Records > 0 {
		repairs = append(repairs, fmt.Sprintf("Rebuilt memory_fts index (%d record(s))", report.Records))
	}
	return repairs, nil
}
