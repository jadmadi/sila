package assess

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/skillcreator"
	"github.com/jadmadi/sila/internal/skills"
	"github.com/jadmadi/sila/internal/store"
)

// SkillAssessmentReport is the complete, read-only evaluation of a project's skill context.
type SkillAssessmentReport struct {
	ProjectDir     string                `json:"project_dir"`
	ProjectID      string                `json:"project_id"`
	EvaluatedAt    time.Time             `json:"evaluated_at"`
	Grounding      SkillGroundingSummary `json:"grounding"`
	Policy         SkillPolicySummary    `json:"policy"`
	CatalogSummary SkillCatalogSummary   `json:"catalog_summary"`
	Recommended    []RecommendedSkill    `json:"recommended"`
	MissingPins    []MissingPinReport    `json:"missing_pins,omitempty"`
	CostAccounting SkillCostAccounting   `json:"cost_accounting"`
}

type SkillGroundingSummary struct {
	TechStack    []string `json:"tech_stack"`
	Manifests    []string `json:"manifests"`
	ActiveGoals  []string `json:"active_goals"`
	RulesPresent bool     `json:"rules_present"`
}

type SkillPolicySummary struct {
	ConfigPath string   `json:"config_path,omitempty"`
	Pinned     []string `json:"pinned"`
	Required   []string `json:"required"`
	Exclude    []string `json:"exclude"`
}

type SkillCatalogSummary struct {
	TotalAvailable int `json:"total_available"`
	ProjectLocal   int `json:"project_local"`
	GlobalLibrary  int `json:"global_library"`
	TotalBytes     int `json:"total_bytes"`
	TotalTokens    int `json:"total_tokens"`
}

type RecommendedSkill struct {
	Slug            string   `json:"slug"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Source          string   `json:"source"` // "project" or "global"
	Path            string   `json:"path"`
	Pinned          bool     `json:"pinned"`
	Required        bool     `json:"required"`
	Reasons         []string `json:"reasons"`
	EstimatedTokens int      `json:"estimated_tokens"`
	SchemaBytes     int      `json:"schema_bytes"`
}

type MissingPinReport struct {
	Slug     string `json:"slug"`
	Required bool   `json:"required"`
	Message  string `json:"message"`
}

type SkillCostAccounting struct {
	UnfilteredTokens int     `json:"unfiltered_tokens"`
	SelectedTokens   int     `json:"selected_tokens"`
	ReclaimedTokens  int     `json:"reclaimed_tokens"`
	SavingsPercent   float64 `json:"savings_percent"`
	Methodology      string  `json:"methodology"`
}

type SkillAssessOptions struct {
	ProjectDir string
	Query      string // Optional query or task intent
}

type projectSkillConfigWrapper struct {
	Skills *struct {
		Pinned   []string `json:"pinned"`
		Required []string `json:"required"`
		Exclude  []string `json:"exclude"`
	} `json:"skills,omitempty"`
	PinnedSkills   []string `json:"pinned_skills,omitempty"`
	RequiredSkills []string `json:"required_skills,omitempty"`
	ExcludedSkills []string `json:"excluded_skills,omitempty"`
}

// LoadProjectSkillPolicy safely loads user pins and exclusions without modifying any files.
func LoadProjectSkillPolicy(projectDir string) (SkillPolicySummary, error) {
	summary := SkillPolicySummary{
		Pinned:   []string{},
		Required: []string{},
		Exclude:  []string{},
	}

	cfgPath := config.FindProjectConfig(projectDir)
	if cfgPath != "" {
		summary.ConfigPath = cfgPath
		if data, err := os.ReadFile(cfgPath); err == nil {
			var wrapper projectSkillConfigWrapper
			if err := json.Unmarshal(data, &wrapper); err == nil {
				if wrapper.Skills != nil {
					summary.Pinned = append(summary.Pinned, wrapper.Skills.Pinned...)
					summary.Required = append(summary.Required, wrapper.Skills.Required...)
					summary.Exclude = append(summary.Exclude, wrapper.Skills.Exclude...)
				}
				summary.Pinned = append(summary.Pinned, wrapper.PinnedSkills...)
				summary.Required = append(summary.Required, wrapper.RequiredSkills...)
				summary.Exclude = append(summary.Exclude, wrapper.ExcludedSkills...)
			}
		}
	}

	// Also check .agents/skills.json
	harnessPath := filepath.Join(projectDir, ".agents", "skills.json")
	if data, err := os.ReadFile(harnessPath); err == nil {
		var hCfg skills.JSONConfig
		if err := json.Unmarshal(data, &hCfg); err == nil {
			summary.Exclude = append(summary.Exclude, hCfg.Exclude...)
			for _, entry := range hCfg.Entries {
				summary.Pinned = append(summary.Pinned, entry.IncludeOnly...)
			}
		}
	}

	summary.Pinned = normalizeSlugs(summary.Pinned)
	summary.Required = normalizeSlugs(summary.Required)
	summary.Exclude = normalizeSlugs(summary.Exclude)

	return summary, nil
}

func normalizeSlugs(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range items {
		cleaned := strings.TrimSpace(strings.ToLower(item))
		if cleaned != "" && !seen[cleaned] {
			seen[cleaned] = true
			result = append(result, cleaned)
		}
	}
	return result
}

// AssessProjectSkills evaluates project grounding, unified catalog, and policy strictly read-only.
func AssessProjectSkills(ctx context.Context, st *store.Store, opts SkillAssessOptions) (*SkillAssessmentReport, error) {
	pDir := opts.ProjectDir
	if pDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		pDir = cwd
	}

	// 1. Gather Project Grounding in memory
	grounding, err := skillcreator.CollectProjectGrounding(st, pDir)
	if err != nil {
		return nil, fmt.Errorf("collect grounding: %w", err)
	}

	techStack := detectTechStack(grounding.Manifests)
	groundingSummary := SkillGroundingSummary{
		TechStack:    techStack,
		Manifests:    grounding.Manifests,
		ActiveGoals:  grounding.ActiveGoals,
		RulesPresent: grounding.RulesSummary != "",
	}

	// 2. Load Policy
	policy, err := LoadProjectSkillPolicy(pDir)
	if err != nil {
		return nil, fmt.Errorf("load policy: %w", err)
	}

	// 3. Scan Unified Catalog
	allSkills, err := skills.ListUnifiedSkills(pDir)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}

	totalBytes := 0
	totalTokens := 0
	pCount := 0
	gCount := 0
	skillsBySlug := make(map[string]skills.Skill, len(allSkills))

	for _, sk := range allSkills {
		skillsBySlug[sk.Slug] = sk
		bLen := len(sk.RawMarkdown)
		if bLen == 0 {
			bLen = len(sk.Description) + 100
		}
		totalBytes += bLen
		totalTokens += EstimateTokens(bLen)

		if sk.Source == "project" {
			pCount++
		} else {
			gCount++
		}
	}

	catSummary := SkillCatalogSummary{
		TotalAvailable: len(allSkills),
		ProjectLocal:   pCount,
		GlobalLibrary:  gCount,
		TotalBytes:     totalBytes,
		TotalTokens:    totalTokens,
	}

	// 4. Evidence Matching & Recommendation
	excludeMap := make(map[string]bool)
	for _, exc := range policy.Exclude {
		excludeMap[strings.ToLower(exc)] = true
	}

	pinnedMap := make(map[string]bool)
	for _, pin := range policy.Pinned {
		pinnedMap[strings.ToLower(pin)] = true
	}

	requiredMap := make(map[string]bool)
	for _, req := range policy.Required {
		requiredMap[strings.ToLower(req)] = true
	}

	var recommended []RecommendedSkill

	for _, sk := range allSkills {
		sSlug := strings.ToLower(sk.Slug)
		if excludeMap[sSlug] || skills.IsDeniedByPolicy(pDir, sk.Slug) {
			continue
		}

		isPinned := pinnedMap[sSlug]
		isRequired := requiredMap[sSlug]
		var reasons []string

		if isPinned || isRequired {
			if isRequired {
				reasons = append(reasons, "required in project policy")
			} else {
				reasons = append(reasons, "pinned in .sila/config.json")
			}
		}

		// Check manifests
		for _, m := range grounding.Manifests {
			mLow := strings.ToLower(m)
			switch {
			case mLow == "go.mod":
				if strings.HasPrefix(sSlug, "golang-") || strings.Contains(sSlug, "go-") {
					reasons = append(reasons, "manifest:go.mod (Go stack detected)")
				}
			case mLow == "package.json":
				if strings.HasPrefix(sSlug, "typescript-") || strings.HasPrefix(sSlug, "node-") || strings.Contains(sSlug, "frontend") {
					reasons = append(reasons, "manifest:package.json (Node/TS detected)")
				}
			case mLow == "wrangler.jsonc" || mLow == "wrangler.toml":
				if strings.HasPrefix(sSlug, "cf-") || strings.Contains(sSlug, "cloudflare") || strings.Contains(sSlug, "worker") {
					reasons = append(reasons, fmt.Sprintf("manifest:%s (Cloudflare Workers detected)", m))
				}
			case mLow == "cargo.toml":
				if strings.HasPrefix(sSlug, "rust-") {
					reasons = append(reasons, "manifest:Cargo.toml (Rust stack detected)")
				}
			case mLow == "pyproject.toml" || mLow == "requirements.txt":
				if strings.HasPrefix(sSlug, "python-") {
					reasons = append(reasons, fmt.Sprintf("manifest:%s (Python detected)", m))
				}
			}
		}

		// Check active goals
		for _, g := range grounding.ActiveGoals {
			gTokens := strings.Fields(strings.ToLower(g))
			for _, tok := range gTokens {
				tok = strings.Trim(tok, ":,-_()[]{}")
				if len(tok) >= 4 {
					if strings.Contains(sSlug, tok) || strings.Contains(strings.ToLower(sk.Name), tok) {
						reasons = append(reasons, fmt.Sprintf("goal keyword: %s", tok))
						break
					}
				}
			}
		}

		// Check optional query
		if opts.Query != "" {
			qTokens := strings.Fields(strings.ToLower(opts.Query))
			for _, q := range qTokens {
				if strings.Contains(sSlug, q) || strings.Contains(strings.ToLower(sk.Description), q) {
					reasons = append(reasons, fmt.Sprintf("query match: %s", opts.Query))
					break
				}
			}
		}

		// Core repository governance
		if len(reasons) == 0 && (sSlug == "git-workflow-and-versioning" || sSlug == "test-driven-development") {
			reasons = append(reasons, "repository governance standard")
		}

		if len(reasons) > 0 {
			reasons = deduplicateStrings(reasons)
			bLen := len(sk.RawMarkdown)
			if bLen == 0 {
				bLen = len(sk.Description) + 100
			}
			tokens := EstimateTokens(bLen)

			recommended = append(recommended, RecommendedSkill{
				Slug:            sk.Slug,
				Name:            sk.Name,
				Description:     sk.Description,
				Source:          sk.Source,
				Path:            sk.Path,
				Pinned:          isPinned,
				Required:        isRequired,
				Reasons:         reasons,
				EstimatedTokens: tokens,
				SchemaBytes:     bLen,
			})
		}
	}

	// Sort recommended: pinned first, then by token size descending, then slug
	sort.Slice(recommended, func(i, j int) bool {
		if recommended[i].Pinned != recommended[j].Pinned {
			return recommended[i].Pinned
		}
		if recommended[i].Required != recommended[j].Required {
			return recommended[i].Required
		}
		return recommended[i].Slug < recommended[j].Slug
	})

	// 5. Verify Missing Pins
	var missingPins []MissingPinReport
	for pin := range pinnedMap {
		if _, ok := skillsBySlug[pin]; !ok {
			missingPins = append(missingPins, MissingPinReport{
				Slug:     pin,
				Required: requiredMap[pin],
				Message:  fmt.Sprintf("Required skill %q in project policy was not found in catalog", pin),
			})
		}
	}
	for req := range requiredMap {
		if !pinnedMap[req] {
			if _, ok := skillsBySlug[req]; !ok {
				missingPins = append(missingPins, MissingPinReport{
					Slug:     req,
					Required: true,
					Message:  fmt.Sprintf("Required skill %q in project policy was not found in catalog", req),
				})
			}
		}
	}
	sort.Slice(missingPins, func(i, j int) bool {
		return missingPins[i].Slug < missingPins[j].Slug
	})

	// 6. Cost Accounting
	selectedTokens := 0
	for _, r := range recommended {
		selectedTokens += r.EstimatedTokens
	}
	reclaimedTokens := catSummary.TotalTokens - selectedTokens
	if reclaimedTokens < 0 {
		reclaimedTokens = 0
	}
	var savingsPct float64
	if catSummary.TotalTokens > 0 {
		savingsPct = math.Round((float64(reclaimedTokens)/float64(catSummary.TotalTokens)*100.0)*10) / 10
	}

	cost := SkillCostAccounting{
		UnfilteredTokens: catSummary.TotalTokens,
		SelectedTokens:   selectedTokens,
		ReclaimedTokens:  reclaimedTokens,
		SavingsPercent:   savingsPct,
		Methodology:      "ceil(schema_bytes / 3.7)",
	}

	pID := grounding.ProjectID
	if pID == "" {
		pID = filepath.Base(pDir)
	}

	report := &SkillAssessmentReport{
		ProjectDir:     pDir,
		ProjectID:      pID,
		EvaluatedAt:    time.Now().UTC(),
		Grounding:      groundingSummary,
		Policy:         policy,
		CatalogSummary: catSummary,
		Recommended:    recommended,
		MissingPins:    missingPins,
		CostAccounting: cost,
	}

	return report, nil
}

func detectTechStack(manifests []string) []string {
	var stack []string
	for _, m := range manifests {
		mLow := strings.ToLower(m)
		switch {
		case mLow == "go.mod":
			stack = append(stack, "Go")
		case mLow == "package.json":
			stack = append(stack, "Node.js / TypeScript")
		case mLow == "wrangler.jsonc" || mLow == "wrangler.toml":
			stack = append(stack, "Cloudflare Workers")
		case mLow == "cargo.toml":
			stack = append(stack, "Rust")
		case mLow == "pyproject.toml" || mLow == "requirements.txt":
			stack = append(stack, "Python")
		}
	}
	return deduplicateStrings(stack)
}

func deduplicateStrings(items []string) []string {
	seen := make(map[string]bool)
	var res []string
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			res = append(res, it)
		}
	}
	return res
}

// RenderSkillAssessmentTOON renders the assessment report in TOON format.
func RenderSkillAssessmentTOON(report *SkillAssessmentReport) string {
	var sb strings.Builder
	sb.WriteString("skill_assessment_summary:\n")
	fmt.Fprintf(&sb, "  project: %s\n", report.ProjectDir)
	fmt.Fprintf(&sb, "  tech_stack: %s\n", strings.Join(report.Grounding.TechStack, ","))
	fmt.Fprintf(&sb, "  manifests: %s\n", strings.Join(report.Grounding.Manifests, ","))
	fmt.Fprintf(&sb, "  total_available: %d\n", report.CatalogSummary.TotalAvailable)
	fmt.Fprintf(&sb, "  recommended_count: %d\n", len(report.Recommended))
	fmt.Fprintf(&sb, "  unfiltered_tokens: %d\n", report.CostAccounting.UnfilteredTokens)
	fmt.Fprintf(&sb, "  selected_tokens: %d\n", report.CostAccounting.SelectedTokens)
	fmt.Fprintf(&sb, "  reclaimed_tokens: %d\n", report.CostAccounting.ReclaimedTokens)
	fmt.Fprintf(&sb, "  savings_pct: %.1f%%\n\n", report.CostAccounting.SavingsPercent)

	fmt.Fprintf(&sb, "recommendations[%d]{slug,pinned,required,source,tokens,reasons}:\n", len(report.Recommended))
	for _, r := range report.Recommended {
		reasonsStr := strings.Join(r.Reasons, "; ")
		fmt.Fprintf(&sb, "  %s,%t,%t,%s,%d,%q\n", r.Slug, r.Pinned, r.Required, r.Source, r.EstimatedTokens, reasonsStr)
	}

	if len(report.MissingPins) > 0 {
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "missing_pins[%d]{slug,policy,status}:\n", len(report.MissingPins))
		for _, m := range report.MissingPins {
			policyStr := "pinned"
			if m.Required {
				policyStr = "required"
			}
			fmt.Fprintf(&sb, "  %s,%s,not_installed\n", m.Slug, policyStr)
		}
	}

	return sb.String()
}

// RenderSkillAssessmentTerminal prints human-readable assessment to UI.
func RenderSkillAssessmentTerminal(ui render.UI, report *SkillAssessmentReport) {
	fmt.Fprintf(os.Stdout, "· sila · project skill assessment\n")
	fmt.Fprintf(os.Stdout, "  Project:      %s\n", report.ProjectDir)
	if len(report.Grounding.TechStack) > 0 {
		fmt.Fprintf(os.Stdout, "  Stack:        %s (%s)\n", strings.Join(report.Grounding.TechStack, ", "), strings.Join(report.Grounding.Manifests, ", "))
	}
	if len(report.Grounding.ActiveGoals) > 0 {
		fmt.Fprintf(os.Stdout, "  Active Goals: %s\n", strings.Join(report.Grounding.ActiveGoals, ", "))
	}
	fmt.Fprintf(os.Stdout, "  Catalog:      %d available (%d project, %d global)\n\n",
		report.CatalogSummary.TotalAvailable, report.CatalogSummary.ProjectLocal, report.CatalogSummary.GlobalLibrary)

	if len(report.Recommended) > 0 {
		fmt.Fprintf(os.Stdout, "RECOMMENDED PROJECT SKILLS (%d of %d):\n", len(report.Recommended), report.CatalogSummary.TotalAvailable)
		for i, r := range report.Recommended {
			tag := "[RECOMMENDED]"
			if r.Pinned {
				tag = "[PINNED]"
			}
			fmt.Fprintf(os.Stdout, "  %d. %s %s\n", i+1, r.Slug, tag)
			if r.Description != "" {
				desc := r.Description
				if len(desc) > 90 {
					desc = desc[:87] + "..."
				}
				fmt.Fprintf(os.Stdout, "     %s\n", desc)
			}
			if len(r.Reasons) > 0 {
				fmt.Fprintf(os.Stdout, "     Evidence: %s\n", strings.Join(r.Reasons, ", "))
			}
			fmt.Fprintf(os.Stdout, "     Impact:   ~%d tokens (~%d bytes)\n\n", r.EstimatedTokens, r.SchemaBytes)
		}
	} else {
		fmt.Fprintf(os.Stdout, "No specific skills recommended based on current manifests.\n\n")
	}

	if len(report.MissingPins) > 0 {
		fmt.Fprintf(os.Stderr, "WARNINGS:\n")
		for _, m := range report.MissingPins {
			fmt.Fprintf(os.Stderr, "  ⚠ [MISSING PIN] %s\n", m.Message)
		}
		fmt.Fprintf(os.Stderr, "\n")
	}

	fmt.Fprintf(os.Stdout, "CONTEXT IMPACT ACCOUNTING:\n")
	fmt.Fprintf(os.Stdout, "  Global Catalog (unfiltered):      ~%d tokens (%d skills)\n", report.CostAccounting.UnfilteredTokens, report.CatalogSummary.TotalAvailable)
	fmt.Fprintf(os.Stdout, "  Recommended Project Selection:    ~%d tokens (%d skills)\n", report.CostAccounting.SelectedTokens, len(report.Recommended))
	fmt.Fprintf(os.Stdout, "  Context Reclaimed per Turn:       ~%d tokens (%.1f%% reduction)\n", report.CostAccounting.ReclaimedTokens, report.CostAccounting.SavingsPercent)
	fmt.Fprintf(os.Stdout, "  * Note: Token savings estimated via %s.\n\n", report.CostAccounting.Methodology)

	fmt.Fprintf(os.Stdout, "NEXT ACTIONS:\n")
	fmt.Fprintf(os.Stdout, "  Preview harness config:          sila skill preview\n")
	fmt.Fprintf(os.Stdout, "  Apply project skill config:      sila skill apply\n")
	fmt.Fprintf(os.Stdout, "  Search full catalog:             sila skill search <query>\n")
}
