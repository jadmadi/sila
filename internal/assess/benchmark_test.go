package assess

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func seedBenchmarkStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "bench.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// Messages from devin (3) and claude (2) across 2 projects.
	_, _, err = st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h1", TS: "2026-09-01T00:00:00Z", Role: "assistant", Content: "devin msg 1", Tool: "devin"},
		{ProjectID: "p1", ContentHash: "h2", TS: "2026-09-01T00:01:00Z", Role: "user", Content: "devin msg 2", Tool: "devin"},
		{ProjectID: "p2", ContentHash: "h3", TS: "2026-09-01T00:02:00Z", Role: "assistant", Content: "devin msg 3", Tool: "devin"},
	})
	if err != nil {
		t.Fatalf("WriteRun devin: %v", err)
	}
	_, _, err = st.WriteRun("", "claude", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h4", TS: "2026-09-01T00:03:00Z", Role: "assistant", Content: "claude msg 1", Tool: "claude"},
		{ProjectID: "p1", ContentHash: "h5", TS: "2026-09-01T00:04:00Z", Role: "assistant", Content: "claude msg 2", Tool: "claude"},
	})
	if err != nil {
		t.Fatalf("WriteRun claude: %v", err)
	}

	// Facts: devin has 3 claims (2 grounded, 1 ungrounded), claude has 1 (grounded).
	if err := st.UpsertFact(store.Fact{
		ID: "f1", ProjectID: "p1", Claim: "claim 1", Tools: []string{"devin"},
		FileRefsExist: true, PromotedAt: "2026-09-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(store.Fact{
		ID: "f2", ProjectID: "p1", Claim: "claim 2", Tools: []string{"devin"},
		FileRefsExist: true, PromotedAt: "2026-09-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(store.Fact{
		ID: "f3", ProjectID: "p1", Claim: "claim 3 hallucinated", Tools: []string{"devin"},
		FileRefsExist: false, PromotedAt: "2026-09-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(store.Fact{
		ID: "f4", ProjectID: "p1", Claim: "claude claim 1", Tools: []string{"claude"},
		FileRefsExist: true, PromotedAt: "2026-09-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestComputeBenchmarkMetrics(t *testing.T) {
	st := seedBenchmarkStore(t)
	defer st.Close()

	sc, err := ComputeBenchmark(st, "", "")
	if err != nil {
		t.Fatalf("ComputeBenchmark: %v", err)
	}
	if sc.TotalTools != 2 {
		t.Fatalf("TotalTools = %d, want 2", sc.TotalTools)
	}
	// Devin should be first (3 messages > 2).
	devin := sc.Tools[0]
	if devin.Tool != "devin" {
		t.Fatalf("first tool = %s, want devin", devin.Tool)
	}
	if devin.Messages != 3 {
		t.Errorf("devin Messages = %d, want 3", devin.Messages)
	}
	if devin.AssistantTurns != 2 {
		t.Errorf("devin AssistantTurns = %d, want 2", devin.AssistantTurns)
	}
	if devin.UserTurns != 1 {
		t.Errorf("devin UserTurns = %d, want 1", devin.UserTurns)
	}
	if devin.TotalClaims != 3 {
		t.Errorf("devin TotalClaims = %d, want 3", devin.TotalClaims)
	}
	if devin.GroundedClaims != 2 {
		t.Errorf("devin GroundedClaims = %d, want 2", devin.GroundedClaims)
	}
	if devin.UngroundedClaims != 1 {
		t.Errorf("devin UngroundedClaims = %d, want 1", devin.UngroundedClaims)
	}
	// Grounding rate = 2/3 ≈ 0.667 → 🔴
	if devin.GroundingRate < 0.66 || devin.GroundingRate > 0.67 {
		t.Errorf("devin GroundingRate = %.3f, want ~0.667", devin.GroundingRate)
	}
	if devin.HealthBadge != "🔴" {
		t.Errorf("devin HealthBadge = %s, want 🔴 (rate <85%%)", devin.HealthBadge)
	}
	if devin.Projects != 2 {
		t.Errorf("devin Projects = %d, want 2", devin.Projects)
	}

	// Claude: 1/1 grounded = 100% → 🟢
	claude := sc.Tools[1]
	if claude.Tool != "claude" {
		t.Fatalf("second tool = %s, want claude", claude.Tool)
	}
	if claude.GroundingRate != 1.0 {
		t.Errorf("claude GroundingRate = %.3f, want 1.0", claude.GroundingRate)
	}
	if claude.HealthBadge != "🟢" {
		t.Errorf("claude HealthBadge = %s, want 🟢", claude.HealthBadge)
	}
}

func TestComputeBenchmarkEmptyStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	sc, err := ComputeBenchmark(st, "", "")
	if err != nil {
		t.Fatalf("ComputeBenchmark: %v", err)
	}
	if sc.TotalTools != 0 {
		t.Errorf("TotalTools = %d, want 0 on empty store", sc.TotalTools)
	}
}

func TestHealthBadgeThresholds(t *testing.T) {
	cases := []struct {
		rate   float64
		claims int
		want   string
	}{
		{1.0, 10, "🟢"},
		{0.96, 10, "🟢"},
		{0.95, 10, "🟡"},
		{0.90, 10, "🟡"},
		{0.85, 10, "🟡"},
		{0.84, 10, "🔴"},
		{0.50, 10, "🔴"},
		{0.0, 0, "⚪"}, // no claims
		{1.0, 0, "⚪"}, // no claims even if rate is 1.0
	}
	for _, c := range cases {
		got := healthBadge(c.rate, c.claims)
		if got != c.want {
			t.Errorf("healthBadge(%.2f, %d) = %s, want %s", c.rate, c.claims, got, c.want)
		}
	}
}

func TestRenderScorecardTable(t *testing.T) {
	st := seedBenchmarkStore(t)
	defer st.Close()
	sc, err := ComputeBenchmark(st, "", "")
	if err != nil {
		t.Fatalf("ComputeBenchmark: %v", err)
	}
	out := RenderScorecardTable(sc)
	required := []string{"sila · benchmark", "devin", "claude", "tool", "msgs", "claims", "grounded", "rate", "health"}
	for _, s := range required {
		if !strings.Contains(out, s) {
			t.Errorf("table missing %q:\n%s", s, out)
		}
	}
}

func TestRenderScorecardTableEmpty(t *testing.T) {
	out := RenderScorecardTable(&Scorecard{})
	if !strings.Contains(out, "No tools") {
		t.Errorf("empty table should have definite empty state, got:\n%s", out)
	}
}

func TestRenderScorecardTOON(t *testing.T) {
	st := seedBenchmarkStore(t)
	defer st.Close()
	sc, err := ComputeBenchmark(st, "", "")
	if err != nil {
		t.Fatalf("ComputeBenchmark: %v", err)
	}
	out := RenderScorecardTOON(sc)
	if !strings.Contains(out, "benchmark[2]") {
		t.Errorf("TOON missing benchmark[2] header, got:\n%s", out)
	}
	if !strings.Contains(out, "devin") {
		t.Errorf("TOON missing devin row")
	}
}

func TestRenderScorecardTOONEmpty(t *testing.T) {
	out := RenderScorecardTOON(&Scorecard{})
	if !strings.Contains(out, "benchmark[0]") {
		t.Errorf("empty TOON should have benchmark[0] definite empty state")
	}
}

func TestSortToolsByGrounding(t *testing.T) {
	tools := []ToolScore{
		{Tool: "a", GroundingRate: 0.5, TotalClaims: 10},
		{Tool: "b", GroundingRate: 0.95, TotalClaims: 20},
		{Tool: "c", GroundingRate: 0.95, TotalClaims: 5},
	}
	sorted := SortToolsByGrounding(tools)
	if sorted[0].Tool != "b" {
		t.Errorf("first should be b (95%%, 20 claims), got %s", sorted[0].Tool)
	}
	if sorted[1].Tool != "c" {
		t.Errorf("second should be c (95%%, 5 claims), got %s", sorted[1].Tool)
	}
	if sorted[2].Tool != "a" {
		t.Errorf("third should be a (50%%), got %s", sorted[2].Tool)
	}
	// Original should not be mutated.
	if tools[0].Tool != "a" {
		t.Errorf("original slice was mutated")
	}
}

func TestComputeBenchmarkProjectFilter(t *testing.T) {
	st := seedBenchmarkStore(t)
	defer st.Close()

	// Filter to p2 — only devin has messages there.
	sc, err := ComputeBenchmark(st, "p2", "")
	if err != nil {
		t.Fatalf("ComputeBenchmark: %v", err)
	}
	if sc.TotalTools != 1 {
		t.Fatalf("TotalTools = %d, want 1 for p2 filter", sc.TotalTools)
	}
	if sc.Tools[0].Tool != "devin" {
		t.Errorf("tool = %s, want devin", sc.Tools[0].Tool)
	}
	if sc.Tools[0].Messages != 1 {
		t.Errorf("Messages = %d, want 1 (only p2 message)", sc.Tools[0].Messages)
	}
}
