package loaders

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// claudeRecord is one line of a Claude Code session transcript. Only the
// `user` and `assistant` record types carry conversation; the other seven
// (`system`, `attachment`, `file-history-snapshot`, `last-prompt`, `mode`,
// `permission-mode`, `queue-operation`) are session bookkeeping.
type claudeRecord struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	IsMeta    bool   `json:"isMeta"`
	// isApiErrorMessage marks a synthetic assistant record such as
	// "Not logged in · Please run /login" — an error notice, not model output.
	IsAPIError bool `json:"isApiErrorMessage"`
	Message    struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// claudeContentBlock is one entry when message.content is a block list.
type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// NewClaudeCodeLoader returns a loader over Claude Code's project transcripts.
func NewClaudeCodeLoader(path string) Loader {
	return funcLoader{
		name: "claude-code",
		path: path,
		fn: func(p string, project *string, wm string) ([]Message, string, error) {
			return loadClaudeCode(p, project, wm)
		},
		count: countClaudeCode,
	}
}

// claudeTranscripts lists `<root>/<slugified-cwd>/<sessionId>.jsonl`.
func claudeTranscripts(root string) ([]string, error) {
	var out []string
	walkErr := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(p) != ".jsonl" {
			return nil
		}
		out = append(out, p)
		return nil
	})
	return out, walkErr
}

// loadClaudeCode streams Claude Code transcripts. Curation follows the same
// rule as the other loaders: keep text a human or the model actually produced.
//
//   - Only `user` and `assistant` records are read.
//   - `last-prompt` records carry a copy of the prompt text and would
//     double-count every turn, so they are skipped with the other bookkeeping
//     types.
//   - `isMeta: true` records are context injected into the model, not user
//     speech, and are skipped; so are synthetic API-error notices.
//   - `message.content` is either a bare string or a list of typed blocks; only
//     `text` blocks are kept.
//
// The cursor is the newest ISO-8601 timestamp seen, compared inclusively:
// re-emitting the boundary record costs nothing because exact content dedup
// absorbs it.
func loadClaudeCode(root string, project *string, watermark string) ([]Message, string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, watermark, errMissingDB
	}

	// Compare parsed instants, not strings: RFC3339Nano trims trailing zeros,
	// so lexical comparison would mis-order mixed-precision timestamps.
	wmTime, _ := time.Parse(time.RFC3339Nano, watermark)
	var maxTS time.Time

	var msgs []Message
	files, walkErr := claudeTranscripts(root)
	if walkErr != nil {
		return nil, watermark, fmt.Errorf("walk %s: %w", root, walkErr)
	}
	for _, file := range files {
		lineNo := int64(0)
		if serr := ScanJSONL(file, func(line []byte) error {
			lineNo++
			var rec claudeRecord
			if json.Unmarshal(line, &rec) != nil {
				return nil
			}
			if rec.Type != "user" && rec.Type != "assistant" {
				return nil
			}
			if rec.IsMeta || rec.IsAPIError {
				return nil
			}
			text := strings.TrimSpace(claudeContentText(rec.Message.Content))
			if text == "" {
				return nil
			}
			ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
			if err != nil {
				return nil
			}
			if !wmTime.IsZero() && ts.Before(wmTime) {
				return nil
			}
			role := rec.Message.Role
			if role == "" {
				role = rec.Type
			}
			if ts.After(maxTS) {
				maxTS = ts
			}
			msgs = append(msgs, Message{
				Tool:           "claude-code",
				SessionID:      rec.SessionID,
				ProjectDir:     rec.Cwd,
				TS:             ts.UTC(),
				Role:           role,
				Content:        text,
				SourceLocation: file,
				SourceOrder:    lineNo,
			})
			return nil
		}); serr != nil {
			// An interrupted scan must not look like a complete one, and the
			// error has to name the file or the user cannot act on it.
			return nil, watermark, fmt.Errorf("scan %s: %w", file, serr)
		}
	}
	newWM := watermark
	if maxTS.After(wmTime) {
		// Fixed-width so the stored cursor keeps a stable, sortable shape.
		newWM = maxTS.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	if newWM == "0" {
		newWM = ""
	}
	return msgs, newWM, nil
}

// claudeContentText extracts text from either content shape: a bare JSON
// string, or a list of typed blocks of which only `text` counts.
func claudeContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var bare string
	if json.Unmarshal(raw, &bare) == nil {
		return bare
	}
	var blocks []claudeContentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// countClaudeCode counts conversation records without deserializing content
// past the point of classifying a line. It is an upper bound on what a full
// scan emits, keeping assess's Total >= New invariant.
func countClaudeCode(root string, _ *string) (int64, error) {
	if _, err := os.Stat(root); err != nil {
		return 0, errMissingDB
	}
	files, walkErr := claudeTranscripts(root)
	if walkErr != nil {
		return 0, fmt.Errorf("walk %s: %w", root, walkErr)
	}
	var n int64
	for _, file := range files {
		if serr := ScanJSONL(file, func(line []byte) error {
			var rec claudeRecord
			if json.Unmarshal(line, &rec) != nil {
				return nil
			}
			if rec.Type != "user" && rec.Type != "assistant" {
				return nil
			}
			if rec.IsMeta || rec.IsAPIError {
				return nil
			}
			if strings.TrimSpace(claudeContentText(rec.Message.Content)) == "" {
				return nil
			}
			n++
			return nil
		}); serr != nil {
			return 0, serr
		}
	}
	return n, nil
}
