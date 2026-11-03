package dream

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// CandidateEvaluation represents the score and analysis of a single proposal in a tournament.
type CandidateEvaluation struct {
	Index             int          `json:"index"`
	Proposal          string       `json:"proposal"`
	Triage            TriageResult `json:"triage"`
	HistoricalWins    int          `json:"historical_wins"`
	HistoricalMatches int          `json:"historical_matches"`
	PilotSuccess      *bool        `json:"pilot_success,omitempty"`
	PilotOutput       string       `json:"pilot_output,omitempty"`
	TournamentScore   int          `json:"tournament_score"` // combined 0-100 score
	Rank              int          `json:"rank"`
}

// PairwiseMatch represents one head-to-head match between two candidates.
type PairwiseMatch struct {
	CandidateA int    `json:"candidate_a"`
	CandidateB int    `json:"candidate_b"`
	Winner     int    `json:"winner"` // index of winner, or -1 for tie
	Margin     int    `json:"margin"`
	Rationale  string `json:"rationale"`
}

// TournamentResult represents the overall evaluation across 2+ candidates.
type TournamentResult struct {
	ProjectID       string                `json:"project_id"`
	ContextQuery    string                `json:"context_query,omitempty"`
	Candidates      []CandidateEvaluation `json:"candidates"`
	Matches         []PairwiseMatch       `json:"matches"`
	WinningIndex    int                   `json:"winning_index"`
	WinningProposal string                `json:"winning_proposal"`
	Summary         string                `json:"summary"`
	EvaluatedAt     string                `json:"evaluated_at"`
}

// CompareProposals evaluates 2 or more proposals in a round-robin tournament against project non-goals,
// architectural invariants, and stored historical pairwise preferences.
func CompareProposals(st *store.Store, projectID, projectDir, contextQuery string, proposals []string) (*TournamentResult, error) {
	if len(proposals) < 2 {
		return nil, fmt.Errorf("at least 2 candidate proposals required for tournament comparison")
	}
	if projectID == "" {
		projectID = "sila"
	}

	// Fetch historical preferences for project
	var historicalPrefs []store.Preference
	if st != nil {
		prefs, err := st.Preferences(store.PreferenceFilter{ProjectID: projectID, Limit: 100})
		if err == nil {
			historicalPrefs = prefs
		}
	}

	res := &TournamentResult{
		ProjectID:    projectID,
		ContextQuery: contextQuery,
		EvaluatedAt:  time.Now().UTC().Format(time.RFC3339),
		Candidates:   make([]CandidateEvaluation, len(proposals)),
	}

	// 1. Individual Triage & Historical Alignment
	for i, prop := range proposals {
		clean := strings.TrimSpace(prop)
		var tr TriageResult
		var err error
		if st != nil {
			tr, err = TriageProposal(st, projectID, projectDir, clean)
		}
		if err != nil || st == nil {
			tr = TriageResult{
				Proposal: clean,
				Verdict:  "APPROVED",
				FitScore: 70,
			}
		}

		// Calculate historical win rate against stored preferences
		hw, hm := matchHistoricalPreferences(clean, historicalPrefs)

		score := tr.FitScore
		// Bonus for historical consistency (up to +15)
		if hm > 0 {
			rate := float64(hw) / float64(hm)
			score += int(rate * 15)
		}
		if score > 100 {
			score = 100
		}
		if score < 0 {
			score = 0
		}

		res.Candidates[i] = CandidateEvaluation{
			Index:             i,
			Proposal:          clean,
			Triage:            tr,
			HistoricalWins:    hw,
			HistoricalMatches: hm,
			TournamentScore:   score,
		}
	}

	// 2. Round-Robin Pairwise Matches
	for i := 0; i < len(res.Candidates); i++ {
		for j := i + 1; j < len(res.Candidates); j++ {
			ca := &res.Candidates[i]
			cb := &res.Candidates[j]

			match := evaluatePairwise(ca, cb)
			res.Matches = append(res.Matches, match)
		}
	}

	// 3. Rank Candidates by TournamentScore
	type indexedScore struct {
		index int
		score int
	}
	scores := make([]indexedScore, len(res.Candidates))
	for i, c := range res.Candidates {
		scores[i] = indexedScore{index: i, score: c.TournamentScore}
	}
	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	for rank, item := range scores {
		res.Candidates[item.index].Rank = rank + 1
	}

	bestIdx := scores[0].index
	res.WinningIndex = bestIdx
	res.WinningProposal = res.Candidates[bestIdx].Proposal

	winner := res.Candidates[bestIdx]
	if winner.Triage.Verdict == "REJECTED" {
		res.Summary = fmt.Sprintf("All candidates violate project non-goals or invariants. Best relative candidate (%d) still requires major redesign.", bestIdx+1)
	} else if len(scores) > 1 && scores[0].score == scores[1].score {
		res.Summary = fmt.Sprintf("Tie between candidate %d and candidate %d (score %d). Review individual trade-offs.", bestIdx+1, scores[1].index+1, scores[0].score)
	} else {
		res.Summary = fmt.Sprintf("Candidate %d selected as winner (score %d/100, verdict %s). Aligns best with project invariants.", bestIdx+1, winner.TournamentScore, winner.Triage.Verdict)
	}

	return res, nil
}

func evaluatePairwise(a, b *CandidateEvaluation) PairwiseMatch {
	match := PairwiseMatch{
		CandidateA: a.Index,
		CandidateB: b.Index,
		Winner:     -1,
	}

	scoreDiff := a.TournamentScore - b.TournamentScore
	if scoreDiff > 0 {
		match.Winner = a.Index
		match.Margin = scoreDiff
		match.Rationale = fmt.Sprintf("Candidate %d scored %d vs Candidate %d scored %d (%s vs %s)",
			a.Index+1, a.TournamentScore, b.Index+1, b.TournamentScore, a.Triage.Verdict, b.Triage.Verdict)
	} else if scoreDiff < 0 {
		match.Winner = b.Index
		match.Margin = -scoreDiff
		match.Rationale = fmt.Sprintf("Candidate %d scored %d vs Candidate %d scored %d (%s vs %s)",
			b.Index+1, b.TournamentScore, a.Index+1, a.TournamentScore, b.Triage.Verdict, a.Triage.Verdict)
	} else {
		match.Rationale = fmt.Sprintf("Equal fit score (%d). No structural conflict differentiator found.", a.TournamentScore)
	}

	return match
}

func matchHistoricalPreferences(prop string, prefs []store.Preference) (wins, matches int) {
	propLower := strings.ToLower(prop)
	words := strings.Fields(propLower)
	if len(words) == 0 {
		return 0, 0
	}

	for _, p := range prefs {
		aLower := strings.ToLower(p.CandidateA)
		bLower := strings.ToLower(p.CandidateB)

		matchA := containsKeyTerms(aLower, words)
		matchB := containsKeyTerms(bLower, words)

		if matchA && !matchB {
			matches++
			if p.Winner == "A" {
				wins++
			}
		} else if matchB && !matchA {
			matches++
			if p.Winner == "B" {
				wins++
			}
		}
	}
	return wins, matches
}

func containsKeyTerms(target string, terms []string) bool {
	matches := 0
	for _, t := range terms {
		if len(t) <= 3 {
			continue
		}
		if strings.Contains(target, t) {
			matches++
		}
	}
	return matches >= 2 || (len(terms) < 3 && matches >= 1)
}
