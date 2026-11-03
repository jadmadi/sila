package loaders

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type agyRecord struct {
	StepIndex int             `json:"step_index"`
	Source    string          `json:"source"`
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	CreatedAt string          `json:"created_at"`
	Content   string          `json:"content"`
	ToolCalls json.RawMessage `json:"tool_calls"`
}

type agySummary struct {
	ConversationID    string
	Title             string
	Preview           string
	WorkspaceDir      string
	LastModifiedTime  string
	LastUserInputTime string
	StepCount         int
	Status            string
}

func parseWorkspaceURIs(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return ""
	}
	var uris []string
	if err := json.Unmarshal([]byte(raw), &uris); err == nil {
		for _, u := range uris {
			cand := strings.TrimSpace(u)
			cand = strings.TrimPrefix(cand, "file://")
			if unescaped, err := url.PathUnescape(cand); err == nil {
				cand = unescaped
			}
			if filepath.IsAbs(cand) {
				return filepath.Clean(cand)
			}
		}
	}
	cand := strings.TrimPrefix(raw, "file://")
	if unescaped, err := url.PathUnescape(cand); err == nil {
		cand = unescaped
	}
	if filepath.IsAbs(cand) {
		return filepath.Clean(cand)
	}
	return ""
}

func parseAgyTime(raw string) (time.Time, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "0001-01-01") {
		return time.Time{}, ""
	}
	iso := strings.Replace(raw, " ", "T", 1)
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		t, err = time.Parse(time.RFC3339, iso)
		if err != nil {
			return time.Time{}, ""
		}
	}
	utc := t.UTC()
	return utc, utc.Format(time.RFC3339)
}

func loadAgySummaries(dbPath string) (map[string]string, []agySummary, error) {
	wsMap := make(map[string]string)
	var summaries []agySummary

	db, err := openReadOnly(dbPath)
	if err != nil {
		return wsMap, summaries, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT conversation_id, title, preview, step_count, last_modified_time, workspace_uris, status, last_user_input_time
		FROM conversation_summaries
	`)
	if err != nil {
		return wsMap, summaries, err
	}
	defer rows.Close()

	for rows.Next() {
		var s agySummary
		var rawURIs string
		if err := rows.Scan(&s.ConversationID, &s.Title, &s.Preview, &s.StepCount, &s.LastModifiedTime, &rawURIs, &s.Status, &s.LastUserInputTime); err != nil {
			continue
		}
		s.WorkspaceDir = parseWorkspaceURIs(rawURIs)
		if s.WorkspaceDir != "" {
			wsMap[s.ConversationID] = s.WorkspaceDir
		}
		summaries = append(summaries, s)
	}
	return wsMap, summaries, nil
}

func agyRoots(root string) []string {
	seen := make(map[string]bool)
	var roots []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			seen[p] = true
			roots = append(roots, p)
		}
	}

	add(root)

	base := filepath.Base(root)
	dir := filepath.Dir(root)
	if base == "antigravity-cli" {
		add(filepath.Join(dir, "antigravity"))
	} else if base == "antigravity" {
		add(filepath.Join(dir, "antigravity-cli"))
	}
	return roots
}

// NewAgyLoader returns a loader over the Antigravity brain and SQLite tree. It
// scans every transcript.jsonl, extracts SQLite conversation_summaries.db for
// authoritative workspace URIs and archived sessions, and aggregates curated user
// and assistant content.
func NewAgyLoader(root string) Loader {
	return funcLoader{
		name: "agy",
		path: root,
		fn: func(_ string, project *string, wm string) ([]Message, string, error) {
			roots := agyRoots(root)
			var msgs []Message
			maxTS := wm

			// 1. Gather all conversation_summaries.db maps across roots
			wsMap := make(map[string]string)
			var allSummaries []agySummary
			seenSummaryCID := make(map[string]bool)

			for _, r := range roots {
				dbPath := filepath.Join(r, "conversation_summaries.db")
				if fi, err := os.Stat(dbPath); err == nil && !fi.IsDir() {
					m, sums, err := loadAgySummaries(dbPath)
					if err == nil {
						for cid, ws := range m {
							if wsMap[cid] == "" {
								wsMap[cid] = ws
							}
						}
						for _, s := range sums {
							if !seenSummaryCID[s.ConversationID] {
								seenSummaryCID[s.ConversationID] = true
								allSummaries = append(allSummaries, s)
							}
						}
					}
				}
			}

			// 2. Scan all transcript.jsonl across roots
			transcripts, walkErr := agyTranscripts(roots...)
			if walkErr != nil {
				return nil, wm, fmt.Errorf("walk agy roots: %w", walkErr)
			}
			seenTranscripts := make(map[string]bool)
			for _, p := range transcripts {
				cid := parseAgySessionID(p)
				if cid != "" {
					seenTranscripts[cid] = true
				}
				m, c, err := loadAgyStream(p, project, wm, wsMap)
				if err != nil {
					// A skipped transcript is an incomplete scan: report it and
					// leave the cursor alone rather than quietly advancing past
					// records this file still holds.
					return nil, wm, fmt.Errorf("scan %s: %w", p, err)
				}
				msgs = append(msgs, m...)
				if c > maxTS {
					maxTS = c
				}
			}

			// 3. Ingest archived / pruned conversations from summaries
			for _, s := range allSummaries {
				if seenTranscripts[s.ConversationID] {
					continue // Already ingested in full via transcript.jsonl
				}
				ws := s.WorkspaceDir
				if ws == "" {
					ws = wsMap[s.ConversationID]
				}
				if project != nil && *project != "" {
					if ws == "" || !strings.Contains(ws, *project) {
						continue
					}
				}

				t, tsStr := parseAgyTime(s.LastModifiedTime)
				if t.IsZero() {
					t, tsStr = parseAgyTime(s.LastUserInputTime)
				}
				if t.IsZero() {
					continue
				}
				if tsStr != "" && tsStr <= wm {
					continue
				}
				if tsStr != "" && tsStr > maxTS {
					maxTS = tsStr
				}

				content := s.Title
				if s.Preview != "" {
					if content != "" {
						content += "\n\n" + s.Preview
					} else {
						content = s.Preview
					}
				}
				if strings.TrimSpace(content) == "" {
					continue
				}

				msgs = append(msgs, Message{
					Tool:       "agy",
					SessionID:  s.ConversationID,
					ProjectDir: ws,
					TS:         t,
					Role:       "user",
					Content:    content,
				})
			}

			sort.SliceStable(msgs, func(i, j int) bool {
				return msgs[i].TS.Before(msgs[j].TS)
			})

			return msgs, maxTS, nil
		},
		count: countAgy,
	}
}

// countAgy counts curated records (non-empty content) across every
// transcript.jsonl and archived conversation summaries, honoring the same
// per-stream project (workspace) scoping that loadAgyStream applies.
func countAgy(root string, project *string) (int64, error) {
	roots := agyRoots(root)
	var n int64

	wsMap := make(map[string]string)
	var allSummaries []agySummary
	seenSummaryCID := make(map[string]bool)

	for _, r := range roots {
		dbPath := filepath.Join(r, "conversation_summaries.db")
		if fi, err := os.Stat(dbPath); err == nil && !fi.IsDir() {
			m, sums, err := loadAgySummaries(dbPath)
			if err == nil {
				for cid, ws := range m {
					if wsMap[cid] == "" {
						wsMap[cid] = ws
					}
				}
				for _, s := range sums {
					if !seenSummaryCID[s.ConversationID] {
						seenSummaryCID[s.ConversationID] = true
						allSummaries = append(allSummaries, s)
					}
				}
			}
		}
	}

	transcripts, walkErr := agyTranscripts(roots...)
	if walkErr != nil {
		return 0, fmt.Errorf("walk agy roots: %w", walkErr)
	}
	seenTranscripts := make(map[string]bool)
	for _, p := range transcripts {
		cid := parseAgySessionID(p)
		if cid != "" {
			seenTranscripts[cid] = true
		}
		workspace := wsMap[cid]
		sn := int64(0)
		if serr := ScanJSONLStream[agyRecord](p, func(r agyRecord) error {
			if r.Content == "" {
				return nil
			}
			sn++
			if workspace == "" {
				if ws := extractWorkspaceFromContent(r.Content); ws != "" {
					workspace = ws
				}
			}
			return nil
		}); serr != nil {
			return 0, serr
		}
		if project != nil && *project != "" && (workspace == "" || !strings.Contains(workspace, *project)) {
			continue
		}
		n += sn
	}

	for _, s := range allSummaries {
		if seenTranscripts[s.ConversationID] {
			continue
		}
		ws := s.WorkspaceDir
		if ws == "" {
			ws = wsMap[s.ConversationID]
		}
		if project != nil && *project != "" && (ws == "" || !strings.Contains(ws, *project)) {
			continue
		}
		content := s.Title + s.Preview
		if strings.TrimSpace(content) != "" {
			n++
		}
	}

	return n, nil
}

// agyTranscripts walks the brain trees collecting transcript.jsonl files.
func agyTranscripts(roots ...string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && d.Name() == "transcript.jsonl" {
				if !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
			return nil
		}); err != nil {
			// An unreadable subtree is not an empty one.
			return nil, fmt.Errorf("scan %s: %w", root, err)
		}
	}
	return out, nil
}

func parseAgySessionID(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		if part == "brain" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func extractWorkspaceFromContent(content string) string {
	// Look for [URI] -> [CorpusName] format
	lines := strings.SplitSeq(content, "\n")
	for l := range lines {
		l = strings.TrimSpace(l)
		if strings.Contains(l, " -> ") {
			parts := strings.Split(l, " -> ")
			cand := strings.TrimSpace(parts[0])
			if filepath.IsAbs(cand) {
				cand = filepath.Clean(cand)
				if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
					return cand
				}
			}
		}
	}
	return ""
}

func classifyAgyRole(r agyRecord) string {
	t := strings.ToUpper(r.Type)
	s := strings.ToUpper(r.Source)
	if t == "USER_INPUT" || t == "USER_EXPLICIT" || s == "USER_EXPLICIT" || s == "USER" {
		return "user"
	}
	if t == "PLANNER_RESPONSE" || t == "MODEL" || s == "MODEL" || s == "AGENT" {
		return "assistant"
	}
	return "assistant"
}

// loadAgyStream ingests one transcript.jsonl; watermark is the max created_at
// (ISO string, lexically ordered) already processed.
func loadAgyStream(path string, project *string, watermark string, wsMap map[string]string) ([]Message, string, error) {
	sessionID := parseAgySessionID(path)
	workspace := wsMap[sessionID]

	// Pass 1: find workspace from transcript header if not present in wsMap
	var records []agyRecord
	if serr := ScanJSONLStream[agyRecord](path, func(r agyRecord) error {
		if r.Content != "" {
			records = append(records, r)
			if workspace == "" {
				if ws := extractWorkspaceFromContent(r.Content); ws != "" {
					workspace = ws
				}
			}
		}
		return nil
	}); serr != nil {
		// An interrupted scan must not look like a complete one.
		return nil, watermark, serr
	}

	if project != nil && *project != "" {
		if workspace == "" || !strings.Contains(workspace, *project) {
			return nil, watermark, nil
		}
	}

	var msgs []Message
	maxTS := watermark
	for _, r := range records {
		if r.CreatedAt <= watermark {
			continue
		}
		if r.CreatedAt > maxTS {
			maxTS = r.CreatedAt
		}
		role := classifyAgyRole(r)
		ts, _ := time.Parse(time.RFC3339, strings.Replace(r.CreatedAt, " ", "T", 1))
		content := r.Content
		if role == "user" {
			content = StripAGYUserEnvelope(content)
		} else {
			content = StripAGYAssistantEnvelope(content)
		}
		msgs = append(msgs, Message{
			Tool:       "agy",
			SessionID:  sessionID,
			ProjectDir: workspace,
			TS:         ts.UTC(),
			Role:       role,
			Content:    content,
		})
	}
	return msgs, maxTS, nil
}

// StripAGYUserEnvelope returns the text a user actually typed, without the
// harness envelope Antigravity records around it.
//
// A recorded agy user turn looks like this:
//
//	<USER_REQUEST>
//	<the prompt>
//	</USER_REQUEST>
//	<ADDITIONAL_METADATA>…</ADDITIONAL_METADATA>
//	<USER_SETTINGS_CHANGE>…</USER_SETTINGS_CHANGE>
//
// Measured on the maintainer's store before this change: all 2,676 enveloped
// user turns began with <USER_REQUEST>, 2,670 closed it, none carried user
// text before the opening tag, and everything after the closing tag was
// harness metadata. So the pair is the whole of the user's turn and everything
// outside it is dropped. Six records are truncated without a closing tag; for
// those, the text after the opening tag is kept up to the first line that
// opens another harness block.
//
// Content carrying no envelope is returned unchanged, and an envelope that
// would strip to nothing is returned unchanged rather than emptied: a
// transformation that loses a turn is worse than one that leaves it noisy.
func StripAGYUserEnvelope(content string) string {
	const open, closeTag = "<USER_REQUEST>", "</USER_REQUEST>"
	before, after, ok := strings.Cut(content, open)
	if !ok {
		return content
	}
	rest := after
	if j := strings.Index(rest, closeTag); j >= 0 {
		rest = rest[:j]
	} else {
		rest = cutAtHarnessBlock(rest)
	}
	stripped := strings.TrimSpace(rest)
	if stripped == "" {
		return content
	}
	// Every recorded agy turn begins with the envelope, so this prefix is
	// normally empty. It is preserved when present because a record that does
	// not match the measured shape must not lose its leading text.
	if prefix := strings.TrimSpace(before); prefix != "" {
		return prefix + "\n" + stripped
	}
	return stripped
}

// StripAGYAssistantEnvelope removes the timestamp header Antigravity records at
// the start of every non-user turn.
//
// A recorded agy assistant turn looks like this:
//
//	Created At: 2026-08-31T04:22:04+03:00
//	Completed At: 2026-08-31T04:22:04+03:00
//	<the model's actual output>
//
// Measured on the maintainer's source store (213 transcripts, 32,859 records):
// 26,680 begin with "Created At:", every one of them followed by an RFC3339
// timestamp, 25,445 by a second "Completed At:" timestamp, and the remaining
// 1,235 by real content directly (background-task status, tool-call errors).
// No user turn begins with the header.
//
// The header is matched on whitespace boundaries rather than line boundaries
// because a pending claim is the message text with whitespace normalized, so
// the envelope arrives space-separated. Both stamps must parse as RFC3339, so
// content a human genuinely begins with the words "Created At:" is kept; an
// envelope that would strip to nothing is returned unchanged rather than
// emptied, matching StripAGYUserEnvelope.
var agyEnvelopeRe = regexp.MustCompile(
	`^Created At: (\S+)(?:\s+Completed At: (\S+))?\s*`)

// StripAGYAssistantEnvelope removes leading Created At / Completed At harness envelopes
// from assistant responses while leaving user content intact.
func StripAGYAssistantEnvelope(content string) string {
	m := agyEnvelopeRe.FindStringSubmatch(content)
	if m == nil {
		return content
	}
	if !isAGYTimestamp(m[1]) {
		return content
	}
	if m[2] != "" && !isAGYTimestamp(m[2]) {
		return content
	}
	rest := strings.TrimSpace(content[len(m[0]):])
	if rest == "" {
		return content
	}
	return rest
}

// isAGYTimestamp reports whether s is the RFC3339 stamp the harness writes.
// Requiring a parseable timestamp is what stops the strip from eating a turn a
// human happened to begin with "Created At:".
func isAGYTimestamp(s string) bool {
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

// cutAtHarnessBlock truncates s at the first line that opens a harness metadata
// block, used only for records that never closed their <USER_REQUEST>.
func cutAtHarnessBlock(s string) string {
	for idx := 0; idx < len(s); {
		line := s[idx:]
		next := strings.IndexByte(line, '\n')
		if next < 0 {
			if isHarnessBlockLine(line) {
				return s[:idx]
			}
			break
		}
		if isHarnessBlockLine(line[:next]) {
			return s[:idx]
		}
		idx += next + 1
	}
	return s
}

// isHarnessBlockLine reports whether a line is an upper-case tag such as
// <ADDITIONAL_METADATA> rather than anything a person would type.
func isHarnessBlockLine(line string) bool {
	t := strings.TrimSpace(line)
	if len(t) < 3 || t[0] != '<' {
		return false
	}
	end := strings.IndexByte(t, '>')
	if end <= 1 {
		return false
	}
	for _, r := range t[1:end] {
		if r != '_' && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}
