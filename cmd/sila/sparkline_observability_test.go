package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func setupTestStoreWithActivity(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Insert messages & occurrences
	_, _, err = st.WriteRun("", "claude-code", "sess-1", []store.LoadMessage{
		{
			ProjectID:   "proj-a",
			ContentHash: "hash-1",
			TS:          today + "T10:00:00Z",
			Role:        "user",
			Content:     "Hello from today",
			Tool:        "claude-code",
			SessionID:   "sess-1",
			SourceOrder: 1,
			SourceTime:  store.SortableTime(now),
		},
	})
	if err != nil {
		t.Fatalf("WriteRun 1: %v", err)
	}

	_, _, err = st.WriteRun("", "codex", "sess-2", []store.LoadMessage{
		{
			ProjectID:   "proj-a",
			ContentHash: "hash-2",
			TS:          yesterday + "T12:00:00Z",
			Role:        "assistant",
			Content:     "Response from yesterday",
			Tool:        "codex",
			SessionID:   "sess-2",
			SourceOrder: 1,
			SourceTime:  store.SortableTime(now.AddDate(0, 0, -1)),
		},
	})
	if err != nil {
		t.Fatalf("WriteRun 2: %v", err)
	}

	// Insert facts: 1 grounded, 1 ungrounded
	_ = st.UpsertFact(store.Fact{
		ID:         "f1",
		ProjectID:  "proj-a",
		Claim:      "Ground claim",
		Confidence: "0.9",
		Verified:   true,
		FirstSeen:  today + "T10:00:00Z",
	})
	_ = st.UpsertFact(store.Fact{
		ID:         "f2",
		ProjectID:  "proj-a",
		Claim:      "Ungrounded claim",
		Confidence: "0.5",
		Verified:   false,
		FirstSeen:  yesterday + "T10:00:00Z",
	})

	return dbPath, st
}

func TestStandupSparklineInteractiveVsPiped(t *testing.T) {
	dbPath, st := setupTestStoreWithActivity(t)
	defer st.Close()

	origWd, _ := os.Getwd()
	if err := os.Chdir(filepath.Dir(dbPath)); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	// 1. Non-interactive (piped stdout): sparkline should be omitted
	ui := render.New(os.Stdout, true)
	outPiped := captureStdout(t, func() {
		runStandup(ui, dbPath, "proj-a", "24h", "", 10, false, false, false, false, false, nil)
	})
	if strings.Contains(outPiped, "Sessions (7d):") {
		t.Errorf("expected sparklines omitted on piped non-interactive output, got:\n%s", outPiped)
	}

	// 2. Interactive (SILA_FORCE_TTY=1): sparkline should appear
	t.Setenv("SILA_FORCE_TTY", "1")
	outInteractive := captureStdout(t, func() {
		runStandup(ui, dbPath, "proj-a", "24h", "", 10, false, false, false, false, false, nil)
	})
	if !strings.Contains(outInteractive, "Sessions (7d):") {
		t.Errorf("expected Sessions (7d): in interactive standup output, got:\n%s", outInteractive)
	}
	if !strings.Contains(outInteractive, "total]") {
		t.Errorf("expected [N total] in interactive standup output, got:\n%s", outInteractive)
	}

	// 3. JSON mode: numerical array emitted, no sparkline string
	outJSON := captureStdout(t, func() {
		runStandup(ui, dbPath, "proj-a", "24h", "", 10, false, true, false, false, false, nil)
	})
	var report struct {
		SessionsDaily []int `json:"sessions_daily"`
	}
	if err := json.Unmarshal([]byte(outJSON), &report); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\nJSON:\n%s", err, outJSON)
	}
	if len(report.SessionsDaily) != 7 {
		t.Errorf("expected 7 days in sessions_daily array, got %d", len(report.SessionsDaily))
	}

	// 4. TOON mode: sessions_daily field in TOON summary
	outTOON := captureStdout(t, func() {
		runStandup(ui, dbPath, "proj-a", "24h", "", 10, false, false, true, false, false, nil)
	})
	if !strings.Contains(outTOON, "sessions_daily:") {
		t.Errorf("expected sessions_daily: in TOON output, got:\n%s", outTOON)
	}
}

func TestStatusSparklineInteractiveVsJSON(t *testing.T) {
	dbPath, st := setupTestStoreWithActivity(t)
	defer st.Close()

	// 1. Non-interactive (piped stdout): sparklines omitted
	ui := render.New(os.Stdout, true)
	outPiped := captureStdout(t, func() {
		runStatus(ui, dbPath, false, false, false)
	})
	if strings.Contains(outPiped, "Claims velocity (7d):") {
		t.Errorf("expected sparklines omitted on piped non-interactive output, got:\n%s", outPiped)
	}

	// 2. Interactive (SILA_FORCE_TTY=1): sparklines appear
	t.Setenv("SILA_FORCE_TTY", "1")
	outInteractive := captureStdout(t, func() {
		runStatus(ui, dbPath, false, false, false)
	})
	if !strings.Contains(outInteractive, "Claims velocity (7d):") {
		t.Errorf("expected Claims velocity (7d): in interactive output, got:\n%s", outInteractive)
	}
	if !strings.Contains(outInteractive, "Database growth (7d):") {
		t.Errorf("expected Database growth (7d): in interactive output, got:\n%s", outInteractive)
	}

	// 3. JSON mode: arrays present, no sparklines
	outJSON := captureStdout(t, func() {
		runStatus(ui, dbPath, true, false, false)
	})
	var stat jsonStatus
	if err := json.Unmarshal([]byte(outJSON), &stat); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\nJSON:\n%s", err, outJSON)
	}
	if len(stat.ClaimVelocity) != 7 {
		t.Errorf("expected 7 elements in ClaimVelocity, got %d", len(stat.ClaimVelocity))
	}
	if len(stat.PageGrowth) != 7 {
		t.Errorf("expected 7 elements in PageGrowth, got %d", len(stat.PageGrowth))
	}
}

func TestStorageSparklineInteractiveVsJSON(t *testing.T) {
	dbPath, st := setupTestStoreWithActivity(t)
	defer st.Close()

	// 1. Non-interactive (piped stdout): trendline omitted
	ui := render.New(os.Stdout, true)
	outPiped := captureStdout(t, func() {
		runStorage(ui, dbPath, false)
	})
	if strings.Contains(outPiped, "trend (7d)") {
		t.Errorf("expected trend (7d) omitted on piped output, got:\n%s", outPiped)
	}

	// 2. Interactive (SILA_FORCE_TTY=1): trendline appears
	t.Setenv("SILA_FORCE_TTY", "1")
	outInteractive := captureStdout(t, func() {
		runStorage(ui, dbPath, false)
	})
	if !strings.Contains(outInteractive, "trend (7d)") {
		t.Errorf("expected trend (7d) in interactive storage output, got:\n%s", outInteractive)
	}

	// 3. JSON mode: footprint_trend array emitted
	outJSON := captureStdout(t, func() {
		runStorage(ui, dbPath, true)
	})
	var rep StorageReport
	if err := json.Unmarshal([]byte(outJSON), &rep); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\nJSON:\n%s", err, outJSON)
	}
	if len(rep.Store.FootprintTrend) != 7 {
		t.Errorf("expected 7 elements in FootprintTrend, got %d", len(rep.Store.FootprintTrend))
	}
}
