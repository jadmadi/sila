package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/skills"
	"github.com/jadmadi/sila/internal/store"
)

func runSkillAssess(ui render.UI, dbPath string, jsonOut, toonOut bool, projectDir string, subArgs, rawArgs []string) {
	query := ""
	if len(subArgs) > 0 {
		query = subArgs[0]
	}

	opts := assess.SkillAssessOptions{
		ProjectDir: projectDir,
		Query:      query,
	}

	// Strictly read-only store check: use OpenInspection to ensure zero writes,
	// zero migrations, zero deletions, and zero WAL/SHM file creation!
	var st *store.Store
	if _, err := os.Stat(dbPath); err == nil {
		inspectionStore, _, err := store.OpenInspection(dbPath)
		if err == nil && inspectionStore != nil {
			st = inspectionStore
			defer st.Close()
		}
	}

	report, err := assess.AssessProjectSkills(context.Background(), st, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila skill assess: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(report)
		if hasMissingRequiredPins(report) {
			os.Exit(1)
		}
		return
	}

	if toonOut {
		fmt.Fprint(os.Stdout, assess.RenderSkillAssessmentTOON(report))
		if hasMissingRequiredPins(report) {
			os.Exit(1)
		}
		return
	}

	assess.RenderSkillAssessmentTerminal(ui, report)
	if hasMissingRequiredPins(report) {
		os.Exit(1)
	}
}

func hasMissingRequiredPins(report *assess.SkillAssessmentReport) bool {
	for _, m := range report.MissingPins {
		if m.Required {
			return true
		}
	}
	return false
}

func runSkillSearch(ui render.UI, jsonOut, toonOut bool, projectDir string, subArgs, rawArgs []string) {
	if len(subArgs) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: sila skill search <query>\n")
		os.Exit(1)
	}
	query := subArgs[0]
	skillsList, err := skills.SearchCatalog(projectDir, query, nil, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila skill search: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]interface{}{
			"query":  query,
			"count":  len(skillsList),
			"skills": skillsList,
		})
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "skills_search[count=%d,query=%q]:\n", len(skillsList), query)
		for _, s := range skillsList {
			fmt.Fprintf(os.Stdout, "  %s,%s,%t,%t,%q\n", s.Slug, s.Source, s.HasScripts, s.HasRefs, s.Description)
		}
		return
	}

	fmt.Fprintf(os.Stdout, "Found %d skill(s) matching %q:\n\n", len(skillsList), query)
	for i, s := range skillsList {
		fmt.Fprintf(os.Stdout, "  %d. %s [%s]\n", i+1, s.Slug, s.Source)
		if s.Description != "" {
			fmt.Fprintf(os.Stdout, "     %s\n", s.Description)
		}
	}
}

func runSkillPreview(ui render.UI, jsonOut, toonOut bool, projectDir string, subArgs, rawArgs []string) {
	fmt.Fprintf(os.Stderr, "sila skill preview: harness adapter not yet configured (scheduled for Milestone 3)\n")
	os.Exit(1)
}

func runSkillApply(ui render.UI, jsonOut, toonOut bool, projectDir string, subArgs, rawArgs []string) {
	fmt.Fprintf(os.Stderr, "sila skill apply: harness adapter not yet configured (scheduled for Milestone 3)\n")
	os.Exit(1)
}

func runSkillRemove(ui render.UI, jsonOut, toonOut bool, projectDir string, subArgs, rawArgs []string) {
	fmt.Fprintf(os.Stderr, "sila skill remove: harness adapter not yet configured (scheduled for Milestone 3)\n")
	os.Exit(1)
}

func runSkillSync(ui render.UI, jsonOut, toonOut bool, projectDir string, subArgs, rawArgs []string) {
	fmt.Fprintf(os.Stderr, "sila skill sync: harness adapter not yet configured (scheduled for Milestone 3)\n")
	os.Exit(1)
}
