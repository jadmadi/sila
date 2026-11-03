package loaders

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// NewCodewhaleLoader returns a loader over ~/.codewhale/sessions/*.json.
func NewCodewhaleLoader(dir string) Loader {
	return funcLoader{
		name: "codewhale",
		path: dir,
		fn: func(p string, _ *string, wm string) ([]Message, string, error) {
			return loadCodewhale(p, wm)
		},
		count: countCodewhale,
	}
}

// countCodewhale counts curated transcript messages (non-empty content) across
// every session *.json, without buffering them.
func countCodewhale(dir string, _ *string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s struct {
			Transcript []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		for _, m := range s.Transcript {
			if m.Content != "" {
				n++
			}
		}
	}
	return n, nil
}

// loadCodewhale scans ~/.codewhale/sessions/*.json. Each file represents a
// session; watermark is the max file mtime (unix seconds) processed, so new
// files are ingested incrementally. Treats the transcript as best-effort:
// missing/malformed transcript fields never fail the scan.
func loadCodewhale(dir string, watermark string) ([]Message, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}

	ceil, _ := strconv.ParseInt(watermark, 10, 64)
	var msgs []Message
	maxMtime := ceil
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		mt := info.ModTime().Unix()
		if mt > maxMtime {
			maxMtime = mt
		}
		if mt <= ceil {
			continue // already ingested
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var s struct {
			Title string `json:"title"`
			Meta  struct {
				TotalTokens int64 `json:"total_tokens"`
			} `json:"metadata"`
			Transcript []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
				TS      int64  `json:"ts"`
			} `json:"messages"`
		}
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		for _, m := range s.Transcript {
			if m.Content == "" {
				continue
			}
			ts := time.Unix(m.TS, 0).UTC()
			if m.TS == 0 {
				ts = info.ModTime().UTC()
			}
			msgs = append(msgs, Message{
				Tool: "codewhale", SessionID: e.Name(),
				TS: ts, Role: m.Role, Content: m.Content,
			})
		}
	}
	newWM := watermark
	if maxMtime > ceil {
		newWM = strconv.FormatInt(maxMtime, 10)
	}
	if newWM == "0" {
		newWM = ""
	}
	return msgs, newWM, nil
}
