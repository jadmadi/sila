package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/crew"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"
)

// writeJSON formats and writes a JSON response with status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// writeError formats a structured JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error":  msg,
		"status": status,
	})
}

// isUngroundedClaim reports a code claim whose files are missing. Invariants,
// non-goals, and milestones are deliberate rules, not stale claims, so the
// health strip and the Memory view agree on the same set.
func isUngroundedClaim(f store.Fact) bool {
	return !f.FileRefsExist && !f.IsInvariant() && !f.IsNonGoal() && !f.IsMilestone()
}

// overviewCounts is the top-line health strip.
type overviewCounts struct {
	Messages int `json:"messages"`
	Facts    int `json:"facts"`
	Verified int `json:"verified"`
	Stale    int `json:"stale_claims"`
	Projects int `json:"projects"`
}

// overviewMemory is the deliberate-memory corpus.
type overviewMemory struct {
	Lessons    int `json:"lessons"`
	Cases      int `json:"cases"`
	Invariants int `json:"invariants"`
	NonGoals   int `json:"non_goals"`
}

// overviewHarness is one harness's share of recent activity.
type overviewHarness struct {
	Tool      string `json:"tool"`
	Messages  int    `json:"messages"`
	Assistant int    `json:"assistant"`
	User      int    `json:"user"`
}

// timelineItem is one recent turn shown on the console.
type timelineItem struct {
	ID      int64    `json:"id"`
	TS      string   `json:"ts"`
	Role    string   `json:"role"`
	Tools   []string `json:"tools"`
	Content string   `json:"content"`
}

// overviewGoals is the goal-board summary.
type overviewGoals struct {
	Total       int `json:"total"`
	Claimed     int `json:"claimed"`
	Ready       int `json:"ready"`
	Blocked     int `json:"blocked"`
	Implemented int `json:"implemented"`
}

// overviewResponse is the single payload the console loads on first paint.
type overviewResponse struct {
	ProjectID  string            `json:"project_id"`
	ProjectDir string            `json:"project_dir"`
	Counts     overviewCounts    `json:"counts"`
	Memory     overviewMemory    `json:"memory"`
	Harnesses  []overviewHarness `json:"harnesses"`
	Recent     []timelineItem    `json:"recent"`
	Handoff    *store.Handoff    `json:"handoff,omitempty"`
	Goals      overviewGoals     `json:"goals"`
}

// handleOverview returns the console's first-paint payload.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	msgs, factsCount, verified, projects, err := s.cfg.Store.Counts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("counts failed: %v", err))
		return
	}

	projectFacts, _ := s.cfg.Store.FactsByProject(s.cfg.ProjectID)
	stale := 0
	invariants := 0
	nonGoals := 0
	for _, f := range projectFacts {
		if isUngroundedClaim(f) {
			stale++
		}
		if f.IsInvariant() {
			invariants++
		}
		if f.IsNonGoal() {
			nonGoals++
		}
	}
	lessons, _ := s.cfg.Store.LessonFacts(s.cfg.ProjectID, 500)
	cases, _ := s.cfg.Store.CaseFacts(s.cfg.ProjectID, 500)

	// One bounded scan powers both the harness mix and the visible timeline.
	recent, _ := s.cfg.Store.RecentMessages(s.cfg.ProjectID, 200)
	harnessMix := map[string]*overviewHarness{}
	for _, m := range recent {
		for _, tool := range m.SourceTools {
			h := harnessMix[tool]
			if h == nil {
				h = &overviewHarness{Tool: tool}
				harnessMix[tool] = h
			}
			h.Messages++
			switch strings.ToLower(m.Role) {
			case "assistant":
				h.Assistant++
			case "user":
				h.User++
			}
		}
	}
	harnesses := make([]overviewHarness, 0, len(harnessMix))
	for _, h := range harnessMix {
		harnesses = append(harnesses, *h)
	}
	sort.Slice(harnesses, func(i, j int) bool {
		if harnesses[i].Messages != harnesses[j].Messages {
			return harnesses[i].Messages > harnesses[j].Messages
		}
		return harnesses[i].Tool < harnesses[j].Tool
	})

	items := make([]timelineItem, 0, 100)
	for i, m := range recent {
		if i == 100 {
			break
		}
		items = append(items, timelineItem{
			ID:      m.ID,
			TS:      m.TS,
			Role:    m.Role,
			Tools:   m.SourceTools,
			Content: truncateText(m.Content, 600),
		})
	}

	handoff, _ := s.cfg.Store.LatestHandoff(s.cfg.ProjectID)

	goalSummary := overviewGoals{}
	if audit, scanErr := goals.ScanGoals(s.cfg.ProjectDir); scanErr == nil && audit != nil {
		goalSummary.Total = audit.Total
		goalSummary.Claimed = audit.ClaimedCount
		goalSummary.Ready = audit.ReadyCount
		goalSummary.Blocked = audit.BlockedCount
		goalSummary.Implemented = audit.ImplementedCount
	}

	writeJSON(w, http.StatusOK, overviewResponse{
		ProjectID:  s.cfg.ProjectID,
		ProjectDir: s.cfg.ProjectDir,
		Counts: overviewCounts{
			Messages: msgs,
			Facts:    factsCount,
			Verified: verified,
			Stale:    stale,
			Projects: projects,
		},
		Memory: overviewMemory{
			Lessons:    len(lessons),
			Cases:      len(cases),
			Invariants: invariants,
			NonGoals:   nonGoals,
		},
		Harnesses: harnesses,
		Recent:    items,
		Handoff:   handoff,
		Goals:     goalSummary,
	})
}

// handleRecall answers "ask your history" with a ranked recall pack.
func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	var paths []string
	for p := range strings.SplitSeq(r.URL.Query().Get("paths"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	res, err := dream.RecallSolutions(s.cfg.Store, query, dream.RecallOptions{
		Project:      s.cfg.ProjectID,
		ProjectDir:   s.cfg.ProjectDir,
		Limit:        8,
		BudgetTokens: 1200,
		Context: &dream.RecallContext{
			Task:  strings.TrimSpace(r.URL.Query().Get("task")),
			Paths: paths,
		},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("recall failed: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// truncateText bounds one timeline cell without cutting a UTF-8 rune.
func truncateText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}

// handleStatus returns store metrics, active project, and database health.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Link", mcp.DefaultLinkHeaders(getRequestBaseURL(r)))

	msgs, factsCount, verified, projects, err := s.cfg.Store.Counts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("counts failed: %v", err))
		return
	}

	facts, _ := s.cfg.Store.FactsByProject(s.cfg.ProjectID)
	staleCount := 0
	for _, f := range facts {
		if isUngroundedClaim(f) {
			staleCount++
		}
	}

	dbStats, _ := s.cfg.Store.Stats()

	resp := map[string]any{
		"project_id":   s.cfg.ProjectID,
		"project_dir":  s.cfg.ProjectDir,
		"messages":     msgs,
		"facts":        factsCount,
		"verified":     verified,
		"projects":     projects,
		"stale_claims": staleCount,
	}
	if dbStats != nil {
		resp["page_size"] = dbStats.PageSize
		resp["page_count"] = dbStats.PageCount
		resp["freelist_pages"] = dbStats.FreelistCount
		resp["file_size_bytes"] = dbStats.FileSize
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleSearch handles full-text transcript search across AI CLI tools.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	limit := 30
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	tool := r.URL.Query().Get("tool")
	opts := store.SearchMessagesOptions{
		Project: s.cfg.ProjectID,
		Tool:    tool,
		Limit:   limit,
	}

	results, err := s.cfg.Store.SearchMessagesFTS(q, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("search failed: %v", err))
		return
	}

	type SearchResultItem struct {
		ID        int64    `json:"id"`
		ProjectID string   `json:"project_id"`
		Role      string   `json:"role"`
		Content   string   `json:"content"`
		Tool      string   `json:"tool"`
		Tools     []string `json:"tools"`
		CreatedAt string   `json:"created_at"`
	}

	var formatted []SearchResultItem
	for _, m := range results {
		toolName := "unknown"
		if len(m.SourceTools) > 0 {
			toolName = m.SourceTools[0]
		}
		formatted = append(formatted, SearchResultItem{
			ID:        m.ID,
			ProjectID: m.ProjectID,
			Role:      m.Role,
			Content:   m.Content,
			Tool:      toolName,
			Tools:     m.SourceTools,
			CreatedAt: m.TS,
		})
	}

	writeJSON(w, http.StatusOK, formatted)
}

// handleExpand returns conversation turns surrounding a message.
func (s *Server) handleExpand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	idStr := r.URL.Query().Get("id")
	msgID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message id")
		return
	}

	window := 3
	if wStr := r.URL.Query().Get("window"); wStr != "" {
		if n, err := strconv.Atoi(wStr); err == nil && n > 0 && n <= 20 {
			window = n
		}
	}

	occurrence := 0
	if oStr := r.URL.Query().Get("occurrence"); oStr != "" {
		if n, err := strconv.Atoi(oStr); err == nil && n > 0 {
			occurrence = n
		}
	}

	ctx, err := s.cfg.Store.MessageInContext(msgID, window, occurrence)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("expand failed: %v", err))
		return
	}

	type TurnItem struct {
		ID        int64  `json:"id"`
		Role      string `json:"role"`
		Content   string `json:"content"`
		Tool      string `json:"tool"`
		CreatedAt string `json:"created_at"`
		IsTarget  bool   `json:"is_target"`
	}

	var turns []TurnItem
	for _, m := range ctx.Before {
		t := "unknown"
		if len(m.SourceTools) > 0 {
			t = m.SourceTools[0]
		}
		turns = append(turns, TurnItem{
			ID:        m.ID,
			Role:      m.Role,
			Content:   m.Content,
			Tool:      t,
			CreatedAt: m.TS,
			IsTarget:  false,
		})
	}

	targetTool := "unknown"
	if len(ctx.TargetMessage.SourceTools) > 0 {
		targetTool = ctx.TargetMessage.SourceTools[0]
	}
	turns = append(turns, TurnItem{
		ID:        ctx.TargetMessage.ID,
		Role:      ctx.TargetMessage.Role,
		Content:   ctx.TargetMessage.Content,
		Tool:      targetTool,
		CreatedAt: ctx.TargetMessage.TS,
		IsTarget:  true,
	})

	for _, m := range ctx.After {
		t := "unknown"
		if len(m.SourceTools) > 0 {
			t = m.SourceTools[0]
		}
		turns = append(turns, TurnItem{
			ID:        m.ID,
			Role:      m.Role,
			Content:   m.Content,
			Tool:      t,
			CreatedAt: m.TS,
			IsTarget:  false,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"turns":       turns,
		"target_id":   msgID,
		"session_id":  ctx.SessionID,
		"occurrences": ctx.OccurrenceCount,
		"note":        ctx.Note,
	})
}

// handleClaims returns claims/facts with optional staleness filtering.
func (s *Server) handleClaims(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		staleOnly := r.URL.Query().Get("stale") == "true"
		facts, err := s.cfg.Store.FactsByProject(s.cfg.ProjectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("read claims: %v", err))
			return
		}

		var out []store.Fact
		for _, f := range facts {
			if staleOnly {
				if f.IsStale() || isUngroundedClaim(f) {
					out = append(out, f)
				}
			} else {
				out = append(out, f)
			}
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		// Reconcile / Re-verify on disk
		res, err := s.cfg.Store.ReconcileFactDrift(s.cfg.ProjectID, s.cfg.ProjectDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("reconcile failed: %v", err))
			return
		}

		facts, _ := s.cfg.Store.FactsByProject(s.cfg.ProjectID)
		grounded := 0
		stale := 0
		for _, f := range facts {
			if f.FileRefsExist || f.IsInvariant() {
				grounded++
			} else {
				stale++
			}
		}

		resp := map[string]any{
			"total_checked":  res.TotalChecked,
			"re_verified":    res.ReVerified,
			"recovered":      res.Recovered,
			"newly_stale":    res.NewlyStale,
			"total_grounded": grounded,
			"total_stale":    stale,
		}

		s.events.Broadcast("claims_reconciled", resp)
		writeJSON(w, http.StatusOK, resp)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleFacts provides listing, creation, promotion, and deletion of durable facts/invariants.
func (s *Server) handleFacts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		facts, err := s.cfg.Store.FactsByProject(s.cfg.ProjectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("read facts: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, facts)

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "id parameter is required")
			return
		}
		if err := s.cfg.Store.DeleteFact(id); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("delete fact: %v", err))
			return
		}
		s.events.Broadcast("fact_deleted", map[string]string{"id": id})
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})

	case http.MethodPost:
		if r.URL.Query().Get("action") == "promote" {
			var req struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
				writeError(w, http.StatusBadRequest, "fact id is required")
				return
			}
			facts, err := s.cfg.Store.FactsByProject(s.cfg.ProjectID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			var target *store.Fact
			for _, f := range facts {
				if f.ID == req.ID {
					target = &f
					break
				}
			}
			if target == nil {
				writeError(w, http.StatusNotFound, "fact not found")
				return
			}
			// Promotion is the human authorization step: an observed candidate
			// becomes an approved rule. Without setting the review status the
			// write would be a no-op on every authority-bearing surface.
			// kind is a hashed ledger field and cannot change in place; the
			// authority surfaces already treat confidence/corroboration
			// "invariant" plus approved review as authoritative.
			target.Confidence = "invariant"
			target.Corroboration = "invariant"
			target.ReviewStatus = store.ReviewApproved
			if err := s.cfg.Store.UpsertFact(*target); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("promote fact: %v", err))
				return
			}
			s.events.Broadcast("fact_promoted", target)
			writeJSON(w, http.StatusOK, target)
			return
		}

		var req struct {
			Claim     string   `json:"claim"`
			Kind      string   `json:"kind"`
			RepoFiles []string `json:"repo_files"`
			Agent     string   `json:"agent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if strings.TrimSpace(req.Claim) == "" {
			writeError(w, http.StatusBadRequest, "claim cannot be empty")
			return
		}
		if req.Agent == "" {
			req.Agent = "web-ui"
		}
		if req.Kind == "" {
			req.Kind = "invariant"
		}

		fact, err := s.cfg.Store.InsertMemoWithKind(s.cfg.ProjectID, req.Claim, req.Agent, s.cfg.ProjectDir, req.RepoFiles, req.Kind)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("insert fact: %v", err))
			return
		}

		s.events.Broadcast("fact_created", fact)
		writeJSON(w, http.StatusCreated, fact)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleFeedback provides ticket listing and resolution.
func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status := r.URL.Query().Get("status")
		filter := store.FeedbackFilter{
			ProjectID: s.cfg.ProjectID,
			Status:    status,
			Limit:     100,
		}
		items, err := s.cfg.Store.QueryFeedback(filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("query feedback: %v", err))
			return
		}

		stats, _ := s.cfg.Store.FeedbackStatusStats()
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"stats": stats,
		})

	case http.MethodPost:
		action := r.URL.Query().Get("action")
		if action == "reopen" {
			var req struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
				writeError(w, http.StatusBadRequest, "missing ticket id")
				return
			}
			if err := s.cfg.Store.ReopenFeedback(req.ID); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("reopen feedback: %v", err))
				return
			}
			s.events.Broadcast("feedback_reopened", map[string]string{"id": req.ID})
			writeJSON(w, http.StatusOK, map[string]string{"status": "reopened", "id": req.ID})
			return
		}

		// Default action: resolve
		var req struct {
			ID    string `json:"id"`
			Agent string `json:"agent"`
			Goal  string `json:"goal"`
			Note  string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
			writeError(w, http.StatusBadRequest, "invalid resolve request")
			return
		}
		if req.Agent == "" {
			req.Agent = "web-ui"
		}

		if err := s.cfg.Store.ResolveFeedback(req.ID, req.Agent, req.Goal, req.Note); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("resolve feedback: %v", err))
			return
		}

		s.events.Broadcast("feedback_resolved", req)
		writeJSON(w, http.StatusOK, map[string]string{"status": "resolved", "id": req.ID})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleADRs provides MADR listing, health checks, and memo promotion.
func (s *Server) handleADRs(w http.ResponseWriter, r *http.Request) {
	adrDir := filepath.Join(s.cfg.ProjectDir, dream.ADRDir)

	switch r.Method {
	case http.MethodGet:
		adrs, _ := dream.LoadADRs(adrDir)
		report, _ := dream.Doctor(adrDir)
		writeJSON(w, http.StatusOK, map[string]any{
			"adrs":   adrs,
			"report": report,
		})

	case http.MethodPost:
		var req struct {
			MemoID   string `json:"memo_id"`
			Deciders string `json:"deciders"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MemoID == "" {
			writeError(w, http.StatusBadRequest, "missing memo_id")
			return
		}

		path, adr, err := dream.FromMemo(s.cfg.Store, adrDir, s.cfg.ProjectID, req.MemoID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("promote memo to adr: %v", err))
			return
		}

		s.events.Broadcast("adr_created", adr)
		writeJSON(w, http.StatusCreated, map[string]any{
			"path": path,
			"adr":  adr,
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleVisionTriage runs automated proposal evaluation against non-goals.
func (s *Server) handleVisionTriage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Proposal string `json:"proposal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Proposal) == "" {
		writeError(w, http.StatusBadRequest, "proposal text is required")
		return
	}

	res, err := dream.TriageProposal(s.cfg.Store, s.cfg.ProjectID, s.cfg.ProjectDir, req.Proposal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("triage failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, res)
}

// handleGoals returns the roadmap audit matrix and task breakdown.
func (s *Server) handleGoals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	audit, err := goals.ScanGoals(s.cfg.ProjectDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan goals: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, audit)
}

// handleCrew returns active crewmates with zero-token process liveness.
func (s *Server) handleCrew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	mgr, err := crew.NewCrewManager(s.cfg.ProjectDir)
	if err != nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	crewmates, err := mgr.List()
	if err != nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	writeJSON(w, http.StatusOK, crewmates)
}

// handleShutdown stops the web server cleanly upon authorized POST request.
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "shutting_down",
		"message": "Sila web cockpit is shutting down cleanly.",
	})

	if s.cfg.OnShutdown != nil {
		go func() {
			time.Sleep(100 * time.Millisecond)
			s.cfg.OnShutdown()
		}()
	}
}

// handleVision returns current project non-goals, architectural invariants, and milestones.
func (s *Server) handleVision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	vFacts, err := s.cfg.Store.VisionFacts(s.cfg.ProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("read vision facts: %v", err))
		return
	}

	var nonGoals []map[string]any
	var invariants []map[string]any
	var milestones []map[string]any

	for _, f := range vFacts {
		item := map[string]any{
			"id":         f.ID,
			"claim":      f.Claim,
			"kind":       f.Kind,
			"created_at": f.FirstSeen,
		}
		if f.IsNonGoal() {
			nonGoals = append(nonGoals, item)
		} else if f.IsMilestone() {
			milestones = append(milestones, item)
		} else if f.IsInvariant() {
			invariants = append(invariants, item)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project_id": s.cfg.ProjectID,
		"non_goals":  nonGoals,
		"invariants": invariants,
		"milestones": milestones,
	})
}

// handleMCPServerCard serves the SEP-1649 MCP Server Card JSON.
func (s *Server) handleMCPServerCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	baseURL := getRequestBaseURL(r)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Link", mcp.DefaultLinkHeaders(baseURL))
	card := mcp.DefaultServerCard(version.Version)
	writeJSON(w, http.StatusOK, card)
}

// handleLinkset serves RFC 9727 linkset discovery JSON.
func (s *Server) handleLinkset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	baseURL := getRequestBaseURL(r)
	w.Header().Set("Content-Type", "application/linkset+json; charset=utf-8")
	w.Header().Set("Link", mcp.DefaultLinkHeaders(baseURL))
	linkset := mcp.DefaultLinkset(baseURL)
	writeJSON(w, http.StatusOK, linkset)
}

// handleLLMsTxt serves standard llms.txt markdown for AI agent discovery.
func (s *Server) handleLLMsTxt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	baseURL := getRequestBaseURL(r)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Link", mcp.DefaultLinkHeaders(baseURL))
	_, _ = w.Write([]byte(mcp.DefaultLLMsTxt(baseURL, version.Version)))
}

func getRequestBaseURL(r *http.Request) string {
	proto := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		proto = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s://%s", proto, host)
}
