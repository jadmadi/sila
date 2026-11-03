package dream

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/store"
)

const (
	// DefaultReanchorBudget is the mid-session re-anchor byte cap.
	DefaultReanchorBudget = 512
	// MinReanchorBudget and MaxReanchorBudget clamp --budget for the anchor.
	MinReanchorBudget = 256
	MaxReanchorBudget = 2048
	// ReanchorSchema is the JSON contract for the post-compaction event.
	ReanchorSchema = "sila/post-compaction/v1"
)

// ReanchorBrief is the structured post-compaction memory anchor.
type ReanchorBrief struct {
	Schema                string `json:"schema"`
	Project               string `json:"project"`
	Branch                string `json:"branch"`
	Head                  string `json:"head"`
	ActiveGoal            string `json:"active_goal,omitempty"`
	SessionID             string `json:"session_id,omitempty"`
	HandoffID             string `json:"handoff_id,omitempty"`
	NextCommand           string `json:"next_command,omitempty"`
	MemoryContractVersion string `json:"memory_contract_version"`
	MemoryUnavailable     bool   `json:"memory_unavailable,omitempty"`
	Bytes                 int    `json:"bytes"`
}

// BuildReanchorBrief renders the <=maxBytes anchor. A nil store degrades to
// the static contract instead of failing the hook.
func BuildReanchorBrief(st *store.Store, projectID, projectDir, sessionID string, maxBytes int) (*ReanchorBrief, string, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultReanchorBudget
	}
	if maxBytes < MinReanchorBudget {
		maxBytes = MinReanchorBudget
	}
	if maxBytes > MaxReanchorBudget {
		maxBytes = MaxReanchorBudget
	}
	if projectDir == "" {
		projectDir, _ = os.Getwd()
	}
	if projectID == "" && projectDir != "" {
		projectID = store.ProjectID(projectDir)
	}
	if projectID == "" {
		projectID = "unregistered project"
	}

	branch, head := reanchorGitState(projectDir)
	brief := &ReanchorBrief{
		Schema:                ReanchorSchema,
		Project:               projectID,
		Branch:                branch,
		Head:                  head,
		SessionID:             sessionID,
		NextCommand:           "sila goals ready",
		MemoryContractVersion: hookconfig.MemoryContractVersion,
	}
	if goal := strings.TrimSpace(os.Getenv("SILA_ACTIVE_GOAL")); goal != "" {
		brief.ActiveGoal = goal
	}
	if st == nil {
		brief.MemoryUnavailable = true
	} else {
		if brief.ActiveGoal == "" && sessionID != "" {
			if sess, err := st.GetAgentSession(sessionID); err == nil && sess != nil {
				brief.ActiveGoal = strings.TrimSpace(sess.ActiveGoal)
			}
		}
		if h, err := st.LatestHandoff(projectID); err == nil && h != nil {
			if created, parseErr := time.Parse(time.RFC3339, h.CreatedAt); parseErr == nil {
				if time.Since(created) <= 7*24*time.Hour {
					brief.HandoffID = h.ID
				}
			}
		}
	}

	text := renderReanchor(brief, maxBytes)
	brief.Bytes = len(text)
	return brief, text, nil
}

func reanchorGitState(dir string) (string, string) {
	if dir == "" {
		return "none", "none"
	}
	branch := "none"
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			branch = v
		}
	}
	head := "none"
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--short=7", "HEAD").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			head = v
		}
	}
	return branch, head
}

func renderReanchor(brief *ReanchorBrief, maxBytes int) string {
	includeNext, includeHandoff, includeBefore, includeGit, includeGoalSession := true, true, true, true, true
	for {
		text := reanchorText(brief, includeNext, includeHandoff, includeBefore, includeGit, includeGoalSession)
		if len(text) <= maxBytes {
			return text
		}
		switch {
		case includeNext:
			includeNext = false
		case includeHandoff:
			includeHandoff = false
		case includeBefore:
			includeBefore = false
		case includeGit:
			includeGit = false
		case includeGoalSession:
			includeGoalSession = false
		default:
			return truncateReanchor(text, maxBytes)
		}
	}
}

func reanchorText(brief *ReanchorBrief, next, handoff, before, git, goalSession bool) string {
	var b strings.Builder
	b.WriteString("Sila re-anchor · ")
	b.WriteString(brief.Project)
	if git {
		b.WriteString(" · ")
		b.WriteString(brief.Branch)
		b.WriteString(" · ")
		b.WriteString(brief.Head)
	}
	b.WriteString("\n")

	goal := brief.ActiveGoal
	session := brief.SessionID
	if !goalSession {
		goal, session = "none", "none"
	}
	if goal == "" {
		goal = "none"
	}
	if session == "" {
		session = "none"
	}
	b.WriteString("goal: " + goal + " · session: " + session)
	if handoff && brief.HandoffID != "" {
		b.WriteString(" · handoff: " + brief.HandoffID)
	}
	b.WriteString("\n")

	b.WriteString("verbs: " + strings.Join(agents.MemoryVerbNames, " ") + "\n")
	b.WriteString("contract: " + brief.MemoryContractVersion + "\n")
	if before {
		b.WriteString("before grep/web: sila recall \"<symptom>\" or sila search \"<query>\" --omni\n")
	}
	if next {
		nextCommand := brief.NextCommand
		if nextCommand == "" {
			nextCommand = "sila goals ready"
		}
		b.WriteString("next: " + nextCommand + "\n")
	}
	return b.String()
}

func truncateReanchor(text string, maxBytes int) string {
	if maxBytes <= 1 {
		return "\n"
	}
	if len(text) > maxBytes-1 {
		text = text[:maxBytes-1]
	}
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return strings.TrimRight(text, "\n") + "\n"
}

// ReanchorMarkerPath returns the per-session dedupe marker path.
func ReanchorMarkerPath(projectDir, sessionID string) string {
	if projectDir == "" {
		return ""
	}
	name := sanitizeReanchorSession(sessionID)
	return filepath.Join(projectDir, ".sila", "state", "reanchor", name+".json")
}

func sanitizeReanchorSession(sessionID string) string {
	name := strings.TrimSpace(sessionID)
	if name == "" {
		return "unbound"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "unbound"
	}
	return b.String()
}

// ReanchorMarker is the dedupe state written under .sila/state/reanchor/.
type ReanchorMarker struct {
	Schema    string `json:"schema"`
	LastKey   string `json:"last_key"`
	EmittedAt string `json:"emitted_at"`
	Count     int    `json:"count"`
	LastText  string `json:"last_text,omitempty"`
}

// ReanchorKey is sha256(session + NUL + head + NUL + event + NUL + sequence).
func ReanchorKey(sessionID, head, event, sequence string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s", sessionID, head, event, sequence)))
	return fmt.Sprintf("%x", sum)
}
