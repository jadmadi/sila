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

	"github.com/jadmadi/sila/internal/store"
)

// SoakRequiredHours is the minimum wall-clock soak duration (3 days = 72 hours)
// required between the latest feature/goal certification and cutting a release.
const SoakRequiredHours = 72.0

// SoakRequiredActiveDays is the minimum number of distinct local dogfooding
// calendar days with live telemetry required during the soak window.
const SoakRequiredActiveDays = 3

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
	PrimeErrorCount    int                            `json:"prime_error_count"`
	DiagnosticClusters int                            `json:"diagnostic_clusters"`
	FailedTools        []SoakToolFailure              `json:"failed_tools,omitempty"`
	PrimeErrors        []string                       `json:"prime_errors,omitempty"`
	ClusterSummaries   []string                       `json:"cluster_summaries,omitempty"`
	FeatureLedger      []store.FeatureSoakRecord      `json:"feature_ledger,omitempty"`
	DailyReports       []store.FeatureSoakDailyReport `json:"daily_reports,omitempty"`
	Summary            string                         `json:"summary"`
	DailyChecklist     []string                       `json:"daily_checklist,omitempty"`
}

// EvaluateSoak inspects post-tag goal certifications, feature commits, and local
// sila.db telemetry (tool_events, prime_events, diagnostic_clusters) to verify
// the 3-day (72-hour) local dogfooding soak period.
func EvaluateSoak(opts SoakOptions) SoakReport {
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = ResolveGitRepoDir("")
	}
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
		baseTag = latestLocalTag(repoDir)
	}
	tagTime := tagCommitTime(repoDir, baseTag)

	certGoals, newestGoalTime, newestGoalSlug := scanPostTagCertifications(repoDir, tagTime, now, reqHours)
	newestCommitTime, newestCommitSubj := latestFeatureCommitTime(repoDir, baseTag)

	var soakStart time.Time
	var soakTrigger string

	if !newestGoalTime.IsZero() && (newestCommitTime.IsZero() || !newestGoalTime.Before(newestCommitTime)) {
		soakStart = newestGoalTime
		soakTrigger = fmt.Sprintf("goal certification (%s)", newestGoalSlug)
	} else if !newestCommitTime.IsZero() {
		soakStart = newestCommitTime
		soakTrigger = fmt.Sprintf("commit (%s)", newestCommitSubj)
	}

	report := SoakReport{
		State:              SoakUpToDate,
		BaseTag:            baseTag,
		RequiredHours:      reqHours,
		RequiredActiveDays: reqDays,
		CertifiedGoals:     certGoals,
	}

	if soakStart.IsZero() {
		report.Summary = fmt.Sprintf("No unreleased feature commits or goal certifications since %s.", nonEmptyTag(baseTag))
		return report
	}

	elapsed := now.Sub(soakStart).Hours()
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
	report.SoakTrigger = soakTrigger
	report.ElapsedHours = elapsed
	report.RemainingHours = rem
	report.CurrentDay = curDay

	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = store.DefaultDBPath()
	}
	populateSoakTelemetry(&report, repoDir, dbPath, soakStart)
	syncFeatureSoakLedger(&report, repoDir, dbPath, baseTag, tagTime, now, reqHours, reqDays)

	hasErrors := report.ToolErrorCount > 0 || report.PrimeErrorCount > 0 || report.DiagnosticClusters > 0
	durationMet := report.ElapsedHours >= reqHours
	daysMet := report.ActiveDays >= reqDays

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
	case !durationMet || !daysMet:
		report.State = SoakInProgress
		report.Summary = fmt.Sprintf(
			"Soak Day %d/%d in progress: %.1fh/%.0fh elapsed (%.1fh remaining), %d/%d active dogfood day(s), %d tool + %d prime events, 0 errors.",
			report.CurrentDay, reqDays, report.ElapsedHours, reqHours, report.RemainingHours,
			report.ActiveDays, reqDays, report.ToolEventsTotal, report.PrimeEventsTotal,
		)
		report.DailyChecklist = buildSoakingChecklist(report)
	default:
		report.State = SoakReady
		report.Summary = fmt.Sprintf(
			"3-day local soak verified: %.1fh/%.0fh elapsed across %d/%d active dogfood days (%d tool + %d prime events, 0 errors).",
			report.ElapsedHours, reqHours, report.ActiveDays, reqDays,
			report.ToolEventsTotal, report.PrimeEventsTotal,
		)
		report.DailyChecklist = []string{
			"3-day local dogfood soak complete with zero unresolved telemetry errors",
			"Ready to run 'sila release preflight --strict' and cut annotated release tag",
		}
	}

	return report
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
	cmd := exec.Command("git", "log", "-1", "--format=%cI", tag)
	cmd.Dir = repoDir
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

func latestFeatureCommitTime(repoDir, baseTag string) (time.Time, string) {
	args := []string{"log", "--pretty=format:%cI%x1f%s"}
	if baseTag != "" && baseTag != "v0.0.0" {
		args = append(args, baseTag+"..HEAD")
	} else {
		args = append(args, "-n", "50")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		parts := strings.SplitN(strings.TrimSpace(line), "\x1f", 2)
		if len(parts) != 2 {
			continue
		}
		subj := strings.TrimSpace(parts[1])
		m := convCommitRE.FindStringSubmatch(subj)
		if len(m) == 0 {
			continue
		}
		cType := strings.ToLower(m[1])
		isBreaking := m[3] == "!"
		if isBreaking || cType == "feat" || cType == "fix" || cType == "perf" {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[0])); err == nil {
				return t.UTC(), subj
			}
		}
	}
	return time.Time{}, ""
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

func populateSoakTelemetry(report *SoakReport, repoDir, dbPath string, soakStart time.Time) {
	st, exists, err := store.OpenInspection(dbPath)
	if err != nil || !exists || st == nil {
		return
	}
	defer st.Close()

	activeDatesSet := make(map[string]struct{})
	failedToolsMap := make(map[string]*SoakToolFailure)

	toolEvents, err := st.ListToolEvents(store.ToolEventFilter{
		Since: soakStart,
		Limit: 10000,
	})
	if err == nil {
		report.ToolEventsTotal = len(toolEvents)
		for _, ev := range toolEvents {
			if !ev.CreatedAt.IsZero() {
				activeDatesSet[ev.CreatedAt.UTC().Format("2006-01-02")] = struct{}{}
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

	primeEvents, err := st.ListPrimeEvents(store.PrimeEventFilter{
		Since: soakStart,
		Limit: 5000,
	})
	if err == nil {
		report.PrimeEventsTotal = len(primeEvents)
		for _, pev := range primeEvents {
			if !pev.CreatedAt.IsZero() {
				activeDatesSet[pev.CreatedAt.UTC().Format("2006-01-02")] = struct{}{}
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
	rows, qErr := st.DB.Query(`
		SELECT dc.category, dc.template, dc.target_file, dc.occurrence_count, dc.last_seen
		FROM diagnostic_clusters dc
		LEFT JOIN silence_overrides so
			ON dc.project_id = so.project_id AND dc.signature = so.signature
		WHERE dc.last_seen >= ? AND so.signature IS NULL
		ORDER BY dc.occurrence_count DESC, dc.last_seen DESC
		LIMIT 25`, sinceISO)
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

	for _, tf := range failedToolsMap {
		report.FailedTools = append(report.FailedTools, *tf)
	}
	sort.Slice(report.FailedTools, func(i, j int) bool {
		if report.FailedTools[i].FailureCount != report.FailedTools[j].FailureCount {
			return report.FailedTools[i].FailureCount > report.FailedTools[j].FailureCount
		}
		return report.FailedTools[i].ToolName < report.FailedTools[j].ToolName
	})
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
