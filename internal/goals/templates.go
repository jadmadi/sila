package goals

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// GoalTemplateData contains metadata exposed to repository goal templates.
// Slice fields (DependsOn, Personas, FilesToTouch, Invariants, Tasks) support range iteration.
// Pre-joined string counterparts (DependsOnStr, PersonasStr) support direct scalar embedding.
type GoalTemplateData struct {
	Slug           string   // Normalized goal identifier
	Title          string   // Human-readable title
	Description    string   // Brief description
	Mode           string   // Execution mode label (Independent or Dependent)
	IsIndependent  bool     // True when goal has no prerequisites
	DependsOn      []string // Prerequisite goal slugs for range iteration
	DependsOnStr   string   // Comma-separated prerequisite slugs, or "none"
	Sequence       int      // Execution order sequence index
	Shape          string   // Task shape (ship or scout)
	Tier           string   // Roadmap tier (immediate, roadmap, nice-to-have, wont-fix)
	MemoryProtocol string   // required or legacy
	Personas       []string // Assigned persona slugs for range iteration
	PersonasStr    string   // Comma-separated persona slugs
	Status         string   // Lifecycle status (pending or implemented)
	Commit         string   // Verifying git commit hash if closed
	FilesToTouch   []string // Declared file paths to touch for range iteration
	Invariants     []string // Architectural invariants for range iteration
	Tasks          []string // Scaffolding tasks for range iteration
	Date           string   // Scaffolding date (YYYY-MM-DD)
	Year           int      // UTC year
	Month          int      // UTC month (1-12)
	Day            int      // UTC day (1-31)
}

func defaultTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"trim":  strings.TrimSpace,
		"join":  strings.Join,
	}
}

// BuildGoalTemplateData constructs template data from CreateGoalParams and derived metadata.
func BuildGoalTemplateData(params CreateGoalParams, slug, title, desc string, isIndep bool, commitSha string, isImplemented bool) GoalTemplateData {
	mode := "Independent (disjoint, parallelizable)"
	if !isIndep {
		mode = "Dependent (requires prerequisite goals)"
	}

	depStr := "none"
	if len(params.DependsOn) > 0 {
		depStr = strings.Join(params.DependsOn, ", ")
	}

	shapeVal := params.Shape
	if shapeVal == "" {
		shapeVal = "ship"
	}

	now := time.Now().UTC()

	var personasStr string
	if len(params.Personas) > 0 {
		personasStr = strings.Join(params.Personas, ", ")
	}

	status := "pending"
	memoryProtocol := "required"
	if isImplemented {
		status = "implemented"
		memoryProtocol = "legacy"
	}

	return GoalTemplateData{
		Slug:           slug,
		Title:          title,
		Description:    desc,
		Mode:           mode,
		IsIndependent:  isIndep,
		DependsOn:      params.DependsOn,
		DependsOnStr:   depStr,
		Sequence:       params.Sequence,
		Shape:          shapeVal,
		Tier:           NormalizeTier(params.Tier),
		MemoryProtocol: memoryProtocol,
		Personas:       params.Personas,
		PersonasStr:    personasStr,
		Status:         status,
		Commit:         commitSha,
		FilesToTouch:   params.FilesToTouch,
		Invariants:     params.Invariants,
		Tasks:          params.Tasks,
		Date:           now.Format("2006-01-02"),
		Year:           now.Year(),
		Month:          int(now.Month()),
		Day:            now.Day(),
	}
}

// FindGoalTemplate discovers custom goal template files from repository directories or explicit templateDir.
// targetFile is typically "goal.md", "facts.md", or "plan.md".
func FindGoalTemplate(rootDir, customDir, targetFile string) (string, error) {
	candidates := []string{
		targetFile,
		targetFile + ".tmpl",
		strings.TrimSuffix(targetFile, ".md") + ".tmpl",
	}

	if customDir != "" {
		resolvedCustom := customDir
		if !filepath.IsAbs(resolvedCustom) {
			resolvedCustom = filepath.Join(rootDir, customDir)
		}
		// If customDir directly points to a file, verify matching base
		fi, err := os.Stat(resolvedCustom)
		if err == nil && !fi.IsDir() {
			base := filepath.Base(resolvedCustom)
			for _, cand := range candidates {
				if strings.EqualFold(base, cand) {
					return resolvedCustom, nil
				}
			}
			return "", nil
		}
		for _, cand := range candidates {
			p := filepath.Join(resolvedCustom, cand)
			if fileExists(p) {
				return p, nil
			}
		}
		return "", nil
	}

	searchDirs := []string{
		filepath.Join(rootDir, ".sila", "templates", "goals"),
		filepath.Join(rootDir, ".sila", "templates"),
		filepath.Join(rootDir, ".github", "sila", "templates"),
		filepath.Join(rootDir, "templates", "goals"),
	}

	for _, dir := range searchDirs {
		for _, cand := range candidates {
			p := filepath.Join(dir, cand)
			if fileExists(p) {
				return p, nil
			}
		}
	}

	return "", nil
}

// RenderTemplateFile loads a template file and renders it with GoalTemplateData.
func RenderTemplateFile(path string, data GoalTemplateData) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read template file %s: %w", path, err)
	}

	tmpl, err := template.New(filepath.Base(path)).Funcs(defaultTemplateFuncs()).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", path, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %s: %w", path, err)
	}

	return buf.String(), nil
}
