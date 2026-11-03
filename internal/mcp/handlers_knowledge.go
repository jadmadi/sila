package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/gopls"
	"github.com/jadmadi/sila/internal/ingest"
	"github.com/jadmadi/sila/internal/store"
)

func (s *Server) handleKnowledgeTool(req JSONRPCRequest, name string, rawArgs json.RawMessage) bool {
	switch name {
	case "search_knowledge":
		var args struct {
			Query         string `json:"query"`
			Project       string `json:"project"`
			Tool          string `json:"tool"`
			RefsExistOnly bool   `json:"refs_exist_only"`
			VerifiedOnly  bool   `json:"verified_only"`
			FactsOnly     bool   `json:"facts_only"`
			Limit         int    `json:"limit"`
			Offset        int    `json:"offset"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Query == "" {
			s.sendError(req.ID, -32602, "Invalid params", "query parameter is required")
			return true
		}
		limit := clampLimit(args.Limit, 10, 200)
		if args.Offset < 0 {
			args.Offset = 0
		}
		refsExist := args.RefsExistOnly || args.VerifiedOnly
		terms := strings.Fields(args.Query)
		proj := args.Project
		if proj != "" {
			if resolved, err := s.resolveTargetProject(proj); err == nil {
				proj = resolved
			}
		}
		facts, err := dream.QueryStoreFactsPaged(s.store, terms, proj, args.Tool, refsExist, false, args.FactsOnly, limit, args.Offset)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		formatted, _ := json.MarshalIndent(facts, "", "  ")
		s.sendToolResult(req.ID, facts, string(formatted))
		return true

	case "search_messages":
		var args struct {
			Query     string `json:"query"`
			Project   string `json:"project"`
			Tool      string `json:"tool"`
			Role      string `json:"role"`
			Since     string `json:"since"`
			Limit     int    `json:"limit"`
			Offset    int    `json:"offset"`
			TwoPass   bool   `json:"two_pass"`
			Window    int    `json:"window"`
			MaxTokens int    `json:"max_tokens"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Query == "" {
			s.sendError(req.ID, -32602, "Invalid params", "query parameter is required")
			return true
		}
		limit := clampLimit(args.Limit, 10, 200)
		if args.Offset < 0 {
			args.Offset = 0
		}
		proj := args.Project
		if proj != "" {
			if resolved, err := s.resolveTargetProject(proj); err == nil {
				proj = resolved
			}
		}
		if args.TwoPass {
			exchanges, err := s.store.SearchTwoPass(context.Background(), args.Query, store.SearchPassOptions{
				Project:           proj,
				Tool:              args.Tool,
				Role:              args.Role,
				Since:             args.Since,
				CandidateLimit:    limit,
				ContextTurnWindow: args.Window,
				MaxTurnTokens:     args.MaxTokens,
			})
			if err != nil {
				s.sendError(req.ID, -32603, "Internal error", err.Error())
				return true
			}
			formatted, _ := json.MarshalIndent(exchanges, "", "  ")
			s.sendToolResult(req.ID, exchanges, string(formatted))
			return true
		}

		msgs, err := s.store.SearchMessagesFTS(args.Query, store.SearchMessagesOptions{
			Project: proj,
			Tool:    args.Tool,
			Role:    args.Role,
			Since:   args.Since,
			Limit:   limit,
			Offset:  args.Offset,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		formatted, _ := json.MarshalIndent(msgs, "", "  ")
		s.sendToolResult(req.ID, msgs, string(formatted))
		return true

	case "save_memo":
		var args struct {
			Project      string   `json:"project"`
			Content      string   `json:"content"`
			Agent        string   `json:"agent"`
			RepoFiles    []string `json:"repo_files"`
			Files        []string `json:"files"`
			Decision     string   `json:"decision"`
			ToolName     string   `json:"tool_name"`
			ClientKind   string   `json:"client_kind"`
			OriginEntity string   `json:"origin_entity"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Content == "" {
			s.sendError(req.ID, -32602, "Invalid params", "content is required")
			return true
		}
		projectID, err := s.resolveTargetProject(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		memoFiles := mergeUniqueStrings(args.RepoFiles, args.Files)
		content := args.Content
		if args.Decision != "" {
			if _, canonical, pErr := store.ParseAddress(args.Decision); pErr == nil {
				memoFiles = append(memoFiles, "decision:"+canonical)
				if !strings.Contains(content, "[decision:") {
					content = fmt.Sprintf("[decision: %s] %s", canonical, content)
				}
			}
		}
		fact, err := s.store.InsertMemoWithProvenance(projectID, content, args.Agent, "", memoFiles, "", store.Provenance{
			ToolName:     args.ToolName,
			ClientKind:   args.ClientKind,
			OriginEntity: args.OriginEntity,
			SessionRef:   s.sessionID,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.recordMemoryEvent(projectID, "memo", "ok", "", 0)
		formatted, _ := json.MarshalIndent(fact, "", "  ")
		s.sendToolResult(req.ID, fact, string(formatted))
		return true
	case "save_lesson":
		var args struct {
			Project      string   `json:"project"`
			Symptom      string   `json:"symptom"`
			Cause        string   `json:"cause"`
			Fix          string   `json:"fix"`
			Tags         string   `json:"tags"`
			Files        []string `json:"files"`
			RepoFiles    []string `json:"repo_files"`
			Agent        string   `json:"agent"`
			ToolName     string   `json:"tool_name"`
			ClientKind   string   `json:"client_kind"`
			OriginEntity string   `json:"origin_entity"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Symptom == "" {
			s.sendError(req.ID, -32602, "Invalid params", "symptom is required")
			return true
		}
		projectID, err := s.resolveTargetProject(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		lessonFiles := mergeUniqueStrings(args.Files, args.RepoFiles)
		content := store.FormatLesson(args.Symptom, args.Cause, args.Fix, args.Tags)
		fact, err := s.store.InsertMemoWithProvenance(projectID, content, args.Agent, "", lessonFiles, "lesson", store.Provenance{
			ToolName:     args.ToolName,
			ClientKind:   args.ClientKind,
			OriginEntity: args.OriginEntity,
			SessionRef:   s.sessionID,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		formatted, _ := json.MarshalIndent(fact, "", "  ")
		s.sendToolResult(req.ID, fact, string(formatted))
		return true

	case "save_case":
		var args struct {
			Project      string   `json:"project"`
			Intent       string   `json:"intent"`
			Approach     string   `json:"approach"`
			Invariant    string   `json:"invariant"`
			Tags         string   `json:"tags"`
			Files        []string `json:"files"`
			RepoFiles    []string `json:"repo_files"`
			Agent        string   `json:"agent"`
			ToolName     string   `json:"tool_name"`
			ClientKind   string   `json:"client_kind"`
			OriginEntity string   `json:"origin_entity"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if strings.TrimSpace(args.Intent) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "intent is required")
			return true
		}
		projectID, err := s.resolveTargetProject(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		caseFiles := mergeUniqueStrings(args.Files, args.RepoFiles)
		content := store.FormatCase(args.Intent, args.Approach, args.Invariant, args.Tags)
		fact, err := s.store.InsertMemoWithProvenance(projectID, content, args.Agent, "", caseFiles, "case", store.Provenance{
			ToolName:     args.ToolName,
			ClientKind:   args.ClientKind,
			OriginEntity: args.OriginEntity,
			SessionRef:   s.sessionID,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.recordMemoryEvent(projectID, "case", "ok", "", 0)
		formatted, _ := json.MarshalIndent(fact, "", "  ")
		s.sendToolResult(req.ID, fact, string(formatted))
		return true

	case "save_doc":
		var args struct {
			Project string `json:"project"`
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Title == "" || args.Content == "" {
			s.sendError(req.ID, -32602, "Invalid params", "title and content are required")
			return true
		}
		projectID, err := s.resolveTargetProject(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		doc := store.Doc{
			ProjectID: projectID,
			Title:     args.Title,
			URL:       args.URL,
			Content:   args.Content,
			Format:    "markdown",
		}
		if err := s.store.InsertDoc(doc); err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		text := fmt.Sprintf("Saved reference doc %q for project %q.", args.Title, projectID)
		s.sendToolResult(req.ID, doc, text)
		return true

	case "search_docs":
		var args struct {
			Query   string `json:"query"`
			Project string `json:"project"`
			Limit   int    `json:"limit"`
			Offset  int    `json:"offset"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Query == "" {
			s.sendError(req.ID, -32602, "Invalid params", "query is required")
			return true
		}
		projectID := args.Project
		if projectID != "" {
			if resolved, err := s.resolveTargetProject(projectID); err == nil {
				projectID = resolved
			}
		}
		fetchLimit := clampLimit(args.Limit, 10, 100)
		if args.Offset > 0 {
			fetchLimit += args.Offset
		}
		if args.Offset < 0 {
			args.Offset = 0
		}
		docs, err := s.store.SearchDocsFTS(args.Query, projectID, fetchLimit)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		if args.Offset > 0 {
			if args.Offset >= len(docs) {
				docs = nil
			} else {
				docs = docs[args.Offset:]
			}
		}
		if args.Limit > 0 && len(docs) > args.Limit {
			docs = docs[:args.Limit]
		}
		formatted, _ := json.MarshalIndent(docs, "", "  ")
		s.sendToolResult(req.ID, docs, string(formatted))
		return true

	case "get_doc":
		var args struct {
			ID string `json:"id"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.ID == "" {
			s.sendError(req.ID, -32602, "Invalid params", "id is required")
			return true
		}
		doc, err := s.store.GetDoc(args.ID)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		if doc == nil {
			s.sendToolResult(req.ID, nil, fmt.Sprintf("Document not found with ID %q.", args.ID))
			return true
		}
		formatted, _ := json.MarshalIndent(doc, "", "  ")
		s.sendToolResult(req.ID, doc, string(formatted))
		return true

	case "search_web_docs":
		var args struct {
			Query    string `json:"query"`
			Project  string `json:"project"`
			Top      int    `json:"top"`
			AutoSave bool   `json:"auto_save"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Query == "" {
			s.sendError(req.ID, -32602, "Invalid params", "query is required")
			return true
		}
		cfg, _ := config.Load("")
		args.Top = clampLimit(args.Top, 5, 50)
		results, _, err := ingest.SearchWeb(args.Query, args.Top, cfg.DocSites, cfg)
		if err != nil {
			s.sendError(req.ID, -32603, "Search error", err.Error())
			return true
		}
		if args.AutoSave && len(results) > 0 && args.Project != "" {
			_, _ = ingest.IngestSearchResults(s.store, results, args.Project)
		}
		formatted, _ := json.MarshalIndent(results, "", "  ")
		s.sendToolResult(req.ID, results, string(formatted))
		return true

	case "search_code":
		var args struct {
			Query     string `json:"query"`
			Language  string `json:"language"`
			Repo      string `json:"repo"`
			Extension string `json:"extension"`
			Project   string `json:"project"`
			Top       int    `json:"top"`
			AutoSave  bool   `json:"auto_save"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Query == "" {
			s.sendError(req.ID, -32602, "Invalid params", "query is required")
			return true
		}
		cfg, _ := config.Load("")
		args.Top = clampLimit(args.Top, 5, 50)
		q := ingest.CodeSearchQuery{
			Query:     args.Query,
			Language:  args.Language,
			Repo:      args.Repo,
			Extension: args.Extension,
			Top:       args.Top,
		}
		results, _, err := ingest.SearchCode(q, cfg)
		if err != nil {
			s.sendError(req.ID, -32603, "Search error", err.Error())
			return true
		}
		if args.AutoSave && len(results) > 0 && args.Project != "" {
			_, _ = ingest.IngestCodeSearchResults(s.store, results, args.Project)
		}
		formatted, _ := json.MarshalIndent(results, "", "  ")
		s.sendToolResult(req.ID, results, string(formatted))
		return true

	case "get_package_doc":
		var args struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
			Project   string `json:"project"`
			AutoSave  bool   `json:"auto_save"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Name == "" {
			s.sendError(req.ID, -32602, "Invalid params", "name is required")
			return true
		}
		doc, err := ingest.FetchPackageDoc(args.Name, args.Ecosystem)
		if err != nil {
			s.sendError(req.ID, -32603, "Fetch error", err.Error())
			return true
		}
		if args.AutoSave && args.Project != "" {
			_, _ = ingest.IngestPackageDoc(s.store, doc, args.Project)
		}
		formatted, _ := json.MarshalIndent(doc, "", "  ")
		s.sendToolResult(req.ID, doc, string(formatted))
		return true

	case "search_stackoverflow":
		var args struct {
			Query    string `json:"query"`
			Tag      string `json:"tag"`
			Project  string `json:"project"`
			Top      int    `json:"top"`
			AutoSave bool   `json:"auto_save"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Query == "" {
			s.sendError(req.ID, -32602, "Invalid params", "query is required")
			return true
		}
		cfg, _ := config.Load("")
		args.Top = clampLimit(args.Top, 5, 50)
		results, err := ingest.SearchStackOverflow(args.Query, args.Tag, args.Top, cfg)
		if err != nil {
			s.sendError(req.ID, -32603, "Search error", err.Error())
			return true
		}
		if args.AutoSave && len(results) > 0 && args.Project != "" {
			_, _ = ingest.IngestStackOverflowResults(s.store, results, args.Project)
		}
		formatted, _ := json.MarshalIndent(results, "", "  ")
		s.sendToolResult(req.ID, results, string(formatted))
		return true

	case "get_message_context":
		var args struct {
			ID         int64 `json:"id"`
			Window     int   `json:"window"`
			Occurrence int   `json:"occurrence"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if args.ID <= 0 {
			s.sendError(req.ID, -32602, "Invalid params", "id is required and must be > 0")
			return true
		}
		if args.Window <= 0 {
			args.Window = 3
		}
		if args.Window > 20 {
			args.Window = 20
		}
		msgCtx, err := s.store.MessageInContext(args.ID, args.Window, args.Occurrence)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Expanded transcript context for message %d (window: ±%d turns, project: %s):\n\n",
			msgCtx.TargetMessage.ID, msgCtx.Window, msgCtx.TargetMessage.ProjectID))
		if msgCtx.SessionID != "" {
			sb.WriteString(fmt.Sprintf("session: %s\n", msgCtx.SessionID))
		}
		if msgCtx.OccurrenceCount > 1 {
			sb.WriteString(fmt.Sprintf("note: %d recorded source occurrences; showing #%d\n", msgCtx.OccurrenceCount, msgCtx.SelectedOccurrence))
		}
		if msgCtx.Note != "" {
			sb.WriteString("note: " + msgCtx.Note + "\n")
		}
		sb.WriteString("\n")
		if len(msgCtx.Before) > 0 {
			sb.WriteString("--- PRECEDING CONTEXT ---\n")
			for _, m := range msgCtx.Before {
				sb.WriteString(fmt.Sprintf("[%s · %s · %s]\n%s\n\n", m.TS, m.Role, strings.Join(m.SourceTools, ","), m.Content))
			}
		}
		sb.WriteString(">>> TARGET MESSAGE <<<\n")
		sb.WriteString(fmt.Sprintf("[%s · %s · %s]\n%s\n\n",
			msgCtx.TargetMessage.TS, msgCtx.TargetMessage.Role, strings.Join(msgCtx.TargetMessage.SourceTools, ","), msgCtx.TargetMessage.Content))
		if len(msgCtx.After) > 0 {
			sb.WriteString("--- FOLLOWING CONTEXT ---\n")
			for _, m := range msgCtx.After {
				sb.WriteString(fmt.Sprintf("[%s · %s · %s]\n%s\n\n", m.TS, m.Role, strings.Join(m.SourceTools, ","), m.Content))
			}
		}
		s.sendToolResult(req.ID, msgCtx, strings.TrimSpace(sb.String()))
		return true

	case "omni_search":
		var args struct {
			Query    string `json:"query"`
			Project  string `json:"project"`
			Limit    int    `json:"limit"`
			Tool     string `json:"tool"`
			Role     string `json:"role"`
			Since    string `json:"since"`
			Semantic bool   `json:"semantic"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if args.Query == "" {
			s.sendError(req.ID, -32602, "Invalid params", "query is required")
			return true
		}
		res, err := dream.OmniSearchFiltered(s.store, args.Query, dream.OmniOptions{
			Project:  args.Project,
			Limit:    clampLimit(args.Limit, 15, 100),
			Tool:     args.Tool,
			Role:     args.Role,
			Since:    args.Since,
			Semantic: args.Semantic,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d results across all knowledge tiers for %q:\n\n", len(res.Items), args.Query))
		for _, w := range res.Warnings {
			sb.WriteString("warning: " + w + "\n")
		}
		if len(res.Warnings) > 0 {
			sb.WriteString("\n")
		}
		for i, it := range res.Items {
			prefix := fmt.Sprintf("[%s]", strings.ToUpper(string(it.Type)))
			if it.Title != "" {
				sb.WriteString(fmt.Sprintf("%d. %s %s (project: %s)\n   %s\n\n", i+1, prefix, it.Title, it.ProjectID, it.Content))
			} else {
				sb.WriteString(fmt.Sprintf("%d. %s (project: %s · %s)\n   %s\n\n", i+1, prefix, it.ProjectID, it.Timestamp, it.Content))
			}
		}
		s.sendToolResult(req.ID, res, strings.TrimSpace(sb.String()))
		return true

	case "recall":
		var args struct {
			Query    string `json:"query"`
			Project  string `json:"project"`
			Limit    int    `json:"limit"`
			Semantic bool   `json:"semantic"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if strings.TrimSpace(args.Query) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "query is required")
			return true
		}
		res, err := dream.RecallSolutions(s.store, args.Query, dream.RecallOptions{
			Project:  args.Project,
			Limit:    clampLimit(args.Limit, 5, 50),
			Semantic: args.Semantic,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		var rb strings.Builder
		rb.WriteString(fmt.Sprintf("Found %d prior resolutions for %q (lessons & cases first):\n\n", res.Total, args.Query))
		for _, w := range res.Warnings {
			rb.WriteString("warning: " + w + "\n")
		}
		if len(res.Warnings) > 0 {
			rb.WriteString("\n")
		}
		for i, h := range res.Hits {
			ground := "ungrounded"
			if h.Grounded {
				ground = "grounded"
			}
			rb.WriteString(fmt.Sprintf("%d. [%s] %s · %s · %s\n   %s\n   %s\n\n", i+1, strings.ToUpper(h.Kind), h.ProjectID, h.Timestamp, ground, h.Resolution, h.SourceRef))
		}
		s.sendToolResult(req.ID, res, strings.TrimSpace(rb.String()))
		return true

	case "go_symbol_references":
		var args struct {
			File    string `json:"file"`
			Symbol  string `json:"symbol"`
			Dir     string `json:"dir"`
			Project string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if strings.TrimSpace(args.File) == "" || strings.TrimSpace(args.Symbol) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "file and symbol are required")
			return true
		}
		connector := gopls.NewConnector()
		res, err := connector.FindSymbolReferences(context.Background(), args.File, args.Symbol, args.Dir)
		if err != nil {
			s.sendError(req.ID, -32603, "Symbol lookup error", err.Error())
			return true
		}
		summary := fmt.Sprintf("Found %d reference(s) to %q via %s", res.TotalMatches, res.Symbol, res.Provider)
		s.sendToolResult(req.ID, res, summary)
		return true

	case "go_package_api":
		var args struct {
			Pkg               string `json:"pkg"`
			IncludeUnexported bool   `json:"include_unexported"`
			Project           string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if strings.TrimSpace(args.Pkg) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "pkg parameter is required")
			return true
		}
		connector := gopls.NewConnector()
		res, err := connector.GetPackageAPI(context.Background(), args.Pkg, args.IncludeUnexported)
		if err != nil {
			s.sendError(req.ID, -32603, "Package API error", err.Error())
			return true
		}
		summary := fmt.Sprintf("Package %s (%s): %d types, %d functions (via %s)", res.Package, res.Import, len(res.Types), len(res.Functions), res.Provider)
		s.sendToolResult(req.ID, res, summary)
		return true

	case "go_diagnostics":
		var args struct {
			Path    string `json:"path"`
			Tags    string `json:"tags"`
			Project string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		connector := gopls.NewConnector()
		res, err := connector.GetDiagnostics(context.Background(), args.Path, args.Tags)
		if err != nil {
			s.sendError(req.ID, -32603, "Diagnostics error", err.Error())
			return true
		}
		var summary string
		if res.Clean {
			summary = fmt.Sprintf("✔ Clean: zero diagnostics across %s (via %s)", res.Path, res.Provider)
		} else {
			summary = fmt.Sprintf("▲ Found %d diagnostic(s) across %s (via %s)", len(res.Diagnostics), res.Path, res.Provider)
		}
		s.sendToolResult(req.ID, res, summary)
		return true

	default:
		return false
	}
}
