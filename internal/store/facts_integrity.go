package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GenesisHash is the root SHA-256 zero-block predecessor for fact certification.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// ComputeFactHash calculates the deterministic SHA-256 digest of a fact and its preceding hash.
func ComputeFactHash(f Fact, prevHash string) string {
	if prevHash == "" {
		prevHash = GenesisHash
	}
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write([]byte{0})
	h.Write([]byte(f.ID))
	h.Write([]byte{0})
	h.Write([]byte(f.ProjectID))
	h.Write([]byte{0})
	h.Write([]byte(f.Claim))
	h.Write([]byte{0})
	h.Write([]byte(f.Kind))
	h.Write([]byte{0})
	h.Write([]byte(f.SessionRef))
	h.Write([]byte{0})
	h.Write([]byte(f.FirstSeen))
	h.Write([]byte{0})
	h.Write([]byte(f.OriginRole))
	return hex.EncodeToString(h.Sum(nil))
}

// LatestFactHash returns the most recently computed fact_hash from the ledger.
func (s *Store) LatestFactHash() (string, error) {
	var hash string
	err := s.DB.QueryRow(`SELECT fact_hash FROM facts WHERE fact_hash != '' ORDER BY rowid DESC LIMIT 1`).Scan(&hash)
	if err == sql.ErrNoRows {
		return GenesisHash, nil
	}
	if err != nil {
		return "", err
	}
	if hash == "" {
		return GenesisHash, nil
	}
	return hash, nil
}

// SECURITY NOTICE & INTEGRITY GUARANTEE:
// The fact ledger uses SHA-256 hash chaining (similar to git commit DAGs) to provide
// tamper-evidence and detect accidental corruption, silent redactions, or unauthorized
// row modifications in the local SQLite store.
//
// Threat Model Limitation:
// Local hashes stored in the same SQLite database DO NOT authenticate against an
// attacker who has write access to the host machine or database file. An attacker
// with direct filesystem access can modify data and recompute hashes. Hash chaining
// guarantees internal ledger consistency, regression detection, and durable before-repair
// evidence preservation, but does not replace digital signatures or external trust services.

// FactIncident represents a recorded integrity violation or explicit rebaseline event in the fact ledger.
type FactIncident struct {
	ID               string `json:"id"`
	FactID           string `json:"fact_id"`
	IncidentType     string `json:"incident_type"` // "tamper_mismatch", "broken_link", "rebaseline_mismatch", "rebaseline_broken_link", "rebaseline_legacy"
	StoredHash       string `json:"stored_hash"`
	ComputedHash     string `json:"computed_hash"`
	StoredPrevHash   string `json:"stored_prev_hash"`
	ExpectedPrevHash string `json:"expected_prev_hash"`
	Claim            string `json:"claim"`
	Details          string `json:"details"`
	CreatedAt        string `json:"created_at"`
}

// VerifyViolation is the structured classification of one ledger violation.
type VerifyViolation struct {
	Block        int    `json:"block"`
	FactID       string `json:"fact_id"`
	Type         string `json:"type"`
	Stored       string `json:"stored,omitempty"`
	Computed     string `json:"computed,omitempty"`
	ExpectedPrev string `json:"expected_prev,omitempty"`
	Cascade      bool   `json:"cascade"`
	Independent  bool   `json:"independent,omitempty"`
}

// VerifySummary counts every violation class plus root/cascade boundaries.
type VerifySummary struct {
	BrokenPrevLink  int `json:"broken_prev_link"`
	DigestMismatch  int `json:"digest_mismatch"`
	MissingHash     int `json:"missing_hash"`
	GenesisMismatch int `json:"genesis_mismatch"`
	DuplicateHash   int `json:"duplicate_hash"`
	OrderGap        int `json:"order_gap"`
	Roots           int `json:"roots"`
	Cascades        int `json:"cascades"`
}

// VerifyFirstDivergence identifies the earliest non-cascade break.
type VerifyFirstDivergence struct {
	Block  int    `json:"block"`
	FactID string `json:"fact_id"`
	Type   string `json:"type"`
}

// FileGroundingSummary reports on-disk file reference existence for a project's facts.
type FileGroundingSummary struct {
	ProjectID      string   `json:"project_id,omitempty"`
	ProjectDir     string   `json:"project_dir,omitempty"`
	FactsWithFiles int      `json:"facts_with_files"`
	GroundedFacts  int      `json:"grounded_facts"`
	StaleFacts     int      `json:"stale_facts"`
	MissingFiles   []string `json:"missing_files,omitempty"`
}

// ChainAuditResult represents the verification report of the hash-chained fact ledger.
type ChainAuditResult struct {
	TotalBlocks      int                    `json:"total_blocks"`
	VerifiedBlocks   int                    `json:"verified_blocks"`
	LegacyBlocks     int                    `json:"legacy_blocks"`
	GenesisHash      string                 `json:"genesis_hash"`
	TipHash          string                 `json:"tip_hash"`
	Intact           bool                   `json:"intact"`
	Violations       []string               `json:"violations,omitempty"`
	IncidentCount    int                    `json:"incident_count,omitempty"`
	Incidents        []FactIncident         `json:"incidents,omitempty"`
	DurationMs       int64                  `json:"duration_ms"`
	Schema           string                 `json:"schema,omitempty"`
	Summary          *VerifySummary         `json:"summary,omitempty"`
	FirstDivergence  *VerifyFirstDivergence `json:"first_divergence,omitempty"`
	ViolationDetails []VerifyViolation      `json:"violation_details,omitempty"`
	Grounding        *FileGroundingSummary  `json:"grounding,omitempty"`
}

// RecordFactIncident writes a durable record of an integrity violation or explicit rebaseline.
func (s *Store) RecordFactIncident(inc FactIncident) error {
	if inc.ID == "" {
		inc.ID = "inc_" + FactID(inc.FactID, inc.StoredHash+inc.ComputedHash+time.Now().UTC().Format(time.RFC3339Nano))
	}
	if inc.CreatedAt == "" {
		inc.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.DB.Exec(`
		INSERT OR IGNORE INTO fact_incidents(
			id, fact_id, incident_type, stored_hash, computed_hash,
			stored_prev_hash, expected_prev_hash, claim, details, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inc.ID, inc.FactID, inc.IncidentType, inc.StoredHash, inc.ComputedHash,
		inc.StoredPrevHash, inc.ExpectedPrevHash, inc.Claim, inc.Details, inc.CreatedAt,
	)
	return err
}

// FactIncidents returns all recorded integrity incidents and rebaselines in the ledger.
func (s *Store) FactIncidents() ([]FactIncident, error) {
	rows, err := s.DB.Query(`
		SELECT id, fact_id, incident_type, stored_hash, computed_hash,
		       stored_prev_hash, expected_prev_hash, claim, details, created_at
		FROM fact_incidents
		ORDER BY created_at ASC, rowid ASC
	`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("query fact incidents: %w", err)
	}
	defer rows.Close()

	var incidents []FactIncident
	for rows.Next() {
		var inc FactIncident
		if err := rows.Scan(
			&inc.ID, &inc.FactID, &inc.IncidentType, &inc.StoredHash, &inc.ComputedHash,
			&inc.StoredPrevHash, &inc.ExpectedPrevHash, &inc.Claim, &inc.Details, &inc.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan fact incident: %w", err)
		}
		incidents = append(incidents, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fact incidents: %w", err)
	}
	return incidents, nil
}

// VerifyChain audits the cryptographic integrity of the fact ledger.
// It verifies that each record's prev_hash points accurately to the preceding record,
// and that its fact_hash matches the recomputed canonical SHA-256 digest.
// It classifies unhashed legacy entries separately from tamper mismatches and broken links,
// propagates any database iteration errors, and includes inspectable incident evidence.
func (s *Store) VerifyChain(projectSel string) (*ChainAuditResult, error) {
	return s.verifyChain(projectSel, false)
}

// VerifyChainExplained is the structured diagnostic form used by --explain and
// JSON. It classifies every violation and identifies the first divergence.
func (s *Store) VerifyChainExplained(projectSel string) (*ChainAuditResult, error) {
	return s.verifyChain(projectSel, true)
}

func (s *Store) verifyChain(projectSel string, explain bool) (*ChainAuditResult, error) {
	start := time.Now()
	res := &ChainAuditResult{
		GenesisHash: GenesisHash,
		Intact:      true,
		Schema:      "sila/facts-verify/v1",
		Summary:     &VerifySummary{},
	}

	if pastIncidents, err := s.FactIncidents(); err == nil && len(pastIncidents) > 0 {
		res.IncidentCount = len(pastIncidents)
		const maxReturnedIncidents = 50
		if len(pastIncidents) > maxReturnedIncidents {
			res.Incidents = append(res.Incidents, pastIncidents[len(pastIncidents)-maxReturnedIncidents:]...)
		} else {
			res.Incidents = append(res.Incidents, pastIncidents...)
		}
	}

	filterProject := strings.TrimSpace(projectSel)
	if filterProject != "" {
		if p, err := s.ResolveScope(filterProject, ""); err == nil && p.ID != "" {
			filterProject = p.ID
		}
	}

	rowids := make([]int64, 0, 1024)
	if idRows, idErr := s.DB.Query(`SELECT rowid FROM facts ORDER BY rowid ASC`); idErr == nil {
		for idRows.Next() {
			var id int64
			if idRows.Scan(&id) == nil {
				rowids = append(rowids, id)
			}
		}
		idRows.Close()
	}

	rows, err := s.DB.Query(`SELECT ` + selectFactsFields + ` FROM facts ORDER BY rowid ASC`)
	if err != nil {
		return nil, fmt.Errorf("query facts for chain audit: %w", err)
	}
	defer rows.Close()

	expectedPrev := GenesisHash
	seenHashes := map[string]bool{}
	rootSeen := false
	prevRowID := int64(0)
	globalBlock := 0
	add := func(block int, f Fact, typ, stored, computed, expected string, cascade bool) {
		if explain {
			res.ViolationDetails = append(res.ViolationDetails, VerifyViolation{
				Block: block, FactID: f.ID, Type: typ, Stored: stored, Computed: computed,
				ExpectedPrev: expected, Cascade: cascade, Independent: !cascade,
			})
		}
		res.Intact = false
		switch typ {
		case "broken_prev_link":
			res.Summary.BrokenPrevLink++
		case "digest_mismatch":
			res.Summary.DigestMismatch++
		case "missing_hash":
			res.Summary.MissingHash++
		case "genesis_mismatch":
			res.Summary.GenesisMismatch++
		case "duplicate_hash":
			res.Summary.DuplicateHash++
		case "order_gap":
			res.Summary.OrderGap++
		}
		if cascade {
			res.Summary.Cascades++
		} else {
			res.Summary.Roots++
			rootSeen = true
			if res.FirstDivergence == nil {
				res.FirstDivergence = &VerifyFirstDivergence{Block: block, FactID: f.ID, Type: typ}
			}
		}
		var msg string
		switch typ {
		case "missing_hash":
			msg = fmt.Sprintf("block %d (fact %s): unhashed legacy record (missing fact_hash)", block, f.ID)
		case "broken_prev_link":
			msg = fmt.Sprintf("block %d (fact %s): broken prev_hash link (expected %s, got %s)", block, f.ID, expected, stored)
		case "genesis_mismatch":
			msg = fmt.Sprintf("block %d (fact %s): genesis mismatch (expected %s, got %s)", block, f.ID, expected, stored)
		case "duplicate_hash":
			msg = fmt.Sprintf("block %d (fact %s): duplicate fact_hash %s", block, f.ID, stored)
		case "order_gap":
			msg = fmt.Sprintf("block %d (fact %s): rowid order gap (broken prev_hash link; expected %s, got %s)", block, f.ID, expected, stored)
		default:
			msg = fmt.Sprintf("block %d (fact %s, claim %q): hash mismatch (computed %s, stored %s)", block, f.ID, f.Claim, computed, stored)
		}
		res.Violations = append(res.Violations, msg)
	}

	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan fact in chain audit: %w", err)
		}
		globalBlock++
		rowID := int64(globalBlock)
		if globalBlock-1 < len(rowids) {
			rowID = rowids[globalBlock-1]
		}
		rowGap := prevRowID != 0 && rowID != prevRowID+1
		prevRowID = rowID

		inScope := filterProject == "" || f.ProjectID == filterProject
		if !inScope {
			if f.FactHash != "" {
				seenHashes[f.FactHash] = true
			}
			expectedPrev = f.FactHash
			continue
		}

		block := res.TotalBlocks + 1
		res.TotalBlocks = block

		if f.FactHash == "" {
			res.LegacyBlocks++
			add(block, f, "missing_hash", "", "", expectedPrev, false)
			expectedPrev = f.FactHash
			continue
		}
		if seenHashes[f.FactHash] {
			add(block, f, "duplicate_hash", f.FactHash, "", expectedPrev, false)
		}
		seenHashes[f.FactHash] = true

		linkBroken := false
		if globalBlock == 1 && f.PrevHash != GenesisHash {
			add(block, f, "genesis_mismatch", f.PrevHash, "", GenesisHash, false)
			linkBroken = true
		} else if f.PrevHash != expectedPrev {
			typ := "broken_prev_link"
			if rowGap {
				typ = "order_gap"
			}
			add(block, f, typ, f.PrevHash, "", expectedPrev, rootSeen)
			linkBroken = true
		}

		computed := ComputeFactHash(f, f.PrevHash)
		if f.FactHash != computed {
			add(block, f, "digest_mismatch", f.FactHash, computed, expectedPrev, linkBroken || rootSeen)
			res.Incidents = append(res.Incidents, FactIncident{
				ID:               "active_" + f.ID,
				FactID:           f.ID,
				IncidentType:     "tamper_mismatch",
				StoredHash:       f.FactHash,
				ComputedHash:     computed,
				StoredPrevHash:   f.PrevHash,
				ExpectedPrevHash: expectedPrev,
				Claim:            f.Claim,
				Details:          fmt.Sprintf("block %d: active uncorrected hash mismatch", block),
				CreatedAt:        time.Now().UTC().Format(time.RFC3339),
			})
			res.IncidentCount++
		} else if !linkBroken {
			res.VerifiedBlocks++
		}

		expectedPrev = f.FactHash
		res.TipHash = f.FactHash
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate facts in chain audit: %w", err)
	}
	if res.TipHash == "" {
		res.TipHash = GenesisHash
	}
	if filterProject != "" {
		res.Grounding = s.AuditFileGrounding(filterProject, "")
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

// AuditFileGrounding checks whether referenced repo_files exist on disk for the selected project.
func (s *Store) AuditFileGrounding(projectSel, fallbackDir string) *FileGroundingSummary {
	p, err := s.ResolveScope(projectSel, fallbackDir)
	projID := p.ID
	projDir := p.Path
	if err != nil || projID == "" {
		if fallbackDir != "" {
			projDir = fallbackDir
			projID = ProjectID(fallbackDir)
		} else if projectSel != "" {
			projID = projectSel
		}
	}
	if projDir == "" && fallbackDir != "" {
		projDir = fallbackDir
	}
	if projID == "" || projDir == "" {
		return nil
	}
	if fi, err := os.Stat(projDir); err != nil || !fi.IsDir() {
		return nil
	}
	facts, err := s.FactsByProject(projID)
	if err != nil {
		return nil
	}
	summary := &FileGroundingSummary{
		ProjectID:  projID,
		ProjectDir: projDir,
	}
	seenMissing := make(map[string]bool)
	for _, f := range facts {
		if len(f.RepoFiles) == 0 {
			continue
		}
		summary.FactsWithFiles++
		existCount := 0
		for _, rf := range f.RepoFiles {
			full := filepath.Join(projDir, rf)
			if fi, statErr := os.Stat(full); statErr == nil && !fi.IsDir() {
				existCount++
			} else if !seenMissing[rf] {
				seenMissing[rf] = true
				summary.MissingFiles = append(summary.MissingFiles, rf)
			}
		}
		if float64(existCount)/float64(len(f.RepoFiles)) >= 0.5 {
			summary.GroundedFacts++
		} else {
			summary.StaleFacts++
		}
	}
	return summary
}

// RepairChain audits the cryptographic hash chain and repairs unhashed legacy records.
// It explicitly refuses to silently overwrite mismatches on previously hashed records,
// recording them as durable integrity incidents instead of erasing tampering evidence.
// To explicitly rebaseline the chain after authorized modifications, use RebaselineChain.
func (s *Store) RepairChain() (int, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin repair chain tx: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT ` + selectFactsFields + ` FROM facts ORDER BY rowid ASC`)
	if err != nil {
		return 0, fmt.Errorf("query facts for chain repair: %w", err)
	}
	defer rows.Close()

	type legacyItem struct {
		id       string
		factHash string
		prevHash string
	}
	var toInitialize []legacyItem
	var incidentsToRecord []FactIncident

	expectedPrev := GenesisHash
	blockNum := 0
	for rows.Next() {
		blockNum++
		f, err := scanFact(rows)
		if err != nil {
			return 0, fmt.Errorf("scan fact in chain repair: %w", err)
		}

		if f.FactHash == "" {
			// Unhashed legacy record: initialize hash
			computed := ComputeFactHash(f, expectedPrev)
			toInitialize = append(toInitialize, legacyItem{
				id:       f.ID,
				factHash: computed,
				prevHash: expectedPrev,
			})
			expectedPrev = computed
		} else {
			// Previously hashed record: check integrity
			computed := ComputeFactHash(f, f.PrevHash)
			if f.PrevHash != expectedPrev || f.FactHash != computed {
				// Tampering or link break detected on previously hashed record!
				// DO NOT overwrite hash; record incident evidence.
				incType := "tamper_mismatch"
				if f.PrevHash != expectedPrev && f.FactHash == computed {
					incType = "broken_link"
				}
				incidentsToRecord = append(incidentsToRecord, FactIncident{
					ID:               "inc_" + FactID(f.ID, f.FactHash+computed+time.Now().UTC().Format(time.RFC3339Nano)),
					FactID:           f.ID,
					IncidentType:     incType,
					StoredHash:       f.FactHash,
					ComputedHash:     computed,
					StoredPrevHash:   f.PrevHash,
					ExpectedPrevHash: expectedPrev,
					Claim:            f.Claim,
					Details:          fmt.Sprintf("block %d: routine repair detected mismatch; preserved before-repair evidence without overwriting", blockNum),
					CreatedAt:        time.Now().UTC().Format(time.RFC3339),
				})
			}
			expectedPrev = f.FactHash
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate facts in chain repair: %w", err)
	}
	_ = rows.Close()

	// Record any preserved incidents
	if len(incidentsToRecord) > 0 {
		stmtInc, err := tx.Prepare(`
			INSERT OR IGNORE INTO fact_incidents(
				id, fact_id, incident_type, stored_hash, computed_hash,
				stored_prev_hash, expected_prev_hash, claim, details, created_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err == nil {
			defer stmtInc.Close()
			for _, inc := range incidentsToRecord {
				_, _ = stmtInc.Exec(
					inc.ID, inc.FactID, inc.IncidentType, inc.StoredHash, inc.ComputedHash,
					inc.StoredPrevHash, inc.ExpectedPrevHash, inc.Claim, inc.Details, inc.CreatedAt,
				)
			}
		}
	}

	// Update legacy unhashed records
	if len(toInitialize) > 0 {
		stmt, err := tx.Prepare(`UPDATE facts SET fact_hash = ?, prev_hash = ? WHERE id = ?`)
		if err != nil {
			return 0, fmt.Errorf("prepare update fact hash: %w", err)
		}
		defer stmt.Close()

		for _, item := range toInitialize {
			if _, err := stmt.Exec(item.factHash, item.prevHash, item.id); err != nil {
				return 0, fmt.Errorf("update fact hash for %s: %w", item.id, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit chain repair tx: %w", err)
	}

	return len(toInitialize), nil
}

// RebaselineChain explicitly recalculates all hashes in the fact ledger to match current content,
// recording durable provenance in fact_incidents for every modified block.
// WARNING: Rebaseline resets cryptographic hashes to match current store state. It proves internal
// consistency from this point forward; it DOES NOT recover original truth or certify historical authenticity.
func (s *Store) RebaselineChain(reason string) (int, error) {
	if reason == "" {
		reason = "manual operator rebaseline"
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin rebaseline tx: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT ` + selectFactsFields + ` FROM facts ORDER BY rowid ASC`)
	if err != nil {
		return 0, fmt.Errorf("query facts for rebaseline: %w", err)
	}
	defer rows.Close()

	type updateItem struct {
		id        string
		factHash  string
		prevHash  string
		recordInc bool
		inc       FactIncident
	}
	var toUpdate []updateItem

	expectedPrev := GenesisHash
	blockNum := 0
	rootSeen := false
	for rows.Next() {
		blockNum++
		f, err := scanFact(rows)
		if err != nil {
			return 0, fmt.Errorf("scan fact in rebaseline: %w", err)
		}

		computed := ComputeFactHash(f, expectedPrev)
		if f.PrevHash != expectedPrev || f.FactHash != computed {
			selfComputed := ComputeFactHash(f, f.PrevHash)
			incType := "rebaseline_mismatch"
			recordInc := false
			if f.FactHash == "" {
				incType = "rebaseline_legacy"
				recordInc = true
				rootSeen = true
			} else if f.FactHash != selfComputed {
				incType = "rebaseline_mismatch"
				recordInc = true
				rootSeen = true
			} else if f.PrevHash != expectedPrev {
				incType = "rebaseline_broken_link"
				if !rootSeen {
					recordInc = true
					rootSeen = true
				}
			}

			item := updateItem{
				id:        f.ID,
				factHash:  computed,
				prevHash:  expectedPrev,
				recordInc: recordInc,
			}
			if recordInc {
				item.inc = FactIncident{
					ID:               "reb_" + FactID(f.ID, f.FactHash+computed+time.Now().UTC().Format(time.RFC3339Nano)),
					FactID:           f.ID,
					IncidentType:     incType,
					StoredHash:       f.FactHash,
					ComputedHash:     computed,
					StoredPrevHash:   f.PrevHash,
					ExpectedPrevHash: expectedPrev,
					Claim:            f.Claim,
					Details:          fmt.Sprintf("block %d: rebaselined by operator: %s", blockNum, reason),
					CreatedAt:        time.Now().UTC().Format(time.RFC3339),
				}
			}
			toUpdate = append(toUpdate, item)
		}
		expectedPrev = computed
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate facts in rebaseline: %w", err)
	}
	_ = rows.Close()

	if len(toUpdate) == 0 {
		return 0, nil
	}

	stmtInc, err := tx.Prepare(`
		INSERT OR IGNORE INTO fact_incidents(
			id, fact_id, incident_type, stored_hash, computed_hash,
			stored_prev_hash, expected_prev_hash, claim, details, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare insert rebaseline incident: %w", err)
	}
	defer stmtInc.Close()

	stmt, err := tx.Prepare(`UPDATE facts SET fact_hash = ?, prev_hash = ? WHERE id = ?`)
	if err != nil {
		return 0, fmt.Errorf("prepare update fact hash: %w", err)
	}
	defer stmt.Close()

	for _, item := range toUpdate {
		if item.recordInc {
			if _, err := stmtInc.Exec(
				item.inc.ID, item.inc.FactID, item.inc.IncidentType, item.inc.StoredHash, item.inc.ComputedHash,
				item.inc.StoredPrevHash, item.inc.ExpectedPrevHash, item.inc.Claim, item.inc.Details, item.inc.CreatedAt,
			); err != nil {
				return 0, fmt.Errorf("insert rebaseline incident for %s: %w", item.id, err)
			}
		}

		if _, err := stmt.Exec(item.factHash, item.prevHash, item.id); err != nil {
			return 0, fmt.Errorf("update fact hash for %s: %w", item.id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit rebaseline tx: %w", err)
	}

	return len(toUpdate), nil
}

// ActiveFactViolations returns any current hash mismatches or broken links in the ledger.
func (s *Store) ActiveFactViolations() ([]string, error) {
	audit, err := s.VerifyChain("")
	if err != nil {
		return nil, err
	}
	return audit.Violations, nil
}

// RebuildFTS executes in-place FTS5 index reconstruction within a transaction.
func RebuildFTS(tx *sql.Tx, table string) error {
	switch table {
	case "facts_fts", "messages_fts", "docs_fts":
		_, err := tx.Exec(fmt.Sprintf("INSERT INTO %s(%s) VALUES('rebuild')", table, table))
		return err
	default:
		return fmt.Errorf("untracked or invalid fts table: %s", table)
	}
}
