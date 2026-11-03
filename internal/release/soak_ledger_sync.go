package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

type discoveredFeature struct {
	FeatureID   string
	Title       string
	SourceKind  string
	CommitSHA   string
	MappedTools []string
	StartedAt   time.Time
}

// syncFeatureSoakLedger discovers all post-tag certified goals, feature/fix commits,
// and worktree feature additions, evaluates their live usage in tool_events/prime_events,
// persists their state and today's daily usage report in sila.db, and attaches the
// ledger to SoakReport.
func syncFeatureSoakLedger(report *SoakReport, repoDir, dbPath, baseTag string, tagTime, now time.Time, reqHours float64, reqDays int) {
	features := discoverUnreleasedFeatures(repoDir, baseTag, tagTime, now, report.CertifiedGoals)
	if len(features) == 0 {
		return
	}

	st, err := store.Open(dbPath)
	if err != nil || st == nil {
		// Fallback to read-only inspection if DB cannot be opened read-write
		stRO, exists, errRO := store.OpenInspection(dbPath)
		if errRO != nil || !exists || stRO == nil {
			return
		}
		defer stRO.Close()
		st = stRO
	} else {
		defer st.Close()
	}

	projID := store.ProjectID(repoDir)
	if projID == "" {
		projID = "sila"
	}
	_ = st.MarkPriorTagFeatureSoaksReleased(projID, baseTag)

	existingRows, _ := st.ListFeatureSoaks(projID, false)
	existingStart := make(map[string]time.Time, len(existingRows))
	for _, r := range existingRows {
		if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
			existingStart[r.FeatureID] = t.UTC()
		}
	}

	earliest := now
	for _, f := range features {
		if t, ok := existingStart[f.FeatureID]; ok && t.Before(f.StartedAt) {
			f.StartedAt = t
		}
		if f.StartedAt.Before(earliest) {
			earliest = f.StartedAt
		}
	}

	allTools, _ := st.ListToolEvents(store.ToolEventFilter{
		Since: earliest,
		Limit: 10000,
	})
	allPrimes, _ := st.ListPrimeEvents(store.PrimeEventFilter{
		Since: earliest,
		Limit: 5000,
	})

	todayStr := now.UTC().Format("2006-01-02")
	nowISO := now.UTC().Format(time.RFC3339)

	var records []store.FeatureSoakRecord
	for _, f := range features {
		if t, ok := existingStart[f.FeatureID]; ok && t.Before(f.StartedAt) {
			f.StartedAt = t
		}
		elapsed := now.Sub(f.StartedAt).Hours()
		if elapsed < 0 {
			elapsed = 0
		}
		dayNum := int(elapsed/24.0) + 1
		if dayNum < 1 {
			dayNum = 1
		}

		usageCount, errCount, activeDays, lastErr := evaluateFeatureUsage(f, allTools, allPrimes)
		status := "soaking"
		switch {
		case errCount > 0:
			status = "needs_debug"
		case elapsed >= reqHours && activeDays >= reqDays:
			status = "ready"
		default:
			status = "soaking"
		}

		rec := store.FeatureSoakRecord{
			ProjectID:      projID,
			FeatureID:      f.FeatureID,
			Title:          f.Title,
			SourceKind:     f.SourceKind,
			CommitSHA:      f.CommitSHA,
			BaseTag:        baseTag,
			MappedTools:    f.MappedTools,
			StartedAt:      f.StartedAt.UTC().Format(time.RFC3339),
			ElapsedHours:   elapsed,
			ActiveDays:     activeDays,
			UsageCount:     usageCount,
			ErrorCount:     errCount,
			Status:         status,
			LastError:      lastErr,
			LastVerifiedAt: nowISO,
		}
		_ = st.UpsertFeatureSoak(rec)
		records = append(records, rec)

		summary := "soaking cleanly"
		if status == "needs_debug" {
			summary = "errors detected: " + lastErr
		} else if status == "ready" {
			summary = "3-day soak verified"
		}
		_ = st.RecordFeatureSoakDailyReport(store.FeatureSoakDailyReport{
			ProjectID:    projID,
			FeatureID:    f.FeatureID,
			ReportDate:   todayStr,
			DayNumber:    dayNum,
			ElapsedHours: elapsed,
			ActiveDays:   activeDays,
			UsageCount:   usageCount,
			ErrorCount:   errCount,
			Status:       status,
			Summary:      summary,
			CreatedAt:    nowISO,
		})
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].StartedAt != records[j].StartedAt {
			return records[i].StartedAt > records[j].StartedAt
		}
		return records[i].FeatureID < records[j].FeatureID
	})
	report.FeatureLedger = records
	report.DailyReports, _ = st.ListFeatureSoakReports(projID, "", 15)
	_ = st.SetState("soak_ledger:last_sync_date:"+projID, todayStr)
}

// SyncSoakDailyIfDue runs a fast read-only check on sila.db and, if today's
// feature soak ledger and daily usage reports have not yet been collected for
// the repository, triggers EvaluateSoak automatically.
func SyncSoakDailyIfDue(repoDir, dbPath string) {
	if repoDir == "" {
		repoDir = ResolveGitRepoDir("")
	}
	if dbPath == "" {
		dbPath = store.DefaultDBPath()
	}
	projID := store.ProjectID(repoDir)
	if projID == "" {
		projID = "sila"
	}
	today := time.Now().UTC().Format("2006-01-02")

	if stRO, exists, err := store.OpenInspection(dbPath); err == nil && exists && stRO != nil {
		lastDate, _ := stRO.GetState("soak_ledger:last_sync_date:" + projID)
		_ = stRO.Close()
		if lastDate == today {
			return
		}
	}

	_ = EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  dbPath,
	})
}

func discoverUnreleasedFeatures(repoDir, baseTag string, tagTime, now time.Time, certGoals []SoakGoalInfo) []discoveredFeature {
	seen := make(map[string]bool)
	var out []discoveredFeature

	for _, cg := range certGoals {
		t, err := time.Parse(time.RFC3339, cg.CertifiedAt)
		if err != nil {
			t = now
		}
		title := readGoalTitle(repoDir, cg.Slug)
		out = append(out, discoveredFeature{
			FeatureID:   cg.Slug,
			Title:       title,
			SourceKind:  "goal",
			CommitSHA:   cg.CommitSHA,
			MappedTools: inferMappedTools(cg.Slug, "", title),
			StartedAt:   t.UTC(),
		})
		seen[cg.Slug] = true
	}

	// Scan feat/fix/perf commits since baseTag that are not already covered by a certified goal.
	args := []string{"log", "--pretty=format:%h%x1f%cI%x1f%s"}
	if baseTag != "" && baseTag != "v0.0.0" {
		args = append(args, baseTag+"..HEAD")
	} else {
		args = append(args, "-n", "30")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	if raw, err := cmd.Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			parts := strings.SplitN(strings.TrimSpace(line), "\x1f", 3)
			if len(parts) != 3 {
				continue
			}
			hash, dateStr, subj := parts[0], parts[1], strings.TrimSpace(parts[2])
			m := convCommitRE.FindStringSubmatch(subj)
			if len(m) == 0 {
				continue
			}
			cType := strings.ToLower(m[1])
			cScope := strings.ToLower(m[2])
			if cType != "feat" && cType != "fix" && cType != "perf" {
				continue
			}
			if gm := goalPathRe.FindStringSubmatch(subj); len(gm) > 1 && seen[gm[1]] {
				continue
			}
			commitTime, err := time.Parse(time.RFC3339, dateStr)
			if err != nil {
				continue
			}
			if !tagTime.IsZero() && !commitTime.After(tagTime) {
				continue
			}
			featID := cType + "-" + hash
			if cScope != "" {
				featID = cType + "-" + cScope + "-" + hash
			}
			if seen[featID] {
				continue
			}
			seen[featID] = true
			out = append(out, discoveredFeature{
				FeatureID:   featID,
				Title:       subj,
				SourceKind:  "commit",
				CommitSHA:   hash,
				MappedTools: inferMappedTools(featID, cScope, subj),
				StartedAt:   commitTime.UTC(),
			})
		}
	}

	// Also detect uncommitted worktree feature modules (e.g. gate_receipt.go, soak.go).
	wtCmd := exec.Command("git", "status", "--porcelain")
	wtCmd.Dir = repoDir
	if rawWT, err := wtCmd.Output(); err == nil {
		rawStr := string(rawWT)
		if strings.Contains(rawStr, "gate_receipt.go") && !seen["sila-gate-receipts"] {
			seen["sila-gate-receipts"] = true
			out = append(out, discoveredFeature{
				FeatureID:   "sila-gate-receipts",
				Title:       "Working-tree SHA-256 green receipt cache for sila gate",
				SourceKind:  "worktree",
				MappedTools: []string{"gate", "run_gate_pipeline"},
				StartedAt:   fileModTimeOrNow(filepath.Join(repoDir, "cmd", "sila", "gate_receipt.go"), now),
			})
		}
		if strings.Contains(rawStr, "soak.go") && !seen["release-3day-soak-gate"] {
			seen["release-3day-soak-gate"] = true
			out = append(out, discoveredFeature{
				FeatureID:   "release-3day-soak-gate",
				Title:       "3-day local dogfood soak and automated DB telemetry ledger",
				SourceKind:  "worktree",
				MappedTools: []string{"release", "prime", "prime_context", "get_status"},
				StartedAt:   fileModTimeOrNow(filepath.Join(repoDir, "internal", "release", "soak.go"), now),
			})
		}
	}

	return out
}

func fileModTimeOrNow(path string, fallback time.Time) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime().UTC()
	}
	return fallback.UTC()
}

func readGoalTitle(repoDir, slug string) string {
	data, err := os.ReadFile(filepath.Join(repoDir, "goals", slug, "goal.md"))
	if err != nil {
		return slug
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return slug
}

func inferMappedTools(slug, scope, title string) []string {
	combined := strings.ToLower(slug + " " + scope + " " + title)
	var tools []string
	add := func(names ...string) {
		for _, n := range names {
			exists := false
			for _, cur := range tools {
				if cur == n {
					exists = true
					break
				}
			}
			if !exists {
				tools = append(tools, n)
			}
		}
	}

	if strings.Contains(combined, "gate") {
		add("gate", "run_gate_pipeline")
	}
	if strings.Contains(combined, "goal") || strings.Contains(combined, "charter") {
		add("goals", "list_goals", "audit_goal", "certify_goal", "claim_goal")
	}
	if strings.Contains(combined, "prime") || strings.Contains(combined, "subagent") || strings.Contains(combined, "session") {
		add("prime", "prime_context")
	}
	if strings.Contains(combined, "salam") || strings.Contains(combined, "heal") || strings.Contains(combined, "onboard") {
		add("salam", "heal", "get_status")
	}
	if strings.Contains(combined, "memo") || strings.Contains(combined, "fact") || strings.Contains(combined, "memory") {
		add("memo", "save_memo", "get_project_facts")
	}
	if strings.Contains(combined, "search") || strings.Contains(combined, "doc") {
		add("search", "omni_search", "search_docs")
	}
	if strings.Contains(combined, "hook") || strings.Contains(combined, "harness") {
		add("hook", "prime", "prime_context")
	}
	if strings.Contains(combined, "mcp") {
		add("*")
	}
	if len(tools) == 0 {
		add("*")
	}
	return tools
}

func evaluateFeatureUsage(f discoveredFeature, allTools []store.ToolEvent, allPrimes []store.PrimeEvent) (usageCount, errCount, activeDays int, lastErr string) {
	matchAll := false
	matchPrime := false
	toolSet := make(map[string]bool, len(f.MappedTools))
	for _, m := range f.MappedTools {
		if m == "*" {
			matchAll = true
		}
		if m == "prime" || m == "prime_context" {
			matchPrime = true
		}
		toolSet[strings.ToLower(m)] = true
	}

	days := make(map[string]struct{})
	for _, ev := range allTools {
		if ev.CreatedAt.Before(f.StartedAt) {
			continue
		}
		tName := strings.ToLower(ev.ToolName)
		matched := matchAll || toolSet[tName] || strings.EqualFold(ev.GoalSlug, f.FeatureID)
		if !matched {
			for k := range toolSet {
				if k != "*" && strings.Contains(tName, k) {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}
		usageCount++
		if !ev.CreatedAt.IsZero() {
			days[ev.CreatedAt.UTC().Format("2006-01-02")] = struct{}{}
		}
		outcome := strings.ToLower(strings.TrimSpace(ev.Outcome))
		if outcome != "" && outcome != "ok" {
			errCount++
			if lastErr == "" {
				lastErr = ev.ToolName + ": " + ev.Outcome
			}
		}
	}

	if matchAll || matchPrime {
		for _, pev := range allPrimes {
			if pev.CreatedAt.Before(f.StartedAt) {
				continue
			}
			usageCount++
			if !pev.CreatedAt.IsZero() {
				days[pev.CreatedAt.UTC().Format("2006-01-02")] = struct{}{}
			}
			st := strings.ToLower(strings.TrimSpace(pev.Status))
			if st == "failed" || st == "error" {
				errCount++
				if lastErr == "" {
					lastErr = "prime (" + pev.Harness + "): " + pev.ErrorClass
				}
			}
		}
	}

	return usageCount, errCount, len(days), lastErr
}
