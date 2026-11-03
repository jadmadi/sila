package dream

import (
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// CategorizedOpinions holds opinion facts grouped by domain.
type CategorizedOpinions struct {
	Architecture []store.Fact `json:"architecture"`
	Agents       []store.Fact `json:"agents"`
	Verification []store.Fact `json:"verification"`
	Taste        []store.Fact `json:"taste"`
	General      []store.Fact `json:"general"`
}

// CompileOpinions builds a structured, living OPINIONS.md from recorded memos,
// architectural invariants, verified facts, and project feedback.
func CompileOpinions(st *store.Store, projectID string) (string, error) {
	if projectID == "" {
		projectID = "global"
	}

	facts, err := st.FactsByProject(projectID)
	if err != nil {
		facts = nil
	}

	grouped := categorizeFacts(facts)

	var b strings.Builder
	now := time.Now().UTC().Format("2006-01-02 15:04")

	b.WriteString(fmt.Sprintf("# OPINIONS.md — %s\n\n", projectID))
	b.WriteString(fmt.Sprintf("> **Living Technical Stances & Engineering Taste**  \n"))
	b.WriteString(fmt.Sprintf("> Auto-compiled by `sila opinions` on %s UTC. Grounded in recorded memos and verified facts.\n\n", now))
	b.WriteString("---\n\n")

	// 1. Architecture & Boundaries
	b.WriteString("## 1. Architectural Invariants & System Boundaries\n\n")
	if len(grouped.Architecture) == 0 {
		b.WriteString("_No explicit architectural invariants recorded yet. Run `sila opinions add --category=arch \"<stance>\"` to register one._\n\n")
	} else {
		for _, f := range grouped.Architecture {
			b.WriteString(formatOpinionItem(f))
		}
		b.WriteString("\n")
	}

	// 2. Agents & Orchestration
	b.WriteString("## 2. AI Agents & Orchestration Stances\n\n")
	if len(grouped.Agents) == 0 {
		b.WriteString("_No explicit agent orchestration stances recorded yet. Run `sila opinions add --category=agents \"<stance>\"` to register one._\n\n")
	} else {
		for _, f := range grouped.Agents {
			b.WriteString(formatOpinionItem(f))
		}
		b.WriteString("\n")
	}

	// 3. Verification & Testing
	b.WriteString("## 3. Verification, Testing & Grounding Heuristics\n\n")
	if len(grouped.Verification) == 0 {
		b.WriteString("_No explicit verification heuristics recorded yet. Run `sila opinions add --category=verification \"<stance>\"` to register one._\n\n")
	} else {
		for _, f := range grouped.Verification {
			b.WriteString(formatOpinionItem(f))
		}
		b.WriteString("\n")
	}

	// 4. Taste & Tradeoffs
	b.WriteString("## 4. Technical Taste, Tooling & Tradeoffs\n\n")
	if len(grouped.Taste) == 0 && len(grouped.General) == 0 {
		b.WriteString("_No explicit technical taste entries recorded yet. Run `sila opinions add \"<stance>\"` to register one._\n\n")
	} else {
		for _, f := range grouped.Taste {
			b.WriteString(formatOpinionItem(f))
		}
		for _, f := range grouped.General {
			b.WriteString(formatOpinionItem(f))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n")
	b.WriteString("### How this document is maintained\n")
	b.WriteString("This file is a living product of `sila`. As decisions are made via `sila memo`, `sila feedback`, or `sila opinions add`, ")
	b.WriteString("they are reconciled into the SQLite store and compiled here to prime incoming agents with consistent engineering taste.\n")

	return b.String(), nil
}

// AddOpinion inserts a manual opinion into the store under kind="invariant".
func AddOpinion(st *store.Store, projectID, category, stance, agent, cwd string) (*store.Fact, error) {
	if stance == "" {
		return nil, fmt.Errorf("opinion stance cannot be empty")
	}
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		category = "taste"
	}
	prefixed := fmt.Sprintf("[%s] %s", category, stance)
	return st.InsertMemo(projectID, prefixed, agent, cwd, nil)
}

// ListOpinions retrieves all opinion/memo facts for a project.
func ListOpinions(st *store.Store, projectID string) ([]store.Fact, error) {
	facts, err := st.FactsByProject(projectID)
	if err != nil {
		return nil, err
	}
	var res []store.Fact
	for _, f := range facts {
		if f.SessionRef == "manual_memo" || f.IsInvariant() {
			res = append(res, f)
		}
	}
	return res, nil
}

func categorizeFacts(facts []store.Fact) CategorizedOpinions {
	var g CategorizedOpinions
	seen := make(map[string]bool)

	for _, f := range facts {
		if f.SessionRef != "manual_memo" && !f.IsInvariant() {
			continue
		}
		claim := strings.TrimSpace(f.Claim)
		if claim == "" || seen[strings.ToLower(claim)] {
			continue
		}
		seen[strings.ToLower(claim)] = true

		lower := strings.ToLower(claim)
		switch {
		case strings.HasPrefix(lower, "[arch]") || strings.Contains(lower, "architecture") || strings.Contains(lower, "invariant") || strings.Contains(lower, "sqlite") || strings.Contains(lower, "cgo") || strings.Contains(lower, "boundary"):
			g.Architecture = append(g.Architecture, f)
		case strings.HasPrefix(lower, "[agents]") || strings.Contains(lower, "agent") || strings.Contains(lower, "harness") || strings.Contains(lower, "orchestrat") || strings.Contains(lower, "claude") || strings.Contains(lower, "codex") || strings.Contains(lower, "devin"):
			g.Agents = append(g.Agents, f)
		case strings.HasPrefix(lower, "[verification]") || strings.Contains(lower, "test") || strings.Contains(lower, "grounding") || strings.Contains(lower, "verify") || strings.Contains(lower, "doctor") || strings.Contains(lower, "gate"):
			g.Verification = append(g.Verification, f)
		case strings.HasPrefix(lower, "[taste]") || strings.Contains(lower, "taste") || strings.Contains(lower, "tradeoff") || strings.Contains(lower, "cli") || strings.Contains(lower, "toon") || strings.Contains(lower, "format"):
			g.Taste = append(g.Taste, f)
		default:
			g.General = append(g.General, f)
		}
	}
	return g
}

func formatOpinionItem(f store.Fact) string {
	claim := strings.TrimSpace(f.Claim)
	// Strip category tags like [arch] if present
	if strings.HasPrefix(claim, "[") {
		if idx := strings.Index(claim, "]"); idx != -1 && idx < 20 {
			claim = strings.TrimSpace(claim[idx+1:])
		}
	}
	fileNote := ""
	if len(f.RepoFiles) > 0 {
		fileNote = fmt.Sprintf(" `(%s)`", strings.Join(f.RepoFiles, ", "))
	}
	return fmt.Sprintf("- **%s**%s\n", claim, fileNote)
}
