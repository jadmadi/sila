package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func joinJSON(ss []string) string {
	if ss == nil {
		ss = []string{}
	}
	b, _ := json.Marshal(ss)
	return string(b)
}

func unjoinJSON(s string) []string {
	var out []string
	if s == "" {
		return out
	}
	json.Unmarshal([]byte(s), &out)
	return out
}

// Fact is one durable, attributed claim distilled from messages.
type Fact struct {
	ID            string   `json:"id"`
	ProjectID     string   `json:"project_id"`
	Claim         string   `json:"claim"`
	Kind          string   `json:"kind,omitempty"` // invariant | code_claim | ephemeral
	Confidence    string   `json:"confidence"`     // invariant | ungrounded | grounded | corroborated
	Corroboration string   `json:"corroboration"`  // invariant | ungrounded | grounded | corroborated
	Verified      bool     `json:"verified"`       // deprecated alias for FileRefsExist
	FileRefsExist bool     `json:"file_refs_exist"`
	RepoFiles     []string `json:"repo_files"`
	Tools         []string `json:"tools"`
	SessionRef    string   `json:"session_ref"`
	FirstSeen     string   `json:"first_seen"`
	PromotedAt    string   `json:"promoted_at"`
	RefsCheckedAt string   `json:"refs_checked_at"`
	Distilled     string   `json:"distilled"`
	DistillStatus string   `json:"distill_status"` // pending | distilled | skipped | failed

	// Authority lifecycle: a candidate is an attributed observation (e.g. a
	// directive-looking transcript line) that no consumer may treat as an
	// authorized rule until it is approved.
	ReviewStatus string `json:"review_status"` // candidate | approved
	OriginRole   string `json:"origin_role,omitempty"`
	Quoted       bool   `json:"quoted,omitempty"`
	FactHash     string `json:"fact_hash,omitempty"`
	PrevHash     string `json:"prev_hash,omitempty"`
	CompactedAt  string `json:"compacted_at,omitempty"`
	SupersedesID string `json:"supersedes_id,omitempty"`

	// Access-weighted retention decay telemetry (Migration 25)
	AccessCount    int     `json:"access_count"`
	LastAccessedAt string  `json:"last_accessed_at,omitempty"`
	DistinctTools  int     `json:"distinct_tools"`
	Tier           string  `json:"tier,omitempty"`
	Salience       float64 `json:"salience,omitempty"`
	IsStaleFlag    bool    `json:"is_stale,omitempty"`

	// Provenance metadata (Goal: agent-provenance-metadata-schema)
	ToolName     string `json:"tool_name,omitempty"`     // e.g. "antigravity", "cursor", "claude_code"
	ClientKind   string `json:"client_kind,omitempty"`   // e.g. "ai_agent", "human", "ci_job"
	OriginEntity string `json:"origin_entity,omitempty"` // e.g. "org:waqftech", "pat:personal", "system"

	// Byte-bounded literal recovery (Migration 26)
	ByteStart int64  `json:"byte_start"`
	ByteEnd   int64  `json:"byte_end"`
	SourceRef string `json:"source_ref,omitempty"`
}

// Provenance returns the structured provenance metadata for this fact.
func (f Fact) Provenance() Provenance {
	return Provenance{
		ToolName:     f.ToolName,
		ClientKind:   f.ClientKind,
		OriginEntity: f.OriginEntity,
	}
}

// PackProvenance encodes ToolName, ClientKind, and OriginEntity into f.Tools
// using clean namespaced prefixes ("prov:client=...", "prov:origin=...").
func (f *Fact) PackProvenance() {
	if f.ToolName != "" {
		found := false
		for _, t := range f.Tools {
			if t == f.ToolName {
				found = true
				break
			}
		}
		if !found {
			f.Tools = append([]string{f.ToolName}, f.Tools...)
		}
	}
	if f.ClientKind != "" {
		tag := "prov:client=" + f.ClientKind
		found := false
		for _, t := range f.Tools {
			if t == tag {
				found = true
				break
			}
		}
		if !found {
			f.Tools = append(f.Tools, tag)
		}
	}
	if f.OriginEntity != "" {
		tag := "prov:origin=" + f.OriginEntity
		found := false
		for _, t := range f.Tools {
			if t == tag {
				found = true
				break
			}
		}
		if !found {
			f.Tools = append(f.Tools, tag)
		}
	}
}

// UnpackProvenance extracts ToolName, ClientKind, and OriginEntity from f.Tools.
func (f *Fact) UnpackProvenance() {
	for _, t := range f.Tools {
		if strings.HasPrefix(t, "prov:client=") {
			f.ClientKind = strings.TrimPrefix(t, "prov:client=")
		} else if strings.HasPrefix(t, "prov:origin=") {
			f.OriginEntity = strings.TrimPrefix(t, "prov:origin=")
		} else if f.ToolName == "" && !strings.HasPrefix(t, "prov:") {
			f.ToolName = t
		}
	}
}

// IsCompacted reports whether this fact has undergone A2 extractive compaction.
func (f Fact) IsCompacted() bool {
	return f.CompactedAt != ""
}

// Authority review statuses.
const (
	ReviewCandidate = "candidate"
	ReviewApproved  = "approved"
)

// IsCandidate reports whether this fact is an attributed observation (e.g. a
// directive-looking transcript line) that has not been authorized as a rule.
// Candidates must be excluded from every authority-bearing surface: exported
// hard constraints, prime briefings, and trusted-rule reporting.
func (f Fact) IsCandidate() bool {
	return f.ReviewStatus == ReviewCandidate
}

// IsStale reports whether this fact is an approved, non-invariant claim whose
// referenced files are no longer on disk — the drift the "stale" surfaces
// exist to report.
//
// Candidates are excluded deliberately: an unapproved observation was never
// grounded, so counting it as "referencing missing files" would report review
// status as filesystem drift. Invariants and explicit memos are conceptual and
// were never expected to be file-grounded.
func (f Fact) IsStale() bool {
	if f.IsStaleFlag {
		return true
	}
	if f.IsCandidate() || f.IsInvariant() {
		return false
	}
	if f.SessionRef == "manual_memo" {
		return false
	}
	return !(f.FileRefsExist || f.Verified)
}

// FilterStale returns the facts that are genuinely stale (see IsStale).
func FilterStale(facts []Fact) []Fact {
	var out []Fact
	for _, f := range facts {
		if f.IsStale() {
			out = append(out, f)
		}
	}
	return out
}

// IsInvariant reports whether a claim represents a durable architectural invariant,
// decision, policy, guideline, or constraint whose validity is conceptual rather than
// tied to transient files on disk. Candidates are never invariants: inferred
// directive-looking text stays an attributed observation until approved.
func (f Fact) IsInvariant() bool {
	if f.ReviewStatus == ReviewCandidate {
		return false
	}
	if f.ReviewStatus == "" {
		// Records persisted before the authority lifecycle default to
		// candidate semantics, never to authority.
		return false
	}
	if f.Kind == "invariant" || f.Kind == "nongoal" || f.Kind == "milestone" || f.Corroboration == "invariant" || f.Confidence == "invariant" {
		return true
	}
	if len(f.RepoFiles) == 0 {
		claimLower := strings.ToLower(f.Claim)
		for _, prefix := range []string{
			"decision:", "architecture:", "arch:", "rule:", "policy:",
			"guideline:", "guidance:", "constraint:", "invariant:", "tradeoff:", "memo:", "convention:",
			"non-goal:", "nongoal:", "milestone:", "vision:",
		} {
			if strings.HasPrefix(claimLower, prefix) || strings.Contains(claimLower, "\n"+prefix) {
				return true
			}
		}
	}
	return false
}

// IsNonGoal reports whether this fact represents an explicit non-goal / negative constraint.
func (f Fact) IsNonGoal() bool {
	if f.Kind == "nongoal" {
		return true
	}
	claimLower := strings.ToLower(f.Claim)
	return strings.HasPrefix(claimLower, "non-goal:") || strings.HasPrefix(claimLower, "nongoal:") || strings.Contains(claimLower, "\nnon-goal:")
}

// IsMilestone reports whether this fact represents a strategic vision milestone.
func (f Fact) IsMilestone() bool {
	if f.Kind == "milestone" {
		return true
	}
	claimLower := strings.ToLower(f.Claim)
	return strings.HasPrefix(claimLower, "milestone:") || strings.HasPrefix(claimLower, "horizon:") || strings.Contains(claimLower, "\nmilestone:")
}

// IsFact reports whether a claim qualifies as a true distilled fact:
// it must be an approved architectural invariant OR (corroborated, distilled, and
// file-grounded on disk). Candidates never qualify.
func (f Fact) IsFact() bool {
	if f.ReviewStatus == ReviewCandidate || f.ReviewStatus == "" {
		return false
	}
	if f.IsInvariant() {
		return true
	}
	return (f.Corroboration == "corroborated" || f.Confidence == "corroborated" || f.Confidence == "high") &&
		(f.DistillStatus == "distilled" && f.Distilled != "") &&
		(f.FileRefsExist || f.Verified)
}

// IsManualMemo reports whether a person wrote this record explicitly through
// `sila memo` rather than sila extracting it from a session. Such a record is
// authorized knowledge by construction: it needs no corroboration and was never
// expected to be file-grounded (its referenced file may not exist on disk, or
// it may be conceptual), so it reaches the prime tier even when IsFact() is
// false. Callers that distinguish authority from evidence use this alongside
// IsFact/IsInvariant.
func (f Fact) IsManualMemo() bool {
	return f.SessionRef == "manual_memo" && f.ReviewStatus != ReviewCandidate
}

// IsExplicitMemo reports whether a directive was written deliberately by a
// person or MCP client, regardless of the write path. CLI memos carry
// session_ref=manual_memo; MCP memos carry a session ref and leave kind empty,
// while distilled assistant claims carry kind=code_claim.
func (f Fact) IsExplicitMemo() bool {
	if f.ReviewStatus != ReviewApproved {
		return false
	}
	if f.Kind == "memo" || f.SessionRef == "manual_memo" {
		return true
	}
	return f.Kind == "" && f.OriginRole == "user"
}

// IsAuthorityBearing reports whether this record is explicit memory rather than
// derived state. Pruning/distillation may remove derived claims, but never an
// explicit memo, lesson, case, vision record, or approved invariant.
func (f Fact) IsAuthorityBearing() bool {
	if f.ReviewStatus != ReviewApproved {
		return false
	}
	switch f.Kind {
	case "memo", "lesson", "case", "nongoal", "milestone", "invariant":
		return true
	}
	if f.IsExplicitMemo() || f.IsLesson() || f.IsCase() || f.IsInvariant() {
		return true
	}
	return false
}

// FormatLesson renders the canonical single-line lesson record shared by the
// CLI and the MCP save_lesson tool.
func FormatLesson(symptom, cause, fix, tags string) string {
	parts := []string{"Lesson: " + strings.TrimSpace(symptom)}
	if c := strings.TrimSpace(cause); c != "" {
		parts = append(parts, "cause: "+c)
	}
	if f := strings.TrimSpace(fix); f != "" {
		parts = append(parts, "fix: "+f)
	}
	if t := strings.TrimSpace(tags); t != "" {
		parts = append(parts, "tags: "+t)
	}
	return strings.Join(parts, " | ")
}

// ParseLesson splits the canonical lesson line into its labeled fields.
// Format: "Lesson: <symptom> | cause: <cause> | fix: <fix> | tags: <tags>".
func ParseLesson(content string) (symptom, cause, fix, tags string) {
	body := strings.TrimSpace(content)
	body = strings.TrimPrefix(body, "Lesson: ")
	for seg := range strings.SplitSeq(body, " | ") {
		seg = strings.TrimSpace(seg)
		lower := strings.ToLower(seg)
		switch {
		case strings.HasPrefix(lower, "cause:"):
			cause = strings.TrimSpace(seg[len("cause:"):])
		case strings.HasPrefix(lower, "fix:"):
			fix = strings.TrimSpace(seg[len("fix:"):])
		case strings.HasPrefix(lower, "tags:"):
			tags = strings.TrimSpace(seg[len("tags:"):])
		default:
			if symptom == "" {
				symptom = seg
			}
		}
	}
	return symptom, cause, fix, tags
}

// IsLesson reports whether this fact is a deliberately recorded solution lesson.
func (f Fact) IsLesson() bool {
	if f.Kind == "lesson" {
		return true
	}
	claimLower := strings.ToLower(f.Claim)
	return strings.HasPrefix(claimLower, "lesson:") || strings.Contains(claimLower, "\nlesson:")
}

// AgentCase represents an architectural procedural case capturing intent, approach, and key invariants.
type AgentCase struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	Intent       string    `json:"intent"`        // What architectural or design goal was addressed
	Approach     string    `json:"approach"`      // The chosen pattern, library, or structural approach
	KeyInvariant string    `json:"key_invariant"` // Non-obvious constraint, race hazard, or trade-off
	Tags         string    `json:"tags,omitempty"`
	FileRefs     []string  `json:"file_refs"` // Grounded repository files
	Agent        string    `json:"agent"`
	CreatedAt    time.Time `json:"created_at"`
}

// FormatCase renders the canonical single-line architectural case record shared by the
// CLI and the MCP save_case tool.
// Format: "Case: <intent> | approach: <approach> | invariant: <invariant> | tags: <tags>".
func FormatCase(intent, approach, invariant, tags string) string {
	parts := []string{"Case: " + strings.TrimSpace(intent)}
	if a := strings.TrimSpace(approach); a != "" {
		parts = append(parts, "approach: "+a)
	}
	if inv := strings.TrimSpace(invariant); inv != "" {
		parts = append(parts, "invariant: "+inv)
	}
	if t := strings.TrimSpace(tags); t != "" {
		parts = append(parts, "tags: "+t)
	}
	return strings.Join(parts, " | ")
}

// ParseCase splits the canonical case line into its labeled fields.
// Format: "Case: <intent> | approach: <approach> | invariant: <invariant> | tags: <tags>".
func ParseCase(content string) (intent, approach, invariant, tags string) {
	body := strings.TrimSpace(content)
	body = strings.TrimPrefix(body, "Case: ")
	for seg := range strings.SplitSeq(body, " | ") {
		seg = strings.TrimSpace(seg)
		lower := strings.ToLower(seg)
		switch {
		case strings.HasPrefix(lower, "approach:"):
			approach = strings.TrimSpace(seg[len("approach:"):])
		case strings.HasPrefix(lower, "invariant:"):
			invariant = strings.TrimSpace(seg[len("invariant:"):])
		case strings.HasPrefix(lower, "tags:"):
			tags = strings.TrimSpace(seg[len("tags:"):])
		default:
			if intent == "" {
				intent = seg
			}
		}
	}
	return intent, approach, invariant, tags
}

// IsCase reports whether this fact is an architectural procedural case.
func (f Fact) IsCase() bool {
	if f.Kind == "case" {
		return true
	}
	claimLower := strings.ToLower(f.Claim)
	return strings.HasPrefix(claimLower, "case:") || strings.Contains(claimLower, "\ncase:")
}

const selectFactsFields = `id, project_id, claim, confidence, verified, file_refs_exist, repo_files, tools, session_ref, first_seen, promoted_at, refs_checked_at, distilled, distill_status, kind, review_status, origin_role, quoted, fact_hash, prev_hash, compacted_at, supersedes_id, access_count, last_accessed_at, distinct_tools, tier, salience, is_stale, byte_start, byte_end, source_ref`
const selectFactsPrefixed = `f.id, f.project_id, f.claim, f.confidence, f.verified, f.file_refs_exist, f.repo_files, f.tools, f.session_ref, f.first_seen, f.promoted_at, f.refs_checked_at, f.distilled, f.distill_status, f.kind, f.review_status, f.origin_role, f.quoted, f.fact_hash, f.prev_hash, f.compacted_at, f.supersedes_id, f.access_count, f.last_accessed_at, f.distinct_tools, f.tier, f.salience, f.is_stale, f.byte_start, f.byte_end, f.source_ref`

// ErrFactConflict indicates an attempt to modify the immutable hashed claim or kind of an existing hashed fact.
var ErrFactConflict = errors.New("fact conflict")

var factMu sync.Mutex

func scanFact(rows *sql.Rows) (Fact, error) {
	var f Fact
	var verified, fileRefsExist, quoted, isStale int
	var rf, tools string
	if err := rows.Scan(
		&f.ID, &f.ProjectID, &f.Claim, &f.Confidence, &verified, &fileRefsExist, &rf, &tools, &f.SessionRef,
		&f.FirstSeen, &f.PromotedAt, &f.RefsCheckedAt, &f.Distilled, &f.DistillStatus, &f.Kind, &f.ReviewStatus,
		&f.OriginRole, &quoted, &f.FactHash, &f.PrevHash, &f.CompactedAt, &f.SupersedesID, &f.AccessCount,
		&f.LastAccessedAt, &f.DistinctTools, &f.Tier, &f.Salience, &isStale,
		&f.ByteStart, &f.ByteEnd, &f.SourceRef,
	); err != nil {
		return f, err
	}
	f.FileRefsExist = (fileRefsExist != 0)
	f.Verified = (verified != 0)
	f.Corroboration = f.Confidence
	f.RepoFiles = unjoinJSON(rf)
	f.Tools = unjoinJSON(tools)
	f.Quoted = (quoted != 0)
	f.IsStaleFlag = (isStale != 0)
	if f.DistinctTools < 1 {
		f.DistinctTools = len(f.Tools)
		if f.DistinctTools < 1 {
			f.DistinctTools = 1
		}
	}
	if f.Salience <= 0 {
		f.Salience = 1.0
	}
	if f.Tier == "" {
		if f.IsInvariant() || f.IsManualMemo() {
			f.Tier = string(TierSemantic)
		} else {
			f.Tier = string(TierEpisodic)
		}
	}
	f.UnpackProvenance()
	return f, nil
}

func (s *Store) facts(query string, args ...any) ([]Fact, error) {
	return s.QueryRows(context.Background(), query, scanFact, args...)
}
