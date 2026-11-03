package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/version"
)

// ReleaseStatusOptions configures the release inspection engine.
type ReleaseStatusOptions struct {
	RepoDir    string `json:"repo_dir"`
	Offline    bool   `json:"offline"`
	ShowDiff   bool   `json:"show_diff"`
	AllowDirty bool   `json:"allow_dirty"`
	DBPath     string `json:"db_path,omitempty"`
}

// PublicReleaseInfo captures upstream or local release baseline metadata.
type PublicReleaseInfo struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url"`
	Source      string `json:"source"`
	TimeAgo     string `json:"time_ago"`
}

// DevStateInfo captures local workspace development coordinates.
type DevStateInfo struct {
	GitDescribe      string `json:"git_describe"`
	Branch           string `json:"branch"`
	Commit           string `json:"commit"`
	CommitDate       string `json:"commit_date"`
	InstalledVersion string `json:"installed_version"`
	IsDirty          bool   `json:"is_dirty"`
}

// ImpactSummaryInfo aggregates categorized conventional commit impact.
type ImpactSummaryInfo struct {
	MajorBreaking     int    `json:"major_breaking"`
	MinorFeatures     int    `json:"minor_features"`
	PatchFixes        int    `json:"patch_fixes"`
	InternalDocs      int    `json:"internal_docs"`
	Other             int    `json:"other"`
	TotalCommitsAhead int    `json:"total_commits_ahead"`
	HighestImpact     string `json:"highest_impact"`
}

// RoadmapStatusInfo captures Sila-exclusive epistemic goal completion metrics.
type RoadmapStatusInfo struct {
	TotalGoals        int     `json:"total_goals"`
	ImplementedGoals  int     `json:"implemented_goals"`
	SupersededGoals   int     `json:"superseded_goals"`
	PendingGoals      int     `json:"pending_goals"`
	ClaimedGoals      int     `json:"claimed_goals"`
	BlockedGoals      int     `json:"blocked_goals"`
	TotalTasks        int     `json:"total_tasks"`
	CompletedTasks    int     `json:"completed_tasks"`
	CompletionPct     float64 `json:"completion_pct"`
	IsRoadmapComplete bool    `json:"is_roadmap_complete"`
}

// MCPContractInfo captures tool count and breaking change status.
type MCPContractInfo struct {
	PublicToolsCount     int      `json:"public_tools_count"`
	BreakingChangesCount int      `json:"breaking_changes_count"`
	BreakingChanges      []string `json:"breaking_changes,omitempty"`
}

// WorkingTreeInfo captures local uncommitted status and diff stats.
type WorkingTreeInfo struct {
	IsDirty        bool           `json:"is_dirty"`
	Description    string         `json:"description"`
	ModifiedFiles  int            `json:"modified_files"`
	UntrackedFiles int            `json:"untracked_files"`
	LinesAdded     int            `json:"lines_added"`
	LinesDeleted   int            `json:"lines_deleted"`
	Subsystems     map[string]int `json:"subsystems,omitempty"`
	DiffStat       string         `json:"diffstat,omitempty"`
}

// SemverCandidatesInfo holds SemVer bump candidates and recommendation rationale.
type SemverCandidatesInfo struct {
	BaseTag            string `json:"base_tag"`
	PatchCandidate     string `json:"patch_candidate"`
	MinorCandidate     string `json:"minor_candidate"`
	MajorCandidate     string `json:"major_candidate"`
	MilestoneCandidate string `json:"milestone_candidate,omitempty"`
	RecommendedVersion string `json:"recommended_version"`
	Rationale          string `json:"rationale"`
}

// ReleaseVerdictInfo contains actionable release decision and next steps.
type ReleaseVerdictInfo struct {
	Code        string   `json:"code"`
	Verdict     string   `json:"verdict"`
	Summary     string   `json:"summary"`
	ActionSteps []string `json:"action_steps"`
}

// StatusCommit records an individual analyzed commit.
type StatusCommit struct {
	Hash     string `json:"hash"`
	Type     string `json:"type"`
	Scope    string `json:"scope,omitempty"`
	Breaking bool   `json:"breaking"`
	Subject  string `json:"subject"`
	GoalSlug string `json:"goal_slug,omitempty"`
}

// ReleaseStatusReport brings all dimensions together.
type ReleaseStatusReport struct {
	PublicRelease    PublicReleaseInfo    `json:"public_release"`
	DevState         DevStateInfo         `json:"dev_state"`
	ImpactSummary    ImpactSummaryInfo    `json:"impact_summary"`
	RoadmapStatus    RoadmapStatusInfo    `json:"roadmap_status"`
	MCPContract      MCPContractInfo      `json:"mcp_contract"`
	WorkingTree      WorkingTreeInfo      `json:"working_tree"`
	SoakStatus       SoakReport           `json:"soak_status"`
	SemverCandidates SemverCandidatesInfo `json:"semver_candidates"`
	Verdict          ReleaseVerdictInfo   `json:"verdict"`
	Commits          []StatusCommit       `json:"commits,omitempty"`
}

var (
	convCommitRE    = regexp.MustCompile(`^([a-zA-Z]+)(?:\(([a-zA-Z0-9_, -]+)\))?(!)?:\s*(.*)$`)
	breakingBodyRE  = regexp.MustCompile(`(?i)(?:BREAKING CHANGE|BREAKING-CHANGE):`)
	diffShortStatRE = regexp.MustCompile(`(\d+)\s+insertion|\d+\s+deletion`)
)

// GetReleaseStatus evaluates repository state, commits, roadmap goals, and contracts.
func GetReleaseStatus(opts ReleaseStatusOptions) (ReleaseStatusReport, error) {
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = ResolveGitRepoDir("")
	}

	pubRelease := fetchPublicRelease(repoDir, opts.Offline)
	devState := inspectDevState(repoDir)
	workingTree := inspectWorkingTree(repoDir, opts.ShowDiff)
	commits, impact := analyzeCommits(repoDir, pubRelease.Tag)
	roadmap := inspectRoadmap(repoDir)
	mcpContract := inspectMCPContract(repoDir)
	soak := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		BaseTag: pubRelease.Tag,
		DBPath:  opts.DBPath,
	})

	candidates, verdict := computeSemverCandidates(pubRelease.Tag, impact, roadmap, workingTree, opts.AllowDirty)
	verdict = applySoakVerdict(verdict, soak)

	return ReleaseStatusReport{
		PublicRelease:    pubRelease,
		DevState:         devState,
		ImpactSummary:    impact,
		RoadmapStatus:    roadmap,
		MCPContract:      mcpContract,
		WorkingTree:      workingTree,
		SoakStatus:       soak,
		SemverCandidates: candidates,
		Verdict:          verdict,
		Commits:          commits,
	}, nil
}

func fetchPublicRelease(repoDir string, offline bool) PublicReleaseInfo {
	if !offline {
		client := &http.Client{Timeout: 1500 * time.Millisecond}
		req, err := http.NewRequest("GET", "https://api.github.com/repos/jadmadi/sila/releases/latest", nil)
		if err == nil {
			req.Header.Set("User-Agent", "sila-release-status")
			resp, err := client.Do(req)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					var gh struct {
						TagName     string `json:"tag_name"`
						Name        string `json:"name"`
						PublishedAt string `json:"published_at"`
						HTMLURL     string `json:"html_url"`
					}
					if err := json.NewDecoder(resp.Body).Decode(&gh); err == nil && gh.TagName != "" {
						return PublicReleaseInfo{
							Tag:         gh.TagName,
							Name:        gh.Name,
							PublishedAt: gh.PublishedAt,
							URL:         gh.HTMLURL,
							Source:      "github_api",
							TimeAgo:     calcTimeAgo(gh.PublishedAt),
						}
					}
				}
			}
		}
	}

	// Local Git Tag fallback
	cmd := exec.Command("git", "tag", "-l", "v*", "--sort=-v:refname")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	topTag := ""
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 && lines[0] != "" {
			topTag = lines[0]
		}
	}
	if topTag == "" {
		topTag = "v0.0.0"
		return PublicReleaseInfo{
			Tag:     topTag,
			Name:    "none",
			Source:  "local_git_tag",
			TimeAgo: "unknown",
		}
	}

	dateCmd := exec.Command("git", "log", "-1", "--format=%aI", topTag)
	dateCmd.Dir = repoDir
	dateOut, _ := dateCmd.Output()
	dateStr := strings.TrimSpace(string(dateOut))

	return PublicReleaseInfo{
		Tag:         topTag,
		Name:        topTag,
		PublishedAt: dateStr,
		URL:         fmt.Sprintf("https://github.com/jadmadi/sila/releases/tag/%s", topTag),
		Source:      "local_git_tag",
		TimeAgo:     calcTimeAgo(dateStr),
	}
}

func calcTimeAgo(iso string) string {
	if iso == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	hours := time.Since(t).Hours()
	days := int(hours / 24)
	switch {
	case days == 0:
		if hours < 1 {
			return "just now"
		}
		return fmt.Sprintf("%d hours ago", int(hours))
	case days == 1:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
}

func inspectDevState(repoDir string) DevStateInfo {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = repoDir
	descOut, _ := cmd.Output()

	bCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	bCmd.Dir = repoDir
	bOut, _ := bCmd.Output()

	cCmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cCmd.Dir = repoDir
	cOut, _ := cCmd.Output()

	dCmd := exec.Command("git", "log", "-1", "--format=%aI", "HEAD")
	dCmd.Dir = repoDir
	dOut, _ := dCmd.Output()

	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = repoDir
	statusOut, _ := statusCmd.Output()
	isDirty := len(strings.TrimSpace(string(statusOut))) > 0

	return DevStateInfo{
		GitDescribe:      strings.TrimSpace(string(descOut)),
		Branch:           strings.TrimSpace(string(bOut)),
		Commit:           strings.TrimSpace(string(cOut)),
		CommitDate:       strings.TrimSpace(string(dOut)),
		InstalledVersion: version.String(),
		IsDirty:          isDirty,
	}
}

func inspectWorkingTree(repoDir string, showDiff bool) WorkingTreeInfo {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return WorkingTreeInfo{Description: "Could not inspect git status"}
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return WorkingTreeInfo{
			IsDirty:     false,
			Description: "Clean working tree",
		}
	}

	untracked := 0
	modified := 0
	subsystems := make(map[string]int)

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "??") {
			untracked++
		} else {
			modified++
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			path := parts[len(parts)-1]
			dir := filepath.Dir(path)
			if dir == "." {
				dir = "root"
			} else {
				top := strings.Split(filepath.ToSlash(dir), "/")[0]
				dir = top
			}
			subsystems[dir]++
		}
	}

	var added, deleted int
	shortStatCmd := exec.Command("git", "diff", "--shortstat", "HEAD")
	shortStatCmd.Dir = repoDir
	if statOut, err := shortStatCmd.Output(); err == nil {
		statStr := string(statOut)
		reIns := regexp.MustCompile(`(\d+)\s+insertion`)
		if m := reIns.FindStringSubmatch(statStr); len(m) > 1 {
			added, _ = strconv.Atoi(m[1])
		}
		reDel := regexp.MustCompile(`(\d+)\s+deletion`)
		if m := reDel.FindStringSubmatch(statStr); len(m) > 1 {
			deleted, _ = strconv.Atoi(m[1])
		}
	}

	diffStat := ""
	if showDiff {
		diffCmd := exec.Command("git", "diff", "--stat", "HEAD")
		diffCmd.Dir = repoDir
		if dOut, err := diffCmd.Output(); err == nil {
			diffStat = string(dOut)
		}
	}

	total := modified + untracked
	desc := fmt.Sprintf("%d uncommitted files (+%d/-%d lines)", total, added, deleted)

	return WorkingTreeInfo{
		IsDirty:        true,
		Description:    desc,
		ModifiedFiles:  modified,
		UntrackedFiles: untracked,
		LinesAdded:     added,
		LinesDeleted:   deleted,
		Subsystems:     subsystems,
		DiffStat:       diffStat,
	}
}

func analyzeCommits(repoDir, baseTag string) ([]StatusCommit, ImpactSummaryInfo) {
	tagRange := ""
	if baseTag != "" && baseTag != "v0.0.0" {
		tagRange = baseTag + "..HEAD"
	}

	args := []string{"log", "--pretty=format:%h%x1f%s%x1f%b%x1e"}
	if tagRange != "" {
		args = append(args, tagRange)
	} else {
		args = append(args, "-n", "100")
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, ImpactSummaryInfo{HighestImpact: "NONE"}
	}

	entries := bytes.Split(out, []byte("\x1e"))
	var commits []StatusCommit
	var major, minor, patch, internal, other int

	for _, entry := range entries {
		s := strings.TrimSpace(string(entry))
		if s == "" {
			continue
		}
		parts := strings.SplitN(s, "\x1f", 3)
		if len(parts) < 2 {
			continue
		}
		hash := parts[0]
		subject := strings.TrimSpace(parts[1])
		body := ""
		if len(parts) > 2 {
			body = strings.TrimSpace(parts[2])
		}

		cType := ""
		cScope := ""
		cBreaking := false

		if m := convCommitRE.FindStringSubmatch(subject); len(m) > 0 {
			cType = strings.ToLower(m[1])
			cScope = m[2]
			if m[3] == "!" {
				cBreaking = true
			}
		}

		if breakingBodyRE.MatchString(body) {
			cBreaking = true
		}

		goalSlug := ""
		if m := goalPathRe.FindStringSubmatch(subject + " " + body); len(m) > 1 {
			goalSlug = m[1]
		}

		if cBreaking {
			major++
		} else {
			switch cType {
			case "feat":
				minor++
			case "fix", "perf", "revert":
				patch++
			case "docs", "chore", "refactor", "test", "ci", "build", "style":
				internal++
			default:
				other++
			}
		}

		commits = append(commits, StatusCommit{
			Hash:     hash,
			Type:     cType,
			Scope:    cScope,
			Breaking: cBreaking,
			Subject:  subject,
			GoalSlug: goalSlug,
		})
	}

	highest := "NONE"
	switch {
	case major > 0:
		highest = "MAJOR"
	case minor > 0:
		highest = "MINOR"
	case patch > 0:
		highest = "PATCH"
	case internal > 0:
		highest = "INTERNAL"
	case other > 0:
		highest = "OTHER"
	}

	return commits, ImpactSummaryInfo{
		MajorBreaking:     major,
		MinorFeatures:     minor,
		PatchFixes:        patch,
		InternalDocs:      internal,
		Other:             other,
		TotalCommitsAhead: len(commits),
		HighestImpact:     highest,
	}
}

func inspectRoadmap(repoDir string) RoadmapStatusInfo {
	audit, err := goals.ScanGoals(repoDir)
	if err != nil {
		return RoadmapStatusInfo{}
	}

	isComplete := false
	if audit.Total > 0 && audit.ClaimedCount == 0 && audit.PendingCount == 0 {
		if audit.ImplementedCount+audit.SupersededCount == audit.Total {
			isComplete = true
		}
	}

	pct := 0.0
	if audit.Total > 0 {
		pct = (float64(audit.ImplementedCount) / float64(audit.Total)) * 100.0
	}

	return RoadmapStatusInfo{
		TotalGoals:        audit.Total,
		ImplementedGoals:  audit.ImplementedCount,
		SupersededGoals:   audit.SupersededCount,
		PendingGoals:      audit.PendingCount,
		ClaimedGoals:      audit.ClaimedCount,
		BlockedGoals:      audit.BlockedCount,
		TotalTasks:        audit.TotalTasks,
		CompletedTasks:    audit.CompletedTasks,
		CompletionPct:     pct,
		IsRoadmapComplete: isComplete,
	}
}

func inspectMCPContract(repoDir string) MCPContractInfo {
	currentTools := Contract()
	toolsCount := len(currentTools)

	contractPath := filepath.Join(repoDir, "contracts", "mcp-v1.json")
	baseline, err := ReadContract(contractPath)
	if err != nil {
		return MCPContractInfo{PublicToolsCount: toolsCount}
	}

	changes := BreakingChanges(baseline, currentTools)
	return MCPContractInfo{
		PublicToolsCount:     toolsCount,
		BreakingChangesCount: len(changes),
		BreakingChanges:      changes,
	}
}

func computeSemverCandidates(baseTag string, impact ImpactSummaryInfo, roadmap RoadmapStatusInfo, wt WorkingTreeInfo, allowDirty bool) (SemverCandidatesInfo, ReleaseVerdictInfo) {
	clean := strings.TrimPrefix(baseTag, "v")
	parts := strings.Split(clean, ".")
	var maj, min, pat int
	if len(parts) >= 1 {
		maj, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		min, _ = strconv.Atoi(parts[1])
	}
	if len(parts) >= 3 {
		patPart := strings.Split(parts[2], "-")[0]
		pat, _ = strconv.Atoi(patPart)
	}

	patchCand := fmt.Sprintf("v%d.%d.%d", maj, min, pat+1)
	minorCand := fmt.Sprintf("v%d.%d.0", maj, min+1)
	majorCand := fmt.Sprintf("v%d.0.0", maj+1)

	milestoneCand := ""
	recommended := fmt.Sprintf("v%s", clean)
	rationale := ""

	if roadmap.IsRoadmapComplete {
		milestoneCand = "v1.0.0"
		recommended = "v1.0.0"
		rationale = fmt.Sprintf("100%% of epistemic roadmap goals completed (%d/%d verified) + %d feature commits since %s. Recommended for Watershed v1.0.0 Milestone (or conservative %s if staying pre-1.0).",
			roadmap.ImplementedGoals, roadmap.TotalGoals, impact.MinorFeatures, baseTag, minorCand)
	} else if impact.MajorBreaking > 0 {
		if maj == 0 {
			recommended = minorCand
			rationale = fmt.Sprintf("%d breaking change commit(s) on 0.x line triggers minor bump %s", impact.MajorBreaking, minorCand)
		} else {
			recommended = majorCand
			rationale = fmt.Sprintf("%d breaking change commit(s) triggers major release %s", impact.MajorBreaking, majorCand)
		}
	} else if impact.MinorFeatures > 0 {
		recommended = minorCand
		rationale = fmt.Sprintf("%d feature commit(s) since %s triggers minor bump %s", impact.MinorFeatures, baseTag, minorCand)
	} else if impact.PatchFixes > 0 {
		recommended = patchCand
		rationale = fmt.Sprintf("%d bug/perf fix commit(s) since %s triggers patch bump %s", impact.PatchFixes, baseTag, patchCand)
	} else if impact.InternalDocs > 0 {
		rationale = fmt.Sprintf("%d commit(s) ahead of %s are internal/documentation; no public release triggered", impact.InternalDocs, baseTag)
	} else {
		rationale = fmt.Sprintf("Branch HEAD matches latest public release %s. Zero commits ahead.", baseTag)
	}

	candidates := SemverCandidatesInfo{
		BaseTag:            baseTag,
		PatchCandidate:     patchCand,
		MinorCandidate:     minorCand,
		MajorCandidate:     majorCand,
		MilestoneCandidate: milestoneCand,
		RecommendedVersion: recommended,
		Rationale:          rationale,
	}

	var verdict ReleaseVerdictInfo

	if wt.IsDirty && !allowDirty {
		verdict = ReleaseVerdictInfo{
			Code:    "PENDING_COMMITS",
			Verdict: "PENDING COMMITS (WORKING TREE DIRTY)",
			Summary: fmt.Sprintf("Working tree contains %d uncommitted modification(s) (+%d/-%d lines). Clean worktree required for release.", wt.ModifiedFiles+wt.UntrackedFiles, wt.LinesAdded, wt.LinesDeleted),
			ActionSteps: []string{
				"Commit or stash modified files before releasing",
				"Re-run 'sila release status' to verify clean working tree",
			},
		}
	} else if impact.TotalCommitsAhead == 0 {
		verdict = ReleaseVerdictInfo{
			Code:    "UP_TO_DATE",
			Verdict: "UP TO DATE (NO COMMITS AHEAD)",
			Summary: fmt.Sprintf("Current HEAD matches latest release %s. No release action required.", baseTag),
			ActionSteps: []string{
				"Continue active development on roadmap goals",
			},
		}
	} else if roadmap.IsRoadmapComplete || impact.MinorFeatures > 0 || impact.MajorBreaking > 0 || impact.PatchFixes > 0 {
		vLabel := "READY TO RELEASE"
		if roadmap.IsRoadmapComplete {
			vLabel = "READY TO RELEASE (WATERSHED MILESTONE)"
		}
		verdict = ReleaseVerdictInfo{
			Code:    "READY_TO_RELEASE",
			Verdict: vLabel,
			Summary: fmt.Sprintf("Release-triggering changes detected since %s. Recommended release candidate: %s", baseTag, recommended),
			ActionSteps: []string{
				"Run preflight quality scorecard: 'sila release preflight'",
				"Verify sentry and architecture gates: 'sila gate --pre-commit'",
				"Review changelog section: 'sila changelog --write'",
				fmt.Sprintf("Cut annotated release tag: git tag -a %s -m \"release: %s\"", recommended, recommended),
				"Push release tag to origin remote",
			},
		}
	} else {
		verdict = ReleaseVerdictInfo{
			Code:    "INTERNAL_COMMITS_ONLY",
			Verdict: "NO PUBLIC RELEASE NEEDED (INTERNAL COMMITS ONLY)",
			Summary: fmt.Sprintf("%d commit(s) ahead of %s, but all are internal documentation or maintenance changes.", impact.TotalCommitsAhead, baseTag),
			ActionSteps: []string{
				"Internal commits remain on branch; release-please will not open a release PR",
			},
		}
	}

	return candidates, verdict
}

func applySoakVerdict(verdict ReleaseVerdictInfo, soak SoakReport) ReleaseVerdictInfo {
	if verdict.Code != "READY_TO_RELEASE" {
		return verdict
	}
	switch soak.State {
	case SoakNeedsDebug:
		return ReleaseVerdictInfo{
			Code:        "SOAK_NEEDS_DEBUG",
			Verdict:     "SOAK REGRESSION DETECTED (DEBUG & FIX REQUIRED)",
			Summary:     soak.Summary,
			ActionSteps: soak.DailyChecklist,
		}
	case SoakInProgress:
		return ReleaseVerdictInfo{
			Code:        "SOAK_IN_PROGRESS",
			Verdict:     fmt.Sprintf("LOCAL SOAK IN PROGRESS (DAY %d OF %d)", soak.CurrentDay, soak.RequiredActiveDays),
			Summary:     soak.Summary,
			ActionSteps: soak.DailyChecklist,
		}
	default:
		return verdict
	}
}
