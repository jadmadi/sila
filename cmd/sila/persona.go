package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/persona"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runPersona(ui render.UI, dbPath string, jsonOut, toonOut bool, args []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila persona: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	mgr := persona.NewManager(st)

	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}

	switch sub {
	case "list":
		domainFilter := ""
		tagFilter := ""
		groupFilter := ""

		for i := 0; i < len(args); i++ {
			a := args[i]
			if strings.HasPrefix(a, "--domain=") {
				domainFilter = strings.TrimPrefix(a, "--domain=")
			} else if strings.HasPrefix(a, "--tag=") {
				tagFilter = strings.TrimPrefix(a, "--tag=")
			} else if strings.HasPrefix(a, "--group=") {
				groupFilter = strings.TrimPrefix(a, "--group=")
			} else if strings.HasPrefix(a, "-g=") {
				groupFilter = strings.TrimPrefix(a, "-g=")
			} else if a == "-g" || a == "--group" {
				if i+1 < len(args) {
					groupFilter = args[i+1]
					i++
				}
			} else if a == "group" {
				if i+1 < len(args) {
					groupFilter = args[i+1]
					i++
				}
			} else if !strings.HasPrefix(a, "-") && groupFilter == "" {
				groupFilter = a
			}
		}

		if groupFilter != "" {
			groupPersonas, grp, err := mgr.ListGroup(groupFilter, domainFilter, tagFilter)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila persona list: %v\n", err)
				os.Exit(1)
			}

			if jsonOut {
				emitJSON(map[string]interface{}{
					"group":    grp,
					"personas": groupPersonas,
					"count":    len(groupPersonas),
				})
				return
			}

			if toonOut {
				fmt.Fprintf(os.Stdout, "group[%d]{id,slug,name,personas_count}:\n", 1)
				fmt.Fprintf(os.Stdout, "  %d  %s  %q  %d\n\n", grp.ID, grp.Slug, grp.Name, len(groupPersonas))
				fmt.Fprintf(os.Stdout, "personas[%d]{num_id,group_id,id,domain,name,skills_count,usage}:\n", len(groupPersonas))
				for _, p := range groupPersonas {
					fmt.Fprintf(os.Stdout, "  %d  %d  %s  %s  %q  %d  %d\n", p.NumID, p.GroupID, p.ID, p.Domain, p.Name, len(p.SkillsInvoked), p.UsageCount)
				}
				return
			}

			fmt.Fprintf(os.Stdout, "%s sila · personas in Group %d: %s (slug: %s, %d registered)\n",
				ui.Cyan("·"), grp.ID, ui.Bold(grp.Name), ui.Cyan(grp.Slug), len(groupPersonas))
			fmt.Fprintf(os.Stdout, "  %s\n\n", ui.Dim(grp.Description))

			var numIDs []string
			for _, p := range groupPersonas {
				numIDs = append(numIDs, fmt.Sprintf("%d", p.NumID))
				skillsStr := ui.Dim("none (prompt only)")
				if len(p.SkillsInvoked) > 0 {
					skillsStr = ui.Cyan(strings.Join(p.SkillsInvoked, ", "))
				}
				tagsStr := ""
				if len(p.Tags) > 0 {
					tagsStr = ui.Dim("[" + strings.Join(p.Tags, ", ") + "]")
				}

				fmt.Fprintf(os.Stdout, "    %s %s %s %s\n",
					ui.Bold(fmt.Sprintf("[%d]", p.NumID)),
					ui.Bold(p.ID),
					ui.Dim("("+p.Domain+")"),
					tagsStr,
				)
				fmt.Fprintf(os.Stdout, "      %s\n", p.Name)
				fmt.Fprintf(os.Stdout, "      Skills: %s | Usage: %d | Approval: %s\n\n", skillsStr, p.UsageCount, p.ApprovalRate)
			}

			fmt.Fprintf(os.Stdout, "%s\n", ui.Bold("Hire This Group:"))
			fmt.Fprintf(os.Stdout, "  sila hire group %d                  # Hire group by numeric ID\n", grp.ID)
			fmt.Fprintf(os.Stdout, "  sila hire group %s        # Hire group by slug\n", grp.Slug)
			fmt.Fprintf(os.Stdout, "  sila hire %s             # Hire mesh by persona numeric IDs\n", strings.Join(numIDs, ","))
			fmt.Fprintf(os.Stdout, "  sila hire group %d <url>            # Run audit directly on target URL\n", grp.ID)
			fmt.Fprintf(os.Stdout, "  sila hire group %d --prompt-only    # Generate master groupthink prompt\n\n", grp.ID)
			return
		}

		personas, err := mgr.List(domainFilter, tagFilter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila persona list: %v\n", err)
			os.Exit(1)
		}

		grouped := persona.GroupPersonas(personas)

		if jsonOut {
			emitJSON(map[string]interface{}{
				"groups":   grouped,
				"personas": personas,
				"count":    len(personas),
			})
			return
		}

		if toonOut {
			fmt.Fprintf(os.Stdout, "groups[%d]{id,slug,name,personas_count}:\n", len(grouped))
			for _, g := range grouped {
				fmt.Fprintf(os.Stdout, "  %d  %s  %q  %d\n", g.Group.ID, g.Group.Slug, g.Group.Name, len(g.Personas))
			}
			fmt.Fprintf(os.Stdout, "\npersonas[%d]{num_id,group_id,id,domain,name,skills_count,usage}:\n", len(personas))
			for _, p := range personas {
				fmt.Fprintf(os.Stdout, "  %d  %d  %s  %s  %q  %d  %d\n", p.NumID, p.GroupID, p.ID, p.Domain, p.Name, len(p.SkillsInvoked), p.UsageCount)
			}
			return
		}

		fmt.Fprintf(os.Stdout, "%s sila · personas (%d registered across %d groups)\n\n", ui.Cyan("·"), len(personas), len(grouped))
		for _, g := range grouped {
			fmt.Fprintf(os.Stdout, "%s [Group %d] %s %s\n",
				ui.Cyan("▶"), g.Group.ID, ui.Bold(g.Group.Name), ui.Dim("(slug: "+g.Group.Slug+")"))
			fmt.Fprintf(os.Stdout, "  %s\n\n", ui.Dim(g.Group.Description))

			for _, p := range g.Personas {
				skillsStr := ui.Dim("none (prompt only)")
				if len(p.SkillsInvoked) > 0 {
					skillsStr = ui.Cyan(strings.Join(p.SkillsInvoked, ", "))
				}
				tagsStr := ""
				if len(p.Tags) > 0 {
					tagsStr = ui.Dim("[" + strings.Join(p.Tags, ", ") + "]")
				}

				fmt.Fprintf(os.Stdout, "    %s %s %s %s\n",
					ui.Bold(fmt.Sprintf("[%d]", p.NumID)),
					ui.Bold(p.ID),
					ui.Dim("("+p.Domain+")"),
					tagsStr,
				)
				fmt.Fprintf(os.Stdout, "      %s\n", p.Name)
				fmt.Fprintf(os.Stdout, "      Skills: %s | Usage: %d | Approval: %s\n\n", skillsStr, p.UsageCount, p.ApprovalRate)
			}
		}

		fmt.Fprintf(os.Stdout, "%s\n", ui.Bold("Hire Personas for Audit:"))
		fmt.Fprintf(os.Stdout, "  sila hire group 1                   # Hire entire group by ID (#1 or 'agent-ready')\n")
		fmt.Fprintf(os.Stdout, "  sila hire group agent-ready         # Hire entire group by slug\n")
		fmt.Fprintf(os.Stdout, "  sila hire 1,4,6,3,8                 # Hire custom mesh of personas by numeric ID\n")
		fmt.Fprintf(os.Stdout, "  sila hire 1,4,6,3,8 <url>           # Run audit immediately on target URL\n")
		fmt.Fprintf(os.Stdout, "  sila hire group 1 --prompt-only     # Generate master groupthink prompt for agents\n\n")

	case "get", "view", "show":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "sila persona get: specify persona id or number (e.g. sila persona get 1 or sila persona get accessibility-sentinel)")
			os.Exit(2)
		}
		id := args[0]
		p, err := mgr.Get(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila persona get: %v\n", err)
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(p)
			return
		}

		fmt.Fprintf(os.Stdout, "%s sila · persona [%d] %s\n\n", ui.Cyan("·"), p.NumID, ui.Bold(p.ID))
		fmt.Fprintf(os.Stdout, "  Name:     %s\n", p.Name)
		fmt.Fprintf(os.Stdout, "  Group:    [%d] %s (%s)\n", p.GroupID, p.GroupName, ui.Dim(p.GroupSlug))
		fmt.Fprintf(os.Stdout, "  Domain:   %s\n", ui.Cyan(p.Domain))
		fmt.Fprintf(os.Stdout, "  Tags:     %s\n", strings.Join(p.Tags, ", "))
		fmt.Fprintf(os.Stdout, "  Skills:   %s\n", strings.Join(p.SkillsInvoked, ", "))
		fmt.Fprintf(os.Stdout, "  Usage:    %d runs (%s approval)\n", p.UsageCount, p.ApprovalRate)
		if len(p.NegativeLessons) > 0 {
			fmt.Fprintf(os.Stdout, "  Learned Anti-patterns (%d):\n", len(p.NegativeLessons))
			for _, l := range p.NegativeLessons {
				fmt.Fprintf(os.Stdout, "    - %s\n", ui.Dim(l))
			}
		}
		fmt.Fprintf(os.Stdout, "\n  %s\n", ui.Bold("System Stance:"))
		fmt.Fprintf(os.Stdout, "  %s\n\n", p.SystemPrompt)
		fmt.Fprintf(os.Stdout, "  %s\n", ui.Bold("Evaluation Rubric:"))
		fmt.Fprintf(os.Stdout, "%s\n", p.EvaluationRubric)

	case "add", "create":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "sila persona add: specify json file path or params")
			os.Exit(2)
		}

		var p persona.Persona
		filePath := args[0]
		if strings.HasSuffix(filePath, ".json") {
			data, err := os.ReadFile(filePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila persona add: read file: %v\n", err)
				os.Exit(1)
			}
			if err := json.Unmarshal(data, &p); err != nil {
				fmt.Fprintf(os.Stderr, "sila persona add: parse json: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Fprintln(os.Stderr, "sila persona add: pass a valid .json persona definition file")
			os.Exit(2)
		}

		if err := mgr.Upsert(p); err != nil {
			fmt.Fprintf(os.Stderr, "sila persona add: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "%s Successfully registered persona %s (%s)\n", ui.Green("✔"), ui.Bold(p.ID), p.Name)

	case "sync":
		remoteURL := os.Getenv("SILA_EDGE_URL")
		token := os.Getenv("SILA_EDGE_AUTH_TOKEN")
		for _, a := range args {
			if strings.HasPrefix(a, "--remote=") {
				remoteURL = strings.TrimPrefix(a, "--remote=")
			} else if strings.HasPrefix(a, "--token=") {
				token = strings.TrimPrefix(a, "--token=")
			}
		}

		if remoteURL == "" {
			fmt.Fprintln(os.Stderr, "sila persona sync: remote edge URL required (--remote=https://... or SILA_EDGE_URL)")
			os.Exit(2)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		synced, err := mgr.SyncFromRemote(ctx, remoteURL, token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila persona sync error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "%s Successfully synchronized %d personas from Cloudflare D1 (%s)\n", ui.Green("✔"), synced, remoteURL)

	default:
		fmt.Fprintf(os.Stderr, "sila persona: unknown subcommand %q\n", sub)
		fmt.Fprintln(os.Stdout, "Usage: sila persona [list|get|add|sync] [options]")
		os.Exit(2)
	}
}
