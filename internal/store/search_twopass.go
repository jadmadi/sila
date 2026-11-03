package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SearchPassOptions encapsulates configuration for two-pass asymmetric message retrieval.
type SearchPassOptions struct {
	Project           string  `json:"project,omitempty"`
	Tool              string  `json:"tool,omitempty"`
	Role              string  `json:"role,omitempty"`
	Since             string  `json:"since,omitempty"`
	UserRoleWeight    float64 `json:"user_role_weight,omitempty"`    // Default: 2.5
	AssistantWeight   float64 `json:"assistant_weight,omitempty"`    // Default: 1.0
	CandidateLimit    int     `json:"candidate_limit,omitempty"`     // Default: 10 sessions
	ContextTurnWindow int     `json:"context_turn_window,omitempty"` // Default: 2 (±2 turns)
	MaxTurnTokens     int     `json:"max_turn_tokens,omitempty"`     // Default: 800 tokens
}

// MessageTurn represents a single conversational turn in an unrolled exchange.
type MessageTurn struct {
	ID          int64    `json:"id"`
	Role        string   `json:"role"`
	Content     string   `json:"content"`
	Timestamp   string   `json:"timestamp"`
	SourceTools []string `json:"source_tools,omitempty"`
}

// ResolvedExchange packages a candidate session exchange with high-signal context.
type ResolvedExchange struct {
	SessionID         string        `json:"session_id"`
	MatchedTurnID     string        `json:"matched_turn_id"`
	UserPrompt        string        `json:"user_prompt"`
	AssistantFix      string        `json:"assistant_fix"`
	SurroundingTurns  []MessageTurn `json:"surrounding_turns"`
	FusedScore        float64       `json:"fused_score"`
	ProjectID         string        `json:"project_id,omitempty"`
	Timestamp         string        `json:"timestamp,omitempty"`
	FormattedExchange string        `json:"formatted_exchange,omitempty"`
}

// Format returns clearly delineated turn boundaries for the immediate question-and-answer exchange.
func (ex ResolvedExchange) Format() string {
	var sb strings.Builder
	if ex.UserPrompt != "" {
		sb.WriteString("User: ")
		sb.WriteString(ex.UserPrompt)
	}
	if ex.AssistantFix != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("Assistant: ")
		sb.WriteString(ex.AssistantFix)
	}
	return sb.String()
}

// FormatAllTurns returns all surrounding turns with clear role and timestamp delimiters.
func (ex ResolvedExchange) FormatAllTurns() string {
	var sb strings.Builder
	for i, turn := range ex.SurroundingTurns {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		roleTitle := strings.ToUpper(turn.Role[:1]) + strings.ToLower(turn.Role[1:])
		sb.WriteString(fmt.Sprintf("%s: %s", roleTitle, turn.Content))
	}
	return sb.String()
}

var codeBlockRegex = regexp.MustCompile("(?s)```[a-zA-Z0-9_-]*\n?(.*?)```")

// ExtractResolvingSnippet isolates the precise code block or resolving sentences in an assistant response.
func ExtractResolvingSnippet(content string, queryTerms []string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	// If the entire message is concise (e.g. <= 4 lines and < 400 chars), return it as-is.
	lines := strings.Split(content, "\n")
	if len(lines) <= 4 && len(content) < 400 {
		return content
	}

	// Check for code blocks
	matches := codeBlockRegex.FindAllStringSubmatch(content, -1)
	if len(matches) > 0 {
		for _, m := range matches {
			blockContent := strings.TrimSpace(m[1])
			if len(blockContent) == 0 {
				continue
			}
			lowerBlock := strings.ToLower(blockContent)
			for _, term := range queryTerms {
				if len(term) >= 2 && strings.Contains(lowerBlock, strings.ToLower(term)) {
					return m[0]
				}
			}
		}
		if len(content) > 1000 && len(matches[0][0]) > 0 {
			return matches[0][0]
		}
	}

	// Look for sentences matching query terms
	sentences := splitSentences(content)
	var matchingSentences []string
	for _, s := range sentences {
		sLower := strings.ToLower(s)
		for _, term := range queryTerms {
			if len(term) >= 2 && strings.Contains(sLower, strings.ToLower(term)) {
				matchingSentences = append(matchingSentences, strings.TrimSpace(s))
				break
			}
		}
	}
	if len(matchingSentences) > 0 {
		return strings.Join(matchingSentences, " ")
	}

	return content
}

func splitSentences(content string) []string {
	var sentences []string
	var cur strings.Builder
	for i := 0; i < len(content); i++ {
		cur.WriteByte(content[i])
		if (content[i] == '.' || content[i] == '!' || content[i] == '?') &&
			(i+1 == len(content) || content[i+1] == ' ' || content[i+1] == '\n' || content[i+1] == '\r') {
			s := strings.TrimSpace(cur.String())
			if len(s) > 0 {
				sentences = append(sentences, s)
			}
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		s := strings.TrimSpace(cur.String())
		if len(s) > 0 {
			sentences = append(sentences, s)
		}
	}
	return sentences
}

// FormatPreview bounds content to maxTokens (default 800) and preserves query-matching snippets.
func FormatPreview(content string, maxTokens int, queryTerms []string) string {
	if maxTokens <= 0 {
		maxTokens = 800
	}
	maxChars := maxTokens * 4
	if len(content) <= maxChars {
		return content
	}

	lowerContent := strings.ToLower(content)
	earliestMatch := -1
	for _, term := range queryTerms {
		term = strings.ToLower(strings.TrimSpace(term))
		if len(term) < 2 {
			continue
		}
		pos := strings.Index(lowerContent, term)
		if pos != -1 {
			if earliestMatch == -1 || pos < earliestMatch {
				earliestMatch = pos
			}
		}
	}

	if earliestMatch == -1 {
		return strings.TrimSpace(content[:maxChars]) + " ... [truncated]"
	}

	half := maxChars / 2
	start := earliestMatch - half
	if start < 0 {
		start = 0
	}
	end := start + maxChars
	if end > len(content) {
		end = len(content)
		start = end - maxChars
		if start < 0 {
			start = 0
		}
	}

	clamped := content[start:end]
	if start > 0 {
		clamped = "... " + strings.TrimLeft(clamped, " \t\r\n")
	}
	if end < len(content) {
		clamped = strings.TrimRight(clamped, " \t\r\n") + " ... [truncated]"
	}
	return clamped
}

func scoreTurn(content string, terms []string) float64 {
	lower := strings.ToLower(content)
	sc := 0.0
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if len(term) < 2 {
			continue
		}
		if strings.Contains(lower, term) {
			sc += 1.0
		}
	}
	return sc
}

// SearchTwoPass executes two-pass asymmetric candidate retrieval and turn-dilution shielding.
// Pass 1 targets high-signal turns (user prompts, tool actions, commits weighted 2.5x over assistant prose)
// to identify top distinct session candidates.
// Pass 2 expands the chronological exchange window, performs local re-scoring, and bounds turns.
func (s *Store) SearchTwoPass(ctx context.Context, query string, opts SearchPassOptions) ([]ResolvedExchange, error) {
	if opts.UserRoleWeight <= 0 {
		opts.UserRoleWeight = 2.5
	}
	if opts.AssistantWeight <= 0 {
		opts.AssistantWeight = 1.0
	}
	if opts.CandidateLimit <= 0 {
		opts.CandidateLimit = 10
	}
	if opts.ContextTurnWindow <= 0 {
		opts.ContextTurnWindow = 2
	}
	if opts.MaxTurnTokens <= 0 {
		opts.MaxTurnTokens = 800
	}
	if ctx == nil {
		ctx = context.Background()
	}

	target, cleanQuery := ParseTemporalAnchor(query, time.Now().UTC())
	effectiveQuery := query
	if target != nil && strings.TrimSpace(cleanQuery) != "" {
		effectiveQuery = cleanQuery
	}
	terms := strings.Fields(strings.ToLower(effectiveQuery))
	ftsQ := FormatFTS5Query(effectiveQuery)
	if ftsQ == "" {
		return nil, nil
	}

	matching, err := s.MatchingProjectIDs(opts.Project)
	if err != nil {
		return nil, err
	}
	if opts.Project != "" && len(matching) == 0 {
		return nil, nil
	}

	sqlQ := `
		SELECT m.id, m.project_id, m.content_hash, m.ts, m.role, m.content, m.source_tools, m.first_seen,
			COALESCE((SELECT o.session_id FROM source_occurrences o WHERE o.message_id = m.id AND o.session_id != '' ORDER BY o.id LIMIT 1), '') AS session_id,
			bm25(messages_fts) AS raw_bm25,
			((CASE WHEN LOWER(m.role) IN ('user', 'tool', 'commit') THEN ? ELSE ? END) * bm25(messages_fts)) AS weighted_score
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ?`
	args := []any{opts.UserRoleWeight, opts.AssistantWeight, ftsQ}

	if len(matching) == 1 {
		sqlQ += ` AND m.project_id = ?`
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = strings.TrimSuffix(placeholders, ",")
		sqlQ += ` AND m.project_id IN (` + placeholders + `)`
		for _, id := range matching {
			args = append(args, id)
		}
	}

	if opts.Tool != "" {
		sqlQ += ` AND m.source_tools LIKE ?`
		args = append(args, "%"+opts.Tool+"%")
	}
	if opts.Role != "" {
		sqlQ += ` AND LOWER(m.role) = LOWER(?)`
		args = append(args, opts.Role)
	}
	if opts.Since != "" {
		sqlQ += ` AND m.ts >= ?`
		args = append(args, opts.Since)
	}

	sqlQ += ` ORDER BY weighted_score ASC, m.ts DESC LIMIT ?`
	args = append(args, max(100, opts.CandidateLimit*10))

	type rawHit struct {
		msg           StoredMessage
		sessionID     string
		rawBM25       float64
		weightedScore float64
		fusedScore    float64
	}

	executePass1 := func() ([]rawHit, error) {
		qCtx, cancel := context.WithTimeout(ctx, FTSQueryTimeout)
		defer cancel()

		rows, err := s.DB.QueryContext(qCtx, sqlQ, args...)
		if err != nil {
			if qCtx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("fts5 pass 1 query timed out after %v: %w", FTSQueryTimeout, context.DeadlineExceeded)
			}
			return nil, err
		}
		defer rows.Close()

		var hits []rawHit
		for rows.Next() {
			var h rawHit
			var tools string
			if err := rows.Scan(
				&h.msg.ID, &h.msg.ProjectID, &h.msg.ContentHash, &h.msg.TS,
				&h.msg.Role, &h.msg.Content, &tools, &h.msg.FirstSeen,
				&h.sessionID, &h.rawBM25, &h.weightedScore,
			); err != nil {
				return nil, err
			}
			h.msg.SourceTools = unjoinJSON(tools)
			h.fusedScore = -1.0 * h.weightedScore

			if target != nil {
				if tDoc, ok := ParseDocTimestamp(h.msg.TS); ok {
					boost := CalculateTemporalBoost(tDoc, target)
					h.fusedScore += boost * 2.0
				}
			}
			hits = append(hits, h)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return hits, nil
	}

	hits, err := executePass1()
	if err != nil && isFTSCorruption(err) {
		if healErr := s.RebuildFTSIndex("messages_fts"); healErr == nil {
			hits, err = executePass1()
		}
	}
	if err != nil {
		return nil, err
	}

	if target != nil && len(hits) > 1 {
		sort.SliceStable(hits, func(i, j int) bool {
			return hits[i].fusedScore > hits[j].fusedScore
		})
	}

	// Group by session_id to deduplicate multiple turn matches within the same session
	var candidates []rawHit
	seenSessions := make(map[string]bool)
	for _, hit := range hits {
		sess := hit.sessionID
		if sess != "" {
			if seenSessions[sess] {
				continue
			}
			seenSessions[sess] = true
		}
		candidates = append(candidates, hit)
		if len(candidates) >= opts.CandidateLimit {
			break
		}
	}

	// Pass 2: Context window expansion & local re-scoring
	var exchanges []ResolvedExchange
	for _, cand := range candidates {
		msgCtx, err := s.ExpandSessionExchange(ctx, cand.sessionID, fmt.Sprintf("%d", cand.msg.ID), opts.ContextTurnWindow)
		if err != nil || msgCtx == nil {
			clamped := FormatPreview(cand.msg.Content, opts.MaxTurnTokens, terms)
			uPrompt := ""
			aFix := ""
			if strings.EqualFold(cand.msg.Role, "user") {
				uPrompt = clamped
			} else {
				aFix = clamped
			}
			ex := ResolvedExchange{
				SessionID:     cand.sessionID,
				MatchedTurnID: fmt.Sprintf("%d", cand.msg.ID),
				UserPrompt:    uPrompt,
				AssistantFix:  aFix,
				SurroundingTurns: []MessageTurn{
					{
						ID:          cand.msg.ID,
						Role:        cand.msg.Role,
						Content:     clamped,
						Timestamp:   cand.msg.TS,
						SourceTools: cand.msg.SourceTools,
					},
				},
				FusedScore: cand.fusedScore,
				ProjectID:  cand.msg.ProjectID,
				Timestamp:  cand.msg.TS,
			}
			ex.FormattedExchange = ex.Format()
			exchanges = append(exchanges, ex)
			continue
		}

		var allMsgs []StoredMessage
		allMsgs = append(allMsgs, msgCtx.Before...)
		allMsgs = append(allMsgs, msgCtx.TargetMessage)
		allMsgs = append(allMsgs, msgCtx.After...)

		surroundingTurns := make([]MessageTurn, len(allMsgs))
		for i, m := range allMsgs {
			surroundingTurns[i] = MessageTurn{
				ID:          m.ID,
				Role:        m.Role,
				Content:     FormatPreview(m.Content, opts.MaxTurnTokens, terms),
				Timestamp:   m.TS,
				SourceTools: m.SourceTools,
			}
		}

		// Identify UserPrompt
		var userPrompt string
		if strings.EqualFold(msgCtx.TargetMessage.Role, "user") {
			userPrompt = msgCtx.TargetMessage.Content
		} else {
			for i := len(msgCtx.Before) - 1; i >= 0; i-- {
				if strings.EqualFold(msgCtx.Before[i].Role, "user") {
					userPrompt = msgCtx.Before[i].Content
					break
				}
			}
			if userPrompt == "" {
				for _, m := range msgCtx.After {
					if strings.EqualFold(m.Role, "user") {
						userPrompt = m.Content
						break
					}
				}
			}
		}
		if userPrompt == "" && strings.EqualFold(msgCtx.TargetMessage.Role, "user") {
			userPrompt = msgCtx.TargetMessage.Content
		}

		// Identify AssistantFix via local re-scoring
		var bestAssistant string
		bestAssistantScore := -1.0
		for _, m := range allMsgs {
			if !strings.EqualFold(m.Role, "assistant") {
				continue
			}
			sc := scoreTurn(m.Content, terms)
			if strings.Contains(m.Content, "```") {
				sc += 3.0
			}
			if m.ID > cand.msg.ID {
				sc += 1.0
			}
			if sc > bestAssistantScore {
				bestAssistantScore = sc
				bestAssistant = m.Content
			}
		}

		if bestAssistant == "" && strings.EqualFold(msgCtx.TargetMessage.Role, "assistant") {
			bestAssistant = msgCtx.TargetMessage.Content
		}

		assistantFix := ExtractResolvingSnippet(bestAssistant, terms)
		if assistantFix == "" {
			assistantFix = bestAssistant
		}

		userPrompt = FormatPreview(userPrompt, opts.MaxTurnTokens, terms)
		assistantFix = FormatPreview(assistantFix, opts.MaxTurnTokens, terms)

		ex := ResolvedExchange{
			SessionID:        cand.sessionID,
			MatchedTurnID:    fmt.Sprintf("%d", cand.msg.ID),
			UserPrompt:       userPrompt,
			AssistantFix:     assistantFix,
			SurroundingTurns: surroundingTurns,
			FusedScore:       cand.fusedScore,
			ProjectID:        cand.msg.ProjectID,
			Timestamp:        cand.msg.TS,
		}
		ex.FormattedExchange = ex.Format()
		exchanges = append(exchanges, ex)
	}

	return exchanges, nil
}
