package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/skillcreator"
	"github.com/jadmadi/sila/internal/skills"
	"github.com/jadmadi/sila/internal/store"
)

type skillCreateOptions struct {
	promptOnly  bool
	interactive bool
	force       bool
	outDir      string
	dryRun      bool
	harness     string
	skills      string
	exclude     string
	yes         bool
	compact     bool
}

func runSkills(ui render.UI, dbPath string, jsonOut, toonOut bool, opts skillCreateOptions, args []string, invokedAs string) {
	cwd, _ := os.Getwd()
	targetDir := cwd
	if opts.outDir != "" {
		targetDir = opts.outDir
	}

	var cleanArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--prompt-only":
			opts.promptOnly = true
		case a == "--interactive" || a == "-i":
			opts.interactive = true
		case a == "--force" || a == "-f":
			opts.force = true
		case a == "--yes" || a == "-y":
			opts.yes = true
		case a == "--compact" || a == "-compact":
			opts.compact = true
		case a == "--dry-run" || a == "-n" || a == "--preview":
			opts.dryRun = true
		case strings.HasPrefix(a, "--out="):
			opts.outDir = strings.TrimPrefix(a, "--out=")
			targetDir = opts.outDir
		case a == "--out" && i+1 < len(args):
			i++
			opts.outDir = args[i]
			targetDir = opts.outDir
		case strings.HasPrefix(a, "--harness="):
			opts.harness = strings.TrimPrefix(a, "--harness=")
		case a == "--harness" && i+1 < len(args):
			i++
			opts.harness = args[i]
		case strings.HasPrefix(a, "--skills="):
			opts.skills = strings.TrimPrefix(a, "--skills=")
		case strings.HasPrefix(a, "--skill="):
			opts.skills = strings.TrimPrefix(a, "--skill=")
		case (a == "--skills" || a == "--skill") && i+1 < len(args):
			i++
			opts.skills = args[i]
		case strings.HasPrefix(a, "--exclude="):
			opts.exclude = strings.TrimPrefix(a, "--exclude=")
		case a == "--exclude" && i+1 < len(args):
			i++
			opts.exclude = args[i]
		case !strings.HasPrefix(a, "-"):
			cleanArgs = append(cleanArgs, a)
		}
	}

	switch invokedAs {
	case "skills":
		// Plural command: by default lists all project skills
		if len(cleanArgs) == 0 || cleanArgs[0] == "list" || cleanArgs[0] == "ls" {
			runSkillsList(ui, jsonOut, toonOut, opts.compact, targetDir)
			return
		}
		switch cleanArgs[0] {
		case "show", "get", "info":
			if len(cleanArgs) < 2 {
				fmt.Fprintf(os.Stderr, "Usage: sila skills show <name>\n")
				os.Exit(1)
			}
			runSkillShow(ui, jsonOut, toonOut, targetDir, cleanArgs[1])
			return
		case "create", "new", "scaffold":
			runSkillCreate(ui, dbPath, jsonOut, toonOut, opts, cleanArgs[1:])
			return
		case "preview", "diff", "dry-run":
			runSkillPreviewCmd(ui, jsonOut, toonOut, targetDir, opts, cleanArgs[1:], args)
			return
		case "apply":
			runSkillApplyCmd(ui, jsonOut, toonOut, targetDir, opts, cleanArgs[1:], args)
			return
		case "remove", "rm", "uninstall", "rollback":
			runSkillRemoveCmd(ui, jsonOut, toonOut, targetDir, opts, cleanArgs[1:], args)
			return
		case "assess", "audit", "eval":
			runSkillAssess(ui, dbPath, jsonOut, toonOut, targetDir, cleanArgs[1:], args)
			return
		case "search", "find":
			runSkillSearch(ui, jsonOut, toonOut, targetDir, cleanArgs[1:], args)
			return
		case "sync":
			runSkillSync(ui, jsonOut, toonOut, targetDir, cleanArgs[1:], args)
			return
		}
		// If single argument matches an existing skill slug, show it
		if len(cleanArgs) == 1 {
			if _, err := skillcreator.LoadProjectSkill(targetDir, cleanArgs[0]); err == nil {
				runSkillShow(ui, jsonOut, toonOut, targetDir, cleanArgs[0])
				return
			}
		}
		runSkillsList(ui, jsonOut, toonOut, opts.compact, targetDir)
		return

	case "skill":
		// Singular command: manages, creates, or inspects a skill
		if len(cleanArgs) == 0 {
			printSkillUsage(ui, jsonOut, targetDir)
			return
		}
		switch cleanArgs[0] {
		case "assess", "audit", "eval":
			runSkillAssess(ui, dbPath, jsonOut, toonOut, targetDir, cleanArgs[1:], args)
			return
		case "search", "find":
			runSkillSearch(ui, jsonOut, toonOut, targetDir, cleanArgs[1:], args)
			return
		case "preview", "diff", "dry-run":
			runSkillPreviewCmd(ui, jsonOut, toonOut, targetDir, opts, cleanArgs[1:], args)
			return
		case "apply":
			runSkillApplyCmd(ui, jsonOut, toonOut, targetDir, opts, cleanArgs[1:], args)
			return
		case "remove", "rm", "uninstall", "rollback":
			runSkillRemoveCmd(ui, jsonOut, toonOut, targetDir, opts, cleanArgs[1:], args)
			return
		case "sync":
			runSkillSync(ui, jsonOut, toonOut, targetDir, cleanArgs[1:], args)
			return
		case "list", "ls":
			runSkillsList(ui, jsonOut, toonOut, opts.compact, targetDir)
			return
		case "show", "get", "info":
			if len(cleanArgs) < 2 {
				fmt.Fprintf(os.Stderr, "Usage: sila skill show <name>\n")
				os.Exit(1)
			}
			runSkillShow(ui, jsonOut, toonOut, targetDir, cleanArgs[1])
			return
		case "create", "new", "scaffold":
			runSkillCreate(ui, dbPath, jsonOut, toonOut, opts, cleanArgs[1:])
			return
		}

		// If single argument matches an existing skill slug, show it
		if len(cleanArgs) == 1 {
			if _, err := skillcreator.LoadProjectSkill(targetDir, cleanArgs[0]); err == nil {
				runSkillShow(ui, jsonOut, toonOut, targetDir, cleanArgs[0])
				return
			}
		}
		// Treat cleanArgs as topic tokens for skill creation (graceful singular shortcut)
		runSkillCreate(ui, dbPath, jsonOut, toonOut, opts, cleanArgs)
		return

	default: // "skill-create", "skills-create"
		runSkillCreate(ui, dbPath, jsonOut, toonOut, opts, cleanArgs)
		return
	}
}

func runSkillsList(ui render.UI, jsonOut, toonOut, compact bool, projectDir string) {
	if compact {
		summaries, err := skills.ListSkillSummaries(projectDir, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila skills: %v\n", err)
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(map[string]interface{}{
				"skills":      summaries,
				"count":       len(summaries),
				"project_dir": projectDir,
				"compact":     true,
			})
			return
		}

		if toonOut {
			fmt.Fprintf(os.Stdout, "skills{slug,source,tokens,has_scripts,trigger}:\n")
			for _, s := range summaries {
				fmt.Fprintf(os.Stdout, "  %s,%s,%d,%t,%q\n", s.Slug, s.Source, s.EstimatedTokens, s.HasScripts, s.Trigger)
			}
			fmt.Fprintln(os.Stdout)
			return
		}

		fmt.Fprintf(os.Stdout, "%s sila · compact skill manifest (progressive disclosure)\n\n", ui.Cyan("·"))
		if len(summaries) == 0 {
			fmt.Fprintln(os.Stdout, "  No skills available.")
			return
		}

		fmt.Fprintf(os.Stdout, "  %-24s %-8s %7s %-8s %s\n", "SLUG", "SOURCE", "TOKENS", "SCRIPTS", "PROMPT TRIGGER")
		fmt.Fprintf(os.Stdout, "  %-24s %-8s %7s %-8s %s\n", "----", "------", "------", "-------", "--------------")
		for _, s := range summaries {
			scriptsStr := "no"
			if s.HasScripts {
				scriptsStr = "yes"
			}
			trigger := s.Trigger
			if len(trigger) > 60 {
				trigger = trigger[:57] + "..."
			}
			fmt.Fprintf(os.Stdout, "  %-24s %-8s %7d %-8s %s\n", s.Slug, s.Source, s.EstimatedTokens, scriptsStr, trigger)
		}
		fmt.Fprintln(os.Stdout)
		return
	}

	skillsList, err := skillcreator.ListProjectSkills(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila skills: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]interface{}{
			"skills":      skillsList,
			"count":       len(skillsList),
			"project_dir": projectDir,
		})
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "skills{slug,title,path,has_scripts,has_refs}:\n")
		for _, s := range skillsList {
			fmt.Fprintf(os.Stdout, "  %s,%q,%s,%t,%t\n", s.Slug, s.Name, s.Path, s.HasScripts, s.HasRefs)
		}
		fmt.Fprintln(os.Stdout)
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · project agent skills (.agents/skills/)\n\n", ui.Cyan("·"))
	if len(skillsList) == 0 {
		fmt.Fprintln(os.Stdout, "  No project skills found in .agents/skills/.")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "  Craft a bespoke project skill:")
		fmt.Fprintln(os.Stdout, "    sila skill create <topic...>")
		fmt.Fprintln(os.Stdout)
		return
	}

	for i, s := range skillsList {
		fmt.Fprintf(os.Stdout, "  %2d. %s\n", i+1, ui.Bold(ui.Cyan(s.Slug)))
		if s.Name != "" && s.Name != s.Slug {
			fmt.Fprintf(os.Stdout, "      %s\n", ui.Bold(s.Name))
		}
		if s.Description != "" {
			fmt.Fprintf(os.Stdout, "      %s\n", ui.Dim(s.Description))
		}
		fmt.Fprintf(os.Stdout, "      %s %s\n", ui.Dim("→"), s.Path)
		fmt.Fprintln(os.Stdout)
	}

	fmt.Fprintf(os.Stdout, "%s\n", ui.Bold("Commands:"))
	fmt.Fprintf(os.Stdout, "  Assess project skills: sila skill assess [--json] [--toon]\n")
	fmt.Fprintf(os.Stdout, "  Search unified catalog: sila skill search <query>\n")
	fmt.Fprintf(os.Stdout, "  Inspect a skill:       sila skill show <name>\n")
	fmt.Fprintf(os.Stdout, "  Craft a new skill:     sila skill create <topic...> [options]\n\n")
}

func runSkillShow(ui render.UI, jsonOut, toonOut bool, projectDir, slugOrPath string) {
	skill, err := skillcreator.LoadProjectSkill(projectDir, slugOrPath)
	if err != nil {
		if uSkill, uErr := skills.LoadSkill(projectDir, slugOrPath, true); uErr == nil {
			skill = &skillcreator.ProjectSkill{
				Slug:        uSkill.Slug,
				Name:        uSkill.Name,
				Description: uSkill.Description,
				Path:        uSkill.Path,
				HasScripts:  uSkill.HasScripts,
				HasRefs:     uSkill.HasRefs,
				Triggers:    uSkill.Triggers,
				RawMarkdown: uSkill.RawMarkdown,
			}
			err = nil
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila skill show: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(skill)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "skill{slug,title,path,triggers_count}:\n")
		fmt.Fprintf(os.Stdout, "  %s,%q,%s,%d\n\n", skill.Slug, skill.Name, skill.Path, len(skill.Triggers))
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · project skill: %s\n\n", ui.Cyan("·"), ui.Bold(skill.Slug))
	if skill.Name != "" {
		fmt.Fprintf(os.Stdout, "  Title:        %s\n", ui.Bold(skill.Name))
	}
	fmt.Fprintf(os.Stdout, "  Location:     %s\n", skill.Path)
	if skill.Description != "" {
		fmt.Fprintf(os.Stdout, "  Description:  %s\n", ui.Dim(skill.Description))
	}
	if len(skill.Triggers) > 0 {
		fmt.Fprintf(os.Stdout, "\n  %s\n", ui.Bold("Trigger Scenarios:"))
		for _, t := range skill.Triggers {
			fmt.Fprintf(os.Stdout, "    • %s\n", t)
		}
	}
	fmt.Fprintln(os.Stdout)

	if skill.RawMarkdown != "" {
		fmt.Fprintf(os.Stdout, "%s\n", ui.Dim("──────────────────────────────────────────────────────────────────"))
		fmt.Fprintln(os.Stdout, skill.RawMarkdown)
	}
}

func printSkillUsage(ui render.UI, jsonOut bool, projectDir string) {
	skills, _ := skillcreator.ListProjectSkills(projectDir)
	if jsonOut {
		emitJSON(map[string]interface{}{
			"message": "Specify skill action or topic to create",
			"usage":   "sila skills | sila skill create <topic...> | sila skill show <name>",
			"skills":  skills,
		})
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · bespoke project skill authoring & management\n\n", ui.Cyan("·"))
	fmt.Fprintln(os.Stdout, "Usage:")
	fmt.Fprintln(os.Stdout, "  sila skills                          List all project skills (plural)")
	fmt.Fprintln(os.Stdout, "  sila skill list                      List all project skills")
	fmt.Fprintln(os.Stdout, "  sila skill show <name>               Inspect a project skill (singular)")
	fmt.Fprintln(os.Stdout, "  sila skill create <topic...>         Craft a bespoke project skill (singular)")
	fmt.Fprintln(os.Stdout, "  sila skill assess                    Assess project skill recommendations")
	fmt.Fprintln(os.Stdout, "  sila skill search <query>            Search unified skill catalog")
	fmt.Fprintln(os.Stdout, "  sila skill preview [--dry-run]       Preview planned harness configuration diff")
	fmt.Fprintln(os.Stdout, "  sila skill apply [--force]           Atomically apply harness configuration")
	fmt.Fprintln(os.Stdout, "  sila skill remove [--force]          Roll back harness configuration cleanly")
	fmt.Fprintln(os.Stdout, "  sila skill-create <topic...>         Direct alias for skill create")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Example Usage:")
	fmt.Fprintln(os.Stdout, "  # 1. Output the master grilling prompt for LLM chat (Antigravity, Claude Code, Cursor):")
	fmt.Fprintln(os.Stdout, "  sila skill create production readiness scorecard map/roadmap --prompt-only")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "  # 2. Run the interactive terminal interview:")
	fmt.Fprintln(os.Stdout, "  sila skill create production readiness scorecard map/roadmap --interactive")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "  # 3. Direct scaffolding with project grounding defaults:")
	fmt.Fprintln(os.Stdout, "  sila skill create production readiness scorecard map/roadmap")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "  # 4. View existing project skills (plural):")
	fmt.Fprintln(os.Stdout, "  sila skills")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "  # 5. Inspect a specific project skill (singular):")
	fmt.Fprintln(os.Stdout, "  sila skill show production-readiness-scorecard-map-roadmap")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Options for skill create:")
	fmt.Fprintln(os.Stdout, "  --prompt-only     Generate turnkey master grilling prompt for LLM agent chat")
	fmt.Fprintln(os.Stdout, "  --interactive, -i Conduct 4-question interactive terminal interview in CLI")
	fmt.Fprintln(os.Stdout, "  --force           Overwrite existing skill package if already present")
	fmt.Fprintln(os.Stdout, "  --out=<dir>       Custom root directory (defaults to project root)")
	fmt.Fprintln(os.Stdout, "  --json            Emit machine-readable JSON output")
	fmt.Fprintln(os.Stdout, "  --toon            Emit Token-Oriented Object Notation (TOON)")
	fmt.Fprintln(os.Stdout)

	if len(skills) > 0 {
		fmt.Fprintf(os.Stdout, "Existing Project Skills (%d found in .agents/skills/):\n", len(skills))
		for _, s := range skills {
			fmt.Fprintf(os.Stdout, "  • %s\n", s.Slug)
		}
		fmt.Fprintln(os.Stdout)
	}
}

func runSkillCreate(ui render.UI, dbPath string, jsonOut, toonOut bool, opts skillCreateOptions, args []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila skill-create: store open: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	var topicTokens []string

	for _, a := range args {
		if a == "--prompt-only" {
			opts.promptOnly = true
		} else if a == "--interactive" || a == "-i" {
			opts.interactive = true
		} else if a == "--force" || a == "-f" {
			opts.force = true
		} else if strings.HasPrefix(a, "--out=") {
			opts.outDir = strings.TrimPrefix(a, "--out=")
		} else if !strings.HasPrefix(a, "-") {
			topicTokens = append(topicTokens, a)
		}
	}

	targetDir := cwd
	if opts.outDir != "" {
		targetDir = opts.outDir
	}

	grounding, err := skillcreator.CollectProjectGrounding(st, targetDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila skill-create: collect grounding: %v\n", err)
		os.Exit(1)
	}

	// If no topic words provided, print interactive usage guide
	if len(topicTokens) == 0 {
		printSkillUsage(ui, jsonOut, targetDir)
		return
	}

	topic := skillcreator.ParseTopic(topicTokens)

	// Mode 1: --prompt-only (emits turnkey master interrogation prompt)
	if opts.promptOnly {
		prompt := skillcreator.AssembleGrillPrompt(grounding, topic)
		fmt.Println(prompt)
		return
	}

	// Mode 2: --interactive (runs terminal interrogation loop)
	var draft skillcreator.SkillDraft
	if opts.interactive {
		var err error
		draft, err = skillcreator.RunInteractiveGrill(ui, os.Stdin, os.Stdout, grounding, topic)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila skill-create: interactive grill error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Non-interactive scaffold with project grounding defaults
		draft = skillcreator.DefaultSkillDraft(grounding, topic)
	}

	res, err := skillcreator.ScaffoldSkill(targetDir, draft, opts.force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila skill-create: %v\n", err)
		os.Exit(1)
	}

	// Record skill creation into Sila store for self-improvement and provenance tracking
	lessonBody := fmt.Sprintf("Created project-tailored agent skill '%s' (%s) under .agents/skills/%s/",
		draft.Name, topic.RawInput, draft.Slug)
	_, _ = st.SaveLesson(grounding.ProjectID, "agent skill authoring", "sila skill create", lessonBody, "skills,skill-creator,agents", "sila", targetDir, []string{res.SkillMDPath})

	if jsonOut {
		emitJSON(map[string]interface{}{
			"scaffold": res,
			"draft":    draft,
			"grounding": map[string]interface{}{
				"tech_stack":  grounding.TechStack,
				"project_dir": grounding.ProjectDir,
			},
		})
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "scaffold{slug,title,destination,files_count,bytes}:\n")
		fmt.Fprintf(os.Stdout, "  %s,%q,%s,%d,%d\n\n",
			draft.Slug, draft.Name, res.SkillMDPath, len(res.FilesWritten), res.BytesWritten)
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · tailored agent skill created successfully\n\n", ui.Green("✔"))
	fmt.Fprintf(os.Stdout, "  Skill Name:   %s\n", ui.Bold(draft.Name))
	fmt.Fprintf(os.Stdout, "  Slug:         %s\n", ui.Cyan(draft.Slug))
	fmt.Fprintf(os.Stdout, "  Destination:  %s\n", ui.Bold(res.SkillMDPath))
	if len(grounding.TechStack) > 0 {
		fmt.Fprintf(os.Stdout, "  Tech Ground:  %s\n", strings.Join(grounding.TechStack, ", "))
	}
	fmt.Fprintf(os.Stdout, "  Description:  %s\n\n", ui.Dim(draft.Description))

	fmt.Fprintf(os.Stdout, "%s\n", ui.Bold("Active Triggers Configured:"))
	for _, t := range draft.Triggers {
		fmt.Fprintf(os.Stdout, "  • %s\n", t)
	}
	fmt.Fprintln(os.Stdout)

	fmt.Fprintf(os.Stdout, "%s\n", ui.Bold("Verification & Self-Improvement Rubric:"))
	for _, r := range draft.Rubric {
		fmt.Fprintf(os.Stdout, "  - [ ] %s\n", r)
	}
	fmt.Fprintln(os.Stdout)

	fmt.Fprintf(os.Stdout, "%s\n", ui.Bold("Next Actions:"))
	fmt.Fprintf(os.Stdout, "  1. Inspect skill:    sila skill show %s\n", draft.Slug)
	fmt.Fprintf(os.Stdout, "  2. View all skills:  sila skills\n")
	fmt.Fprintf(os.Stdout, "  3. Further refine:   sila skill create %s --interactive\n", topic.RawInput)
	fmt.Fprintf(os.Stdout, "  4. Prompt AI agent:  sila skill create %s --prompt-only\n\n", topic.RawInput)
}
