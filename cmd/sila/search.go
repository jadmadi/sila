package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func omniSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func runSearch(ui render.UI, dbPath, project, tool, role string, sinceTime time.Time, omni, semantic, twoPass bool, window, occurrence int, refsExistOnly, staleOnly, factsOnly, jsonOut, toonOut, verbose bool, top int, terms []string) {
	if len(terms) == 0 {
		fmt.Fprintln(os.Stderr, "sila search: specify search terms (e.g. sila search sqlite, sila search expand <id>, or sila search --omni <terms>)")
		os.Exit(2)
	}
	st, _, err := openReadStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	// 1. Expand message context mode (sila search expand <id> [--window=3] [--occurrence=N])
	if len(terms) >= 2 && (terms[0] == "expand" || terms[0] == "context") {
		var id int64
		if _, err := fmt.Sscanf(terms[1], "%d", &id); err != nil || id <= 0 {
			fmt.Fprintf(os.Stderr, "sila search expand: invalid message id %q (must be positive integer)\n", terms[1])
			os.Exit(2)
		}
		msgCtx, err := st.MessageInContext(id, window, occurrence)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				message := fmt.Sprintf("no message with id %d", id)
				fix := `find ids with: sila search "<query>"`
				if jsonOut {
					emitJSON(map[string]any{"error": map[string]any{
						"code": "message_not_found", "message": message, "fix": fix,
					}})
					os.Exit(1)
				}
				fmt.Fprint(os.Stderr, render.RenderError(ui, "sila search expand: "+message, fix))
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "sila search expand: %s\n", friendlyStoreError(err))
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(msgCtx)
			return
		}
		if toonOut {
			fields := [][2]string{
				{"id", fmt.Sprintf("%d", id)},
				{"project", msgCtx.TargetMessage.ProjectID},
				{"role", msgCtx.TargetMessage.Role},
				{"tools", strings.Join(msgCtx.TargetMessage.SourceTools, ",")},
				{"time", msgCtx.TargetMessage.TS},
				{"content", strings.ReplaceAll(msgCtx.TargetMessage.Content, "\n", " ")},
			}
			fmt.Fprint(os.Stdout, render.FormatTOONObject("expanded_message", fields))
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("sila · search expand #%d (window: ±%d turns · project: %s)", id, msgCtx.Window, msgCtx.TargetMessage.ProjectID))))
		if msgCtx.SessionID != "" {
			fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("session: %s", msgCtx.SessionID)))
		}
		if msgCtx.OccurrenceCount > 1 {
			fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("this message has %d recorded source occurrences; showing #%d (select with --occurrence=N)", msgCtx.OccurrenceCount, msgCtx.SelectedOccurrence)))
		}
		if msgCtx.Note != "" {
			fmt.Fprintln(os.Stdout, ui.Yellow(ui.Bold("note: "))+msgCtx.Note)
		}
		fmt.Fprintln(os.Stdout)
		if len(msgCtx.Before) > 0 {
			fmt.Fprintln(os.Stdout, ui.Dim("--- PRECEDING CONTEXT ---"))
			for _, m := range msgCtx.Before {
				fmt.Fprintf(os.Stdout, "  [%s · %s · %s]\n  %s\n\n",
					ui.Dim(m.TS), ui.Bold(m.Role), ui.Dim(strings.Join(m.SourceTools, ",")), strings.ReplaceAll(m.Content, "\n", "\n  "))
			}
		}
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Yellow(">>> TARGET MESSAGE <<<")))
		fmt.Fprintf(os.Stdout, "  [%s · %s · %s]\n  %s\n\n",
			ui.Dim(msgCtx.TargetMessage.TS), ui.Bold(ui.Yellow(msgCtx.TargetMessage.Role)),
			ui.Dim(strings.Join(msgCtx.TargetMessage.SourceTools, ",")),
			strings.ReplaceAll(msgCtx.TargetMessage.Content, "\n", "\n  "))
		if len(msgCtx.After) > 0 {
			fmt.Fprintln(os.Stdout, ui.Dim("--- FOLLOWING CONTEXT ---"))
			for _, m := range msgCtx.After {
				fmt.Fprintf(os.Stdout, "  [%s · %s · %s]\n  %s\n\n",
					ui.Dim(m.TS), ui.Bold(m.Role), ui.Dim(strings.Join(m.SourceTools, ",")), strings.ReplaceAll(m.Content, "\n", "\n  "))
			}
		}
		return
	}

	// 2. Claims search mode (sila search claims <terms> or sila search facts <terms>)
	if len(terms) >= 1 && (terms[0] == "claims" || terms[0] == "facts") {
		runDreamQuery(ui, st, project, tool, refsExistOnly, staleOnly, factsOnly, jsonOut, toonOut, verbose, top, terms[1:])
		return
	}

	// 3. Docs search mode (sila search docs <terms> or sila search doc <terms>)
	if len(terms) >= 1 && (terms[0] == "docs" || terms[0] == "doc") {
		runSearchDocs(ui, st, project, jsonOut, toonOut, verbose, top, terms[1:])
		return
	}

	// 4. Literal recovery mode (sila search literal <source-id> <start> <end> [doc|message|file])
	if len(terms) >= 1 && (terms[0] == "literal" || terms[0] == "byte") {
		if len(terms) < 4 {
			fmt.Fprintln(os.Stderr, "sila search literal: usage: sila search literal <source-id> <byte-start> <byte-end> [doc|message|file]")
			os.Exit(2)
		}
		sourceID := terms[1]
		var start, end int64
		if _, err := fmt.Sscanf(terms[2], "%d", &start); err != nil || start < 0 {
			fmt.Fprintf(os.Stderr, "sila search literal: invalid start offset %q (must be non-negative integer)\n", terms[2])
			os.Exit(2)
		}
		if _, err := fmt.Sscanf(terms[3], "%d", &end); err != nil || end < start {
			fmt.Fprintf(os.Stderr, "sila search literal: invalid end offset %q (must be integer >= start)\n", terms[3])
			os.Exit(2)
		}
		sourceType := "doc"
		if len(terms) >= 5 {
			sourceType = terms[4]
		} else {
			if strings.HasPrefix(sourceID, "doc_") {
				sourceType = "doc"
			} else if strings.HasPrefix(sourceID, "msg_") || strings.HasPrefix(sourceID, "msg:") {
				sourceType = "message"
				sourceID = strings.TrimPrefix(strings.TrimPrefix(sourceID, "msg_"), "msg:")
			} else if _, err := strconv.ParseInt(sourceID, 10, 64); err == nil {
				sourceType = "message"
			} else if strings.Contains(sourceID, ".") || strings.Contains(sourceID, "/") {
				sourceType = "file"
			}
		}

		cwd, _ := os.Getwd()
		extracted, err := st.ExtractLiteral(cwd, project, sourceType, sourceID, start, end)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila search literal: %v\n", err)
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(map[string]any{
				"source_id":   sourceID,
				"source_type": sourceType,
				"byte_start":  start,
				"byte_end":    end,
				"literal":     extracted,
				"bytes":       len([]byte(extracted)),
			})
			return
		}
		if toonOut {
			fields := [][2]string{
				{"source_id", sourceID},
				{"source_type", sourceType},
				{"byte_start", fmt.Sprintf("%d", start)},
				{"byte_end", fmt.Sprintf("%d", end)},
				{"literal", strings.ReplaceAll(extracted, "\n", "\\n")},
				{"bytes", fmt.Sprintf("%d", len([]byte(extracted)))},
			}
			fmt.Fprint(os.Stdout, render.FormatTOONObject("byte_literal", fields))
			return
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("sila · search literal [%d:%d] from %s (%s)", start, end, sourceID, sourceType))))
		fmt.Fprintf(os.Stdout, "  Bytes: %d\n", len([]byte(extracted)))
		fmt.Fprintln(os.Stdout, ui.Dim("---------------------------------------------------------------"))
		fmt.Fprintln(os.Stdout, extracted)
		fmt.Fprintln(os.Stdout, ui.Dim("---------------------------------------------------------------"))
		return
	}

	// 5. Lineage / Assertions mode (sila search lineage <id|terms...> or sila search assertions [id])
	if len(terms) >= 1 && (terms[0] == "lineage" || terms[0] == "assertions" || terms[0] == "assertion") {
		runSearchLineage(ui, st, project, refsExistOnly, jsonOut, toonOut, verbose, top, terms[1:])
		return
	}

	// 6. Omni-Search mode (sila search --omni <terms> or sila search omni <terms>)
	if omni || (len(terms) > 0 && terms[0] == "omni") {
		queryTerms := terms
		if len(terms) > 0 && terms[0] == "omni" {
			queryTerms = terms[1:]
		}
		query := strings.Join(queryTerms, " ")
		if query == "" {
			fmt.Fprintln(os.Stderr, "sila search omni: specify search terms")
			os.Exit(2)
		}
		// Forward the filters the CLI already accepted: silently dropping them
		// made an unsupported combination look like "no such evidence".
		res, err := dream.OmniSearchFiltered(st, query, dream.OmniOptions{
			Project:  project,
			Limit:    top,
			Tool:     tool,
			Role:     role,
			Since:    omniSince(sinceTime),
			Semantic: semantic,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila search: %v\n", err)
			os.Exit(1)
		}
		// A tier that failed must be visible: returning zero hits silently is
		// indistinguishable from having no evidence.
		for _, w := range res.Warnings {
			fmt.Fprintln(os.Stderr, ui.Yellow("warning: ")+w)
		}
		if jsonOut {
			emitJSON(res)
			return
		}
		if len(res.Items) == 0 {
			if toonOut {
				fmt.Print("omni[0]{type,project,title,content}:\n")
			} else {
				fmt.Fprintf(os.Stdout, "search: 0 omni-search results matching %q %s\n", query, scopeLabel(project))
				if !outModes.quiet {
					fmt.Fprintln(os.Stdout, "help: try standard search with 'sila search <terms>'")
				}
			}
			return
		}
		if toonOut {
			rows := make([][]string, len(res.Items))
			for i, it := range res.Items {
				content := it.Content
				if !verbose {
					content = trunc(strings.ReplaceAll(content, "\n", " "), 100)
				}
				rows[i] = []string{string(it.Type), it.ProjectID, it.Title, content}
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("omni", []string{"type", "project", "title", "content"}, rows))
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · omni-search"))+ui.Dim(fmt.Sprintf(" (query: %q · %d results)", query, len(res.Items))))
		fmt.Fprintln(os.Stdout)
		for i, it := range res.Items {
			typeBadge := string(it.Type)
			switch it.Type {
			case dream.OmniTypeInvariant:
				typeBadge = ui.Bold(ui.Cyan("[INVARIANT]"))
			case dream.OmniTypeFact:
				typeBadge = ui.Green("[FACT]")
			case dream.OmniTypeCommit:
				typeBadge = ui.Yellow("[COMMIT]")
			case dream.OmniTypeMessage:
				typeBadge = ui.Dim("[MSG]")
			case dream.OmniTypeDoc:
				typeBadge = ui.Bold(ui.Paint(render.Magenta, "[DOC]"))
			}
			header := fmt.Sprintf("%2d. %s %s", i+1, typeBadge, ui.Bold(it.ProjectID))
			if it.Title != "" {
				header += fmt.Sprintf(" · %s", ui.Bold(it.Title))
			} else if it.Timestamp != "" {
				if t, err := time.Parse(time.RFC3339, it.Timestamp); err == nil {
					header += fmt.Sprintf(" · %s", ui.Dim(t.Format("2006-01-02 15:04")))
				}
			}
			fmt.Fprintln(os.Stdout, header)
			content := it.Content
			if !verbose {
				content = trunc(strings.ReplaceAll(content, "\n", " "), 100)
			} else {
				content = strings.ReplaceAll(content, "\n", "\n   ")
			}
			fmt.Fprintf(os.Stdout, "    %s\n\n", content)
		}
		return
	}

	// 3. Standard Raw Transcript Message Search
	query := strings.Join(terms, " ")
	sinceStr := ""
	if !sinceTime.IsZero() {
		sinceStr = sinceTime.Format(time.RFC3339)
	}

	if twoPass {
		candLimit := top
		if candLimit <= 0 {
			candLimit = 10
		}
		exchanges, err := st.SearchTwoPass(context.Background(), query, store.SearchPassOptions{
			Project:           project,
			Tool:              tool,
			Role:              role,
			Since:             sinceStr,
			CandidateLimit:    candLimit,
			ContextTurnWindow: window,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila search: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(exchanges)
			return
		}
		if len(exchanges) == 0 {
			if toonOut {
				fmt.Print("exchanges[0]{session_id,turn_id,user_prompt,assistant_fix}:\n")
			} else {
				fmt.Fprintf(os.Stdout, "search: 0 conversation exchanges found matching %q %s\n", query, scopeLabel(project))
				if !outModes.quiet {
					fmt.Fprintf(os.Stdout, "help: try 'sila search --no-two-pass %q' or 'sila search --omni %q'\n", query, query)
				}
			}
			return
		}
		if toonOut {
			rows := make([][]string, len(exchanges))
			for i, ex := range exchanges {
				u := ex.UserPrompt
				a := ex.AssistantFix
				if !verbose {
					u = trunc(strings.ReplaceAll(u, "\n", " "), 80)
					a = trunc(strings.ReplaceAll(a, "\n", " "), 80)
				}
				rows[i] = []string{ex.SessionID, ex.MatchedTurnID, u, a}
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("exchanges", []string{"session_id", "turn_id", "user_prompt", "assistant_fix"}, rows))
			return
		}
		if outModes.porcelain {
			for _, ex := range exchanges {
				fmt.Fprintf(os.Stdout, "%s\t%s\t%.1f\t%s\t%s\n", ex.SessionID, ex.MatchedTurnID, ex.FusedScore, sanitizePorcelain(ex.UserPrompt), sanitizePorcelain(ex.AssistantFix))
			}
			return
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · search (two-pass resolved exchanges)"))+ui.Dim(fmt.Sprintf(" (query: %q · %d results)", query, len(exchanges))))
		fmt.Fprintln(os.Stdout)
		for i, ex := range exchanges {
			sessLabel := ex.SessionID
			if sessLabel == "" {
				sessLabel = "session-unspecified"
			}
			fmt.Fprintf(os.Stdout, "%s %s %s\n",
				ui.Bold(fmt.Sprintf("[%d] Session: %s", i+1, sessLabel)),
				ui.Dim(fmt.Sprintf("(turn #%s · score: %.1f)", ex.MatchedTurnID, ex.FusedScore)),
				ui.Dim(ex.ProjectID),
			)
			if ex.UserPrompt != "" {
				uText := ex.UserPrompt
				if !verbose {
					uText = trunc(strings.ReplaceAll(uText, "\n", " "), 120)
				}
				fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold(ui.Cyan("User:")), uText)
			}
			if ex.AssistantFix != "" {
				aText := ex.AssistantFix
				if !verbose {
					aText = trunc(strings.ReplaceAll(aText, "\n", " "), 120)
				}
				fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold(ui.Green("Assistant:")), aText)
			}
			if verbose && len(ex.SurroundingTurns) > 0 {
				fmt.Fprintln(os.Stdout, ui.Dim("  --- Surrounding Turns ---"))
				for _, turn := range ex.SurroundingTurns {
					fmt.Fprintf(os.Stdout, "    [%s · %s]\n    %s\n",
						ui.Dim(turn.Timestamp), ui.Bold(turn.Role),
						strings.ReplaceAll(turn.Content, "\n", "\n    "))
				}
			}
			fmt.Fprintln(os.Stdout)
		}
		fmt.Fprintln(os.Stdout, ui.Bold(fmt.Sprintf("Found %d resolved %s for %q.", len(exchanges), plural(int64(len(exchanges)), "exchange"), query)))
		fmt.Fprintln(os.Stdout, ui.Dim("Pass --no-two-pass to search raw individual turns without exchange expansion."))
		return
	}

	msgs, err := st.SearchMessagesFTS(query, store.SearchMessagesOptions{
		Project: project,
		Tool:    tool,
		Role:    role,
		Since:   sinceStr,
		Limit:   top,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	if semantic && len(msgs) > 1 {
		if provider := assist.ActiveProviderFor("search", project); provider != nil {
			var candidates []assist.RerankCandidate
			for i, m := range msgs {
				candidates = append(candidates, assist.RerankCandidate{
					Key:     fmt.Sprintf("%d", i),
					Excerpt: fmt.Sprintf("[%s] %s", m.Role, m.Content),
				})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
			res, err := assist.Rerank(ctx, provider, query, candidates)
			cancel()
			if err == nil && res != nil && len(res.Scores) > 0 {
				type scoredMsg struct {
					msg   store.StoredMessage
					score float64
				}
				scored := make([]scoredMsg, len(msgs))
				for i, m := range msgs {
					scored[i] = scoredMsg{
						msg:   m,
						score: res.Scores[fmt.Sprintf("%d", i)],
					}
				}
				sort.SliceStable(scored, func(i, j int) bool {
					return scored[i].score > scored[j].score
				})
				for i := range msgs {
					msgs[i] = scored[i].msg
				}
			}
		}
	}

	if jsonOut {
		emitJSON(msgs)
		return
	}
	if len(msgs) == 0 {
		if toonOut {
			fmt.Print("messages[0]{id,project,tool,role,time,content}:\n")
		} else {
			fmt.Fprintf(os.Stdout, "search: 0 messages found matching %q %s\n", query, scopeLabel(project))
			if !outModes.quiet {
				fmt.Fprintf(os.Stdout, "help: try 'sila search --omni %q' or 'sila search claims %q'\n", query, query)
				if len(strings.Fields(query)) > 1 {
					fmt.Fprintf(os.Stdout, "help: every term must appear in one message — try fewer terms\n")
				}
			}
		}
		return
	}
	if toonOut {
		rows := make([][]string, len(msgs))
		for i, m := range msgs {
			content := m.Content
			if !verbose {
				content = trunc(strings.ReplaceAll(content, "\n", " "), 100)
			}
			tools := strings.Join(m.SourceTools, ",")
			rows[i] = []string{fmt.Sprintf("%d", m.ID), m.ProjectID, tools, m.Role, m.TS, content}
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("messages", []string{"id", "project", "tools", "role", "time", "content"}, rows))
		fmt.Fprintln(os.Stdout, "help[1]: Run `sila search expand <id>` to unroll full surrounding conversation context")
		return
	}
	if outModes.porcelain {
		for _, m := range msgs {
			fmt.Fprintf(os.Stdout, "%d\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.ProjectID, strings.Join(m.SourceTools, ","), m.Role, m.TS, sanitizePorcelain(m.Content))
		}
		return
	}
	headers := []string{"id", "project", "tool", "role", "time", "content"}
	var rows [][]string
	for _, m := range msgs {
		content := m.Content
		if !verbose {
			content = trunc(strings.ReplaceAll(content, "\n", " "), 60)
		}
		tools := strings.Join(m.SourceTools, ",")
		ts := m.TS
		if t, err := time.Parse(time.RFC3339, m.TS); err == nil {
			ts = t.Format("2006-01-02 15:04")
		}
		rows = append(rows, []string{fmt.Sprintf("%d", m.ID), m.ProjectID, ui.Dim(tools), m.Role, ui.Dim(ts), content})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"r", "l", "l", "l", "l", "l"}))
	if !outModes.quiet {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Bold(fmt.Sprintf("Found %d matching %s for %q.", len(msgs), plural(int64(len(msgs)), "message"), query)))
		fmt.Fprintln(os.Stdout, ui.Dim("Run `sila search --omni <query>` to search across facts, invariants, and docs."))
		fmt.Fprintln(os.Stdout, ui.Dim("Run `sila search expand <id>` to unroll full surrounding conversation context."))
	}
}

func runSearchDocs(ui render.UI, st *store.Store, project string, jsonOut, toonOut, verbose bool, top int, terms []string) {
	if len(terms) == 0 {
		fmt.Fprintln(os.Stderr, "sila search docs: specify search query")
		os.Exit(2)
	}
	query := strings.Join(terms, " ")
	docs, err := st.SearchDocsFTS(query, project, top)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila search docs: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(docs)
		return
	}
	if len(docs) == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("No documents match %q.", query)))
		return
	}
	headers := []string{"id", "project", "title", "content snippet"}
	var rows [][]string
	for _, d := range docs {
		snippet := trunc(strings.ReplaceAll(d.Content, "\n", " "), 60)
		rows = append(rows, []string{d.ID, d.ProjectID, ui.Bold(d.Title), snippet})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l"}))
}
