package assist_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/store"
)

type fixedMockProvider struct {
	noul   float64
	action string
	err    error
}

func (f *fixedMockProvider) Name() assist.ModelProvider {
	return assist.ProviderMock
}

func (f *fixedMockProvider) Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]assist.Question) (*assist.ProviderResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &assist.ProviderResponse{
		Nouls: map[string]assist.NoulAnswer{
			"needs_instruction_change": {Noul: f.noul},
		},
		Choices: map[string]assist.ChoiceAnswer{
			"suggested_action": {Choice: f.action, Confidence: 0.95},
		},
	}, nil
}

func TestInstructionReview_DistinctIncidentsProduceDistinctDiffs(t *testing.T) {
	ctx := context.Background()
	p := &fixedMockProvider{noul: 0.95, action: "add"}

	msgs1 := []store.StoredMessage{{ID: 101, TS: "2026-09-18T10:00:00Z", Role: "assistant", Content: "An agent introduced CGO into a pure Go package."}}
	msgs2 := []store.StoredMessage{{ID: 102, TS: "2026-09-18T10:05:00Z", Role: "assistant", Content: "An agent deleted an unbacked SQLite database file."}}

	res1 := assist.ProposeInstructionUpdates(ctx, p, "Use pure Go.\n", "AGENTS.md", msgs1)
	res2 := assist.ProposeInstructionUpdates(ctx, p, "Use pure Go.\n", "AGENTS.md", msgs2)

	if len(res1) != 1 || len(res2) != 1 {
		t.Fatalf("expected 1 proposal each, got %d and %d", len(res1), len(res2))
	}
	if res1[0].ProposedDiff == res2[0].ProposedDiff {
		t.Errorf("expected distinct diffs for distinct incidents, got identical diff: %s", res1[0].ProposedDiff)
	}
	if res1[0].FailureSummary == res2[0].FailureSummary {
		t.Errorf("expected distinct failure summaries, got identical: %s", res1[0].FailureSummary)
	}
	if !strings.Contains(res1[0].ProposedDiff, "CGO") {
		t.Errorf("expected res1 diff to mention CGO, got: %s", res1[0].ProposedDiff)
	}
	if !strings.Contains(res2[0].ProposedDiff, "SQLite") && !strings.Contains(res2[0].ProposedDiff, "database") {
		t.Errorf("expected res2 diff to mention SQLite/database, got: %s", res2[0].ProposedDiff)
	}
}

func TestInstructionReview_ActionsImplemented(t *testing.T) {
	ctx := context.Background()
	existing := "# Rules\n\n- Avoid table locks and database contention\n- Existing rule 2\n"

	actions := []string{"add", "clarify", "narrow", "remove"}
	for _, action := range actions {
		p := &fixedMockProvider{noul: 0.95, action: action}
		msgs := []store.StoredMessage{{ID: 1, TS: "2026-09-18T10:00:00Z", Role: "assistant", Content: "Contention detected on table locks."}}
		props := assist.ProposeInstructionUpdates(ctx, p, existing, "AGENTS.md", msgs)
		if len(props) != 1 {
			t.Fatalf("action %q: expected 1 proposal, got %d", action, len(props))
		}
		prop := props[0]
		if string(prop.Action) != action {
			t.Errorf("action %q: got action %q", action, prop.Action)
		}
		if prop.ProposedDiff == "" {
			t.Errorf("action %q: proposed diff is empty", action)
		}
		if prop.TargetContentHash == "" {
			t.Errorf("action %q: target hash is empty", action)
		}
		if len(prop.EvidenceSources) != 1 || !strings.Contains(prop.EvidenceSources[0], "msg_1") {
			t.Errorf("action %q: expected specific evidence source, got %v", action, prop.EvidenceSources)
		}
	}
}

func TestInstructionReview_RuleMatchingAndNoUnrelatedRemoval(t *testing.T) {
	ctx := context.Background()
	existing := "# Rules\n- Never commit secrets.\n- Use the obsolete runtime.\n"
	msgs := []store.StoredMessage{{
		ID:      1,
		TS:      "2026-09-18T12:00:00Z",
		Role:    "assistant",
		Content: "Node upgrade failed because the legacy deployment requirement conflicts with the current supported runtime.",
	}}

	pRemove := &fixedMockProvider{noul: 0.99, action: "remove"}
	props := assist.ProposeInstructionUpdates(ctx, pRemove, existing, "AGENTS.md", msgs)
	if len(props) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(props))
	}
	if strings.Contains(props[0].ProposedDiff, "-- Never commit secrets.") {
		t.Errorf("unrelated secret rule was removed in diff: %s", props[0].ProposedDiff)
	}
	if !strings.Contains(props[0].ProposedDiff, "-- Use the obsolete runtime.") {
		t.Errorf("expected matching runtime rule to be removed in diff: %s", props[0].ProposedDiff)
	}

	// When no rule matches, clarify/narrow/remove produce no proposals
	unmatchedRules := "# Rules\n- Rule about something completely unrelated.\n"
	for _, act := range []string{"clarify", "narrow", "remove"} {
		p := &fixedMockProvider{noul: 0.99, action: act}
		unmatchedProps := assist.ProposeInstructionUpdates(ctx, p, unmatchedRules, "AGENTS.md", msgs)
		if len(unmatchedProps) != 0 {
			t.Errorf("action %s: expected 0 proposals when no matching rule exists, got %d", act, len(unmatchedProps))
		}
	}
}

func TestInstructionReview_AlreadyCoveredRule(t *testing.T) {
	ctx := context.Background()
	p := &fixedMockProvider{noul: 0.95, action: "add"}
	existing := "# Rules\n\n- Invariant: Never allow an agent introduced CGO.\n"
	msgs := []store.StoredMessage{{ID: 1, TS: "2026-09-18T10:00:00Z", Role: "assistant", Content: "An agent introduced CGO."}}

	props := assist.ProposeInstructionUpdates(ctx, p, existing, "AGENTS.md", msgs)
	if len(props) != 0 {
		t.Errorf("expected no proposals when rule is already covered, got %d", len(props))
	}
}

func TestInstructionReview_WeakEvidenceAndNoChange(t *testing.T) {
	ctx := context.Background()
	msgs := []store.StoredMessage{{ID: 1, Role: "assistant", Content: "Normal operational log."}}

	// Low noul
	pWeak := &fixedMockProvider{noul: 0.40, action: "add"}
	if props := assist.ProposeInstructionUpdates(ctx, pWeak, "# Rules\n", "AGENTS.md", msgs); len(props) != 0 {
		t.Errorf("expected 0 proposals for low noul, got %d", len(props))
	}

	// Action no_change
	pNoChange := &fixedMockProvider{noul: 0.95, action: "no_change"}
	if props := assist.ProposeInstructionUpdates(ctx, pNoChange, "# Rules\n", "AGENTS.md", msgs); len(props) != 0 {
		t.Errorf("expected 0 proposals for no_change, got %d", len(props))
	}
}

func TestInstructionReview_EmptyTargetHandling(t *testing.T) {
	ctx := context.Background()
	p := &fixedMockProvider{noul: 0.95, action: "add"}
	msgs := []store.StoredMessage{{ID: 1, TS: "2026-09-18T10:00:00Z", Role: "assistant", Content: "Build failed: pure Go constraint violated."}}

	props := assist.ProposeInstructionUpdates(ctx, p, "", "AGENTS.md", msgs)
	if len(props) != 1 {
		t.Fatalf("expected 1 proposal for empty target on add, got %d", len(props))
	}
	if props[0].TargetContentHash != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("unexpected target content hash for empty file: %s", props[0].TargetContentHash)
	}
	if !strings.Contains(props[0].ProposedDiff, "+# Rules") {
		t.Errorf("expected initial rules in diff, got: %s", props[0].ProposedDiff)
	}
}

func TestInstructionReview_TargetStalenessDetection(t *testing.T) {
	original := "# Rules\n- Initial rule\n"
	modified := "# Rules\n- Modified rule\n"

	p := &fixedMockProvider{noul: 0.95, action: "add"}
	msgs := []store.StoredMessage{{ID: 1, Role: "assistant", Content: "Lock contention observed."}}
	props := assist.ProposeInstructionUpdates(context.Background(), p, original, "AGENTS.md", msgs)
	if len(props) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(props))
	}

	prop := props[0]
	if prop.IsTargetStale(original) {
		t.Errorf("expected target to not be stale when content matches")
	}
	if !prop.IsTargetStale(modified) {
		t.Errorf("expected target to be reported stale when content changed")
	}
}

func TestInstructionReview_GlobalScopeRequiresCrossProjectEvidence(t *testing.T) {
	ctx := context.Background()
	p := &fixedMockProvider{noul: 0.95, action: "add"}

	// Single project evidence: rejected for global scope
	singleProjectMsgs := []store.StoredMessage{
		{ID: 1, ProjectID: "proj-alpha", Content: "Timeout in worker loop."},
		{ID: 2, ProjectID: "proj-alpha", Content: "Worker deadlock encountered."},
	}
	propsSingle := assist.ProposeInstructionUpdatesWithOptions(ctx, p, "# Rules\n", "AGENTS.md", singleProjectMsgs, assist.InstructionReviewOptions{Scope: assist.ScopeGlobal})
	if len(propsSingle) != 0 {
		t.Errorf("expected global scope to be rejected with single-project evidence, got %d", len(propsSingle))
	}

	// Multi-project evidence: accepted for global scope
	multiProjectMsgs := []store.StoredMessage{
		{ID: 1, ProjectID: "proj-alpha", Content: "Worker deadlock encountered."},
		{ID: 2, ProjectID: "proj-beta", Content: "Worker deadlock observed in beta."},
	}
	propsMulti := assist.ProposeInstructionUpdatesWithOptions(ctx, p, "# Rules\n", "AGENTS.md", multiProjectMsgs, assist.InstructionReviewOptions{Scope: assist.ScopeGlobal})
	if len(propsMulti) != 1 {
		t.Fatalf("expected 1 proposal for global scope with cross-project evidence, got %d", len(propsMulti))
	}
	if propsMulti[0].Scope != assist.ScopeGlobal {
		t.Errorf("expected scope %q, got %q", assist.ScopeGlobal, propsMulti[0].Scope)
	}

	// Default project scope works with single project evidence
	propsLocal := assist.ProposeInstructionUpdates(ctx, p, "# Rules\n", "AGENTS.md", singleProjectMsgs)
	if len(propsLocal) != 1 {
		t.Fatalf("expected 1 proposal for local project scope, got %d", len(propsLocal))
	}
	if propsLocal[0].Scope != assist.ScopeProject {
		t.Errorf("expected scope %q, got %q", assist.ScopeProject, propsLocal[0].Scope)
	}
}

func TestInstructionReview_ZeroFileMutations(t *testing.T) {
	tmp := t.TempDir()
	targetFile := filepath.Join(tmp, "AGENTS.md")
	content := "# Invariant\n- Rule 1\n"
	if err := os.WriteFile(targetFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	p := &fixedMockProvider{noul: 0.95, action: "add"}
	msgs := []store.StoredMessage{{ID: 1, Role: "assistant", Content: "Fatal deadlock on channel read."}}

	props := assist.ProposeInstructionUpdates(context.Background(), p, content, targetFile, msgs)
	if len(props) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(props))
	}

	// Read file after proposal generation: MUST be strictly unchanged
	readBack, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(readBack) != content {
		t.Errorf("file was mutated during instruction review! Expected %q, got %q", content, string(readBack))
	}
}
