package store

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"time"
)

// CheckStatus represents the diagnostic outcome of a check.
type CheckStatus string

// CheckStatus constants indicate the diagnostic severity or result.
const (
	StatusPass CheckStatus = "PASS"
	StatusWarn CheckStatus = "WARN"
	StatusFail CheckStatus = "FAIL"
)

// CheckResult represents a single diagnostic check.
type CheckResult struct {
	Category string      `json:"category"`
	Name     string      `json:"name"`
	Status   CheckStatus `json:"status"`
	Message  string      `json:"message"`
	Detail   string      `json:"detail,omitempty"`
	Repair   string      `json:"repair,omitempty"`
}

// DefaultContinuityToolCount mirrors mcp.ContinuityToolCount. It lives here so
// internal/store can report continuity-surface health without importing
// internal/mcp, which already imports this package.
const DefaultContinuityToolCount = 10

// CheckMCPClientCache compares the client-cached Antigravity MCP schema
// directory against the expected continuity surface. A stale cache hides tools
// that the rulebooks already instruct agents to call.
func CheckMCPClientCache(homeDir string, expectedMinTools int) CheckResult {
	res := CheckResult{Category: "MCP", Name: "MCP Continuity Surface & Client Cache"}
	if expectedMinTools <= 0 {
		expectedMinTools = DefaultContinuityToolCount
	}
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	cacheDir := filepath.Join(homeDir, ".gemini", "antigravity", "mcp", "sila")
	fi, err := os.Stat(cacheDir)
	if err != nil || !fi.IsDir() {
		res.Status = StatusPass
		res.Message = fmt.Sprintf("MCP continuity profile ready (%d tools registered; no local Antigravity schema cache directory to check).", expectedMinTools)
		return res
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		res.Status = StatusWarn
		res.Message = fmt.Sprintf("Antigravity MCP schema cache could not be read: %v", err)
		res.Repair = "Restart your AI harness or run 'sila install' so the client refreshes its MCP tools/list cache."
		return res
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			count++
		}
	}
	if count < expectedMinTools {
		res.Status = StatusWarn
		res.Message = fmt.Sprintf("Antigravity MCP schema cache has %d tool(s); expected at least %d continuity tools.", count, expectedMinTools)
		res.Repair = "Restart your AI harness or run 'sila install' so the client refreshes its MCP tools/list cache."
		return res
	}
	res.Status = StatusPass
	res.Message = fmt.Sprintf("Antigravity MCP schema cache is current (%d tool schemas cached).", count)
	return res
}

// MCPProcess describes one running Sila MCP server process.
type MCPProcess struct {
	PID        int    `json:"pid"`
	Client     string `json:"client,omitempty"`
	Executable string `json:"executable,omitempty"`
	Stale      bool   `json:"stale"`
	Orphan     bool   `json:"orphan,omitempty"`
}

// MCPFreshnessReport summarizes running Sila MCP server processes.
type MCPFreshnessReport struct {
	Running          int          `json:"running"`
	Stale            int          `json:"stale"`
	Orphans          int          `json:"orphans"`
	RestartRequired  bool         `json:"restart_required"`
	InstalledVersion string       `json:"installed_version,omitempty"`
	Processes        []MCPProcess `json:"processes,omitempty"`
}

// DoctorReport contains all diagnostic checks and overall summary.
type DoctorReport struct {
	Timestamp      time.Time           `json:"timestamp"`
	StorePath      string              `json:"store_path"`
	Passed         int                 `json:"passed"`
	Warnings       int                 `json:"warnings"`
	Failures       int                 `json:"failures"`
	OverallStatus  CheckStatus         `json:"overall_status"`
	Checks         []CheckResult       `json:"checks"`
	MCP            *MCPFreshnessReport `json:"mcp,omitempty"`
	RepairExecuted bool                `json:"repair_executed"`
	RepairedItems  []string            `json:"repaired_items,omitempty"`
}

// Doctor runs an end-to-end diagnostic audit of the knowledge store, tables,
// FTS5 virtual indexes, disk permissions, and projects on disk.
func (s *Store) Doctor(repair bool) (*DoctorReport, error) {
	rep := &DoctorReport{
		Timestamp: time.Now().UTC(),
		StorePath: s.Path,
		Checks:    make([]CheckResult, 0, 16),
	}

	// 1. SQLite DB Integrity Check
	var integrityResult string
	err := s.DB.QueryRow(`PRAGMA integrity_check(10)`).Scan(&integrityResult)
	if err != nil {
		rep.addCheck("Database", "SQLite Integrity Check", StatusFail, fmt.Sprintf("Query failed: %v", err), "", "Check database file permissions or restore from backup.")
	} else if integrityResult == "ok" {
		rep.addCheck("Database", "SQLite Integrity Check", StatusPass, "Database pages and B-trees are physically intact (ok).", "", "")
	} else {
		rep.addCheck("Database", "SQLite Integrity Check", StatusFail, fmt.Sprintf("Integrity issues detected: %s", integrityResult), "", "Database corruption detected. Rebuild or restore store.")
	}

	// 2. Schema Migration Version Check
	var currentVer int
	_ = s.DB.QueryRow(`PRAGMA user_version`).Scan(&currentVer)
	targetVer := CurrentSchemaVersion()
	if currentVer > targetVer {
		rep.addCheck("Schema", "Schema Migrations", StatusFail,
			fmt.Sprintf("Store schema is version %d, newer than this sila build supports (%d).", currentVer, targetVer),
			"", "Upgrade sila to a build that understands this schema, or restore a backup made with this version.")
	} else if currentVer >= targetVer {
		rep.addCheck("Schema", "Schema Migrations", StatusPass, fmt.Sprintf("Store schema is up to date (version %d/%d).", currentVer, targetVer), "", "")
	} else {
		rep.addCheck("Schema", "Schema Migrations", StatusWarn, fmt.Sprintf("Schema is at version %d; latest is %d.", currentVer, targetVer), "", "Run `sila doctor --repair` to apply pending migrations.")
	}

	// 2b. Hash-Chained Fact Ledger Integrity
	chainRes, chainErr := s.VerifyChain("")
	if chainErr != nil {
		rep.addCheck("Ledger", "Fact Hash Chain Integrity", StatusFail, fmt.Sprintf("Hash chain audit query failed: %v", chainErr), "", "Run `sila facts verify` to inspect ledger.")
	} else if !chainRes.Intact {
		var detailMsg string
		if len(chainRes.Violations) > 0 {
			detailMsg = strings.Join(chainRes.Violations, "; ")
		}
		incidentNote := ""
		if len(chainRes.Incidents) > 0 {
			incidentNote = fmt.Sprintf(" (%d inspectable incident record(s))", len(chainRes.Incidents))
		}
		repairSuggestion := "Run `sila facts verify` to inspect incident evidence (or `sila facts rebaseline` if intentional)."
		if chainRes.LegacyBlocks > 0 && len(chainRes.Violations) == chainRes.LegacyBlocks {
			repairSuggestion = "Run `sila doctor --repair` to initialize hashes for legacy unhashed blocks."
		}
		rep.addCheck("Ledger", "Fact Hash Chain Integrity", StatusFail,
			fmt.Sprintf("Ledger tampering or corruption detected: %d violation(s) across %d block(s)%s.", len(chainRes.Violations), chainRes.TotalBlocks, incidentNote),
			detailMsg, repairSuggestion)
	} else {
		tipPreview := chainRes.TipHash
		if len(tipPreview) > 16 {
			tipPreview = tipPreview[:16] + "…"
		}
		rep.addCheck("Ledger", "Fact Hash Chain Integrity", StatusPass, fmt.Sprintf("Tamper-evident ledger intact (%d blocks verified, tip: %s).", chainRes.TotalBlocks, tipPreview), "", "")
	}

	// 3. FTS5 Virtual Table Index Consistency
	allowedTables := map[string]bool{
		"messages":     true,
		"messages_fts": true,
		"facts":        true,
		"facts_fts":    true,
		"docs":         true,
		"docs_fts":     true,
	}
	checkFTS := func(mainTable, ftsTable string) {
		if !allowedTables[mainTable] || !allowedTables[ftsTable] {
			rep.addCheck("Index", fmt.Sprintf("FTS5 %s Index", ftsTable), StatusFail, "Invalid table name provided for integrity check", "", "Code bug: unauthorized table accessed.")
			return
		}
		var mainCount, ftsCount int64
		errMain := s.DB.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s`, mainTable)).Scan(&mainCount)
		errFTS := s.DB.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s`, ftsTable)).Scan(&ftsCount)

		if errMain != nil || errFTS != nil {
			rep.addCheck("Index", fmt.Sprintf("FTS5 %s Index", ftsTable), StatusFail, fmt.Sprintf("Failed querying %s/%s: main=%v, fts=%v", mainTable, ftsTable, errMain, errFTS), "", fmt.Sprintf("Run `sila doctor --repair` to rebuild %s index.", ftsTable))
			return
		}

		var isReadOnly int
		_ = s.DB.QueryRow("PRAGMA query_only").Scan(&isReadOnly)
		if isReadOnly == 0 {
			if _, errIntegrity := s.DB.Exec(fmt.Sprintf(`INSERT INTO %s(%s) VALUES('integrity-check');`, ftsTable, ftsTable)); errIntegrity != nil {
				if !strings.Contains(strings.ToLower(errIntegrity.Error()), "readonly") {
					if repair {
						if errRebuild := s.RebuildFTSIndex(ftsTable); errRebuild == nil {
							rep.addCheck("Index", fmt.Sprintf("FTS5 %s Integrity", ftsTable), StatusPass, fmt.Sprintf("Auto-healed corrupted %s index online.", ftsTable), "", "")
							return
						}
					}
					rep.addCheck("Index", fmt.Sprintf("FTS5 %s Integrity", ftsTable), StatusFail, fmt.Sprintf("FTS5 integrity check failed on %s: %v", ftsTable, errIntegrity), "", fmt.Sprintf("Run `sila doctor --repair` to rebuild %s index.", ftsTable))
					return
				}
			}
		}

		if mainCount == ftsCount {
			rep.addCheck("Index", fmt.Sprintf("FTS5 %s Index", ftsTable), StatusPass, fmt.Sprintf("%d rows synchronized between %s and %s.", mainCount, mainTable, ftsTable), "", "")
		} else {
			diff := mainCount - ftsCount
			if diff < 0 {
				diff = -diff
			}
			rep.addCheck("Index", fmt.Sprintf("FTS5 %s Index", ftsTable), StatusWarn, fmt.Sprintf("Row count mismatch: %s has %d, %s has %d (drift: %d).", mainTable, mainCount, ftsTable, ftsCount, diff), "", fmt.Sprintf("Run `sila doctor --repair` to rebuild %s index.", ftsTable))
		}
	}

	checkFTS("messages", "messages_fts")
	checkFTS("facts", "facts_fts")
	checkFTS("docs", "docs_fts")

	// 3b. Typed Memory Envelope Health
	if mem, memErr := s.MemoryDiagnostics(); memErr == nil {
		issues := mem.OrphanedAssertions + mem.OrphanedEvidence + mem.OrphanedRelations +
			mem.BrokenRelationTargets + mem.CorruptEnvelopes + mem.FTSMissing +
			mem.OrphanLifecycleEvents + mem.BrokenLifecycleChains + mem.StateIndexMismatches
		if issues == 0 {
			rep.addCheck("Index", "Typed Memory Envelopes", StatusPass,
				fmt.Sprintf("Schema v%d: %d record(s), %d lifecycle event(s), normalized rows and memory_fts are consistent.",
					mem.SchemaVersion, mem.Records, mem.LifecycleEvents), "", "")
		} else {
			rep.addCheck("Index", "Typed Memory Envelopes", StatusWarn,
				fmt.Sprintf("Schema v%d: %d record(s), %d lifecycle event(s), %d orphaned row(s), %d broken relation target(s), %d corrupt envelope(s), %d missing FTS row(s), %d orphan lifecycle event(s), %d broken lifecycle chain(s), %d state/index mismatch(es).",
					mem.SchemaVersion, mem.Records, mem.LifecycleEvents, mem.OrphanedAssertions+mem.OrphanedEvidence+mem.OrphanedRelations,
					mem.BrokenRelationTargets, mem.CorruptEnvelopes, mem.FTSMissing,
					mem.OrphanLifecycleEvents, mem.BrokenLifecycleChains, mem.StateIndexMismatches), "",
				"Run `sila doctor --repair` to prune unreachable child rows and rebuild memory_fts; corrupt envelopes are reported, never deleted.")
		}
	} else {
		rep.addCheck("Index", "Typed Memory Envelopes", StatusWarn, fmt.Sprintf("Typed memory diagnostics unavailable: %v", memErr), "", "")
	}

	// 3c. Memory Relevance Feedback Ledger Health
	if fb, fbErr := s.MemoryFeedbackDiagnostics(time.Now().UTC()); fbErr == nil {
		issues := fb.OrphanedItems + fb.OrphanedFeedback
		message := fmt.Sprintf("%d retrieval(s), %d returned item(s), %d feedback row(s), %d superseded, %d expired retrieval(s), %d expired feedback row(s), %d orphaned item(s), %d orphaned feedback row(s).",
			fb.Retrievals, fb.Items, fb.Feedback, fb.Superseded,
			fb.ExpiredRetrievals, fb.ExpiredFeedback, fb.OrphanedItems, fb.OrphanedFeedback)
		if issues == 0 {
			rep.addCheck("Index", "Memory Relevance Feedback", StatusPass, message, "", "")
		} else {
			rep.addCheck("Index", "Memory Relevance Feedback", StatusWarn, message, "",
				"Orphaned feedback rows are reported, never deleted. New retrievals prune expired rows in bounded batches.")
		}
	} else {
		rep.addCheck("Index", "Memory Relevance Feedback", StatusWarn, fmt.Sprintf("Memory feedback diagnostics unavailable: %v", fbErr), "", "")
	}

	// 4. File Storage & Fragmentation Check
	stats, err := s.Stats()
	if err == nil {
		if stats.FreelistCount > 100 || stats.FragmentationPct >= 10.0 {
			rep.addCheck("Storage", "Database Fragmentation", StatusWarn, fmt.Sprintf("%.1f%% fragmentation (%d free pages · %d bytes reclaimable).", stats.FragmentationPct, stats.FreelistCount, stats.FreeBytes), "", "Run `sila vacuum` to reclaim unused pages.")
		} else {
			rep.addCheck("Storage", "Database Fragmentation", StatusPass, fmt.Sprintf("Storage is compact (%.1f%% fragmentation · %d free pages).", stats.FragmentationPct, stats.FreelistCount), "", "")
		}
	} else {
		rep.addCheck("Storage", "Database Metrics", StatusWarn, fmt.Sprintf("Failed fetching stats: %v", err), "", "")
	}

	// 5. Storage Directory Permissions Check
	dbDir := filepath.Dir(s.FilePath())
	if fi, err := os.Stat(dbDir); err == nil {
		mode := fi.Mode().Perm()
		if mode&0077 != 0 {
			rep.addCheck("Security", "Store Directory Permissions", StatusWarn, fmt.Sprintf("Directory %s has mode %04o (group/world accessible).", dbDir, mode), "", fmt.Sprintf("chmod 700 %s", dbDir))
		} else {
			rep.addCheck("Security", "Store Directory Permissions", StatusPass, fmt.Sprintf("Directory %s is private (%04o).", dbDir, mode), "", "")
		}
	}
	var exposed []string
	for _, path := range []string{s.Path, s.Path + "-wal", s.Path + "-shm", s.Path + "-journal"} {
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0077 != 0 {
			exposed = append(exposed, filepath.Base(path)+fmt.Sprintf(" (%04o)", fi.Mode().Perm()))
		}
	}
	if len(exposed) > 0 {
		rep.addCheck("Security", "Store File Permissions", StatusWarn, "Database files are group/world accessible.", strings.Join(exposed, ", "), "Run `sila doctor --repair` to restrict them to 0600.")
	} else {
		rep.addCheck("Security", "Store File Permissions", StatusPass, "Database files are private (0600).", "", "")
	}

	// 6. Registered Projects Validation on Disk
	projects, err := s.Projects()
	if err == nil {
		var missingProjects []string
		for _, p := range projects {
			if p.Path == filepath.Join(os.Getenv("HOME"), "sila", "no-project") {
				continue
			}
			if _, err := os.Stat(p.Path); os.IsNotExist(err) {
				missingProjects = append(missingProjects, p.ID+" ("+p.Path+")")
			}
		}
		if len(missingProjects) > 0 {
			rep.addCheck("Projects", "Registered Project Repositories", StatusWarn, fmt.Sprintf("%d registered project paths do not exist on disk.", len(missingProjects)), strings.Join(missingProjects, ", "), "Run `sila doctor --repair` to canonicalize worktrees and prune deleted projects.")
		} else {
			rep.addCheck("Projects", "Registered Project Repositories", StatusPass, fmt.Sprintf("All %d registered project directories exist on disk.", len(projects)), "", "")
		}
	}

	// 6b. Duplicate registrations pointing at the same real directory.
	if pairs, pairsErr := s.AliasDuplicatePairs(); pairsErr == nil {
		if len(pairs) > 0 {
			details := make([]string, 0, len(pairs))
			for _, p := range pairs {
				details = append(details, fmt.Sprintf("%s ↔ %s (%s)", p.KeepID, p.DropID, p.KeepPath))
			}
			rep.addCheck("Projects", "Duplicate Project Registrations", StatusWarn, fmt.Sprintf("%d registrations point at the same real directory through path aliases.", len(pairs)), strings.Join(details, ", "), "Run `sila doctor --repair` to merge them, or `sila projects merge <keep> <drop>`.")
		} else {
			rep.addCheck("Projects", "Duplicate Project Registrations", StatusPass, "Each real directory is registered once.", "", "")
		}
	}

	// 7. Grounding & Stale Claim Ratio. Staleness counts only approved,
	// non-invariant claims whose files disappeared; unapproved candidates and
	// conceptual invariants are not drift and must not inflate this number.
	_, factsCount, verifiedCount, _, err := s.Counts()
	if err == nil && factsCount > 0 {
		stale := 0
		if staleFacts, sErr := s.StaleFacts("", 0); sErr == nil {
			stale = len(staleFacts)
		}
		stalePct := float64(stale) / float64(factsCount) * 100.0
		if stalePct > 30.0 && stale > 20 {
			rep.addCheck("Knowledge", "Claim Grounding & Staleness", StatusWarn, fmt.Sprintf("%d of %d claims (%.1f%%) reference missing files on disk.", stale, factsCount, stalePct), "", "Run `sila dream reconcile` to refresh on-disk file existence.")
		} else {
			rep.addCheck("Knowledge", "Claim Grounding & Staleness", StatusPass, fmt.Sprintf("%d of %d claims verified against disk files.", verifiedCount, factsCount), "", "")
		}
	}

	// 8. Always-Load Context Surface Hygiene
	cwd, _ := os.Getwd()
	budget := s.MeasureContextBudget(cwd, nil)
	switch budget.Status {
	case "heavy":
		rep.addCheck("Context", "Always-Load Context Surface", StatusWarn,
			fmt.Sprintf("Context surface is heavy (%s). Consumes significant tokens on every session start.", budget.FormatSummary()),
			strings.Join(budget.TrimTargets, "; "),
			"Move accreted history into *-history files; keep current state + a pointer.")
	case "watch":
		rep.addCheck("Context", "Always-Load Context Surface", StatusWarn,
			fmt.Sprintf("Context surface exceeds soft guidelines (%s).", budget.FormatSummary()),
			strings.Join(budget.TrimTargets, "; "),
			"Consider trimming historical notes or completed goals into *-history files.")
	default:
		rep.addCheck("Context", "Always-Load Context Surface", StatusPass,
			fmt.Sprintf("Context surface is lean: %s.", budget.FormatSummary()), "", "")
	}

	// 9. Toolchain Version Drift Sentry
	tcCheck := AuditToolchain(cwd)
	rep.addCheck(tcCheck.Category, tcCheck.Name, tcCheck.Status, tcCheck.Message, tcCheck.Detail, tcCheck.Repair)

	// 9b. MCP continuity surface vs. client-side schema cache
	mcpCache := CheckMCPClientCache(os.Getenv("HOME"), DefaultContinuityToolCount)
	rep.addCheck(mcpCache.Category, mcpCache.Name, mcpCache.Status, mcpCache.Message, mcpCache.Detail, mcpCache.Repair)

	// 10. Concurrency & Goroutine Profile Check
	prof := pprof.Lookup("goroutineleak")
	if prof == nil {
		prof = pprof.Lookup("goroutine")
	}
	if prof != nil {
		count := prof.Count()
		if count > 200 {
			rep.addCheck("Concurrency", "Goroutine Leak Sentry", StatusWarn,
				fmt.Sprintf("Elevated active goroutine count: %d goroutines", count),
				"Possible orphaned background watcher, poller, or subagent routine.",
				"Check active processes using `sila live status` or terminate orphaned watchers.")
		} else {
			rep.addCheck("Concurrency", "Goroutine Leak Sentry", StatusPass,
				fmt.Sprintf("Goroutine profile clean: %d active routines (within normal bounds).", count),
				"", "")
		}
	}

	// Compute overall summary counts
	rep.Finalize()

	if rep.Failures > 0 {
		rep.OverallStatus = StatusFail
	} else if rep.Warnings > 0 {
		rep.OverallStatus = StatusWarn
	} else {
		rep.OverallStatus = StatusPass
	}

	// 9. Execute Repairs if requested
	if repair {
		var repaired []string

		// Merge registrations that resolve to the same real directory before the
		// worktree pass, which assumes each real repository has one row.
		if aliasMerged, aliasErr := s.MergeAliasDuplicates(); aliasErr == nil {
			repaired = append(repaired, aliasMerged...)
		} else {
			repaired = append(repaired, fmt.Sprintf("alias merge incomplete: %v", aliasErr))
		}

		// Canonicalize Git worktrees and prune deleted empty project registrations
		if tx, err := s.DB.Begin(); err == nil {
			_, _ = tx.Exec(`PRAGMA defer_foreign_keys = ON`)
			worktreesRepaired := false
			if err := repairWorktreeProjects(tx); err == nil {
				worktreesRepaired = true
			} else {
				repaired = append(repaired, fmt.Sprintf("worktree canonicalization incomplete: %v", err))
			}
			type emptyProj struct {
				id   string
				path string
			}
			// Memos are facts with kind='memo', so the facts count covers them;
			// a separate memos table does not exist and naming it here aborted
			// the whole prune query, leaving every dead registration in place.
			pRows, pErr := tx.Query(`
				SELECT p.id, p.path FROM projects p
				WHERE (SELECT count(*) FROM messages WHERE project_id = p.id) = 0
				  AND (SELECT count(*) FROM facts WHERE project_id = p.id) = 0
				  AND (SELECT count(*) FROM docs WHERE project_id = p.id) = 0
				  AND (SELECT count(*) FROM handoffs WHERE project_id = p.id) = 0
			`)
			if pErr == nil {
				var toPrune []emptyProj
				for pRows.Next() {
					var ep emptyProj
					if err := pRows.Scan(&ep.id, &ep.path); err == nil {
						_, statErr := os.Stat(ep.path)
						isMissing := os.IsNotExist(statErr) && ep.path != filepath.Join(os.Getenv("HOME"), "sila", "no-project")
						isTempOrNonRepo := statErr == nil && !IsTempDirPath(ep.path) && !IsRepoOrWorkspaceDir(ep.path)
						if isMissing || isTempOrNonRepo {
							toPrune = append(toPrune, ep)
						}
					}
				}
				pRows.Close()
				for _, ep := range toPrune {
					if _, err := tx.Exec(`DELETE FROM projects WHERE path = ?`, ep.path); err == nil {
						repaired = append(repaired, fmt.Sprintf("Pruned deleted empty project registration: %s (%s)", ep.id, ep.path))
					}
				}
			}
			if worktreesRepaired {
				_ = tx.Commit()
				repaired = append(repaired, "Canonicalized Git worktree project registrations to main repository paths")
			} else {
				_ = tx.Rollback()
			}
		}

		// Rebuild FTS5 indexes
		if currentVer < targetVer {
			if err := migrate(s.DB); err == nil {
				repaired = append(repaired, fmt.Sprintf("Applied pending schema migrations (upgraded to version %d)", targetVer))
			}
		}

		var ftsRebuilt bool
		for _, ftsTable := range []string{"messages_fts", "facts_fts", "docs_fts"} {
			if err := s.RebuildFTSIndex(ftsTable); err == nil {
				ftsRebuilt = true
			}
		}
		if ftsRebuilt {
			repaired = append(repaired, "Rebuilt FTS5 virtual full-text indexes (messages_fts, facts_fts, docs_fts)")
		}

		// Typed memory: prune unreachable child rows and rebuild memory_fts.
		if mem, memErr := s.MemoryDiagnostics(); memErr == nil {
			if mem.OrphanedAssertions+mem.OrphanedEvidence+mem.OrphanedRelations+mem.FTSMissing > 0 {
				if memRepairs, rErr := s.RepairMemoryIndexes(); rErr != nil {
					repaired = append(repaired, fmt.Sprintf("Typed memory repair error: %v", rErr))
				} else {
					repaired = append(repaired, memRepairs...)
				}
			}
		}

		// Repair fact ledger cryptographic hash chain if unhashed legacy blocks exist
		if count, err := s.RepairChain(); err != nil {
			repaired = append(repaired, fmt.Sprintf("Fact ledger repair error: %v", err))
		} else if count > 0 {
			repaired = append(repaired, fmt.Sprintf("Initialized %d legacy unhashed fact ledger blocks (cryptographic SHA-256 chain established)", count))
		}
		if postAudit, err := s.VerifyChain(""); err == nil && !postAudit.Intact {
			repaired = append(repaired, fmt.Sprintf("Preserved %d integrity violation(s); routine repair refuses to erase tampering on previously hashed facts. Inspect with `sila facts verify` or rebaseline with `sila facts rebaseline`.", len(postAudit.Violations)))
		}

		// Re-tighten directory permissions if needed
		if fi, err := os.Stat(dbDir); err == nil && fi.Mode().Perm()&0077 != 0 {
			if err := os.Chmod(dbDir, 0700); err == nil {
				repaired = append(repaired, fmt.Sprintf("Restored owner-only 0700 permissions on %s", dbDir))
			}
		}
		if err := s.SecurePermissions(); err == nil {
			repaired = append(repaired, "Restored owner-only 0600 permissions on database files")
		}

		// Optimize SQLite query planner
		if _, err := s.DB.Exec(`PRAGMA optimize;`); err == nil {
			repaired = append(repaired, "Optimized SQLite query planner statistics")
		}

		// Defragment & vacuum if freelist > 0
		if stats != nil && stats.FreelistCount > 0 {
			if vRes, err := s.Vacuum(); err == nil && vRes != nil {
				repaired = append(repaired, fmt.Sprintf("Defragmented database and reclaimed %d bytes", vRes.ReclaimedBytes))
			}
		}

		// Synchronize toolchain version drift in CI workflows if any
		if tcRepaired, err := RepairToolchain(cwd); err == nil && len(tcRepaired) > 0 {
			repaired = append(repaired, tcRepaired...)
		}

		rep.RepairExecuted = true
		rep.RepairedItems = repaired
	}

	return rep, nil
}

// HasCheck reports whether the report already contains a check for the given category.
func (r *DoctorReport) HasCheck(category string) bool {
	for _, c := range r.Checks {
		if c.Category == category {
			return true
		}
	}
	return false
}

// AddCheck appends a diagnostic result. Callers outside the store package use
// it to contribute checks that belong to the command layer (for example, how
// sila itself is currently being run), then call Finalize so the summary
// counts and overall status include it.
func (r *DoctorReport) AddCheck(category, name string, status CheckStatus, message, detail, repair string) {
	r.addCheck(category, name, status, message, detail, repair)
}

// Finalize recomputes the summary counts and overall status from Checks.
func (r *DoctorReport) Finalize() {
	r.Passed, r.Warnings, r.Failures = 0, 0, 0
	for _, c := range r.Checks {
		switch c.Status {
		case StatusPass:
			r.Passed++
		case StatusWarn:
			r.Warnings++
		case StatusFail:
			r.Failures++
		}
	}
	switch {
	case r.Failures > 0:
		r.OverallStatus = StatusFail
	case r.Warnings > 0:
		r.OverallStatus = StatusWarn
	default:
		r.OverallStatus = StatusPass
	}
}

func (r *DoctorReport) addCheck(category, name string, status CheckStatus, message, detail, repair string) {
	r.Checks = append(r.Checks, CheckResult{
		Category: category,
		Name:     name,
		Status:   status,
		Message:  message,
		Detail:   detail,
		Repair:   repair,
	})
}
