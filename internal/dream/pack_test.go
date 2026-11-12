package dream

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func packTestHit(kind, id string, score float64, files ...string) RecallHit {
	return RecallHit{
		Kind:       kind,
		ID:         id,
		Resolution: "resolution for " + id,
		Score:      score,
		Grounded:   len(files) > 0,
		SourceRef:  "sila show " + id,
		RepoFiles:  files,
	}
}

func packCodes(diags []PathDiagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

func TestRecallContextValidation(t *testing.T) {
	validHead := "4f2a91c"
	tests := []struct {
		name    string
		ctx     RecallContext
		wantErr string
	}{
		{name: "valid", ctx: RecallContext{Task: "fix deploy ordering", Branch: "main", Head: validHead, Symbols: []string{"runDeploy"}}},
		{name: "long task", ctx: RecallContext{Task: strings.Repeat("x", 201)}, wantErr: "task_too_long"},
		{name: "too many symbols", ctx: RecallContext{Symbols: seventeenSymbols()}, wantErr: "too_many_symbols"},
		{name: "bad head", ctx: RecallContext{Head: "abc"}, wantErr: "invalid_head"},
		{name: "non hex head", ctx: RecallContext{Head: "zzzzzzz"}, wantErr: "invalid_head"},
		{name: "long branch", ctx: RecallContext{Branch: strings.Repeat("b", 256)}, wantErr: "branch_too_long"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ValidateRecallContext(tc.ctx, "")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var ce *RecallContextError
			if !asRecallContextError(err, &ce) {
				t.Fatalf("error type = %T, want RecallContextError", err)
			}
			if ce.Code != tc.wantErr {
				t.Fatalf("code = %q, want %q", ce.Code, tc.wantErr)
			}
		})
	}

	normalized, _, err := ValidateRecallContext(RecallContext{Head: strings.ToUpper(validHead), Symbols: []string{"Run", "Run", " run "}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Head != validHead {
		t.Fatalf("head = %q, want lowercase %q", normalized.Head, validHead)
	}
	if len(normalized.Symbols) != 1 || normalized.Symbols[0] != "Run" {
		t.Fatalf("symbols = %v, want one deduplicated entry", normalized.Symbols)
	}
}

func asRecallContextError(err error, target **RecallContextError) bool {
	ce, ok := err.(*RecallContextError)
	if ok {
		*target = ce
	}
	return ok
}

func seventeenSymbols() []string {
	out := make([]string, 17)
	for i := range out {
		out[i] = "sym" + string(rune('a'+i))
	}
	return out
}

func TestNormalizeRecallPaths(t *testing.T) {
	paths, diags := normalizeRecallPaths([]string{
		"./internal/dream/pack.go",
		`internal\dream\pack.go`,
		"internal//dream//recall.go",
		"internal/./dream/types.go",
	}, "")
	want := []string{"internal/dream/pack.go", "internal/dream/recall.go", "internal/dream/types.go"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
	codes := packCodes(diags)
	if len(codes) != 1 || codes[0] != "duplicate_path" {
		t.Fatalf("diagnostics = %v, want one duplicate_path", codes)
	}
}

func TestRecallPathsRejectTraversal(t *testing.T) {
	_, diags := normalizeRecallPaths([]string{"../secret.env", "a/../../b.go", "/etc/passwd", "~/keys", "docs/./../x", ""}, "")
	codes := packCodes(diags)
	want := []string{"traversal", "traversal", "absolute_path", "home_path", "traversal", "empty_path"}
	if strings.Join(codes, ",") != strings.Join(want, ",") {
		t.Fatalf("codes = %v, want %v", codes, want)
	}
}

func TestRecallPathsDedupeAndCap(t *testing.T) {
	input := make([]string, 0, 35)
	for i := 0; i < 35; i++ {
		input = append(input, "dir/file"+string(rune('a'+i))+".go")
	}
	paths, diags := normalizeRecallPaths(input, "")
	if len(paths) != RecallContextMaxPaths {
		t.Fatalf("paths = %d, want %d", len(paths), RecallContextMaxPaths)
	}
	found := false
	for _, d := range diags {
		if d.Code == "too_many_paths" && d.Index == RecallContextMaxPaths {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing too_many_paths diagnostic: %v", diags)
	}
}

func TestRecallPathsRejectSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	paths, diags := normalizeRecallPaths([]string{"escape/secret.txt", "inside.txt"}, root)
	if len(paths) != 1 || paths[0] != "inside.txt" {
		t.Fatalf("paths = %v, want only inside.txt", paths)
	}
	if codes := packCodes(diags); len(codes) != 1 || codes[0] != "symlink_escape" {
		t.Fatalf("diagnostics = %v, want symlink_escape", codes)
	}
}

func TestContextBoostPaths(t *testing.T) {
	hit := packTestHit("fact", "f1", 600, "internal/dream/pack.go")
	tests := []struct {
		name  string
		paths []string
		want  float64
	}{
		{name: "exact file", paths: []string{"internal/dream/pack.go"}, want: 100},
		{name: "parent directory", paths: []string{"internal/dream"}, want: 100},
		{name: "repo root prefix", paths: []string{"internal"}, want: 100},
		{name: "unrelated", paths: []string{"cmd/sila/recall.go"}, want: 0},
		{name: "no substring", paths: []string{"internal/dream/pack"}, want: 0},
		{name: "no extension drift", paths: []string{"internal/dream/pack.go.bak"}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ContextBoost(hit, RecallContext{Paths: tc.paths}, "")
			if got != tc.want {
				t.Fatalf("boost = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContextBoostCap(t *testing.T) {
	hit := packTestHit("fact", "f1", 600, "a.go", "b.go", "c.go", "d.go")
	ctx := RecallContext{Paths: []string{"a.go", "b.go", "c.go", "d.go"}}
	if got := ContextBoost(hit, ctx, ""); got != recallContextCap {
		t.Fatalf("path boost = %v, want %v", got, recallContextCap)
	}
}

func TestContextBoostTaskAndSymbols(t *testing.T) {
	hit := packTestHit("fact", "f1", 600)
	hit.Resolution = "deploy ordering migration must run before the app"
	hit.SourceRef = "sila note show runDeploy"
	ctx := RecallContext{Task: "fix deploy ordering migration", Symbols: []string{"runDeploy", "missingSymbol"}}
	boost := ContextBoost(hit, ctx, "")
	if boost != 80 {
		t.Fatalf("boost = %v, want 60 task + 20 symbol", boost)
	}
	if strings.Contains(strings.ToLower(hit.Resolution), "fix deploy ordering migration") {
		t.Fatal("test fixture leaked the task into the hit")
	}
}

func TestContextBoostNoProvenance(t *testing.T) {
	hit := packTestHit("fact", "f1", 600)
	ctx := RecallContext{Branch: "main", Head: "4f2a91c"}
	if got := ContextBoost(hit, ctx, ""); got != 0 {
		t.Fatalf("boost without provenance = %v, want 0", got)
	}
	hit.Branch = "main"
	hit.Head = "4f2a91c9e8d7c6b5a4"
	if got := ContextBoost(hit, ctx, ""); got != recallBranchBoost+recallHeadBoost {
		t.Fatalf("boost with provenance = %v, want %v", got, recallBranchBoost+recallHeadBoost)
	}
}

func TestApplyRecallContextTierClamp(t *testing.T) {
	lesson := packTestHit("lesson", "l1", 1000)
	fact := packTestHit("fact", "f1", 800, "a.go", "b.go", "c.go", "d.go")
	transcript := packTestHit("transcript", "t1", 400, "a.go", "b.go", "c.go", "d.go")
	ctx := RecallContext{Paths: []string{"a.go", "b.go", "c.go", "d.go"}}
	if got := ApplyRecallContext(fact, ctx, "").Score; got != recallFactBase+recallTierSpan {
		t.Fatalf("fact score = %v, want %v", got, recallFactBase+recallTierSpan)
	}
	if got := ApplyRecallContext(transcript, ctx, "").Score; got != recallTranscriptBase+recallTierSpan {
		t.Fatalf("transcript score = %v, want %v", got, recallTranscriptBase+recallTierSpan)
	}
	if ApplyRecallContext(fact, ctx, "").Score >= lesson.Score {
		t.Fatal("boosted fact reached the lesson tier")
	}
}

func TestBuildRecallPackBudget(t *testing.T) {
	var hits []RecallHit
	for i := 0; i < 12; i++ {
		hits = append(hits, packTestHit("fact", "f"+string(rune('a'+i)), float64(700-i)))
	}

	pack := BuildRecallPack(hits, 1200, 8)
	if len(pack.Items) > 8 {
		t.Fatalf("items = %d, want <= 8", len(pack.Items))
	}
	if pack.Tokens > 1200 {
		t.Fatalf("tokens = %d, want <= 1200", pack.Tokens)
	}
	if pack.Omitted != len(hits)-len(pack.Items) {
		t.Fatalf("omitted = %d, want %d", pack.Omitted, len(hits)-len(pack.Items))
	}
	if pack.RankingVersion != RankingVersion {
		t.Fatalf("version = %q, want %q", pack.RankingVersion, RankingVersion)
	}

	limited := BuildRecallPack(hits, 8192, 1)
	if len(limited.Items) != 1 {
		t.Fatalf("limit 1 produced %d items", len(limited.Items))
	}
	if limited.Omitted != len(hits)-1 {
		t.Fatalf("limit 1 omitted = %d, want %d", limited.Omitted, len(hits)-1)
	}

	if limited.Tokens > 8192 {
		t.Fatalf("budget 8192 exceeded: %d", limited.Tokens)
	}
}

func TestBuildRecallPackEmpty(t *testing.T) {
	if pack := BuildRecallPack(nil, 1200, 8); pack.EmptyReason != RecallEmptyNoCandidates {
		t.Fatalf("nil hits reason = %q, want %q", pack.EmptyReason, RecallEmptyNoCandidates)
	}
	zero := []RecallHit{packTestHit("fact", "f0", 0)}
	if pack := BuildRecallPack(zero, 1200, 8); pack.EmptyReason != RecallEmptyAllBelowThreshold {
		t.Fatalf("zero-score reason = %q, want %q", pack.EmptyReason, RecallEmptyAllBelowThreshold)
	}
	big := packTestHit("fact", strings.Repeat("big", 1000), 900)
	big.Resolution = strings.Repeat("x", 4000)
	if pack := BuildRecallPack([]RecallHit{big}, 256, 8); pack.EmptyReason != RecallEmptyBudgetExhausted {
		t.Fatalf("oversized first item reason = %q, want %q", pack.EmptyReason, RecallEmptyBudgetExhausted)
	}
}

func TestBuildRecallPackDeterministic(t *testing.T) {
	var hits []RecallHit
	for i := 0; i < 50; i++ {
		hits = append(hits, packTestHit("fact", "f"+string(rune('a'+i%26))+string(rune('a'+i/26)), float64(600-i)))
	}
	first, _ := json.Marshal(BuildRecallPack(hits, 1200, 8))
	second, _ := json.Marshal(BuildRecallPack(hits, 1200, 8))
	if string(first) != string(second) {
		t.Fatalf("pack JSON is not byte-identical:\n%s\n%s", first, second)
	}
}

func TestRecallPackScaleDeterminism(t *testing.T) {
	var hits []RecallHit
	for i := 0; i < 1000; i++ {
		hits = append(hits, RecallHit{
			Kind:       "fact",
			ID:         "f" + strings.Repeat("0", 4) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)),
			Resolution: strings.Repeat("r", 40),
			Score:      float64(600 + i%50),
			Timestamp:  "2026-11-03T10:00:00Z",
		})
	}
	sortRecallHits(hits)
	first, _ := json.Marshal(BuildRecallPack(hits, 1200, 8))

	shuffled := append([]RecallHit(nil), hits...)
	sort.SliceStable(shuffled, func(i, j int) bool { return shuffled[i].ID > shuffled[j].ID })
	sortRecallHits(shuffled)
	second, _ := json.Marshal(BuildRecallPack(shuffled, 1200, 8))

	if string(first) != string(second) {
		t.Fatal("shuffled input changed the pack")
	}
}

func TestRecallPackPrivacySurface(t *testing.T) {
	task := "fix deploy ordering"
	secretPath := "scripts/private/deploy.sh"
	hit := packTestHit("fact", "f1", 700, secretPath)
	ctx, diags, err := ValidateRecallContext(RecallContext{Task: task, Paths: []string{secretPath}}, "")
	if err != nil {
		t.Fatal(err)
	}
	pack := BuildRecallPack([]RecallHit{hit}, 1200, 8)
	summary := SummarizeRecallContext(ctx, diags)
	pack.Context = &summary
	raw, _ := json.Marshal(pack)
	payload := string(raw)
	if strings.Contains(payload, task) {
		t.Fatalf("pack echoed the task: %s", payload)
	}
	if strings.Contains(payload, secretPath) {
		t.Fatalf("pack echoed the context path: %s", payload)
	}
	if !strings.Contains(payload, `"paths_count":1`) || !strings.Contains(payload, `"task_present":true`) {
		t.Fatalf("pack summary missing metrics: %s", payload)
	}
}

func TestBuildRecallPackCarriesMemoryIDs(t *testing.T) {
	hits := []RecallHit{
		{Kind: "lesson", ID: "fact-1", MemoryID: "mem_1", Resolution: "fix", Score: 1000},
		{Kind: "transcript", ID: "42", Resolution: "raw turn", Score: 100},
	}
	pack := BuildRecallPack(hits, RecallPackDefaultTokens, 8)
	if len(pack.Items) != 2 {
		t.Fatalf("pack items = %d, want 2", len(pack.Items))
	}
	if pack.Items[0].MemoryID != "mem_1" || pack.Items[1].MemoryID != "" {
		t.Fatalf("pack memory ids = %+v", pack.Items)
	}
}

func TestRecordPackRetrievalSkipsItemsWithoutMemoryIDs(t *testing.T) {
	st, project := feedbackRecallStore(t)
	now := recallFeedbackClock()
	rec := seedDreamFeedbackMemory(t, st, project.ID, "Pack retrieval memory")
	pack := BuildRecallPack([]RecallHit{
		{Kind: "lesson", ID: rec.FactID, MemoryID: rec.ID, Resolution: "fix", Score: 1000},
		{Kind: "transcript", ID: "99", Resolution: "raw turn", Score: 100},
	}, RecallPackDefaultTokens, 8)
	if err := RecordPackRetrieval(st, &pack, RecallRetrievalOptions{
		Project: project.ID, Query: "pack retrieval", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	row, err := st.MemoryRetrievalByID(pack.RetrievalID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ItemCount != 1 {
		t.Fatalf("ledger item count = %d, want only the typed memory", row.ItemCount)
	}
	items, err := st.MemoryRetrievalItems(pack.RetrievalID)
	if err != nil || len(items) != 1 || items[0].MemoryID != rec.ID {
		t.Fatalf("ledger items = %+v err=%v", items, err)
	}
}

func seedDreamFeedbackMemory(t *testing.T, st *store.Store, project, title string) *store.MemoryRecord {
	t.Helper()
	env := &store.MemoryEnvelope{
		SchemaVersion: store.MemorySchemaVersion,
		ProjectID:     project,
		Kind:          store.MemoryKindFix,
		Title:         title,
		Summary:       title + " summary",
		Assertions: []store.MemoryAssertion{{
			ID:            "A-001",
			AssertionType: store.MemoryAssertionDescriptive,
			Predicate:     "causes",
			Statement:     title + " summary",
			Subject:       store.MemorySubject{ContextEntity: "project", Value: project},
			Object:        store.MemoryObject{Literal: &store.MemoryLiteral{Type: "string", Value: "fix"}},
			Confidence:    0.8,
			Importance:    4,
			EvidenceRefs:  []string{"E-001"},
		}},
		Evidence: []store.MemoryEvidence{{
			ID:      "E-001",
			Kind:    store.MemoryEvidenceAgentInitiated,
			Summary: "Recorded during a test.",
		}},
	}
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), project, env, store.Provenance{})
	if err != nil {
		t.Fatalf("save memory: %v", err)
	}
	return rec
}
