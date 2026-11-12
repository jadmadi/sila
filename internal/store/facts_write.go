package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrFactImmutable is returned when an upsert tries to change a hashed field
// on an existing fact. The ledger is append-only: corrections must create a
// new fact that supersedes the old one.
var ErrFactImmutable = errors.New("fact hashed fields are immutable")

// FactImmutabilityError names the field that would have broken the chain.
type FactImmutabilityError struct {
	ID       string
	Field    string
	Existing string
	Incoming string
}

func (e *FactImmutabilityError) Error() string {
	return fmt.Sprintf("fact %s: field %q is immutable (existing %q, incoming %q); create a superseding fact instead", e.ID, e.Field, e.Existing, e.Incoming)
}

func (e *FactImmutabilityError) Unwrap() error { return ErrFactImmutable }

// guardImmutableFields rejects the three hashed fields the upsert SQL would
// actually overwrite. project_id, first_seen, and session_ref are insert-only
// in the conflict clause, so they cannot break an existing link.
func guardImmutableFields(id, existingClaim, existingKind, existingOriginRole string, f Fact) error {
	if f.Claim != existingClaim {
		return &FactImmutabilityError{ID: id, Field: "claim", Existing: existingClaim, Incoming: f.Claim}
	}
	if f.Kind != "" && f.Kind != existingKind {
		return &FactImmutabilityError{ID: id, Field: "kind", Existing: existingKind, Incoming: f.Kind}
	}
	return nil
}

// SupersedeFact appends a new fact that replaces oldID and marks the old fact
// stale. The old row is never deleted and its hashed fields are never mutated,
// so the append-only chain stays valid.
func (s *Store) SupersedeFact(oldID string, f Fact) (*Fact, error) {
	if strings.TrimSpace(oldID) == "" {
		return nil, fmt.Errorf("supersede fact: old ID is required")
	}
	f.ID = FactID(f.ProjectID, f.Claim)
	f.FactHash = ""
	f.PrevHash = ""
	f.SupersedesID = oldID
	if err := s.UpsertFact(f); err != nil {
		return nil, err
	}
	if err := s.MarkFactStale(oldID); err != nil {
		return nil, err
	}
	return s.FactByID(f.ID)
}

// MarkFactStale flips the non-hashed is_stale flag. It never touches content
// or hashes, so it cannot break the ledger chain.
func (s *Store) MarkFactStale(id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	_, err := s.DB.Exec(`UPDATE facts SET is_stale = 1 WHERE id = ?`, id)
	return err
}

// UpsertFact inserts a fact or updates it in place while preserving cryptographic chain determinism.
func (s *Store) UpsertFact(f Fact) error {
	factMu.Lock()
	defer factMu.Unlock()

	if mergeID, veto, guardErr := s.nearDuplicateGuard(f); guardErr == nil && mergeID != "" && len(veto) == 0 {
		return s.mergeFactEvidence(mergeID, f)
	}
	return WithRetry(context.Background(), func() error {
		return s.upsertFactTx(f)
	})
}

func (s *Store) upsertFactTx(f Fact) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin upsert fact tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := upsertFactInTx(tx, f); err != nil {
		return err
	}
	return tx.Commit()
}

// upsertFactInTx writes one fact and repairs the hash chain inside a caller
// transaction. It is the single implementation shared by plain fact writes and
// typed-memory bridge writes so the ledger can never diverge.
func upsertFactInTx(tx *sql.Tx, f Fact) (string, error) {
	f.PackProvenance()
	visibility, visErr := NormalizeVisibility(f.Visibility)
	if visErr != nil {
		return "", visErr
	}
	f.Visibility = visibility
	if f.ID == "" {
		f.ID = FactID(f.ProjectID, f.Claim)
	}

	// Invariant check: if a fact already exists with (project_id, claim),
	// preserve its canonical ID so the update in place honors both primary key and UNIQUE(project_id, claim).
	var existingID, existingClaim, existingKind, existingOriginRole, existingFactHash, existingPrevHash string
	var existingRowid int64
	_ = tx.QueryRow(`SELECT rowid, id, claim, kind, origin_role, fact_hash, prev_hash FROM facts WHERE project_id = ? AND claim = ?`, f.ProjectID, f.Claim).
		Scan(&existingRowid, &existingID, &existingClaim, &existingKind, &existingOriginRole, &existingFactHash, &existingPrevHash)

	if existingID != "" {
		f.ID = existingID
		if err := guardImmutableFields(existingID, existingClaim, existingKind, existingOriginRole, f); err != nil {
			return "", err
		}
		// origin_role is provenance and the first write wins; preserving it
		// keeps the stored digest valid.
		f.OriginRole = existingOriginRole
		if existingFactHash != "" {
			f.FactHash = existingFactHash
			f.PrevHash = existingPrevHash
		}
	} else if f.ID != "" {
		err := tx.QueryRow(`SELECT rowid, claim, kind, origin_role, fact_hash, prev_hash FROM facts WHERE id = ?`, f.ID).
			Scan(&existingRowid, &existingClaim, &existingKind, &existingOriginRole, &existingFactHash, &existingPrevHash)
		if err == nil && existingFactHash != "" {
			if err := guardImmutableFields(f.ID, existingClaim, existingKind, existingOriginRole, f); err != nil {
				return "", err
			}
			f.OriginRole = existingOriginRole
			// Metadata-only update: preserve the existing hash and link. The
			// hashed fields are either identical or empty (preserved by SQL).
			f.FactHash = existingFactHash
			f.PrevHash = existingPrevHash
		}
	}

	if f.FirstSeen == "" {
		f.FirstSeen = time.Now().UTC().Format(time.RFC3339)
	}

	if f.FactHash == "" {
		var prevHash string
		err := tx.QueryRow(`SELECT fact_hash FROM facts WHERE fact_hash != '' ORDER BY rowid DESC LIMIT 1`).Scan(&prevHash)
		if err != nil || prevHash == "" {
			f.PrevHash = GenesisHash
		} else {
			f.PrevHash = prevHash
		}
		f.FactHash = ComputeFactHash(f, f.PrevHash)
	}

	conf := f.Corroboration
	if conf == "" {
		conf = f.Confidence
	}
	if conf == "" {
		conf = "grounded"
	}
	distStatus := f.DistillStatus
	if distStatus == "" {
		distStatus = "pending"
	}
	refsChecked := f.RefsCheckedAt
	if refsChecked == "" {
		refsChecked = f.FirstSeen
	}
	reviewStatus := f.ReviewStatus
	if reviewStatus == "" {
		// Unlabeled writes default to candidate: authority is always explicit.
		reviewStatus = ReviewCandidate
	}

	tier := f.Tier
	if tier == "" {
		if f.IsInvariant() || f.IsManualMemo() {
			tier = string(TierSemantic)
		} else {
			tier = string(TierEpisodic)
		}
	}
	salience := f.Salience
	if salience <= 0 {
		salience = 1.0
	}
	distinct := f.DistinctTools
	if distinct < 1 {
		distinct = len(f.Tools)
		if distinct < 1 {
			distinct = 1
		}
	}

	bStart := f.ByteStart
	bEnd := f.ByteEnd
	if bStart == 0 && bEnd == 0 {
		bStart = -1
		bEnd = -1
	}

	_, err := tx.Exec(`INSERT INTO facts(id,project_id,claim,confidence,verified,file_refs_exist,repo_files,tools,session_ref,first_seen,promoted_at,refs_checked_at,distilled,distill_status,kind,review_status,origin_role,quoted,fact_hash,prev_hash,compacted_at,supersedes_id,access_count,last_accessed_at,distinct_tools,tier,salience,is_stale,byte_start,byte_end,source_ref,visibility)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			confidence      = excluded.confidence,
			verified        = excluded.verified,
			file_refs_exist = excluded.file_refs_exist,
			claim           = excluded.claim,
			tools           = excluded.tools,
			repo_files      = excluded.repo_files,
			refs_checked_at = excluded.refs_checked_at,
			distilled       = CASE WHEN excluded.distilled != '' THEN excluded.distilled ELSE facts.distilled END,
			distill_status  = CASE WHEN excluded.distill_status != 'pending' THEN excluded.distill_status ELSE facts.distill_status END,
			kind            = CASE WHEN excluded.kind != '' THEN excluded.kind ELSE facts.kind END,
			review_status   = CASE WHEN facts.review_status = 'archived' THEN 'archived'
			                       WHEN facts.review_status = 'approved' THEN 'approved'
			                       WHEN excluded.review_status != '' THEN excluded.review_status
			                       ELSE facts.review_status END,
			origin_role     = CASE WHEN excluded.origin_role != '' THEN excluded.origin_role ELSE facts.origin_role END,
			quoted          = excluded.quoted,
			fact_hash       = excluded.fact_hash,
			prev_hash       = excluded.prev_hash,
			compacted_at    = CASE WHEN excluded.compacted_at != '' THEN excluded.compacted_at ELSE facts.compacted_at END,
			supersedes_id   = CASE WHEN excluded.supersedes_id != '' THEN excluded.supersedes_id ELSE facts.supersedes_id END,
			tier            = CASE WHEN excluded.tier != '' THEN excluded.tier ELSE facts.tier END,
			salience        = CASE WHEN excluded.salience > 0 THEN excluded.salience ELSE facts.salience END,
			is_stale        = CASE WHEN excluded.is_stale != 0 THEN excluded.is_stale ELSE facts.is_stale END,
			byte_start      = CASE WHEN excluded.byte_start >= 0 THEN excluded.byte_start ELSE facts.byte_start END,
			byte_end        = CASE WHEN excluded.byte_end >= 0 THEN excluded.byte_end ELSE facts.byte_end END,
			source_ref      = CASE WHEN excluded.source_ref != '' THEN excluded.source_ref ELSE facts.source_ref END,
			visibility      = CASE WHEN excluded.visibility = 'global' THEN 'global' ELSE facts.visibility END`,
		f.ID, f.ProjectID, f.Claim, conf, boolInt(f.Verified), boolInt(f.FileRefsExist),
		joinJSON(f.RepoFiles), joinJSON(f.Tools), f.SessionRef, f.FirstSeen, f.PromotedAt,
		refsChecked, f.Distilled, distStatus, f.Kind, reviewStatus, f.OriginRole, boolInt(f.Quoted),
		f.FactHash, f.PrevHash, f.CompactedAt, f.SupersedesID,
		f.AccessCount, f.LastAccessedAt, distinct, tier, salience, boolInt(f.IsStaleFlag),
		bStart, bEnd, f.SourceRef, f.Visibility)
	if err != nil {
		return "", fmt.Errorf("insert/update fact %s: %w", f.ID, err)
	}

	// Maintain assertion-to-evidence lineage links (Goal: knowledge-assertion-lineage)
	var hasAssertionsTable int
	_ = tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='assertions'`).Scan(&hasAssertionsTable)
	if hasAssertionsTable > 0 {
		if err := linkFactEvidenceTx(tx, f); err != nil {
			return "", fmt.Errorf("link fact evidence lineage: %w", err)
		}
	}

	// If a payload changed on an existing fact that is not at the tip, rechain subsequent blocks to preserve determinism
	if existingRowid > 0 && existingFactHash != "" && existingFactHash != f.FactHash {
		if err := rechainFactsFromTx(tx, existingRowid); err != nil {
			return "", err
		}
	}

	return f.ID, nil
}

// rechainFactsFromTx recalculates fact_hash and prev_hash for all hashed rows in facts
// with rowid > afterRowID (or all rows if afterRowID <= 0), maintaining a continuous
// SHA-256 chain after authorized store mutations (UpsertFact updates, PruneFacts,
// DeleteFact, CompactFact, or Store.Repair deduplication).
func rechainFactsFromTx(tx *sql.Tx, afterRowID int64) error {
	if afterRowID < 0 {
		afterRowID = 0
	}
	expectedPrev := GenesisHash
	if afterRowID > 0 {
		err := tx.QueryRow(`SELECT fact_hash FROM facts WHERE rowid <= ? AND fact_hash != '' ORDER BY rowid DESC LIMIT 1`, afterRowID).Scan(&expectedPrev)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("lookup predecessor hash for rechain: %w", err)
		}
		if expectedPrev == "" {
			expectedPrev = GenesisHash
		}
	}

	subRows, err := tx.Query(`SELECT `+selectFactsFields+` FROM facts WHERE rowid > ? ORDER BY rowid ASC`, afterRowID)
	if err != nil {
		return fmt.Errorf("query subsequent facts for rechain: %w", err)
	}
	defer subRows.Close()

	type rechainItem struct {
		id       string
		factHash string
		prevHash string
	}
	var rechains []rechainItem
	for subRows.Next() {
		subF, err := scanFact(subRows)
		if err != nil {
			return fmt.Errorf("scan subsequent fact: %w", err)
		}
		if subF.FactHash == "" {
			expectedPrev = subF.FactHash
			continue
		}
		newHash := ComputeFactHash(subF, expectedPrev)
		if subF.FactHash != newHash || subF.PrevHash != expectedPrev {
			rechains = append(rechains, rechainItem{
				id:       subF.ID,
				factHash: newHash,
				prevHash: expectedPrev,
			})
		}
		expectedPrev = newHash
	}
	if err := subRows.Err(); err != nil {
		return fmt.Errorf("iterate subsequent facts: %w", err)
	}
	_ = subRows.Close()

	if len(rechains) > 0 {
		stmt, err := tx.Prepare(`UPDATE facts SET fact_hash = ?, prev_hash = ? WHERE id = ?`)
		if err != nil {
			return fmt.Errorf("prepare update rechain: %w", err)
		}
		defer stmt.Close()

		for _, rc := range rechains {
			if _, err := stmt.Exec(rc.factHash, rc.prevHash, rc.id); err != nil {
				return fmt.Errorf("rechain fact %s: %w", rc.id, err)
			}
		}
	}
	return nil
}

// PruneFacts deletes derived facts for a project whose id is not in keep, so
// re-running dream reconciles the store with the current distillation.
// Authority-bearing records (explicit memos, lessons, cases, non-goals,
// milestones, approved invariants) are never deleted; keep=nil removes every
// derived fact but preserves that authority set.
func (s *Store) PruneFacts(projectID string, keep []string) error {
	factMu.Lock()
	defer factMu.Unlock()

	existing, err := s.FactsByProject(projectID)
	if err != nil {
		return err
	}
	keepSet := make(map[string]bool, len(keep))
	for _, id := range keep {
		keepSet[id] = true
	}
	var deleteIDs []string
	for _, f := range existing {
		if keepSet[f.ID] || f.IsAuthorityBearing() {
			continue
		}
		deleteIDs = append(deleteIDs, f.ID)
	}
	if len(deleteIDs) == 0 {
		return nil
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin prune facts tx: %w", err)
	}
	defer tx.Rollback()

	for start := 0; start < len(deleteIDs); start += 500 {
		end := start + 500
		if end > len(deleteIDs) {
			end = len(deleteIDs)
		}
		chunk := deleteIDs[start:end]
		placeholders := strings.Repeat("?,", len(chunk))
		placeholders = strings.TrimSuffix(placeholders, ",")
		args := make([]any, 0, len(chunk)+1)
		args = append(args, projectID)
		for _, id := range chunk {
			args = append(args, id)
		}
		if _, err := tx.Exec(`DELETE FROM facts WHERE project_id=? AND id IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	_, _ = tx.Exec(`DELETE FROM facts_fts WHERE id NOT IN (SELECT id FROM facts)`)
	if err := rechainFactsFromTx(tx, 0); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteFact removes a single fact by ID and rechians subsequent blocks.
func (s *Store) DeleteFact(id string) error {
	factMu.Lock()
	defer factMu.Unlock()

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin delete fact tx: %w", err)
	}
	defer tx.Rollback()

	var rowid int64
	_ = tx.QueryRow(`SELECT rowid FROM facts WHERE id=?`, id).Scan(&rowid)
	res, err := tx.Exec(`DELETE FROM facts WHERE id=?`, id)
	if err != nil {
		return err
	}
	_, _ = tx.Exec(`DELETE FROM facts_fts WHERE id=?`, id)
	if affected, _ := res.RowsAffected(); affected > 0 {
		if err := rechainFactsFromTx(tx, rowid-1); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertMemo records an explicit architectural decision or project directive directly as a verified fact.
func (s *Store) InsertMemo(projectID, content, agent, projectDir string, repoFiles []string) (*Fact, error) {
	return s.InsertMemoWithProvenance(projectID, content, agent, projectDir, repoFiles, "", Provenance{})
}

// InsertMemoWithKind records an explicit architectural decision, milestone, or non-goal with a designated kind.
func (s *Store) InsertMemoWithKind(projectID, content, agent, projectDir string, repoFiles []string, kind string) (*Fact, error) {
	return s.InsertMemoWithProvenance(projectID, content, agent, projectDir, repoFiles, kind, Provenance{})
}

// InsertMemoWithProvenance records an explicit architectural decision, milestone, or non-goal with provenance metadata.
func (s *Store) InsertMemoWithProvenance(projectID, content, agent, projectDir string, repoFiles []string, kind string, prov Provenance) (*Fact, error) {
	return s.InsertMemoWithProvenanceAndVisibility(projectID, content, agent, projectDir, repoFiles, kind, "", prov)
}

// InsertMemoWithProvenanceAndVisibility records an explicit memory with an
// explicit visibility scope: "project" (default) or "global".
func (s *Store) InsertMemoWithProvenanceAndVisibility(projectID, content, agent, projectDir string, repoFiles []string, kind, visibility string, prov Provenance) (*Fact, error) {
	vis, err := NormalizeVisibility(visibility)
	if err != nil {
		return nil, err
	}
	if agent == "" {
		if prov.ToolName != "" {
			agent = prov.ToolName
		} else {
			agent = "user"
		}
	}
	if kind == "lesson" {
		if existing, found, recErr := s.bumpRecurrence(projectID, content); recErr != nil {
			return nil, recErr
		} else if found && existing != nil {
			return existing, nil
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	refsExist := true
	if len(repoFiles) > 0 {
		for _, rf := range repoFiles {
			full := rf
			if projectDir != "" && !filepath.IsAbs(rf) {
				full = filepath.Join(projectDir, rf)
			}
			fi, err := os.Stat(full)
			if err != nil || fi.IsDir() {
				refsExist = false
				break
			}
		}
	}

	sessionRef := prov.SessionRef
	if strings.TrimSpace(sessionRef) == "" {
		sessionRef = "manual_memo"
	}
	originRole := prov.OriginRole
	if originRole == "" {
		originRole = "user"
	}
	fact := Fact{
		ID:            FactID(projectID, content),
		ProjectID:     projectID,
		Claim:         content,
		Kind:          kind,
		Distilled:     content,
		DistillStatus: "distilled",
		Confidence:    "corroborated",
		Corroboration: "corroborated",
		Verified:      refsExist,
		FileRefsExist: refsExist,
		RepoFiles:     repoFiles,
		Tools:         []string{agent},
		SessionRef:    sessionRef,
		FirstSeen:     now,
		PromotedAt:    now,
		RefsCheckedAt: now,
		ReviewStatus:  ReviewApproved,
		Visibility:    vis,
		OriginRole:    originRole,
		ToolName:      prov.ToolName,
		ClientKind:    prov.ClientKind,
		OriginEntity:  prov.OriginEntity,
	}

	if env := legacyMemoryEnvelope(projectID, content, kind, agent, agent, prov); env != nil {
		if _, _, err := s.saveFactWithMemoryEnvelope(fact, env); err != nil {
			return nil, err
		}
	} else if err := s.UpsertFact(fact); err != nil {
		return nil, err
	}
	_ = s.DB.QueryRow(`SELECT fact_hash, prev_hash, recurrence_count, auto_promoted FROM facts WHERE id = ?`, fact.ID).Scan(&fact.FactHash, &fact.PrevHash, &fact.RecurrenceCount, &fact.AutoPromoted)
	if kind == "lesson" {
		_, _ = s.DB.Exec(`UPDATE facts SET recurrence_count = MAX(recurrence_count, 1) WHERE id = ? AND COALESCE(review_status, '') != 'archived'`, fact.ID)
		_ = s.DB.QueryRow(`SELECT recurrence_count, auto_promoted FROM facts WHERE id = ?`, fact.ID).Scan(&fact.RecurrenceCount, &fact.AutoPromoted)
	}
	return &fact, nil
}

// legacyMemoryEnvelope synthesizes a sila/memory/v1 sidecar for an explicit
// legacy memo, lesson, case or decision. It returns nil for kinds the v1 enum
// does not model (non-goals, milestones, operational learnings).
func legacyMemoryEnvelope(projectID, content, kind, agent, provAgent string, prov Provenance) *MemoryEnvelope {
	var envKind, predicate string
	switch kind {
	case "", "memo", "decision":
		envKind, predicate = MemoryKindArchitectureDecision, "has_property"
	case "lesson":
		envKind, predicate = MemoryKindFix, "causes"
	case "case":
		envKind, predicate = MemoryKindProcedure, "has_property"
	case "invariant":
		envKind, predicate = MemoryKindInvariant, "requires"
	default:
		return nil
	}
	summary := truncateMemoryText(content, 2000)
	title := truncateMemoryText(strings.TrimSpace(strings.SplitN(content, "\n", 2)[0]), 200)
	if title == "" {
		title = truncateMemoryText(summary, 200)
	}
	if summary == "" {
		summary = title
	}
	author := firstNonEmptyMemoryString(provAgent, prov.ToolName, agent, "user")
	factID := FactID(projectID, content)
	env := &MemoryEnvelope{
		SchemaVersion: MemorySchemaVersion,
		MemoryID:      "mem_" + factID,
		ProjectID:     projectID,
		Kind:          envKind,
		Title:         title,
		Summary:       summary,
		Assertions: []MemoryAssertion{{
			ID:            "A-001",
			AssertionType: MemoryAssertionDescriptive,
			Predicate:     predicate,
			Statement:     summary,
			Subject:       MemorySubject{ContextEntity: "project", Value: projectID},
			Object: MemoryObject{Literal: &MemoryLiteral{
				Type:  "string",
				Value: truncateMemoryText(summary, 512),
			}},
			Confidence:   0.8,
			Importance:   4,
			EvidenceRefs: []string{"E-001"},
		}},
		Evidence: []MemoryEvidence{{
			ID:      "E-001",
			Kind:    MemoryEvidenceAgentInitiated,
			Summary: truncateMemoryText("Recorded by "+author+" through the legacy "+firstNonEmptyMemoryString(kind, "memo")+" command", 500),
		}},
		IdempotencyKey: "legacy:" + factID,
		Provenance: &MemoryProvenance{
			Agent:        author,
			ToolName:     prov.ToolName,
			ClientKind:   prov.ClientKind,
			OriginEntity: prov.OriginEntity,
			SessionRef:   prov.SessionRef,
		},
	}
	if envKind == MemoryKindInvariant {
		// Normative assertions require a normative predicate.
		env.Assertions[0].AssertionType = MemoryAssertionNormative
	}
	return env
}

func truncateMemoryText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// InsertNonGoal records an explicit negative constraint / non-goal in the project's vision store.
func (s *Store) InsertNonGoal(projectID, content, agent string) (*Fact, error) {
	clean := strings.TrimSpace(content)
	if !strings.HasPrefix(strings.ToLower(clean), "non-goal:") && !strings.HasPrefix(strings.ToLower(clean), "nongoal:") {
		clean = "Non-goal: " + clean
	}
	return s.InsertMemoWithKind(projectID, clean, agent, "", nil, "nongoal")
}

// InsertMilestone records a strategic milestone in the project's vision store.
func (s *Store) InsertMilestone(projectID, content, agent string) (*Fact, error) {
	clean := strings.TrimSpace(content)
	if !strings.HasPrefix(strings.ToLower(clean), "milestone:") {
		clean = "Milestone: " + clean
	}
	return s.InsertMemoWithKind(projectID, clean, agent, "", nil, "milestone")
}

// InsertLesson records a deliberate solution lesson with manual-record authority.
func (s *Store) InsertLesson(projectID, content, agent, projectDir string, repoFiles []string) (*Fact, error) {
	return s.InsertMemoWithKind(projectID, content, agent, projectDir, repoFiles, "lesson")
}

// InsertLessonWithSession records a lesson attributed to a local session.
func (s *Store) InsertLessonWithSession(projectID, content, agent, projectDir string, repoFiles []string, sessionRef string) (*Fact, error) {
	return s.InsertMemoWithProvenance(projectID, content, agent, projectDir, repoFiles, "lesson", Provenance{SessionRef: sessionRef})
}

// InsertCase records an architectural procedural case with manual-record authority.
func (s *Store) InsertCase(projectID, content, agent, projectDir string, repoFiles []string) (*Fact, error) {
	return s.InsertMemoWithKind(projectID, content, agent, projectDir, repoFiles, "case")
}

// InsertCaseWithSession records a case attributed to a local session.
func (s *Store) InsertCaseWithSession(projectID, content, agent, projectDir string, repoFiles []string, sessionRef string) (*Fact, error) {
	return s.InsertMemoWithProvenance(projectID, content, agent, projectDir, repoFiles, "case", Provenance{SessionRef: sessionRef})
}

// InsertLessonWithOrigin records a lesson attributed to a subagent while the
// parent session owns continuity.
func (s *Store) InsertLessonWithOrigin(projectID, content, agent, projectDir string, repoFiles []string, sessionRef, originRole string) (*Fact, error) {
	return s.InsertMemoWithProvenance(projectID, content, agent, projectDir, repoFiles, "lesson", Provenance{SessionRef: sessionRef, OriginRole: originRole})
}

// InsertCaseWithOrigin records a case attributed to a subagent while the
// parent session owns continuity.
func (s *Store) InsertCaseWithOrigin(projectID, content, agent, projectDir string, repoFiles []string, sessionRef, originRole string) (*Fact, error) {
	return s.InsertMemoWithProvenance(projectID, content, agent, projectDir, repoFiles, "case", Provenance{SessionRef: sessionRef, OriginRole: originRole})
}

// InsertMemoWithSession records a memo attributed to a local session.
func (s *Store) InsertMemoWithSession(projectID, content, agent, projectDir string, repoFiles []string, sessionRef string) (*Fact, error) {
	return s.InsertMemoWithProvenance(projectID, content, agent, projectDir, repoFiles, "", Provenance{SessionRef: sessionRef})
}

// SaveFact commits a fact into the store with cryptographic hash certification.
func (s *Store) SaveFact(f Fact) error {
	return s.UpsertFact(f)
}

// SaveLesson records a structured lesson with cryptographic hash certification.
func (s *Store) SaveLesson(projectID, symptom, cause, fix, tags, agent, projectDir string, repoFiles []string) (*Fact, error) {
	content := FormatLesson(symptom, cause, fix, tags)
	return s.InsertLesson(projectID, content, agent, projectDir, repoFiles)
}

// SaveDecision records an explicit architectural decision with cryptographic hash certification.
func (s *Store) SaveDecision(projectID, decision, rationale, agent string, repoFiles []string) (*Fact, error) {
	content := "Decision: " + strings.TrimSpace(decision)
	if r := strings.TrimSpace(rationale); r != "" {
		content += " — Rationale: " + r
	}
	return s.InsertMemoWithKind(projectID, content, agent, "", repoFiles, "decision")
}

// SaveOperationalLearning records a categorized operational learning with cryptographic hash certification.
func (s *Store) SaveOperationalLearning(projectID, content, agent, projectDir string, repoFiles []string) (*Fact, error) {
	return s.InsertMemoWithKind(projectID, content, agent, projectDir, repoFiles, "operational_learning")
}

// SaveCase records a structured architectural case with grounding and file verification.
func (s *Store) SaveCase(projectID, intent, approach, invariant, tags, agent, projectDir string, repoFiles []string) (*Fact, error) {
	content := FormatCase(intent, approach, invariant, tags)
	return s.InsertCase(projectID, content, agent, projectDir, repoFiles)
}

// CompactFact updates an episodic fact with its A2 extractive compaction result,
// recording compacted_at and supersedes_id. If the fact is already compacted, it is a no-op.
func (s *Store) CompactFact(ctx context.Context, id, compactedClaim string, newRepoFiles []string, supersedesID string) error {
	factMu.Lock()
	defer factMu.Unlock()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin compact fact tx: %w", err)
	}
	defer tx.Rollback()

	var rowid int64
	_ = tx.QueryRowContext(ctx, `SELECT rowid FROM facts WHERE id = ?`, id).Scan(&rowid)

	now := time.Now().UTC().Format(time.RFC3339)
	var res sql.Result
	if len(newRepoFiles) > 0 {
		res, err = tx.ExecContext(ctx, `
			UPDATE facts
			SET claim = ?,
				compacted_at = ?,
				supersedes_id = CASE WHEN ? != '' THEN ? ELSE supersedes_id END,
				repo_files = ?
			WHERE id = ? AND (compacted_at IS NULL OR compacted_at = '')
		`, compactedClaim, now, supersedesID, supersedesID, joinJSON(newRepoFiles), id)
	} else {
		res, err = tx.ExecContext(ctx, `
			UPDATE facts
			SET claim = ?,
				compacted_at = ?,
				supersedes_id = CASE WHEN ? != '' THEN ? ELSE supersedes_id END
			WHERE id = ? AND (compacted_at IS NULL OR compacted_at = '')
		`, compactedClaim, now, supersedesID, supersedesID, id)
	}
	if err != nil {
		return fmt.Errorf("compact fact %s: %w", id, err)
	}
	if affected, _ := res.RowsAffected(); affected > 0 {
		if err := rechainFactsFromTx(tx, rowid-1); err != nil {
			return fmt.Errorf("rechain compacted fact %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// nearDuplicateGuard looks for a near-duplicate claim in the same project and
// kind. It returns a merge target or veto terms; a timeout proceeds unmerged.
func (s *Store) nearDuplicateGuard(f Fact) (string, []string, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SILA_IDF_VETO"))) {
	case "off", "0", "false":
		return "", nil, nil
	case "on", "1", "true":
	case "":
		if idfVetoDisabled.Load() {
			return "", nil, nil
		}
	}
	if strings.TrimSpace(f.Kind) == "" || strings.TrimSpace(f.Claim) == "" {
		return "", nil, nil
	}
	var existing string
	if err := s.DB.QueryRow(`SELECT id FROM facts WHERE project_id = ? AND claim = ?`, f.ProjectID, f.Claim).Scan(&existing); err == nil {
		return "", nil, nil
	}
	tokens := TokenizeMemoryText(f.Claim)
	if len(tokens) < 3 {
		return "", nil, nil
	}
	candidates, err := s.ListFactsByKind(f.Kind, f.ProjectID, time.Time{}, 50)
	if err != nil {
		return "", nil, err
	}
	best := 0.0
	var bestFact Fact
	for _, candidate := range candidates {
		if candidate.ID == f.ID {
			continue
		}
		candidateTokens := TokenizeMemoryText(candidate.Claim)
		if len(candidateTokens) < 3 {
			continue
		}
		score := TokenJaccard(tokens, candidateTokens)
		if score > best || (score == best && candidate.FirstSeen > bestFact.FirstSeen) {
			best = score
			bestFact = candidate
		}
	}
	if best < 0.85 || bestFact.ID == "" {
		return "", nil, nil
	}
	diff := symmetricDifference(tokens, TokenizeMemoryText(bestFact.Claim))
	if len(diff) == 0 {
		return "", nil, nil
	}
	if len(diff) > 16 {
		diff = diff[:16]
	}
	deadline := time.Now().Add(20 * time.Millisecond)
	var vetoTerms []string
	for _, term := range diff {
		if time.Now().After(deadline) {
			return "", nil, nil
		}
		df, dfErr := s.DocumentFrequency(f.ProjectID, term)
		if dfErr != nil {
			return "", nil, nil
		}
		if df <= 3 {
			vetoTerms = append(vetoTerms, term)
			if len(vetoTerms) == 3 {
				break
			}
		}
	}
	if len(vetoTerms) > 0 {
		s.recordPrecisionCounter(f.ProjectID, "idf_veto")
		return "", vetoTerms, nil
	}
	s.recordPrecisionCounter(f.ProjectID, "near_dup_merge")
	return bestFact.ID, nil, nil
}

// mergeFactEvidence folds a near-duplicate claim into the candidate.
func (s *Store) mergeFactEvidence(targetID string, incoming Fact) error {
	var existingRepos, existingTools string
	if err := s.DB.QueryRow(`SELECT repo_files, tools FROM facts WHERE id = ?`, targetID).Scan(&existingRepos, &existingTools); err != nil {
		return err
	}
	var repos, tools []string
	_ = json.Unmarshal([]byte(existingRepos), &repos)
	_ = json.Unmarshal([]byte(existingTools), &tools)
	repos = mergeStringSet(repos, incoming.RepoFiles)
	tools = mergeStringSet(tools, incoming.Tools)
	reposJSON, _ := json.Marshal(repos)
	toolsJSON, _ := json.Marshal(tools)
	_, err := s.DB.Exec(`UPDATE facts SET repo_files = ?, tools = ?, refs_checked_at = ? WHERE id = ?`,
		string(reposJSON), string(toolsJSON), time.Now().UTC().Format(time.RFC3339), targetID)
	return err
}

func mergeStringSet(base, extra []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(extra))
	for _, value := range append(append([]string{}, base...), extra...) {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// recordPrecisionCounter records a guard event without content.
func (s *Store) recordPrecisionCounter(projectID, verb string) {
	s.RecordToolEventCounter(projectID, verb)
}

// RecordToolEventCounter increments a guard counter without content.
func (s *Store) RecordToolEventCounter(projectID, toolName string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := FactID("tool-event", projectID+"|"+toolName+"|"+now)
	_, _ = s.DB.Exec(`INSERT OR IGNORE INTO tool_events
		(id, project_id, session_id, agent, source, tool_name, category, outcome, duration_ms, created_at)
		VALUES (?, ?, '', 'system', 'guard', ?, 'retrieval', 'ok', 0, ?)`,
		id, projectID, toolName, now)
}
