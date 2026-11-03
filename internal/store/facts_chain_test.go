package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFactHashChain_Genesis(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "genesis.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 1. Empty ledger verification
	res, err := s.VerifyChain("")
	if err != nil {
		t.Fatalf("verify empty chain: %v", err)
	}
	if !res.Intact {
		t.Fatalf("expected empty chain to be intact")
	}
	if res.TotalBlocks != 0 {
		t.Fatalf("expected 0 blocks, got %d", res.TotalBlocks)
	}
	if res.GenesisHash != GenesisHash {
		t.Fatalf("expected GenesisHash %s, got %s", GenesisHash, res.GenesisHash)
	}

	// 2. Insert genesis fact
	f1 := Fact{
		ID:        "fact_001",
		ProjectID: "proj_a",
		Claim:     "Genesis fact for architecture",
		Kind:      "invariant",
		FirstSeen: "2026-09-24T00:00:00Z",
	}
	if err := s.SaveFact(f1); err != nil {
		t.Fatalf("save fact: %v", err)
	}

	facts, err := s.FactsAll()
	if err != nil || len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %v (%v)", len(facts), err)
	}
	if facts[0].PrevHash != GenesisHash {
		t.Errorf("expected genesis prev_hash %s, got %s", GenesisHash, facts[0].PrevHash)
	}
	if facts[0].FactHash == "" {
		t.Errorf("expected non-empty fact_hash")
	}

	// 3. Verify single-block ledger
	res, err = s.VerifyChain("")
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}
	if !res.Intact || res.TotalBlocks != 1 || res.VerifiedBlocks != 1 {
		t.Fatalf("expected intact single-block ledger, got %+v", res)
	}
	if res.TipHash != facts[0].FactHash {
		t.Errorf("expected tip_hash %s, got %s", facts[0].FactHash, res.TipHash)
	}
}

func TestFactHashChain_SequentialChaining(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "chain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 1. Save an invariant fact
	f1 := Fact{
		ID:        "f1",
		ProjectID: "proj_1",
		Claim:     "Always use pure-Go SQLite",
		Kind:      "invariant",
		FirstSeen: "2026-09-24T01:00:00Z",
	}
	if err := s.SaveFact(f1); err != nil {
		t.Fatal(err)
	}

	// 2. Save a structured lesson
	lesson, err := s.SaveLesson("proj_1", "sqlite lock timeout", "concurrent writes", "use WAL mode", "sqlite,wal", "devin", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Save an architectural decision
	decision, err := s.SaveDecision("proj_1", "Adopt hash-chained ledger", "Ensures tamper-evident auditability across SQLite knowledge base", "antigravity", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Read facts in insertion order
	rows, err := s.DB.Query(`SELECT ` + selectFactsFields + ` FROM facts ORDER BY rowid ASC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var chain []Fact
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, f)
	}

	if len(chain) != 3 {
		t.Fatalf("expected 3 facts in chain, got %d", len(chain))
	}

	// Verify links
	if chain[0].PrevHash != GenesisHash {
		t.Errorf("block 0 prev_hash: expected %s, got %s", GenesisHash, chain[0].PrevHash)
	}
	if chain[1].PrevHash != chain[0].FactHash {
		t.Errorf("block 1 prev_hash: expected %s, got %s", chain[0].FactHash, chain[1].PrevHash)
	}
	if chain[2].PrevHash != chain[1].FactHash {
		t.Errorf("block 2 prev_hash: expected %s, got %s", chain[1].FactHash, chain[2].PrevHash)
	}
	if chain[1].ID != lesson.ID || chain[2].ID != decision.ID {
		t.Errorf("unexpected chain IDs: %s, %s", chain[1].ID, chain[2].ID)
	}

	// Verify whole chain audit
	audit, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if !audit.Intact || audit.TotalBlocks != 3 || audit.VerifiedBlocks != 3 {
		t.Fatalf("expected 3 verified blocks, got %+v", audit)
	}
	if audit.TipHash != chain[2].FactHash {
		t.Errorf("expected TipHash %s, got %s", chain[2].FactHash, audit.TipHash)
	}
}

func TestFactHashChain_TamperDetection_ModifiedClaim(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "tamper_claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveFact(Fact{ID: "f1", ProjectID: "p", Claim: "Original claim 1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFact(Fact{ID: "f2", ProjectID: "p", Claim: "Original claim 2"}); err != nil {
		t.Fatal(err)
	}

	// Verify initially clean
	clean, err := s.VerifyChain("")
	if err != nil || !clean.Intact {
		t.Fatalf("initial chain should be intact: %v, %+v", err, clean)
	}

	// Tamper: modify claim of f1 directly in SQLite
	if _, err := s.DB.Exec(`UPDATE facts SET claim = 'Tampered malicious claim' WHERE id = 'f1'`); err != nil {
		t.Fatal(err)
	}

	// Verify audit detects tampering
	tampered, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if tampered.Intact {
		t.Fatalf("expected audit to detect tampering on modified claim")
	}
	if len(tampered.Violations) == 0 {
		t.Fatalf("expected violation messages, got none")
	}
	foundMismatch := false
	for _, v := range tampered.Violations {
		if strings.Contains(v, "hash mismatch") {
			foundMismatch = true
			break
		}
	}
	if !foundMismatch {
		t.Errorf("expected 'hash mismatch' in violations, got %v", tampered.Violations)
	}
}

func TestFactHashChain_TamperDetection_DeletedRow(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "tamper_del.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveFact(Fact{ID: "b1", ProjectID: "p", Claim: "Block 1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFact(Fact{ID: "b2", ProjectID: "p", Claim: "Block 2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFact(Fact{ID: "b3", ProjectID: "p", Claim: "Block 3"}); err != nil {
		t.Fatal(err)
	}

	// Delete block 2 directly in SQLite
	if _, err := s.DB.Exec(`DELETE FROM facts WHERE id = 'b2'`); err != nil {
		t.Fatal(err)
	}

	// Verify audit detects broken link
	tampered, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if tampered.Intact {
		t.Fatalf("expected audit to detect deleted row in chain")
	}
	foundBrokenLink := false
	for _, v := range tampered.Violations {
		if strings.Contains(v, "broken prev_hash link") {
			foundBrokenLink = true
			break
		}
	}
	if !foundBrokenLink {
		t.Errorf("expected 'broken prev_hash link' in violations, got %v", tampered.Violations)
	}
}

func TestFactHashChain_ReUpsertPreservesHash(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "reupsert.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f := Fact{
		ID:        "f_stable",
		ProjectID: "p",
		Claim:     "Durable invariant",
		Kind:      "invariant",
		FirstSeen: "2026-09-24T00:00:00Z",
	}
	if err := s.SaveFact(f); err != nil {
		t.Fatal(err)
	}

	initialHash, err := s.LatestFactHash()
	if err != nil {
		t.Fatal(err)
	}

	// Reconcile / re-upsert updating metadata
	f.RefsCheckedAt = "2026-09-24T05:00:00Z"
	f.ReviewStatus = ReviewApproved
	if err := s.UpsertFact(f); err != nil {
		t.Fatal(err)
	}

	afterHash, err := s.LatestFactHash()
	if err != nil {
		t.Fatal(err)
	}
	if afterHash != initialHash {
		t.Errorf("re-upsert mutated fact_hash: before %s, after %s", initialHash, afterHash)
	}

	audit, err := s.VerifyChain("")
	if err != nil || !audit.Intact {
		t.Fatalf("audit after re-upsert failed: %v, %+v", err, audit)
	}
}

func TestFactHashChain_ConcurrentAppends(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const numWorkers = 5
	const factsPerWorker = 4
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < factsPerWorker; i++ {
				f := Fact{
					ID:        fmt.Sprintf("w%d_f%d", workerID, i),
					ProjectID: "proj_concurrent",
					Claim:     fmt.Sprintf("Concurrent fact from worker %d index %d", workerID, i),
					Kind:      "invariant",
					FirstSeen: "2026-09-24T00:00:00Z",
				}
				_ = s.SaveFact(f)
			}
		}(w)
	}
	wg.Wait()

	audit, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if !audit.Intact {
		t.Fatalf("concurrent chain broken: %+v", audit)
	}
	if audit.TotalBlocks != numWorkers*factsPerWorker {
		t.Errorf("expected %d blocks, got %d", numWorkers*factsPerWorker, audit.TotalBlocks)
	}
}

func TestFactHashChain_LegacyMigrationBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy_chain.db")

	// 1. Create a database at schema v18
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// Insert legacy rows with empty fact_hash and prev_hash
	if _, err := s1.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, kind, distill_status, fact_hash, prev_hash)
		VALUES('leg_1', 'p', 'Legacy claim 1', 'high', '[]', '["agent"]', 'sess-1', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', 'invariant', 'distilled', '', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, kind, distill_status, fact_hash, prev_hash)
		VALUES('leg_2', 'p', 'Legacy claim 2', 'high', '[]', '["agent"]', 'sess-2', '2026-09-02T00:00:00Z', '2026-09-02T00:00:00Z', 'invariant', 'distilled', '', '')`); err != nil {
		t.Fatal(err)
	}

	// Reset user_version to 18 to trigger migration 19 on reopen
	if _, err := s1.DB.Exec(`PRAGMA user_version = 18`); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// 2. Reopen store — runs migration 19 and backfills legacy rows
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	audit, err := s2.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if !audit.Intact {
		t.Fatalf("backfilled legacy chain not intact: %+v", audit)
	}
	if audit.TotalBlocks != 2 || audit.VerifiedBlocks != 2 {
		t.Errorf("expected 2 verified blocks after legacy migration backfill, got %+v", audit)
	}
}

func TestFactHashChain_RepairHealsBrokenChain(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "repair_chain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Insert 3 valid facts
	if err := s.SaveFact(Fact{ID: "f1", ProjectID: "p", Claim: "Block 1", Kind: "code_claim"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFact(Fact{ID: "f2", ProjectID: "p", Claim: "Block 2", Kind: "code_claim"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFact(Fact{ID: "f3", ProjectID: "p", Claim: "Block 3", Kind: "code_claim"}); err != nil {
		t.Fatal(err)
	}

	// Corrupt block 2 (simulate unhashed legacy write by clearing fact_hash and prev_hash)
	if _, err := s.DB.Exec(`UPDATE facts SET fact_hash = '', prev_hash = '' WHERE id = 'f2'`); err != nil {
		t.Fatal(err)
	}

	// Verify that audit detects the corruption
	audit1, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if audit1.Intact {
		t.Fatalf("expected audit to detect broken chain")
	}

	// Run RepairChain
	repaired, err := s.RepairChain()
	if err != nil {
		t.Fatalf("RepairChain failed: %v", err)
	}
	if repaired < 1 {
		t.Fatalf("expected at least 1 repaired block, got %d", repaired)
	}

	// Verify that audit is now 100% intact
	audit2, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if !audit2.Intact {
		t.Fatalf("expected chain to be intact after repair, got %+v", audit2)
	}
	if audit2.VerifiedBlocks != 3 || audit2.TotalBlocks != 3 {
		t.Errorf("expected 3/3 verified blocks, got %+v", audit2)
	}
}

func TestFactHashChain_TamperPreservedAcrossDoctorRepair(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "tamper_doctor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveFact(Fact{ID: "f1", ProjectID: "p", Claim: "Authentic invariant 1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFact(Fact{ID: "f2", ProjectID: "p", Claim: "Authentic invariant 2"}); err != nil {
		t.Fatal(err)
	}

	initialAudit, err := s.VerifyChain("")
	if err != nil || !initialAudit.Intact {
		t.Fatalf("expected intact initial chain: %v, %+v", err, initialAudit)
	}
	f1OrigHash := initialAudit.TipHash

	// Tamper: directly mutate claim of f1 in SQLite
	if _, err := s.DB.Exec(`UPDATE facts SET claim = 'Malicious modified claim' WHERE id = 'f1'`); err != nil {
		t.Fatal(err)
	}

	// Verify tampering is immediately detected
	tamperedAudit, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if tamperedAudit.Intact {
		t.Fatalf("expected audit to detect tampering")
	}

	// Routine repair via Doctor(true) must NOT erase the integrity incident
	repRepair, err := s.Doctor(true)
	if err != nil {
		t.Fatalf("Doctor(true) failed: %v", err)
	}
	if !repRepair.RepairExecuted {
		t.Fatalf("expected RepairExecuted to be true")
	}

	// Verify that after doctor --repair, the claim REMAINS a reported integrity incident
	postRepairDoctor, err := s.Doctor(false)
	if err != nil {
		t.Fatalf("Doctor(false) failed: %v", err)
	}
	if postRepairDoctor.OverallStatus != StatusFail {
		t.Fatalf("expected StatusFail after doctor --repair on tampered fact, got %s", postRepairDoctor.OverallStatus)
	}

	postRepairAudit, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if postRepairAudit.Intact {
		t.Fatalf("routine doctor --repair erased integrity violation on previously hashed fact!")
	}

	// Original mismatch evidence remains inspectable
	incidents, err := s.FactIncidents()
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) == 0 {
		t.Fatalf("expected preserved incident records, got none")
	}
	var foundIncident *FactIncident
	for i := range incidents {
		if incidents[i].FactID == "f1" {
			foundIncident = &incidents[i]
			break
		}
	}
	if foundIncident == nil {
		t.Fatalf("expected preserved incident for fact f1, got %+v", incidents)
	}
	if foundIncident.Claim != "Malicious modified claim" {
		t.Errorf("expected incident claim %q, got %q", "Malicious modified claim", foundIncident.Claim)
	}
	_ = f1OrigHash
}

func TestFactHashChain_LegacyUnhashedInitializationVsTampering(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "legacy_vs_tamper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 1. Insert a hashed fact
	if err := s.SaveFact(Fact{ID: "f_hashed", ProjectID: "p", Claim: "Hashed fact"}); err != nil {
		t.Fatal(err)
	}

	// 2. Insert an unhashed legacy fact directly via SQL
	if _, err := s.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, kind, distill_status, fact_hash, prev_hash)
		VALUES('f_legacy', 'p', 'Legacy fact', 'high', '[]', '["agent"]', 's1', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', 'invariant', 'distilled', '', '')`); err != nil {
		t.Fatal(err)
	}

	// 3. Tamper with the hashed fact directly
	if _, err := s.DB.Exec(`UPDATE facts SET claim = 'Tampered hashed fact' WHERE id = 'f_hashed'`); err != nil {
		t.Fatal(err)
	}

	// 4. VerifyChain classifies legacy vs tamper separately
	audit, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if audit.LegacyBlocks != 1 {
		t.Errorf("expected 1 legacy block, got %d", audit.LegacyBlocks)
	}
	foundLegacyViolation := false
	foundTamperViolation := false
	for _, v := range audit.Violations {
		if strings.Contains(v, "unhashed legacy record") {
			foundLegacyViolation = true
		}
		if strings.Contains(v, "hash mismatch") {
			foundTamperViolation = true
		}
	}
	if !foundLegacyViolation {
		t.Errorf("expected unhashed legacy record violation, got %v", audit.Violations)
	}
	if !foundTamperViolation {
		t.Errorf("expected hash mismatch violation, got %v", audit.Violations)
	}

	// 5. Run routine repair: initializes legacy, but does NOT erase tamper
	initialized, err := s.RepairChain()
	if err != nil {
		t.Fatal(err)
	}
	if initialized != 1 {
		t.Errorf("expected 1 legacy initialized block, got %d", initialized)
	}

	// Post-repair audit: legacy block is now hashed, but tamper violation remains!
	postAudit, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if postAudit.LegacyBlocks != 0 {
		t.Errorf("expected 0 legacy blocks after repair, got %d", postAudit.LegacyBlocks)
	}
	if postAudit.Intact {
		t.Fatalf("expected chain to remain non-intact due to tampering")
	}
}

func TestFactHashChain_ExplicitRebaselineWithDurableProvenance(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "rebaseline.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveFact(Fact{ID: "f1", ProjectID: "p", Claim: "Claim 1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFact(Fact{ID: "f2", ProjectID: "p", Claim: "Claim 2"}); err != nil {
		t.Fatal(err)
	}

	// Tamper directly
	if _, err := s.DB.Exec(`UPDATE facts SET claim = 'Authorized updated claim 1' WHERE id = 'f1'`); err != nil {
		t.Fatal(err)
	}

	// Routine repair refuses to rebaseline
	initialized, err := s.RepairChain()
	if err != nil {
		t.Fatal(err)
	}
	if initialized != 0 {
		t.Errorf("expected 0 initialized blocks from routine repair, got %d", initialized)
	}

	// Explicit rebaseline
	rebaselined, err := s.RebaselineChain("database schema migration 2026-09-25")
	if err != nil {
		t.Fatalf("RebaselineChain failed: %v", err)
	}
	if rebaselined < 1 {
		t.Fatalf("expected at least 1 rebaselined block, got %d", rebaselined)
	}

	// Post-rebaseline: chain is now intact
	postAudit, err := s.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if !postAudit.Intact {
		t.Fatalf("expected chain to be intact after explicit rebaseline: %+v", postAudit)
	}

	// Durable provenance is preserved in fact_incidents
	incidents, err := s.FactIncidents()
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) == 0 {
		t.Fatalf("expected rebaseline provenance in fact_incidents, got none")
	}
	foundRebaseline := false
	for _, inc := range incidents {
		if strings.Contains(inc.Details, "database schema migration 2026-09-25") {
			foundRebaseline = true
			if inc.StoredHash == inc.ComputedHash {
				t.Errorf("stored_hash and computed_hash should differ in rebaseline incident record")
			}
			break
		}
	}
	if !foundRebaseline {
		t.Errorf("expected rebaseline reason in fact_incidents, got %+v", incidents)
	}
}

func TestFactHashChain_RejectsHashedMutation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "immutable.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f := Fact{
		ID:        "f_immutable",
		ProjectID: "p",
		Claim:     "Original claim",
		Kind:      "lesson",
		FirstSeen: "2026-09-24T00:00:00Z",
	}
	if err := s.SaveFact(f); err != nil {
		t.Fatal(err)
	}

	claimChange := f
	claimChange.Claim = "Changed claim"
	if err := s.UpsertFact(claimChange); !errors.Is(err, ErrFactImmutable) {
		t.Fatalf("claim mutation error = %v, want ErrFactImmutable", err)
	}

	kindChange := f
	kindChange.Kind = "invariant"
	if err := s.UpsertFact(kindChange); !errors.Is(err, ErrFactImmutable) {
		t.Fatalf("kind mutation error = %v, want ErrFactImmutable", err)
	}

	// Non-hashed metadata updates must still work and keep the link intact.
	meta := f
	meta.RefsCheckedAt = "2026-09-24T05:00:00Z"
	meta.Verified = true
	meta.Confidence = "grounded"
	if err := s.UpsertFact(meta); err != nil {
		t.Fatalf("metadata update failed: %v", err)
	}

	audit, err := s.VerifyChain("")
	if err != nil || !audit.Intact {
		t.Fatalf("chain broken after metadata update: %v, %+v", err, audit)
	}
	if audit.TotalBlocks != 1 {
		t.Fatalf("expected one block, got %d", audit.TotalBlocks)
	}
}

func TestFactHashChain_ReattributionSupersedesWithoutDeleting(t *testing.T) {
	root := t.TempDir()
	aDir := filepath.Join(root, "project-a")
	bDir := filepath.Join(root, "project-b")
	if err := os.MkdirAll(aDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bDir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bDir, "pkg", "file.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(filepath.Join(t.TempDir(), "reattr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.EnsureProject(aDir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.EnsureProject(bDir)
	if err != nil {
		t.Fatal(err)
	}

	f := Fact{
		ID:        "f_reattr",
		ProjectID: a.ID,
		Claim:     "Shared artifact lives here",
		FirstSeen: "2026-09-24T00:00:00Z",
		RepoFiles: []string{"pkg/file.go"},
	}
	if err := s.SaveFact(f); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReconcileFactDrift(a.ID, aDir); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	old, err := s.FactByID("f_reattr")
	if err != nil || old == nil {
		t.Fatalf("old fact was deleted: %v", err)
	}
	if !old.IsStaleFlag {
		t.Fatal("old fact was not marked stale")
	}
	newFact, err := s.FactByID(FactID(b.ID, f.Claim))
	if err != nil || newFact == nil {
		t.Fatalf("superseding fact not found in target project: %v", err)
	}
	if newFact.SupersedesID != "f_reattr" {
		t.Fatalf("supersedes_id = %q, want f_reattr", newFact.SupersedesID)
	}
	if audit, err := s.VerifyChain(""); err != nil || !audit.Intact {
		t.Fatalf("chain broken by re-attribution: %v, %+v", err, audit)
	}
}

func TestFactHashChain_DeletePruneCompactPreserveChain(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "mutations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, f := range []Fact{
		{ID: "m1", ProjectID: "proj-a", Claim: "Claim 1", SessionRef: "sess-1"},
		{ID: "m2", ProjectID: "proj-a", Claim: "Claim 2", SessionRef: "sess-1"},
		{ID: "m3", ProjectID: "proj-b", Claim: "Claim 3", SessionRef: "sess-2"},
		{ID: "m4", ProjectID: "proj-a", Claim: "Claim 4", SessionRef: "sess-1"},
	} {
		if err := s.SaveFact(f); err != nil {
			t.Fatal(err)
		}
	}

	// Verify scoped multi-project audit works before mutations
	if auditB, err := s.VerifyChain("proj-b"); err != nil || !auditB.Intact || auditB.TotalBlocks != 1 {
		t.Fatalf("VerifyChain(proj-b) failed: err=%v, audit=%+v", err, auditB)
	}

	// 1. DeleteFact on middle row m2 must rechain m3 and m4
	if err := s.DeleteFact("m2"); err != nil {
		t.Fatalf("DeleteFact: %v", err)
	}
	if audit, err := s.VerifyChain(""); err != nil || !audit.Intact || audit.TotalBlocks != 3 {
		t.Fatalf("chain broken after DeleteFact: err=%v, audit=%+v", err, audit)
	}

	// 2. CompactFact on m1 must rechain m1, m3, and m4
	if err := s.CompactFact(t.Context(), "m1", "Compacted claim 1", []string{"a.go"}, ""); err != nil {
		t.Fatalf("CompactFact: %v", err)
	}
	if audit, err := s.VerifyChain(""); err != nil || !audit.Intact || audit.TotalBlocks != 3 {
		t.Fatalf("chain broken after CompactFact: err=%v, audit=%+v", err, audit)
	}

	// 3. PruneFacts on proj-a keeping only m1 removes m4 and preserves chain across m1, m3
	if err := s.PruneFacts("proj-a", []string{"m1"}); err != nil {
		t.Fatalf("PruneFacts: %v", err)
	}
	if audit, err := s.VerifyChain(""); err != nil || !audit.Intact || audit.TotalBlocks != 2 {
		t.Fatalf("chain broken after PruneFacts: err=%v, audit=%+v", err, audit)
	}
}
