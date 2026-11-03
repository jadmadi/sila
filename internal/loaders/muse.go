package loaders

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// museRecord is one unwrapped Muse session record. Muse does not label messages
// with a role: the role is implied by the payload type and, for assistant
// output, by the event kind inside the payload.
type museRecord struct {
	RecordedAt  int64  `json:"recorded_at"` // epoch microseconds
	Sequence    int64  `json:"sequence"`
	PayloadType string `json:"payload_type"`
	Stream      struct {
		ID string `json:"id"`
	} `json:"stream"`
	Payload struct {
		// user_intent.accepted carries the user's turn here...
		RefillBlocks []struct {
			Text string `json:"text"`
		} `json:"refill_blocks"`
		// ...and here, when the intent was materialized into model messages.
		ModelMessages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"model_messages"`
		// assistant turns arrive as runtime.session events.
		Event struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"event"`
		// workspace metadata carried by branch/observation records.
		Record struct {
			WorkspaceRoot string `json:"workspace_root"`
			Cwd           string `json:"cwd"`
		} `json:"record"`
	} `json:"payload"`
}

// museRecordFrame is the wrapper Muse writes for durable records: the real
// record is a JSON string nested inside `children`.
type museRecordFrame struct {
	Children []struct {
		RecordJSON string `json:"record_json"`
	} `json:"children"`
}

// NewMuseLoader returns a loader over Muse's session transcripts.
func NewMuseLoader(path string) Loader {
	return funcLoader{
		name: "muse",
		path: path,
		fn: func(p string, project *string, wm string) ([]Message, string, error) {
			return loadMuse(p, project, wm)
		},
		count: countMuse,
	}
}

// museSessionFiles lists `<root>/YYYY/MM/DD/<sessionId>/session.jsonl`.
func museSessionFiles(root string) ([]string, error) {
	var out []string
	walkErr := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// The materialized view holds the same content in a binary framing
			// that this loader does not read.
			if d.Name() == ".msp-view-v1" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "session.jsonl" {
			out = append(out, p)
		}
		return nil
	})
	return out, walkErr
}

// loadMuse streams Muse session transcripts.
//
// Fidelity limits, all verified against the real store and documented rather
// than papered over:
//
//   - The advertised `session-index.db` holds no message text (it is metadata
//     and pointers only, and can be stale), so transcripts are read directly
//     from `sessions/YYYY/MM/DD/<sessionId>/session.jsonl`.
//   - Roles are inferred: `runtime.user_intent.accepted` is the user's turn,
//     and `runtime.session` with `event.kind == "assistant_message_committed"`
//     is the assistant's.
//   - `session.started` carries the prompt again and is skipped: ingesting it
//     would double-count the user's turn, the same trap as Claude's
//     `last-prompt` records.
//   - Reasoning events are skipped: they are model-internal, and their content
//     is usually `encrypted_content` rather than text.
//
// The cursor is the newest `recorded_at` (microseconds), compared inclusively.
func loadMuse(root string, project *string, watermark string) ([]Message, string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, watermark, errMissingDB
	}

	threshold, _ := strconv.ParseInt(watermark, 10, 64)
	var msgs []Message
	var maxTS int64 = threshold

	// Workspace metadata can arrive in a different record from the message, so
	// each session's directory is resolved first and applied to its messages.
	files, walkErr := museSessionFiles(root)
	if walkErr != nil {
		return nil, watermark, fmt.Errorf("walk %s: %w", root, walkErr)
	}
	for _, file := range files {
		sessionID := filepath.Base(filepath.Dir(file))
		workspace, msgsFromFile, err := museWorkspaceAndMessages(file, threshold, sessionID)
		if err != nil {
			return nil, watermark, err
		}
		if workspace != "" {
			for i := range msgsFromFile {
				if msgsFromFile[i].ProjectDir == "" {
					msgsFromFile[i].ProjectDir = workspace
				}
			}
		}
		for _, m := range msgsFromFile {
			if ts := m.TS.UnixNano() / 1000; ts > maxTS {
				maxTS = ts
			}
			msgs = append(msgs, m)
		}
	}

	newWM := watermark
	if maxTS > threshold {
		newWM = strconv.FormatInt(maxTS, 10)
	}
	if newWM == "0" {
		newWM = ""
	}
	return msgs, newWM, nil
}

// museWorkspaceAndMessages reads one session file, returning the session's
// workspace root (when a record carries it) and its curated messages.
func museWorkspaceAndMessages(file string, threshold int64, fallbackSessionID string) (string, []Message, error) {
	workspace := ""
	var msgs []Message
	lineNo := int64(0)

	if serr := ScanJSONL(file, func(line []byte) error {
		lineNo++
		for _, rec := range museUnwrap(line, &lineNo) {
			if w := firstNonEmptyString(rec.Payload.Record.WorkspaceRoot, rec.Payload.Record.Cwd); w != "" && workspace == "" {
				workspace = w
			}
			role, text := museMessage(rec)
			if role == "" || strings.TrimSpace(text) == "" {
				continue
			}
			if rec.RecordedAt < threshold {
				continue
			}
			sessionID := rec.Stream.ID
			if sessionID == "" {
				sessionID = fallbackSessionID
			}
			msgs = append(msgs, Message{
				Tool:           "muse",
				SessionID:      sessionID,
				ProjectDir:     firstNonEmptyString(rec.Payload.Record.WorkspaceRoot, rec.Payload.Record.Cwd),
				TS:             time.UnixMicro(rec.RecordedAt).UTC(),
				Role:           role,
				Content:        strings.TrimSpace(text),
				SourceLocation: file,
				SourceOrder:    rec.Sequence,
			})
		}
		return nil
	}); serr != nil {
		// An interrupted scan must not look like a complete one, and the error
		// has to name the file or the user cannot act on it.
		return "", nil, fmt.Errorf("scan %s: %w", file, serr)
	}
	return workspace, msgs, nil
}

// museUnwrap returns the records on one line, descending through transaction
// frames. A frame's child `record_json` is a JSON string that may itself wrap
// further frames, so this recurses instead of assuming one level.
func museUnwrap(line []byte, lineNo *int64) []museRecord {
	var frame museRecordFrame
	if json.Unmarshal(line, &frame) == nil && len(frame.Children) > 0 {
		var out []museRecord
		for _, child := range frame.Children {
			if strings.TrimSpace(child.RecordJSON) == "" {
				continue
			}
			out = append(out, museUnwrap([]byte(child.RecordJSON), lineNo)...)
		}
		return out
	}
	var rec museRecord
	if json.Unmarshal(line, &rec) != nil {
		return nil
	}
	if rec.PayloadType == "" && rec.RecordedAt == 0 {
		return nil
	}
	return []museRecord{rec}
}

// museMessage maps a record to (role, text), returning an empty role for records
// that are not conversation turns.
func museMessage(rec museRecord) (string, string) {
	switch rec.PayloadType {
	case "runtime.user_intent.accepted":
		for _, b := range rec.Payload.RefillBlocks {
			if strings.TrimSpace(b.Text) != "" {
				return "user", b.Text
			}
		}
		for _, m := range rec.Payload.ModelMessages {
			if m.Role != "" && m.Role != "user" {
				continue
			}
			var parts []string
			for _, c := range m.Content {
				if strings.TrimSpace(c.Text) != "" {
					parts = append(parts, c.Text)
				}
			}
			if len(parts) > 0 {
				return "user", strings.Join(parts, "\n")
			}
		}
	case "runtime.session":
		if rec.Payload.Event.Kind == "assistant_message_committed" {
			return "assistant", rec.Payload.Event.Text
		}
	}
	return "", ""
}

// countMuse counts conversation records without retaining content. Upper bound
// on what a full scan emits, keeping assess's Total >= New invariant.
func countMuse(root string, _ *string) (int64, error) {
	if _, err := os.Stat(root); err != nil {
		return 0, errMissingDB
	}
	files, walkErr := museSessionFiles(root)
	if walkErr != nil {
		return 0, fmt.Errorf("walk %s: %w", root, walkErr)
	}
	var n int64
	var lineNo int64
	for _, file := range files {
		if serr := ScanJSONL(file, func(line []byte) error {
			lineNo++
			for _, rec := range museUnwrap(line, &lineNo) {
				if role, text := museMessage(rec); role != "" && strings.TrimSpace(text) != "" {
					n++
				}
			}
			return nil
		}); serr != nil {
			return 0, serr
		}
	}
	return n, nil
}
