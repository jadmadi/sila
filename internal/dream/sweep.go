package dream

import (
	"context"
	"fmt"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// CompactionSweepOptions configures the A2 extractive compaction sweep over cold episodic memories.
type CompactionSweepOptions struct {
	ProjectID     string  `json:"project_id"`
	ColdThreshold float64 `json:"cold_threshold"` // default: 0.35 (or age > 30d)
	DryRun        bool    `json:"dry_run"`
	MaxCandidates int     `json:"max_candidates"` // 0 = default (100)
}

// CompactionSweepTelemetry holds comprehensive aggregate statistics from a compaction sweep.
type CompactionSweepTelemetry struct {
	ProjectID        string                     `json:"project_id"`
	CandidatesFound  int                        `json:"candidates_found"`
	CompactedCount   int                        `json:"compacted_count"`
	SkippedCount     int                        `json:"skipped_count"`
	OriginalTokens   int                        `json:"original_tokens"`
	FinalTokens      int                        `json:"final_tokens"`
	SavedTokens      int                        `json:"saved_tokens"`
	SavingsRatio     float64                    `json:"savings_ratio"`
	TotalPathsMined  int                        `json:"total_paths_mined"`
	TotalErrorsMined int                        `json:"total_errors_mined"`
	TotalSymsMined   int                        `json:"total_syms_mined"`
	TotalCmdsMined   int                        `json:"total_cmds_mined"`
	DryRun           bool                       `json:"dry_run"`
	Items            []ExtractiveCompactionItem `json:"items,omitempty"`
}

// ExtractiveCompactionItem records per-item compaction diagnostics.
type ExtractiveCompactionItem struct {
	ID             string  `json:"id"`
	Kind           string  `json:"kind"`
	OriginalTokens int     `json:"original_tokens"`
	FinalTokens    int     `json:"final_tokens"`
	SavingsRatio   float64 `json:"savings_ratio"`
	Summary        string  `json:"summary"`
}

// RunCompactionSweep iterates through cold uncompacted episodic facts/memos/lessons
// and performs A2 zero-LLM extractive keep-token compaction before any eviction.
func RunCompactionSweep(ctx context.Context, st *store.Store, opts CompactionSweepOptions) (*CompactionSweepTelemetry, error) {
	if opts.MaxCandidates <= 0 {
		opts.MaxCandidates = 100
	}
	if opts.ColdThreshold <= 0 {
		opts.ColdThreshold = 0.35
	}

	candidates, err := st.GetCompactionCandidates(opts.ProjectID, opts.MaxCandidates)
	if err != nil {
		return nil, fmt.Errorf("retrieve compaction candidates: %w", err)
	}

	telemetry := &CompactionSweepTelemetry{
		ProjectID:       opts.ProjectID,
		CandidatesFound: len(candidates),
		DryRun:          opts.DryRun,
		Items:           make([]ExtractiveCompactionItem, 0, len(candidates)),
	}

	now := time.Now().UTC()

	for _, f := range candidates {
		// Terminal anti-recompaction invariant
		if f.IsCompacted() || IsAlreadyCompacted(f.Claim) {
			telemetry.SkippedCount++
			continue
		}

		// Assess whether memory qualifies as cold using mathematical retention decay
		isCold := false
		if opts.ColdThreshold >= 1.0 {
			// Forced sweep
			isCold = true
		} else {
			// Semantic invariants and manual memos are strictly immune to decay and compaction
			if f.Tier == string(store.TierSemantic) || f.IsInvariant() || f.IsManualMemo() {
				isCold = false
			} else {
				retention := store.CalculateFactRetention(f, now, store.DefaultDecayParams())
				if retention < opts.ColdThreshold {
					isCold = true
				}
				// Also qualify if older than 30 days without access reinforcement
				if t, err := time.Parse(time.RFC3339, f.FirstSeen); err == nil && t.Before(now.Add(-30*24*time.Hour)) && f.AccessCount == 0 {
					isCold = true
				}
			}
		}

		if !isCold {
			telemetry.SkippedCount++
			continue
		}

		res := CompactExtractive(f.Claim)
		if res.FinalTokens >= res.OriginalTokens {
			telemetry.SkippedCount++
			continue
		}
		telemetry.CompactedCount++
		telemetry.OriginalTokens += res.OriginalTokens
		telemetry.FinalTokens += res.FinalTokens
		telemetry.TotalPathsMined += len(res.Tokens.FilePaths)
		telemetry.TotalErrorsMined += len(res.Tokens.ErrorCodes)
		telemetry.TotalSymsMined += len(res.Tokens.Symbols)
		telemetry.TotalCmdsMined += len(res.Tokens.Commands)

		item := ExtractiveCompactionItem{
			ID:             f.ID,
			Kind:           f.Kind,
			OriginalTokens: res.OriginalTokens,
			FinalTokens:    res.FinalTokens,
			SavingsRatio:   res.SavingsRatio,
			Summary:        truncateWithEllipsis(res.CompactedBody, 80),
		}
		telemetry.Items = append(telemetry.Items, item)

		if !opts.DryRun {
			// Merge newly mined repo files with existing repo files
			mergedFiles := append([]string{}, f.RepoFiles...)
			for _, p := range res.Tokens.FilePaths {
				found := false
				for _, existing := range mergedFiles {
					if existing == p {
						found = true
						break
					}
				}
				if !found {
					mergedFiles = append(mergedFiles, p)
				}
			}

			origRef := f.FactHash
			if origRef == "" {
				origRef = f.ID
			}

			if err := st.CompactFact(ctx, f.ID, res.CompactedBody, mergedFiles, origRef); err != nil {
				return telemetry, fmt.Errorf("compact fact %s: %w", f.ID, err)
			}
		}
	}

	if telemetry.OriginalTokens > 0 {
		telemetry.SavedTokens = telemetry.OriginalTokens - telemetry.FinalTokens
		if telemetry.SavedTokens < 0 {
			telemetry.SavedTokens = 0
		}
		telemetry.SavingsRatio = float64(telemetry.SavedTokens) / float64(telemetry.OriginalTokens)
	}

	return telemetry, nil
}
