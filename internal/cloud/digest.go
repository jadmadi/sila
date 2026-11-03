package cloud

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// Turn represents one parsed turn in a conversation.
type Turn struct {
	Role    string
	Content string
}

var (
	// Turn header matchers (e.g. "### User", "### Assistant", "**User**:", "User:", "Gemini:")
	headerPattern = regexp.MustCompile(`(?m)^(?:#{1,4}\s*|\*\*|__)?(User|Assistant|Model|Gemini|Human)(?:\*\*|__)?:\s*`)
)

// ParseConversationTurns decomposes raw conversation content into role-attributed turns.
func ParseConversationTurns(content string) []Turn {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	locs := headerPattern.FindAllStringIndex(content, -1)
	if len(locs) == 0 {
		return []Turn{
			{Role: "assistant", Content: content},
		}
	}

	var turns []Turn
	for i := range locs {
		start := locs[i][0]
		matchEnd := locs[i][1]
		headerText := content[start:matchEnd]

		role := "assistant"
		lowerHeader := strings.ToLower(headerText)
		if strings.Contains(lowerHeader, "user") || strings.Contains(lowerHeader, "human") {
			role = "user"
		}

		end := len(content)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}

		turnBody := strings.TrimSpace(content[matchEnd:end])
		if turnBody != "" {
			turns = append(turns, Turn{
				Role:    role,
				Content: turnBody,
			})
		}
	}

	if len(turns) == 0 {
		return []Turn{
			{Role: "assistant", Content: content},
		}
	}

	return turns
}

// Digester coordinates draining items from the edge mailbox into the local SQLite store.
type Digester struct {
	Store *store.Store
}

// NewDigester constructs a new cloud digester.
func NewDigester(st *store.Store) *Digester {
	return &Digester{Store: st}
}

// IngestResult records the result of ingesting one inbound conversation item.
type IngestResult struct {
	ItemID          string
	NewMessages     int
	SkippedMessages int
	Error           error
}

// IngestConversation parses an inbound conversation and commits it to local SQLite.
func (d *Digester) IngestConversation(ctx context.Context, item InboundItem) (IngestResult, error) {
	if d.Store == nil {
		return IngestResult{ItemID: item.ID}, fmt.Errorf("local store is nil")
	}

	// 1. Resolve project
	projectID := strings.TrimSpace(item.Project)
	if projectID == "" {
		projectID = "default"
	}
	if proj, err := d.Store.ResolveProject(projectID); err == nil {
		projectID = proj.ID
	}

	// Absorb remote HLC if present to advance local causal clock
	if item.HLC != "" {
		_ = store.ObserveHLC(item.HLC)
	}

	// 2. Canonical tool source
	sourceTool := strings.ToLower(strings.TrimSpace(item.Source))
	if sourceTool == "" {
		sourceTool = "gemini"
	}

	// 3. Parse timestamp
	ts := time.Now().UTC()
	if item.CreatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, item.CreatedAt); err == nil {
			ts = parsed.UTC()
		}
	}

	// 4. Parse turns
	turns := ParseConversationTurns(item.Content)
	if len(turns) == 0 {
		return IngestResult{ItemID: item.ID}, nil
	}

	// 5. Prepare LoadMessages
	loadMessages := make([]store.LoadMessage, 0, len(turns))
	for i, turn := range turns {
		turnTS := ts.Add(time.Duration(i) * time.Second)
		hash := store.ContentHash(turn.Role, turn.Content)
		loadMessages = append(loadMessages, store.LoadMessage{
			ProjectID:      projectID,
			ContentHash:    hash,
			TS:             turnTS.Format(time.RFC3339),
			Role:           turn.Role,
			Content:        turn.Content,
			Tool:           sourceTool,
			SessionID:      item.ID,
			SourceLocation: "cloud:" + item.ID,
			SourceOrder:    int64(i + 1),
			SourceTime:     store.SortableTime(turnTS),
		})
	}

	// 6. WriteRunScoped into SQLite store
	runID := fmt.Sprintf("cloud_%s_%d", item.ID, ts.Unix())
	watermarkKey := fmt.Sprintf("cloud:%s:%s", sourceTool, projectID)
	newRec, _, err := d.Store.WriteRunScoped(runID, sourceTool, watermarkKey, item.CreatedAt, loadMessages)
	if err != nil {
		return IngestResult{ItemID: item.ID, Error: err}, err
	}

	return IngestResult{
		ItemID:          item.ID,
		NewMessages:     newRec,
		SkippedMessages: len(loadMessages) - newRec,
	}, nil
}

// DigestSummary aggregates the results of a drain and digest cycle.
type DigestSummary struct {
	TotalFetched     int      `json:"total_fetched"`
	IngestedMessages int      `json:"ingested_messages"`
	SkippedMessages  int      `json:"skipped_messages"`
	AckedItems       int      `json:"acked_items"`
	Errors           []string `json:"errors,omitempty"`
}

// DrainAndDigest drains pending items from the mailbox, commits them locally, and acks.
func (d *Digester) DrainAndDigest(ctx context.Context, client *Client, projectFilter string, limit int) (DigestSummary, error) {
	items, sessionToken, err := client.DrainInboundWithSession(ctx, projectFilter, limit)
	if err != nil {
		return DigestSummary{}, err
	}

	summary := DigestSummary{
		TotalFetched: len(items),
	}
	if len(items) == 0 {
		return summary, nil
	}

	var ackIDs []string
	for _, item := range items {
		res, err := d.IngestConversation(ctx, item)
		if err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("item %s: %v", item.ID, err))
			continue // Do not acknowledge items that failed local persistence
		}
		summary.IngestedMessages += res.NewMessages
		summary.SkippedMessages += res.SkippedMessages
		ackIDs = append(ackIDs, item.ID)
	}

	if len(ackIDs) > 0 {
		acked, err := client.AckInboundWithSession(ctx, ackIDs, sessionToken)
		if err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("ack error: %v", err))
		}
		summary.AckedItems = acked
	}

	return summary, nil
}
