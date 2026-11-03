package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func writeTestCert(t *testing.T, repoDir, slug string, certTime time.Time) {
	t.Helper()
	goalDir := filepath.Join(repoDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatalf("mkdir goal: %v", err)
	}
	rec := map[string]any{
		"goal_slug":    slug,
		"certified_at": certTime.UTC().Format(time.RFC3339Nano),
		"commit_sha":   "abc1234",
		"verified":     true,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal cert: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "certification.json"), data, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
}

func seedSoakStore(t *testing.T, dbPath string, toolEvents []store.ToolEvent, primeEvents []store.PrimeEvent) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	defer st.Close()

	for _, ev := range toolEvents {
		if err := st.RecordToolEvent(ev); err != nil {
			t.Fatalf("RecordToolEvent: %v", err)
		}
	}
	for _, pev := range primeEvents {
		if err := st.RecordPrimeEvent(pev); err != nil {
			t.Fatalf("RecordPrimeEvent: %v", err)
		}
	}
}

func TestEvaluateSoak_InProgressDuration(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "sila.db")
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	certTime := now.Add(-24 * time.Hour)

	writeTestCert(t, repoDir, "feature-alpha", certTime)
	seedSoakStore(t, dbPath, []store.ToolEvent{
		{
			ProjectID: "sila",
			ToolName:  "prime_context",
			Outcome:   "ok",
			CreatedAt: certTime.Add(2 * time.Hour),
		},
	}, nil)

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  dbPath,
		Now:     now,
	})

	if rep.State != SoakInProgress {
		t.Fatalf("expected SoakInProgress, got %s (%s)", rep.State, rep.Summary)
	}
	if rep.CurrentDay != 2 {
		t.Errorf("expected CurrentDay 2 after 24h, got %d", rep.CurrentDay)
	}
	if rep.RemainingHours != 48.0 {
		t.Errorf("expected 48.0 remaining hours, got %.1f", rep.RemainingHours)
	}
	if rep.ActiveDays != 1 {
		t.Errorf("expected 1 active day, got %d", rep.ActiveDays)
	}
}

func TestEvaluateSoak_InProgressInsufficientActiveDays(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "sila.db")
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	certTime := now.Add(-80 * time.Hour)

	writeTestCert(t, repoDir, "feature-beta", certTime)
	seedSoakStore(t, dbPath, []store.ToolEvent{
		{
			ProjectID: "sila",
			ToolName:  "prime_context",
			Outcome:   "ok",
			CreatedAt: certTime.Add(2 * time.Hour),
		},
		{
			ProjectID: "sila",
			ToolName:  "run_gate_pipeline",
			Outcome:   "ok",
			CreatedAt: certTime.Add(28 * time.Hour),
		},
	}, nil)

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  dbPath,
		Now:     now,
	})

	if rep.State != SoakInProgress {
		t.Fatalf("expected SoakInProgress when only 2/3 active days exist, got %s", rep.State)
	}
	if rep.ActiveDays != 2 {
		t.Errorf("expected 2 active days, got %d", rep.ActiveDays)
	}
}

func TestEvaluateSoak_NeedsDebugOnToolOrPrimeError(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "sila.db")
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	certTime := now.Add(-80 * time.Hour)

	writeTestCert(t, repoDir, "feature-gamma", certTime)
	seedSoakStore(t, dbPath, []store.ToolEvent{
		{
			ProjectID: "sila",
			ToolName:  "prime_context",
			Outcome:   "ok",
			CreatedAt: certTime.Add(2 * time.Hour),
		},
		{
			ProjectID: "sila",
			ToolName:  "audit_goal",
			Outcome:   "error",
			CreatedAt: certTime.Add(26 * time.Hour),
		},
		{
			ProjectID: "sila",
			ToolName:  "run_gate_pipeline",
			Outcome:   "ok",
			CreatedAt: certTime.Add(52 * time.Hour),
		},
	}, []store.PrimeEvent{
		{
			ProjectID:  "sila",
			Harness:    "claude-code",
			Status:     "failed",
			ErrorClass: "timeout",
			Reason:     "db busy",
			CreatedAt:  certTime.Add(55 * time.Hour),
		},
	})

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  dbPath,
		Now:     now,
	})

	if rep.State != SoakNeedsDebug {
		t.Fatalf("expected SoakNeedsDebug on tool/prime error, got %s", rep.State)
	}
	if rep.ToolErrorCount != 1 {
		t.Errorf("expected 1 tool error, got %d", rep.ToolErrorCount)
	}
	if rep.PrimeErrorCount != 1 {
		t.Errorf("expected 1 prime error, got %d", rep.PrimeErrorCount)
	}
	if len(rep.FailedTools) != 1 || rep.FailedTools[0].ToolName != "audit_goal" {
		t.Errorf("expected audit_goal in FailedTools, got %+v", rep.FailedTools)
	}

	baseVerdict := ReleaseVerdictInfo{Code: "READY_TO_RELEASE", Verdict: "READY TO RELEASE"}
	overridden := applySoakVerdict(baseVerdict, rep)
	if overridden.Code != "SOAK_NEEDS_DEBUG" {
		t.Errorf("expected SOAK_NEEDS_DEBUG verdict override, got %s", overridden.Code)
	}
}

func TestEvaluateSoak_ReadyAfterThreeActiveDaysAnd72Hours(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "sila.db")
	now := time.Now().UTC().Truncate(time.Second)
	certTime := now.Add(-80 * time.Hour)

	writeTestCert(t, repoDir, "feature-delta", certTime)
	seedSoakStore(t, dbPath, []store.ToolEvent{
		{
			ProjectID: "sila",
			ToolName:  "prime_context",
			Outcome:   "ok",
			CreatedAt: certTime.Add(1 * time.Hour),
		},
		{
			ProjectID: "sila",
			ToolName:  "omni_search",
			Outcome:   "ok",
			CreatedAt: certTime.Add(25 * time.Hour),
		},
		{
			ProjectID: "sila",
			ToolName:  "run_gate_pipeline",
			Outcome:   "ok",
			CreatedAt: certTime.Add(49 * time.Hour),
		},
	}, []store.PrimeEvent{
		{
			ProjectID: "sila",
			Harness:   "claude-code",
			Status:    "ok",
			CreatedAt: certTime.Add(73 * time.Hour),
		},
	})

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  dbPath,
		Now:     now,
	})

	if rep.State != SoakReady {
		t.Fatalf("expected SoakReady after 80h and 3 active days with 0 errors, got %s (%s)", rep.State, rep.Summary)
	}
	if rep.ActiveDays < 3 {
		t.Errorf("expected at least 3 active days, got %d", rep.ActiveDays)
	}

	baseVerdict := ReleaseVerdictInfo{Code: "READY_TO_RELEASE", Verdict: "READY TO RELEASE"}
	overridden := applySoakVerdict(baseVerdict, rep)
	if overridden.Code != "READY_TO_RELEASE" {
		t.Errorf("expected READY_TO_RELEASE preserved when soak is ready, got %s", overridden.Code)
	}

	chk := checkLocalSoak(repoDir, PreflightOptions{RepoDir: repoDir, DBPath: dbPath, Strict: true})
	if chk.Status != StatusPass {
		t.Errorf("expected StatusPass from checkLocalSoak in strict mode when ready, got %s (%s)", chk.Status, chk.Detail)
	}
	if !strings.Contains(chk.Detail, "3-day local soak verified") {
		t.Errorf("expected verified detail, got %s", chk.Detail)
	}
	if len(rep.FeatureLedger) == 0 {
		t.Fatalf("expected automated FeatureLedger rows in sila.db, got 0")
	}
	if rep.FeatureLedger[0].FeatureID != "feature-delta" || rep.FeatureLedger[0].Status != "ready" {
		t.Errorf("expected feature-delta status=ready in FeatureLedger, got %+v", rep.FeatureLedger[0])
	}
	if len(rep.DailyReports) == 0 {
		t.Fatalf("expected automated DailyReports row in sila.db, got 0")
	}
}
