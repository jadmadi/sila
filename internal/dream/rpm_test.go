package dream

import (
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestCompareProposalsTournament(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "rpm_test.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	// Setup non-goal: No CGO dependencies
	_, err = st.InsertNonGoal("test-project", "No CGO dependencies or external C toolchains", "antigravity")
	if err != nil {
		t.Fatalf("InsertNonGoal failed: %v", err)
	}

	// Setup historical preference: pure-go preferred
	_ = st.SavePreference(&store.Preference{
		ProjectID:    "test-project",
		ContextQuery: "sqlite driver",
		CandidateA:   "pure-go modernc.org/sqlite",
		CandidateB:   "mattn/go-sqlite3 with CGO",
		Winner:       "A",
		Rationale:    "Zero CGO build portability",
	})

	cands := []string{
		"Use mattn/go-sqlite3 with CGO for maximum speed",
		"Use modernc.org/sqlite pure-Go implementation",
	}

	res, err := CompareProposals(st, "test-project", "", "choose sqlite library", cands)
	if err != nil {
		t.Fatalf("CompareProposals failed: %v", err)
	}

	if len(res.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(res.Candidates))
	}

	// Candidate 1 (index 1: pure-Go) should beat Candidate 0 (index 0: CGO)
	if res.WinningIndex != 1 {
		t.Fatalf("expected candidate index 1 to win, got %d (Candidate 0 score=%d, Candidate 1 score=%d)",
			res.WinningIndex, res.Candidates[0].TournamentScore, res.Candidates[1].TournamentScore)
	}

	if res.Candidates[0].Triage.Verdict != "REJECTED" {
		t.Fatalf("expected CGO proposal to be REJECTED, got %s", res.Candidates[0].Triage.Verdict)
	}

	if res.Candidates[1].Triage.Verdict != "APPROVED" {
		t.Fatalf("expected pure-Go proposal to be APPROVED, got %s", res.Candidates[1].Triage.Verdict)
	}

	if len(res.Matches) != 1 {
		t.Fatalf("expected 1 pairwise match, got %d", len(res.Matches))
	}

	if res.Matches[0].Winner != 1 {
		t.Fatalf("expected Match winner to be 1, got %d", res.Matches[0].Winner)
	}
}
