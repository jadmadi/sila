package loaders

import (
	"encoding/json"
	"strconv"
	"time"
)

type commandCodeRecord struct {
	P string  `json:"p"`
	T float64 `json:"t"` // epoch ms
}

// NewCommandCodeLoader returns a loader over the command-code history.jsonl.
func NewCommandCodeLoader(path string) Loader {
	return funcLoader{
		name: "command-code",
		path: path,
		fn: func(p string, _ *string, wm string) ([]Message, string, error) {
			return loadCommandCode(p, wm)
		},
		count: countCommandCode,
	}
}

// countCommandCode counts curated records (non-empty prompt) in history.jsonl
// without buffering message content.
func countCommandCode(path string, _ *string) (int64, error) {
	var n int64
	err := ScanJSONL(path, func(line []byte) error {
		var r commandCodeRecord
		if json.Unmarshal(line, &r) == nil && r.P != "" {
			n++
		}
		return nil
	})
	return n, err
}

// loadCommandCode reads the command-code history.jsonl. watermark is max t (ms).
func loadCommandCode(path string, watermark string) ([]Message, string, error) {
	threshold, _ := strconv.ParseFloat(watermark, 64)
	var msgs []Message
	var maxT float64 = threshold
	err := ScanJSONL(path, func(line []byte) error {
		var r commandCodeRecord
		if json.Unmarshal(line, &r) != nil || r.P == "" {
			return nil
		}
		if r.T <= threshold {
			return nil
		}
		if r.T > maxT {
			maxT = r.T
		}
		msgs = append(msgs, Message{
			Tool: "command-code", TS: time.UnixMilli(int64(r.T)).UTC(),
			Role: "user", Content: r.P,
		})
		return nil
	})
	newWM := watermark
	if maxT > threshold {
		newWM = strconv.FormatFloat(maxT, 'f', 0, 64)
	}
	if newWM == "0" {
		newWM = ""
	}
	return msgs, newWM, err
}
