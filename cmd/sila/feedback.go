package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/feedback"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runFeedback(ui render.UI, dbPath, project, agent, category, status, goal, note, customID string, jsonOut bool, top int, openOnly, closedOnly, issueFlag, shareFlag, yesConfirm, noOpen bool, args []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila feedback: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	var cleanArgs []string
	remoteURL := os.Getenv("SILA_EDGE_URL")
	token := os.Getenv("SILA_EDGE_AUTH_TOKEN")
	if cfg, _ := config.Load(""); cfg != nil {
		if token == "" && cfg.CloudToken != "" {
			token = cfg.CloudToken
		}
		if remoteURL == "" && cfg.CloudEndpoint != "" {
			remoteURL = cfg.CloudEndpoint
		}
	}
	for _, a := range args {
		switch {
		case a == "--issue" || a == "--github" || a == "--gh":
			issueFlag = true
		case a == "--share" || a == "--edge" || a == "--d1":
			shareFlag = true
		case a == "--yes" || a == "-y":
			yesConfirm = true
		case a == "--no-open":
			noOpen = true
		case strings.HasPrefix(a, "--id="):
			customID = strings.TrimPrefix(a, "--id=")
		case strings.HasPrefix(a, "--remote="):
			remoteURL = strings.TrimPrefix(a, "--remote=")
		case strings.HasPrefix(a, "--token="):
			token = strings.TrimPrefix(a, "--token=")
		default:
			cleanArgs = append(cleanArgs, a)
		}
	}
	args = cleanArgs

	if openOnly {
		status = "open"
	} else if closedOnly {
		status = "closed"
	}

	if len(args) > 0 {
		subcmd := strings.ToLower(args[0])
		switch subcmd {
		case "open":
			status = "open"
			args = args[1:]

		case "closed":
			status = "closed"
			args = args[1:]

		case "resolve", "close":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: sila feedback resolve <id> [--goal=<slug>] [--note=\"...\"] [--agent=<name>]")
				os.Exit(2)
			}
			id := args[1]
			effAgent := agent
			if effAgent == "" {
				effAgent = "agent"
			}
			effNote := note
			if effNote == "" && len(args) > 2 {
				effNote = strings.Join(args[2:], " ")
			}
			if err := st.ResolveFeedback(id, effAgent, goal, effNote); err != nil {
				if jsonOut {
					emitJSON(map[string]any{"error": err.Error(), "id": id, "resolved": false})
					return
				}
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("feedback resolve failed: %v", err), "sila feedback list"))
				os.Exit(1)
			}
			fb, err := st.GetFeedback(id)
			if err != nil {
				if jsonOut {
					emitJSON(map[string]any{"error": err.Error(), "id": id, "resolved": false})
					return
				}
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("feedback resolve failed: %v", err), "sila feedback list"))
				os.Exit(1)
			}
			if jsonOut {
				emitJSON(map[string]any{"resolved": true, "feedback": fb})
				return
			}
			fmt.Fprintln(os.Stdout, ui.Green("✔")+" Feedback closed & resolved: "+ui.Bold(fb.ID))
			if fb.ResolvedGoal != "" {
				fmt.Fprintf(os.Stdout, "  Goal:   %s\n", ui.Cyan(fb.ResolvedGoal))
			}
			if fb.ResolvedBy != "" {
				fmt.Fprintf(os.Stdout, "  Agent:  %s\n", ui.Bold(fb.ResolvedBy))
			}
			if fb.ResolutionNote != "" {
				fmt.Fprintf(os.Stdout, "  Note:   %s\n", fb.ResolutionNote)
			}
			return

		case "reopen":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: sila feedback reopen <id>")
				os.Exit(2)
			}
			id := args[1]
			if err := st.ReopenFeedback(id); err != nil {
				if jsonOut {
					emitJSON(map[string]any{"error": err.Error(), "id": id, "reopened": false})
					return
				}
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("feedback reopen failed: %v", err), "sila feedback list"))
				os.Exit(1)
			}
			fb, err := st.GetFeedback(id)
			if err != nil {
				if jsonOut {
					emitJSON(map[string]any{"error": err.Error(), "id": id, "reopened": false})
					return
				}
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("feedback reopen failed: %v", err), "sila feedback list"))
				os.Exit(1)
			}
			if jsonOut {
				emitJSON(map[string]any{"reopened": true, "feedback": fb})
				return
			}
			fmt.Fprintln(os.Stdout, ui.Yellow("↺")+" Feedback reopened: "+ui.Bold(fb.ID))
			return

		case "submit", "issue":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: sila feedback submit <id> [--yes] [--no-open]")
				os.Exit(2)
			}
			id := args[1]
			fb, err := st.GetFeedback(id)
			if err != nil || fb == nil {
				if jsonOut {
					emitJSON(map[string]any{"error": "feedback not found: " + id, "submitted": false})
					return
				}
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("feedback %q not found", id), "sila feedback list"))
				os.Exit(1)
			}
			dispatchFeedbackToGitHub(ui, st, fb, yesConfirm, noOpen, jsonOut)
			return

		case "share":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: sila feedback share <id> [--remote=https://...]")
				os.Exit(2)
			}
			id := args[1]
			for _, a := range args[2:] {
				if strings.HasPrefix(a, "--remote=") {
					remoteURL = strings.TrimPrefix(a, "--remote=")
				}
			}
			fb, err := st.GetFeedback(id)
			if err != nil || fb == nil {
				if jsonOut {
					emitJSON(map[string]any{"error": "feedback not found: " + id, "shared": false})
					return
				}
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("feedback %q not found", id), "sila feedback list"))
				os.Exit(1)
			}
			dispatchFeedbackToEdge(ui, st, fb, remoteURL, jsonOut)
			return

		case "drain", "pull":
			limit := top
			if limit <= 0 {
				limit = 50
			}
			for _, a := range args[1:] {
				switch {
				case strings.HasPrefix(a, "--limit="):
					if val, err := strconv.Atoi(strings.TrimPrefix(a, "--limit=")); err == nil && val > 0 {
						limit = val
					}
				case strings.HasPrefix(a, "--token="):
					token = strings.TrimPrefix(a, "--token=")
				case strings.HasPrefix(a, "--remote="):
					remoteURL = strings.TrimPrefix(a, "--remote=")
				}
			}
			if token == "" {
				if jsonOut {
					emitJSON(map[string]any{"error": "maintainer authentication token required (configure via 'sila config set edge_token <token>' or SILA_EDGE_AUTH_TOKEN)", "drained": 0})
					return
				}
				fmt.Fprintln(os.Stderr, "sila feedback drain: maintainer authentication token required")
				fmt.Fprintln(os.Stderr, "  Only the project developer/maintainer can drain community feedback from Cloudflare D1.")
				fmt.Fprintln(os.Stderr, "  To authenticate, save your edge token in user config:")
				fmt.Fprintln(os.Stderr, "    sila config set edge_token <token>")
				fmt.Fprintln(os.Stderr, "  Or pass it via environment variable:")
				fmt.Fprintln(os.Stderr, "    export SILA_EDGE_AUTH_TOKEN=<token>")
				os.Exit(2)
			}

			drainURL := feedback.ResolveDrainURL(remoteURL)
			ackURL := feedback.ResolveAckURL(remoteURL)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			drainRes, err := feedback.DrainFromEdge(ctx, drainURL, token, limit)
			if err != nil {
				if jsonOut {
					emitJSON(map[string]any{"error": err.Error(), "drained": 0})
					return
				}
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("feedback drain failed: %v", err), "sila feedback drain --help"))
				os.Exit(1)
			}

			if len(drainRes.Items) == 0 {
				if jsonOut {
					emitJSON(map[string]any{"drained": 0, "acknowledged": 0, "items": []any{}})
					return
				}
				fmt.Fprintln(os.Stdout, ui.Cyan("sila · feedback drain")+" — Cloudflare D1 Edge Inbox")
				fmt.Fprintln(os.Stdout, ui.Dim("No pending feedback items in edge queue."))
				return
			}

			var ackIDs []string
			var savedItems []store.Feedback
			for _, item := range drainRes.Items {
				note := fmt.Sprintf("Cloudflare D1 edge inbox (os: %s, arch: %s, version: %s)", item.OS, item.Arch, item.SilaVersion)
				projID := "edge"
				if item.ProjectHash != "" {
					projID = "edge:" + item.ProjectHash
				}
				fbItem := &store.Feedback{
					ID:             item.ID,
					ProjectID:      projID,
					Agent:          item.Agent,
					Category:       item.Category,
					Content:        item.Content,
					Status:         "open",
					CreatedAt:      item.CreatedAt,
					ResolutionNote: note,
				}
				_ = st.SaveFeedback(fbItem)
				ackIDs = append(ackIDs, item.ID)
				savedItems = append(savedItems, *fbItem)
			}

			ackedCount, err := feedback.AcknowledgeEdgeFeedback(ctx, ackURL, token, ackIDs, drainRes.SessionToken)
			if err != nil {
				if jsonOut {
					emitJSON(map[string]any{
						"error":        fmt.Sprintf("failed to acknowledge drained items: %v", err),
						"drained":      len(savedItems),
						"acknowledged": 0,
						"items":        savedItems,
					})
					return
				}
				fmt.Fprintf(os.Stderr, "sila feedback drain: warning: acknowledgment failed: %v\n", err)
			}

			if jsonOut {
				emitJSON(map[string]any{
					"drained":      len(savedItems),
					"acknowledged": ackedCount,
					"session":      drainRes.SessionToken,
					"items":        savedItems,
				})
				return
			}

			fmt.Fprintln(os.Stdout, ui.Green("✔")+fmt.Sprintf(" Drained & acknowledged %d feedback items from Cloudflare D1 into local store", len(savedItems)))
			for _, it := range savedItems {
				fmt.Fprintf(os.Stdout, "  %s  %s  %s\n", feedback.StatusBadge(ui, it.Status), feedback.CategoryBadge(ui, it.Category), ui.Dim(it.ID))
				fmt.Fprintf(os.Stdout, "    %s\n", it.Content)
			}
			return

		case "list":
			// Drop the "list" command and proceed to query
			args = args[1:]
			if len(args) > 0 {
				sub := strings.ToLower(args[0])
				if sub == "open" || sub == "closed" {
					status = sub
					args = args[1:]
				}
			}

		default:
			// If args contains flags like --open or --closed, or matches filter keywords, don't record
			isFilter := false
			for _, a := range args {
				if a == "--open" || a == "--closed" || a == "open" || a == "closed" {
					isFilter = true
					break
				}
			}
			if isFilter {
				for _, a := range args {
					if a == "--open" || a == "open" {
						status = "open"
					} else if a == "--closed" || a == "closed" {
						status = "closed"
					}
				}
			} else {
				// If args[0] doesn't look like a subcommand or filter, record new feedback
				content := strings.Join(args, " ")
				cat := feedback.NormalizeCategory(category)
				if agent == "" {
					agent = "agent"
				}
				resolvedProject := project
				if resolvedProject == "" {
					if p, found, err := st.ProjectForDir(""); err == nil && found && p.ID != "" {
						resolvedProject = p.ID
					}
				} else if p, err := st.ResolveProject(resolvedProject); err == nil && p.ID != "" {
					resolvedProject = p.ID
				}
				fb := &store.Feedback{
					ID:        customID,
					ProjectID: resolvedProject,
					Agent:     agent,
					Category:  cat,
					Content:   content,
				}
				if err := st.SaveFeedback(fb); err != nil {
					if jsonOut {
						emitJSON(map[string]any{"error": err.Error(), "created": false})
						return
					}
					fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("feedback write failed: %v", err), "sila feedback --help"))
					os.Exit(1)
				}
				if shareFlag {
					dispatchFeedbackToEdge(ui, st, fb, remoteURL, jsonOut)
					if !issueFlag {
						return
					}
				}
				if issueFlag {
					dispatchFeedbackToGitHub(ui, st, fb, yesConfirm, noOpen, jsonOut)
					return
				}
				if jsonOut {
					emitJSON(fb)
					return
				}
				fmt.Fprint(os.Stdout, render.RenderCard(ui, render.Card{
					Verb: "Feedback recorded",
					ID:   fb.ID,
					Fields: []render.Field{
						{Label: "Type", Value: cat},
						{Label: "Agent", Value: fb.Agent},
						{Label: "Content", Value: strings.ReplaceAll(fb.Content, "\n", " ")},
						{Label: "Project", Value: cardProjectLabel(st, fb.ProjectID)},
					},
				}))
				return
			}
		}
	}

	for _, a := range args {
		if a == "--open" {
			status = "open"
		} else if a == "--closed" {
			status = "closed"
		}
	}

	limit := top
	if limit <= 0 {
		limit = 50
	}
	filterCat := ""
	if category != "idea" {
		filterCat = feedback.NormalizeCategory(category)
	}
	items, err := st.QueryFeedback(store.FeedbackFilter{
		ProjectID: project,
		Category:  filterCat,
		Status:    status,
		Limit:     limit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila feedback: %v\n", err)
		os.Exit(1)
	}
	stats, _ := st.FeedbackStats()
	statusStats, _ := st.FeedbackStatusStats()

	if jsonOut {
		emitJSON(struct {
			Total       int              `json:"total"`
			Stats       map[string]int   `json:"stats"`
			StatusStats map[string]int   `json:"status_stats"`
			Feedback    []store.Feedback `json:"feedback"`
		}{
			Total:       len(items),
			Stats:       stats,
			StatusStats: statusStats,
			Feedback:    items,
		})
		return
	}

	feedback.PrintFeedbackLedger(ui, items, stats, statusStats, status)
}

// dispatchFeedbackToGitHub handles user confirmation, payload formatting,
// upstream GitHub issue submission, and local SQLite feedback note linking.
func dispatchFeedbackToGitHub(ui render.UI, st *store.Store, fb *store.Feedback, yesConfirm, noOpen, jsonOut bool) {
	title, body, labels := feedback.FormatGitHubIssue(fb.Category, fb.Content, fb.ProjectID)

	if !yesConfirm && !jsonOut {
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("▲ GitHub Issue Submission Preview")))
		fmt.Fprintf(os.Stdout, "  %-14s %s\n", "Repository:", ui.Bold(feedback.DefaultGitHubRepo))
		fmt.Fprintf(os.Stdout, "  %-14s %s\n", "Title:", title)
		fmt.Fprintf(os.Stdout, "  %-14s %s\n", "Category:", feedback.CategoryBadge(ui, fb.Category))
		fmt.Fprintf(os.Stdout, "  %-14s %s\n", "Labels:", strings.Join(labels, ", "))
		fmt.Fprintf(os.Stdout, "  %-14s %s\n", "Payload:", ui.Dim("sanitized, local usernames & secrets stripped"))
		fmt.Fprintln(os.Stdout)
		fmt.Fprint(os.Stdout, "Submit feedback upstream to GitHub Issues? [y/N]: ")

		var response string
		fmt.Scanln(&response)
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Fprintln(os.Stdout, ui.Yellow("Submission cancelled.")+" Local feedback retained: "+ui.Bold(fb.ID))
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := feedback.DispatchToGitHub(ctx, feedback.DefaultGitHubRepo, title, body, labels, !noOpen)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "submitted": false, "id": fb.ID})
			return
		}
		fmt.Fprintf(os.Stderr, "sila feedback: failed to dispatch issue: %v\n", err)
		return
	}

	if res != nil && res.IssueURL != "" {
		note := "GitHub Issue: " + res.IssueURL
		if fb.ResolutionNote != "" && !strings.Contains(fb.ResolutionNote, res.IssueURL) {
			note = fb.ResolutionNote + " · " + note
		}
		_ = st.UpdateFeedbackNote(fb.ID, note)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"submitted":    true,
			"feedback_id":  fb.ID,
			"github_issue": res,
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Green("✔")+" Feedback submitted upstream to GitHub Issues")
	fmt.Fprintf(os.Stdout, "  Local ID:     %s\n", ui.Dim(fb.ID))
	fmt.Fprintf(os.Stdout, "  Issue URL:    %s\n", ui.Cyan(ui.Bold(res.IssueURL)))
	fmt.Fprintf(os.Stdout, "  Method:       %s\n", ui.Dim(res.Detail))
}

// dispatchFeedbackToEdge submits sanitized feedback anonymously to the Cloudflare D1 edge.
func dispatchFeedbackToEdge(ui render.UI, st *store.Store, fb *store.Feedback, remoteURL string, jsonOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	endpoint := feedback.ResolveFeedbackURL(remoteURL)
	res, err := feedback.SubmitToEdge(ctx, endpoint, fb)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "shared": false, "id": fb.ID})
			return
		}
		fmt.Fprintf(os.Stderr, "sila feedback: warning: failed to share with edge: %v\n", err)
		return
	}

	if res != nil && res.ID != "" {
		note := "Shared to Sila edge: " + res.ID
		if fb.ResolutionNote != "" && !strings.Contains(fb.ResolutionNote, res.ID) {
			note = fb.ResolutionNote + " · " + note
		}
		_ = st.UpdateFeedbackNote(fb.ID, note)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"shared":      true,
			"feedback_id": fb.ID,
			"remote_id":   res.ID,
			"status":      res.Status,
			"message":     res.Message,
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Green("✔")+" Feedback recorded & shared anonymously with Sila maintainer: "+feedback.CategoryBadge(ui, fb.Category))
	fmt.Fprintf(os.Stdout, "  Local ID:  %s\n", ui.Dim(fb.ID))
	fmt.Fprintf(os.Stdout, "  Remote ID: %s\n", ui.Cyan(ui.Bold(res.ID)))
	if fb.Agent != "" {
		fmt.Fprintf(os.Stdout, "  Agent:     %s\n", ui.Bold(fb.Agent))
	}
	fmt.Fprintf(os.Stdout, "  Content:   %s\n", fb.Content)
}
