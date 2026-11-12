package release

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

// SoakRequiredHours is the minimum wall-clock soak duration (3 days = 72 hours)
// required between the latest feature/goal certification and cutting a release.
const SoakRequiredHours = 72.0

// SoakRequiredActiveDays is the minimum number of distinct local dogfooding
// calendar days with live telemetry required during the soak window.
const SoakRequiredActiveDays = 3

// SoakMinUsage is the minimum number of live telemetry events required during
// the soak window. Days alone prove calendar time, not real exercise.
const SoakMinUsage = 20

// SoakMinHarnesses is the minimum number of distinct harnesses that must
// exercise the build during the soak window.
const SoakMinHarnesses = 2

// SoakState represents the lifecycle state of the 3-day local dogfood soak.
type SoakState string

const (
	// SoakUpToDate indicates no unreleased feature/fix changes require soaking.
	SoakUpToDate SoakState = "UP_TO_DATE"
	// SoakInProgress indicates features are soaking with zero unresolved errors so far.
	SoakInProgress SoakState = "SOAKING"
	// SoakNeedsDebug indicates live telemetry recorded tool/prime errors during the soak window.
	SoakNeedsDebug SoakState = "NEEDS_DEBUG"
	// SoakReady indicates the 72h + 3-active-day soak completed with zero unresolved errors.
	SoakReady SoakState = "READY"
)

// SoakGoalInfo records a goal certified since the baseline release tag.
type SoakGoalInfo struct {
	Slug        string  `json:"slug"`
	CertifiedAt string  `json:"certified_at"`
	CommitSHA   string  `json:"commit_sha,omitempty"`
	HoursSoaked float64 `json:"hours_soaked"`
}

// SoakToolFailure summarizes non-ok invocations for a single tool during the soak window.
type SoakToolFailure struct {
	ToolName     string `json:"tool_name"`
	FailureCount int    `json:"failure_count"`
	LastOutcome  string `json:"last_outcome"`
	LastSeenAt   string `json:"last_seen_at"`
}

// SoakOptions configures the 3-day local soak evaluation.
type SoakOptions struct {
	RepoDir       string
	BaseTag       string
	BaseTime      time.Time
	DBPath        string
	Now           time.Time
	RequiredHours float64
	RequiredDays  int
}

// SoakReport captures the 3-day local dogfood soak and daily telemetry verification metrics.
type SoakReport struct {
	State              SoakState                      `json:"state"`
	BaseTag            string                         `json:"base_tag"`
	SoakStartAt        string                         `json:"soak_start_at,omitempty"`
	SoakTrigger        string                         `json:"soak_trigger,omitempty"`
	ElapsedHours       float64                        `json:"elapsed_hours"`
	RequiredHours      float64                        `json:"required_hours"`
	RemainingHours     float64                        `json:"remaining_hours"`
	CurrentDay         int                            `json:"current_day"`
	ActiveDays         int                            `json:"active_days"`
	RequiredActiveDays int                            `json:"required_active_days"`
	ActiveDates        []string                       `json:"active_dates,omitempty"`
	CertifiedGoals     []SoakGoalInfo                 `json:"certified_goals,omitempty"`
	ToolEventsTotal    int                            `json:"tool_events_total"`
	ToolErrorCount     int                            `json:"tool_error_count"`
	PrimeEventsTotal   int                            `json:"prime_events_total"`
	HarnessCount       int                            `json:"harness_count"`
	Harnesses          []string                       `json:"harnesses,omitempty"`
	RequiredUsage      int                            `json:"required_usage"`
	RequiredHarnesses  int                            `json:"required_harnesses"`
	PrimeErrorCount    int                            `json:"prime_error_count"`
	DiagnosticClusters int                            `json:"diagnostic_clusters"`
	FailedTools        []SoakToolFailure              `json:"failed_tools,omitempty"`
	PrimeErrors        []string                       `json:"prime_errors,omitempty"`
	ClusterSummaries   []string                       `json:"cluster_summaries,omitempty"`
	FeatureLedger      []store.FeatureSoakRecord      `json:"feature_ledger,omitempty"`
	ReadyFeatures      int                            `json:"ready_features"`
	SoakingFeatures    int                            `json:"soaking_features"`
	DebugFeatures      int                            `json:"debug_features"`
	ReadyThroughCommit string                         `json:"ready_through_commit,omitempty"`
	DailyReports       []store.FeatureSoakDailyReport `json:"daily_reports,omitempty"`
	Summary            string                         `json:"summary"`
	DailyChecklist     []string                       `json:"daily_checklist,omitempty"`
}

// EvaluateSoak inspects post-tag goal certifications, feature commits, and local
// sila.db telemetry (tool_events, prime_events, diagnostic_clusters) to verify
// independent 3-day (72-hour) per-feature local dogfooding soak timers.
func EvaluateSoak(opts SoakOptions) SoakReport {
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = ResolveGitRepoDir("")
	}
	repoDir = soakReleaseScope(repoDir)
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	reqHours := opts.RequiredHours
	if reqHours <= 0 {
		reqHours = SoakRequiredHours
	}
	reqDays := opts.RequiredDays
	if reqDays <= 0 {
		reqDays = SoakRequiredActiveDays
	}

	baseTag := opts.BaseTag
	if baseTag == "" {
		baseTag = latestPublishedTag(repoDir)
	}
	tagTime := tagCommitTime(repoDir, baseTag)
	if tagTime.IsZero() && !opts.BaseTime.IsZero() {
		tagTime = opts.BaseTime
	}
	baseRef := ""
	if refExists(repoDir, baseTag) {
		baseRef = baseTag
	}

	certGoals, _, _ := scanPostTagCertifications(repoDir, tagTime, now, reqHours)

	report := SoakReport{
		State:              SoakUpToDate,
		BaseTag:            baseTag,
		RequiredHours:      reqHours,
		RequiredActiveDays: reqDays,
		RequiredUsage:      SoakMinUsage,
		RequiredHarnesses:  SoakMinHarnesses,
		CertifiedGoals:     certGoals,
	}

	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = store.DefaultDBPath()
	}
	drainSpoolBeforeSoak(repoDir, dbPath)
	syncFeatureSoakLedger(&report, repoDir, dbPath, baseTag, baseRef, tagTime, now, reqHours, reqDays)

	if len(report.FeatureLedger) == 0 {
		report.Summary = fmt.Sprintf("No unreleased feature commits or goal certifications since %s.", nonEmptyTag(baseTag))
		return report
	}

	var earliestStart time.Time
	var oldestReady *store.FeatureSoakRecord
	oldestFeature := report.FeatureLedger[len(report.FeatureLedger)-1]
	contiguousReady := true

	for i := len(report.FeatureLedger) - 1; i >= 0; i-- {
		rec := report.FeatureLedger[i]
		if t, err := time.Parse(time.RFC3339, rec.StartedAt); err == nil {
			t = t.UTC()
			if earliestStart.IsZero() || t.Before(earliestStart) {
				earliestStart = t
			}
		}
		switch rec.Status {
		case "ready":
			report.ReadyFeatures++
			if oldestReady == nil {
				rCopy := rec
				oldestReady = &rCopy
			}
			if contiguousReady && rec.CommitSHA != "" {
				report.ReadyThroughCommit = rec.CommitSHA
			}
		case "needs_debug":
			report.DebugFeatures++
			contiguousReady = false
		default:
			report.SoakingFeatures++
			contiguousReady = false
		}
	}

	anchor := oldestFeature
	if oldestReady != nil {
		anchor = *oldestReady
	}
	soakStart, err := time.Parse(time.RFC3339, anchor.StartedAt)
	if err != nil || soakStart.IsZero() {
		soakStart = earliestStart
	}
	soakStart = soakStart.UTC()
	if earliestStart.IsZero() {
		earliestStart = soakStart
	}

	elapsed := anchor.ElapsedHours
	if elapsed <= 0 && !soakStart.IsZero() {
		elapsed = now.Sub(soakStart).Hours()
	}
	if elapsed < 0 {
		elapsed = 0
	}
	rem := reqHours - elapsed
	if rem < 0 {
		rem = 0
	}
	curDay := int(elapsed/24.0) + 1
	if curDay < 1 {
		curDay = 1
	}
	if curDay > reqDays {
		curDay = reqDays
	}

	report.SoakStartAt = soakStart.UTC().Format(time.RFC3339)
	report.SoakTrigger = formatFeatureTrigger(anchor)
	report.ElapsedHours = elapsed
	report.RemainingHours = rem
	report.CurrentDay = curDay

	projectIDs := populateSoakTelemetry(&report, repoDir, dbPath, earliestStart)
	report.FeatureLedger = filterFeatureLedger(report.FeatureLedger, projectIDs, tagTime)

	hasErrors := report.DebugFeatures > 0 || report.ToolErrorCount > 0 || report.PrimeErrorCount > 0 || report.DiagnosticClusters > 0
	durationMet := (report.ReadyFeatures > 0 && elapsed >= reqHours) || report.ElapsedHours >= reqHours
	daysMet := report.ReadyFeatures > 0 || report.ActiveDays >= reqDays
	usageMet := report.ToolEventsTotal+report.PrimeEventsTotal >= SoakMinUsage
	harnessMet := report.HarnessCount >= SoakMinHarnesses

	switch {
	case hasErrors:
		report.State = SoakNeedsDebug
		report.Summary = fmt.Sprintf(
			"Soak Day %d/%d (%.1fh/%.0fh): %d tool error(s), %d prime failure(s), %d diagnostic cluster(s) detected since %s — debug & fix required before release.",
			report.CurrentDay, reqDays, report.ElapsedHours, reqHours,
			report.ToolErrorCount, report.PrimeErrorCount, report.DiagnosticClusters,
			soakStart.Format("2006-01-02 15:04 UTC"),
		)
		report.DailyChecklist = buildDebugChecklist(report)
	case report.ReadyFeatures == 0 || !durationMet || !daysMet || !usageMet || !harnessMet:
		report.State = SoakInProgress
		report.Summary = fmt.Sprintf(
			"Soak Day %d/%d in progress: %.1fh/%.0fh elapsed (%.1fh remaining), %d/%d active dogfood day(s), %d/%d harness(es), %d events (floor %d), 0 errors.",
			report.CurrentDay, reqDays, report.ElapsedHours, reqHours, report.RemainingHours,
			report.ActiveDays, reqDays, report.HarnessCount, SoakMinHarnesses,
			report.ToolEventsTotal+report.PrimeEventsTotal, SoakMinUsage,
		)
		report.DailyChecklist = buildSoakingChecklist(report)
	default:
		report.State = SoakReady
		if report.SoakingFeatures > 0 {
			report.Summary = fmt.Sprintf(
				"3-day local soak verified: %d ready feature(s) (through %s), %d soaking on own timer (%.1fh/%.0fh elapsed across %d/%d active dogfood days, %d harness(es), %d events, 0 errors).",
				report.ReadyFeatures, nonEmptyCommit(report.ReadyThroughCommit), report.SoakingFeatures,
				report.ElapsedHours, reqHours, report.ActiveDays, reqDays,
				report.HarnessCount, report.ToolEventsTotal+report.PrimeEventsTotal,
			)
		} else {
			report.Summary = fmt.Sprintf(
				"3-day local soak verified: %.1fh/%.0fh elapsed across %d/%d active dogfood days, %d harness(es), %d events, 0 errors.",
				report.ElapsedHours, reqHours, report.ActiveDays, reqDays,
				report.HarnessCount, report.ToolEventsTotal+report.PrimeEventsTotal,
			)
		}
		report.DailyChecklist = []string{
			"3-day local dogfood soak complete with zero unresolved telemetry errors",
			"Ready to run 'sila release preflight --strict' and cut annotated release tag",
		}
	}

	return report
}

func formatFeatureTrigger(rec store.FeatureSoakRecord) string {
	if rec.SourceKind == "commit" || rec.CommitSHA != "" {
		return fmt.Sprintf("commit (%s)", rec.Title)
	}
	return fmt.Sprintf("goal certification (%s)", rec.FeatureID)
}

func nonEmptyCommit(sha string) string {
	if sha == "" {
		return "HEAD"
	}
	return sha
}

func harnessIdentity(agent, source string) string {
	name := strings.TrimSpace(agent)
	if name == "" {
		name = strings.TrimSpace(source)
	}
	switch strings.ToLower(name) {
	case "", "cli", "mcp", "system", "unknown", "anonymous":
		return ""
	}
	return name
}

func latestPublishedTag(repoDir string) string {
	candidates := []string{
		filepath.Join(filepath.Dir(repoDir), "public-export"),
		filepath.Join(repoDir, "public-export"),
	}
	for _, dir := range candidates {
		if tag := latestTagIn(dir); tag != "" {
			return tag
		}
	}
	return latestLocalTag(repoDir)
}

func latestTagIn(repoDir string) string {
	cmd := exec.Command("git", "-C", repoDir, "tag", "-l", "v*", "--sort=-v:refname")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func nonEmptyTag(tag string) string {
	if tag == "" {
		return "initial commit"
	}
	return tag
}

func latestLocalTag(repoDir string) string {
	cmd := exec.Command("git", "tag", "-l", "v*", "--sort=-v:refname")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 && lines[0] != "" {
		return strings.TrimSpace(lines[0])
	}
	return ""
}

func tagCommitTime(repoDir, tag string) time.Time {
	if tag == "" || tag == "v0.0.0" {
		return time.Time{}
	}
	dirs := []string{
		repoDir,
		filepath.Join(filepath.Dir(repoDir), "public-export"),
		filepath.Join(repoDir, "public-export"),
	}
	for _, dir := range dirs {
		cmd := exec.Command("git", "-C", dir, "log", "-1", "--format=%cI", tag)
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out))); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

type rawCertRecord struct {
	GoalSlug    string `json:"goal_slug"`
	CertifiedAt string `json:"certified_at"`
	CommitSHA   string `json:"commit_sha"`
	Verified    bool   `json:"verified"`
}

func scanPostTagCertifications(repoDir string, tagTime, now time.Time, reqHours float64) ([]SoakGoalInfo, time.Time, string) {
	goalsDir := filepath.Join(repoDir, "goals")
	entries, err := os.ReadDir(goalsDir)
	if err != nil {
		return nil, time.Time{}, ""
	}

	var results []SoakGoalInfo
	var newestTime time.Time
	var newestSlug string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		certPath := filepath.Join(goalsDir, entry.Name(), "certification.json")
		data, err := os.ReadFile(certPath)
		if err != nil {
			continue
		}
		var rec rawCertRecord
		if err := json.Unmarshal(data, &rec); err != nil || !rec.Verified || rec.CertifiedAt == "" {
			continue
		}
		certTime, err := time.Parse(time.RFC3339Nano, rec.CertifiedAt)
		if err != nil {
			certTime, err = time.Parse(time.RFC3339, rec.CertifiedAt)
			if err != nil {
				continue
			}
		}
		certTime = certTime.UTC()
		hours := now.Sub(certTime).Hours()
		if hours < 0 {
			hours = 0
		}
		inRecentWindow := reqHours > 0 && hours < reqHours
		afterTag := !tagTime.IsZero() && certTime.After(tagTime)
		if !tagTime.IsZero() && !afterTag && !inRecentWindow {
			continue
		}
		slug := rec.GoalSlug
		if slug == "" {
			slug = entry.Name()
		}
		results = append(results, SoakGoalInfo{
			Slug:        slug,
			CertifiedAt: certTime.Format(time.RFC3339),
			CommitSHA:   rec.CommitSHA,
			HoursSoaked: hours,
		})
		if newestTime.IsZero() || certTime.After(newestTime) {
			newestTime = certTime
			newestSlug = slug
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].CertifiedAt > results[j].CertifiedAt
	})
	return results, newestTime, newestSlug
}

// drainSpoolBeforeSoak persists queued lifecycle events so a delayed hook
// spool cannot hide harnesses or active days from the soak evaluation.
func drainSpoolBeforeSoak(repoDir, dbPath string) {
	if dbPath == "" {
		dbPath = store.DefaultDBPath()
	}
	spooler, err := loaders.NewSpooler(loaders.DefaultSpoolDir())
	if err != nil {
		return
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return
	}
	defer st.Close()
	proj := store.ProjectID(repoDir)
	if proj == "" {
		return
	}
	_, _ = spooler.Drain(st, &proj)
}

func populateSoakTelemetry(report *SoakReport, repoDir, dbPath string, soakStart time.Time) []string {
	st, exists, err := store.OpenInspection(dbPath)
	if err != nil || !exists || st == nil {
		return nil
	}
	defer st.Close()
	projectIDs := soakProjectIDs(st, repoDir)

	activeDatesSet := make(map[string]struct{})
	failedToolsMap := make(map[string]*SoakToolFailure)
	harnessSet := make(map[string]struct{})

	toolEvents := soakToolEvents(st, projectIDs, soakStart)
	if err == nil {
		report.ToolEventsTotal = len(toolEvents)
		for _, ev := range toolEvents {
			if !ev.CreatedAt.IsZero() {
				activeDatesSet[ev.CreatedAt.UTC().Format("2006-01-02")] = struct{}{}
			}
			if harness := harnessIdentity(ev.Agent, ev.Source); harness != "" {
				harnessSet[harness] = struct{}{}
			}
			outcome := strings.ToLower(strings.TrimSpace(ev.Outcome))
			if outcome != "" && outcome != "ok" {
				report.ToolErrorCount++
				tf, ok := failedToolsMap[ev.ToolName]
				if !ok {
					tf = &SoakToolFailure{
						ToolName:    ev.ToolName,
						LastOutcome: ev.Outcome,
						LastSeenAt:  ev.CreatedAt.UTC().Format(time.RFC3339),
					}
					failedToolsMap[ev.ToolName] = tf
				}
				tf.FailureCount++
			}
		}
	}

	primeEvents := soakPrimeEvents(st, projectIDs, soakStart)
	if err == nil {
		report.PrimeEventsTotal = len(primeEvents)
		for _, pev := range primeEvents {
			if !pev.CreatedAt.IsZero() {
				activeDatesSet[pev.CreatedAt.UTC().Format("2006-01-02")] = struct{}{}
			}
			if h := strings.TrimSpace(pev.Harness); h != "" {
				harnessSet[h] = struct{}{}
			}
			status := strings.ToLower(strings.TrimSpace(pev.Status))
			if status == "failed" || status == "error" {
				report.PrimeErrorCount++
				if len(report.PrimeErrors) < 5 {
					errDesc := pev.ErrorClass
					if pev.Reason != "" {
						errDesc += ": " + pev.Reason
					}
					if errDesc == "" {
						errDesc = "prime failure"
					}
					report.PrimeErrors = append(report.PrimeErrors, fmt.Sprintf("%s (%s)", pev.Harness, errDesc))
				}
			}
		}
	}

	sinceISO := soakStart.UTC().Format(time.RFC3339)
	clusterQuery := `
		SELECT dc.category, dc.template, dc.target_file, dc.occurrence_count, dc.last_seen
		FROM diagnostic_clusters dc
		LEFT JOIN silence_overrides so
			ON dc.project_id = so.project_id AND dc.signature = so.signature
		WHERE dc.last_seen >= ? AND so.signature IS NULL`
	clusterArgs := []any{sinceISO}
	if len(projectIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(projectIDs)), ",")
		clusterQuery += " AND dc.project_id IN (" + placeholders + ")"
		for _, id := range projectIDs {
			clusterArgs = append(clusterArgs, id)
		}
	}
	clusterQuery += " ORDER BY dc.occurrence_count DESC, dc.last_seen DESC LIMIT 25"
	rows, qErr := st.DB.Query(clusterQuery, clusterArgs...)
	if qErr == nil {
		defer rows.Close()
		for rows.Next() {
			var category, tmpl, targetFile, lastSeen string
			var count int
			if scanErr := rows.Scan(&category, &tmpl, &targetFile, &count, &lastSeen); scanErr == nil {
				if targetFile != "" && repoDir != "" {
					if _, statErr := os.Stat(filepath.Join(repoDir, targetFile)); statErr != nil {
						continue
					}
				}
				report.DiagnosticClusters++
				if len(report.ClusterSummaries) < 5 {
					report.ClusterSummaries = append(report.ClusterSummaries,
						fmt.Sprintf("%s [%s] ×%d (last: %s)", category, tmpl, count, lastSeen))
				}
			}
		}
	}

	for d := range activeDatesSet {
		report.ActiveDates = append(report.ActiveDates, d)
	}
	sort.Strings(report.ActiveDates)
	report.ActiveDays = len(report.ActiveDates)
	report.HarnessCount = len(harnessSet)
	for harness := range harnessSet {
		report.Harnesses = append(report.Harnesses, harness)
	}
	sort.Strings(report.Harnesses)

	for _, tf := range failedToolsMap {
		report.FailedTools = append(report.FailedTools, *tf)
	}
	sort.Slice(report.FailedTools, func(i, j int) bool {
		if report.FailedTools[i].FailureCount != report.FailedTools[j].FailureCount {
			return report.FailedTools[i].FailureCount > report.FailedTools[j].FailureCount
		}
		return report.FailedTools[i].ToolName < report.FailedTools[j].ToolName
	})
	return projectIDs
}

func soakProjectIDs(st *store.Store, repoDir string) []string {
	clean := filepath.Clean(soakReleaseScope(repoDir))
	projects, err := st.Projects()
	if err != nil {
		return nil
	}
	var ids []string
	parentID := ""
	parentLen := -1
	for _, project := range projects {
		path := filepath.Clean(project.Path)
		if path == clean {
			ids = append(ids, project.ID)
			continue
		}
		if strings.HasPrefix(clean, path+string(os.PathSeparator)) && len(path) > parentLen {
			parentID = project.ID
			parentLen = len(path)
		}
	}
	if parentID != "" {
		ids = append(ids, parentID)
	}
	sort.Strings(ids)
	return ids
}

func soakReleaseScope(dir string) string {
	if isSilaModule(dir) {
		return dir
	}
	if found := findSilaModule(dir, 2); found != "" {
		return found
	}
	return dir
}

func findSilaModule(dir string, depth int) string {
	if depth <= 0 {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor" {
			continue
		}
		child := filepath.Join(dir, entry.Name())
		if isSilaModule(child) {
			return child
		}
		if found := findSilaModule(child, depth-1); found != "" {
			return found
		}
	}
	return ""
}

func isSilaModule(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")) == "github.com/jadmadi/sila"
		}
	}
	return false
}

func soakToolEvents(st *store.Store, projectIDs []string, since time.Time) []store.ToolEvent {
	if len(projectIDs) == 0 {
		events, err := st.ListToolEvents(store.ToolEventFilter{Since: since, Limit: 10000})
		if err != nil {
			return nil
		}
		return events
	}
	var out []store.ToolEvent
	for _, id := range projectIDs {
		events, err := st.ListToolEvents(store.ToolEventFilter{ProjectID: id, Since: since, Limit: 10000})
		if err != nil {
			continue
		}
		out = append(out, events...)
	}
	return out
}

func soakPrimeEvents(st *store.Store, projectIDs []string, since time.Time) []store.PrimeEvent {
	if len(projectIDs) == 0 {
		events, err := st.ListPrimeEvents(store.PrimeEventFilter{Since: since, Limit: 5000})
		if err != nil {
			return nil
		}
		return events
	}
	var out []store.PrimeEvent
	for _, id := range projectIDs {
		events, err := st.ListPrimeEvents(store.PrimeEventFilter{ProjectID: id, Since: since, Limit: 5000})
		if err != nil {
			continue
		}
		out = append(out, events...)
	}
	return out
}

func filterFeatureLedger(ledger []store.FeatureSoakRecord, projectIDs []string, tagTime time.Time) []store.FeatureSoakRecord {
	allowed := make(map[string]bool, len(projectIDs))
	for _, id := range projectIDs {
		allowed[id] = true
	}
	filtered := make([]store.FeatureSoakRecord, 0, len(ledger))
	for _, feature := range ledger {
		if len(allowed) > 0 && feature.ProjectID != "" && !allowed[feature.ProjectID] {
			continue
		}
		if !tagTime.IsZero() && feature.StartedAt != "" {
			if started, err := time.Parse(time.RFC3339, feature.StartedAt); err == nil && !started.After(tagTime) {
				continue
			}
		}
		filtered = append(filtered, feature)
	}
	return filtered
}

func buildDebugChecklist(report SoakReport) []string {
	var steps []string
	for _, tf := range report.FailedTools {
		steps = append(steps, fmt.Sprintf("Debug & fix tool %q (%d failure(s), last outcome: %s at %s)",
			tf.ToolName, tf.FailureCount, tf.LastOutcome, tf.LastSeenAt))
	}
	for _, pe := range report.PrimeErrors {
		steps = append(steps, fmt.Sprintf("Debug & fix prime failure: %s", pe))
	}
	for _, cs := range report.ClusterSummaries {
		steps = append(steps, fmt.Sprintf("Resolve recurring diagnostic cluster: %s", cs))
	}
	steps = append(steps,
		"Record root cause and fix via 'sila lesson \"<symptom>\" --cause=... --fix=...'",
		"Rebuild binary ('./build.sh') and continue 3-day local dogfood verification",
	)
	return steps
}

func buildSoakingChecklist(report SoakReport) []string {
	return []string{
		fmt.Sprintf("Daily check (Day %d/%d): run 'sila release status' & 'sila standup last 24h'",
			report.CurrentDay, report.RequiredActiveDays),
		fmt.Sprintf("Continue normal local usage across %d more active day(s) (%.1fh remaining of %.0fh window)",
			maxInt(0, report.RequiredActiveDays-report.ActiveDays), report.RemainingHours, report.RequiredHours),
		"If any tool friction or failure surfaces, debug/fix immediately and record a 'sila lesson'",
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
