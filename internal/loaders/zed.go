package loaders

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

// zedDecoder is a shared zstd decoder instance.
var zedDecoder, _ = zstd.NewReader(nil)

// zedThreadPayload mirrors the JSON stored in threads.data after zstd decompression.
type zedThreadPayload struct {
	Title                  string `json:"title"`
	UpdatedAt              string `json:"updated_at"`
	InitialProjectSnapshot struct {
		WorktreeSnapshots []struct {
			WorktreePath string `json:"worktree_path"`
		} `json:"worktree_snapshots"`
	} `json:"initial_project_snapshot"`
	Messages []zedRawMessage `json:"messages"`
}

// zedRawMessage captures turns in a Zed assistant thread.
type zedRawMessage struct {
	User      *zedTurn `json:"User,omitempty"`
	Agent     *zedTurn `json:"Agent,omitempty"`
	Assistant *zedTurn `json:"Assistant,omitempty"`
}

type zedTurn struct {
	ID      string           `json:"id,omitempty"`
	Content []zedContentPart `json:"content,omitempty"`
}

type zedContentPart struct {
	Text string `json:"Text,omitempty"`
}

// NewZedLoader returns a loader over Zed's SQLite threads store.
func NewZedLoader(path string) Loader {
	if path == "" {
		path = filepath.Join(homeDir(), ".local", "share", "zed", "threads", "threads.db")
	}
	return funcLoader{
		name: "zed",
		path: path,
		fn: func(p string, project *string, wm string) ([]Message, string, error) {
			return loadZed(p, project, wm)
		},
		count: countZed,
	}
}

func countZed(path string, project *string) (int64, error) {
	msgs, _, err := loadZed(path, project, "")
	if err != nil {
		return 0, err
	}
	return int64(len(msgs)), nil
}

func loadZed(path string, project *string, watermark string) ([]Message, string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, watermark, errMissingDB
		}
		return nil, watermark, err
	}

	db, err := openReadOnly(path)
	if err != nil {
		return nil, watermark, err
	}
	defer db.Close()

	query := `
		SELECT id, summary, updated_at, worktree_branch, folder_paths, data_type, data
		FROM threads
		WHERE ? = '' OR updated_at >= ?
		ORDER BY updated_at ASC`

	rows, err := db.Query(query, watermark, watermark)
	if err != nil {
		return nil, watermark, err
	}
	defer rows.Close()

	sidebarWorktrees := loadZedSidebarWorktrees(path)

	var out []Message
	newWatermark := watermark

	for rows.Next() {
		var (
			id             string
			summary        string
			updatedAtStr   string
			worktreeBranch sql.NullString
			folderPaths    sql.NullString
			dataType       string
			blob           []byte
		)

		if err := rows.Scan(&id, &summary, &updatedAtStr, &worktreeBranch, &folderPaths, &dataType, &blob); err != nil {
			return nil, watermark, err
		}

		if updatedAtStr > newWatermark {
			newWatermark = updatedAtStr
		}

		if dataType != "zstd" || len(blob) == 0 {
			continue
		}

		decompressed, err := zedDecoder.DecodeAll(blob, nil)
		if err != nil {
			// Skip corrupt or unreadable blob without failing the entire stream
			continue
		}

		var payload zedThreadPayload
		if err := json.Unmarshal(decompressed, &payload); err != nil {
			continue
		}

		projectDir := resolveZedProjectDir(folderPaths.String, &payload)
		if projectDir == "" && sidebarWorktrees != nil {
			projectDir = cleanZedProjectPath(sidebarWorktrees[id])
		}
		if !zedMatchesProject(projectDir, project) {
			continue
		}

		threadTS := parseZedTimestamp(updatedAtStr)

		for msgIdx, raw := range payload.Messages {
			var role string
			var textParts []string

			switch {
			case raw.User != nil:
				role = "user"
				for _, part := range raw.User.Content {
					if t := strings.TrimSpace(part.Text); t != "" {
						textParts = append(textParts, part.Text)
					}
				}
			case raw.Agent != nil:
				role = "assistant"
				for _, part := range raw.Agent.Content {
					if t := strings.TrimSpace(part.Text); t != "" {
						textParts = append(textParts, part.Text)
					}
				}
			case raw.Assistant != nil:
				role = "assistant"
				for _, part := range raw.Assistant.Content {
					if t := strings.TrimSpace(part.Text); t != "" {
						textParts = append(textParts, part.Text)
					}
				}
			}

			content := strings.TrimSpace(strings.Join(textParts, "\n"))
			if content == "" {
				continue
			}

			msgTS := threadTS.Add(time.Duration(msgIdx) * time.Millisecond)

			out = append(out, Message{
				Tool:           "zed",
				SessionID:      id,
				ProjectDir:     projectDir,
				TS:             msgTS,
				Role:           role,
				Content:        content,
				SourceLocation: path,
				SourceOrder:    int64(msgIdx),
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, watermark, err
	}

	return out, newWatermark, nil
}

func resolveZedProjectDir(folderPaths string, payload *zedThreadPayload) string {
	if folderPaths != "" {
		var arr []string
		if err := json.Unmarshal([]byte(folderPaths), &arr); err == nil && len(arr) > 0 && arr[0] != "" {
			return cleanZedProjectPath(arr[0])
		}
		for _, p := range strings.Split(folderPaths, "\n") {
			p = strings.TrimSpace(p)
			if p != "" {
				return cleanZedProjectPath(p)
			}
		}
	}

	if payload != nil && len(payload.InitialProjectSnapshot.WorktreeSnapshots) > 0 {
		p := strings.TrimSpace(payload.InitialProjectSnapshot.WorktreeSnapshots[0].WorktreePath)
		if p != "" {
			return cleanZedProjectPath(p)
		}
	}

	return ""
}

func cleanZedProjectPath(p string) string {
	p = filepath.Clean(p)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return filepath.Dir(p)
	}
	return p
}

func zedMatchesProject(projectDir string, project *string) bool {
	if project == nil || *project == "" {
		return true
	}
	target := *project
	if projectDir == target {
		return true
	}
	if filepath.Base(projectDir) == target {
		return true
	}
	if strings.Contains(projectDir, target) {
		return true
	}
	return false
}

func parseZedTimestamp(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return time.Now().UTC()
}

// loadZedSidebarWorktrees reads session_id -> main_worktree_paths from db/0-stable/db.sqlite if present.
func loadZedSidebarWorktrees(threadsDBPath string) map[string]string {
	m := make(map[string]string)
	zedShare := filepath.Dir(filepath.Dir(threadsDBPath))
	stableDB := filepath.Join(zedShare, "db", "0-stable", "db.sqlite")
	if _, err := os.Stat(stableDB); err != nil {
		return m
	}
	db, err := openReadOnly(stableDB)
	if err != nil {
		return m
	}
	defer db.Close()

	rows, err := db.Query(`SELECT session_id, COALESCE(main_worktree_paths, folder_paths, '') FROM sidebar_threads WHERE session_id IS NOT NULL AND session_id != ''`)
	if err != nil {
		return m
	}
	defer rows.Close()

	for rows.Next() {
		var sid, p string
		if err := rows.Scan(&sid, &p); err == nil && sid != "" && p != "" {
			m[sid] = p
		}
	}
	return m
}
