package dream

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/store"
)

// MemoryContractVersion is the versioned session-start memory contract.
const MemoryContractVersion = hookconfig.MemoryContractVersion

// SessionStartBrief is the bounded, deterministic briefing emitted by
// `sila hook session-start`.
type SessionStartBrief struct {
	Schema                string   `json:"schema"`
	Project               string   `json:"project"`
	ProjectDir            string   `json:"project_dir,omitempty"`
	Branch                string   `json:"branch,omitempty"`
	Head                  string   `json:"head,omitempty"`
	Detached              bool     `json:"detached,omitempty"`
	ContextSource         string   `json:"context_source,omitempty"`
	ActiveGoal            string   `json:"active_goal,omitempty"`
	SessionID             string   `json:"session_id,omitempty"`
	HandoffSummary        string   `json:"handoff_summary,omitempty"`
	OpenItems             []string `json:"open_items,omitempty"`
	Memory                []string `json:"memory"`
	NextGoal              string   `json:"next_goal,omitempty"`
	MemoryUnavailable     bool     `json:"memory_unavailable,omitempty"`
	MemoryContractVersion string   `json:"memory_contract_version"`
	Bytes                 int      `json:"bytes"`
	Markdown              string   `json:"markdown,omitempty"`
}

// DefaultSessionBriefBudget is the default byte budget for the session-start brief.
const DefaultSessionBriefBudget = 2048

// MinSessionBriefBudget and MaxSessionBriefBudget clamp --budget.
const (
	MinSessionBriefBudget = 512
	MaxSessionBriefBudget = 8192
)

// BuildSessionStartBrief builds a bounded session-start brief for projectSel
// (or the workspace-resolved project when empty).
func BuildSessionStartBrief(st *store.Store, projectSel, projectDir, sessionID string, budget int) (*SessionStartBrief, error) {
	if projectDir == "" {
		cwd, err := os.Getwd()
		if err == nil {
			projectDir = cwd
		}
	}
	if budget <= 0 {
		budget = DefaultSessionBriefBudget
	}
	if budget < MinSessionBriefBudget {
		budget = MinSessionBriefBudget
	}
	if budget > MaxSessionBriefBudget {
		budget = MaxSessionBriefBudget
	}

	brief := &SessionStartBrief{
		Schema:                MemoryContractVersion,
		MemoryContractVersion: MemoryContractVersion,
		SessionID:             sessionID,
		ProjectDir:            projectDir,
		ActiveGoal:            strings.TrimSpace(os.Getenv("SILA_ACTIVE_GOAL")),
		Memory: []string{
			`1. Prime: sila prime (or MCP prime_context)`,
			`2. Before web/grep: sila recall "<symptom>" or sila search "<query>" --omni`,
			`3. Decision made: sila memo "<directive>" (or MCP save_memo)`,
			`4. Fix proven: sila lesson "<symptom>" --cause=... --fix=... (or MCP save_lesson)`,
			`5. Before closure: sila facts verify; sila gate`,
			`6. Session end: sila handoff "<summary>" --open="..." (or MCP save_handoff)`,
		},
		NextGoal: "next: sila goals ready",
	}

	// Resolve the active project without letting an empty store fail the brief.
	if st != nil {
		if identity, err := store.ResolveWorkspaceIdentity(projectDir); err == nil && identity.ActiveDir != "" {
			projectDir = identity.ActiveDir
			brief.ProjectDir = projectDir
		}
		p, err := st.ResolveScope(projectSel, projectDir)
		if err == nil && p.ID != "" {
			brief.Project = p.ID
			if p.Path != "" {
				brief.ProjectDir = p.Path
				projectDir = p.Path
			}
		} else {
			brief.Project = store.ProjectID(projectDir)
			brief.MemoryUnavailable = true
		}
	} else {
		brief.Project = store.ProjectID(projectDir)
		brief.MemoryUnavailable = true
	}
	if brief.Project == "" {
		brief.Project = "default"
	}

	gitCtx := DetectGitContext(projectDir)
	brief.Branch = gitCtx.Branch
	brief.Head = gitCtx.Commit
	brief.ContextSource = "resolved"
	if st != nil && sessionID != "" {
		if sess, err := st.GetAgentSession(sessionID); err == nil && sess != nil {
			if sess.Branch != "" || sess.Head != "" {
				brief.Branch = sess.Branch
				brief.Head = sess.Head
				brief.Detached = sess.Detached
				brief.ContextSource = "session"
			} else {
				brief.ContextSource = "unknown"
			}
		}
	}

	if st != nil {
		if handoff, err := st.LatestHandoff(brief.Project); err == nil && handoff != nil {
			brief.HandoffSummary = handoff.Summary
			for _, item := range handoff.OpenItems {
				if !strings.HasPrefix(item, "- [x]") && !strings.HasPrefix(item, "[x]") {
					brief.OpenItems = append(brief.OpenItems, item)
				}
			}
		}
	}

	brief.Markdown = renderSessionBrief(brief, budget)
	brief.Bytes = len(brief.Markdown)
	return brief, nil
}

func renderSessionBrief(brief *SessionStartBrief, budget int) string {
	header := fmt.Sprintf("Sila session · %s", brief.Project)
	if brief.Branch != "" {
		header += " · " + brief.Branch
	}
	if brief.Head != "" {
		header += " · " + brief.Head
	}
	if brief.ActiveGoal != "" {
		header += " · goal " + brief.ActiveGoal
	}

	// Mandatory section: header and the memory contract.
	var mandatory []string
	mandatory = append(mandatory, header)
	if brief.MemoryUnavailable {
		mandatory = append(mandatory, "memory: unavailable (run sila heal or sila install)")
	}
	mandatory = append(mandatory, "Memory required ("+MemoryContractVersion+"):")
	mandatory = append(mandatory, brief.Memory...)

	// Optional sections in drop order: next pointer, open items, handoff summary.
	optional := []string{}
	if brief.NextGoal != "" {
		optional = append(optional, brief.NextGoal)
	}
	if len(brief.OpenItems) > 0 {
		optional = append(optional, "Open items:")
		for _, item := range brief.OpenItems {
			optional = append(optional, "  - "+item)
		}
	}
	if brief.HandoffSummary != "" {
		optional = append(optional, "Last handoff: "+brief.HandoffSummary)
	}

	var b strings.Builder
	used := 0
	appendLine := func(line string) bool {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return true
		}
		need := len(line) + 1
		if used+need > budget {
			return false
		}
		b.WriteString(line)
		b.WriteByte('\n')
		used += need
		return true
	}
	for _, line := range mandatory {
		if !appendLine(line) {
			// The mandatory contract itself does not fit. Truncate at a rune
			// boundary; never cut a line mid-rune.
			if b.Len() < budget {
				remaining := budget - b.Len() - 1
				if remaining > 0 {
					text := line
					if len(text) > remaining {
						text = text[:remaining]
						for len(text) > 0 && !utf8.ValidString(text) {
							text = text[:len(text)-1]
						}
					}
					b.WriteString(text)
					b.WriteByte('\n')
				}
			}
			return b.String()
		}
	}
	for _, line := range optional {
		if !appendLine(line) {
			break
		}
	}
	return b.String()
}

// HandoffAge returns a human-readable age for a handoff timestamp.
func HandoffAge(createdAt string) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return "unknown"
	}
	age := time.Since(t)
	switch {
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}
}
