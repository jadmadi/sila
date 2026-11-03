package release

import (
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestComputeSemverCandidates_Milestone(t *testing.T) {
	impact := ImpactSummaryInfo{
		MinorFeatures:     13,
		PatchFixes:        2,
		InternalDocs:      26,
		TotalCommitsAhead: 41,
		HighestImpact:     "MINOR",
	}
	roadmap := RoadmapStatusInfo{
		TotalGoals:        217,
		ImplementedGoals:  216,
		SupersededGoals:   1,
		TotalTasks:        1249,
		CompletedTasks:    1249,
		CompletionPct:     100.0,
		IsRoadmapComplete: true,
	}
	wt := WorkingTreeInfo{
		IsDirty: false,
	}

	cand, verdict := computeSemverCandidates("v0.9.0", impact, roadmap, wt, false)

	if cand.RecommendedVersion != "v1.0.0" {
		t.Errorf("expected recommended v1.0.0 for 100%% roadmap completion, got %s", cand.RecommendedVersion)
	}
	if cand.MilestoneCandidate != "v1.0.0" {
		t.Errorf("expected milestone candidate v1.0.0, got %s", cand.MilestoneCandidate)
	}
	if cand.MinorCandidate != "v0.10.0" {
		t.Errorf("expected minor candidate v0.10.0, got %s", cand.MinorCandidate)
	}
	if cand.PatchCandidate != "v0.9.1" {
		t.Errorf("expected patch candidate v0.9.1, got %s", cand.PatchCandidate)
	}
	if verdict.Code != "READY_TO_RELEASE" {
		t.Errorf("expected READY_TO_RELEASE, got %s", verdict.Code)
	}
	if !strings.Contains(verdict.Verdict, "WATERSHED MILESTONE") {
		t.Errorf("expected WATERSHED MILESTONE in verdict headline, got %s", verdict.Verdict)
	}
}

func TestComputeSemverCandidates_ConventionalMinor(t *testing.T) {
	impact := ImpactSummaryInfo{
		MinorFeatures:     3,
		PatchFixes:        1,
		TotalCommitsAhead: 4,
		HighestImpact:     "MINOR",
	}
	roadmap := RoadmapStatusInfo{
		TotalGoals:        100,
		ImplementedGoals:  50,
		IsRoadmapComplete: false,
	}
	wt := WorkingTreeInfo{IsDirty: false}

	cand, verdict := computeSemverCandidates("v0.9.0", impact, roadmap, wt, false)

	if cand.RecommendedVersion != "v0.10.0" {
		t.Errorf("expected recommended v0.10.0, got %s", cand.RecommendedVersion)
	}
	if cand.MilestoneCandidate != "" {
		t.Errorf("expected empty milestone candidate, got %s", cand.MilestoneCandidate)
	}
	if verdict.Code != "READY_TO_RELEASE" {
		t.Errorf("expected READY_TO_RELEASE, got %s", verdict.Code)
	}
}

func TestComputeSemverCandidates_DirtyWorktree(t *testing.T) {
	impact := ImpactSummaryInfo{
		MinorFeatures:     5,
		TotalCommitsAhead: 5,
		HighestImpact:     "MINOR",
	}
	roadmap := RoadmapStatusInfo{IsRoadmapComplete: true}
	wt := WorkingTreeInfo{
		IsDirty:        true,
		ModifiedFiles:  2,
		UntrackedFiles: 1,
		LinesAdded:     10,
		LinesDeleted:   4,
	}

	_, verdict := computeSemverCandidates("v0.9.0", impact, roadmap, wt, false)
	if verdict.Code != "PENDING_COMMITS" {
		t.Errorf("expected PENDING_COMMITS for dirty worktree, got %s", verdict.Code)
	}

	// With allowDirty == true
	_, verdictAllowed := computeSemverCandidates("v0.9.0", impact, roadmap, wt, true)
	if verdictAllowed.Code != "READY_TO_RELEASE" {
		t.Errorf("expected READY_TO_RELEASE when allowDirty is true, got %s", verdictAllowed.Code)
	}
}

func TestComputeSemverCandidates_UpToDate(t *testing.T) {
	impact := ImpactSummaryInfo{TotalCommitsAhead: 0, HighestImpact: "NONE"}
	roadmap := RoadmapStatusInfo{IsRoadmapComplete: false}
	wt := WorkingTreeInfo{IsDirty: false}

	cand, verdict := computeSemverCandidates("v0.9.0", impact, roadmap, wt, false)
	if verdict.Code != "UP_TO_DATE" {
		t.Errorf("expected UP_TO_DATE, got %s", verdict.Code)
	}
	if cand.RecommendedVersion != "v0.9.0" {
		t.Errorf("expected recommended v0.9.0, got %s", cand.RecommendedVersion)
	}
}

func TestComputeSemverCandidates_InternalOnly(t *testing.T) {
	impact := ImpactSummaryInfo{
		InternalDocs:      2,
		TotalCommitsAhead: 2,
		HighestImpact:     "INTERNAL",
	}
	roadmap := RoadmapStatusInfo{IsRoadmapComplete: false}
	wt := WorkingTreeInfo{IsDirty: false}

	_, verdict := computeSemverCandidates("v0.9.0", impact, roadmap, wt, false)
	if verdict.Code != "INTERNAL_COMMITS_ONLY" {
		t.Errorf("expected INTERNAL_COMMITS_ONLY, got %s", verdict.Code)
	}
}

func TestRenderStatusTOON(t *testing.T) {
	report := ReleaseStatusReport{
		PublicRelease: PublicReleaseInfo{Tag: "v0.9.0", PublishedAt: "2026-10-01T07:57:37Z"},
		DevState:      DevStateInfo{Branch: "main", Commit: "1234567", GitDescribe: "v0.9.0-10-g1234567"},
		ImpactSummary: ImpactSummaryInfo{MinorFeatures: 5, TotalCommitsAhead: 5},
		RoadmapStatus: RoadmapStatusInfo{TotalGoals: 217, ImplementedGoals: 216, IsRoadmapComplete: true},
		WorkingTree:   WorkingTreeInfo{IsDirty: false},
		SemverCandidates: SemverCandidatesInfo{
			RecommendedVersion: "v1.0.0",
			MinorCandidate:     "v0.10.0",
			PatchCandidate:     "v0.9.1",
			Rationale:          "100% roadmap completed",
		},
		Verdict: ReleaseVerdictInfo{Code: "READY_TO_RELEASE"},
	}

	toon := RenderStatusTOON(report)
	if !strings.Contains(toon, "public_tag: v0.9.0") {
		t.Errorf("TOON missing public_tag: %s", toon)
	}
	if !strings.Contains(toon, "is_complete: true") {
		t.Errorf("TOON missing is_complete: true: %s", toon)
	}
	if !strings.Contains(toon, "recommended: v1.0.0") {
		t.Errorf("TOON missing recommended: v1.0.0: %s", toon)
	}
}

func TestRenderStatusTerminal(t *testing.T) {
	ui := render.New(nil, true)
	report := ReleaseStatusReport{
		PublicRelease: PublicReleaseInfo{Tag: "v0.9.0", PublishedAt: "2026-10-01T07:57:37Z", TimeAgo: "1 day ago"},
		DevState:      DevStateInfo{Branch: "main", Commit: "1234567", GitDescribe: "v0.9.0-10-g1234567"},
		ImpactSummary: ImpactSummaryInfo{MinorFeatures: 5, TotalCommitsAhead: 5, HighestImpact: "MINOR"},
		RoadmapStatus: RoadmapStatusInfo{
			TotalGoals:        217,
			ImplementedGoals:  216,
			SupersededGoals:   1,
			TotalTasks:        1249,
			CompletedTasks:    1249,
			IsRoadmapComplete: true,
		},
		MCPContract: MCPContractInfo{PublicToolsCount: 63, BreakingChangesCount: 0},
		WorkingTree: WorkingTreeInfo{IsDirty: false},
		SemverCandidates: SemverCandidatesInfo{
			MilestoneCandidate: "v1.0.0",
			MinorCandidate:     "v0.10.0",
			PatchCandidate:     "v0.9.1",
			RecommendedVersion: "v1.0.0",
		},
		Verdict: ReleaseVerdictInfo{
			Code:        "READY_TO_RELEASE",
			Verdict:     "READY TO RELEASE (WATERSHED MILESTONE)",
			Summary:     "Ready to release candidate v1.0.0",
			ActionSteps: []string{"Step 1: Gate", "Step 2: Tag"},
		},
	}

	rendered := RenderStatusTerminal(ui, report, false)
	if !strings.Contains(rendered, "Sila Release Status & Epistemic Decision Engine") {
		t.Errorf("expected header, got %s", rendered)
	}
	if !strings.Contains(rendered, "216 / 217 Implemented") {
		t.Errorf("expected roadmap stats, got %s", rendered)
	}
	if !strings.Contains(rendered, "63 Tools Registered") {
		t.Errorf("expected MCP stats, got %s", rendered)
	}
	if !strings.Contains(rendered, "Watershed Milestone:") {
		t.Errorf("expected watershed milestone, got %s", rendered)
	}
}
