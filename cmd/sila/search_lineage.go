package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runSearchLineage(ui render.UI, st *store.Store, project string, refsExistOnly, jsonOut, toonOut, verbose bool, top int, terms []string) {
	if top <= 0 {
		top = 50
	}
	if project != "" {
		p, err := st.ResolveProject(project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila search lineage: %v\n", err)
			os.Exit(1)
		}
		project = p.ID
	}
	filterProject := func(assertions []store.Assertion) []store.Assertion {
		if project == "" {
			return assertions
		}
		filtered := make([]store.Assertion, 0, len(assertions))
		for _, a := range assertions {
			if a.ProjectID == project {
				filtered = append(filtered, a)
			}
		}
		return filtered
	}

	// 1. Direct listing of assertions if no terms provided
	if len(terms) == 0 {
		assertions, err := st.AssertionsByProject(project, refsExistOnly, top)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila search assertions: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(assertions)
			return
		}
		if toonOut {
			var rows [][]string
			for _, a := range assertions {
				verStr := "false"
				if a.Verified {
					verStr = "true"
				}
				rows = append(rows, []string{
					a.ID,
					a.ProjectID,
					a.StatementID,
					a.StatementType,
					a.EvidenceType,
					a.EvidenceID,
					a.EvidenceRef,
					a.Confidence,
					verStr,
					a.VerificationError,
				})
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("assertions", []string{"id", "project_id", "statement_id", "statement_type", "evidence_type", "evidence_id", "evidence_ref", "confidence", "verified", "verification_error"}, rows))
			return
		}
		if len(assertions) == 0 {
			fmt.Fprintf(os.Stdout, "assertions: 0 assertions found %s\n", scopeLabel(project))
			return
		}
		headers := []string{"id", "statement", "type", "evidence", "ref", "verified", "reason"}
		var rows [][]string
		for _, a := range assertions {
			ver := ui.Red("unverified")
			if a.Verified {
				ver = ui.Green("verified")
			}
			rows = append(rows, []string{
				a.ID,
				a.StatementID,
				a.EvidenceType,
				trunc(a.EvidenceID, 40),
				a.EvidenceRef,
				ver,
				a.VerificationError,
			})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l", "l", "l", "l"}))
		return
	}

	// 2. Exact fact ID lookup or direct assertion lookup by statement/evidence ID
	if len(terms) == 1 {
		target := terms[0]
		fact, err := st.FactByID(target)
		if err == nil && fact != nil && (project == "" || fact.ProjectID == project) {
			assertions, err := st.AssertionsForStatement(fact.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila search lineage: %v\n", err)
				os.Exit(1)
			}
			lineage := store.FactWithLineage{
				Fact:       *fact,
				Assertions: assertions,
			}
			if jsonOut {
				emitJSON(lineage)
				return
			}
			if toonOut {
				fields := [][2]string{
					{"id", fact.ID},
					{"project_id", fact.ProjectID},
					{"claim", strings.ReplaceAll(fact.Claim, "\n", "\\n")},
					{"kind", fact.Kind},
					{"confidence", fact.Confidence},
					{"assertions_count", fmt.Sprintf("%d", len(assertions))},
				}
				fmt.Fprint(os.Stdout, render.FormatTOONObject("fact_lineage", fields))
				return
			}
			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("Fact: [%s] (%s · %s)", fact.ID, fact.ProjectID, fact.Kind))))
			fmt.Fprintf(os.Stdout, "  Claim: %s\n", fact.Claim)
			fmt.Fprintf(os.Stdout, "  Stored confidence: %s (source availability does not prove claim truth)\n", fact.Confidence)
			if len(assertions) == 0 {
				fmt.Fprintln(os.Stdout, ui.Dim("  No assertion evidence records linked."))
			} else {
				fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("  Lineage Evidence (%d records):", len(assertions))))
				for _, a := range assertions {
					ver := ui.Red("[ ]")
					if a.Verified {
						ver = ui.Green("[" + render.GlyphSuccess + "]")
					}
					refStr := ""
					if a.EvidenceRef != "" {
						refStr = fmt.Sprintf(" (%s)", a.EvidenceRef)
					}
					if a.VerificationError != "" {
						refStr += " [" + a.VerificationError + "]"
					}
					fmt.Fprintf(os.Stdout, "    %s %s: %s%s\n", ver, ui.Bold(a.EvidenceType), a.EvidenceID, refStr)
				}
			}
			return
		}

		// Check if target is a statement_id or evidence_id in assertions table
		assertions, err := st.AssertionsForStatement(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila search assertions: %v\n", err)
			os.Exit(1)
		}
		assertions = filterProject(assertions)
		if len(assertions) == 0 {
			assertions, err = st.AssertionsForEvidence("", target)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila search assertions: %v\n", err)
				os.Exit(1)
			}
			assertions = filterProject(assertions)
		}
		if len(assertions) > 0 {
			if jsonOut {
				emitJSON(assertions)
				return
			}
			if toonOut {
				var rows [][]string
				for _, a := range assertions {
					verStr := "false"
					if a.Verified {
						verStr = "true"
					}
					rows = append(rows, []string{
						a.ID,
						a.StatementID,
						a.EvidenceType,
						a.EvidenceID,
						verStr,
						a.VerificationError,
					})
				}
				fmt.Fprint(os.Stdout, render.FormatTOONTable("assertions", []string{"id", "statement_id", "evidence_type", "evidence_id", "verified", "verification_error"}, rows))
				return
			}
			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("Assertions linked to: %s (%d records)", target, len(assertions)))))
			for _, a := range assertions {
				ver := ui.Red("[ ]")
				if a.Verified {
					ver = ui.Green("[" + render.GlyphSuccess + "]")
				}
				refStr := ""
				if a.EvidenceRef != "" {
					refStr = fmt.Sprintf(" (%s)", a.EvidenceRef)
				}
				if a.VerificationError != "" {
					refStr += " [" + a.VerificationError + "]"
				}
				fmt.Fprintf(os.Stdout, "  %s [%s -> %s] %s%s\n", ver, a.StatementID, a.EvidenceType, a.EvidenceID, refStr)
			}
			return
		}
	}

	// 3. Search query with assertion provenance
	query := strings.Join(terms, " ")
	results, err := st.SearchFactsWithLineage(query, project, refsExistOnly, top)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila search lineage: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(results)
		return
	}
	if toonOut {
		var rows [][]string
		for _, r := range results {
			rows = append(rows, []string{
				r.Fact.ID,
				strings.ReplaceAll(r.Fact.Claim, "\n", "\\n"),
				fmt.Sprintf("%d", len(r.Assertions)),
			})
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("facts_with_lineage", []string{"id", "claim", "assertions"}, rows))
		return
	}
	if len(results) == 0 {
		fmt.Fprintf(os.Stdout, "lineage: 0 facts found matching %q %s\n", query, scopeLabel(project))
		return
	}
	for _, r := range results {
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("[%s] %s", r.Fact.ID, trunc(r.Fact.Claim, 70)))))
		if len(r.Assertions) > 0 {
			for _, a := range r.Assertions {
				ver := ui.Red("[ ]")
				if a.Verified {
					ver = ui.Green("[" + render.GlyphSuccess + "]")
				}
				fmt.Fprintf(os.Stdout, "   %s %s: %s (%s) %s\n", ver, a.EvidenceType, a.EvidenceID, a.EvidenceRef, a.VerificationError)
			}
		} else {
			fmt.Fprintln(os.Stdout, ui.Dim("   No linked assertions"))
		}
	}
}

// openMCPStore opens a writable store because several public MCP tools persist
// handoffs, memos, documents, repair results, and feedback.
