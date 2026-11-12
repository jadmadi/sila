package dream

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContinuityMarkdownGolden(t *testing.T) {
	st := reportFixture(t)
	rep, err := BuildContinuityReport(context.Background(), st, "p",
		time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	md := RenderContinuityMarkdown(rep)
	path := filepath.Join("testdata", "continuity-report.golden")
	if os.Getenv("UPDATE_GOLDENS") == "1" {
		if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run UPDATE_GOLDENS=1)", err)
	}
	if string(want) != md {
		t.Fatalf("golden mismatch:\n--- want ---\n%s\n--- got ---\n%s", want, md)
	}
}

func TestContinuityReportPerformance(t *testing.T) {
	st := reportFixture(t)
	start := time.Now()
	rep, err := BuildContinuityReport(context.Background(), st, "p", time.Now().Add(-30*24*time.Hour), time.Now())
	if err != nil || rep.Schema == "" {
		t.Fatalf("err=%v schema=%q", err, rep.Schema)
	}
	elapsed := time.Since(start)
	t.Logf("continuity report took %s", elapsed)
	if elapsed > 2*time.Second {
		t.Fatalf("report took %s", elapsed)
	}
}
