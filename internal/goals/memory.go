package goals

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// MemoryWrites is the canonical sila/memory-writes/v1 evidence block.
type MemoryWrites struct {
	Schema   string              `json:"schema"`
	GoalSlug string              `json:"goal_slug"`
	Project  string              `json:"project,omitempty"`
	Prime    MemoryPrime         `json:"prime"`
	Recall   []MemoryRecallQuery `json:"recall"`
	Memos    []MemoryRecord      `json:"memos"`
	Lessons  []MemoryRecord      `json:"lessons"`
	Cases    []MemoryRecord      `json:"cases"`
	Facts    MemoryFactsVerify   `json:"facts_verify"`
	Handoff  MemoryHandoff       `json:"handoff"`
	Waivers  []MemoryWaiver      `json:"waivers,omitempty"`
}

// MemoryPrime records the session-start priming event.
type MemoryPrime struct {
	Ran        bool   `json:"ran"`
	Command    string `json:"command,omitempty"`
	RecordedAt string `json:"recorded_at,omitempty"`
}

// MemoryRecallQuery records one recall query and its match count.
type MemoryRecallQuery struct {
	Query      string `json:"query"`
	Matches    int    `json:"matches"`
	RecordedAt string `json:"recorded_at,omitempty"`
}

// MemoryRecord identifies one memo, lesson, or case fact.
type MemoryRecord struct {
	ID      string `json:"id"`
	Text    string `json:"text,omitempty"`
	Symptom string `json:"symptom,omitempty"`
	Intent  string `json:"intent,omitempty"`
}

// MemoryFactsVerify records the facts verification outcome.
type MemoryFactsVerify struct {
	ExitCode   int `json:"exit_code"`
	Violations int `json:"violations,omitempty"`
}

// MemoryHandoff identifies the session handoff or an explicit mid-session deferral to session end.
type MemoryHandoff struct {
	ID       string `json:"id"`
	Summary  string `json:"summary,omitempty"`
	Deferred bool   `json:"deferred,omitempty"`
}

func isDeferredHandoff(h MemoryHandoff) bool {
	id := strings.ToLower(strings.TrimSpace(h.ID))
	return h.Deferred || id == "session-end" || id == "deferred"
}

// MemoryWaiver excuses one missing verb with a reason.
type MemoryWaiver struct {
	Verb   string `json:"verb"`
	Reason string `json:"reason"`
}

var (
	memoryProtocolRE = regexp.MustCompile(`(?im)^[-*]\s*(?:\*\*)?Memory Protocol(?:\*\*)?:\s*([a-zA-Z-]+)`)
	memoryBlockRE    = regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")
)

// ParseMemoryProtocol reads the Memory Protocol marker from goal.md.
func ParseMemoryProtocol(content string) string {
	if m := memoryProtocolRE.FindStringSubmatch(content); len(m) > 1 {
		return strings.ToLower(strings.TrimSpace(m[1]))
	}
	return "legacy"
}

// ParseMemoryWrites extracts the canonical sila/memory-writes/v1 block from a
// markdown evidence document. A missing block returns (nil, nil); a malformed
// or conflicting block returns diagnostics.
func ParseMemoryWrites(evidence string) (*MemoryWrites, []string) {
	matches := memoryBlockRE.FindAllStringSubmatch(evidence, -1)
	var found *MemoryWrites
	var diagnostics []string
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(match[1]))
		dec.DisallowUnknownFields()
		var mw MemoryWrites
		if err := dec.Decode(&mw); err != nil {
			diagnostics = append(diagnostics, "MEMORY_SCHEMA_INVALID: "+err.Error())
			continue
		}
		if mw.Schema != "sila/memory-writes/v1" {
			continue
		}
		if found != nil {
			diagnostics = append(diagnostics, "MEMORY_BLOCK_DUPLICATE: more than one sila/memory-writes/v1 block")
			continue
		}
		found = &mw
	}
	if found == nil && len(diagnostics) == 0 {
		return nil, nil
	}
	return found, diagnostics
}

// ValidateMemoryWrites checks a parsed block against the goal and, when
// available, the live knowledge store. It returns diagnostic strings.
func ValidateMemoryWrites(mw *MemoryWrites, goalSlug string, st *store.Store) []string {
	if mw == nil {
		return []string{memoryBlockMissingMsg}
	}
	var diags []string
	if mw.Schema != "sila/memory-writes/v1" {
		diags = append(diags, "MEMORY_SCHEMA_INVALID: schema must be sila/memory-writes/v1")
	}
	if mw.GoalSlug != goalSlug {
		diags = append(diags, fmt.Sprintf("MEMORY_GOAL_MISMATCH: block goal_slug %q does not match %q", mw.GoalSlug, goalSlug))
	}
	allowed := map[string]bool{}
	for _, w := range mw.Waivers {
		if strings.TrimSpace(w.Reason) == "" {
			diags = append(diags, "MEMORY_WAIVER_REASON_REQUIRED: every waiver must carry a non-empty reason")
			continue
		}
		allowed[strings.ToLower(strings.TrimSpace(w.Verb))] = true
	}
	if !mw.Prime.Ran && !allowed["prime"] {
		diags = append(diags, "MEMORY_PRIME_MISSING: prime.ran must be true or a prime waiver is required")
	}
	if len(mw.Recall) == 0 && !allowed["recall"] {
		diags = append(diags, "MEMORY_RECALL_MISSING: at least one recall query is required or a recall waiver is required")
	}
	if len(mw.Memos) == 0 && len(mw.Lessons) == 0 && len(mw.Cases) == 0 && !allowed["memo"] && !allowed["lesson"] && !allowed["case"] {
		diags = append(diags, "MEMORY_WRITE_MISSING: at least one memo, lesson, or case is required or a write waiver is required")
	}
	if mw.Facts.ExitCode != 0 && !allowed["facts"] {
		diags = append(diags, "MEMORY_FACTS_VERIFY_FAILED: facts_verify.exit_code must be 0 or a facts waiver is required")
	}
	if strings.TrimSpace(mw.Handoff.ID) == "" && !mw.Handoff.Deferred && !allowed["handoff"] {
		diags = append(diags, "MEMORY_HANDOFF_MISSING: handoff.id is required (or set handoff.deferred=true / handoff.id=\"session-end\" for mid-session goals, or a handoff waiver)")
	}

	if st != nil {
		for _, record := range append(append(append([]MemoryRecord{}, mw.Memos...), mw.Lessons...), mw.Cases...) {
			if strings.TrimSpace(record.ID) == "" {
				diags = append(diags, "MEMORY_FACT_UNKNOWN: empty fact ID")
				continue
			}
			fact, err := st.FactByID(record.ID)
			if err != nil || fact == nil {
				diags = append(diags, fmt.Sprintf("MEMORY_FACT_UNKNOWN: %s does not resolve in the knowledge store", record.ID))
			} else if mw.Project != "" && fact.ProjectID != "" && fact.ProjectID != mw.Project {
				diags = append(diags, fmt.Sprintf("MEMORY_FACT_UNKNOWN: %s belongs to project %q, not %q", record.ID, fact.ProjectID, mw.Project))
			}
		}
		if strings.TrimSpace(mw.Handoff.ID) != "" && !isDeferredHandoff(mw.Handoff) {
			if handoff, err := st.GetHandoff(context.Background(), mw.Handoff.ID); err != nil || handoff == nil {
				diags = append(diags, fmt.Sprintf("MEMORY_HANDOFF_MISSING: %s does not resolve in the knowledge store", mw.Handoff.ID))
			}
		}
	}
	return diags
}

// LoadMemoryStore opens the default knowledge store read-only for certification.
func LoadMemoryStore() (*store.Store, error) {
	return store.OpenRO(store.DefaultDBPath())
}

// ValidateMemoryEvidence parses the evidence block and validates it against the
// live store. Required goals fail closed when the store cannot be opened.
func ValidateMemoryEvidence(goalSlug, evidence string) []string {
	mw, diags := ParseMemoryWrites(evidence)
	if len(diags) > 0 {
		return diags
	}
	if mw == nil {
		return []string{memoryBlockMissingMsg}
	}
	st, err := LoadMemoryStore()
	if err != nil {
		return []string{"MEMORY_STORE_UNAVAILABLE: " + err.Error()}
	}
	defer st.Close()
	return ValidateMemoryWrites(mw, goalSlug, st)
}

// memoryBlockMissingMsg is a copy-pasteable remediation for a missing evidence block.
const memoryBlockMissingMsg = `MEMORY_BLOCK_MISSING: add a ## Memory Writes section to the evidence file with a fenced json block: {"schema":"sila/memory-writes/v1","goal_slug":"<slug>","prime":{"ran":true},"recall":[{"query":"<q>","matches":1}],"memos":[{"id":"<fact id>"}],"lessons":[],"cases":[],"facts_verify":{"exit_code":0},"handoff":{"id":"<handoff id>"}} (or pass --no-memory --reason="...")`
