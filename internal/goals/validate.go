package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// DiagnosticCode enumerates standard goal package validation issue codes.
const (
	DiagMissingFile         = "MISSING_FILE"
	DiagUnreadableFile      = "UNREADABLE_FILE"
	DiagParseError          = "PARSE_ERROR"
	DiagAmbiguousTier       = "AMBIGUOUS_TIER"
	DiagInvalidTier         = "INVALID_TIER"
	DiagInvalidShape        = "INVALID_SHAPE"
	DiagInvalidSequence     = "INVALID_SEQUENCE"
	DiagSequenceCollision   = "SEQUENCE_COLLISION"
	DiagPathEscape          = "PATH_ESCAPE"
	DiagSelfDependency      = "SELF_DEPENDENCY"
	DiagMissingPrerequisite = "MISSING_PREREQUISITE"
	DiagCycleDetected       = "CYCLE_DETECTED"
	DiagClaimInvalid        = "CLAIM_ATTRIBUTION_INVALID"
	DiagReopenStale         = "REOPEN_PROVENANCE_STALE"
	DiagSupersedeInvalid    = "SUPERSEDE_RECORD_INVALID"
	DiagTaskInconsistent    = "TASK_STATE_INCONSISTENT"
	DiagRegistryOutOfSync   = "REGISTRY_OUT_OF_SYNC"
	DiagTrailingWhitespace  = "TRAILING_WHITESPACE"
	DiagExpectedMismatch    = "EXPECTED_STATE_MISMATCH"
	DiagSurfaceCollision    = "SURFACE_COLLISION"
	DiagDependencyProse     = "DEPENDENCY_PROSE"
)

// Diagnostic describes a deterministic validation issue discovered within a goal package or graph.
type Diagnostic struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"` // "error" | "warning"
	GoalSlug    string `json:"goal_slug,omitempty"`
	Path        string `json:"path,omitempty"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

// ValidationReport captures the full diagnostic outcome of a goal validation run.
type ValidationReport struct {
	Version       string       `json:"version"`
	ProjectDir    string       `json:"project_dir"`
	TargetSlug    string       `json:"target_slug,omitempty"`
	Valid         bool         `json:"valid"`
	CheckedGoals  int          `json:"checked_goals"`
	ErrorsCount   int          `json:"errors_count"`
	WarningsCount int          `json:"warnings_count"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
	Summary       string       `json:"summary"`
}

// ValidateOptions configures goal package validation behavior.
type ValidateOptions struct {
	TargetDir        string                 `json:"target_dir,omitempty"`
	Slug             string                 `json:"slug,omitempty"`
	Strict           bool                   `json:"strict,omitempty"`
	StrictMemory     bool                   `json:"strict_memory,omitempty"`
	ManifestPath     string                 `json:"manifest_path,omitempty"`
	ExpectedManifest *ExpectedStateManifest `json:"expected_manifest,omitempty"`
}

// ExpectedStateManifest defines optional deterministic assertions for selected goals.
type ExpectedStateManifest struct {
	Goals map[string]ExpectedGoalState `json:"goals"`
}

// ExpectedGoalState asserts desired metadata and status for a single goal.
type ExpectedGoalState struct {
	Status       *string   `json:"status,omitempty"`
	Dependencies *[]string `json:"dependencies,omitempty"`
	Sequence     *int      `json:"sequence,omitempty"`
	Tier         *string   `json:"tier,omitempty"`
	TotalTasks   *int      `json:"total_tasks,omitempty"`
	DoneTasks    *int      `json:"done_tasks,omitempty"`
	Shape        *string   `json:"shape,omitempty"`
}

// ValidateGoals executes package, dependency, lifecycle, and registry diagnostics.
func ValidateGoals(rootDir string, opts ValidateOptions) (*ValidationReport, error) {
	if opts.TargetDir != "" {
		rootDir = opts.TargetDir
	}
	projDir, err := ResolveProjectDir(nil, rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve project dir: %w", err)
	}
	resolvedRoot := ResolveGoalsRootDir(projDir)

	manifest := opts.ExpectedManifest
	if manifest == nil && opts.ManifestPath != "" {
		mPath := opts.ManifestPath
		if !filepath.IsAbs(mPath) {
			mPath = filepath.Join(resolvedRoot, mPath)
		}
		data, err := os.ReadFile(mPath)
		if err != nil {
			return nil, fmt.Errorf("read expected-state manifest %q: %w", opts.ManifestPath, err)
		}
		var loaded ExpectedStateManifest
		if err := json.Unmarshal(data, &loaded); err != nil {
			return nil, fmt.Errorf("parse expected-state manifest %q: %w", opts.ManifestPath, err)
		}
		manifest = &loaded
	}

	audit, err := ScanGoals(resolvedRoot)
	if err != nil && !strings.Contains(err.Error(), "no goals directory") {
		return nil, fmt.Errorf("scan goals: %w", err)
	}
	if audit == nil {
		audit = &AuditResult{Project: resolvedRoot}
	}

	auditMap := make(map[string]*Goal, len(audit.Goals))
	seqMap := make(map[int][]string)
	for i := range audit.Goals {
		g := &audit.Goals[i]
		auditMap[g.Slug] = g
		if g.Sequence > 0 && g.Status != "superseded" {
			seqMap[g.Sequence] = append(seqMap[g.Sequence], g.Slug)
		}
	}

	graph := BuildDependencyGraphFromSlice(audit.Goals)
	var diags []Diagnostic
	checkedCount := 0
	goalsDir := filepath.Join(resolvedRoot, "goals")

	if opts.Slug != "" {
		slug := cleanSlug(opts.Slug)
		gDir := filepath.Join(goalsDir, slug)
		if _, err := os.Stat(gDir); os.IsNotExist(err) {
			diags = append(diags, Diagnostic{
				Code: DiagMissingFile, Severity: "error", GoalSlug: slug,
				Path:        filepath.Join("goals", slug, "goal.md"),
				Message:     fmt.Sprintf("goal package %q does not exist", slug),
				Remediation: fmt.Sprintf("Run 'sila goals create %s' to initialize", slug),
			})
		} else {
			checkedCount = 1
			g := auditMap[slug]
			if g == nil {
				g = &Goal{Slug: slug}
			}
			diags = append(diags, validateGoalPackage(resolvedRoot, g, auditMap, seqMap, manifest, opts.StrictMemory)...)
			diags = append(diags, validateCycles(graph, slug)...)
			diags = append(diags, validateSurfaceCollisions(audit.Goals, slug)...)
			if fileExists(filepath.Join(goalsDir, "README.md")) {
				diags = append(diags, validateRegistrySync(resolvedRoot, audit)...)
			}
		}
	} else {
		checkedCount = len(audit.Goals)
		if entries, err := os.ReadDir(goalsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				slug := e.Name()
				if _, exists := auditMap[slug]; !exists && !fileExists(filepath.Join(goalsDir, slug, "goal.md")) {
					diags = append(diags, Diagnostic{
						Code: DiagMissingFile, Severity: "error", GoalSlug: slug,
						Path:        filepath.Join("goals", slug, "goal.md"),
						Message:     "missing required goal.md file",
						Remediation: "Create goal.md with metadata frontmatter or heading",
					})
				}
			}
		}
		for _, g := range audit.Goals {
			diags = append(diags, validateGoalPackage(resolvedRoot, &g, auditMap, seqMap, manifest, opts.StrictMemory)...)
		}
		diags = append(diags, validateCycles(graph, "")...)
		diags = append(diags, validateSurfaceCollisions(audit.Goals, "")...)
		if manifest != nil {
			for expSlug := range manifest.Goals {
				if _, exists := auditMap[expSlug]; !exists {
					diags = append(diags, Diagnostic{
						Code: DiagExpectedMismatch, Severity: "error", GoalSlug: expSlug,
						Path:        filepath.Join("goals", expSlug),
						Message:     fmt.Sprintf("goal %q declared in expected-state manifest does not exist", expSlug),
						Remediation: fmt.Sprintf("Create goal package goals/%s or update manifest", expSlug),
					})
				}
			}
		}
		diags = append(diags, validateRegistrySync(resolvedRoot, audit)...)
	}

	sort.Slice(diags, func(i, j int) bool {
		if diags[i].GoalSlug != diags[j].GoalSlug {
			return diags[i].GoalSlug < diags[j].GoalSlug
		}
		if diags[i].Severity != diags[j].Severity {
			return diags[i].Severity == "error"
		}
		if diags[i].Code != diags[j].Code {
			return diags[i].Code < diags[j].Code
		}
		if diags[i].Path != diags[j].Path {
			return diags[i].Path < diags[j].Path
		}
		return diags[i].Message < diags[j].Message
	})

	errCount, warnCount := 0, 0
	for _, d := range diags {
		if d.Severity == "error" {
			errCount++
		} else {
			warnCount++
		}
	}
	valid := errCount == 0 && (!opts.Strict || warnCount == 0)

	summary := fmt.Sprintf("checked %d goal(s): %d error(s), %d warning(s)", checkedCount, errCount, warnCount)
	if valid {
		summary = fmt.Sprintf("all %d goal package(s) valid (%d warning(s))", checkedCount, warnCount)
		if warnCount == 0 {
			summary = fmt.Sprintf("all %d goal package(s) fully valid", checkedCount)
		}
	}

	return &ValidationReport{
		Version: "sila/goals-validate/v1", ProjectDir: resolvedRoot, TargetSlug: opts.Slug,
		Valid: valid, CheckedGoals: checkedCount, ErrorsCount: errCount, WarningsCount: warnCount,
		Diagnostics: diags, Summary: summary,
	}, nil
}

func validateGoalPackage(rootDir string, g *Goal, auditMap map[string]*Goal, seqMap map[int][]string, manifest *ExpectedStateManifest, strictMemory bool) []Diagnostic {
	var diags []Diagnostic
	goalDir := filepath.Join(rootDir, "goals", g.Slug)

	// Memory protocol: legacy pending goals warn; strict promotes to error.
	protocol := strings.ToLower(strings.TrimSpace(g.MemoryProtocol))
	switch protocol {
	case "", "legacy":
		if g.Status == "pending" {
			sev := "warning"
			if strictMemory {
				sev = "error"
			}
			diags = append(diags, Diagnostic{
				Code: "MEMORY_PROTOCOL_MISSING", Severity: sev, GoalSlug: g.Slug,
				Path:        filepath.Join("goals", g.Slug, "goal.md"),
				Message:     "pending goal has no Memory Protocol marker (legacy default)",
				Remediation: "Add '- **Memory Protocol**: required' and a ## Memory Plan section, or pass --strict-memory only after migration",
			})
		}
	case "required":
		// The certification path enforces the evidence block. Also ensure the
		// plan names at least one write verb.
		if data, err := os.ReadFile(filepath.Join(goalDir, "goal.md")); err == nil {
			text := strings.ToLower(string(data))
			if !strings.Contains(text, "memo") && !strings.Contains(text, "lesson") && !strings.Contains(text, "case") {
				diags = append(diags, Diagnostic{
					Code: "MEMORY_PLAN_NO_WRITES", Severity: "error", GoalSlug: g.Slug,
					Path:        filepath.Join("goals", g.Slug, "goal.md"),
					Message:     "required goal's Memory Plan names no memo, lesson, or case write verb",
					Remediation: "Add a write verb to ## Memory Plan or record a waiver",
				})
			}
		}
	default:
		diags = append(diags, Diagnostic{
			Code: "MEMORY_PROTOCOL_INVALID", Severity: "error", GoalSlug: g.Slug,
			Path:        filepath.Join("goals", g.Slug, "goal.md"),
			Message:     fmt.Sprintf("unknown Memory Protocol %q", g.MemoryProtocol),
			Remediation: "Use required or legacy",
		})
	}

	// 1. Required files
	for _, rf := range []struct{ name, rem string }{
		{"goal.md", "Create goal.md with metadata frontmatter or title"},
		{"facts.md", "Create facts.md describing background facts and invariants"},
		{"plan.md", "Create plan.md detailing task breakdown and verification checks"},
	} {
		p := filepath.Join(goalDir, rf.name)
		rel := filepath.Join("goals", g.Slug, rf.name)
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			diags = append(diags, Diagnostic{Code: DiagMissingFile, Severity: "error", GoalSlug: g.Slug, Path: rel, Message: fmt.Sprintf("missing required %s file", rf.name), Remediation: rf.rem})
		} else if err != nil {
			diags = append(diags, Diagnostic{Code: DiagUnreadableFile, Severity: "error", GoalSlug: g.Slug, Path: rel, Message: fmt.Sprintf("cannot access %s: %v", rf.name, err), Remediation: "Ensure file permissions allow reading"})
		} else if info.IsDir() {
			diags = append(diags, Diagnostic{Code: DiagParseError, Severity: "error", GoalSlug: g.Slug, Path: rel, Message: fmt.Sprintf("%s is a directory, expected file", rf.name), Remediation: "Replace directory with file"})
		}
	}

	// 2. Metadata parsing
	var goalMDContent string
	if data, err := os.ReadFile(filepath.Join(goalDir, "goal.md")); err == nil {
		goalMDContent = string(data)
		content := goalMDContent
		if strings.TrimSpace(content) == "" {
			diags = append(diags, Diagnostic{Code: DiagParseError, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: "goal.md is empty", Remediation: "Add goal metadata"})
		}
		if isAmb, t1, t2 := detectPriorityTierAmbiguity(content); isAmb {
			diags = append(diags, Diagnostic{Code: DiagAmbiguousTier, Severity: "warning", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: fmt.Sprintf("ambiguous tier/priority declarations: %q vs %q", t1, t2), Remediation: "Standardize on single - **Tier**: <tier> declaration"})
		}
		if rawTier := extractRawTier(content); rawTier != "" && !isValidRawTier(rawTier) {
			diags = append(diags, Diagnostic{Code: DiagInvalidTier, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: fmt.Sprintf("invalid tier %q", rawTier), Remediation: "Set Tier to immediate, roadmap, nice-to-have, or wont-fix"})
		}
		if rawShape := extractRawShape(content); rawShape != "" && rawShape != "ship" && rawShape != "scout" {
			diags = append(diags, Diagnostic{Code: DiagInvalidShape, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: fmt.Sprintf("invalid shape %q", rawShape), Remediation: "Set Shape to ship or scout"})
		}
	}

	// 3. Sequence & Path Escapes
	if g.Sequence < 0 || negSeqRE.MatchString(goalMDContent) {
		diags = append(diags, Diagnostic{Code: DiagInvalidSequence, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: fmt.Sprintf("invalid negative sequence in %s", g.Slug), Remediation: "Sequence must be positive"})
	} else if g.Sequence > 0 && g.Status != "superseded" && len(seqMap[g.Sequence]) > 1 {
		var others []string
		for _, c := range seqMap[g.Sequence] {
			if c != g.Slug {
				others = append(others, c)
			}
		}
		if len(others) > 0 {
			diags = append(diags, Diagnostic{Code: DiagSequenceCollision, Severity: "warning", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: fmt.Sprintf("sequence #%d shared with %s", g.Sequence, strings.Join(others, ", ")), Remediation: "Assign unique sequence numbers"})
		}
	}
	for _, f := range g.FilesToTouch {
		clean := filepath.Clean(f)
		if filepath.IsAbs(f) || strings.HasPrefix(clean, "..") || strings.Contains(f, "../") {
			diags = append(diags, Diagnostic{Code: DiagPathEscape, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: fmt.Sprintf("path %q escapes repository boundary", f), Remediation: "Use relative paths within repository root"})
		}
	}

	// 4. Dependencies
	if slices.Contains(g.DependsOn, g.Slug) {
		diags = append(diags, Diagnostic{Code: DiagSelfDependency, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: fmt.Sprintf("goal %q depends on itself", g.Slug), Remediation: "Remove self-reference from dependencies"})
	}
	for _, dep := range g.DependsOn {
		if _, exists := auditMap[dep]; !exists {
			diags = append(diags, Diagnostic{Code: DiagMissingPrerequisite, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: fmt.Sprintf("prerequisite %q does not exist", dep), Remediation: fmt.Sprintf("Scaffold prerequisite %q or remove dependency", dep)})
		}
	}
	for _, warn := range g.DependencyWarnings {
		diags = append(diags, Diagnostic{
			Code:        DiagDependencyProse,
			Severity:    "warning",
			GoalSlug:    g.Slug,
			Path:        filepath.Join("goals", g.Slug, "goal.md"),
			Message:     fmt.Sprintf("dependency declaration contains unparsed prose %q", warn),
			Remediation: "Remove prose comments from dependency declaration and use clean comma-separated slugs",
		})
	}

	// 5. Lifecycle attribution & tasks
	if g.Claim != nil && strings.TrimSpace(g.Claim.Agent) == "" {
		diags = append(diags, Diagnostic{Code: DiagClaimInvalid, Severity: "warning", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "claim.json"), Message: "active claim has empty agent attribution", Remediation: "Claim goal with agent attribution"})
	}
	if reopen, _ := GetActiveReopen(rootDir, g.Slug); reopen != nil && reopen.Active {
		if strings.TrimSpace(reopen.ReopenedBy) == "" || strings.TrimSpace(reopen.Reason) == "" || reopen.ReopenedAt.IsZero() {
			diags = append(diags, Diagnostic{Code: DiagReopenStale, Severity: "warning", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "reopen.json"), Message: "reopen record missing provenance metadata", Remediation: "Reopen with agent and reason"})
		}
	}
	if sup, _ := GetActiveSupersede(rootDir, g.Slug); sup != nil && sup.Active {
		if strings.TrimSpace(sup.SupersededBy) == "" || strings.TrimSpace(sup.Reason) == "" {
			diags = append(diags, Diagnostic{Code: DiagSupersedeInvalid, Severity: "warning", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "supersede.json"), Message: "supersede record missing target or reason", Remediation: "Re-record supersede target"})
		}
	}
	if g.TasksTotal > 0 && g.TasksMet > g.TasksTotal {
		diags = append(diags, Diagnostic{Code: DiagTaskInconsistent, Severity: "warning", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "plan.md"), Message: fmt.Sprintf("completed tasks (%d) exceeds total (%d)", g.TasksMet, g.TasksTotal), Remediation: "Realign plan.md task checkboxes"})
	}
	if g.Status == "pending" && g.TasksTotal > 0 && g.TasksMet == g.TasksTotal && g.Commit == "" {
		diags = append(diags, Diagnostic{Code: DiagTaskInconsistent, Severity: "warning", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "plan.md"), Message: "all tasks checked in plan.md but goal has no commit attribution; checked tasks alone do not certify implementation", Remediation: "Commit implementation with git commit -m '... (goals/<slug>/goal.md)'"})
	}

	if manifest != nil {
		if exp, exists := manifest.Goals[g.Slug]; exists {
			diags = append(diags, validateManifestExpectations(g, exp)...)
		}
	}

	// 6. Structured work package & spec review readiness (opted-in goals)
	if fileExists(filepath.Join(goalDir, "work-package.json")) {
		wpDiags, _ := CheckWorkPackageReady(rootDir, g.Slug)
		diags = append(diags, wpDiags...)
	}

	return diags
}

func validateCycles(graph *DependencyGraph, targetSlug string) []Diagnostic {
	var diags []Diagnostic
	if graph == nil {
		return diags
	}
	for _, c := range graph.DetectCycles() {
		if targetSlug != "" && !slices.Contains(c, targetSlug) {
			continue
		}
		diags = append(diags, Diagnostic{
			Code: DiagCycleDetected, Severity: "error", GoalSlug: c[0],
			Path:        filepath.Join("goals", c[0], "goal.md"),
			Message:     fmt.Sprintf("circular dependency detected: %s", strings.Join(c, " -> ")),
			Remediation: "Remove or modify depends_on edges to maintain a DAG",
		})
	}
	return diags
}

func validateRegistrySync(rootDir string, audit *AuditResult) []Diagnostic {
	var diags []Diagnostic
	data, err := os.ReadFile(filepath.Join(rootDir, "goals", "README.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return []Diagnostic{{Code: DiagRegistryOutOfSync, Severity: "error", Path: "goals/README.md", Message: "goals/README.md does not exist on disk", Remediation: "Run 'sila goals' to generate registry"}}
		}
		return []Diagnostic{{Code: DiagUnreadableFile, Severity: "error", Path: "goals/README.md", Message: err.Error(), Remediation: "Ensure goals/README.md is readable"}}
	}
	content := string(data)
	if strings.HasSuffix(content, "\n\n") || strings.HasSuffix(content, "\r\n\r\n") {
		diags = append(diags, Diagnostic{Code: DiagTrailingWhitespace, Severity: "warning", Path: "goals/README.md", Message: "goals/README.md contains trailing empty line(s)", Remediation: "Run 'sila goals' to regenerate index"})
	}
	if err := CheckReadmeSync(rootDir, audit); err != nil {
		diags = append(diags, Diagnostic{Code: DiagRegistryOutOfSync, Severity: "error", Path: "goals/README.md", Message: err.Error(), Remediation: "Run 'sila goals' to synchronize registry"})
	}
	return diags
}

func validateManifestExpectations(g *Goal, exp ExpectedGoalState) []Diagnostic {
	var diags []Diagnostic
	check := func(field, expVal, actVal string) {
		if expVal != actVal {
			diags = append(diags, Diagnostic{
				Code: DiagExpectedMismatch, Severity: "error", GoalSlug: g.Slug,
				Path:        filepath.Join("goals", g.Slug, "goal.md"),
				Message:     fmt.Sprintf("manifest %s mismatch: expected %q, got %q", field, expVal, actVal),
				Remediation: "Align goal state or update manifest expectation",
			})
		}
	}
	if exp.Status != nil {
		check("status", *exp.Status, g.Status)
	}
	if exp.Tier != nil {
		check("tier", NormalizeTier(*exp.Tier), NormalizeTier(g.Tier))
	}
	if exp.Sequence != nil {
		check("sequence", fmt.Sprintf("%d", *exp.Sequence), fmt.Sprintf("%d", g.Sequence))
	}
	if exp.Shape != nil {
		check("shape", NormalizeShape(*exp.Shape), NormalizeShape(g.Shape))
	}
	if exp.TotalTasks != nil && *exp.TotalTasks != g.TasksTotal {
		diags = append(diags, Diagnostic{Code: DiagExpectedMismatch, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "plan.md"), Message: fmt.Sprintf("manifest total_tasks mismatch: expected %d, got %d", *exp.TotalTasks, g.TasksTotal), Remediation: "Align task counts"})
	}
	if exp.DoneTasks != nil && *exp.DoneTasks != g.TasksMet {
		diags = append(diags, Diagnostic{Code: DiagExpectedMismatch, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "plan.md"), Message: fmt.Sprintf("manifest done_tasks mismatch: expected %d, got %d", *exp.DoneTasks, g.TasksMet), Remediation: "Align task counts"})
	}
	if exp.Dependencies != nil {
		expDeps := append([]string(nil), *exp.Dependencies...)
		actDeps := append([]string(nil), g.DependsOn...)
		sort.Strings(expDeps)
		sort.Strings(actDeps)
		if !slices.Equal(expDeps, actDeps) {
			diags = append(diags, Diagnostic{Code: DiagExpectedMismatch, Severity: "error", GoalSlug: g.Slug, Path: filepath.Join("goals", g.Slug, "goal.md"), Message: fmt.Sprintf("manifest dependencies mismatch: expected %v, got %v", *exp.Dependencies, g.DependsOn), Remediation: "Align dependencies"})
		}
	}
	return diags
}

var (
	explicitTierRE     = regexp.MustCompile(`(?im)^[ \t]*[-*][ \t]*\*\*Tier\*\*[ \t]*:[ \t]*([^\r\n]+)$`)
	explicitPriorityRE = regexp.MustCompile(`(?im)^[ \t]*[-*][ \t]*\*\*Priority\*\*[ \t]*:[ \t]*([^\r\n]+)$`)
	frontmatterTierRE  = regexp.MustCompile(`(?im)^(?:tier)[ \t]*:[ \t]*([^\r\n]+)$`)
	frontmatterPrioRE  = regexp.MustCompile(`(?im)^(?:priority)[ \t]*:[ \t]*([^\r\n]+)$`)
	rawShapeRE         = regexp.MustCompile(`(?im)^[ \t]*[-*][ \t]*\*\*Shape\*\*[ \t]*:[ \t]*([^\r\n]+)$`)
	negSeqRE           = regexp.MustCompile(`(?im)^[ \t]*(?:[-*][ \t]*\*\*Sequence\*\*|(?:sequence|order|seq))[ \t]*:[ \t]*#?(-\d+)`)
)

func detectPriorityTierAmbiguity(content string) (bool, string, string) {
	var explicitTier, explicitPrio string
	if m := explicitTierRE.FindStringSubmatch(content); len(m) > 1 {
		explicitTier = cleanWhitespace(m[1])
	}
	if m := explicitPriorityRE.FindStringSubmatch(content); len(m) > 1 {
		explicitPrio = cleanWhitespace(m[1])
	}
	if explicitTier != "" && explicitPrio != "" && NormalizeTier(explicitTier) != NormalizeTier(explicitPrio) {
		return true, explicitTier, explicitPrio
	}
	if strings.HasPrefix(content, "---\n") || strings.HasPrefix(content, "---\r\n") {
		parts := strings.SplitN(content[3:], "\n---", 2)
		if len(parts) == 2 {
			fm := parts[0]
			if m := frontmatterTierRE.FindStringSubmatch(fm); len(m) > 1 {
				fmTier := cleanWhitespace(m[1])
				if explicitTier != "" && NormalizeTier(fmTier) != NormalizeTier(explicitTier) {
					return true, "frontmatter: " + fmTier, "body: " + explicitTier
				}
			}
			if m := frontmatterPrioRE.FindStringSubmatch(fm); len(m) > 1 {
				fmPrio := cleanWhitespace(m[1])
				if explicitTier != "" && NormalizeTier(fmPrio) != NormalizeTier(explicitTier) {
					return true, "frontmatter priority: " + fmPrio, "body tier: " + explicitTier
				}
			}
		}
	}
	return false, "", ""
}

func extractRawTier(content string) string {
	if m := explicitTierRE.FindStringSubmatch(content); len(m) > 1 {
		return cleanWhitespace(m[1])
	}
	if m := frontmatterTierRE.FindStringSubmatch(content); len(m) > 1 {
		return cleanWhitespace(m[1])
	}
	return ""
}

func isValidRawTier(raw string) bool {
	c := strings.NewReplacer(" ", "", "-", "", "_", "", "'", "").Replace(strings.ToLower(strings.TrimSpace(raw)))
	switch c {
	case "immediate", "tier1", "1", "urgent", "blocker", "p0", "p1",
		"nicetohave", "tier3", "3", "optional", "polish", "p3",
		"wontfix", "defer", "deferred", "tier4", "4", "p4",
		"roadmap", "tier2", "2", "scheduled", "p2", "default", "":
		return true
	default:
		return false
	}
}

func extractRawShape(content string) string {
	if m := rawShapeRE.FindStringSubmatch(content); len(m) > 1 {
		return strings.ToLower(cleanWhitespace(m[1]))
	}
	return ""
}

func validateSurfaceCollisions(goalsList []Goal, targetSlug string) []Diagnostic {
	var diags []Diagnostic
	var activeGoals []Goal
	for _, g := range goalsList {
		if (g.Status == "pending" || g.Status == "claimed") && g.Status != "superseded" {
			if len(g.FilesToTouch) > 0 {
				activeGoals = append(activeGoals, g)
			}
		}
	}

	for i := 0; i < len(activeGoals); i++ {
		g1 := activeGoals[i]
		if targetSlug != "" && g1.Slug != targetSlug {
			continue
		}
		for j := 0; j < len(activeGoals); j++ {
			if i == j {
				continue
			}
			g2 := activeGoals[j]
			isConcurrent := false
			if g1.Sequence > 0 && g1.Sequence == g2.Sequence {
				isConcurrent = true
			} else if g1.Sequence == 0 && g2.Sequence == 0 {
				isConcurrent = true
			}

			if isConcurrent {
				overlaps := OverlappingPaths(g1.FilesToTouch, g2.FilesToTouch)
				var conflictPaths []string
				for _, op := range overlaps {
					if !IsSharedLedgerFile(op) {
						conflictPaths = append(conflictPaths, op)
					}
				}
				if len(conflictPaths) > 0 {
					diags = append(diags, Diagnostic{
						Code:        DiagSurfaceCollision,
						Severity:    "warning",
						GoalSlug:    g1.Slug,
						Path:        filepath.Join("goals", g1.Slug, "goal.md"),
						Message:     fmt.Sprintf("concurrent write-surface collision with goal %q on %v", g2.Slug, conflictPaths),
						Remediation: "Partition FilesToTouch or specify explicit sequence ordering to avoid concurrent mutation collisions",
					})
				}
			}
		}
	}
	return diags
}
