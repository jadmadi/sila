package eval

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CausalVerdict classifies the observed effect of a component edit across rollouts.
type CausalVerdict string

const (
	VerdictHelped           CausalVerdict = "helped"
	VerdictHurt             CausalVerdict = "hurt"
	VerdictNoObservedEffect CausalVerdict = "no_observed_effect"
	VerdictConfounded       CausalVerdict = "confounded"
)

// ComponentAttribution records the causal verdict for a single component edit in a candidate tree.
type ComponentAttribution struct {
	Candidate string        `json:"candidate"`
	Component ComponentID   `json:"component"`
	Path      string        `json:"path"`
	Verdict   CausalVerdict `json:"verdict"`
	Evidence  string        `json:"evidence"`
}

// ExperienceRecord captures a non-confounded transferable harness improvement lesson.
type ExperienceRecord struct {
	Domain       string                 `json:"domain"`
	Cue          string                 `json:"cue"`
	Lesson       string                 `json:"lesson"`
	Evidence     string                 `json:"evidence"`
	Confidence   string                 `json:"confidence"`
	Attributions []ComponentAttribution `json:"attributions"`
}

// SelectionResult holds the outcome of blind anonymized candidate selection.
type SelectionResult struct {
	Pool         []string          `json:"pool"`
	AnonymousIDs map[string]string `json:"anonymous_ids"`
	Chosen       string            `json:"chosen"`
	Rationale    string            `json:"rationale"`
}

// BlindSelectionRequest encapsulates inputs for blind deliverable-based selection.
type BlindSelectionRequest struct {
	TaskID     string
	TaskPrompt string
	K          int
	Mode       RecursionMode
	Rollouts   map[string]RolloutEvidence
}

// SelectionJudgeFunc evaluates the anonymized candidate pool prompt and returns XML <rationale> and <selected>.
type SelectionJudgeFunc func(ctx context.Context, prompt string) (string, error)

// FilterDeliveringPool returns sorted candidate labels that produced at least one deliverable.
func FilterDeliveringPool(rollouts map[string]RolloutEvidence) []string {
	pool := make([]string, 0, len(rollouts))
	for k, ev := range rollouts {
		if ev.Delivered && len(ev.Deliverables) > 0 {
			pool = append(pool, k)
		}
	}
	sort.Strings(pool)
	return pool
}

// AssignAnonymousIDs maps candidate names to deterministic shuffled uppercase letters (A, B, C...)
// seeded by SHA-256(taskID + "|" + strconv.Itoa(k) + "|" + string(mode)).
func AssignAnonymousIDs(pool []string, taskID string, k int, mode RecursionMode) map[string]string {
	sorted := make([]string, len(pool))
	copy(sorted, pool)
	sort.Strings(sorted)

	letters := make([]string, len(sorted))
	for i := range sorted {
		if i < 26 {
			letters[i] = string(rune('A' + i))
		} else {
			letters[i] = fmt.Sprintf("C%d", i+1)
		}
	}

	sum := sha256.Sum256([]byte(taskID + "|" + strconv.Itoa(k) + "|" + string(mode)))
	seed := int64(binary.BigEndian.Uint64(sum[:8]))
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(letters), func(i, j int) {
		letters[i], letters[j] = letters[j], letters[i]
	})

	out := make(map[string]string, len(sorted))
	for i, cand := range sorted {
		out[cand] = letters[i]
	}
	return out
}

var (
	selectedTagRE  = regexp.MustCompile(`(?s)<selected>\s*([A-Za-z0-9_]+)\s*</selected>`)
	rationaleTagRE = regexp.MustCompile(`(?s)<rationale>(.*?)</rationale>`)
)

// RunBlindSelection anonymizes delivering candidates and selects the best deliverable across 6 ordered criteria.
func RunBlindSelection(ctx context.Context, req BlindSelectionRequest, judge SelectionJudgeFunc) (*SelectionResult, error) {
	pool := FilterDeliveringPool(req.Rollouts)
	if len(pool) == 0 {
		fallback := "base1"
		if _, ok := req.Rollouts[fallback]; !ok {
			for k := range req.Rollouts {
				fallback = k
				break
			}
		}
		return &SelectionResult{
			Pool:         []string{},
			AnonymousIDs: map[string]string{},
			Chosen:       fallback,
			Rationale:    "no candidate produced a deliverable; falling back to base",
		}, nil
	}

	ids := AssignAnonymousIDs(pool, req.TaskID, req.K, req.Mode)
	if len(pool) == 1 || judge == nil {
		return &SelectionResult{
			Pool:         pool,
			AnonymousIDs: ids,
			Chosen:       pool[0],
			Rationale:    "single delivering candidate or default fallback",
		}, nil
	}

	rev := make(map[string]string, len(ids))
	anonKeys := make([]string, 0, len(ids))
	for cand, anon := range ids {
		rev[anon] = cand
		anonKeys = append(anonKeys, anon)
	}
	sort.Strings(anonKeys)

	var b strings.Builder
	b.WriteString("## Blind Deliverable Selection (6 Ordered Criteria)\n")
	b.WriteString("1. Delivery: Every requested deliverable exists and is non-empty.\n")
	b.WriteString("2. Conformance: Output structure, schema, and values match request specifications.\n")
	b.WriteString("3. Fidelity: Claims and computations are grounded in source inputs.\n")
	b.WriteString("4. Internal Consistency: Totals, cross-references, and constraints agree.\n")
	b.WriteString("5. Operational Fitness: Clean execution without unverified guesses.\n")
	b.WriteString("6. Finish: Complete, well-formatted output.\n\n")
	b.WriteString("## Task Prompt\n")
	b.WriteString(strings.TrimSpace(req.TaskPrompt))
	b.WriteString("\n\n## Anonymized Candidate Pool\n")
	for _, anon := range anonKeys {
		cand := rev[anon]
		ev := req.Rollouts[cand]
		b.WriteString(fmt.Sprintf("### Candidate `%s` (deliverables=%v)\n", anon, ev.Deliverables))
		if ev.SanitizedLog != "" {
			b.WriteString(ev.SanitizedLog)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	raw, err := judge(ctx, b.String())
	if err != nil {
		return nil, err
	}

	rationale := ""
	if m := rationaleTagRE.FindStringSubmatch(raw); len(m) >= 2 {
		rationale = strings.TrimSpace(m[1])
	}
	chosen := pool[0]
	if m := selectedTagRE.FindStringSubmatch(raw); len(m) >= 2 {
		anonID := strings.TrimSpace(m[1])
		if realCand, ok := rev[anonID]; ok {
			chosen = realCand
		}
	}

	return &SelectionResult{
		Pool:         pool,
		AnonymousIDs: ids,
		Chosen:       chosen,
		Rationale:    rationale,
	}, nil
}

// AttributeComponentEdits diffs every candidate tree against baseTree and assigns a CausalVerdict:
//   - If a candidate modifies >= 2 distinct components (or paths) without isolated single-edit probe support
//     in another candidate, those edits are marked VerdictConfounded.
//   - Otherwise, edits in candidates that improved delivery over base are marked VerdictHelped, edits that
//     degraded delivery are marked VerdictHurt, and edits with identical delivery are marked VerdictNoObservedEffect.
func AttributeComponentEdits(baseTree ComponentTree, trees map[string]ComponentTree, rollouts map[string]RolloutEvidence, spec SurfaceSpec) []ComponentAttribution {
	baseDeliveredAll := true
	baseCount := 0
	for k, ev := range rollouts {
		if strings.HasPrefix(k, "base") {
			baseCount++
			if !ev.Delivered {
				baseDeliveredAll = false
			}
		}
	}

	candNames := make([]string, 0, len(trees))
	for k := range trees {
		if strings.HasPrefix(k, "base") {
			continue
		}
		candNames = append(candNames, k)
	}
	sort.Strings(candNames)

	// First pass: record which individual paths were tested in isolation (len(diffs) == 1).
	isolatedPaths := make(map[string]bool)
	perCandDiffs := make(map[string][]ComponentEdit, len(candNames))
	for _, cand := range candNames {
		diffs := DiffComponentTrees(baseTree, trees[cand], spec)
		perCandDiffs[cand] = diffs
		if len(diffs) == 1 {
			isolatedPaths[diffs[0].Path] = true
		}
	}

	attrs := make([]ComponentAttribution, 0)
	for _, cand := range candNames {
		diffs := perCandDiffs[cand]
		if len(diffs) == 0 {
			continue
		}
		ev := rollouts[cand]
		for _, d := range diffs {
			if len(diffs) > 1 && !isolatedPaths[d.Path] {
				attrs = append(attrs, ComponentAttribution{
					Candidate: cand,
					Component: d.Component,
					Path:      d.Path,
					Verdict:   VerdictConfounded,
					Evidence:  fmt.Sprintf("candidate %s bundled %d component edits without isolated probe verification for %s", cand, len(diffs), d.Path),
				})
				continue
			}

			var verdict CausalVerdict
			var evidence string
			switch {
			case ev.Delivered && (baseCount == 0 || !baseDeliveredAll):
				verdict = VerdictHelped
				evidence = fmt.Sprintf("isolated %s edit (%s %s) in %s delivered %v where base replicate(s) failed", d.Component, d.Action, d.Path, cand, ev.Deliverables)
			case !ev.Delivered && baseDeliveredAll && baseCount > 0:
				verdict = VerdictHurt
				evidence = fmt.Sprintf("isolated %s edit (%s %s) in %s failed to deliver while base replicates delivered", d.Component, d.Action, d.Path, cand)
			default:
				verdict = VerdictNoObservedEffect
				evidence = fmt.Sprintf("isolated %s edit (%s %s) in %s showed no delivery delta over base", d.Component, d.Action, d.Path, cand)
			}

			attrs = append(attrs, ComponentAttribution{
				Candidate: cand,
				Component: d.Component,
				Path:      d.Path,
				Verdict:   verdict,
				Evidence:  evidence,
			})
		}
	}
	return attrs
}

// ExtractCausalExperience distills non-confounded VerdictHelped attributions into an ExperienceRecord.
// Returns nil if zero component edits have VerdictHelped.
func ExtractCausalExperience(taskID, domain string, attrs []ComponentAttribution) *ExperienceRecord {
	helped := make([]ComponentAttribution, 0)
	for _, a := range attrs {
		if a.Verdict == VerdictHelped {
			helped = append(helped, a)
		}
	}
	if len(helped) == 0 {
		return nil
	}

	first := helped[0]
	return &ExperienceRecord{
		Domain:       domain,
		Cue:          fmt.Sprintf("when running %s tasks where replicates diverge before delivery", domain),
		Lesson:       fmt.Sprintf("apply isolated %s component (%s) to enforce pre-delivery verification", first.Component, first.Path),
		Evidence:     fmt.Sprintf("[%s] %s", taskID, first.Evidence),
		Confidence:   "high",
		Attributions: helped,
	}
}
