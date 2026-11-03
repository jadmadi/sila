package learn

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

// GuidebookSection represents a categorized domain section of operational learnings.
type GuidebookSection struct {
	Domain      string                `json:"domain"`
	DisplayName string                `json:"display_name"`
	Description string                `json:"description"`
	Count       int                   `json:"count"`
	Learnings   []OperationalLearning `json:"learnings"`
}

// Guidebook represents the complete compiled developer operational guidebook.
type Guidebook struct {
	Title           string             `json:"title"`
	CompiledAt      time.Time          `json:"compiled_at"`
	TotalLearnings  int                `json:"total_learnings"`
	DomainBreakdown map[string]int     `json:"domain_breakdown"`
	Sections        []GuidebookSection `json:"sections"`
}

// CompileGuidebook aggregates and structures operational learnings into a cohesive guidebook.
func CompileGuidebook(learnings []OperationalLearning, domainFilter string) *Guidebook {
	domainMap := make(map[string][]OperationalLearning)
	breakdown := make(map[string]int)

	for _, l := range learnings {
		if domainFilter != "" && !strings.EqualFold(l.Domain, domainFilter) {
			continue
		}
		// Legacy rows predate kind tracking; normalize the display copy only.
		l.Kind = l.EffectiveKind()
		domainMap[l.Domain] = append(domainMap[l.Domain], l)
		breakdown[l.Domain]++
	}

	var sections []GuidebookSection
	// Canonical domain order
	domains := AllDomains
	if domainFilter != "" {
		domains = []string{strings.ToLower(domainFilter)}
	}

	for _, d := range domains {
		items := domainMap[d]
		if len(items) == 0 {
			continue
		}
		sections = append(sections, GuidebookSection{
			Domain:      d,
			DisplayName: DomainDisplayName(d),
			Description: DomainDescription(d),
			Count:       len(items),
			Learnings:   items,
		})
	}

	total := 0
	for _, s := range sections {
		total += s.Count
	}

	return &Guidebook{
		Title:           "Sila Developer Operational Guidebook",
		CompiledAt:      time.Now(),
		TotalLearnings:  total,
		DomainBreakdown: breakdown,
		Sections:        sections,
	}
}

// ToMarkdown serializes the Guidebook into GitHub-Flavored Markdown.
func (g *Guidebook) ToMarkdown() string {
	var b strings.Builder

	b.WriteString("# Sila Developer Operational Guidebook\n\n")
	b.WriteString("> Living, cross-project engineering discipline and operational invariants distilled across AI CLI sessions and registered repositories.\n\n")
	b.WriteString(fmt.Sprintf("**Compiled At**: `%s` · **Total Learnings**: `%d`\n\n", g.CompiledAt.Format(time.RFC3339), g.TotalLearnings))

	// Table of Contents
	b.WriteString("## Table of Contents\n\n")
	for i, s := range g.Sections {
		slug := strings.ReplaceAll(strings.ToLower(s.DisplayName), " ", "-")
		slug = strings.ReplaceAll(slug, "(", "")
		slug = strings.ReplaceAll(slug, ")", "")
		slug = strings.ReplaceAll(slug, "&", "")
		slug = strings.ReplaceAll(slug, ",", "")
		b.WriteString(fmt.Sprintf("%d. [%s](#%s) (%d learnings)\n", i+1, s.DisplayName, slug, s.Count))
	}
	b.WriteString("\n---\n\n")

	// Domain Sections
	for _, s := range g.Sections {
		b.WriteString(fmt.Sprintf("## %s\n\n", s.DisplayName))
		b.WriteString(fmt.Sprintf("*%s*\n\n", s.Description))

		for idx, l := range s.Learnings {
			b.WriteString(fmt.Sprintf("### %d. %s\n\n", idx+1, l.Title))
			if l.TechStack != "" {
				b.WriteString(fmt.Sprintf("- **Tech Stack**: `%s`\n", l.TechStack))
			}
			if l.ProjectID != "" {
				b.WriteString(fmt.Sprintf("- **Origin Project**: `%s`\n", l.ProjectID))
			}
			if len(l.Tags) > 0 {
				b.WriteString(fmt.Sprintf("- **Tags**: `%s`\n", strings.Join(l.Tags, ", ")))
			}
			b.WriteString("\n")

			switch l.EffectiveKind() {
			case KindCase:
				if l.Intent != "" {
					b.WriteString(fmt.Sprintf("**Intent**:\n%s\n\n", l.Intent))
				}
				if l.Approach != "" {
					b.WriteString(fmt.Sprintf("**Approach**:\n%s\n\n", l.Approach))
				}
				if l.Invariant != "" {
					b.WriteString(fmt.Sprintf("> [!IMPORTANT] **Invariant**\n> %s\n\n", strings.ReplaceAll(l.Invariant, "\n", "\n> ")))
				}
			case KindMemo:
				if directive := l.DirectiveText(); directive != "" {
					b.WriteString(fmt.Sprintf("**Directive**:\n%s\n\n", directive))
				}
			case KindDecision:
				if directive := l.DirectiveText(); directive != "" {
					b.WriteString(fmt.Sprintf("**Decision**:\n%s\n\n", directive))
				}
			default:
				if l.Symptom != "" {
					b.WriteString(fmt.Sprintf("> [!WARNING] **Symptom & Failure Mode**\n> %s\n\n", strings.ReplaceAll(l.Symptom, "\n", "\n> ")))
				}
				if l.RootCause != "" {
					b.WriteString(fmt.Sprintf("**Root Cause**:\n%s\n\n", l.RootCause))
				}
				if l.PivotalFix != "" {
					b.WriteString(fmt.Sprintf("**Pivotal Fix**:\n%s\n\n", l.PivotalFix))
				}
				if l.GuardrailRule != "" {
					b.WriteString(fmt.Sprintf("> [!IMPORTANT] **Guardrail Rule & Architectural Invariant**\n> %s\n\n", strings.ReplaceAll(l.GuardrailRule, "\n", "\n> ")))
				}
			}
			b.WriteString("---\n\n")
		}
	}

	return b.String()
}

// ToHTML serializes the Guidebook into a standalone, accessible, zero-dependency HTML document.
func (g *Guidebook) ToHTML() string {
	var b strings.Builder

	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Sila Developer Operational Guidebook</title>
<style>
  :root {
    --bg: #0b0f17;
    --card-bg: #141c2b;
    --card-border: #233147;
    --text: #e2e8f0;
    --text-muted: #94a3b8;
    --brand: #ff6a00;
    --teal: #4fd6be;
    --rose: #fb7185;
    --amber: #fbbf24;
    --periwinkle: #818cf8;
    --font: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    --mono: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, Courier, monospace;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background-color: var(--bg);
    color: var(--text);
    font-family: var(--font);
    line-height: 1.6;
    padding: 2rem 1rem;
  }
  .container { max-width: 1100px; margin: 0 auto; }
  header {
    border-bottom: 1px solid var(--card-border);
    padding-bottom: 1.5rem;
    margin-bottom: 2rem;
  }
  h1 { font-size: 2.2rem; color: #fff; margin-bottom: 0.5rem; }
  .subtitle { color: var(--text-muted); font-size: 1.1rem; margin-bottom: 1rem; }
  .stats-bar {
    display: flex;
    gap: 1rem;
    flex-wrap: wrap;
    margin-top: 1rem;
  }
  .stat-badge {
    background: #1e293b;
    border: 1px solid var(--card-border);
    padding: 0.4rem 0.8rem;
    border-radius: 6px;
    font-size: 0.9rem;
    font-family: var(--mono);
  }
  .search-box {
    margin: 1.5rem 0;
    width: 100%;
    padding: 0.8rem 1rem;
    background: var(--card-bg);
    border: 1px solid var(--card-border);
    border-radius: 8px;
    color: #fff;
    font-size: 1rem;
  }
  .search-box:focus { outline: 2px solid var(--brand); }
  .domain-section { margin-bottom: 3rem; }
  .domain-header {
    display: flex;
    align-items: baseline;
    gap: 0.8rem;
    border-bottom: 2px solid var(--brand);
    padding-bottom: 0.4rem;
    margin-bottom: 0.8rem;
  }
  .domain-title { font-size: 1.6rem; color: #fff; }
  .domain-count {
    background: var(--brand);
    color: #fff;
    font-size: 0.8rem;
    font-weight: bold;
    padding: 0.2rem 0.6rem;
    border-radius: 12px;
  }
  .domain-desc { color: var(--text-muted); font-size: 0.95rem; margin-bottom: 1.2rem; }
  .cards-grid {
    display: grid;
    grid-template-columns: 1fr;
    gap: 1.5rem;
  }
  .card {
    background: var(--card-bg);
    border: 1px solid var(--card-border);
    border-radius: 10px;
    padding: 1.5rem;
    box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.3);
  }
  .card-title { font-size: 1.25rem; color: #fff; margin-bottom: 0.5rem; }
  .card-meta {
    display: flex;
    gap: 0.8rem;
    font-size: 0.85rem;
    color: var(--text-muted);
    font-family: var(--mono);
    margin-bottom: 1rem;
    flex-wrap: wrap;
  }
  .tag { background: #0f172a; padding: 0.1rem 0.4rem; border-radius: 4px; border: 1px solid #334155; }
  .box-warning {
    background: rgba(251, 113, 133, 0.1);
    border-left: 4px solid var(--rose);
    padding: 0.8rem 1rem;
    border-radius: 4px;
    margin-bottom: 1rem;
    font-size: 0.95rem;
  }
  .box-fix {
    background: rgba(79, 214, 190, 0.1);
    border-left: 4px solid var(--teal);
    padding: 0.8rem 1rem;
    border-radius: 4px;
    margin-bottom: 1rem;
    font-size: 0.95rem;
  }
  .box-rule {
    background: rgba(255, 106, 0, 0.12);
    border-left: 4px solid var(--brand);
    padding: 0.8rem 1rem;
    border-radius: 4px;
    font-size: 0.95rem;
    font-weight: 500;
  }
  .section-label { font-weight: bold; margin-bottom: 0.2rem; }
  footer {
    text-align: center;
    color: var(--text-muted);
    font-size: 0.85rem;
    margin-top: 4rem;
    border-top: 1px solid var(--card-border);
    padding-top: 1.5rem;
  }
</style>
</head>
<body>
<div class="container">
<header>
  <h1>Sila Developer Operational Guidebook</h1>
  <div class="subtitle">Living, cross-project engineering discipline and operational invariants distilled across AI CLI sessions.</div>
  <div class="stats-bar">
    <div class="stat-badge">Total Learnings: ` + fmt.Sprintf("%d", g.TotalLearnings) + `</div>
    <div class="stat-badge">Domains: ` + fmt.Sprintf("%d", len(g.Sections)) + `</div>
    <div class="stat-badge">Compiled: ` + html.EscapeString(g.CompiledAt.Format("2006-01-02 15:04:05 UTC")) + `</div>
  </div>
  <input type="text" id="guideSearch" class="search-box" placeholder="Filter operational learnings by keyword, symptom, stack, or rule..." oninput="filterCards()">
</header>
<main id="guideContent">
`)

	for _, s := range g.Sections {
		b.WriteString(fmt.Sprintf(`
<section class="domain-section" data-domain="%s">
  <div class="domain-header">
    <h2 class="domain-title">%s</h2>
    <span class="domain-count">%d</span>
  </div>
  <p class="domain-desc">%s</p>
  <div class="cards-grid">
`, html.EscapeString(s.Domain), html.EscapeString(s.DisplayName), s.Count, html.EscapeString(s.Description)))

		for _, l := range s.Learnings {
			tagSpans := ""
			for _, t := range l.Tags {
				tagSpans += fmt.Sprintf(`<span class="tag">%s</span> `, html.EscapeString(t))
			}

			var cardBody strings.Builder
			switch l.EffectiveKind() {
			case KindCase:
				if l.Intent != "" {
					cardBody.WriteString(fmt.Sprintf(`
      <div class="box-warning">
        <div class="section-label" style="color: var(--rose);">Intent</div>
        <div>%s</div>
      </div>`, html.EscapeString(l.Intent)))
				}
				if l.Approach != "" {
					cardBody.WriteString(fmt.Sprintf(`
      <div class="box-fix">
        <div class="section-label" style="color: var(--teal);">Approach</div>
        <div>%s</div>
      </div>`, html.EscapeString(l.Approach)))
				}
				if l.Invariant != "" {
					cardBody.WriteString(fmt.Sprintf(`
      <div class="box-rule">
        <div class="section-label" style="color: var(--brand);">Invariant</div>
        <div>%s</div>
      </div>`, html.EscapeString(l.Invariant)))
				}
			case KindMemo, KindDecision:
				label := "Directive"
				if l.EffectiveKind() == KindDecision {
					label = "Decision"
				}
				if directive := l.DirectiveText(); directive != "" {
					cardBody.WriteString(fmt.Sprintf(`
      <div class="box-rule">
        <div class="section-label" style="color: var(--brand);">%s</div>
        <div>%s</div>
      </div>`, label, html.EscapeString(directive)))
				}
			default:
				if l.Symptom != "" {
					cardBody.WriteString(fmt.Sprintf(`
      <div class="box-warning">
        <div class="section-label" style="color: var(--rose);">Symptom &amp; Failure Mode</div>
        <div>%s</div>
      </div>`, html.EscapeString(l.Symptom)))
				}
				if l.RootCause != "" {
					cardBody.WriteString(fmt.Sprintf(`
      <div style="margin-bottom: 1rem;">
        <div class="section-label" style="color: var(--periwinkle);">Root Cause</div>
        <div>%s</div>
      </div>`, html.EscapeString(l.RootCause)))
				}
				if l.PivotalFix != "" {
					cardBody.WriteString(fmt.Sprintf(`
      <div class="box-fix">
        <div class="section-label" style="color: var(--teal);">Pivotal Fix</div>
        <div>%s</div>
      </div>`, html.EscapeString(l.PivotalFix)))
				}
				if l.GuardrailRule != "" {
					cardBody.WriteString(fmt.Sprintf(`
      <div class="box-rule">
        <div class="section-label" style="color: var(--brand);">Guardrail Rule &amp; Invariant</div>
        <div>%s</div>
      </div>`, html.EscapeString(l.GuardrailRule)))
				}
			}

			searchText := strings.ToLower(strings.Join([]string{
				l.Title, l.Symptom, l.RootCause, l.PivotalFix,
				l.Intent, l.Approach, l.Invariant, l.Directive,
				l.GuardrailRule, string(l.EffectiveKind()), strings.Join(l.Tags, " "),
			}, " "))

			b.WriteString(fmt.Sprintf(`
    <div class="card" data-search="%s">
      <h3 class="card-title">%s</h3>
      <div class="card-meta">
        <span>Stack: %s</span>
        <span>Project: %s</span>
        <span>%s</span>
      </div>%s
    </div>
`,
				html.EscapeString(searchText),
				html.EscapeString(l.Title),
				html.EscapeString(l.TechStack),
				html.EscapeString(l.ProjectID),
				tagSpans,
				cardBody.String(),
			))
		}

		b.WriteString(`
  </div>
</section>
`)
	}

	b.WriteString(`
</main>
<footer>
  Generated by Sila (صِلَة) · Digital Data Sovereignty & Continuity Engine
</footer>
</div>
<script>
function filterCards() {
  const q = document.getElementById('guideSearch').value.toLowerCase();
  const cards = document.querySelectorAll('.card');
  cards.forEach(card => {
    const text = card.getAttribute('data-search') || '';
    if (!q || text.includes(q)) {
      card.style.display = '';
    } else {
      card.style.display = 'none';
    }
  });
  // Hide empty domain sections
  document.querySelectorAll('.domain-section').forEach(sec => {
    const visibleCards = sec.querySelectorAll('.card:not([style*="display: none"])');
    sec.style.display = visibleCards.length > 0 ? '' : 'none';
  });
}
</script>
</body>
</html>
`)

	return b.String()
}

// ToJSON serializes the Guidebook into formatted JSON.
func (g *Guidebook) ToJSON() ([]byte, error) {
	return json.MarshalIndent(g, "", "  ")
}

// ToTOON serializes the Guidebook into compact Token-Oriented Object Notation.
func (g *Guidebook) ToTOON() string {
	var b strings.Builder

	summaryFields := [][2]string{
		{"title", g.Title},
		{"total_learnings", fmt.Sprintf("%d", g.TotalLearnings)},
		{"total_sections", fmt.Sprintf("%d", len(g.Sections))},
		{"compiled_at", g.CompiledAt.Format(time.RFC3339)},
	}
	b.WriteString(render.FormatTOONObject("guidebook_summary", summaryFields))

	var lessonRows, caseRows, directiveRows [][]string
	for _, s := range g.Sections {
		for _, l := range s.Learnings {
			switch l.EffectiveKind() {
			case KindCase:
				caseRows = append(caseRows, []string{
					string(KindCase), l.Domain, l.Title, l.TechStack,
					l.Intent, l.Approach, l.Invariant,
				})
			case KindMemo, KindDecision:
				directiveRows = append(directiveRows, []string{
					string(l.EffectiveKind()), l.Domain, l.Title, l.TechStack, l.DirectiveText(),
				})
			default:
				lessonRows = append(lessonRows, []string{
					string(KindLesson), l.Domain, l.Title, l.TechStack,
					l.Symptom, l.RootCause, l.PivotalFix, l.GuardrailRule,
				})
			}
		}
	}

	if len(lessonRows) > 0 {
		b.WriteString(render.FormatTOONTable("learnings",
			[]string{"kind", "domain", "title", "stack", "symptom", "cause", "fix", "rule"}, lessonRows))
	}
	if len(caseRows) > 0 {
		b.WriteString(render.FormatTOONTable("cases",
			[]string{"kind", "domain", "title", "stack", "intent", "approach", "invariant"}, caseRows))
	}
	if len(directiveRows) > 0 {
		b.WriteString(render.FormatTOONTable("directives",
			[]string{"kind", "domain", "title", "stack", "directive"}, directiveRows))
	}

	return b.String()
}

// ToSummary serializes the Guidebook into a human-readable high-level breakdown.
func (g *Guidebook) ToSummary() string {
	var b strings.Builder
	kindCounts := make(map[LearningKind]int)
	for _, s := range g.Sections {
		for _, l := range s.Learnings {
			kindCounts[l.EffectiveKind()]++
		}
	}
	b.WriteString(fmt.Sprintf("Sila Operational Guidebook: %d learnings across %d domains (compiled at %s)\n",
		g.TotalLearnings, len(g.Sections), g.CompiledAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("Kinds: %d lessons, %d cases, %d memos, %d decisions\n",
		kindCounts[KindLesson], kindCounts[KindCase], kindCounts[KindMemo], kindCounts[KindDecision]))
	for _, s := range g.Sections {
		b.WriteString(fmt.Sprintf("- %s (%d): %s\n", s.DisplayName, s.Count, s.Description))
	}
	return b.String()
}

// FilterLearnings filters a slice of operational learnings by matching a search query
// against title, lesson fields, case fields, directives, guardrail rule, stack, and tags.
func FilterLearnings(learnings []OperationalLearning, query string) []OperationalLearning {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return learnings
	}
	var res []OperationalLearning
	for _, l := range learnings {
		if strings.Contains(strings.ToLower(l.Title), q) ||
			strings.Contains(strings.ToLower(l.Symptom), q) ||
			strings.Contains(strings.ToLower(l.RootCause), q) ||
			strings.Contains(strings.ToLower(l.PivotalFix), q) ||
			strings.Contains(strings.ToLower(l.Intent), q) ||
			strings.Contains(strings.ToLower(l.Approach), q) ||
			strings.Contains(strings.ToLower(l.Invariant), q) ||
			strings.Contains(strings.ToLower(l.Directive), q) ||
			strings.Contains(strings.ToLower(l.GuardrailRule), q) ||
			strings.Contains(strings.ToLower(l.TechStack), q) {
			res = append(res, l)
			continue
		}
		matchedTag := false
		for _, t := range l.Tags {
			if strings.Contains(strings.ToLower(t), q) {
				matchedTag = true
				break
			}
		}
		if matchedTag {
			res = append(res, l)
		}
	}
	return res
}
