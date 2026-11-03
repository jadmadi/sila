package loaders

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type codexRecord struct {
	SessionID string  `json:"session_id"`
	TS        float64 `json:"ts"`
	Role      string  `json:"role"`
	Text      string  `json:"text"`
}

// codexRolloutScan is the result of walking Codex's sessions tree: the
// session_id -> cwd map used for history attribution, plus the assistant
// response_item messages recovered from the rollouts themselves (history.jsonl
// records user prompts only; the answers live in the rollout logs).
type codexRolloutScan struct {
	cwdBySession map[string]string
	messages     []Message
	err          error // first scan failure; a partial scan is not a complete scan
}

// codexFlexTime decodes a rollout timestamp that may be an RFC3339 string or
// epoch seconds.
type codexFlexTime struct{ T time.Time }

// UnmarshalJSON decodes an RFC3339 string or Unix epoch float into a time.Time.
func (c *codexFlexTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		sec := int64(f)
		c.T = time.Unix(sec, int64((f-float64(sec))*1e9)).UTC()
		return nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		c.T = t.UTC()
	}
	return nil
}

// scanCodexRollouts walks the Codex sessions tree once, recovering session
// cwd metadata and importing assistant response_item messages with their real
// session id, timestamp, and working directory.
func scanCodexRollouts(sessionsRoot string) codexRolloutScan {
	out := codexRolloutScan{cwdBySession: map[string]string{}}
	if werr := filepath.WalkDir(sessionsRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".jsonl" {
			return nil
		}
		type rolloutMessage struct {
			Type      string        `json:"type"`
			Timestamp codexFlexTime `json:"timestamp"`
			Ordinal   int64         `json:"ordinal"`
			Payload   struct {
				ID      string `json:"id"`
				Cwd     string `json:"cwd"`
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		var curSession, curCwd string
		if serr := ScanJSONLStream[rolloutMessage](p, func(m rolloutMessage) error {
			switch m.Type {
			case "session_meta":
				if m.Payload.ID != "" {
					curSession, curCwd = m.Payload.ID, m.Payload.Cwd
					out.cwdBySession[curSession] = curCwd
				}
			case "response_item":
				if m.Payload.Type != "message" || m.Payload.Role != "assistant" || curSession == "" {
					return nil
				}
				var sb strings.Builder
				for _, part := range m.Payload.Content {
					if part.Type == "output_text" && part.Text != "" {
						sb.WriteString(part.Text)
					}
				}
				text := strings.TrimSpace(sb.String())
				if text == "" || m.Timestamp.T.IsZero() {
					return nil
				}
				out.messages = append(out.messages, Message{
					Tool:           "codex",
					SessionID:      curSession,
					ProjectDir:     curCwd,
					TS:             m.Timestamp.T,
					Role:           "assistant",
					Content:        text,
					SourceLocation: p,
					SourceOrder:    m.Ordinal,
				})
			}
			return nil
		}); serr != nil && out.err == nil {
			out.err = fmt.Errorf("scan %s: %w", p, serr)
		}
		return nil
	}); werr != nil && !os.IsNotExist(werr) && out.err == nil {
		// A missing sessions/ tree simply means this install has no rollouts;
		// any other traversal failure means the scan is incomplete.
		out.err = fmt.Errorf("walk %s: %w", sessionsRoot, werr)
	}
	sort.SliceStable(out.messages, func(i, j int) bool {
		return out.messages[i].TS.Before(out.messages[j].TS)
	})
	return out
}

// NewCodexLoader returns a loader over the Codex history.jsonl and its
// sibling rollout logs.
func NewCodexLoader(path string) Loader {
	return funcLoader{
		name: "codex",
		path: path,
		fn: func(p string, _ *string, wm string) ([]Message, string, error) {
			return loadCodex(p, wm)
		},
		count: countCodex,
	}
}

// countCodex counts curated records (non-empty text) in history.jsonl plus
// assistant rollout messages, without buffering message content.
func countCodex(path string, _ *string) (int64, error) {
	var n int64
	err := ScanJSONLStream[codexRecord](path, func(r codexRecord) error {
		if r.Text != "" {
			n++
		}
		return nil
	})
	scan := scanCodexRollouts(filepath.Join(filepath.Dir(path), "sessions"))
	if err == nil {
		err = scan.err
	}
	return n + int64(len(scan.messages)), err
}

// loadCodex streams Codex history.jsonl plus the sibling rollout logs.
// watermark is the max source timestamp (seconds, sub-second precision
// preserved) already ingested; records are re-emitted when their timestamp is
// at or after the watermark so same-second appends are never silently
// dropped — exact duplicates are absorbed downstream by content hashing.
func loadCodex(path string, watermark string) ([]Message, string, error) {
	threshold, _ := strconv.ParseFloat(watermark, 64)
	// History timestamps are whole seconds while rollout timestamps are
	// sub-second precise, and both streams share one cursor. Comparing history
	// against the exact threshold lets a rollout answer at T+0.5 hide a history
	// prompt stamped T, so history compares against the floored cursor — the
	// coarsest stream precision. Re-emission is free: content hashing absorbs
	// duplicates downstream.
	historyThreshold := math.Floor(threshold)
	rolloutRoot := filepath.Join(filepath.Dir(path), "sessions")
	scan := scanCodexRollouts(rolloutRoot)
	if scan.err != nil {
		// An incomplete scan must not look like a complete one: report it and
		// leave the cursor where it was.
		return nil, watermark, scan.err
	}

	var msgs []Message
	for _, m := range scan.messages {
		if float64(m.TS.UnixNano())/1e9 < threshold {
			continue
		}
		msgs = append(msgs, m)
	}

	err := ScanJSONLStream[codexRecord](path, func(r codexRecord) error {
		if r.Text == "" || r.TS < historyThreshold {
			return nil
		}
		role := r.Role
		if role == "" {
			// history.jsonl records are the user's prompts; only carry an
			// explicit role when the source provides one.
			role = "user"
		}
		msgs = append(msgs, Message{
			Tool: "codex", SessionID: r.SessionID,
			ProjectDir:     scan.cwdBySession[r.SessionID],
			TS:             time.Unix(int64(r.TS), 0).UTC(),
			Role:           role,
			Content:        r.Text,
			SourceLocation: path,
		})
		return nil
	})

	if err != nil {
		// Partial history scan: same contract as a partial rollout scan.
		return nil, watermark, err
	}
	maxTS := threshold
	for _, m := range msgs {
		if ts := float64(m.TS.UnixNano()) / 1e9; ts > maxTS {
			maxTS = ts
		}
	}
	newWM := watermark
	if maxTS > threshold {
		newWM = strconv.FormatFloat(maxTS, 'f', -1, 64)
	}
	if newWM == "0" {
		newWM = ""
	}
	return msgs, newWM, nil
}
