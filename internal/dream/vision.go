package dream

import (
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// VisionHorizon represents a strategic milestone or roadmap objective.
type VisionHorizon struct {
	Slug        string `json:"slug,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"` // delivered | active | planned
	Commit      string `json:"commit,omitempty"`
}

// VisionDocument holds the structured components of a project's vision.
type VisionDocument struct {
	ProjectID         string          `json:"project_id"`
	NorthStar         string          `json:"north_star"`
	DeliveredHorizons []VisionHorizon `json:"delivered_horizons"`
	NextHorizons      []VisionHorizon `json:"next_horizons"`
	NonGoals          []store.Fact    `json:"non_goals"`
	Tenets            []store.Fact    `json:"tenets"`
}

// TriageResult represents the structured evaluation of a feature/refactor proposal.
type TriageResult struct {
	Proposal          string   `json:"proposal"`
	Verdict           string   `json:"verdict"`   // APPROVED | REJECTED | NEEDS_ALIGNMENT
	FitScore          int      `json:"fit_score"` // 0-100
	DirectConflicts   []string `json:"direct_conflicts"`
	ViolatedNonGoals  []string `json:"violated_nongoals"`
	StrategicFit      string   `json:"strategic_fit"`
	RecommendedAction string   `json:"recommended_action"`
}

// CompileVision aggregates closed goals, roadmap objectives, architectural invariants,
// and explicit non-goals into a living VISION.md document.
func CompileVision(st *store.Store, projectID, projectDir string) (string, error) {
	if projectID == "" {
		projectID = "sila"
	}
	vFacts, err := st.VisionFacts(projectID)
	if err != nil {
		return "", fmt.Errorf("read vision facts: %w", err)
	}

	var nonGoals []store.Fact
	var milestones []store.Fact
	var tenets []store.Fact

	for _, f := range vFacts {
		if f.IsNonGoal() {
			nonGoals = append(nonGoals, f)
		} else if f.IsMilestone() {
			milestones = append(milestones, f)
		} else if f.IsInvariant() {
			tenets = append(tenets, f)
		}
	}

	// Scan repository goals for delivered & planned horizons
	var delivered []VisionHorizon
	var planned []VisionHorizon

	if projectDir != "" {
		audit, err := goals.ScanGoals(projectDir)
		if err == nil && audit != nil {
			for _, g := range audit.Goals {
				h := VisionHorizon{
					Slug:        g.Slug,
					Title:       g.Title,
					Description: g.Description,
					Status:      g.Status,
					Commit:      g.Commit,
				}
				if g.Status == "implemented" {
					delivered = append(delivered, h)
				} else {
					planned = append(planned, h)
				}
			}
		}
	}

	// Add stored milestones to horizons
	for _, m := range milestones {
		claim := cleanPrefix(m.Claim, "milestone:")
		planned = append(planned, VisionHorizon{
			Title:       claim,
			Description: "Strategic milestone recorded in vision store.",
			Status:      "planned",
		})
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# VISION.md — %s\n\n", projectID))
	now := time.Now().UTC().Format("2006-01-02 15:04")
	b.WriteString(fmt.Sprintf("> **Strategic North Star & Product Guardrails**  \n> Auto-compiled by `sila vision` on %s UTC. Grounded in roadmap achievements and strategic constraints.\n\n---\n\n", now))

	// 1. Mission & North Star
	b.WriteString("## 1. Core Mission & North Star\n\n")
	b.WriteString("Be the cross-harness continuity engine for AI coding agents: one durable, local store for what they learned, what was decided, and what is being worked on. Provide continuous context priming, living specifications, and zero-token-waste ergonomics.\n\n")

	// 2. Strategic Horizons & Milestones
	b.WriteString("## 2. Strategic Horizons & Milestones\n\n")
	if len(delivered) > 0 {
		b.WriteString("### 🟢 Delivered Capabilities\n\n")
		for _, d := range delivered {
			sha := d.Commit
			if len(sha) > 7 {
				sha = sha[:7]
			}
			if sha != "" {
				b.WriteString(fmt.Sprintf("- **%s** (`%s`): %s\n", d.Title, sha, d.Description))
			} else {
				b.WriteString(fmt.Sprintf("- **%s**: %s\n", d.Title, d.Description))
			}
		}
		b.WriteString("\n")
	}

	if len(planned) > 0 {
		b.WriteString("### 🟡 Upcoming Horizons & Objectives\n\n")
		for _, p := range planned {
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", p.Title, p.Description))
		}
		b.WriteString("\n")
	}

	// 3. Explicit Non-Goals
	b.WriteString("## 3. Explicit Non-Goals (What We Will NOT Build)\n\n")
	if len(nonGoals) == 0 {
		b.WriteString("*(No explicit non-goals recorded yet. Use `sila vision add-nongoal` to define negative boundaries.)*\n\n")
	} else {
		for _, ng := range nonGoals {
			claim := cleanPrefix(ng.Claim, "non-goal:")
			claim = cleanPrefix(claim, "nongoal:")
			b.WriteString(fmt.Sprintf("- ⛔ **%s**\n", strings.TrimSpace(claim)))
		}
		b.WriteString("\n")
	}

	// 4. Architectural Tenets
	b.WriteString("## 4. Architectural Tenets & Invariants\n\n")
	if len(tenets) == 0 {
		b.WriteString("- 🛡️ **Pure-Go SQLite**: Keep `CGO_ENABLED=0` with `modernc.org/sqlite`.\n")
		b.WriteString("- 🛡️ **Local-First Grounding**: All claims must ground-truth against repository files on disk.\n")
	} else {
		for _, t := range tenets {
			claim := cleanPrefix(t.Claim, "decision:")
			claim = cleanPrefix(claim, "architecture:")
			claim = cleanPrefix(claim, "arch:")
			claim = cleanPrefix(claim, "rule:")
			claim = cleanPrefix(claim, "invariant:")
			b.WriteString(fmt.Sprintf("- 🛡️ **%s**\n", strings.TrimSpace(claim)))
		}
	}
	b.WriteString("\n---\n\n")

	// Footer
	b.WriteString("### How this document is maintained\n")
	b.WriteString("This file is a living product of `sila`. As roadmap milestones are achieved via `sila goals` or negative constraints recorded via `sila vision add-nongoal`, run `sila vision compile --out=VISION.md` to keep all human developers and AI agents aligned.\n")

	return b.String(), nil
}

// TriageProposal evaluates a feature, refactor, or dependency proposal against stored non-goals,
// architectural invariants, and existing roadmap goals.
func TriageProposal(st *store.Store, projectID, projectDir, proposal string) (TriageResult, error) {
	if projectID == "" {
		projectID = "sila"
	}
	cleanProposal := strings.TrimSpace(proposal)
	res := TriageResult{
		Proposal: cleanProposal,
		FitScore: 85,
		Verdict:  "APPROVED",
	}
	if cleanProposal == "" {
		res.Verdict = "REJECTED"
		res.FitScore = 0
		res.StrategicFit = "Empty proposal cannot be evaluated."
		res.RecommendedAction = "Provide a specific proposal statement or feature description."
		return res, nil
	}

	vFacts, err := st.VisionFacts(projectID)
	if err != nil {
		return res, fmt.Errorf("read vision facts: %w", err)
	}

	propLower := strings.ToLower(cleanProposal)

	// Check against explicit non-goals
	for _, f := range vFacts {
		if !f.IsNonGoal() {
			continue
		}
		claimClean := cleanPrefix(strings.ToLower(f.Claim), "non-goal:")
		claimClean = cleanPrefix(claimClean, "nongoal:")
		claimClean = strings.TrimSpace(claimClean)

		// Check for word overlap and negative conflict
		if hasSemanticConflict(propLower, claimClean) {
			res.ViolatedNonGoals = append(res.ViolatedNonGoals, strings.TrimSpace(f.Claim))
			res.DirectConflicts = append(res.DirectConflicts, fmt.Sprintf("Violates explicit non-goal: %s", strings.TrimSpace(f.Claim)))
		}
	}

	// Check against architectural invariants (e.g. CGO, cloud db, etc.)
	for _, f := range vFacts {
		if f.IsNonGoal() || f.IsMilestone() || !f.IsInvariant() {
			continue
		}
		invClean := strings.ToLower(f.Claim)
		if hasInvariantConflict(propLower, invClean) {
			res.DirectConflicts = append(res.DirectConflicts, fmt.Sprintf("Violates architectural invariant: %s", strings.TrimSpace(f.Claim)))
		}
	}

	// Check against existing roadmap goals
	if projectDir != "" {
		audit, _ := goals.ScanGoals(projectDir)
		if audit != nil {
			for _, g := range audit.Goals {
				titleLower := strings.ToLower(g.Title)
				slugLower := strings.ToLower(g.Slug)
				if strings.Contains(propLower, slugLower) || strings.Contains(propLower, titleLower) || strings.Contains(titleLower, propLower) {
					if g.Status == "implemented" {
						res.StrategicFit = fmt.Sprintf("Already implemented in roadmap goal '%s' (%s).", g.Title, g.Slug)
						res.RecommendedAction = fmt.Sprintf("Goal '%s' is already completed; verify existing implementation before expanding.", g.Slug)
						return res, nil
					}
					res.StrategicFit = fmt.Sprintf("Directly matches active roadmap goal '%s' (%s).", g.Title, g.Slug)
					res.RecommendedAction = fmt.Sprintf("Run 'sila goals claim %s' to begin implementation.", g.Slug)
					return res, nil
				}
			}
		}
	}

	// Determine final verdict based on conflicts
	if len(res.ViolatedNonGoals) > 0 {
		res.Verdict = "REJECTED"
		res.FitScore = 15
		res.StrategicFit = "Proposal directly contradicts one or more explicit project non-goals."
		res.RecommendedAction = "Reject proposal or redesign scope to avoid violating non-goal guardrails."
	} else if len(res.DirectConflicts) > 0 {
		res.Verdict = "NEEDS_ALIGNMENT"
		res.FitScore = 45
		res.StrategicFit = "Proposal conflicts with recorded architectural invariants or constraints."
		res.RecommendedAction = "Revise proposal architecture to comply with invariant standards."
	} else {
		res.Verdict = "APPROVED"
		res.FitScore = 90
		res.StrategicFit = "Proposal aligns with strategic vision, respects non-goals, and preserves architectural invariants."
		res.RecommendedAction = "Proceed to create a structured goal package: 'sila goals create <slug> --touch=...'"
	}

	return res, nil
}

// AddNonGoal saves a negative constraint to the vision store.
func AddNonGoal(st *store.Store, projectID, statement, agent string) (*store.Fact, error) {
	if projectID == "" {
		projectID = "sila"
	}
	return st.InsertNonGoal(projectID, statement, agent)
}

// AddMilestone saves a strategic milestone to the vision store.
func AddMilestone(st *store.Store, projectID, statement, agent string) (*store.Fact, error) {
	if projectID == "" {
		projectID = "sila"
	}
	return st.InsertMilestone(projectID, statement, agent)
}

// ListVisionItems retrieves all non-goals, milestones, and tenets for a project.
func ListVisionItems(st *store.Store, projectID string) ([]store.Fact, error) {
	if projectID == "" {
		projectID = "sila"
	}
	return st.VisionFacts(projectID)
}

// hasSemanticConflict checks if proposal introduces concepts forbidden by nonGoal.
func hasSemanticConflict(prop, nonGoal string) bool {
	// Common forbidden patterns
	forbiddenPairs := []struct {
		propKeywords    []string
		nonGoalKeywords []string
	}{
		{[]string{"postgres", "postgresql", "mysql", "cockroach", "cloud db", "mongo"}, []string{"pure-go", "sqlite only", "no cloud db", "no external db", "cloud database", "cloud dependency", "no cloud"}},
		{[]string{"cgo", "c library", "node-gyp", "native c"}, []string{"pure-go", "cgo_enabled=0", "no cgo", "zero cgo"}},
		{[]string{"telemetry", "tracking", "phone home", "analytics"}, []string{"reads content, not telemetry", "no telemetry", "privacy", "local only"}},
		{[]string{"daemon", "background service", "systemd", "server process"}, []string{"standalone binary", "cli tool", "no daemon", "ephemeral"}},
		{[]string{"vector db", "pinecone", "weaviate", "chroma", "qdrant"}, []string{"sqlite fts5", "fts5 only", "no vector db"}},
	}

	for _, pair := range forbiddenPairs {
		hasProp := false
		for _, pk := range pair.propKeywords {
			if strings.Contains(prop, pk) {
				hasProp = true
				break
			}
		}
		if hasProp {
			for _, ngk := range pair.nonGoalKeywords {
				if strings.Contains(nonGoal, ngk) {
					return true
				}
			}
		}
	}

	// Negation-aware concept matching. A non-goal that forbids a concept is
	// violated only when the proposal shares the concept, carries an action
	// that introduces it, and names the same location the non-goal scopes.
	// This catches "raw SQL ... in the checkout handler" without rejecting a
	// compliant "parameterized SQL through the repository layer".
	if sharesForbiddenConcept(prop, nonGoal) {
		return true
	}

	return false
}

var (
	negationWords  = []string{"no", "not", "never", "avoid", "forbid", "without", "zero"}
	violationVerbs = []string{
		"add", "introduce", "implement", "support", "enable", "allow",
		"raw", "inline", "hardcoded", "hardcode", "manual", "direct",
		"bypass", "skip", "disable", "remove", "replace", "unparameterized",
		"concatenate", "concatenation", "adhoc", "ad-hoc",
		"require", "depend", "rely", "force", "mandate",
		"store", "persist", "save", "send", "write", "host", "deploy", "migrate",
	}
	locationWords = []string{
		"handler", "route", "controller", "endpoint", "frontend", "ui",
		"service", "daemon", "binary", "cli", "shell",
	}
)

// sharesForbiddenConcept reports whether a proposal violates a negated
// non-goal by sharing its concept, adding an action that introduces it, and
// naming the same scoped location.
func sharesForbiddenConcept(prop, nonGoal string) bool {
	if !hasNegation(nonGoal) {
		return false
	}
	propTokens := contentTokens(prop)
	goalTokens := contentTokens(nonGoal)
	if len(propTokens) == 0 || len(goalTokens) == 0 {
		return false
	}
	shared := 0
	for tok := range propTokens {
		if goalTokens[tok] {
			shared++
		}
	}
	if shared < 2 {
		return false
	}
	if !containsToken(rawTokens(prop), violationVerbs) {
		return false
	}
	// If the non-goal scopes the ban to a location, the proposal must name the
	// same location. This keeps a migration in the repository layer clear of a
	// ban on raw SQL in route handlers.
	goalScoped, propScoped := false, false
	for _, loc := range locationWords {
		if goalTokens[loc] {
			goalScoped = true
			if propTokens[loc] {
				propScoped = true
			}
		}
	}
	if goalScoped && !propScoped {
		return false
	}
	return true
}

// hasNegation reports whether the text carries an explicit negative marker.
func hasNegation(s string) bool {
	for _, word := range negationWords {
		if strings.Contains(" "+s+" ", " "+word+" ") {
			return true
		}
	}
	return false
}

// rawTokens keeps stop words so an action verb such as "store" still counts
// as a violation signal.
func rawTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, raw := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_'
	}) {
		raw = strings.Trim(raw, "-_")
		if raw == "" {
			continue
		}
		out[singularize(raw)] = true
	}
	return out
}

// contentTokens lowercases, splits, drops stop words, and singularizes words so
// "handlers" matches "handler" and "dependencies" matches "dependency".
func contentTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, raw := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_'
	}) {
		raw = strings.Trim(raw, "-_")
		if len(raw) < 3 || isStopWord(raw) || isNegationWord(raw) {
			continue
		}
		out[singularize(raw)] = true
	}
	return out
}

func isNegationWord(w string) bool {
	for _, n := range negationWords {
		if w == n {
			return true
		}
	}
	return false
}

func singularize(w string) string {
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "sses") && len(w) > 4:
		return w[:len(w)-2]
	case strings.HasSuffix(w, "ches"), strings.HasSuffix(w, "shes"),
		strings.HasSuffix(w, "xes"), strings.HasSuffix(w, "zes"):
		return w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") &&
		!strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is") && len(w) > 3:
		return w[:len(w)-1]
	default:
		return w
	}
}

func containsToken(tokens map[string]bool, words []string) bool {
	for _, w := range words {
		if tokens[singularize(w)] {
			return true
		}
	}
	return false
}

// hasInvariantConflict checks if proposal contradicts an invariant rule.
func hasInvariantConflict(prop, invariant string) bool {
	if strings.Contains(invariant, "pure-go") || strings.Contains(invariant, "cgo_enabled=0") {
		if strings.Contains(prop, "cgo") || strings.Contains(prop, "native c") || strings.Contains(prop, "libsqlite3") {
			return true
		}
	}
	if strings.Contains(invariant, "read-only") {
		if strings.Contains(prop, "write to source db") || strings.Contains(prop, "modify session db") {
			return true
		}
	}
	return false
}

func cleanPrefix(s, prefix string) string {
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, prefix) {
		return strings.TrimSpace(s[len(prefix):])
	}
	return strings.TrimSpace(s)
}

func isStopWord(w string) bool {
	switch w {
	case "about", "above", "after", "again", "against", "all", "and", "any",
		"because", "been", "before", "being", "below", "between", "both", "but",
		"could", "did", "does", "doing", "down", "during", "each", "few", "for",
		"from", "further", "had", "has", "have", "having", "here", "how", "into",
		"itself", "more", "most", "other", "ought", "our", "ours", "ourselves",
		"out", "over", "own", "same", "should", "some", "such", "than", "that",
		"the", "their", "theirs", "them", "themselves", "then", "there", "these",
		"they", "this", "those", "through", "until", "very", "was", "were",
		"what", "when", "where", "which", "while", "who", "whom", "why", "with",
		"would", "your", "yours", "yourself", "yourselves", "project", "store", "build":
		return true
	}
	return false
}
