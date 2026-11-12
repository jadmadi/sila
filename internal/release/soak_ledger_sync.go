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

// syncFeatureSoakLedger discovers all post-tag certified goals and feature/fix commits,
// evaluates each feature on its own independent 72h / 3-active-day timer against
// project-scoped telemetry in tool_events/prime_events, persists state in sila.db,
// and attaches the ledger to SoakReport.
func syncFeatureSoakLedger(report *SoakReport, repoDir, dbPath, baseTag, baseRef string, tagTime, now time.Time, reqHours float64, reqDays int) {
	features := discoverUnreleasedFeatures(repoDir, baseRef, tagTime, now, report.CertifiedGoals)

	var st *store.Store
	if opened, err := store.Open(dbPath); err == nil && opened != nil {
		defer opened.Close()
		st = opened
	} else if stRO, exists, errRO := store.OpenInspection(dbPath); errRO == nil && exists && stRO != nil {
		defer stRO.Close()
		st = stRO
	}

	projID := store.ProjectID(repoDir)
	if projID == "" {
		projID = "sila"
	}

	activeSet := make(map[string]bool, len(features))
	for _, f := range features {
		activeSet[f.FeatureID] = true
	}

	existingStart := make(map[string]time.Time)
	var projectIDs []string
	if st != nil {
		_ = st.MarkPriorTagFeatureSoaksReleased(projID, baseTag)
		projectIDs = soakProjectIDs(st, repoDir)
		existingRows, _ := st.ListFeatureSoaks(projID, false)
		for _, r := range existingRows {
			if !activeSet[r.FeatureID] {
				r.Status = "released"
				_ = st.UpsertFeatureSoak(r)
				continue
			}
			if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
				existingStart[r.FeatureID] = t.UTC()
			}
		}
	}

	if len(features) == 0 {
		return
	}

	earliest := now
	for i := range features {
		if features[i].CommitSHA == "" {
			if t, ok := existingStart[features[i].FeatureID]; ok && t.Before(features[i].StartedAt) && (tagTime.IsZero() || t.After(tagTime)) {
				features[i].StartedAt = t
			}
		}
		if features[i].StartedAt.Before(earliest) {
			earliest = features[i].StartedAt
		}
	}

	var allTools []store.ToolEvent
	var allPrimes []store.PrimeEvent
	if st != nil {
		allTools = soakToolEvents(st, projectIDs, earliest)
		allPrimes = soakPrimeEvents(st, projectIDs, earliest)
	}

	todayStr := now.UTC().Format("2006-01-02")
	nowISO := now.UTC().Format(time.RFC3339)

	var records []store.FeatureSoakRecord
	for _, f := range features {
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
		if st != nil {
			_ = st.UpsertFeatureSoak(rec)
		}
		records = append(records, rec)

		summary := "soaking cleanly"
		if status == "needs_debug" {
			summary = "errors detected: " + lastErr
		} else if status == "ready" {
			summary = "3-day soak verified"
		}
		if st != nil {
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
	}

	records = filterFeatureLedger(records, projectIDs, tagTime)
	sort.Slice(records, func(i, j int) bool {
		if records[i].StartedAt != records[j].StartedAt {
			return records[i].StartedAt > records[j].StartedAt
		}
		return records[i].FeatureID < records[j].FeatureID
	})
	report.FeatureLedger = records
	if st != nil {
		report.DailyReports, _ = st.ListFeatureSoakReports(projID, "", 15)
		_ = st.SetState("soak_ledger:last_sync_date:"+projID, todayStr)
	}
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

func discoverUnreleasedFeatures(repoDir, baseRef string, tagTime, now time.Time, certGoals []SoakGoalInfo) []discoveredFeature {
	seen := make(map[string]bool)
	var out []discoveredFeature

	hasGit := hasGitCommits(repoDir)

	// Scan runtime feat/fix/perf commits since baseTag so every feature's timer
	// follows its own code commit timestamp rather than re-certification paperwork.
	args := []string{"log", "--pretty=format:%x1e%h%x1f%cI%x1f%s", "--name-only"}
	switch {
	case baseRef != "":
		args = append(args, baseRef+"..HEAD")
	case !tagTime.IsZero():
		args = append(args, "--since="+tagTime.UTC().Format(time.RFC3339))
	default:
		args = append(args, "-n", "100")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	if raw, err := cmd.Output(); err == nil {
		for _, block := range strings.Split(string(raw), "\x1e") {
			block = strings.TrimSpace(block)
			if block == "" {
				continue
			}
			lines := strings.Split(block, "\n")
			header := strings.TrimSpace(lines[0])
			parts := strings.SplitN(header, "\x1f", 3)
			if len(parts) != 3 {
				continue
			}
			hash, dateStr, subj := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
			m := convCommitRE.FindStringSubmatch(subj)
			if len(m) == 0 {
				continue
			}
			cType := strings.ToLower(m[1])
			cScope := strings.ToLower(m[2])
			isBreaking := m[3] == "!"
			if !isBreaking && cType != "feat" && cType != "fix" && cType != "perf" {
				continue
			}
			commitTime, err := time.Parse(time.RFC3339, dateStr)
			if err != nil {
				continue
			}
			if !tagTime.IsZero() && !commitTime.After(tagTime) {
				continue
			}
			var touchedFiles []string
			for _, fLine := range lines[1:] {
				if trimmed := strings.TrimSpace(fLine); trimmed != "" {
					touchedFiles = append(touchedFiles, trimmed)
				}
			}
			if isNonRuntimeCommit(touchedFiles) {
				continue
			}

			slug := ""
			if gm := goalPathRe.FindStringSubmatch(subj); len(gm) > 1 {
				slug = gm[1]
			} else if gm := goalTagRe.FindStringSubmatch(subj); len(gm) > 1 {
				slug = gm[1]
			}
			if slug != "" {
				if seen[slug] {
					continue
				}
				seen[slug] = true
				title := readGoalTitle(repoDir, slug)
				out = append(out, discoveredFeature{
					FeatureID:   slug,
					Title:       title,
					SourceKind:  "goal",
					CommitSHA:   hash,
					MappedTools: inferMappedTools(slug, cScope, title+" "+subj),
					StartedAt:   commitTime.UTC(),
				})
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

	for _, cg := range certGoals {
		if seen[cg.Slug] {
			continue
		}
		if hasGit {
			ct := commitTimeInRepo(repoDir, cg.CommitSHA)
			if ct.IsZero() || (!tagTime.IsZero() && !ct.After(tagTime)) {
				continue
			}
			seen[cg.Slug] = true
			title := readGoalTitle(repoDir, cg.Slug)
			out = append(out, discoveredFeature{
				FeatureID:   cg.Slug,
				Title:       title,
				SourceKind:  "goal",
				CommitSHA:   cg.CommitSHA,
				MappedTools: inferMappedTools(cg.Slug, "", title),
				StartedAt:   ct.UTC(),
			})
			continue
		}

		t, err := time.Parse(time.RFC3339, cg.CertifiedAt)
		if err != nil {
			t = now
		}
		seen[cg.Slug] = true
		title := readGoalTitle(repoDir, cg.Slug)
		out = append(out, discoveredFeature{
			FeatureID:   cg.Slug,
			Title:       title,
			SourceKind:  "goal",
			MappedTools: inferMappedTools(cg.Slug, "", title),
			StartedAt:   t.UTC(),
		})
	}

	return out
}

func hasGitCommits(repoDir string) bool {
	if repoDir == "" {
		return false
	}
	cmd := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", "HEAD")
	return cmd.Run() == nil
}

func commitTimeInRepo(repoDir, sha string) time.Time {
	sha = strings.TrimSpace(sha)
	if repoDir == "" || sha == "" {
		return time.Time{}
	}
	cmd := exec.Command("git", "-C", repoDir, "log", "-1", "--format=%cI", sha)
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func isNonRuntimeCommit(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		p := filepath.ToSlash(strings.TrimSpace(f))
		if p == "" {
			continue
		}
		if !isNonRuntimePath(p) {
			return false
		}
	}
	return true
}

func isNonRuntimePath(p string) bool {
	if !strings.Contains(p, "/") && strings.HasSuffix(p, ".md") {
		return true
	}
	if p == "project.jsonc" {
		return true
	}
	for _, prefix := range []string{"goals/", "docs/", "html-docs/", ".cursor/", ".github/", "agents/", "public/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return strings.HasSuffix(p, "/CHANGELOG.md") || strings.HasSuffix(p, "/README.md")
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
	totalEventsSinceStart := 0
	for _, ev := range allTools {
		if ev.CreatedAt.Before(f.StartedAt) {
			continue
		}
		totalEventsSinceStart++
		if !ev.CreatedAt.IsZero() {
			days[ev.CreatedAt.UTC().Format("2006-01-02")] = struct{}{}
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
		outcome := strings.ToLower(strings.TrimSpace(ev.Outcome))
		if outcome != "" && outcome != "ok" {
			errCount++
			if lastErr == "" {
				lastErr = ev.ToolName + ": " + ev.Outcome
			}
		}
	}

	for _, pev := range allPrimes {
		if pev.CreatedAt.Before(f.StartedAt) {
			continue
		}
		totalEventsSinceStart++
		if !pev.CreatedAt.IsZero() {
			days[pev.CreatedAt.UTC().Format("2006-01-02")] = struct{}{}
		}
		if matchAll || matchPrime {
			usageCount++
			st := strings.ToLower(strings.TrimSpace(pev.Status))
			if st == "failed" || st == "error" {
				errCount++
				if lastErr == "" {
					lastErr = "prime (" + pev.Harness + "): " + pev.ErrorClass
				}
			}
		}
	}
	if usageCount == 0 && totalEventsSinceStart > 0 {
		usageCount = totalEventsSinceStart
	}

	return usageCount, errCount, len(days), lastErr
}
