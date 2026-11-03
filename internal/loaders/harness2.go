package loaders

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// NewHarness2Loader returns a Loader that scans a harness2 output directory
// containing task summary.json, selection.json, and library/<domain>/EXPERIENCE.md files.
func NewHarness2Loader(dataDir string) Loader {
	return funcLoader{
		name: "harness2",
		path: dataDir,
		fn: func(p string, project *string, wm string) ([]Message, string, error) {
			return loadHarness2(p, project, wm)
		},
		count: countHarness2,
	}
}

type harness2SummaryJSON struct {
	TaskID    string         `json:"task_id"`
	Mode      string         `json:"mode"`
	K         int            `json:"k"`
	Selection map[string]any `json:"selection"`
	Stages    map[string]any `json:"stages"`
}

type harness2SelectionJSON struct {
	Pool      []string          `json:"pool"`
	IDs       map[string]string `json:"ids"`
	Chosen    string            `json:"chosen"`
	Rationale string            `json:"rationale"`
}

func countHarness2(rootDir string, project *string) (int64, error) {
	msgs, _, err := loadHarness2(rootDir, project, "")
	if err != nil {
		return 0, err
	}
	return int64(len(msgs)), nil
}

func loadHarness2(rootDir string, project *string, watermark string) ([]Message, string, error) {
	info, err := os.Stat(rootDir)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("harness2 path %s is not a directory", rootDir)
	}

	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		absRoot = rootDir
	}
	if project != nil && *project != "" && !matchesProjectFilter(absRoot, *project) {
		return []Message{}, watermark, nil
	}

	ceil, _ := strconv.ParseInt(watermark, 10, 64)
	maxMtime := ceil

	var summaryFiles []string
	var experienceFiles []string

	err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		switch d.Name() {
		case "summary.json":
			summaryFiles = append(summaryFiles, p)
		case "EXPERIENCE.md":
			experienceFiles = append(experienceFiles, p)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	sort.Strings(summaryFiles)
	sort.Strings(experienceFiles)

	msgs := make([]Message, 0, len(summaryFiles)*2+len(experienceFiles))
	var order int64 = 1

	for _, sumPath := range summaryFiles {
		fi, sErr := os.Stat(sumPath)
		if sErr != nil {
			continue
		}
		mt := fi.ModTime().Unix()
		if mt > maxMtime {
			maxMtime = mt
		}
		if mt <= ceil {
			continue
		}

		raw, rErr := os.ReadFile(sumPath)
		if rErr != nil {
			continue
		}
		var sum harness2SummaryJSON
		if json.Unmarshal(raw, &sum) != nil || sum.TaskID == "" {
			continue
		}

		taskDir := filepath.Dir(sumPath)
		sessionID := "harness2-task-" + sum.TaskID
		ts := fi.ModTime().UTC()

		stageKeys := make([]string, 0, len(sum.Stages))
		for k := range sum.Stages {
			stageKeys = append(stageKeys, k)
		}
		sort.Strings(stageKeys)

		summaryText := fmt.Sprintf("harness2 task %s (mode=%s, k=%d, stages=%s)",
			sum.TaskID, sum.Mode, sum.K, strings.Join(stageKeys, ","))
		msgs = append(msgs, Message{
			Tool:           "harness2",
			SessionID:      sessionID,
			ProjectDir:     absRoot,
			TS:             ts,
			Role:           "user",
			Content:        summaryText,
			SourceLocation: sumPath,
			SourceOrder:    order,
		})
		order++

		selPath := filepath.Join(taskDir, "selection.json")
		if selBytes, selErr := os.ReadFile(selPath); selErr == nil {
			var sel harness2SelectionJSON
			if json.Unmarshal(selBytes, &sel) == nil && sel.Chosen != "" {
				selText := fmt.Sprintf("Selected candidate %s from pool %v: %s", sel.Chosen, sel.Pool, strings.TrimSpace(sel.Rationale))
				msgs = append(msgs, Message{
					Tool:           "harness2",
					SessionID:      sessionID,
					ProjectDir:     absRoot,
					TS:             ts.Add(time.Second),
					Role:           "assistant",
					Content:        selText,
					SourceLocation: selPath,
					SourceOrder:    order,
				})
				order++
			}
		}
	}

	for _, expPath := range experienceFiles {
		fi, sErr := os.Stat(expPath)
		if sErr != nil {
			continue
		}
		mt := fi.ModTime().Unix()
		if mt > maxMtime {
			maxMtime = mt
		}
		if mt <= ceil {
			continue
		}
		raw, rErr := os.ReadFile(expPath)
		if rErr != nil {
			continue
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			continue
		}
		domain := filepath.Base(filepath.Dir(expPath))
		msgs = append(msgs, Message{
			Tool:           "harness2",
			SessionID:      "harness2-library-" + domain,
			ProjectDir:     absRoot,
			TS:             fi.ModTime().UTC(),
			Role:           "assistant",
			Content:        fmt.Sprintf("[domain: %s]\n%s", domain, text),
			SourceLocation: expPath,
			SourceOrder:    order,
		})
		order++
	}

	nextWM := watermark
	if maxMtime > ceil {
		nextWM = strconv.FormatInt(maxMtime, 10)
	}
	return msgs, nextWM, nil
}

func matchesProjectFilter(projectDir, filter string) bool {
	f := strings.ToLower(strings.TrimSpace(filter))
	if f == "" {
		return true
	}
	clean := filepath.Clean(projectDir)
	return strings.EqualFold(filepath.Base(clean), f) || strings.Contains(strings.ToLower(clean), f)
}
