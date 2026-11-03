package goals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// Goal represents a single structured goal package with dependency & sequence awareness.
type Goal struct {
	Slug               string         `json:"slug"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	Status             string         `json:"status"` // "implemented", "claimed", "pending", "draft"
	Independent        bool           `json:"independent"`
	DependsOn          []string       `json:"depends_on,omitempty"`
	DependencyWarnings []string       `json:"dependency_warnings,omitempty"`
	Sequence           int            `json:"sequence,omitempty"`
	FilesToTouch       []string       `json:"files_to_touch,omitempty"` // Declared target files/directories for collision detection
	FileConflicts      []FileConflict `json:"file_conflicts,omitempty"` // Active collisions with other claimed goals
	Blocked            bool           `json:"blocked"`
	BlockedReason      string         `json:"blocked_reason,omitempty"`
	UnmetDeps          []string       `json:"unmet_dependencies,omitempty"`
	RootBlockers       []string       `json:"root_blockers,omitempty"`
	DownstreamCount    int            `json:"downstream_count,omitempty"`
	HasGoalMd          bool           `json:"has_goal_md"`
	HasPlanMd          bool           `json:"has_plan_md"`
	HasFactsMd         bool           `json:"has_facts_md"`
	MissingFiles       []string       `json:"missing_files,omitempty"`
	Commit             string         `json:"commit,omitempty"`
	Task               string         `json:"task,omitempty"`
	// SkippedCandidates lists commits that mention this goal but are
	// documentation-only (docs/test/chore(goals) subjects), so a pending goal
	// can explain why it did not close.
	SkippedCandidates []string `json:"skipped_candidates,omitempty"`
	// NearMisses lists commits that mention this goal's slug in prose but lack a trailer or path.
	NearMisses         []string             `json:"near_misses,omitempty"`
	TasksTotal         int                  `json:"tasks_total,omitempty"`       // Total task checkboxes in plan.md / goal.md
	TasksMet           int                  `json:"tasks_met,omitempty"`         // Completed task checkboxes [x]
	AcceptanceStatus   string               `json:"acceptance_status,omitempty"` // not_run | incomplete | failed | stale | accepted
	AcceptedTasks      int                  `json:"accepted_tasks,omitempty"`
	RequiredCases      int                  `json:"required_cases,omitempty"`
	PassedCases        int                  `json:"passed_cases,omitempty"`
	LaunchCmd          string               `json:"launch_cmd"`
	Claim              *GoalClaim           `json:"claim,omitempty"`
	Reopened           bool                 `json:"reopened,omitempty"`
	ReopenReason       string               `json:"reopen_reason,omitempty"`
	Reopen             *ReopenRecord        `json:"reopen,omitempty"`
	Shape              string               `json:"shape,omitempty"` // "ship" (default) or "scout" (non-mutating investigation)
	Tier               string               `json:"tier,omitempty"`  // "immediate", "roadmap" (default), "nice-to-have", "wont-fix"
	Superseded         bool                 `json:"superseded,omitempty"`
	SupersededBy       string               `json:"superseded_by,omitempty"`
	SupersededReason   string               `json:"superseded_reason,omitempty"`
	Supersede          *SupersededRecord    `json:"supersede,omitempty"`
	Certified          bool                 `json:"certified,omitempty"`
	Certification      *CertificationRecord `json:"certification,omitempty"`
	VerificationStatus string               `json:"verification_status,omitempty"`
	VerificationLabel  string               `json:"verification_label,omitempty"`
	PendingReason      string               `json:"pending_reason,omitempty"`
	PreFlight          *PreFlightReport     `json:"preflight,omitempty"`
	State              GoalState            `json:"state,omitempty"`       // 8-state isnad lifecycle state
	StateLabel         string               `json:"state_label,omitempty"` // human-readable epistemic stage
	Isnad              *IsnadHeader         `json:"isnad,omitempty"`       // 4-stage Chain of Custody
	ReopenEpoch        int                  `json:"reopen_epoch,omitempty"`
	Personas           []string             `json:"personas,omitempty"`
	MemoryProtocol     string               `json:"memory_protocol,omitempty"`
	MemoryStatus       string               `json:"memory_status,omitempty"`
	Memory             *CertificationMemory `json:"memory,omitempty"`
}

// AuditResult holds the complete status report of goals in a repository.
type AuditResult struct {
	Project               string         `json:"project,omitempty"`
	ProjectDir            string         `json:"project_dir,omitempty"`
	OldestReadyAge        string         `json:"oldest_ready_age,omitempty"`
	Total                 int            `json:"total"`
	ImplementedCount      int            `json:"implemented_count"`
	VerifiedCount         int            `json:"verified_count"`
	UnverifiedCount       int            `json:"unverified_count"`
	StaleCount            int            `json:"stale_count,omitempty"`
	SupersededCount       int            `json:"superseded_count,omitempty"`
	ReopenedCount         int            `json:"reopened_count,omitempty"`
	TaintedCount          int            `json:"tainted_count,omitempty"`
	ClaimedCount          int            `json:"claimed_count"`
	PendingCount          int            `json:"pending_count"`
	ContractStatus        string         `json:"contract_status,omitempty"`
	ContractReason        string         `json:"contract_reason,omitempty"`
	ReadyCount            int            `json:"ready_count"`             // Unclaimed and unblocked (ready to execute now)
	ReadyIndependentCount int            `json:"ready_independent_count"` // Unclaimed, unblocked, and independent ⚡
	ReadyUnblockedCount   int            `json:"ready_unblocked_count"`   // Unclaimed, unblocked dependent goals with prerequisites satisfied 🔗
	BlockedCount          int            `json:"blocked_count"`           // Pending but blocked by dependencies
	ContestedCount        int            `json:"contested_count"`         // Pending goals contested by active file collisions
	IndependentCount      int            `json:"independent_count"`       // Completely independent / parallelizable
	DraftCount            int            `json:"draft_count"`
	ImmediateCount        int            `json:"immediate_count,omitempty"`    // Tier 1: immediate blockers and DX priorities
	RoadmapCount          int            `json:"roadmap_count,omitempty"`      // Tier 2: scheduled architectural roadmap milestones
	NiceToHaveCount       int            `json:"nice_to_have_count,omitempty"` // Tier 3: optional enhancements and polish
	WontFixCount          int            `json:"wont_fix_count,omitempty"`     // Tier 4: evaluated and deferred / set aside
	TotalTasks            int            `json:"total_tasks,omitempty"`        // Total task checkboxes across all goals
	CompletedTasks        int            `json:"completed_tasks,omitempty"`    // Completed task checkboxes across all goals
	Cycles                [][]string     `json:"cycles,omitempty"`             // Detected circular dependency chains
	FileConflicts         []FileConflict `json:"file_conflicts,omitempty"`
	Goals                 []Goal         `json:"goals"`
}

var (
	frontmatterRE         = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n?`)
	titleRE               = regexp.MustCompile(`(?m)^#+[ \t]*(?:Goal:[ \t]*)?([^\r\n]+)$`)
	descHeaderRE          = regexp.MustCompile(`(?im)^##+[ \t]*(?:Goal\s*Description|Description|Overview|Summary)[ \t]*\r?\n+([^#\n][^#]+)`)
	firstPgRE             = regexp.MustCompile(`(?s)^([^#\n][^#]+)`)
	slugPathRE            = regexp.MustCompile(`(?i)(?:^|[/\\])([a-z0-9-]+)[/\\]goal\.md`)
	goalHdrRE             = regexp.MustCompile(`(?i)Goal:\s*([a-z0-9-]+)`)
	bracketGoalRE         = regexp.MustCompile(`(?i)\[goal:\s*([a-z0-9-]+)\]`)
	trailerRE             = regexp.MustCompile(`(?im)^[ \t]*(?:Goal-Ref|Sila-Goal|Fixes-Goal|Goal)[ \t]*:[ \t]*(?:goals/)?([a-z0-9-]+)(?:/goal\.md)?[ \t]*$`)
	carriedForwardRE      = regexp.MustCompile(`(?im)^[ \t]*(?:Carried-Forward-From|Supercedes|Supersedes)[ \t]*:[ \t]*(?:goals/)?([a-z0-9-]+)(?:/goal\.md)?[ \t]*$`)
	stepHeadingRE         = regexp.MustCompile(`(?i)^#{2,4}[ \t]*Step[ \t]+[0-9]+[ \t]*[.:\x{2014}\x{2013}-]?[ \t]*(.+)$`)
	depLineRE             = regexp.MustCompile(`(?im)^[ \t]*(?:[-*][ \t]*(?:\*\*)?(?:Depends\s*On|Prerequisites|Dependencies|Blocked\s*By|Requires)(?:\*\*)?|(?:depends_on|dependencies|prerequisites))[ \t]*:[ \t]*([^\r\n]+)$`)
	depHeaderRE           = regexp.MustCompile(`(?i)^[ \t]*(?:[-*][ \t]*(?:\*\*)?(?:Depends\s*On|Prerequisites|Dependencies|Blocked\s*By|Requires)(?:\*\*)?|(?:depends_on|dependencies|prerequisites))[ \t]*:?[ \t]*$`)
	depInlineRE           = regexp.MustCompile(`(?i)^[ \t]*(?:[-*][ \t]*(?:\*\*)?(?:Depends\s*On|Prerequisites|Dependencies|Blocked\s*By|Requires)(?:\*\*)?|(?:depends_on|dependencies|prerequisites))[ \t]*:[ \t]*(.+)$`)
	depSectionRE          = regexp.MustCompile(`(?i)^##+[ \t]*(?:Prerequisites|Dependencies|Blocked\s*By|Requires)\b`)
	touchLineRE           = regexp.MustCompile(`(?im)^[ \t]*(?:[-*][ \t]*(?:\*\*)?(?:Files\s*To\s*Touch|Files|Touch)(?:\*\*)?|(?:files_to_touch|files|touch))[ \t]*:[ \t]*([^\r\n]+)$`)
	touchHeaderRE         = regexp.MustCompile(`(?i)^[ \t]*(?:[-*][ \t]*(?:\*\*)?(?:Files\s*To\s*Touch|Files|Touch)(?:\*\*)?|(?:files_to_touch|files|touch))[ \t]*:?[ \t]*$`)
	touchInlineRE         = regexp.MustCompile(`(?i)^[ \t]*(?:[-*][ \t]*(?:\*\*)?(?:Files\s*To\s*Touch|Files|Touch)(?:\*\*)?|(?:files_to_touch|files|touch))[ \t]*:[ \t]*(.+)$`)
	touchSectionRE        = regexp.MustCompile(`(?i)^##+[ \t]*(?:Files\s*To\s*Touch|Files|Touch)\b`)
	indepRE               = regexp.MustCompile(`(?im)^[ \t]*(?:[-*][ \t]*(?:\*\*)?Mode(?:\*\*)?|(?:mode|independent))[ \t]*:[ \t]*([^\r\n]+)$`)
	explicitTierLineRE    = regexp.MustCompile(`(?im)^[ \t]*(?:[-*][ \t]*(?:\*\*)?Tier(?:\*\*)?|tier)[ \t]*:[ \t]*([^\r\n]+)$`)
	tierLineRE            = regexp.MustCompile(`(?im)^[ \t]*(?:[-*][ \t]*(?:\*\*)?(?:Tier|Priority)(?:\*\*)?|(?:tier|priority))[ \t]*:[ \t]*([^\r\n]+)$`)
	seqLineRE             = regexp.MustCompile(`(?im)^[ \t]*(?:[-*][ \t]*(?:\*\*)?Sequence(?:\*\*)?|(?:sequence|order|seq))[ \t]*:[ \t]*#?(\d+)`)
	titleSeqRE            = regexp.MustCompile(`(?i)(?:^|\s)#(\d+)(?:\s|:|\.|\)|$)`)
	mdLinkRE              = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	personaLineRE         = regexp.MustCompile(`(?im)^[ \t]*(?:[-*][ \t]*(?:\*\*)?(?:Personas|Review\s*Squad|Squad|Reviewers)(?:\*\*)?|(?:personas|review_squad|squad|reviewers))[ \t]*:[ \t]*([^\r\n]+)$`)
	personaHeaderRE       = regexp.MustCompile(`(?i)^[ \t]*(?:[-*][ \t]*(?:\*\*)?(?:Personas|Review\s*Squad|Squad|Reviewers)(?:\*\*)?|(?:personas|review_squad|squad|reviewers))[ \t]*:?[ \t]*$`)
	personaSectionRE      = regexp.MustCompile(`(?i)^##+[ \t]*(?:Review\s*Squad|Personas|Reviewers)\b`)
	alienPropertyBulletRE = regexp.MustCompile(`(?i)^[-*+][ \t]*(?:\*\*)?(?:Mode|Depends\s*On|Prerequisites|Dependencies|Blocked\s*By|Requires|Sequence|Order|Seq|Shape|Tier|Priority|Status|Commit|Personas|Review\s*Squad|Squad|Reviewers|Files\s*To\s*Touch|Files|Touch)(?:\*\*)?[ \t]*:`)
)

func stripInlineComment(s string) string {
	var inSingle, inDouble, escaped bool
	var bracketDepth, parenDepth int
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && (inSingle || inDouble) {
			escaped = true
			continue
		}
		if c == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if c == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if inSingle || inDouble {
			continue
		}
		if c == '[' {
			bracketDepth++
			continue
		}
		if c == ']' && bracketDepth > 0 {
			bracketDepth--
			continue
		}
		if c == '(' {
			parenDepth++
			continue
		}
		if c == ')' && parenDepth > 0 {
			parenDepth--
			continue
		}
		if bracketDepth == 0 && parenDepth == 0 && c == '#' {
			return strings.TrimRight(s[:i], " \t\r\n")
		}
	}
	return s
}

var validSlugRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func isValidSlug(s string) bool {
	return validSlugRE.MatchString(s)
}

func isSpecialDepValue(s string) bool {
	return s == "" || strings.EqualFold(s, "none") || strings.EqualFold(s, "nil") || s == "-" || strings.EqualFold(s, "null") || s == "[]"
}

func isAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

var onDependencyWarning func(raw string, remainder string)

func warnDependencyRemainder(raw string, remainder string) {
	if onDependencyWarning != nil {
		onDependencyWarning(raw, remainder)
		return
	}
	fmt.Fprintf(os.Stderr, "warning: goal dependency remainder %q ignored (not a clean slug list)\n", remainder)
}

// parseDependencySlugTokens extracts only slug-shaped comma-separated tokens from raw,
// stopping at the first non-slug character and returning any unparsed remainder.
func parseDependencySlugTokens(raw string) ([]string, string) {
	raw = stripInlineComment(raw)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}
	if alienPropertyBulletRE.MatchString(raw) {
		return nil, ""
	}

	// Unwrap outer array brackets if present and not a markdown link
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") && !strings.Contains(raw, "](") {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}

	if raw == "" || isSpecialDepValue(raw) {
		return nil, ""
	}

	var slugs []string
	curr := raw

	for {
		curr = strings.TrimLeft(curr, " \t")
		if curr == "" {
			break
		}

		// 1. Markdown link: [text](target)
		if loc := mdLinkRE.FindStringIndex(curr); loc != nil && loc[0] == 0 {
			matched := curr[loc[0]:loc[1]]
			m := mdLinkRE.FindStringSubmatch(matched)
			var slug string
			if len(m) > 2 {
				target := strings.TrimSpace(m[2])
				if sm := slugPathRE.FindStringSubmatch(target); len(sm) > 1 {
					slug = cleanSlug(sm[1])
				} else {
					slug = cleanSlug(strings.Trim(m[1], "`\"'"))
				}
			}
			if slug != "" && isValidSlug(slug) {
				if !slices.Contains(slugs, slug) && !isSpecialDepValue(slug) {
					slugs = append(slugs, slug)
				}
				curr = curr[loc[1]:]
				goto afterToken
			}
			return slugs, curr
		}

		// 2. Quoted slug: "slug", 'slug', or `slug`
		if curr[0] == '"' || curr[0] == '\'' || curr[0] == '`' {
			quoteChar := curr[0]
			closeIdx := strings.IndexByte(curr[1:], quoteChar)
			if closeIdx != -1 {
				quoted := curr[1 : 1+closeIdx]
				slug := cleanSlug(quoted)
				if slug != "" && isValidSlug(slug) {
					if !slices.Contains(slugs, slug) && !isSpecialDepValue(slug) {
						slugs = append(slugs, slug)
					}
					curr = curr[1+closeIdx+1:]
					goto afterToken
				}
			}
			return slugs, curr
		}

		// 3. Plain slug token: must start with alphanumeric
		if !isAlphaNum(curr[0]) {
			return slugs, curr
		}

		{
			end := 0
			for end < len(curr) && (isAlphaNum(curr[end]) || curr[end] == '-' || curr[end] == '_') {
				end++
			}
			tokenStr := curr[:end]
			slug := cleanSlug(tokenStr)
			if slug == "" || !isValidSlug(slug) {
				return slugs, curr
			}
			if slug == "and" || slug == "or" {
				return slugs, curr
			}
			if !isSpecialDepValue(slug) && !slices.Contains(slugs, slug) {
				slugs = append(slugs, slug)
			}
			curr = curr[end:]
		}

	afterToken:
		// After a token, expect either end-of-string or comma separator
		trimmedCurr := strings.TrimLeft(curr, " \t")
		if trimmedCurr == "" {
			return slugs, ""
		}
		if trimmedCurr[0] == ',' {
			curr = trimmedCurr[1:]
			continue
		}
		// First non-slug character encountered: stop and return remainder
		return slugs, trimmedCurr
	}

	return slugs, ""
}

func cleanDepSlug(raw string) string {
	slugs, rem := parseDependencySlugTokens(raw)
	if rem != "" {
		warnDependencyRemainder(raw, rem)
	}
	if len(slugs) > 0 {
		return slugs[0]
	}
	return ""
}

// NormalizeTier normalizes a tier string into one of four standard tiers:
// "immediate" (Tier 1), "roadmap" (Tier 2, default), "nice-to-have" (Tier 3), "wont-fix" (Tier 4).
func NormalizeTier(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	cleaned := strings.ReplaceAll(raw, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, "_", "")
	cleaned = strings.ReplaceAll(cleaned, "'", "")

	switch cleaned {
	case "immediate", "tier1", "1", "urgent", "blocker", "p0", "p1":
		return "immediate"
	case "nicetohave", "tier3", "3", "optional", "polish", "p3":
		return "nice-to-have"
	case "wontfix", "defer", "deferred", "tier4", "4", "p4":
		return "wont-fix"
	case "roadmap", "tier2", "2", "scheduled", "p2", "default", "":
		return "roadmap"
	default:
		return "roadmap"
	}
}

// parseGoalMetadata extracts title, description, dependency graph info, sequence, and target files from goal.md content.
func parseGoalMetadata(content string) (title, desc string, independent bool, dependsOn []string, seq int, filesToTouch []string, shape string) {
	title, desc, independent, dependsOn, seq, filesToTouch, shape, _ = parseGoalMetadataWithTier(content)
	return title, desc, independent, dependsOn, seq, filesToTouch, shape
}

// parseGoalMetadataWithTier extracts title, description, dependency graph info, sequence, target files, shape, and priority tier from goal.md content.
func parseGoalMetadataWithTier(content string) (title, desc string, independent bool, dependsOn []string, seq int, filesToTouch []string, shape string, tier string) {
	title, desc, independent, dependsOn, seq, filesToTouch, shape, tier, _, _ = parseGoalMetadataFull(content)
	return title, desc, independent, dependsOn, seq, filesToTouch, shape, tier
}

// ParseGoalPersonas extracts assigned review squad persona IDs from goal.md content.
func ParseGoalPersonas(content string) []string {
	_, _, _, _, _, _, _, _, personas, _ := parseGoalMetadataFull(content)
	return personas
}

// parseGoalMetadataFull extracts title, description, dependency graph info, sequence, target files, shape, priority tier, and assigned review squad personas from goal.md content.
func parseGoalMetadataFull(content string) (title, desc string, independent bool, dependsOn []string, seq int, filesToTouch []string, shape string, tier string, personas []string, depWarnings []string) {
	independent = true
	shape = "ship"
	tier = "roadmap"

	body := content
	// 1. Strip and parse YAML frontmatter if present
	if m := frontmatterRE.FindStringSubmatch(content); len(m) > 1 {
		fm := m[1]
		body = content[len(m[0]):]

		// Extract fields from frontmatter lines
		fmLines := strings.Split(fm, "\n")
		var inDepsList bool
		var inTouchList bool
		var inPersonasList bool
		for _, line := range fmLines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}

			if inDepsList {
				trimmedCommentless := stripInlineComment(trimmed)
				if after, ok := strings.CutPrefix(trimmedCommentless, "-"); ok {
					slugs, rem := parseDependencySlugTokens(after)
					for _, item := range slugs {
						if !slices.Contains(dependsOn, item) {
							dependsOn = append(dependsOn, item)
						}
					}
					if rem != "" {
						warnDependencyRemainder(after, rem)
						depWarnings = append(depWarnings, rem)
					}
					continue
				} else if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
					inDepsList = false
				}
			}

			if inTouchList {
				trimmedCommentless := stripInlineComment(trimmed)
				if after, ok := strings.CutPrefix(trimmedCommentless, "-"); ok {
					item := NormalizeGoalPath(after)
					if item != "" && !strings.EqualFold(item, "none") && !strings.EqualFold(item, "nil") && !strings.EqualFold(item, "-") {
						filesToTouch = append(filesToTouch, item)
					}
					continue
				} else if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
					inTouchList = false
				}
			}

			if inPersonasList {
				trimmedCommentless := stripInlineComment(trimmed)
				if after, ok := strings.CutPrefix(trimmedCommentless, "-"); ok {
					item := strings.TrimSpace(after)
					if item != "" && !strings.EqualFold(item, "none") && !strings.EqualFold(item, "nil") && !strings.EqualFold(item, "-") {
						if !slices.Contains(personas, item) {
							personas = append(personas, item)
						}
					}
					continue
				} else if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
					inPersonasList = false
				}
			}

			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				k := strings.ToLower(strings.TrimSpace(parts[0]))
				v := strings.TrimSpace(parts[1])
				vClean := stripInlineComment(v)
				vVal := strings.Trim(vClean, `"'`)

				switch k {
				case "title":
					title = vVal
				case "description", "desc", "overview", "summary":
					desc = vVal
				case "sequence", "seq", "order":
					if sm := regexp.MustCompile(`(\d+)`).FindStringSubmatch(v); len(sm) > 1 {
						fmt.Sscanf(sm[1], "%d", &seq)
					}
				case "independent", "mode":
					low := strings.ToLower(vVal)
					if strings.Contains(low, "dependent") || low == "false" || low == "no" {
						independent = false
					} else if strings.Contains(low, "independent") || strings.Contains(low, "disjoint") || low == "true" || low == "yes" {
						independent = true
					}
				case "shape":
					low := strings.ToLower(vVal)
					if low == "scout" || low == "investigate" || low == "research" || low == "audit" {
						shape = "scout"
					} else {
						shape = "ship"
					}
				case "tier", "priority":
					tier = NormalizeTier(vVal)
				case "depends_on", "dependencies", "prerequisites":
					if vClean == "" {
						inDepsList = true
					} else {
						slugs, rem := parseDependencySlugTokens(vVal)
						for _, slug := range slugs {
							if !slices.Contains(dependsOn, slug) {
								dependsOn = append(dependsOn, slug)
							}
						}
						if rem != "" {
							warnDependencyRemainder(vVal, rem)
							depWarnings = append(depWarnings, rem)
						}
					}
				case "files_to_touch", "files", "touch", "touch_files":
					if vClean == "" {
						inTouchList = true
					} else {
						if strings.HasPrefix(vVal, "[") && strings.HasSuffix(vVal, "]") && !strings.Contains(vVal, "](") {
							vVal = strings.Trim(vVal, "[]")
						}
						if vVal != "" && !strings.EqualFold(vVal, "none") && !strings.EqualFold(vVal, "nil") && !strings.EqualFold(vVal, "-") {
							for p := range strings.SplitSeq(vVal, ",") {
								item := NormalizeGoalPath(p)
								if item != "" && !strings.EqualFold(item, "none") && !strings.EqualFold(item, "nil") && !strings.EqualFold(item, "-") {
									filesToTouch = append(filesToTouch, item)
								}
							}
						}
					}
				case "personas", "persona", "review_squad", "reviewers", "squad":
					if vClean == "" {
						inPersonasList = true
					} else {
						if strings.HasPrefix(vVal, "[") && strings.HasSuffix(vVal, "]") && !strings.Contains(vVal, "](") {
							vVal = strings.Trim(vVal, "[]")
						}
						if vVal != "" && !strings.EqualFold(vVal, "none") && !strings.EqualFold(vVal, "nil") && !strings.EqualFold(vVal, "-") {
							for p := range strings.SplitSeq(vVal, ",") {
								item := strings.TrimSpace(p)
								if item != "" && !strings.EqualFold(item, "none") && !strings.EqualFold(item, "nil") && !strings.EqualFold(item, "-") {
									if !slices.Contains(personas, item) {
										personas = append(personas, item)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// 2. Parse Title from markdown body if not found in frontmatter
	if title == "" {
		if m := titleRE.FindStringSubmatch(body); len(m) > 1 {
			title = strings.TrimSpace(m[1])
		}
	}

	// 3. Parse Description from markdown body if not found in frontmatter
	if desc == "" {
		if m := descHeaderRE.FindStringSubmatch(body); len(m) > 1 {
			rawDesc := m[1]
			if idx := strings.Index(rawDesc, "\n- "); idx != -1 {
				rawDesc = rawDesc[:idx]
			}
			if idx := strings.Index(rawDesc, "\n* "); idx != -1 {
				rawDesc = rawDesc[:idx]
			}
			if idx := strings.Index(rawDesc, "\n---"); idx != -1 {
				rawDesc = rawDesc[:idx]
			}
			desc = cleanWhitespace(rawDesc)
		} else if m := firstPgRE.FindStringSubmatch(body); len(m) > 1 {
			rawDesc := m[1]
			if idx := strings.Index(rawDesc, "\n- "); idx != -1 {
				rawDesc = rawDesc[:idx]
			}
			if idx := strings.Index(rawDesc, "\n* "); idx != -1 {
				rawDesc = rawDesc[:idx]
			}
			if idx := strings.Index(rawDesc, "\n---"); idx != -1 {
				rawDesc = rawDesc[:idx]
			}
			desc = cleanWhitespace(rawDesc)
		}
	}

	// 4. Parse Sequence from markdown body if not found in frontmatter
	if seq == 0 {
		if m := seqLineRE.FindStringSubmatch(body); len(m) > 1 {
			fmt.Sscanf(strings.TrimSpace(m[1]), "%d", &seq)
		} else if title != "" {
			if m := titleSeqRE.FindStringSubmatch(title); len(m) > 1 {
				fmt.Sscanf(strings.TrimSpace(m[1]), "%d", &seq)
			}
		}
	}

	// 5. Parse Mode / Independent from markdown body
	if m := indepRE.FindStringSubmatch(body); len(m) > 1 {
		val := strings.ToLower(strings.TrimSpace(m[1]))
		if strings.Contains(val, "independent") || strings.Contains(val, "disjoint") || val == "true" || val == "yes" {
			independent = true
		} else if strings.Contains(val, "dependent") || val == "false" || val == "no" {
			independent = false
		}
	}

	// 5b. Parse Shape from markdown body (- **Shape**: scout or - Shape: scout)
	shapeLineRE := regexp.MustCompile(`(?im)^[-*]\s*(?:\*\*)?Shape(?:\*\*)?:\s*(\w+)`)
	if m := shapeLineRE.FindStringSubmatch(body); len(m) > 1 {
		val := strings.ToLower(strings.TrimSpace(m[1]))
		if val == "scout" || val == "investigate" || val == "research" || val == "audit" {
			shape = "scout"
		} else if val == "ship" || val == "build" || val == "implement" {
			shape = "ship"
		}
	}

	// 5c. Parse Tier from markdown body (- **Tier**: immediate); prefer an explicit Tier line over Priority fallback.
	if m := explicitTierLineRE.FindStringSubmatch(body); len(m) > 1 {
		tier = NormalizeTier(m[1])
	} else if m := tierLineRE.FindStringSubmatch(body); len(m) > 1 {
		tier = NormalizeTier(m[1])
	}
	if tier == "" {
		tier = "roadmap"
	}

	// 5d. Parse Personas from markdown body (- **Personas**: <id1,id2>)
	if len(personas) == 0 {
		addPersona := func(p string) {
			p = strings.TrimSpace(p)
			if p == "" || strings.EqualFold(p, "none") || strings.EqualFold(p, "nil") || strings.EqualFold(p, "-") || strings.EqualFold(p, "null") || strings.EqualFold(p, "[]") {
				return
			}
			if !slices.Contains(personas, p) {
				personas = append(personas, p)
			}
		}

		if m := personaLineRE.FindStringSubmatch(body); len(m) > 1 {
			rawVal := strings.TrimSpace(m[1])
			if strings.HasPrefix(rawVal, "[") && strings.HasSuffix(rawVal, "]") && !strings.Contains(rawVal, "](") {
				rawVal = strings.Trim(rawVal, "[]")
			}
			for p := range strings.SplitSeq(rawVal, ",") {
				addPersona(p)
			}
		}

		// Also check for dedicated ## Review Squad / ## Personas section
		if len(personas) == 0 {
			lines := strings.Split(body, "\n")
			inSquadSec := false
			for _, l := range lines {
				trimmed := strings.TrimSpace(l)
				if strings.HasPrefix(trimmed, "##") {
					if personaSectionRE.MatchString(trimmed) {
						inSquadSec = true
						continue
					} else if inSquadSec {
						break
					}
				}
				if inSquadSec {
					if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
						item := strings.TrimSpace(trimmed[2:])
						addPersona(item)
					}
				}
			}
		}
	}

	// 6. Parse Dependencies from markdown body if not found in frontmatter
	if len(dependsOn) == 0 {
		addDep := func(slug string) {
			if slug == "" || strings.EqualFold(slug, "none") || strings.EqualFold(slug, "nil") || strings.EqualFold(slug, "-") || strings.EqualFold(slug, "null") || strings.EqualFold(slug, "[]") {
				return
			}
			if slices.Contains(dependsOn, slug) {
				return
			}
			dependsOn = append(dependsOn, slug)
		}

		parseInlineDeps := func(rawVal string) {
			slugs, rem := parseDependencySlugTokens(rawVal)
			for _, slug := range slugs {
				addDep(slug)
			}
			if rem != "" {
				warnDependencyRemainder(rawVal, rem)
				depWarnings = append(depWarnings, rem)
			}
		}

		lines := strings.Split(body, "\n")
		var inDepBlock bool

		for _, l := range lines {
			trimmedLine := strings.TrimSpace(l)
			if trimmedLine == "" {
				continue
			}

			var cleanLine string
			if strings.HasPrefix(trimmedLine, "#") {
				cleanLine = trimmedLine
			} else {
				cleanLine = strings.TrimSpace(stripInlineComment(trimmedLine))
			}
			if cleanLine == "" {
				continue
			}

			if !inDepBlock {
				if depHeaderRE.MatchString(cleanLine) || depSectionRE.MatchString(cleanLine) {
					inDepBlock = true
					continue
				}
				if m := depInlineRE.FindStringSubmatch(cleanLine); len(m) > 1 {
					parseInlineDeps(m[1])
					continue
				}
			} else {
				// 1. Heading check: renew if dependency section, terminate if alien section
				if strings.HasPrefix(cleanLine, "#") {
					if depSectionRE.MatchString(cleanLine) {
						inDepBlock = true
						continue
					}
					inDepBlock = false
					continue
				}

				// 2. Thematic break check: terminate block
				if cleanLine == "---" || cleanLine == "***" || cleanLine == "___" ||
					cleanLine == "- - -" || cleanLine == "* * *" ||
					strings.HasPrefix(cleanLine, "---") || strings.HasPrefix(cleanLine, "***") || strings.HasPrefix(cleanLine, "___") {
					inDepBlock = false
					continue
				}

				// 3. Subheader bullet check: skip without parsing as item
				if depHeaderRE.MatchString(cleanLine) {
					continue
				}

				// 4. Inline dependency check within active dependency block
				if m := depInlineRE.FindStringSubmatch(cleanLine); len(m) > 1 {
					parseInlineDeps(m[1])
					continue
				}

				// 5. Alien property bullet check (e.g. - **Sequence**: or - **Mode**: or - Mode:)
				if strings.HasPrefix(cleanLine, "- **") || strings.HasPrefix(cleanLine, "* **") || alienPropertyBulletRE.MatchString(cleanLine) {
					inDepBlock = false
					continue
				}

				// 6. List item bullet check (- item, * item, + item)
				if strings.HasPrefix(cleanLine, "-") || strings.HasPrefix(cleanLine, "*") || strings.HasPrefix(cleanLine, "+") {
					itemStr := strings.TrimLeft(cleanLine, "-*+ \t")
					slugs, rem := parseDependencySlugTokens(itemStr)
					for _, slug := range slugs {
						addDep(slug)
					}
					if rem != "" {
						warnDependencyRemainder(itemStr, rem)
						depWarnings = append(depWarnings, rem)
					}
				} else if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
					// 7. Unindented non-bullet text terminates block
					inDepBlock = false
				}
			}
		}
	}

	// 7. Parse FilesToTouch from markdown body if not found in frontmatter
	if len(filesToTouch) == 0 {
		addTouch := func(path string) {
			path = NormalizeGoalPath(path)
			if path == "" || strings.EqualFold(path, "none") || strings.EqualFold(path, "nil") || strings.EqualFold(path, "-") || strings.EqualFold(path, "null") || strings.EqualFold(path, "[]") {
				return
			}
			if slices.Contains(filesToTouch, path) {
				return
			}
			filesToTouch = append(filesToTouch, path)
		}

		parseInlineTouch := func(rawVal string) {
			rawVal = strings.TrimSpace(rawVal)
			if strings.HasPrefix(rawVal, "[") && strings.HasSuffix(rawVal, "]") && !strings.Contains(rawVal, "](") {
				rawVal = strings.Trim(rawVal, "[]")
			}
			if rawVal == "" || strings.EqualFold(rawVal, "none") || strings.EqualFold(rawVal, "nil") || strings.EqualFold(rawVal, "-") || strings.EqualFold(rawVal, "null") {
				return
			}
			for p := range strings.SplitSeq(rawVal, ",") {
				addTouch(p)
			}
		}

		lines := strings.Split(body, "\n")
		var inTouchBlock bool

		for _, l := range lines {
			trimmedLine := strings.TrimSpace(l)
			if trimmedLine == "" {
				continue
			}

			var cleanLine string
			if strings.HasPrefix(trimmedLine, "#") {
				cleanLine = trimmedLine
			} else {
				cleanLine = strings.TrimSpace(stripInlineComment(trimmedLine))
			}
			if cleanLine == "" {
				continue
			}

			if !inTouchBlock {
				if touchHeaderRE.MatchString(cleanLine) || touchSectionRE.MatchString(cleanLine) {
					inTouchBlock = true
					continue
				}
				if m := touchInlineRE.FindStringSubmatch(cleanLine); len(m) > 1 {
					parseInlineTouch(m[1])
					continue
				}
			} else {
				// 1. Heading check: renew if touch section, terminate if alien section
				if strings.HasPrefix(cleanLine, "#") {
					if touchSectionRE.MatchString(cleanLine) {
						inTouchBlock = true
						continue
					}
					inTouchBlock = false
					continue
				}

				// 2. Thematic break check: terminate block
				if cleanLine == "---" || cleanLine == "***" || cleanLine == "___" ||
					cleanLine == "- - -" || cleanLine == "* * *" ||
					strings.HasPrefix(cleanLine, "---") || strings.HasPrefix(cleanLine, "***") || strings.HasPrefix(cleanLine, "___") {
					inTouchBlock = false
					continue
				}

				// 3. Subheader bullet check: skip without parsing as item
				if touchHeaderRE.MatchString(cleanLine) {
					continue
				}

				// 4. Inline touch check within active touch block
				if m := touchInlineRE.FindStringSubmatch(cleanLine); len(m) > 1 {
					parseInlineTouch(m[1])
					continue
				}

				// 5. Alien property bullet check (e.g. - **Sequence**: or - **Depends On**: or - Depends On:)
				if strings.HasPrefix(cleanLine, "- **") || strings.HasPrefix(cleanLine, "* **") || alienPropertyBulletRE.MatchString(cleanLine) {
					inTouchBlock = false
					continue
				}

				// 6. List item bullet check (- item, * item, + item)
				if strings.HasPrefix(cleanLine, "-") || strings.HasPrefix(cleanLine, "*") || strings.HasPrefix(cleanLine, "+") {
					itemStr := strings.TrimLeft(cleanLine, "-*+ \t")
					addTouch(itemStr)
				} else if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
					// 7. Unindented non-bullet text terminates block
					inTouchBlock = false
				}
			}
		}
	}

	if len(dependsOn) > 0 {
		independent = false
	}

	return title, desc, independent, dependsOn, seq, filesToTouch, shape, tier, personas, depWarnings
}

// ScanGoals scans the given repository root directory for `goals/` and audits implementation status.
func ScanGoals(rootDir string) (*AuditResult, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		absRoot = rootDir
	}
	projectName := store.ProjectID(absRoot)
	goalsDir := filepath.Join(rootDir, "goals")

	if _, err := os.Stat(goalsDir); os.IsNotExist(err) {
		return &AuditResult{
			Project:    projectName,
			ProjectDir: absRoot,
			Goals:      []Goal{},
		}, nil
	}

	entries, err := os.ReadDir(goalsDir)
	if err != nil {
		return nil, fmt.Errorf("read goals directory: %w", err)
	}

	var goalSlugs []string
	for _, e := range entries {
		if e.IsDir() {
			// Underscore-prefixed directories are scaffolding (for example
			// Plannotator _program work dirs), not goal packages.
			if strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			goalSlugs = append(goalSlugs, e.Name())
		}
	}

	gitCommits, skippedCommits, nearMissCommits, carriedForwardCommits, commitParents := scanGitCommits(rootDir, goalSlugs)
	dirtyFiles := scanDirtyFiles(rootDir)

	var rawList []Goal
	statusMap := make(map[string]string)

	for _, slug := range goalSlugs {
		dir := filepath.Join(goalsDir, slug)
		goalPath, hasGoal := findGoalFile(dir, "goal.md")
		planPath, hasPlan := findGoalFile(dir, "plan.md")
		_, hasFacts := findGoalFile(dir, "facts.md")

		title := slug
		description := ""
		independent := true
		var dependsOn []string
		var depWarnings []string
		var filesToTouch []string
		seq := 0
		shape := "ship"
		tier := "roadmap"
		var personas []string
		memoryProtocol := "legacy"

		var isnad *IsnadHeader
		if hasGoal {
			data, err := os.ReadFile(goalPath)
			if err == nil {
				title, description, independent, dependsOn, seq, filesToTouch, shape, tier, personas, depWarnings = parseGoalMetadataFull(string(data))
				if title == "" {
					title = slugToTitle(slug)
				}
				memoryProtocol = ParseMemoryProtocol(string(data))
				isnad = ParseIsnadHeader(string(data))
			}
		}

		var claim *GoalClaim
		claimPath := filepath.Join(dir, "claim.json")
		if fileExists(claimPath) {
			claim, _ = readClaim(claimPath)
			if claim != nil {
				if len(claim.FilesToTouch) > 0 {
					filesToTouch = claim.FilesToTouch
				} else if len(filesToTouch) > 0 {
					claim.FilesToTouch = filesToTouch
				}
			}
		}

		reopenRec, _ := GetActiveReopen(rootDir, slug)
		isReopened := reopenRec != nil && reopenRec.Active
		var reopenReason string
		reopenEpoch := 0
		if reopenRec != nil {
			reopenEpoch = reopenRec.ReopenEpoch
		}
		if isReopened {
			reopenReason = reopenRec.Reason
		}

		certRec, _ := GetActiveCertification(rootDir, slug)
		auditReceipt, _ := GetActiveAuditReceipt(rootDir, slug)
		supersedeRec, _ := GetActiveSupersede(rootDir, slug)
		retroRec, _ := GetActiveRetrospective(rootDir, slug)

		tasksMet := 0
		tasksTotal := 0
		acceptanceStatus := ""
		acceptedTasks := 0
		requiredCases := 0
		passedCases := 0
		isDefaultScaffold := false
		var planContent string
		hasUncheckedTasks := false

		if wpTasks, err := LoadWorkPackageTasksIfPresent(dir); err != nil {
			return nil, fmt.Errorf("goal %s: %w", slug, err)
		} else if wpTasks != nil {
			tasksTotal = len(wpTasks)
			tasksMet = 0
			for _, t := range wpTasks {
				if t.Completed {
					tasksMet++
				}
			}
			acceptedTasks = tasksMet
			if accRes, err := EvaluateGoalAcceptance(rootDir, slug); err == nil && accRes != nil {
				acceptanceStatus = string(accRes.EvidenceStatus)
			}
			if wp, err := LoadWorkPackage(rootDir, slug); err == nil && wp != nil {
				for _, ac := range wp.Acceptance {
					if ac.Mandatory {
						requiredCases++
					}
				}
				if acceptanceStatus == string(EvidenceStatusAccepted) {
					passedCases = requiredCases
				} else if accRes, _ := EvaluateGoalAcceptance(rootDir, slug); accRes != nil {
					passedCases = requiredCases - len(accRes.MissingCaseIDs) - len(accRes.FailedCaseIDs)
					if passedCases < 0 {
						passedCases = 0
					}
				}
			}

			// For structured goals, git attribution determines implemented status based on
			// plan checkboxes (if plan.md exists), while acceptance is distinct.
			if hasPlan {
				if planData, err := os.ReadFile(planPath); err == nil {
					planContent = string(planData)
					if !hasExplicitCheckboxes(planContent) || countUncheckedNonAttributionTasks(planContent) == 0 {
						hasUncheckedTasks = false
					} else {
						hasUncheckedTasks = true
					}
				}
			} else {
				hasUncheckedTasks = false
			}
		} else {
			if hasPlan {
				planData, err := os.ReadFile(planPath)
				if err == nil {
					planContent = string(planData)
					tasksMet, tasksTotal = countCheckboxes(planContent)
					isDefaultScaffold = isDefaultScaffoldPlan(planContent)
				}
			}
			if tasksTotal == 0 && hasGoal {
				goalData, err := os.ReadFile(filepath.Join(dir, "goal.md"))
				if err == nil {
					planContent = string(goalData)
					tasksMet, tasksTotal = countCheckboxes(planContent)
				}
			}

			if tasksTotal > 0 && tasksMet < tasksTotal {
				if planContent != "" && !hasExplicitCheckboxes(planContent) {
					hasUncheckedTasks = false
				} else if planContent != "" && countUncheckedNonAttributionTasks(planContent) == 0 {
					hasUncheckedTasks = false
				} else if tasksMet > 0 || !isDefaultScaffold || isReopened {
					hasUncheckedTasks = true
				}
			}
		}

		pendingReason := ""

		status := "pending"
		state := StateDraft
		commit := ""
		task := ""

		if isReopened {
			if claim != nil {
				status = "claimed"
				state = StateClaimed
			} else {
				status = "pending"
				state = StateRemediation
			}
			task = "Remediation: " + reopenReason
			if c, ok := gitCommits[slug]; ok && hasUncheckedTasks {
				commit = c
				pendingReason = fmt.Sprintf("reopened remediation: commit %s found but %d/%d tasks remain unchecked in plan.md", c, tasksTotal-tasksMet, tasksTotal)
			}
		} else if c, ok := gitCommits[slug]; ok {
			if hasUncheckedTasks {
				if claim != nil {
					status = "claimed"
					state = StateClaimed
				} else {
					status = "pending"
					state = StateDraft
				}
				commit = c
				task = fmt.Sprintf("Commit %s (%d/%d tasks complete)", c, tasksMet, tasksTotal)
				pendingReason = fmt.Sprintf("commit %s attributed but %d/%d tasks remain unchecked in plan.md", c, tasksTotal-tasksMet, tasksTotal)
			} else {
				status = "implemented"
				state = StateImplemented
				commit = c
				task = "Git Commit"
			}
		} else if certRec != nil {
			status = "implemented"
			state = StateImplemented
			if sha := strings.TrimSpace(certRec.CommitSHA); sha != "" && sha != "-" {
				if len(sha) > 7 {
					commit = sha[:7]
				} else {
					commit = sha
				}
			} else {
				commit = "evidence"
			}
			task = fmt.Sprintf("Evidence (%s)", filepath.Base(certRec.EvidenceFile))
		} else if auditReceipt != nil && auditReceipt.CommitSHA != "" && VerifyAuditReceipt(auditReceipt) == nil {
			status = "implemented"
			state = StateImplemented
			commit = auditReceipt.CommitSHA
			task = "Audit Receipt"
		} else if retroRec != nil && retroRec.Commit != "" {
			status = "implemented"
			state = StateImplemented
			commit = retroRec.Commit
			task = "Retrospective"
		} else if acceptanceStatus == string(EvidenceStatusAccepted) {
			status = "implemented"
			state = StateImplemented
			commit = "accepted"
			task = "Accepted Evidence"
		} else if supersedeRec != nil {
			status = "superseded"
			state = StateSuperseded
			task = fmt.Sprintf("Superseded by %s", supersedeRec.SupersededBy)
		} else if cf, ok := carriedForwardCommits[slug]; ok {
			status = "superseded"
			state = StateSuperseded
			supersedeRec = &SupersededRecord{
				GoalSlug:     slug,
				SupersededBy: cf,
				Reason:       "carried forward in " + cf,
				Active:       true,
			}
			task = "Carried Forward"
		} else if claim != nil {
			status = "claimed"
			state = StateClaimed
		} else if !hasGoal || !hasPlan || !hasFacts {
			status = "draft"
			state = StateDraft
		} else {
			status = "pending"
			if isnad != nil && isnad.SpecAuditedBy != "" && !strings.Contains(strings.ToLower(isnad.SpecAuditedBy), "pending") {
				state = StateSpecAudited
			} else {
				state = StateDraft
			}
		}

		var missingFiles []string
		if !hasGoal {
			missingFiles = append(missingFiles, "goal.md")
		}
		if !hasPlan {
			missingFiles = append(missingFiles, "plan.md")
		}
		if !hasFacts {
			missingFiles = append(missingFiles, "facts.md")
		}

		statusMap[slug] = status

		verificationStatus := "unverified"
		verificationLabel := "unverified"

		if isReopened {
			verificationStatus = "reopened"
			verificationLabel = "reopened"
		} else if status == "implemented" {
			var evidenceCommits []string
			hasEvidence := false
			if auditReceipt != nil {
				if err := VerifyAuditReceipt(auditReceipt); err == nil {
					hasEvidence = true
					evidenceCommits = append(evidenceCommits, auditReceipt.CommitSHA)
				}
			}
			if certRec != nil && certRec.Verified {
				hasEvidence = true
				ec := certRec.CommitSHA
				if ec == "" && commit != "" {
					ec = commit
				}
				evidenceCommits = append(evidenceCommits, ec)
			}

			if hasEvidence {
				isStale := false
				if commit != "" && commit != "evidence" {
					matchedAny := false
					for _, evidenceCommit := range evidenceCommits {
						if evidenceCommit == "" {
							continue
						}
						matches := strings.HasPrefix(commit, evidenceCommit) || strings.HasPrefix(evidenceCommit, commit)
						if !matches && commitParents != nil {
							if parents, ok := commitParents[commit]; ok {
								for _, p := range parents {
									if strings.HasPrefix(p, evidenceCommit) || strings.HasPrefix(evidenceCommit, p) {
										matches = true
										break
									}
								}
							}
						}
						if !matches && isCommitAncestor(rootDir, commit, evidenceCommit) {
							matches = true
						}
						if !matches && isCommitAncestor(rootDir, evidenceCommit, commit) && isGoalsOnlyDiffBetweenCommits(rootDir, evidenceCommit, commit) {
							matches = true
						}
						if matches {
							matchedAny = true
							break
						}
					}
					if !matchedAny {
						isStale = true
					}
				}
				if !isStale && len(dirtyFiles) > 0 {
					for _, f := range filesToTouch {
						cleanF := filepath.Clean(f)
						if cleanF == "goals/README.md" ||
							cleanF == filepath.Join("goals", slug, "audit-receipt.json") ||
							cleanF == filepath.Join("goals", slug, "certification.json") ||
							cleanF == filepath.Join("goals", slug, "evidence.md") ||
							cleanF == filepath.Join("goals", slug, "goal.md") {
							continue
						}
						if dirtyFiles[cleanF] {
							isStale = true
							break
						}
					}
				}
				if isStale {
					verificationStatus = "stale"
					verificationLabel = "stale"
				} else {
					verificationStatus = "verified"
					verificationLabel = "verified"
				}
			}
		}

		skippedCandidates := skippedCommits[slug]
		nearMisses := nearMissCommits[slug]
		if status == "implemented" || status == "superseded" {
			skippedCandidates = nil
			nearMisses = nil
		}

		var supersededBy, supersededReason string
		isSuperseded := (status == "superseded")
		if isSuperseded && supersedeRec != nil {
			supersededBy = supersedeRec.SupersededBy
			supersededReason = supersedeRec.Reason
		}

		var certMemory *CertificationMemory
		var certMemoryStatus string
		if certRec != nil && certRec.Memory != nil {
			certMemory = certRec.Memory
			certMemoryStatus = certRec.Memory.Status
		}

		rawList = append(rawList, Goal{
			Slug:               slug,
			Title:              title,
			Description:        description,
			Status:             status,
			SkippedCandidates:  skippedCandidates,
			NearMisses:         nearMisses,
			Reopened:           isReopened,
			ReopenReason:       reopenReason,
			Reopen:             reopenRec,
			Independent:        independent,
			DependsOn:          dependsOn,
			DependencyWarnings: depWarnings,
			Sequence:           seq,
			FilesToTouch:       filesToTouch,
			TasksTotal:         tasksTotal,
			TasksMet:           tasksMet,
			AcceptanceStatus:   acceptanceStatus,
			AcceptedTasks:      acceptedTasks,
			RequiredCases:      requiredCases,
			PassedCases:        passedCases,
			HasGoalMd:          hasGoal,
			HasPlanMd:          hasPlan,
			HasFactsMd:         hasFacts,
			MissingFiles:       missingFiles,
			Commit:             commit,
			Task:               task,
			LaunchCmd:          fmt.Sprintf("/goal goals/%s/goal.md", slug),
			Claim:              claim,
			Shape:              shape,
			Tier:               tier,
			MemoryProtocol:     memoryProtocol,
			MemoryStatus:       certMemoryStatus,
			Memory:             certMemory,
			Superseded:         isSuperseded,
			SupersededBy:       supersededBy,
			SupersededReason:   supersededReason,
			Supersede:          supersedeRec,
			Certified:          verificationStatus == "verified",
			Certification:      certRec,
			VerificationStatus: verificationStatus,
			VerificationLabel:  verificationLabel,
			PendingReason:      pendingReason,
			State:              state,
			StateLabel:         state.Label(),
			Isnad:              isnad,
			ReopenEpoch:        reopenEpoch,
			Personas:           personas,
		})
	}

	// Build DependencyGraph for cycle detection and downstream impact calculation
	graph := BuildDependencyGraphFromSlice(rawList)
	cycles := graph.DetectCycles()
	cyclicMap := make(map[string]string)
	for _, c := range cycles {
		pStr := strings.Join(c, " -> ")
		for _, node := range c {
			cyclicMap[node] = pStr
		}
	}
	impactMap := graph.DownstreamImpactMap()

	// Build tainted map for all descendants of actively reopened goals
	reopenedSet := make(map[string]bool)
	for _, g := range rawList {
		if g.Reopened || g.Status == "remediation" {
			reopenedSet[g.Slug] = true
		}
	}
	taintedMap := make(map[string]string)
	for u := range reopenedSet {
		for _, w := range graph.TransitiveDependents(u) {
			if !reopenedSet[w] {
				taintedMap[w] = u
			}
		}
	}

	// Gather active non-stale claims for file collision detection
	var activeClaims []*GoalClaim
	for _, g := range rawList {
		if g.Claim != nil && !g.Claim.Stale && g.Status == "claimed" {
			activeClaims = append(activeClaims, g.Claim)
		}
	}

	// Second pass: evaluate dependency blockers & graph properties
	var list []Goal
	var implemented, claimed, pending, ready, blocked, contestedCount, independentCount, draft, reopenedCount, supersededCount, taintedCount int
	var verifiedCount, unverifiedCount, staleCount int
	var readyIndependentCount, readyUnblockedCount int
	var immediateCount, roadmapCount, niceToHaveCount, wontFixCount int
	var totalTasks, completedTasks int
	var allConflicts []FileConflict

	for _, g := range rawList {
		if g.Reopened {
			reopenedCount++
		}
		g.DownstreamCount = impactMap[g.Slug]
		g.RootBlockers = graph.RootBlockers(g.Slug, statusMap)

		if cause, isTainted := taintedMap[g.Slug]; isTainted {
			g.State = StateTainted
			g.Status = "pending"
			g.Blocked = true
			g.BlockedReason = fmt.Sprintf("prerequisite %q is in remediation", cause)
			g.Commit = ""
			g.Task = ""
			statusMap[g.Slug] = "pending"
			taintedCount++
		}

		if cyclePath, isCyclic := cyclicMap[g.Slug]; isCyclic {
			g.Blocked = true
			g.Independent = false
			g.BlockedReason = fmt.Sprintf("circular dependency: %s", cyclePath)
		}

		if len(g.DependsOn) > 0 {
			var unmet []string
			for _, dep := range g.DependsOn {
				depStatus, exists := statusMap[dep]
				if !exists || (depStatus != "implemented" && depStatus != "superseded") {
					unmet = append(unmet, dep)
				}
			}
			g.UnmetDeps = unmet
			if len(unmet) > 0 {
				g.Blocked = true
				g.Independent = false
			} else if _, isCyclic := cyclicMap[g.Slug]; !isCyclic {
				g.Blocked = false
			}
		} else if _, isCyclic := cyclicMap[g.Slug]; !isCyclic {
			g.Independent = true
			g.Blocked = false
		}

		// Evaluate structured dependency conditions and blocker gate
		if fileExists(filepath.Join(rootDir, "goals", g.Slug, "work-package.json")) {
			unmetBlockers, reasons := CheckGoalBlockersForScanner(rootDir, g.Slug)
			if len(reasons) > 0 {
				g.Blocked = true
				g.Independent = false
				for _, u := range unmetBlockers {
					if !slices.Contains(g.UnmetDeps, u) {
						g.UnmetDeps = append(g.UnmetDeps, u)
					}
				}
				if g.BlockedReason == "" {
					g.BlockedReason = strings.Join(reasons, "; ")
				} else {
					g.BlockedReason = g.BlockedReason + "; " + strings.Join(reasons, "; ")
				}
			}
		}

		// Evaluate file concurrency conflicts against active claims
		// Scout tasks are non-mutating investigations — exempt from collision checks
		if g.Status != "implemented" && g.Status != "superseded" && g.Claim == nil && len(g.FilesToTouch) > 0 && g.Shape != "scout" {
			for _, ac := range activeClaims {
				if ac.GoalSlug == g.Slug {
					continue
				}
				overlaps := OverlappingPaths(g.FilesToTouch, ac.FilesToTouch)
				if len(overlaps) > 0 {
					agentName := ac.Agent
					if agentName == "" {
						agentName = ac.ClaimedBy
					}
					cf := FileConflict{
						GoalSlug:         g.Slug,
						ConflictingGoal:  ac.GoalSlug,
						HolderAgent:      agentName,
						OverlappingFiles: overlaps,
					}
					g.FileConflicts = append(g.FileConflicts, cf)
					allConflicts = append(allConflicts, cf)
				}
			}
		}

		if len(g.FileConflicts) > 0 {
			contestedCount++
		}

		if g.Independent {
			independentCount++
		}

		if g.Status != "superseded" && NormalizeTier(g.Tier) != "wont-fix" {
			totalTasks += g.TasksTotal
			completedTasks += g.TasksMet
		}

		switch g.Status {
		case "implemented":
			implemented++
			switch g.VerificationStatus {
			case "verified":
				verifiedCount++
			case "stale":
				staleCount++
			case "unverified":
				unverifiedCount++
			default:
				unverifiedCount++
			}
		case "superseded":
			supersededCount++
		case "claimed":
			claimed++
		case "pending":
			pending++
			if g.Blocked {
				blocked++
			} else if len(g.FileConflicts) == 0 {
				ready++
				if g.Independent {
					readyIndependentCount++
				} else {
					readyUnblockedCount++
				}
			}
		case "draft":
			draft++
		}

		switch NormalizeTier(g.Tier) {
		case "immediate":
			immediateCount++
		case "nice-to-have":
			niceToHaveCount++
		case "wont-fix":
			wontFixCount++
		case "roadmap":
			fallthrough
		default:
			roadmapCount++
		}

		if g.Status == "pending" {
			if g.PendingReason != "" {
				// Keep specific pending reason (e.g. from epistemic task completion gate)
			} else if g.State == StateTainted {
				g.PendingReason = fmt.Sprintf("tainted: %s", g.BlockedReason)
			} else if g.Blocked {
				if len(g.RootBlockers) > 0 && !sliceEqual(g.UnmetDeps, g.RootBlockers) {
					g.PendingReason = fmt.Sprintf("blocked by %s (root blockers: %s)", strings.Join(g.UnmetDeps, ", "), strings.Join(g.RootBlockers, ", "))
				} else {
					g.PendingReason = fmt.Sprintf("blocked by %s", strings.Join(g.UnmetDeps, ", "))
				}
			} else if len(g.FileConflicts) > 0 {
				g.PendingReason = fmt.Sprintf("contested: touches %v currently held by active goal %s (@%s)", g.FileConflicts[0].OverlappingFiles, g.FileConflicts[0].ConflictingGoal, g.FileConflicts[0].HolderAgent)
			} else if g.State == StateRemediation {
				g.PendingReason = "ready to claim for remediation"
			} else {
				g.PendingReason = "ready to claim; awaiting implementation commit"
			}
		}

		list = append(list, g)
	}

	// Tiered Status Sorting:
	// 0. Remediation (State == StateRemediation && !Blocked)
	// 1. Ready Pending (Status == "pending" && !Blocked)
	// 2. In Progress / Claimed (Status == "claimed")
	// 3. Blocked Pending / Tainted (Status == "pending" && Blocked)
	// 4. Draft (Status == "draft")
	// 5. Implemented (Status == "implemented")
	// 6. Superseded (Status == "superseded")
	statusTier := func(g Goal) int {
		if g.State == StateRemediation && !g.Blocked {
			return 0 // Remediation goals take precedence over all pending goals
		}
		switch g.Status {
		case "pending":
			if !g.Blocked {
				return 1 // Ready Pending
			}
			return 3 // Blocked Pending
		case "claimed":
			return 2 // Claimed / In-Progress
		case "draft":
			return 4 // Draft
		case "implemented":
			return 5 // Implemented
		case "superseded":
			return 6 // Superseded
		default:
			return 7
		}
	}

	sort.Slice(list, func(i, j int) bool {
		tI := statusTier(list[i])
		tJ := statusTier(list[j])
		if tI != tJ {
			return tI < tJ
		}

		// Within same status tier:
		// 1. Explicit Sequence > 0 (ascending: 1, 2, 3...)
		if list[i].Sequence > 0 && list[j].Sequence > 0 && list[i].Sequence != list[j].Sequence {
			return list[i].Sequence < list[j].Sequence
		}
		if list[i].Sequence > 0 && list[j].Sequence == 0 {
			return true
		}
		if list[i].Sequence == 0 && list[j].Sequence > 0 {
			return false
		}

		// 2. Ready tier (Tier 1): Higher Downstream Impact first
		if tI == 1 || tI == 0 {
			impI := impactMap[list[i].Slug]
			impJ := impactMap[list[j].Slug]
			if impI != impJ {
				return impI > impJ
			}
		}

		// 3. Blocked tier (Tier 3): Fewest unmet dependencies first, then impact
		if tI == 3 {
			if len(list[i].UnmetDeps) != len(list[j].UnmetDeps) {
				return len(list[i].UnmetDeps) < len(list[j].UnmetDeps)
			}
			impI := impactMap[list[i].Slug]
			impJ := impactMap[list[j].Slug]
			if impI != impJ {
				return impI > impJ
			}
		}

		// 4. Deterministic Slug tie-breaker
		return list[i].Slug < list[j].Slug
	})

	return &AuditResult{
		Project:               projectName,
		ProjectDir:            absRoot,
		Total:                 len(list),
		ImplementedCount:      implemented,
		VerifiedCount:         verifiedCount,
		UnverifiedCount:       unverifiedCount,
		StaleCount:            staleCount,
		SupersededCount:       supersededCount,
		ReopenedCount:         reopenedCount,
		TaintedCount:          taintedCount,
		ClaimedCount:          claimed,
		PendingCount:          pending,
		ReadyCount:            ready,
		ReadyIndependentCount: readyIndependentCount,
		ReadyUnblockedCount:   readyUnblockedCount,
		BlockedCount:          blocked,
		ContestedCount:        contestedCount,
		IndependentCount:      independentCount,
		DraftCount:            draft,
		ImmediateCount:        immediateCount,
		RoadmapCount:          roadmapCount,
		NiceToHaveCount:       niceToHaveCount,
		WontFixCount:          wontFixCount,
		TotalTasks:            totalTasks,
		CompletedTasks:        completedTasks,
		Cycles:                cycles,
		FileConflicts:         allConflicts,
		Goals:                 list,
	}, nil
}

// FilterByTier returns a filtered copy of the AuditResult containing only goals matching the specified tier.
func (r *AuditResult) FilterByTier(tier string) *AuditResult {
	if r == nil {
		return nil
	}
	norm := NormalizeTier(tier)
	out := &AuditResult{
		Project:         r.Project,
		ProjectDir:      r.ProjectDir,
		Cycles:          r.Cycles,
		ImmediateCount:  r.ImmediateCount,
		RoadmapCount:    r.RoadmapCount,
		NiceToHaveCount: r.NiceToHaveCount,
		WontFixCount:    r.WontFixCount,
	}
	var filtered []Goal
	for _, g := range r.Goals {
		if NormalizeTier(g.Tier) == norm {
			filtered = append(filtered, g)
			switch g.Status {
			case "implemented":
				out.ImplementedCount++
				switch g.VerificationStatus {
				case "verified":
					out.VerifiedCount++
				case "stale":
					out.StaleCount++
				case "unverified":
					out.UnverifiedCount++
				default:
					out.UnverifiedCount++
				}
			case "superseded":
				out.SupersededCount++
			case "claimed":
				out.ClaimedCount++
			case "pending":
				out.PendingCount++
				if g.Blocked {
					out.BlockedCount++
				} else if len(g.FileConflicts) == 0 {
					out.ReadyCount++
					if g.Independent {
						out.ReadyIndependentCount++
					} else {
						out.ReadyUnblockedCount++
					}
				}
			case "draft":
				out.DraftCount++
			}
			if g.Reopened {
				out.ReopenedCount++
			}
			if g.Independent {
				out.IndependentCount++
			}
			if len(g.FileConflicts) > 0 {
				out.ContestedCount++
				out.FileConflicts = append(out.FileConflicts, g.FileConflicts...)
			}
			if g.Status != "superseded" && NormalizeTier(g.Tier) != "wont-fix" {
				out.TotalTasks += g.TasksTotal
				out.CompletedTasks += g.TasksMet
			}
		}
	}
	out.Total = len(filtered)
	out.Goals = filtered
	return out
}

// GoalsBySession returns the goals whose claim carries the given session id.
// It is the deterministic read side of sila goals focus.
func GoalsBySession(res *AuditResult, session string) []Goal {
	session = strings.TrimSpace(session)
	if res == nil || session == "" {
		return nil
	}
	var out []Goal
	for _, g := range res.Goals {
		if g.Claim != nil && g.Claim.SessionID == session {
			out = append(out, g)
		}
	}
	return out
}

// CreateGoalParams contains options for scaffolding a new goal package.
type CreateGoalParams struct {
	RootDir      string       `json:"root_dir,omitempty"`
	Slug         string       `json:"slug"`
	Title        string       `json:"title,omitempty"`
	Description  string       `json:"description,omitempty"`
	DependsOn    []string     `json:"depends_on,omitempty"`
	Independent  *bool        `json:"independent,omitempty"`
	Sequence     int          `json:"sequence,omitempty"`
	FilesToTouch []string     `json:"files_to_touch,omitempty"`
	Overwrite    bool         `json:"overwrite,omitempty"`
	Shape        string       `json:"shape,omitempty"` // "ship" (default) or "scout"
	Tier         string       `json:"tier,omitempty"`  // "immediate", "roadmap" (default), "nice-to-have", "wont-fix"
	Invariants   []string     `json:"invariants,omitempty"`
	Tasks        []string     `json:"tasks,omitempty"`
	Status       string       `json:"status,omitempty"` // "pending" (default) or "implemented" (retrospective)
	Commit       string       `json:"commit,omitempty"` // commit SHA for retrospective goal
	Personas     []string     `json:"personas,omitempty"`
	Strict       bool         `json:"strict,omitempty"` // if true, reject creation if warnings/collisions exist
	WorkPackage  *WorkPackage `json:"work_package,omitempty"`
	SpecReview   *SpecReview  `json:"spec_review,omitempty"`
	TemplateDir  string       `json:"template_dir,omitempty"`
}

// CreateGoal scaffolds a complete goal package directory with goal.md, facts.md, and plan.md.
func CreateGoal(params CreateGoalParams) (*Goal, error) {
	if params.Slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	slug := cleanSlug(params.Slug)
	if slug == "" {
		return nil, fmt.Errorf("invalid goal slug %q: must contain alphanumeric characters and hyphens", params.Slug)
	}

	rootDir := resolveGoalsRootDir(params.RootDir)

	// Validate structured work package before any filesystem mutations
	if params.WorkPackage != nil {
		if len(params.Tasks) > 0 {
			return nil, fmt.Errorf("cannot provide both work_package and tasks")
		}
		if len(params.Invariants) > 0 {
			return nil, fmt.Errorf("cannot provide both work_package and invariants")
		}
		if params.WorkPackage.GoalSlug != slug {
			return nil, fmt.Errorf("work package goal_slug %q does not match requested slug %q", params.WorkPackage.GoalSlug, slug)
		}
		if err := ValidateWorkPackage(params.WorkPackage); err != nil {
			return nil, fmt.Errorf("invalid work package: %w", err)
		}
		if err := validatePackagePaths(rootDir, params.WorkPackage); err != nil {
			return nil, fmt.Errorf("invalid work package paths: %w", err)
		}
	}

	if params.SpecReview != nil {
		if params.WorkPackage == nil {
			return nil, fmt.Errorf("spec_review requires a work_package")
		}
		if diags := ValidateSpecReview(params.WorkPackage, *params.SpecReview); len(diags) > 0 {
			return nil, fmt.Errorf("invalid spec_review: %s: %s", diags[0].Code, diags[0].Message)
		}
	}

	// Run epistemic pre-flight sentry
	preflight, err := PreFlightGoalCheck(rootDir, params)
	if err != nil {
		return nil, fmt.Errorf("pre-flight check error: %w", err)
	}
	if !preflight.Valid {
		if len(preflight.Errors) > 0 {
			return nil, fmt.Errorf("pre-flight check failed: %s", strings.Join(preflight.Errors, "; "))
		}
		if params.Strict && (len(preflight.Warnings) > 0 || len(preflight.Collisions) > 0) {
			return nil, fmt.Errorf("strict pre-flight check failed: %s", strings.Join(preflight.Warnings, "; "))
		}
	}

	goalsDir := filepath.Join(rootDir, "goals")
	goalDir := filepath.Join(goalsDir, slug)

	if !params.Overwrite && fileExists(filepath.Join(goalDir, "goal.md")) {
		return nil, fmt.Errorf("goal %q already exists in goals/%s. Use --force to replace it", slug, slug)
	}

	newlyCreatedDir := false
	if !fileExists(goalDir) {
		newlyCreatedDir = true
	}
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		return nil, fmt.Errorf("create goal directory: %w", err)
	}
	var success bool
	defer func() {
		if !success && newlyCreatedDir {
			_ = os.RemoveAll(goalDir)
		}
	}()

	title := params.Title
	if title == "" {
		if params.WorkPackage != nil && params.WorkPackage.Purpose != "" {
			title = slugToTitle(slug)
		} else {
			title = slugToTitle(slug)
		}
	}
	desc := params.Description
	if desc == "" {
		if params.WorkPackage != nil && params.WorkPackage.Purpose != "" {
			desc = params.WorkPackage.Purpose
		} else {
			desc = "Implement " + title + " according to architectural invariants and done conditions."
		}
	}

	isIndep := true
	if params.Independent != nil {
		isIndep = *params.Independent
	} else if len(params.DependsOn) > 0 {
		isIndep = false
	}

	// Validate cycle prevention
	if len(params.DependsOn) > 0 {
		existingAudit, err := ScanGoals(rootDir)
		if err == nil && len(existingAudit.Goals) > 0 {
			graph := BuildDependencyGraphFromSlice(existingAudit.Goals)
			if err := graph.ValidateNoCycle(slug, params.DependsOn); err != nil {
				return nil, err
			}
		}
	}

	isImplemented := strings.EqualFold(params.Status, "implemented")
	commitSha := strings.TrimSpace(params.Commit)
	if isImplemented && commitSha == "" {
		if out, err := exec.Command("git", "-C", rootDir, "rev-parse", "--short", "HEAD").Output(); err == nil {
			commitSha = strings.TrimSpace(string(out))
		}
		if commitSha == "" {
			return nil, fmt.Errorf("commit SHA is required when creating an implemented retrospective goal (--commit=<sha>)")
		}
	}

	tmplData := BuildGoalTemplateData(params, slug, title, desc, isIndep, commitSha, isImplemented)
	shapeVal := tmplData.Shape
	tierVal := tmplData.Tier

	// 1. Write goal.md
	goalTmplPath, err := FindGoalTemplate(rootDir, params.TemplateDir, "goal.md")
	if err != nil {
		return nil, fmt.Errorf("find goal template: %w", err)
	}
	if goalTmplPath != "" {
		rendered, err := RenderTemplateFile(goalTmplPath, tmplData)
		if err != nil {
			return nil, fmt.Errorf("render goal template %s: %w", goalTmplPath, err)
		}
		if err := AtomicWriteFile(filepath.Join(goalDir, "goal.md"), []byte(rendered), 0644); err != nil {
			return nil, fmt.Errorf("write goal.md: %w", err)
		}
	} else {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Goal: %s\n\n", title))
		b.WriteString("## Goal Description\n")
		b.WriteString(desc + "\n\n")

		b.WriteString("## Dependencies & Execution Order\n")
		if isIndep {
			b.WriteString("- **Mode**: Independent (disjoint, parallelizable)\n")
		} else {
			b.WriteString("- **Mode**: Dependent (requires prerequisite goals)\n")
		}
		if len(params.DependsOn) > 0 {
			b.WriteString(fmt.Sprintf("- **Depends On**: %s\n", strings.Join(params.DependsOn, ", ")))
		} else {
			b.WriteString("- **Depends On**: none\n")
		}
		if params.Sequence > 0 {
			b.WriteString(fmt.Sprintf("- **Sequence**: %d\n", params.Sequence))
		} else {
			b.WriteString("- **Sequence**: -\n")
		}
		b.WriteString(fmt.Sprintf("- **Shape**: %s\n", shapeVal))
		b.WriteString(fmt.Sprintf("- **Tier**: %s\n", tierVal))
		b.WriteString(fmt.Sprintf("- **Memory Protocol**: %s\n", tmplData.MemoryProtocol))
		if len(params.Personas) > 0 {
			b.WriteString(fmt.Sprintf("- **Personas**: %s\n", strings.Join(params.Personas, ", ")))
		}
		if isImplemented {
			b.WriteString("- **Status**: implemented\n")
			b.WriteString(fmt.Sprintf("- **Commit**: %s\n", commitSha))
		}
		b.WriteString("\n")

		if len(params.FilesToTouch) > 0 {
			b.WriteString("## Files to Touch\n")
			for _, f := range params.FilesToTouch {
				b.WriteString(fmt.Sprintf("- %s\n", NormalizeGoalPath(f)))
			}
			b.WriteString("\n")
		}

		b.WriteString("## References\n")
		if params.WorkPackage != nil {
			b.WriteString(fmt.Sprintf("- **Execution Contract**: [`goals/%s/work-package.json`](work-package.json)\n", slug))
		}
		b.WriteString(fmt.Sprintf("- **Shared Understanding & Fact Sheet**: [`goals/%s/facts.md`](facts.md)\n", slug))
		b.WriteString(fmt.Sprintf("- **Execution Plan**: [`goals/%s/plan.md`](plan.md)\n\n", slug))

		if tmplData.MemoryProtocol == "required" {
			b.WriteString("## Memory Plan\n")
			b.WriteString("- **Prime**: run `sila prime` (CLI) or `prime_context` (MCP) at task start and record the briefing timestamp.\n")
			b.WriteString("- **Recall**: run `sila recall \"<symptom or design question>\"` before implementation; record each query and match count.\n")
			b.WriteString("- **Writes**: record at least one `memo`, `lesson`, or `case` grounded to a file in this goal; use a `case` when the goal introduces a reusable mechanism.\n")
			b.WriteString("- **Facts**: run `sila facts verify` before certification and record the exit code and violation delta.\n")
			b.WriteString("- **Handoff**: run `sila handoff \"<summary>\" --open=\"...\"` at session end and record the handoff ID.\n")
			b.WriteString("- **Evidence**: embed a `sila/memory-writes/v1` block under `## Memory Writes`, per `memory-protocol-goal-contract`.\n\n")
		}

		b.WriteString("## Done Condition\n")
		b.WriteString("1. All tasks in `plan.md` are checked (`- [x]`); unchecked tasks block the `implemented` transition.\n")
		b.WriteString(fmt.Sprintf("2. Implementation is committed with `(goals/%s/goal.md)` or `[goal: %s]` attribution under a `feat:`, `fix:`, `refactor:`, `perf:`, or `chore:` prefix.\n", slug, slug))
		b.WriteString(fmt.Sprintf("3. Goal is certified (`goals/%s/certification.json` with `verified: true` or a signed `audit-receipt.json`) and any markdown evidence contains an explicit standalone `Verdict: PASS` declaration.\n", slug))
		b.WriteString("4. `sila goals --json` reports `status: \"implemented\"`, `verification_status: \"verified\"`, and `unverified_count: 0` for delivered goals, with a clean `git status`.\n")

		if err := AtomicWriteFile(filepath.Join(goalDir, "goal.md"), []byte(b.String()), 0644); err != nil {
			return nil, fmt.Errorf("write goal.md: %w", err)
		}
	}

	// 2. Write facts.md
	if params.WorkPackage != nil {
		var fb strings.Builder
		fb.WriteString(fmt.Sprintf("# Facts: %s\n\n", title))
		fb.WriteString("## Purpose & Architectural Intent\n")
		fb.WriteString(params.WorkPackage.Purpose + "\n\n")
		if len(params.WorkPackage.Constraints) > 0 {
			fb.WriteString("## Architectural Invariants & Constraints\n")
			for _, c := range params.WorkPackage.Constraints {
				fb.WriteString(fmt.Sprintf("- %s\n", c))
			}
			fb.WriteString("\n")
		}
		if len(params.WorkPackage.NonGoals) > 0 {
			fb.WriteString("## Non-Goals\n")
			for _, ng := range params.WorkPackage.NonGoals {
				fb.WriteString(fmt.Sprintf("- %s\n", ng))
			}
			fb.WriteString("\n")
		}
		if err := AtomicWriteFile(filepath.Join(goalDir, "facts.md"), []byte(fb.String()), 0644); err != nil {
			return nil, fmt.Errorf("write facts.md: %w", err)
		}
	} else if !fileExists(filepath.Join(goalDir, "facts.md")) || params.Overwrite {
		factsTmplPath, err := FindGoalTemplate(rootDir, params.TemplateDir, "facts.md")
		if err != nil {
			return nil, fmt.Errorf("find facts template: %w", err)
		}
		if factsTmplPath != "" {
			rendered, err := RenderTemplateFile(factsTmplPath, tmplData)
			if err != nil {
				return nil, fmt.Errorf("render facts template %s: %w", factsTmplPath, err)
			}
			if err := AtomicWriteFile(filepath.Join(goalDir, "facts.md"), []byte(rendered), 0644); err != nil {
				return nil, fmt.Errorf("write facts.md from template: %w", err)
			}
		} else {
			var fb strings.Builder
			fb.WriteString(fmt.Sprintf("# Facts: %s\n\n", title))
			fb.WriteString("## Architectural Invariants & Constraints\n")
			if len(params.Invariants) > 0 {
				for _, inv := range params.Invariants {
					inv = strings.TrimSpace(inv)
					if inv == "" {
						continue
					}
					if !strings.HasPrefix(inv, "- ") && !strings.HasPrefix(inv, "* ") {
						inv = "- " + inv
					}
					fb.WriteString(inv + "\n")
				}
			} else {
				fb.WriteString("- Ground all implementations in official repository standards.\n")
				fb.WriteString("- Ensure deterministic verification before closing goal.\n")
			}
			fb.WriteString("\n## File & Interface Contracts\n")
			if len(params.FilesToTouch) > 0 {
				fb.WriteString("- Relevant files:\n")
				for _, f := range params.FilesToTouch {
					fb.WriteString(fmt.Sprintf("  - `%s`\n", NormalizeGoalPath(f)))
				}
			} else {
				fb.WriteString("- Relevant files: (to be documented during planning)\n")
			}
			if err := AtomicWriteFile(filepath.Join(goalDir, "facts.md"), []byte(fb.String()), 0644); err != nil {
				return nil, fmt.Errorf("write facts.md: %w", err)
			}
		}
	}

	// 3. Write plan.md
	if params.WorkPackage != nil {
		var pb strings.Builder
		pb.WriteString(fmt.Sprintf("# Plan: %s\n\n", title))
		pb.WriteString("## Execution tasks\n")
		for _, t := range params.WorkPackage.Tasks {
			casesStr := strings.Join(t.AcceptanceIDs, ", ")
			if casesStr != "" {
				pb.WriteString(fmt.Sprintf("- [ ] **%s: %s** ([task contract](tasks/%s.md); %s).\n", t.ID, t.Title, t.ID, casesStr))
			} else {
				pb.WriteString(fmt.Sprintf("- [ ] **%s: %s**.\n", t.ID, t.Title))
			}
		}
		if err := AtomicWriteFile(filepath.Join(goalDir, "plan.md"), []byte(pb.String()), 0644); err != nil {
			return nil, fmt.Errorf("write plan.md: %w", err)
		}
	} else if !fileExists(filepath.Join(goalDir, "plan.md")) || params.Overwrite {
		planTmplPath, err := FindGoalTemplate(rootDir, params.TemplateDir, "plan.md")
		if err != nil {
			return nil, fmt.Errorf("find plan template: %w", err)
		}
		if planTmplPath != "" {
			rendered, err := RenderTemplateFile(planTmplPath, tmplData)
			if err != nil {
				return nil, fmt.Errorf("render plan template %s: %w", planTmplPath, err)
			}
			if err := AtomicWriteFile(filepath.Join(goalDir, "plan.md"), []byte(rendered), 0644); err != nil {
				return nil, fmt.Errorf("write plan.md from template: %w", err)
			}
		} else {
			var pb strings.Builder
			pb.WriteString(fmt.Sprintf("# Plan: %s\n\n", title))
			pb.WriteString("## Execution Steps\n")
			box := "- [ ]"
			if isImplemented {
				box = "- [x]"
			}
			if len(params.Tasks) > 0 {
				for i, task := range params.Tasks {
					task = strings.TrimSpace(task)
					if task == "" {
						continue
					}
					if strings.HasPrefix(task, "- [ ]") || strings.HasPrefix(task, "* [ ]") {
						pb.WriteString(box + task[5:] + "\n")
					} else if strings.HasPrefix(task, "- [x]") || strings.HasPrefix(task, "- [X]") ||
						strings.HasPrefix(task, "* [x]") || strings.HasPrefix(task, "* [X]") {
						pb.WriteString(task + "\n")
					} else {
						if strings.HasPrefix(task, "- ") || strings.HasPrefix(task, "* ") {
							task = strings.TrimSpace(task[2:])
						}
						lower := strings.ToLower(task)
						if strings.HasPrefix(lower, "phase ") || strings.HasPrefix(lower, "**phase") ||
							strings.HasPrefix(lower, "step ") || strings.HasPrefix(lower, "**step") {
							pb.WriteString(fmt.Sprintf("%s %s\n", box, task))
						} else {
							pb.WriteString(fmt.Sprintf("%s **Phase %d**: %s\n", box, i+1, task))
						}
					}
				}
			} else {
				isScout := strings.EqualFold(strings.TrimSpace(params.Shape), "scout")
				if isImplemented {
					if isScout {
						pb.WriteString("- [x] **Phase 1 — Discovery & Reconnaissance**: Investigate target systems, constraints, and dependencies.\n")
						pb.WriteString("- [x] **Phase 2 — Analysis & Evidence Gathering**: Collect empirical facts, benchmark outputs, and audit findings.\n")
						pb.WriteString("- [x] **Phase 3 — Documentation & Synthesis**: Synthesize findings into actionable specs, guides, or reports.\n")
						pb.WriteString(fmt.Sprintf("- [x] **Phase 4 — Attribution Commit**: Completed in commit %s.\n", commitSha))
					} else {
						pb.WriteString("- [x] **Phase 1 — Contracts & Schemas**: Define models, data structures, and signatures.\n")
						pb.WriteString("- [x] **Phase 2 — Core Implementation**: Implement business logic, routes, and error handling.\n")
						pb.WriteString("- [x] **Phase 3 — Verification & Tests**: Write unit tests and run end-to-end verification.\n")
						pb.WriteString(fmt.Sprintf("- [x] **Phase 4 — Attribution Commit**: Completed in commit %s.\n", commitSha))
					}
				} else {
					if isScout {
						pb.WriteString("- [ ] **Phase 1 — Discovery & Reconnaissance**: Investigate target systems, constraints, and dependencies.\n")
						pb.WriteString("- [ ] **Phase 2 — Analysis & Evidence Gathering**: Collect empirical facts, benchmark outputs, and audit findings.\n")
						pb.WriteString("- [ ] **Phase 3 — Documentation & Evidence**: Synthesize findings into actionable specs, guides, or reports; any markdown evidence file (e.g. `goals/" + slug + "/evidence.md`) MUST include an explicit standalone `Verdict: PASS` declaration (or `## Verdict` followed by `PASS`) to satisfy `EVIDENCE_VERDICT_REQUIRED`.\n")
						pb.WriteString("- [ ] **Phase 4 — Attribution & Registry Closure**: (1) check all completed tasks in `goals/" + slug + "/plan.md`; (2) commit with `(goals/" + slug + "/goal.md)` or `[goal: " + slug + "]` using a `feat:`, `fix:`, `refactor:`, `perf:`, or `chore:` subject (docs/test commits are ignored); (3) certify via `sila goals certify " + slug + " --evidence=goals/" + slug + "/evidence.md` (with `Verdict: PASS`) or `sila goals audit " + slug + "`; (4) reconcile via `sila goals` and commit the updated `goals/README.md` and certification receipts; verify `sila goals --json` reports `unverified_count: 0` for delivered goals.\n")
					} else {
						pb.WriteString("- [ ] **Phase 1 — Contracts & Schemas**: Define models, data structures, and signatures.\n")
						pb.WriteString("- [ ] **Phase 2 — Core Implementation**: Implement business logic, routes, and error handling.\n")
						pb.WriteString("- [ ] **Phase 3 — Verification & Evidence**: Write unit tests, run end-to-end verification, and write markdown evidence with an explicit standalone `Verdict: PASS` declaration (or `## Verdict` followed by `PASS`) to satisfy `EVIDENCE_VERDICT_REQUIRED`.\n")
						pb.WriteString("- [ ] **Phase 4 — Attribution & Registry Closure**: (1) check all completed tasks in `goals/" + slug + "/plan.md`; (2) commit with `(goals/" + slug + "/goal.md)` or `[goal: " + slug + "]` using a `feat:`, `fix:`, `refactor:`, `perf:`, or `chore:` subject (docs/test commits are ignored); (3) certify via `sila goals certify " + slug + " --evidence=goals/" + slug + "/evidence.md` (with `Verdict: PASS`) or `sila goals audit " + slug + "`; (4) reconcile via `sila goals` and commit the updated `goals/README.md` and certification receipts; verify `sila goals --json` reports `unverified_count: 0` for delivered goals.\n")
					}
				}
			}
			if err := AtomicWriteFile(filepath.Join(goalDir, "plan.md"), []byte(pb.String()), 0644); err != nil {
				return nil, fmt.Errorf("write plan.md: %w", err)
			}
		}
	}

	// 4. Persist work-package.json if structured package provided
	if params.WorkPackage != nil {
		if err := SaveWorkPackage(rootDir, slug, params.WorkPackage); err != nil {
			return nil, fmt.Errorf("save work package: %w", err)
		}
	}

	if params.SpecReview != nil {
		if err := SaveSpecReview(rootDir, slug, *params.SpecReview); err != nil {
			return nil, fmt.Errorf("save spec review: %w", err)
		}
	}

	// 4. If retrospective goal, persist retrospective.json record
	if isImplemented {
		retro := RetrospectiveRecord{
			GoalSlug:   slug,
			Status:     "implemented",
			Commit:     commitSha,
			RecordedAt: time.Now().UTC(),
		}
		if data, err := json.MarshalIndent(retro, "", "  "); err == nil {
			_ = AtomicWriteFile(filepath.Join(goalDir, "retrospective.json"), data, 0644)
		}
	}

	// Auto-sync README
	audit, err := ScanGoals(rootDir)
	if err == nil {
		_ = SyncReadme(rootDir, audit)
	}

	tasksTotal := 4
	tasksMet := 0
	if parsedTasks, err := GetGoalTasks(rootDir, slug); err == nil && len(parsedTasks) > 0 {
		tasksTotal = len(parsedTasks)
		for _, pt := range parsedTasks {
			if pt.Completed {
				tasksMet++
			}
		}
	} else if len(params.Tasks) > 0 {
		tasksTotal = 0
		for _, t := range params.Tasks {
			if strings.TrimSpace(t) != "" {
				tasksTotal++
			}
		}
	}

	goalStatus := "pending"
	taskRef := ""
	if isImplemented {
		goalStatus = "implemented"
		taskRef = "Retrospective"
	}

	success = true
	return &Goal{
		Slug:           slug,
		Title:          title,
		Description:    desc,
		Status:         goalStatus,
		Commit:         commitSha,
		Task:           taskRef,
		Independent:    isIndep,
		DependsOn:      params.DependsOn,
		Sequence:       params.Sequence,
		FilesToTouch:   params.FilesToTouch,
		TasksTotal:     tasksTotal,
		TasksMet:       tasksMet,
		HasGoalMd:      true,
		HasPlanMd:      true,
		HasFactsMd:     true,
		LaunchCmd:      fmt.Sprintf("/goal goals/%s/goal.md", slug),
		Shape:          shapeVal,
		Tier:           tierVal,
		MemoryProtocol: tmplData.MemoryProtocol,
		Personas:       params.Personas,
		PreFlight:      preflight,
	}, nil
}

// UpdateGoalParams contains options for modifying an existing goal package.
type UpdateGoalParams struct {
	RootDir       string      `json:"root_dir,omitempty"`
	Slug          string      `json:"slug"`
	Title         *string     `json:"title,omitempty"`
	Description   *string     `json:"description,omitempty"`
	DependsOn     []string    `json:"depends_on,omitempty"`
	AddDeps       []string    `json:"add_deps,omitempty"`
	RemoveDeps    []string    `json:"remove_deps,omitempty"`
	Independent   *bool       `json:"independent,omitempty"`
	Sequence      *int        `json:"sequence,omitempty"`
	FilesToTouch  []string    `json:"files_to_touch,omitempty"`
	Shape         *string     `json:"shape,omitempty"`
	Tier          *string     `json:"tier,omitempty"`
	Personas      []string    `json:"personas,omitempty"`
	AddPersonas   []string    `json:"add_personas,omitempty"`
	ClearPersonas bool        `json:"clear_personas,omitempty"`
	Strict        bool        `json:"strict,omitempty"`
	SpecReview    *SpecReview `json:"spec_review,omitempty"`
}

func sliceContains(list []string, item string) bool {
	return slices.Contains(list, item)
}

// UpdateGoal updates an existing goal package's metadata, dependencies, and files.
func UpdateGoal(params UpdateGoalParams) (*Goal, error) {
	if params.Slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	slug := cleanSlug(params.Slug)
	if slug == "" {
		return nil, fmt.Errorf("invalid goal slug %q", params.Slug)
	}

	rootDir := resolveGoalsRootDir(params.RootDir)
	goalPath := filepath.Join(rootDir, "goals", slug, "goal.md")
	if !fileExists(goalPath) {
		return nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}

	data, err := os.ReadFile(goalPath)
	if err != nil {
		return nil, fmt.Errorf("read goal.md: %w", err)
	}
	content := string(data)

	currTitle, currDesc, currIndep, currDeps, currSeq, currFiles, currShape, currTier, currPersonas, _ := parseGoalMetadataFull(content)

	// Resolve new dependencies
	var newDeps []string
	depsModified := false
	if params.DependsOn != nil {
		depsModified = true
		for _, d := range params.DependsOn {
			c := cleanDepSlug(d)
			if c != "" && !strings.EqualFold(c, "none") && !strings.EqualFold(c, "nil") && !strings.EqualFold(c, "-") {
				if c == slug {
					return nil, fmt.Errorf("goal cannot depend on itself: %s", slug)
				}
				if !sliceContains(newDeps, c) {
					newDeps = append(newDeps, c)
				}
			}
		}
	} else {
		newDeps = append([]string(nil), currDeps...)
	}

	if len(params.AddDeps) > 0 {
		depsModified = true
		for _, d := range params.AddDeps {
			c := cleanDepSlug(d)
			if c != "" && !strings.EqualFold(c, "none") && !strings.EqualFold(c, "nil") && !strings.EqualFold(c, "-") {
				if c == slug {
					return nil, fmt.Errorf("goal cannot depend on itself: %s", slug)
				}
				if !sliceContains(newDeps, c) {
					newDeps = append(newDeps, c)
				}
			}
		}
	}

	if len(params.RemoveDeps) > 0 {
		depsModified = true
		for _, d := range params.RemoveDeps {
			c := cleanDepSlug(d)
			if c != "" {
				var filtered []string
				for _, existing := range newDeps {
					if existing != c {
						filtered = append(filtered, existing)
					}
				}
				newDeps = filtered
			}
		}
	}

	// Resolve independent
	newIndep := currIndep
	if params.Independent != nil {
		newIndep = *params.Independent
		if newIndep && !depsModified {
			newDeps = nil
		}
	} else if depsModified {
		if len(newDeps) > 0 {
			newIndep = false
		} else {
			newIndep = true
		}
	}

	// Cycle validation
	if len(newDeps) > 0 {
		if slices.Contains(newDeps, slug) {
			return nil, fmt.Errorf("goal cannot depend on itself: %s", slug)
		}
		existingAudit, err := ScanGoals(rootDir)
		if err == nil && len(existingAudit.Goals) > 0 {
			graph := BuildDependencyGraphFromSlice(existingAudit.Goals)
			if err := graph.ValidateNoCycle(slug, newDeps); err != nil {
				return nil, err
			}
		}
	}

	// Resolve other fields
	newTitle := currTitle
	hasTitle := false
	if params.Title != nil && *params.Title != "" {
		newTitle = *params.Title
		hasTitle = true
	}

	newDesc := currDesc
	hasDesc := false
	if params.Description != nil && *params.Description != "" {
		newDesc = *params.Description
		hasDesc = true
	}

	newSeq := currSeq
	hasSeq := false
	if params.Sequence != nil {
		newSeq = *params.Sequence
		hasSeq = true
	}

	newShape := currShape
	hasShape := false
	if params.Shape != nil && *params.Shape != "" {
		newShape = *params.Shape
		hasShape = true
	}

	newTier := currTier
	hasTier := false
	if params.Tier != nil && *params.Tier != "" {
		newTier = NormalizeTier(*params.Tier)
		hasTier = true
	}

	newFiles := currFiles
	hasFiles := false
	if params.FilesToTouch != nil {
		newFiles = nil
		for _, f := range params.FilesToTouch {
			if clean := NormalizeGoalPath(f); clean != "" {
				newFiles = append(newFiles, clean)
			}
		}
		hasFiles = true
	}

	var newPersonas []string
	personasModified := false
	if params.ClearPersonas {
		personasModified = true
		newPersonas = nil
	} else if params.Personas != nil {
		personasModified = true
		for _, p := range params.Personas {
			c := strings.TrimSpace(p)
			if c != "" && !strings.EqualFold(c, "none") && !strings.EqualFold(c, "nil") && !strings.EqualFold(c, "-") {
				if !sliceContains(newPersonas, c) {
					newPersonas = append(newPersonas, c)
				}
			}
		}
	} else {
		newPersonas = append([]string(nil), currPersonas...)
	}

	if len(params.AddPersonas) > 0 {
		personasModified = true
		for _, p := range params.AddPersonas {
			c := strings.TrimSpace(p)
			if c != "" && !strings.EqualFold(c, "none") && !strings.EqualFold(c, "nil") && !strings.EqualFold(c, "-") {
				if !sliceContains(newPersonas, c) {
					newPersonas = append(newPersonas, c)
				}
			}
		}
	}

	// Pre-flight validation against the current roadmap state.
	preflight, err := PreFlightGoalCheck(rootDir, CreateGoalParams{
		RootDir:      rootDir,
		Slug:         slug,
		Title:        newTitle,
		Description:  newDesc,
		DependsOn:    newDeps,
		Independent:  &newIndep,
		Sequence:     newSeq,
		FilesToTouch: newFiles,
		Shape:        newShape,
		Tier:         newTier,
		Overwrite:    true,
		Strict:       params.Strict,
	})
	if err != nil {
		return nil, fmt.Errorf("pre-flight check error: %w", err)
	}
	if len(preflight.Errors) > 0 {
		return nil, fmt.Errorf("pre-flight check failed: %s", strings.Join(preflight.Errors, "; "))
	}
	if params.Strict {
		var issues []string
		issues = append(issues, preflight.Warnings...)
		for _, c := range preflight.Collisions {
			issues = append(issues, fmt.Sprintf("file %s collides with active goal %q", c.File, c.ConflictingGoal))
		}
		if len(issues) > 0 {
			return nil, fmt.Errorf("strict pre-flight check failed: %s", strings.Join(issues, "; "))
		}
	}

	var updateReview *SpecReview
	if params.SpecReview != nil {
		wp, err := LoadWorkPackage(rootDir, slug)
		if err != nil || wp == nil {
			return nil, fmt.Errorf("spec_review requires an existing work-package.json")
		}
		if diags := ValidateSpecReview(wp, *params.SpecReview); len(diags) > 0 {
			return nil, fmt.Errorf("invalid spec_review: %s: %s", diags[0].Code, diags[0].Message)
		}
		updateReview = params.SpecReview
	}

	updatedContent := updateGoalContent(content, newTitle, hasTitle, newDesc, hasDesc, newIndep, newDeps, newSeq, hasSeq, newShape, hasShape, newFiles, hasFiles, newTier, hasTier, newPersonas, personasModified)

	if err := AtomicWriteFile(goalPath, []byte(updatedContent), 0644); err != nil {
		return nil, fmt.Errorf("write updated goal.md: %w", err)
	}

	if updateReview != nil {
		if err := SaveSpecReview(rootDir, slug, *updateReview); err != nil {
			return nil, fmt.Errorf("save spec review: %w", err)
		}
	}

	if hasFiles {
		if err := syncClaimAndCharterForUpdate(rootDir, slug, newFiles); err != nil {
			return nil, err
		}
	}

	// Auto-sync README
	audit, err := ScanGoals(rootDir)
	if err == nil {
		_ = SyncReadme(rootDir, audit)
		for _, g := range audit.Goals {
			if g.Slug == slug {
				g.PreFlight = preflight
				return &g, nil
			}
		}
	}

	return &Goal{
		Slug:         slug,
		Title:        newTitle,
		Description:  newDesc,
		Independent:  newIndep,
		DependsOn:    newDeps,
		Sequence:     newSeq,
		Shape:        newShape,
		Tier:         newTier,
		FilesToTouch: newFiles,
		Personas:     newPersonas,
		PreFlight:    preflight,
	}, nil
}

// syncClaimAndCharterForUpdate re-evaluates charter boundaries and persists the
// new FilesToTouch set on an active claim and charter.
func syncClaimAndCharterForUpdate(rootDir, slug string, files []string) error {
	claimPath := filepath.Join(rootDir, "goals", slug, "claim.json")
	claim, err := readClaim(claimPath)
	if err != nil || claim == nil {
		return nil
	}

	normalized := make([]string, 0, len(files))
	for _, f := range files {
		if n := NormalizeGoalPath(f); n != "" {
			normalized = append(normalized, n)
		}
	}

	charter, err := LoadCharter(rootDir, slug)
	if err != nil || charter == nil {
		created, cerr := CreateCharter(rootDir, slug, claim.Agent, claim.SessionID, normalized, nil, nil, 0)
		if cerr != nil {
			return fmt.Errorf("create charter for update: %w", cerr)
		}
		charter = created
	}

	allow := append([]string(nil), normalized...)
	allow = append(allow, filepath.Join("goals", slug)+"/*")
	candidate := *charter
	candidate.AllowPaths = allow
	if err := EvaluateCharterCollisions(rootDir, &candidate, false); err != nil {
		return err
	}
	charter.AllowPaths = allow
	if err := SaveCharter(rootDir, charter); err != nil {
		return fmt.Errorf("save charter: %w", err)
	}

	claim.FilesToTouch = normalized
	claim.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal claim.json: %w", err)
	}
	if err := AtomicWriteFile(claimPath, data, 0644); err != nil {
		return fmt.Errorf("write claim.json: %w", err)
	}
	return nil
}

var standardDepKeyRE = regexp.MustCompile(`(?i)^\s*[-*]?\s*(?:\*\*)?(mode|independent|depends\s*on|depends_on|dependencies|prerequisites|sequence|seq|order|shape|tier|personas|persona|review\s*squad|review_squad|reviewers|squad)(?:\*\*)?\s*:\s*(.*)$`)

// extractCustomDepSectionLines extracts lines from an existing Dependencies section that are
// not managed by standard Sila goal fields, preserving custom metadata (Priority, Owner, Status, etc.).
func extractCustomDepSectionLines(sectionText string) []string {
	var customLines []string
	lines := strings.Split(sectionText, "\n")
	inStandardList := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if inStandardList {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			inStandardList = false
		}

		if m := standardDepKeyRE.FindStringSubmatch(line); len(m) > 1 {
			val := strings.TrimSpace(m[2])
			if val == "" {
				inStandardList = true
			}
			continue
		}

		customLines = append(customLines, line)
	}

	return customLines
}

func updateGoalContent(content string, newTitle string, hasTitle bool, newDesc string, hasDesc bool, newIndep bool, newDeps []string, newSeq int, hasSeq bool, newShape string, hasShape bool, newFiles []string, hasFiles bool, newTier string, hasTier bool, newPersonas []string, personasModified bool) string {
	body := content
	fmPrefix := ""

	if m := frontmatterRE.FindStringSubmatch(content); len(m) > 1 {
		fm := m[1]
		body = content[len(m[0]):]

		fmLines := strings.Split(fm, "\n")
		var outFmLines []string
		inDepsList := false
		inTouchList := false
		inPersonasList := false

		for _, line := range fmLines {
			trimmed := strings.TrimSpace(line)
			if inDepsList {
				trimmedCommentless := stripInlineComment(trimmed)
				if strings.HasPrefix(trimmedCommentless, "-") {
					continue
				}
				inDepsList = false
			}
			if inTouchList {
				trimmedCommentless := stripInlineComment(trimmed)
				if strings.HasPrefix(trimmedCommentless, "-") {
					continue
				}
				inTouchList = false
			}
			if inPersonasList {
				trimmedCommentless := stripInlineComment(trimmed)
				if strings.HasPrefix(trimmedCommentless, "-") {
					continue
				}
				inPersonasList = false
			}

			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 && !strings.HasPrefix(trimmed, "#") {
				k := strings.ToLower(strings.TrimSpace(parts[0]))
				switch k {
				case "title":
					if hasTitle {
						outFmLines = append(outFmLines, fmt.Sprintf("title: %q", newTitle))
						continue
					}
				case "description", "desc", "overview", "summary":
					if hasDesc {
						outFmLines = append(outFmLines, fmt.Sprintf("description: %q", newDesc))
						continue
					}
				case "depends_on", "dependencies", "prerequisites":
					vClean := stripInlineComment(strings.TrimSpace(parts[1]))
					if vClean == "" {
						inDepsList = true
					}
					if len(newDeps) > 0 {
						outFmLines = append(outFmLines, fmt.Sprintf("depends_on: [%s]", strings.Join(newDeps, ", ")))
					} else {
						outFmLines = append(outFmLines, "depends_on: none")
					}
					continue
				case "independent", "mode":
					if newIndep {
						outFmLines = append(outFmLines, "independent: true")
					} else {
						outFmLines = append(outFmLines, "independent: false")
					}
					continue
				case "sequence", "seq", "order":
					if hasSeq {
						if newSeq > 0 {
							outFmLines = append(outFmLines, fmt.Sprintf("sequence: %d", newSeq))
						} else {
							outFmLines = append(outFmLines, "sequence: -")
						}
						continue
					}
				case "shape":
					if hasShape {
						outFmLines = append(outFmLines, fmt.Sprintf("shape: %s", newShape))
						continue
					}
				case "tier", "priority":
					if hasTier {
						outFmLines = append(outFmLines, fmt.Sprintf("tier: %s", newTier))
						continue
					}
				case "files_to_touch", "files", "touch", "touch_files":
					if hasFiles {
						vClean := stripInlineComment(strings.TrimSpace(parts[1]))
						if vClean == "" {
							inTouchList = true
						}
						if len(newFiles) > 0 {
							outFmLines = append(outFmLines, fmt.Sprintf("files_to_touch: [%s]", strings.Join(newFiles, ", ")))
						} else {
							outFmLines = append(outFmLines, "files_to_touch: []")
						}
						continue
					}
				case "personas", "persona", "review_squad", "reviewers", "squad":
					if personasModified {
						vClean := stripInlineComment(strings.TrimSpace(parts[1]))
						if vClean == "" {
							inPersonasList = true
						}
						if len(newPersonas) > 0 {
							outFmLines = append(outFmLines, fmt.Sprintf("personas: [%s]", strings.Join(newPersonas, ", ")))
						}
						continue
					}
				}
			}
			outFmLines = append(outFmLines, line)
		}
		fmPrefix = "---\n" + strings.Join(outFmLines, "\n") + "\n---\n"
	}

	// Update Title in markdown body
	h1RE := regexp.MustCompile(`(?m)^#[ \t]+(?:Goal:[ \t]*)?[^\r\n]+$`)
	if hasTitle {
		if loc := h1RE.FindStringIndex(body); loc != nil {
			body = body[:loc[0]] + "# Goal: " + newTitle + body[loc[1]:]
		}
	}

	// Update Description in markdown body
	if hasDesc {
		descHdrRE := regexp.MustCompile(`(?im)^##+[ \t]*(?:Goal\s*Description|Description|Overview|Summary)\b.*$`)
		if loc := descHdrRE.FindStringIndex(body); loc != nil {
			start := loc[0]
			afterHdr := body[loc[1]:]
			nextSecRE := regexp.MustCompile(`(?m)^\r?\n##+ `)
			replacement := "## Goal Description\n" + newDesc + "\n"
			if endLoc := nextSecRE.FindStringIndex(afterHdr); endLoc != nil {
				end := loc[1] + endLoc[0]
				body = body[:start] + replacement + body[end:]
			} else {
				body = body[:start] + replacement
			}
		}
	}

	// Format Dependencies & Execution Order block
	modeStr := "Independent (disjoint, parallelizable)"
	if !newIndep {
		modeStr = "Dependent (requires prerequisite goals)"
	}
	depsStr := "none"
	if len(newDeps) > 0 {
		depsStr = strings.Join(newDeps, ", ")
	}
	seqStr := "-"
	if newSeq > 0 {
		seqStr = fmt.Sprintf("%d", newSeq)
	}
	shapeStr := newShape
	if shapeStr == "" {
		shapeStr = "ship"
	}
	tierStr := newTier
	if tierStr == "" {
		tierStr = "roadmap"
	}

	depBlock := fmt.Sprintf("## Dependencies & Execution Order\n- **Mode**: %s\n- **Depends On**: %s\n- **Sequence**: %s\n- **Shape**: %s\n- **Tier**: %s\n",
		modeStr, depsStr, seqStr, shapeStr, tierStr)
	if len(newPersonas) > 0 {
		depBlock += fmt.Sprintf("- **Personas**: %s\n", strings.Join(newPersonas, ", "))
	}

	if personasModified && len(newPersonas) == 0 {
		if loc := personaSectionRE.FindStringIndex(body); loc != nil {
			start := loc[0]
			afterHeader := body[loc[1]:]
			nextSecRE := regexp.MustCompile(`(?m)^\r?\n##+ `)
			if endLoc := nextSecRE.FindStringIndex(afterHeader); endLoc != nil {
				end := loc[1] + endLoc[0]
				body = body[:start] + body[end:]
			} else {
				body = body[:start]
			}
		}
	}

	depSectionHeaderRE := regexp.MustCompile(`(?im)^##+[ \t]*(?:Dependencies\s*&\s*Execution\s*Order|Dependencies|Prerequisites)\b.*$`)
	if loc := depSectionHeaderRE.FindStringIndex(body); loc != nil {
		start := loc[0]
		afterHeader := body[loc[1]:]
		nextSecRE := regexp.MustCompile(`(?m)^\r?\n##+ `)
		var customLines []string
		if endLoc := nextSecRE.FindStringIndex(afterHeader); endLoc != nil {
			end := loc[1] + endLoc[0]
			secBody := afterHeader[:endLoc[0]]
			customLines = extractCustomDepSectionLines(secBody)
			depBlockWithCustom := depBlock
			for _, cl := range customLines {
				depBlockWithCustom += cl + "\n"
			}
			body = body[:start] + depBlockWithCustom + body[end:]
		} else {
			customLines = extractCustomDepSectionLines(afterHeader)
			depBlockWithCustom := depBlock
			for _, cl := range customLines {
				depBlockWithCustom += cl + "\n"
			}
			body = body[:start] + depBlockWithCustom
		}
	} else if fmPrefix == "" {
		descHdrRE := regexp.MustCompile(`(?im)^##+[ \t]*(?:Goal\s*Description|Description|Overview|Summary)\b.*$`)
		if descLoc := descHdrRE.FindStringIndex(body); descLoc != nil {
			afterDesc := body[descLoc[1]:]
			nextSecRE := regexp.MustCompile(`(?m)^\r?\n##+ `)
			if endLoc := nextSecRE.FindStringIndex(afterDesc); endLoc != nil {
				insertAt := descLoc[1] + endLoc[0]
				body = body[:insertAt] + "\n" + depBlock + body[insertAt:]
			} else {
				body = body + "\n\n" + depBlock
			}
		} else {
			body = body + "\n\n" + depBlock
		}
	}

	// Handle Files to Touch if updated
	if hasFiles {
		var touchBlock string
		if len(newFiles) > 0 {
			var tb strings.Builder
			tb.WriteString("## Files to Touch\n")
			for _, f := range newFiles {
				tb.WriteString(fmt.Sprintf("- %s\n", f))
			}
			touchBlock = tb.String()
		}

		if loc := touchSectionRE.FindStringIndex(body); loc != nil {
			start := loc[0]
			afterHeader := body[loc[1]:]
			nextSecRE := regexp.MustCompile(`(?m)^\r?\n##+ `)
			if endLoc := nextSecRE.FindStringIndex(afterHeader); endLoc != nil {
				end := loc[1] + endLoc[0]
				if touchBlock != "" {
					body = body[:start] + touchBlock + body[end:]
				} else {
					body = body[:start] + body[end:]
				}
			} else {
				if touchBlock != "" {
					body = body[:start] + touchBlock
				} else {
					body = body[:start]
				}
			}
		} else if touchBlock != "" {
			// Insert before References or Done Condition
			refRE := regexp.MustCompile(`(?im)^##+[ \t]*(?:References|Done\s*Condition)\b`)
			if rloc := refRE.FindStringIndex(body); rloc != nil {
				body = body[:rloc[0]] + touchBlock + "\n" + body[rloc[0]:]
			} else {
				body = body + "\n" + touchBlock
			}
		}
	}

	return fmPrefix + body
}

// ClaimOptions configures charter boundaries and lease timeouts for a goal claim.
type ClaimOptions struct {
	AllowPaths     []string
	DenyPaths      []string
	RequiredChecks []string
	MaxDuration    time.Duration
}

// ClaimGoal claims and locks a goal for a specific agent.
func ClaimGoal(rootDir, slug, agent, note string, force bool) (*GoalClaim, error) {
	cl, _, err := claimGoal(rootDir, slug, agent, note, "", force)
	return cl, err
}

// ClaimGoalWithSession claims a goal and records the caller's session id on the
// claim so a dashboard can read the binding back deterministically.
func ClaimGoalWithSession(rootDir, slug, agent, note, session string, force bool) (*GoalClaim, error) {
	cl, _, err := claimGoal(rootDir, slug, agent, note, session, force)
	return cl, err
}

// ClaimGoalWithCharter claims and locks a goal, returning both the GoalClaim and
// the enforced GoalCharter boundary.
func ClaimGoalWithCharter(rootDir, slug, agent, note, session string, force bool, opts ClaimOptions) (*GoalClaim, *GoalCharter, error) {
	return claimGoal(rootDir, slug, agent, note, session, force, opts)
}

func claimGoal(rootDir, slug, agent, note, session string, force bool, opts ...ClaimOptions) (*GoalClaim, *GoalCharter, error) {
	if slug == "" {
		return nil, nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return nil, nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}

	// Check if already implemented in git, certified, or superseded
	reopenRec, _ := GetActiveReopen(rootDir, slug)
	isReopened := reopenRec != nil && reopenRec.Active

	commits, _, _, _, _ := scanGitCommits(rootDir, []string{slug})
	if c, ok := commits[slug]; ok && !isReopened {
		hasUnchecked := goalHasUncheckedTasks(rootDir, slug)
		if !hasUnchecked {
			return nil, nil, fmt.Errorf("goal %q is already implemented in commit %s", slug, c)
		}
	}
	if cert, _ := GetActiveCertification(rootDir, slug); cert != nil && !isReopened {
		hasUnchecked := goalHasUncheckedTasks(rootDir, slug)
		if !hasUnchecked {
			return nil, nil, fmt.Errorf("goal %q is already certified from evidence %s", slug, cert.EvidenceFile)
		}
	}
	if sup, _ := GetActiveSupersede(rootDir, slug); sup != nil {
		return nil, nil, fmt.Errorf("goal %q is superseded by %s", slug, sup.SupersededBy)
	}

	// Check dependency blockers before lease writes and forbid --force for structured decision gates
	depBlockers, err := GoalDependencyBlockers(rootDir, slug)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot evaluate dependency blockers for %q: %w", slug, err)
	}
	for _, b := range depBlockers {
		if b.Code == DiagDependencyDecisionMismatch || b.Code == DiagDependencyArtifactMismatch {
			return nil, nil, fmt.Errorf("cannot claim blocked goal %q: structured dependency condition violated: %s: %s (cannot be overridden with --force)", slug, b.Code, b.Message)
		}
	}

	// Enforce task readiness for structured work packages before creating any charter or lease
	if diags, err := CheckWorkPackageReady(rootDir, slug); len(diags) > 0 || err != nil {
		if err != nil {
			return nil, nil, fmt.Errorf("cannot claim goal %q: %w", slug, err)
		}
		return nil, nil, fmt.Errorf("cannot claim goal %q: SPEC_NOT_READY: %d unresolved diagnostic(s)", slug, len(diags))
	}

	// Parse candidate goal files to touch and shape
	var candidateFiles []string
	candidateShape := "ship"
	if data, err := os.ReadFile(filepath.Join(goalDir, "goal.md")); err == nil {
		_, _, _, _, _, candidateFiles, candidateShape = parseGoalMetadata(string(data))
	}
	if factsData, err := os.ReadFile(filepath.Join(goalDir, "facts.md")); err == nil {
		factsPaths := ExtractFactsContractPaths(string(factsData))
		for _, fp := range factsPaths {
			if !slices.Contains(candidateFiles, fp) {
				candidateFiles = append(candidateFiles, fp)
			}
		}
	}

	// Check if goal is blocked by unmet dependencies or cycles, or file conflicts
	audit, err := ScanGoals(rootDir)
	if err == nil {
		for _, g := range audit.Goals {
			if g.Slug == slug {
				if g.Status == "tainted" || g.State == StateTainted {
					return nil, nil, fmt.Errorf("cannot claim goal %q: prerequisite is in remediation (tainted)", slug)
				}
				if g.Blocked && !force {
					if g.BlockedReason != "" {
						return nil, nil, fmt.Errorf("cannot claim blocked goal %q: %s (use --force to override)", slug, g.BlockedReason)
					}
					if len(g.UnmetDeps) > 0 {
						rootBlockersStr := ""
						if len(g.RootBlockers) > 0 && !sliceEqual(g.UnmetDeps, g.RootBlockers) {
							rootBlockersStr = fmt.Sprintf(" (root blockers: %v)", g.RootBlockers)
						}
						return nil, nil, fmt.Errorf("cannot claim blocked goal %q: unmet prerequisites: %v%s (use --force to override)", slug, g.UnmetDeps, rootBlockersStr)
					}
					return nil, nil, fmt.Errorf("cannot claim blocked goal %q: goal is currently blocked by prerequisites (use --force to override)", slug)
				}
				break
			}
		}

		// Enforce file disjointness: prevent claiming if another active worker holds overlapping files
		// Scout tasks are non-mutating investigations — exempt from collision checks
		if len(candidateFiles) > 0 && !force && candidateShape != "scout" {
			for _, otherGoal := range audit.Goals {
				if otherGoal.Slug == slug {
					continue
				}
				if otherGoal.Claim != nil && !otherGoal.Claim.Stale && otherGoal.Status == "claimed" {
					activeFiles := otherGoal.Claim.FilesToTouch
					if len(activeFiles) == 0 {
						activeFiles = otherGoal.FilesToTouch
					}
					overlaps := OverlappingPaths(candidateFiles, activeFiles)
					if len(overlaps) > 0 {
						agentName := otherGoal.Claim.Agent
						if agentName == "" {
							agentName = otherGoal.Claim.ClaimedBy
						}
						return nil, nil, fmt.Errorf("cannot claim goal %q: file conflict on %v with active goal %q (claimed by @%s). Use --force to override", slug, overlaps, otherGoal.Slug, agentName)
					}
				}
			}
		}
	}

	normalizedAgent := strings.TrimPrefix(agent, "@")
	if normalizedAgent == "" {
		normalizedAgent = "agent"
	}

	var claimOpts ClaimOptions
	if len(opts) > 0 {
		claimOpts = opts[0]
	}

	sessionID := strings.TrimSpace(session)
	charter, err := CreateCharter(rootDir, slug, normalizedAgent, sessionID, claimOpts.AllowPaths, claimOpts.DenyPaths, claimOpts.RequiredChecks, claimOpts.MaxDuration)
	if err != nil {
		return nil, nil, err
	}

	// Acquire repository-wide charter lock to ensure atomic collision evaluation and registration
	unlockCharterGlobal, err := AcquireGlobalCharterLock(rootDir, DefaultLockAcquisitionTimeout)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire global charter lock: %w", err)
	}
	defer unlockCharterGlobal()

	// Enforce charter boundary anti-collision across active charters
	// Scout tasks are non-mutating investigations — exempt from collision checks
	if candidateShape != "scout" {
		if err := EvaluateCharterCollisions(rootDir, charter, force); err != nil {
			return nil, nil, err
		}
	}

	unlock, err := acquireGoalLock(goalDir)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire goal lock: %w", err)
	}
	defer unlock()

	unlockCharter, lockToken, err := AcquireCharterLock(rootDir, slug, DefaultLockAcquisitionTimeout)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire charter lock: %w", err)
	}
	defer unlockCharter()
	charter.LockToken = lockToken

	claimPath := filepath.Join(goalDir, "claim.json")

	var originalClaimedAt time.Time
	existingSession := ""
	if fileExists(claimPath) {
		existing, err := readClaim(claimPath)
		if err == nil && existing != nil {
			existingSession = existing.SessionID
			existingAgent := strings.TrimPrefix(existing.ClaimedBy, "@")
			if existingAgent == "" {
				existingAgent = strings.TrimPrefix(existing.Agent, "@")
			}
			isSameAgent := strings.EqualFold(normalizedAgent, existingAgent)

			if existing.Stale || isSameAgent {
				// Cleanly reclaim stale claim, or refresh active claim idempotently for the holding agent
				if isSameAgent && !existing.ClaimedAt.IsZero() {
					originalClaimedAt = existing.ClaimedAt
				}
			} else if !force {
				dur := "recently"
				if !existing.ClaimedAt.IsZero() {
					dur = time.Since(existing.ClaimedAt).Round(time.Second).String() + " ago"
				}
				return nil, nil, fmt.Errorf("goal %q is already claimed by %q (%s). Use --force to override or release it first", slug, existingAgent, dur)
			}
		}
	}

	hostname, _ := os.Hostname()
	now := time.Now().UTC()
	claimedAt := now
	if !originalClaimedAt.IsZero() {
		claimedAt = originalClaimedAt
	}

	if sessionID == "" {
		// An idempotent re-claim keeps the session a caller already recorded.
		sessionID = existingSession
		charter.SessionID = existingSession
	}

	claim := &GoalClaim{
		GoalSlug:     slug,
		ClaimedBy:    normalizedAgent,
		Agent:        normalizedAgent,
		ClaimedAt:    claimedAt,
		UpdatedAt:    now,
		Hostname:     hostname,
		PID:          os.Getppid(),
		SessionID:    sessionID,
		Status:       "in_progress",
		Note:         note,
		FilesToTouch: candidateFiles,
	}

	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	if err := AtomicWriteFile(claimPath, data, 0644); err != nil {
		return nil, nil, fmt.Errorf("write claim file: %w", err)
	}

	if err := SaveCharter(rootDir, charter); err != nil {
		return nil, nil, fmt.Errorf("save charter file: %w", err)
	}

	_, _ = EnsureGitIgnoreClaim(rootDir)

	return claim, charter, nil
}

// ReleaseGoal removes the claim lock and returns the goal to the pending pool.
func ReleaseGoal(rootDir, slug string) error {
	if slug == "" {
		return fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	unlock, err := acquireGoalLock(goalDir)
	if err != nil {
		return fmt.Errorf("acquire goal lock: %w", err)
	}
	defer unlock()

	claimPath := filepath.Join(goalDir, "claim.json")
	if !fileExists(claimPath) {
		return fmt.Errorf("goal %q is not currently claimed", slug)
	}
	if err := os.Remove(claimPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove claim file: %w", err)
	}
	_ = RemoveCharter(rootDir, slug)
	return nil
}

// AttributeGoal marks a goal as implemented by attributing it to an existing local git commit
// without requiring an additional chore commit.
func AttributeGoal(rootDir, slug, commitHash string) (*Goal, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	if strings.TrimSpace(commitHash) == "" {
		return nil, fmt.Errorf("commit hash is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)

	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}

	unlock, err := acquireGoalLock(goalDir)
	if err != nil {
		return nil, fmt.Errorf("acquire goal lock: %w", err)
	}
	defer unlock()

	// 1. Verify commit exists in the local git repository
	cmd := exec.Command("git", "-C", rootDir, "rev-parse", "--verify", strings.TrimSpace(commitHash)+"^{commit}")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("commit %q does not exist in local git repository: %s", commitHash, strings.TrimSpace(string(out)))
	}

	// 2. Resolve short commit SHA (7 chars)
	shortCmd := exec.Command("git", "-C", rootDir, "rev-parse", "--short", strings.TrimSpace(commitHash))
	shortOut, err := shortCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve short commit hash: %w", err)
	}
	shortSHA := strings.TrimSpace(string(shortOut))

	// 3. Clear active claim and charter if present
	claimPath := filepath.Join(goalDir, "claim.json")
	if fileExists(claimPath) {
		_ = os.Remove(claimPath)
	}
	_ = RemoveCharter(rootDir, slug)

	// 4. Persist retrospective.json
	retro := RetrospectiveRecord{
		GoalSlug:   slug,
		Status:     "implemented",
		Commit:     shortSHA,
		RecordedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(retro, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal retrospective record: %w", err)
	}
	if err := AtomicWriteFile(filepath.Join(goalDir, "retrospective.json"), data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write retrospective.json: %w", err)
	}

	// 5. Update plan.md tasks to checked if present
	planPath := filepath.Join(goalDir, "plan.md")
	if planBytes, err := os.ReadFile(planPath); err == nil {
		planStr := string(planBytes)
		updatedPlan := strings.ReplaceAll(planStr, "- [ ]", "- [x]")
		_ = AtomicWriteFile(planPath, []byte(updatedPlan), 0644)
	}

	// 6. Scan goals and sync README.md
	audit, err := ScanGoals(rootDir)
	if err != nil {
		return nil, fmt.Errorf("scan goals after attribution: %w", err)
	}
	_ = SyncReadme(rootDir, audit)

	for i := range audit.Goals {
		if audit.Goals[i].Slug == slug {
			return &audit.Goals[i], nil
		}
	}

	return nil, fmt.Errorf("goal %q not found in roadmap audit", slug)
}

// ReportGoal updates the live progress note or status for a claimed goal.
func ReportGoal(rootDir, slug, note, status string) (*GoalClaim, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}

	unlock, err := acquireGoalLock(goalDir)
	if err != nil {
		return nil, fmt.Errorf("acquire goal lock: %w", err)
	}
	defer unlock()

	claimPath := filepath.Join(goalDir, "claim.json")
	if !fileExists(claimPath) {
		return nil, fmt.Errorf("goal %q is not currently claimed; run 'sila goals claim %s' first", slug, slug)
	}
	claim, err := readClaim(claimPath)
	if err != nil || claim == nil {
		return nil, fmt.Errorf("read claim %s: %w", claimPath, err)
	}

	hostname, _ := os.Hostname()
	now := time.Now().UTC()
	claim.UpdatedAt = now
	pid := os.Getppid()
	if pid <= 1 {
		pid = os.Getpid()
	}
	claim.PID = pid
	claim.Hostname = hostname
	claim.Stale = false
	claim.StaleReason = ""
	claim.StaleAge = ""

	if status != "" {
		claim.Status = status
	} else if claim.Status == "" {
		claim.Status = "in_progress"
	}
	if note != "" {
		claim.Note = note
	}

	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := AtomicWriteFile(claimPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write claim file: %w", err)
	}
	if err := RenewCharter(rootDir, slug); err != nil {
		return nil, fmt.Errorf("renew charter: %w", err)
	}

	return claim, nil
}

// TouchGoal refreshes the heartbeat timestamp and renews the claim lease for a goal.
func TouchGoal(rootDir, slug, agent string) (*GoalClaim, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}

	unlock, err := acquireGoalLock(goalDir)
	if err != nil {
		return nil, fmt.Errorf("acquire goal lock: %w", err)
	}
	defer unlock()

	claimPath := filepath.Join(goalDir, "claim.json")
	if !fileExists(claimPath) {
		return nil, fmt.Errorf("goal %q is not currently claimed", slug)
	}

	claim, err := readClaim(claimPath)
	if err != nil || claim == nil {
		return nil, fmt.Errorf("read claim %s: %w", claimPath, err)
	}

	normalizedAgent := strings.TrimPrefix(agent, "@")
	if normalizedAgent != "" {
		existingAgent := strings.TrimPrefix(claim.Agent, "@")
		if existingAgent == "" {
			existingAgent = strings.TrimPrefix(claim.ClaimedBy, "@")
		}
		if existingAgent != "" && !strings.EqualFold(normalizedAgent, existingAgent) {
			return nil, fmt.Errorf("cannot touch claim for goal %q: claimed by %q, not %q", slug, existingAgent, normalizedAgent)
		}
	}

	now := time.Now().UTC()
	claim.UpdatedAt = now
	pid := os.Getppid()
	if pid <= 1 {
		pid = os.Getpid()
	}
	claim.PID = pid
	hostname, _ := os.Hostname()
	if hostname != "" {
		claim.Hostname = hostname
	}
	claim.Stale = false
	claim.StaleReason = ""
	claim.StaleAge = ""

	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := AtomicWriteFile(claimPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write claim file: %w", err)
	}
	if err := RenewCharter(rootDir, slug); err != nil {
		return nil, fmt.Errorf("renew charter: %w", err)
	}

	return claim, nil
}

func readClaim(path string) (*GoalClaim, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var claim GoalClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		return nil, err
	}
	if claim.ClaimedBy == "" && claim.Agent != "" {
		claim.ClaimedBy = claim.Agent
	}
	if claim.Agent == "" && claim.ClaimedBy != "" {
		claim.Agent = claim.ClaimedBy
	}
	if claim.GoalSlug == "" {
		claim.GoalSlug = filepath.Base(filepath.Dir(path))
	}
	claim.CheckStale()
	return &claim, nil
}

// scanGitCommitsRaw parses recent git history to detect commits verifying a goal, ignoring any active reopens.
func scanGitCommitsRaw(rootDir string, validSlugs []string) map[string]string {
	commits := make(map[string]string)
	validSet := make(map[string]bool, len(validSlugs))
	for _, s := range validSlugs {
		validSet[s] = true
	}

	cmd := exec.Command("git", "log", "--pretty=format:%h%x1f%s%x1f%b%x1e")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return commits
	}

	entries := bytes.SplitSeq(out, []byte("\x1e"))
	for raw := range entries {
		rawStr := strings.TrimSpace(string(raw))
		if rawStr == "" {
			continue
		}
		parts := strings.Split(rawStr, "\x1f")
		if len(parts) < 2 {
			continue
		}
		hash := parts[0]
		subject := parts[1]
		body := ""
		if len(parts) > 2 {
			body = parts[2]
		}
		text := subject + "\n\n" + body

		trailerSlugs := make(map[string]bool)
		for _, m := range trailerRE.FindAllStringSubmatch(text, -1) {
			if len(m) > 1 {
				slug := m[1]
				if validSet[slug] {
					trailerSlugs[slug] = true
				}
			}
		}

		isDocOnly := documentationOnlyCommit(subject)
		isClosure := isConventionalClosureSubject(subject)

		// 1. Direct path match: goals/<slug>/goal.md
		if !isDocOnly && isClosure {
			for _, m := range slugPathRE.FindAllStringSubmatch(text, -1) {
				if len(m) > 1 {
					slug := m[1]
					if validSet[slug] && commits[slug] == "" {
						commits[slug] = hash
					}
				}
			}

			// 2. Goal header match: Goal: <slug>
			for _, m := range goalHdrRE.FindAllStringSubmatch(text, -1) {
				if len(m) > 1 {
					slug := m[1]
					if validSet[slug] && commits[slug] == "" {
						commits[slug] = hash
					}
				}
			}

			// 3. Bracket match: [goal: <slug>]
			for _, m := range bracketGoalRE.FindAllStringSubmatch(text, -1) {
				if len(m) > 1 {
					slug := m[1]
					if validSet[slug] && commits[slug] == "" {
						commits[slug] = hash
					}
				}
			}
		}

		// 3. Git trailer match: Goal-Ref: <slug>, Sila-Goal: <slug>, Fixes-Goal: <slug>
		// Explicit trailers are honored even if the subject is docs: or test:
		for slug := range trailerSlugs {
			if commits[slug] == "" {
				commits[slug] = hash
			}
		}
	}
	return commits
}

// scanGitCommits parses recent git history to detect commits verifying a goal.
// It is reopen-aware: if a goal has an active reopen.json, commits matching OriginalCommit
// (or committed before ReopenedAt) are ignored. If a newer commit is found, the reopen is resolved.
// It also returns commits that mention a goal but are documentation-only
// (docs/test subjects), so callers can explain why a goal did not
// close instead of leaving the user guessing.
func scanGitCommits(rootDir string, validSlugs []string) (map[string]string, map[string][]string, map[string][]string, map[string]string, map[string][]string) {
	commits := make(map[string]string)
	skipped := make(map[string][]string)
	nearMisses := make(map[string][]string)
	carriedForward := make(map[string]string)
	commitParents := make(map[string][]string)
	validSet := make(map[string]bool, len(validSlugs))
	activeReopens := make(map[string]*ReopenRecord)
	for _, s := range validSlugs {
		validSet[s] = true
		if rec, err := GetActiveReopen(rootDir, s); err == nil && rec != nil {
			activeReopens[s] = rec
		}
	}

	cmd := exec.Command("git", "log", "--pretty=format:%h%x1f%s%x1f%b%x1f%cI%x1f%p%x1e")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return commits, skipped, nearMisses, carriedForward, commitParents
	}

	entries := bytes.SplitSeq(out, []byte("\x1e"))
	for raw := range entries {
		rawStr := strings.TrimSpace(string(raw))
		if rawStr == "" {
			continue
		}
		parts := strings.Split(rawStr, "\x1f")
		if len(parts) < 2 {
			continue
		}
		hash := parts[0]
		subject := parts[1]
		body := ""
		if len(parts) > 2 {
			body = parts[2]
		}
		committerDateStr := ""
		if len(parts) > 3 {
			committerDateStr = parts[3]
		}
		if len(parts) > 4 {
			parentStr := strings.TrimSpace(parts[4])
			if parentStr != "" {
				commitParents[hash] = strings.Fields(parentStr)
			}
		}
		text := subject + "\n\n" + body

		// Check for Carried-Forward-From trailer
		for _, m := range carriedForwardRE.FindAllStringSubmatch(text, -1) {
			if len(m) > 1 {
				oldSlug := m[1]
				if validSet[oldSlug] && carriedForward[oldSlug] == "" {
					carriedForward[oldSlug] = fmt.Sprintf("commit %s", hash)
				}
			}
		}

		matchedSlugs := make(map[string]bool)
		trailerSlugs := make(map[string]bool)

		// 1. Direct path match: goals/<slug>/goal.md
		for _, m := range slugPathRE.FindAllStringSubmatch(text, -1) {
			if len(m) > 1 {
				slug := m[1]
				if validSet[slug] {
					matchedSlugs[slug] = true
				}
			}
		}

		// 2. Goal header match: Goal: <slug>
		for _, m := range goalHdrRE.FindAllStringSubmatch(text, -1) {
			if len(m) > 1 {
				slug := m[1]
				if validSet[slug] {
					matchedSlugs[slug] = true
				}
			}
		}

		// 2b. Bracket match: [goal: <slug>]
		for _, m := range bracketGoalRE.FindAllStringSubmatch(text, -1) {
			if len(m) > 1 {
				slug := m[1]
				if validSet[slug] {
					matchedSlugs[slug] = true
				}
			}
		}

		// 3. Git trailer match: Goal-Ref: <slug>, Sila-Goal: <slug>, Fixes-Goal: <slug>
		for _, m := range trailerRE.FindAllStringSubmatch(text, -1) {
			if len(m) > 1 {
				slug := m[1]
				if validSet[slug] {
					matchedSlugs[slug] = true
					trailerSlugs[slug] = true
				}
			}
		}

		if len(matchedSlugs) == 0 {
			// Check for near-miss mentions of valid slugs in prose
			for _, slug := range validSlugs {
				if commits[slug] != "" {
					continue
				}
				if len(nearMisses[slug]) < 3 && containsSlugProse(text, slug) {
					nearMisses[slug] = append(nearMisses[slug], fmt.Sprintf("%s (%s)", hash, subject))
				}
			}
			continue
		}

		isDocOnly := documentationOnlyCommit(subject)
		isClosure := isConventionalClosureSubject(subject)

		for slug := range matchedSlugs {
			// Skip documentation-only commits and non-conventional-closure commits
			// UNLESS the goal is explicitly matched by a git trailer
			if (isDocOnly || !isClosure) && !trailerSlugs[slug] {
				skipped[slug] = append(skipped[slug], hash)
				continue
			}

			if commits[slug] != "" {
				continue
			}
			rec, hasReopen := activeReopens[slug]
			if !hasReopen {
				commits[slug] = hash
				continue
			}

			// Has active reopen: check if this commit is the old one or a newer one
			if hash == rec.OriginalCommit {
				continue
			}
			var commitTime time.Time
			if committerDateStr != "" {
				commitTime, _ = time.Parse(time.RFC3339, strings.TrimSpace(committerDateStr))
			}
			if !commitTime.IsZero() && !rec.ReopenedAt.IsZero() && commitTime.Before(rec.ReopenedAt.Add(-2*time.Second)) {
				continue
			}

			// Epistemic Task Completion Gate:
			// Goals that were reopened with a remediation charter require all tasks
			// to be checked before the reopened status can be cleared by commit attribution.
			if goalHasUncheckedTasks(rootDir, slug) {
				continue
			}

			// New commit resolving the reopen!
			commits[slug] = hash
			_ = ResolveReopen(rootDir, slug, hash)
			delete(activeReopens, slug)
		}
	}
	return commits, skipped, nearMisses, carriedForward, commitParents
}

// scanDirtyFiles returns the set of cleaned relative file paths with uncommitted modifications.
func scanDirtyFiles(rootDir string) map[string]bool {
	dirty := make(map[string]bool)
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return dirty
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) < 3 {
			continue
		}
		pathPart := strings.TrimSpace(line[2:])
		if idx := strings.Index(pathPart, " -> "); idx != -1 {
			pathPart = strings.TrimSpace(pathPart[idx+4:])
		}
		pathPart = strings.Trim(pathPart, `"`)
		if pathPart != "" {
			dirty[filepath.Clean(pathPart)] = true
		}
	}
	return dirty
}

// documentationOnlyCommit reports whether a commit subject represents documentation,
// testing, or goal registration/scaffolding work that should not close a goal unless
// an explicit git trailer is present.
func documentationOnlyCommit(subject string) bool {
	subject = strings.ToLower(strings.TrimSpace(subject))
	if strings.HasPrefix(subject, "docs") || strings.HasPrefix(subject, "test") {
		return true
	}
	// Scaffolding or registration commits under chore should not close goals
	if strings.HasPrefix(subject, "chore") {
		rem := subject[len("chore"):]
		scope := ""
		if strings.HasPrefix(rem, "(") {
			idx := strings.Index(rem, ")")
			if idx != -1 {
				scope = strings.TrimSpace(rem[1:idx])
				rem = rem[idx+1:]
			}
		}
		rem = strings.TrimSpace(rem)
		if strings.HasPrefix(rem, "!") {
			rem = strings.TrimSpace(rem[1:])
		}
		if strings.HasPrefix(rem, ":") || strings.HasPrefix(rem, "-") {
			desc := strings.TrimSpace(rem[1:])
			switch scope {
			case "goal", "goals", "scaffold", "scaffolding", "register", "registration":
				return true
			}
			for _, verb := range []string{"register", "scaffold", "init goal", "create goal"} {
				if strings.HasPrefix(desc, verb) {
					return true
				}
			}
		}
	}
	return false
}

// isCommitAncestor reports whether ancestor is reachable from descendant in git history.
func isCommitAncestor(dir, ancestor, descendant string) bool {
	if dir == "" || ancestor == "" || descendant == "" {
		return false
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// isGoalsOnlyDiffBetweenCommits reports whether all files modified between
// ancestor and descendant reside under goals/ (e.g., committing evidence.md,
// certification.json, audit-receipt.json, or goals/README.md after certifying).
func isGoalsOnlyDiffBetweenCommits(dir, ancestor, descendant string) bool {
	if dir == "" || ancestor == "" || descendant == "" {
		return false
	}
	cmd := exec.Command("git", "diff", "--name-only", ancestor, descendant)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return true
	}
	for _, f := range lines {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(f)))
		if clean == "" {
			continue
		}
		if !strings.HasPrefix(clean, "goals/") {
			return false
		}
	}
	return true
}

// isConventionalClosureSubject reports whether a commit subject starts with one of
// the conventional closure types (feat, fix, refactor, perf, chore).
func isConventionalClosureSubject(subject string) bool {
	subject = strings.TrimSpace(subject)
	lower := strings.ToLower(subject)
	for _, prefix := range []string{"feat", "fix", "refactor", "perf", "chore"} {
		if strings.HasPrefix(lower, prefix) {
			rem := lower[len(prefix):]
			if strings.HasPrefix(rem, "(") {
				idx := strings.Index(rem, ")")
				if idx != -1 {
					rem = rem[idx+1:]
				}
			}
			rem = strings.TrimSpace(rem)
			if strings.HasPrefix(rem, "!") {
				rem = strings.TrimSpace(rem[1:])
			}
			if strings.HasPrefix(rem, ":") || strings.HasPrefix(rem, "-") {
				return true
			}
		}
	}
	return false
}

func containsSlugProse(text, slug string) bool {
	if slug == "" {
		return false
	}
	slugLower := strings.ToLower(slug)
	textLower := strings.ToLower(text)
	idx := strings.Index(textLower, slugLower)
	for idx != -1 {
		start := idx
		end := idx + len(slugLower)
		validBefore := start == 0 || !isSlugBoundaryChar(textLower[start-1])
		validAfter := end == len(textLower) || !isSlugBoundaryChar(textLower[end])
		if validBefore && validAfter {
			return true
		}
		next := strings.Index(textLower[end:], slugLower)
		if next == -1 {
			break
		}
		idx = end + next
	}
	return false
}

func isSlugBoundaryChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-' || b == '_'
}

// isAttributionCommitTask reports whether the task text represents an attribution commit step
// (e.g. "- [ ] Phase 4 — Attribution Commit: Commit with ...").
func isAttributionCommitTask(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "attribution commit") ||
		(strings.Contains(lower, "attribution") && strings.Contains(lower, "commit"))
}

// hasExplicitCheckboxes reports whether the markdown content contains explicit task checkbox
// markers (- [ ], - [x], * [ ], + [ ]) or step headings with explicit completion markers ([ ], [x]).
func hasExplicitCheckboxes(content string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if isCheckboxLine(trimmed) {
			return true
		}
		if _, _, ok := stepHeadingTask(trimmed); ok {
			if strings.HasSuffix(trimmed, "[x]") || strings.HasSuffix(trimmed, "[X]") || strings.HasSuffix(trimmed, "[ ]") {
				return true
			}
		}
		if rest, ok := splitNumberedPrefix(trimmed); ok {
			if isCheckedMarker(rest) || strings.HasPrefix(rest, "[ ]") {
				return true
			}
		}
	}
	return false
}

// CountUncheckedNonAttributionTasks returns the number of unchecked non-attribution
// tasks in a goal's plan.md.
func CountUncheckedNonAttributionTasks(rootDir, slug string) (int, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	planPath, hasPlan := findGoalFile(filepath.Join(rootDir, "goals", slug), "plan.md")
	if !hasPlan {
		return 0, nil
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		return 0, err
	}
	return countUncheckedNonAttributionTasks(string(data)), nil
}

// CheckAllNonAttributionTasks marks every unchecked non-attribution task in
// plan.md as complete and returns the number of tasks updated.
func CheckAllNonAttributionTasks(rootDir, slug string) (int, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	planPath, hasPlan := findGoalFile(filepath.Join(rootDir, "goals", slug), "plan.md")
	if !hasPlan {
		return 0, nil
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(data), "\n")
	updated := 0
	replaceUnchecked := func(line, text string) (string, bool) {
		if isAttributionCommitTask(text) {
			return line, false
		}
		idx := strings.Index(line, "[ ]")
		if idx < 0 {
			return line, false
		}
		return line[:idx] + "[x]" + line[idx+3:], true
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "* [ ]") || strings.HasPrefix(trimmed, "+ [ ]") {
			text := ""
			if len(trimmed) > 5 {
				text = strings.TrimSpace(trimmed[5:])
			}
			if replaced, ok := replaceUnchecked(line, text); ok {
				lines[i] = replaced
				updated++
			}
			continue
		}
		if text, completed, ok := stepHeadingTask(trimmed); ok && !completed && strings.HasSuffix(trimmed, "[ ]") {
			if replaced, ok := replaceUnchecked(line, text); ok {
				lines[i] = replaced
				updated++
			}
			continue
		}
		if rest, ok := splitNumberedPrefix(trimmed); ok && strings.HasPrefix(rest, "[ ]") {
			text := strings.TrimSpace(rest[3:])
			if replaced, ok := replaceUnchecked(line, text); ok {
				lines[i] = replaced
				updated++
			}
		}
	}

	if updated > 0 {
		if err := AtomicWriteFile(planPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			return 0, err
		}
	}
	return updated, nil
}

// countUncheckedNonAttributionTasks counts how many unchecked tasks exist in the plan content
// excluding any attribution commit tasks.
func countUncheckedNonAttributionTasks(content string) int {
	unchecked := 0
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "* [ ]") || strings.HasPrefix(trimmed, "+ [ ]") {
			text := ""
			if len(trimmed) > 5 {
				text = strings.TrimSpace(trimmed[5:])
			}
			if !isAttributionCommitTask(text) {
				unchecked++
			}
		} else if text, completed, ok := stepHeadingTask(trimmed); ok {
			if !completed && strings.HasSuffix(trimmed, "[ ]") {
				if !isAttributionCommitTask(text) {
					unchecked++
				}
			}
		} else if rest, ok := splitNumberedPrefix(trimmed); ok {
			if strings.HasPrefix(rest, "[ ]") {
				text := strings.TrimSpace(rest[3:])
				if !isAttributionCommitTask(text) {
					unchecked++
				}
			}
		}
	}
	return unchecked
}

// isDefaultScaffoldPlan reports whether the plan content consists solely of
// the default placeholder steps generated by CreateGoal when no custom tasks were specified.
func isDefaultScaffoldPlan(content string) bool {
	lines := strings.Split(content, "\n")
	var tasks []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if isCheckboxLine(trimmed) {
			tasks = append(tasks, trimmed)
		}
	}
	if len(tasks) == 4 &&
		((strings.Contains(tasks[0], "Contracts & Schemas") &&
			strings.Contains(tasks[1], "Core Implementation") &&
			(strings.Contains(tasks[2], "Verification & Tests") ||
				strings.Contains(tasks[2], "Verification & Evidence"))) ||
			(strings.Contains(tasks[0], "Discovery & Reconnaissance") &&
				strings.Contains(tasks[1], "Analysis & Evidence Gathering") &&
				(strings.Contains(tasks[2], "Documentation & Synthesis") ||
					strings.Contains(tasks[2], "Documentation & Evidence")))) &&
		(strings.Contains(tasks[3], "Attribution Commit") ||
			strings.Contains(tasks[3], "Attribution & Registry Closure")) {
		return true
	}
	if len(tasks) == 3 &&
		strings.Contains(tasks[0], "Initial investigation") &&
		strings.Contains(tasks[1], "Implementation of core") &&
		strings.Contains(tasks[2], "Comprehensive testing") {
		return true
	}
	return false
}

// goalHasUncheckedTasks returns true if the goal declares task checkboxes in plan.md or goal.md
// and one or more checkboxes remain unchecked.
func goalHasUncheckedTasks(rootDir, slug string) bool {
	rootDir = resolveGoalsRootDir(rootDir)
	dir := filepath.Join(rootDir, "goals", slug)
	planPath, hasPlan := findGoalFile(dir, "plan.md")
	met, total := 0, 0
	var planContent string
	if hasPlan {
		if data, err := os.ReadFile(planPath); err == nil {
			planContent = string(data)
			met, total = countCheckboxes(planContent)
		}
	}
	if total == 0 {
		goalPath, hasGoal := findGoalFile(dir, "goal.md")
		if hasGoal {
			if data, err := os.ReadFile(goalPath); err == nil {
				planContent = string(data)
				met, total = countCheckboxes(planContent)
			}
		}
	}
	if total == 0 || met >= total {
		return false
	}
	if planContent != "" {
		if !hasExplicitCheckboxes(planContent) {
			return false
		}
		if countUncheckedNonAttributionTasks(planContent) == 0 {
			return false
		}
	}
	return true
}

// parseReadmeImplemented extracts goal slugs that are already recorded in goals/README.md
// under the ## 🟢 Implemented & Verified section.
func parseReadmeImplemented(rootDir string) map[string]bool {
	result := make(map[string]bool)
	readmePath := filepath.Join(resolveGoalsRootDir(rootDir), "goals", "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		return result
	}
	lines := strings.Split(string(data), "\n")
	inImplementedSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## 🟢 Implemented") || strings.HasPrefix(trimmed, "## Implemented") {
			inImplementedSection = true
			continue
		}
		if inImplementedSection && strings.HasPrefix(trimmed, "## ") {
			inImplementedSection = false
			continue
		}
		if inImplementedSection && strings.HasPrefix(trimmed, "| [`") {
			start := strings.Index(trimmed, "[`")
			if start != -1 {
				end := strings.Index(trimmed[start+2:], "`]")
				if end != -1 {
					slug := trimmed[start+2 : start+2+end]
					if slug != "" {
						result[slug] = true
					}
				}
			}
		}
	}
	return result
}

// SyncReadmeOptions holds options for syncing the goals README.
type SyncReadmeOptions struct {
	Stage     bool
	CommitMsg string
	// ExtraPaths are repository-relative files changed alongside README.md
	// (for example cleared claim records) that staging and committing include.
	ExtraPaths []string
	// CheckOnly verifies that goals/README.md matches expected state without writing to disk.
	CheckOnly bool
}

// ClearClosedClaims removes claim records left on goals that have closed
// (implemented or superseded). The audit promises that running `sila goals`
// clears the claim lock when a goal closes; without this, a claim.json survives
// forever and the global dashboard keeps counting it as a stale lock. It
// returns the repository-relative paths it removed, for staging.
func ClearClosedClaims(rootDir string, res *AuditResult) ([]string, error) {
	if res == nil {
		return nil, nil
	}
	var cleared []string
	for _, g := range res.Goals {
		if g.Status == "implemented" || g.Status == "superseded" {
			_ = RemoveCharter(rootDir, g.Slug)
		}
		if g.Claim == nil || (g.Status != "implemented" && g.Status != "superseded") {
			continue
		}
		rel := filepath.Join("goals", g.Slug, "claim.json")
		if err := os.Remove(filepath.Join(rootDir, rel)); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return cleared, fmt.Errorf("clear claim for %s: %w", g.Slug, err)
		}
		cleared = append(cleared, rel)
	}
	return cleared, nil
}

// SortGoalsTopologically sorts a slice of Goal structs in topological dependency order.
// Prerequisites are strictly guaranteed to precede their downstream dependents.
// For goals without direct or transitive dependency relationships, tie-breaking follows:
// 1. Ready before Blocked (for pending goals)
// 2. Topological depth (shallower foundational goals first)
// 3. Explicit Sequence number (ascending: 1, 2, 3...)
// 4. Downstream impact count (descending: goals unblocking more work first)
// 5. Alphanumeric slug (deterministic tie-breaker)
func SortGoalsTopologically(goals []Goal, contextGoals ...[]Goal) []Goal {
	if len(goals) <= 1 {
		return append([]Goal(nil), goals...)
	}

	// Combine target goals with any broader context goals (e.g. full project audit)
	var all []Goal
	seen := make(map[string]bool)
	for _, g := range goals {
		if !seen[g.Slug] {
			seen[g.Slug] = true
			all = append(all, g)
		}
	}
	for _, ctx := range contextGoals {
		for _, g := range ctx {
			if !seen[g.Slug] {
				seen[g.Slug] = true
				all = append(all, g)
			}
		}
	}

	graph := BuildDependencyGraphFromSlice(all)
	cycles := graph.DetectCycles()

	// Transitive prerequisites map: transitivePrereqs[u][v] == true means v is an ancestor of u
	transitivePrereqs := make(map[string]map[string]bool, len(all))
	for _, slug := range graph.sortedNodes() {
		prereqs := graph.TransitivePrerequisites(slug)
		set := make(map[string]bool, len(prereqs))
		for _, p := range prereqs {
			set[p] = true
		}
		transitivePrereqs[slug] = set
	}

	// Downstream impact map
	impactMap := make(map[string]int, len(all))
	for _, slug := range graph.sortedNodes() {
		impactMap[slug] = len(graph.TransitiveDependents(slug))
	}

	// Topological depth DP (longest path from root)
	depthMap := make(map[string]int, len(all))
	if len(cycles) == 0 {
		if topoOrder, err := graph.TopologicalSort(); err == nil {
			for _, u := range topoOrder {
				maxPrereqDepth := -1
				for _, p := range graph.Edges()[u] {
					if d, ok := depthMap[p]; ok && d > maxPrereqDepth {
						maxPrereqDepth = d
					}
				}
				depthMap[u] = maxPrereqDepth + 1
			}
		}
	}

	result := append([]Goal(nil), goals...)

	sort.SliceStable(result, func(i, j int) bool {
		u := result[i].Slug
		v := result[j].Slug

		// 1. Strict prerequisite relationship:
		// If u is an ancestor of v (v depends on u), u MUST precede v.
		if transitivePrereqs[v] != nil && transitivePrereqs[v][u] {
			return true
		}
		// If v is an ancestor of u (u depends on v), v MUST precede u.
		if transitivePrereqs[u] != nil && transitivePrereqs[u][v] {
			return false
		}

		// 2. Status readiness: Ready goals precede Blocked goals
		if !result[i].Blocked && result[j].Blocked {
			return true
		}
		if result[i].Blocked && !result[j].Blocked {
			return false
		}

		// 3. Topological depth: shallower/foundational prerequisites precede deeper nodes
		dU := depthMap[u]
		dV := depthMap[v]
		if dU != dV {
			return dU < dV
		}

		// 4. Explicit Sequence number (if > 0)
		seqI := result[i].Sequence
		seqJ := result[j].Sequence
		if seqI > 0 && seqJ > 0 && seqI != seqJ {
			return seqI < seqJ
		}
		if seqI > 0 && seqJ == 0 {
			return true
		}
		if seqI == 0 && seqJ > 0 {
			return false
		}

		// 5. Downstream Impact: unblocking more downstream goals comes first
		impI := impactMap[u]
		impJ := impactMap[v]
		if impI != impJ {
			return impI > impJ
		}

		// 6. Alphanumeric slug tie-breaker
		return u < v
	})

	return result
}

// extractReadmePreamble preserves any custom user banner or freeze notice
// placed above "# Goals Status & Pragmatic Execution Roadmap" in goals/README.md.
func extractReadmePreamble(readmePath string) string {
	data, err := os.ReadFile(readmePath)
	if err != nil || len(data) == 0 {
		return ""
	}
	content := string(data)
	const marker = "# Goals Status & Pragmatic Execution Roadmap"
	idx := strings.Index(content, marker)
	if idx <= 0 {
		return ""
	}
	preamble := strings.TrimSpace(content[:idx])
	if preamble == "" {
		return ""
	}
	return preamble + "\n\n"
}

// SyncReadme writes the updated goals/README.md markdown file.
func SyncReadme(rootDir string, res *AuditResult) error {
	return SyncReadmeWithOpts(rootDir, res, SyncReadmeOptions{})
}

// SyncReadmeWithOpts writes the updated goals/README.md markdown file and optionally stages or commits it.
func SyncReadmeWithOpts(rootDir string, res *AuditResult, opts SyncReadmeOptions) error {
	rootDir = resolveGoalsRootDir(rootDir)
	goalsDir := filepath.Join(rootDir, "goals")
	if _, err := os.Stat(goalsDir); os.IsNotExist(err) {
		return nil
	}

	readmePath := filepath.Join(goalsDir, "README.md")
	var b strings.Builder
	if preamble := extractReadmePreamble(readmePath); preamble != "" {
		b.WriteString(preamble)
	}
	b.WriteString("# Goals Status & Pragmatic Execution Roadmap\n\n")
	b.WriteString("This index tracks all goal packages under `goals/`, their execution mode (Independent ⚡ vs Dependent 🔗), dependencies, and progress status.\n\n")
	b.WriteString("## 🤖 Agent Guide: How to Create, Claim & Execute Goals\n\n")
	b.WriteString("1. **Create a Goal**: Scaffold a new goal package conforming to standards:\n")
	b.WriteString("   ```bash\n   sila goals create <slug> --title=\"...\" [--depends-on=\"...\"] [--independent]\n   ```\n")
	b.WriteString("2. **Claim a Goal**: Lock the goal so no other agent duplicates work:\n")
	b.WriteString("   ```bash\n   sila goals claim <slug> --agent=<name> [--note=\"evaluating...\"]\n   ```\n")
	b.WriteString("3. **Launch & Implement**: Follow `facts.md` and `plan.md` until all tests pass:\n")
	b.WriteString("   ```bash\n   /goal goals/<slug>/goal.md\n   ```\n")
	b.WriteString("4. **Progress Updates**: Report status updates during execution:\n")
	b.WriteString("   ```bash\n   sila goals report <slug> \"Running e2e test suite\"\n   ```\n")
	b.WriteString("5. **Commit with Goal Reference**: Include the goal path in your commit message:\n")
	b.WriteString("   ```bash\n   git commit -m \"feat(scope): implement description (goals/<slug>/goal.md)\"\n   ```\n")
	b.WriteString("6. **Auto-Reconciliation**: Run `sila goals` to scan commits, mark 🟢 **Implemented**, and clear the claim lock.\n\n")

	b.WriteString("## Summary\n")
	fmt.Fprintf(&b, "- **Total Goals**: %d\n", res.Total)
	fmt.Fprintf(&b, "- 🟡 **Ready to Execute (Pending)**: %d (%d independent ⚡, %d unblocked 🔗)\n",
		res.ReadyCount, res.ReadyIndependentCount, res.ReadyUnblockedCount)
	if res.ClaimedCount > 0 {
		fmt.Fprintf(&b, "- 🔵 **In Progress (Claimed)**: %d\n", res.ClaimedCount)
	}
	if res.BlockedCount > 0 {
		fmt.Fprintf(&b, "- ⛔ **Blocked on Prerequisites**: %d\n", res.BlockedCount)
	}
	if res.StaleCount > 0 {
		fmt.Fprintf(&b, "- 🟢 **Implemented**: %d (%d verified 📜, %d stale ⚠️, %d unverified)\n",
			res.ImplementedCount, res.VerifiedCount, res.StaleCount, res.UnverifiedCount)
	} else {
		fmt.Fprintf(&b, "- 🟢 **Implemented**: %d (%d verified 📜, %d unverified)\n",
			res.ImplementedCount, res.VerifiedCount, res.UnverifiedCount)
	}
	if res.SupersededCount > 0 {
		fmt.Fprintf(&b, "- 🟣 **Superseded**: %d\n", res.SupersededCount)
	}
	if res.ReopenedCount > 0 {
		fmt.Fprintf(&b, "- 🔁 **Reopened for Remediation**: %d\n", res.ReopenedCount)
	}
	if res.DraftCount > 0 {
		fmt.Fprintf(&b, "- ⚪ **Draft / In-Setup**: %d\n", res.DraftCount)
	}
	if res.ImmediateCount > 0 || res.RoadmapCount > 0 || res.NiceToHaveCount > 0 || res.WontFixCount > 0 {
		fmt.Fprintf(&b, "- 🎯 **By Tier**: %d immediate (Tier 1), %d roadmap (Tier 2), %d nice-to-have (Tier 3), %d wont-fix (Tier 4)\n",
			res.ImmediateCount, res.RoadmapCount, res.NiceToHaveCount, res.WontFixCount)
	}
	if res.TotalTasks > 0 {
		pct := (res.CompletedTasks * 100) / res.TotalTasks
		fmt.Fprintf(&b, "- 📋 **Execution Tasks Progress**: %d/%d completed (%d%%)\n", res.CompletedTasks, res.TotalTasks, pct)
	}
	b.WriteString("\n---\n\n")

	if len(res.Cycles) > 0 {
		b.WriteString("> ⚠️ **WARNING: Circular dependencies detected:**\n")
		for _, c := range res.Cycles {
			b.WriteString(fmt.Sprintf("> - `%s`\n", strings.Join(c, " -> ")))
		}
		b.WriteString("\n---\n\n")
	}

	var claimed []Goal
	var pending []Goal
	var implemented []Goal
	var superseded []Goal
	for _, g := range res.Goals {
		switch g.Status {
		case "claimed":
			claimed = append(claimed, g)
		case "pending":
			pending = append(pending, g)
		case "implemented":
			implemented = append(implemented, g)
		case "superseded":
			superseded = append(superseded, g)
		}
	}

	claimed = SortGoalsTopologically(claimed, res.Goals)
	pending = SortGoalsTopologically(pending, res.Goals)
	implemented = SortGoalsTopologically(implemented, res.Goals)
	superseded = SortGoalsTopologically(superseded, res.Goals)

	if len(claimed) > 0 {
		b.WriteString("## 🔵 In Progress (Claimed Goals)\n\n")
		b.WriteString("| Goal Package | Tier | Mode | Agent | Status & Note | Started |\n")
		b.WriteString("| :--- | :---: | :---: | :--- | :--- | :--- |\n")
		for _, g := range claimed {
			agentStr := "@agent"
			noteStr := "in progress"
			durStr := "recently"
			modeStr := "⚡ Indep"
			if !g.Independent {
				modeStr = "🔗 Dep"
			}
			tierStr := "🗺️ Roadmap"
			switch NormalizeTier(g.Tier) {
			case "immediate":
				tierStr = "🎯 Immediate"
			case "nice-to-have":
				tierStr = "✨ Nice-to-Have"
			case "wont-fix":
				tierStr = "⏸️ Wont-Fix"
			}
			if g.Claim != nil {
				if g.Claim.ClaimedBy != "" {
					agentStr = "@" + g.Claim.ClaimedBy
				} else if g.Claim.Agent != "" {
					agentStr = "@" + g.Claim.Agent
				}
				statusPrefix := ""
				if g.Reopened {
					statusPrefix = "[REOPENED] "
				}
				if g.Claim.Status != "" && g.Claim.Status != "in_progress" && g.Claim.Status != "in-progress" {
					statusPrefix += fmt.Sprintf("[%s] ", strings.ToUpper(g.Claim.Status))
				}
				if g.Claim.Note != "" {
					noteStr = statusPrefix + g.Claim.Note
				} else if statusPrefix != "" {
					noteStr = strings.TrimSpace(statusPrefix)
				}
				if g.Claim.Stale {
					noteStr += " ⚠️ (stale: " + g.Claim.StaleReason + ")"
				}
				if !g.Claim.ClaimedAt.IsZero() {
					durStr = time.Since(g.Claim.ClaimedAt).Round(time.Minute).String() + " ago"
				}
			} else if g.Reopened {
				noteStr = "[REOPENED]"
			}
			fmt.Fprintf(&b, "| [`%s`](%s/goal.md) | `%s` | `%s` | `%s` | %s | %s |\n", g.Slug, g.Slug, tierStr, modeStr, agentStr, noteStr, durStr)
		}
		b.WriteString("\n---\n\n")
	}

	if len(pending) > 0 {
		b.WriteString("## 🟡 Ready to Execute (Pending Goals)\n\n")
		b.WriteString("| Goal Package | Tier | Mode & Sequence | Dependencies | Focus & Description | Launch Command |\n")
		b.WriteString("| :--- | :---: | :---: | :--- | :--- | :--- |\n")
		for _, g := range pending {
			descSnippet := ""
			if g.Description != "" {
				if len(g.Description) > 80 {
					descSnippet = " — " + g.Description[:80] + "..."
				} else {
					descSnippet = " — " + g.Description
				}
			}

			tierStr := "🗺️ Roadmap"
			switch NormalizeTier(g.Tier) {
			case "immediate":
				tierStr = "🎯 Immediate"
			case "nice-to-have":
				tierStr = "✨ Nice-to-Have"
			case "wont-fix":
				tierStr = "⏸️ Wont-Fix"
			}

			modeStr := "⚡ Independent"
			if !g.Independent {
				if g.Blocked {
					modeStr = "⛔ Blocked"
				} else {
					modeStr = "🔗 Ready"
				}
			}
			if g.Shape == "scout" {
				modeStr = "🔭 SCOUT " + modeStr
			} else {
				modeStr = "🚢 SHIP " + modeStr
			}
			if g.Sequence > 0 {
				modeStr += fmt.Sprintf(" (#%d)", g.Sequence)
			}

			depsStr := "-"
			if len(g.DependsOn) > 0 {
				if g.Blocked && len(g.UnmetDeps) > 0 {
					depsStr = "Prereqs: " + strings.Join(g.UnmetDeps, ", ")
				} else {
					depsStr = "Deps met: " + strings.Join(g.DependsOn, ", ")
				}
			}

			fmt.Fprintf(&b, "| [`%s`](%s/goal.md) | `%s` | `%s` | %s | %s%s | `%s` |\n",
				g.Slug, g.Slug, tierStr, modeStr, depsStr, g.Title, descSnippet, g.LaunchCmd)
		}
		b.WriteString("\n---\n\n")
	}

	if len(superseded) > 0 {
		b.WriteString("## 🟣 Superseded Goals\n\n")
		b.WriteString("| Goal Package | Mode | Superseded By | Reason |\n")
		b.WriteString("| :--- | :---: | :--- | :--- |\n")
		for _, g := range superseded {
			modeStr := "⚡ Indep"
			if !g.Independent {
				modeStr = "🔗 Dep"
			}
			supBy := g.SupersededBy
			if supBy == "" {
				supBy = "subsequent work"
			}
			reason := g.SupersededReason
			if reason == "" {
				reason = "superseded"
			}
			fmt.Fprintf(&b, "| [`%s`](%s/goal.md) | `%s` | `%s` | %s |\n", g.Slug, g.Slug, modeStr, supBy, reason)
		}
		b.WriteString("\n---\n\n")
	}

	if len(implemented) > 0 {
		b.WriteString("## 🟢 Implemented\n\n")
		b.WriteString("| Goal Package | Mode | Shape | Verification | Task / Ref | Commit |\n")
		b.WriteString("| :--- | :---: | :--- | :---: | :--- | :--- |\n")
		for _, g := range implemented {
			c := g.Commit
			if c == "" {
				c = "verified"
			}
			t := g.Task
			if t == "" {
				t = "-"
			}
			modeStr := "⚡ Indep"
			if !g.Independent {
				modeStr = "🔗 Dep"
			}
			shapeStr := "🚢 SHIP"
			if g.Shape == "scout" {
				shapeStr = "🔭 SCOUT"
			}
			verStr := "unverified"
			switch g.VerificationStatus {
			case "verified":
				verStr = "verified 📜"
			case "stale":
				verStr = "stale ⚠️"
			case "reopened":
				verStr = "reopened 🔁"
			}
			fmt.Fprintf(&b, "| [`%s`](%s/goal.md) | `%s` | %s | %s | %s | `%s` |\n", g.Slug, g.Slug, modeStr, shapeStr, verStr, t, c)
		}
		b.WriteString("\n")
	}

	if opts.CheckOnly {
		existingData, err := os.ReadFile(readmePath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("goals/README.md does not exist on disk")
			}
			return fmt.Errorf("read goals/README.md: %w", err)
		}
		expected := strings.TrimRight(b.String(), "\n") + "\n"
		if string(existingData) != expected {
			diffBytes := len(expected) - len(existingData)
			return fmt.Errorf("goals/README.md is out of sync with current goals state (differs by %d bytes; run 'sila goals' to update)", diffBytes)
		}
		return nil
	}

	cleanOutput := strings.TrimRight(b.String(), "\n") + "\n"
	if err := AtomicWriteFile(readmePath, []byte(cleanOutput), 0644); err != nil {
		return err
	}
	if opts.Stage || opts.CommitMsg != "" {
		cmd := exec.Command("git", append([]string{"add", "goals/README.md"}, opts.ExtraPaths...)...)
		cmd.Dir = rootDir
		_ = cmd.Run()
	}
	if opts.CommitMsg != "" {
		cmd := exec.Command("git", append([]string{"commit", "-m", opts.CommitMsg, "goals/README.md"}, opts.ExtraPaths...)...)
		cmd.Dir = rootDir
		_ = cmd.Run()
	}
	return nil
}

// CheckReadmeSync verifies that goals/README.md matches the current roadmap state
// without mutating any files on disk. Returns an error if discrepancies exist.
func CheckReadmeSync(rootDir string, res *AuditResult) error {
	return SyncReadmeWithOpts(rootDir, res, SyncReadmeOptions{CheckOnly: true})
}

// ReviewWithPlannotator triggers the locally installed Plannotator CLI to visually review a plan.
func ReviewWithPlannotator(ui render.UI, rootDir, slug string) error {
	plannotatorPath, err := exec.LookPath("plannotator")
	if err != nil {
		home, _ := os.UserHomeDir()
		candidate := filepath.Join(home, ".local", "bin", "plannotator")
		if fileExists(candidate) {
			plannotatorPath = candidate
		} else {
			return fmt.Errorf("plannotator is not installed. Install via: curl -fsSL https://plannotator.ai/install.sh | bash")
		}
	}

	rootDir = resolveGoalsRootDir(rootDir)
	targetFile := filepath.Join(rootDir, "goals", slug, "plan.md")
	if !fileExists(targetFile) {
		targetFile = filepath.Join(rootDir, "goals", slug, "goal.md")
	}
	if !fileExists(targetFile) {
		return fmt.Errorf("no plan.md or goal.md found in goals/%s", slug)
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · goals"))+fmt.Sprintf(" — launching Plannotator for %s...", targetFile))
	cmd := exec.Command(plannotatorPath, "annotate", targetFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// PrintAuditTable renders the visual goals matrix to stdout with dependency badges and sequence support.
// shapeBadge returns the visual badge for a goal's task shape.
func shapeBadge(shape string) string {
	if shape == "scout" {
		return "🔭 [SCOUT]"
	}
	return "🚢 [SHIP]"
}

// shapeBadgeDim returns a dimmed version for compact rendering.
func shapeBadgeDim(ui render.UI, shape string) string {
	if shape == "scout" {
		return ui.Dim("🔭")
	}
	return ui.Dim("🚢")
}

// tierBadge returns the visual badge for a goal's priority tier.
func tierBadge(ui render.UI, tier string) string {
	switch NormalizeTier(tier) {
	case "immediate":
		return ui.Red("🎯 [Immediate]")
	case "nice-to-have":
		return ui.Dim("✨ [Nice-to-Have]")
	case "wont-fix":
		return ui.Dim("⏸️ [Wont-Fix]")
	case "roadmap":
		fallthrough
	default:
		return ui.Yellow("🗺️ [Roadmap]")
	}
}

// tierBadgeDim returns a dimmed version for compact rendering.
func tierBadgeDim(ui render.UI, tier string) string {
	switch NormalizeTier(tier) {
	case "immediate":
		return ui.Red("🎯")
	case "nice-to-have":
		return ui.Dim("✨")
	case "wont-fix":
		return ui.Dim("⏸️")
	case "roadmap":
		fallthrough
	default:
		return ui.Yellow("🗺️")
	}
}

// PrintAuditTable renders the visual goals matrix to stdout with dependency badges and sequence support.
// History (implemented goals) renders first with noise-capping, while active goals, roadmap progress,
// and the summary/legend anchor at the bottom of the terminal viewport.
func PrintAuditTable(ui render.UI, res *AuditResult, filterMode string, showAll ...bool) {
	all := false
	if len(showAll) > 0 && showAll[0] {
		all = true
	}
	ascii := len(showAll) > 1 && showAll[1]
	var asciiBuf bytes.Buffer
	var out io.Writer = os.Stdout
	if ascii {
		out = &asciiBuf
	}

	var claimed []Goal
	var pending []Goal
	var implemented []Goal
	for _, g := range res.Goals {
		switch g.Status {
		case "claimed":
			claimed = append(claimed, g)
		case "pending":
			pending = append(pending, g)
		case "implemented":
			implemented = append(implemented, g)
		}
	}

	projHeader := ""
	if res.Project != "" {
		projHeader = " · " + ui.Magenta(ui.Bold(res.Project))
	}
	fmt.Fprintln(out, ui.Cyan(ui.Bold("sila · goals"))+projHeader+" — Goals Audit & Roadmap Matrix")
	fmt.Fprintln(out)

	// 1. History first: Implemented goals
	if (filterMode == "" || filterMode == "implemented" || filterMode == "done") && len(implemented) > 0 {
		fmt.Fprintln(out, ui.Dim("---------------------------------------------------------------"))
		verifiedBreakdown := fmt.Sprintf("(%d verified 📜, %d unverified)", res.VerifiedCount, res.UnverifiedCount)
		if res.StaleCount > 0 {
			verifiedBreakdown = fmt.Sprintf("(%d verified 📜, %d stale ⚠️, %d unverified)", res.VerifiedCount, res.StaleCount, res.UnverifiedCount)
		}
		fmt.Fprintf(out, "🟢 RECENTLY IMPLEMENTED (%d) %s:\n", len(implemented), ui.Dim(verifiedBreakdown))
		fmt.Fprintln(out, ui.Dim("---------------------------------------------------------------"))
		limit := 5
		displayList := implemented
		truncated := false
		if !all && filterMode == "" && len(implemented) > limit {
			displayList = implemented[:limit]
			truncated = true
		}
		for _, g := range displayList {
			c := g.Commit
			if c == "" {
				c = "verified"
			} else {
				c = "commit " + c
			}
			modeTag := "⚡"
			if !g.Independent {
				modeTag = "🔗"
			}
			tasksPart := ""
			if g.TasksTotal > 0 {
				tasksPart = fmt.Sprintf(" (%d tasks)", g.TasksTotal)
			}
			vBadge := ""
			switch g.VerificationStatus {
			case "verified":
				vBadge = " " + ui.Green("[verified 📜]")
			case "stale":
				vBadge = " " + ui.Yellow("[stale ⚠️]")
			case "unverified":
				vBadge = " " + ui.Dim("[unverified]")
			}
			fmt.Fprintf(out, "  ✔ %s %s %-42s [%s]%s%s\n", shapeBadgeDim(ui, g.Shape), modeTag, g.Slug, ui.Dim(c), ui.Green(tasksPart), vBadge)
		}
		if truncated {
			fmt.Fprintf(out, "  %s\n", ui.Dim(fmt.Sprintf("... and %d earlier implemented goals on disk.", len(implemented)-limit)))
			fmt.Fprintf(out, "  %s\n", ui.Cyan("→ View full history: sila goals implemented (or --all, --toon, --json)"))
		}
		fmt.Fprintln(out)
	}

	// 2. Active in-progress goals
	if (filterMode == "" || filterMode == "claimed") && len(claimed) > 0 {
		fmt.Fprintf(out, "🔵 IN PROGRESS / CLAIMED GOALS (%d):\n", len(claimed))
		fmt.Fprintln(out, ui.Dim("---------------------------------------------------------------"))
		for _, g := range claimed {
			agent := "agent"
			dur := "recently"
			note := ""
			if g.Claim != nil {
				if g.Claim.ClaimedBy != "" {
					agent = g.Claim.ClaimedBy
				} else if g.Claim.Agent != "" {
					agent = g.Claim.Agent
				}
				if !g.Claim.ClaimedAt.IsZero() {
					dur = time.Since(g.Claim.ClaimedAt).Round(time.Minute).String() + " ago"
				}
				note = g.Claim.Note
			}
			modeTag := ui.Cyan("⚡ [Independent]")
			if !g.Independent {
				modeTag = ui.Yellow("🔗 [Dependent]")
			}
			staleNotice := ""
			if g.Claim != nil && g.Claim.Stale {
				staleNotice = fmt.Sprintf(" %s", ui.Red(fmt.Sprintf("[STALE: %s]", g.Claim.StaleReason)))
			}
			reopenBadge := ""
			if g.Reopened {
				reopenBadge = " " + ui.Yellow("🔁 [REOPENED]")
			}
			fmt.Fprintf(out, "  🔒 %s %s %s %s%s %s%s\n", ui.Bold(g.Slug), shapeBadgeDim(ui, g.Shape), tierBadge(ui, g.Tier), modeTag, reopenBadge, ui.Cyan(fmt.Sprintf("(@%s · %s)", agent, dur)), staleNotice)
			fmt.Fprintf(out, "     Title:  %s\n", g.Title)
			if g.Reopened && g.ReopenReason != "" {
				byAgent := ""
				if g.Reopen != nil && g.Reopen.ReopenedBy != "" {
					byAgent = fmt.Sprintf(" (by @%s)", g.Reopen.ReopenedBy)
				}
				fmt.Fprintf(out, "     Reopen: %s%s\n", ui.Yellow(g.ReopenReason), byAgent)
			}
			statusBadge := ""
			if g.Claim != nil && g.Claim.Status != "" {
				switch strings.ToLower(g.Claim.Status) {
				case "blocked":
					statusBadge = ui.Red("⛔ [BLOCKED]")
				case "evaluating", "review":
					statusBadge = ui.Yellow("🔍 [EVALUATING]")
				case "done", "completed":
					statusBadge = ui.Green("✔ [DONE]")
				case "in_progress", "in-progress":
					statusBadge = ui.Cyan("🔵 [IN-PROGRESS]")
				default:
					statusBadge = ui.Cyan(fmt.Sprintf("[%s]", strings.ToUpper(g.Claim.Status)))
				}
			}
			if statusBadge != "" && note != "" {
				fmt.Fprintf(out, "     Status: %s %s\n", statusBadge, ui.Yellow(note))
			} else if statusBadge != "" {
				fmt.Fprintf(out, "     Status: %s\n", statusBadge)
			} else if note != "" {
				fmt.Fprintf(out, "     Status: %s\n", ui.Yellow(note))
			}
			if len(g.FilesToTouch) > 0 {
				fmt.Fprintf(out, "     Touch:  %s\n", strings.Join(g.FilesToTouch, ", "))
			}
			if len(g.Personas) > 0 {
				fmt.Fprintf(out, "     Squad:  %s\n", ui.Cyan(strings.Join(g.Personas, ", ")))
			}
			if g.TasksTotal > 0 {
				pct := (g.TasksMet * 100) / g.TasksTotal
				bar := renderProgressBar(ui, g.TasksMet, g.TasksTotal, GoalProgressBarWidth)
				if ascii {
					bar = renderProgressBarASCII(g.TasksMet, g.TasksTotal, GoalProgressBarWidth)
				}
				fmt.Fprintf(out, "     Plan:   %s %d/%d tasks (%d%%)\n", bar, g.TasksMet, g.TasksTotal, pct)
			}
			fmt.Fprintf(out, "     Launch: %s\n\n", ui.Dim(g.LaunchCmd))
		}
	}

	renderGoalItem := func(g Goal) {
		modeTag := ui.Cyan("⚡ [Independent]")
		if !g.Independent {
			if g.Blocked {
				modeTag = ui.Red(fmt.Sprintf("⛔ [Blocked by: %s]", strings.Join(g.UnmetDeps, ", ")))
			} else {
				modeTag = ui.Green("🔗 [Ready · Dependencies Met]")
			}
		}
		if len(g.FileConflicts) > 0 {
			modeTag += " " + ui.Red("⚠️ [File Conflict]")
		}
		seqTag := ""
		if g.Sequence > 0 {
			seqTag = fmt.Sprintf(" %s", ui.Dim(fmt.Sprintf("(Seq #%d)", g.Sequence)))
		}
		reopenBadge := ""
		if g.Reopened {
			reopenBadge = " " + ui.Yellow("🔁 [REOPENED]")
		}
		fmt.Fprintf(out, "  ▶ %s %s %s %s%s%s\n", ui.Bold(g.Slug), shapeBadgeDim(ui, g.Shape), tierBadge(ui, g.Tier), modeTag, reopenBadge, seqTag)
		fmt.Fprintf(out, "    Title:  %s\n", g.Title)
		if g.Reopened && g.ReopenReason != "" {
			byAgent := ""
			if g.Reopen != nil && g.Reopen.ReopenedBy != "" {
				byAgent = fmt.Sprintf(" (by @%s)", g.Reopen.ReopenedBy)
			}
			fmt.Fprintf(out, "    Reopen: %s%s\n", ui.Yellow(g.ReopenReason), byAgent)
		}
		if g.Blocked {
			if g.BlockedReason != "" {
				fmt.Fprintf(out, "    Status: %s\n", ui.Red(g.BlockedReason))
			} else if len(g.RootBlockers) > 0 && !sliceEqual(g.UnmetDeps, g.RootBlockers) {
				fmt.Fprintf(out, "    Status: %s\n", ui.Red(fmt.Sprintf("Must implement %s first (root blockers: %s)", strings.Join(g.UnmetDeps, ", "), strings.Join(g.RootBlockers, ", "))))
			} else {
				fmt.Fprintf(out, "    Status: %s\n", ui.Red(fmt.Sprintf("Must implement %s first", strings.Join(g.UnmetDeps, ", "))))
			}
		} else if len(g.FileConflicts) > 0 {
			for _, cf := range g.FileConflicts {
				fmt.Fprintf(out, "    Status: %s\n", ui.Red(fmt.Sprintf("Contested: touches %v currently held by active goal %s (@%s)", cf.OverlappingFiles, cf.ConflictingGoal, cf.HolderAgent)))
			}
		} else if g.Superseded {
			fmt.Fprintf(out, "    Status: %s\n", ui.Dim(fmt.Sprintf("Superseded: %s", g.SupersededReason)))
		} else {
			if g.PendingReason != "" {
				fmt.Fprintf(out, "    Status: %s\n", ui.Yellow(g.PendingReason))
			}
			fmt.Fprintf(out, "    Claim:  %s\n", ui.Cyan(fmt.Sprintf("sila goals claim %s --agent=<name>", g.Slug)))
		}
		if len(g.FilesToTouch) > 0 {
			fmt.Fprintf(out, "    Touch:  %s\n", strings.Join(g.FilesToTouch, ", "))
		}
		if len(g.Personas) > 0 {
			fmt.Fprintf(out, "    Squad:  %s\n", ui.Cyan(strings.Join(g.Personas, ", ")))
		}
		if g.TasksTotal > 0 {
			pct := (g.TasksMet * 100) / g.TasksTotal
			bar := renderProgressBar(ui, g.TasksMet, g.TasksTotal, GoalProgressBarWidth)
			if ascii {
				bar = renderProgressBarASCII(g.TasksMet, g.TasksTotal, GoalProgressBarWidth)
			}
			fmt.Fprintf(out, "    Plan:   %s %d/%d tasks (%d%%)\n", bar, g.TasksMet, g.TasksTotal, pct)
		}
		if len(g.SkippedCandidates) > 0 {
			label := "commits"
			if len(g.SkippedCandidates) == 1 {
				label = "commit"
			}
			fmt.Fprintf(out, "    Note:   %d candidate %s skipped as docs/test/chore(goals) (non-closure); certify with a feat/fix/refactor/perf/chore prefix: %s\n",
				len(g.SkippedCandidates), label, strings.Join(g.SkippedCandidates, ", "))
		}
		if len(g.NearMisses) > 0 {
			label := "commits mention"
			if len(g.NearMisses) == 1 {
				label = "commit mentions"
			}
			fmt.Fprintf(out, "    Near-miss: %d %s slug in prose without trailer/path: %s\n",
				len(g.NearMisses), label, strings.Join(g.NearMisses, ", "))
			fmt.Fprintf(out, "               Certify with: git commit --amend -m \"... (goals/%s/goal.md)\" or trailer Fixes-Goal: %s\n", g.Slug, g.Slug)
		}
		fmt.Fprintf(out, "    Launch: %s\n\n", ui.Dim(g.LaunchCmd))
	}

	renderGoalGroup := func(title string, list []Goal) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(out, "%s (%d):\n", title, len(list))
		fmt.Fprintln(out, ui.Dim("---------------------------------------------------------------"))
		for _, g := range list {
			renderGoalItem(g)
		}
	}

	// 3. Pending goal groups (contested, blocked, then ready to claim)
	showPending := filterMode == "" || filterMode == "pending" || filterMode == "ready" || filterMode == "independent" || filterMode == "blocked" || filterMode == "sequence" || filterMode == "contested"
	if showPending && len(pending) > 0 {
		var readyGoals, blockedGoals, contestedGoals []Goal
		for _, g := range pending {
			if g.Blocked {
				blockedGoals = append(blockedGoals, g)
			} else if len(g.FileConflicts) > 0 {
				contestedGoals = append(contestedGoals, g)
			} else {
				readyGoals = append(readyGoals, g)
			}
		}

		switch filterMode {
		case "ready":
			renderGoalGroup("🟢 UNBLOCKED & CONFLICT-FREE GOALS READY TO EXECUTE", readyGoals)
		case "blocked":
			renderGoalGroup("⛔ BLOCKED GOALS (WAITING ON PREREQUISITES)", blockedGoals)
		case "contested":
			renderGoalGroup("⚠️ CONTESTED GOALS (FILE OVERLAPS WITH ACTIVE WORKERS)", contestedGoals)
		case "independent":
			var indepList []Goal
			for _, g := range pending {
				if g.Independent {
					indepList = append(indepList, g)
				}
			}
			renderGoalGroup("⚡ INDEPENDENT / PARALLELIZABLE GOALS", indepList)
		case "pending":
			renderGoalGroup("🟡 ALL PENDING GOALS", pending)
		default: // filterMode == "" (default audit matrix)
			renderGoalGroup("⚠️ CONTESTED GOALS (FILE OVERLAPS WITH ACTIVE WORKERS)", contestedGoals)
			renderGoalGroup("⛔ BLOCKED GOALS (WAITING ON PREREQUISITES)", blockedGoals)
			renderGoalGroup("🟡 GOALS READY TO CLAIM & EXECUTE", readyGoals)
		}
	}

	// 4. Status summary at the bottom (immediate terminal viewport focus)
	fmt.Fprintln(out, ui.Dim("---------------------------------------------------------------"))
	type summaryRow struct{ label, value string }
	rows := make([]summaryRow, 0, 14)
	if res.Project != "" {
		dirNote := ""
		if res.ProjectDir != "" {
			dirNote = " " + ui.Dim("("+res.ProjectDir+")")
		}
		rows = append(rows, summaryRow{"📁 Project:", ui.Magenta(ui.Bold(res.Project)) + dirNote})
	}
	rows = append(rows, summaryRow{"Total Goals:", fmt.Sprint(res.Total)})
	contestedTag := ""
	if res.ContestedCount > 0 {
		contestedTag = fmt.Sprintf(", %s contested ⚠️", ui.Red(fmt.Sprint(res.ContestedCount)))
	}
	rows = append(rows, summaryRow{"🟡 Ready to Pick:", fmt.Sprintf("%s (%s independent ⚡, %s unblocked 🔗%s)",
		ui.Yellow(fmt.Sprint(res.ReadyCount)), ui.Cyan(fmt.Sprint(res.ReadyIndependentCount)), ui.Green(fmt.Sprint(res.ReadyUnblockedCount)), contestedTag)})
	if res.ClaimedCount > 0 {
		rows = append(rows, summaryRow{"🔵 In Progress:", fmt.Sprintf("%s (🔒 claimed)", ui.Cyan(fmt.Sprint(res.ClaimedCount)))})
	}
	if res.BlockedCount > 0 {
		rows = append(rows, summaryRow{"⛔ Blocked on Deps:", ui.Red(fmt.Sprint(res.BlockedCount))})
	}
	if res.StaleCount > 0 {
		rows = append(rows, summaryRow{"🟢 Implemented:", fmt.Sprintf("%s (%s verified 📜, %s stale ⚠️, %s unverified)",
			ui.Green(fmt.Sprint(res.ImplementedCount)), ui.Green(fmt.Sprint(res.VerifiedCount)), ui.Yellow(fmt.Sprint(res.StaleCount)), ui.Dim(fmt.Sprint(res.UnverifiedCount)))})
	} else if res.ImplementedCount > 0 {
		rows = append(rows, summaryRow{"🟢 Implemented:", fmt.Sprintf("%s (%s verified 📜, %s unverified)",
			ui.Green(fmt.Sprint(res.ImplementedCount)), ui.Green(fmt.Sprint(res.VerifiedCount)), ui.Dim(fmt.Sprint(res.UnverifiedCount)))})
	} else {
		rows = append(rows, summaryRow{"🟢 Implemented:", ui.Green(fmt.Sprint(res.ImplementedCount))})
	}
	if res.SupersededCount > 0 {
		rows = append(rows, summaryRow{"🟣 Superseded:", ui.Dim(fmt.Sprint(res.SupersededCount))})
	}
	if res.ReopenedCount > 0 {
		rows = append(rows, summaryRow{"🔁 Reopened:", fmt.Sprintf("%s (needs remediation)", ui.Yellow(fmt.Sprint(res.ReopenedCount)))})
	}
	if res.ContestedCount > 0 {
		rows = append(rows, summaryRow{"⚠️  Contested Files:", fmt.Sprintf("%s (file overlap with active workers)", ui.Red(fmt.Sprint(res.ContestedCount)))})
	}
	if res.DraftCount > 0 {
		rows = append(rows, summaryRow{"⚪ Draft / Setup:", ui.Dim(fmt.Sprint(res.DraftCount))})
	}
	if res.ImmediateCount > 0 || res.RoadmapCount > 0 || res.NiceToHaveCount > 0 || res.WontFixCount > 0 {
		rows = append(rows, summaryRow{"🎯 By Tier:", fmt.Sprintf("%s immediate, %s roadmap, %s nice-to-have, %s wont-fix",
			ui.Red(fmt.Sprint(res.ImmediateCount)), ui.Yellow(fmt.Sprint(res.RoadmapCount)), ui.Dim(fmt.Sprint(res.NiceToHaveCount)), ui.Dim(fmt.Sprint(res.WontFixCount)))})
	}
	if res.TotalTasks > 0 {
		pct := (res.CompletedTasks * 100) / res.TotalTasks
		bar := renderProgressBar(ui, res.CompletedTasks, res.TotalTasks, GoalProgressBarWidth)
		if ascii {
			bar = renderProgressBarASCII(res.CompletedTasks, res.TotalTasks, GoalProgressBarWidth)
		}
		rows = append(rows, summaryRow{"📋 Task Progress:", fmt.Sprintf("%s %d/%d completed (%d%%)", bar, res.CompletedTasks, res.TotalTasks, pct)})
	}
	labelWidth := 0
	for _, row := range rows {
		label := row.label
		if ascii {
			label = asciiGlyphTags(label)
		}
		if width := goalsDisplayWidth(label); width > labelWidth {
			labelWidth = width
		}
	}
	for _, row := range rows {
		label := row.label
		if ascii {
			label = asciiGlyphTags(label)
		}
		pad := labelWidth - goalsDisplayWidth(label)
		if pad < 1 {
			pad = 1
		}
		fmt.Fprintf(out, "  %s%s %s\n", label, strings.Repeat(" ", pad), row.value)
	}
	if len(res.Cycles) > 0 {
		fmt.Fprintf(out, "  ⚠️  %s\n", ui.Red(fmt.Sprintf("CIRCULAR DEPENDENCIES DETECTED (%d):", len(res.Cycles))))
		for _, c := range res.Cycles {
			fmt.Fprintf(out, "     %s\n", ui.Red(strings.Join(c, " -> ")))
		}
	}
	legend := "Legend: 🚢 ship · 🗺️ roadmap · 🎯 immediate · ⚡ independent · 🔗 dependent · 🔒 claimed · ▶ ready"
	if ascii {
		legend = "Legend: ship · roadmap · immediate · indep · dep · claimed · ready"
	}
	fmt.Fprintln(out, ui.Dim(legend))
	fmt.Fprintln(out, ui.Dim("---------------------------------------------------------------"))
	if ascii {
		fmt.Fprint(os.Stdout, asciiGoalsOutput(asciiBuf.String()))
	}
}

// RenderGoalsTOON formats the audit result or filtered goals into Token-Oriented Object Notation (TOON).
func RenderGoalsTOON(res *AuditResult, filterMode string) string {
	if res == nil {
		return "goals[0]{slug,status,mode,shape,title,deps}:\n"
	}

	switch filterMode {
	case "implemented", "done":
		var rows [][]string
		for _, g := range res.Goals {
			if g.Status == "implemented" {
				c := g.Commit
				if c == "" {
					c = "verified"
				}
				mode := "independent"
				if !g.Independent {
					mode = "dependent"
				}
				tasks := ""
				if g.TasksTotal > 0 {
					tasks = fmt.Sprintf("%d/%d", g.TasksMet, g.TasksTotal)
				}
				rows = append(rows, []string{g.Slug, g.Shape, mode, c, tasks, g.Title})
			}
		}
		return render.FormatTOONTable("implemented_goals", []string{"slug", "shape", "mode", "commit", "tasks", "title"}, rows)

	case "ready":
		var rows [][]string
		for _, g := range res.Goals {
			if g.Status == "pending" && !g.Blocked && len(g.FileConflicts) == 0 {
				mode := "independent"
				if !g.Independent {
					mode = "dependent"
				}
				tasks := ""
				if g.TasksTotal > 0 {
					tasks = fmt.Sprintf("%d/%d", g.TasksMet, g.TasksTotal)
				}
				rows = append(rows, []string{g.Slug, g.Shape, mode, tasks, g.Title})
			}
		}
		return render.FormatTOONTable("ready_goals", []string{"slug", "shape", "mode", "tasks", "title"}, rows)

	case "claimed":
		var rows [][]string
		for _, g := range res.Goals {
			if g.Status == "claimed" {
				agent := "agent"
				status := "in_progress"
				if g.Claim != nil {
					if g.Claim.ClaimedBy != "" {
						agent = g.Claim.ClaimedBy
					} else if g.Claim.Agent != "" {
						agent = g.Claim.Agent
					}
					if g.Claim.Status != "" {
						status = g.Claim.Status
					}
				}
				tasks := ""
				if g.TasksTotal > 0 {
					tasks = fmt.Sprintf("%d/%d", g.TasksMet, g.TasksTotal)
				}
				rows = append(rows, []string{g.Slug, g.Shape, agent, status, tasks, g.Title})
			}
		}
		return render.FormatTOONTable("claimed_goals", []string{"slug", "shape", "agent", "status", "tasks", "title"}, rows)

	case "blocked":
		var rows [][]string
		for _, g := range res.Goals {
			if g.Status == "pending" && g.Blocked {
				deps := strings.Join(g.UnmetDeps, ";")
				rows = append(rows, []string{g.Slug, g.Shape, deps, g.BlockedReason, g.Title})
			}
		}
		return render.FormatTOONTable("blocked_goals", []string{"slug", "shape", "unmet_deps", "reason", "title"}, rows)

	default: // filterMode == "" (complete audit matrix)
		taskProg := ""
		if res.TotalTasks > 0 {
			pct := (res.CompletedTasks * 100) / res.TotalTasks
			taskProg = fmt.Sprintf("%d/%d (%d%%)", res.CompletedTasks, res.TotalTasks, pct)
		}
		summaryFields := [][2]string{
			{"project", res.Project},
			{"total", strconv.Itoa(res.Total)},
			{"implemented", strconv.Itoa(res.ImplementedCount)},
			{"claimed", strconv.Itoa(res.ClaimedCount)},
			{"ready", strconv.Itoa(res.ReadyCount)},
			{"blocked", strconv.Itoa(res.BlockedCount)},
			{"contested", strconv.Itoa(res.ContestedCount)},
		}
		if taskProg != "" {
			summaryFields = append(summaryFields, [2]string{"tasks", taskProg})
		}
		summaryTOON := render.FormatTOONObject("goals_summary", summaryFields)

		var rows [][]string
		for _, g := range res.Goals {
			mode := "independent"
			if !g.Independent {
				mode = "dependent"
			}
			deps := strings.Join(g.DependsOn, ";")
			rows = append(rows, []string{g.Slug, g.Status, mode, g.Shape, g.Title, deps})
		}
		tableTOON := render.FormatTOONTable("goals", []string{"slug", "status", "mode", "shape", "title", "deps"}, rows)
		return summaryTOON + "\n" + tableTOON
	}
}

// FileExists returns true if path exists and is not a directory.
func FileExists(path string) bool {
	return fileExists(path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// findGoalFile returns the path of base inside dir, accepting case variants
// such as PLAN.md next to plan.md. It returns "" and false when absent.
func findGoalFile(dir, base string) (string, bool) {
	if p := filepath.Join(dir, base); fileExists(p) {
		return p, true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(e.Name(), base) {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

func cleanWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func cleanSlug(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	reg := regexp.MustCompile(`[^a-z0-9-]+`)
	s = reg.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func slugToTitle(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// isCheckboxLine reports whether a trimmed line is a markdown task checkbox
// (- [ ], * [x], + [X], ...). Single definition shared by counting, task
// extraction, and toggling so the three never diverge.
func isCheckboxLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [X]") ||
		strings.HasPrefix(trimmed, "* [x]") || strings.HasPrefix(trimmed, "* [X]") ||
		strings.HasPrefix(trimmed, "+ [x]") || strings.HasPrefix(trimmed, "+ [X]") ||
		strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "* [ ]") || strings.HasPrefix(trimmed, "+ [ ]")
}

// splitNumberedPrefix splits a Plannotator-style "N. <rest>" numbered step
// marker. Only the "N." form counts: "N)" never matches, and version-like
// "1.26" never matches because no whitespace follows the dot. Empty task
// text ("1. " alone) is not a step.
func splitNumberedPrefix(trimmed string) (string, bool) {
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(trimmed) || trimmed[i] != '.' || (trimmed[i+1] != ' ' && trimmed[i+1] != '\t') {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[i+1:])
	if rest == "" {
		return "", false
	}
	return rest, true
}

// isCheckedMarker reports whether numbered-step text carries a checked state
// marker ("[x]"/"[X]" written by ToggleGoalTasks).
func isCheckedMarker(rest string) bool {
	return strings.HasPrefix(rest, "[x]") || strings.HasPrefix(rest, "[X]")
}

// usesStepHeadings reports whether a plan tracks work as "## Step N" headings.
// When it does, those headings are the tasks, and numbered prose under them is
// detail rather than a checklist.
func usesStepHeadings(content string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		if stepHeadingRE.MatchString(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

// stepHeadingTask parses a step heading into its task text and completion
// state. ToggleGoalTasks writes "[x]"/"[ ]" as a trailing marker, so the
// parser reads the same marker back.
func stepHeadingTask(trimmed string) (text string, completed bool, ok bool) {
	m := stepHeadingRE.FindStringSubmatch(trimmed)
	if m == nil {
		return "", false, false
	}
	text = strings.TrimSpace(m[1])
	for _, marker := range []string{"[x]", "[X]", "[ ]"} {
		if strings.HasSuffix(text, marker) {
			completed = marker != "[ ]"
			text = strings.TrimSpace(strings.TrimSuffix(text, marker))
			break
		}
	}
	if text == "" {
		return "", false, false
	}
	return text, completed, true
}

// toggleStepHeadingLine rewrites a step heading's trailing completion marker,
// preserving the rest of the heading text.
func toggleStepHeadingLine(line string, completed bool) string {
	line = strings.TrimRight(line, " \t")
	for _, marker := range []string{"[x]", "[X]", "[ ]"} {
		if before, ok := strings.CutSuffix(line, marker); ok {
			line = strings.TrimRight(before, " \t")
			break
		}
	}
	mark := "[ ]"
	if completed {
		mark = "[x]"
	}
	return line + " " + mark
}

// countCheckboxes counts completed (- [x]) and total (- [ ] / - [x]) markdown task checkboxes.
// When the content holds no checkboxes at all, it falls back to the plan's
// step headings ("## Step N — Title") when present, or to numbered steps
// (1. 2. 3.) otherwise: plain "N. text" counts as an open step, "N. [x] text"
// as a met step. The fallback never mixes with checkboxes, so mixed files keep
// checkbox-only counts and nothing is double-counted.
func countCheckboxes(content string) (met, total int) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !isCheckboxLine(trimmed) {
			continue
		}
		if len(trimmed) > 4 && (trimmed[3] == 'x' || trimmed[3] == 'X') {
			met++
		}
		total++
	}
	if total > 0 {
		return met, total
	}
	steps := usesStepHeadings(content)
	for _, line := range lines {
		if steps {
			if _, completed, ok := stepHeadingTask(strings.TrimSpace(line)); ok {
				if completed {
					met++
				}
				total++
			}
			continue
		}
		rest, ok := splitNumberedPrefix(strings.TrimSpace(line))
		if !ok {
			continue
		}
		if isCheckedMarker(rest) {
			met++
		}
		total++
	}
	return met, total
}

// GoalProgressBarWidth is the single progress width for goals surfaces.
const GoalProgressBarWidth = 10

func asciiGlyphTags(label string) string {
	return strings.NewReplacer(
		"📁 Project:", "Project:",
		"🟡 Ready to Pick:", "Ready:",
		"🔵 In Progress:", "In Progress:",
		"⛔ Blocked on Deps:", "Blocked:",
		"🟢 Implemented:", "Implemented:",
		"🟣 Superseded:", "Superseded:",
		"🔁 Reopened:", "Reopened:",
		"⚠️  Contested Files:", "Contested:",
		"⚪ Draft / Setup:", "Draft:",
		"🎯 By Tier:", "By Tier:",
		"📋 Task Progress:", "Tasks:",
	).Replace(label)
}

func goalsDisplayWidth(s string) int {
	width := 0
	for _, r := range s {
		switch {
		case r == 0xfe0f || (r >= 0x0300 && r <= 0x036f):
			continue
		case r >= 0x1f000 || (r >= 0x2600 && r <= 0x27bf):
			width += 2
		default:
			width++
		}
	}
	return width
}

func asciiGoalsOutput(s string) string {
	s = regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(s, "")
	return strings.NewReplacer(
		"🗺", "[roadmap]", "⚠", "[warn]", "🚢", "[ship]", "🎯", "[immediate]",
		"⚡", "[indep]", "🔗", "[dep]", "🔒", "[claimed]", "▶", "[ready]",
		"🟢", "[done]", "🟡", "[pending]", "🔵", "[active]", "🟣", "[superseded]",
		"⛔", "[blocked]", "✔", "[ok]", "✖", "[fail]", "📁", "[project]",
		"📋", "[tasks]", "📜", "[verified]", "🔁", "[reopened]", "🔍", "[evaluating]",
		"⚪", "[draft]", "·", "|", "—", "-", "→", "->", "\ufe0f", "",
	).Replace(s)
}

func renderProgressBarASCII(met, total, width int) string {
	if total <= 0 {
		return ""
	}
	if width <= 0 {
		width = GoalProgressBarWidth
	}
	filled := min((met*width)/total, width)
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
}

func renderProgressBar(ui render.UI, met, total, width int) string {
	if total <= 0 {
		return ""
	}
	if width <= 0 {
		width = 10
	}
	filled := min((met*width)/total, width)
	empty := width - filled
	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	if met == total {
		return "[" + ui.Green(bar) + "]"
	} else if met > 0 {
		return "[" + ui.Cyan(bar) + "]"
	}
	return "[" + ui.Dim(bar) + "]"
}

// GoalTask represents a discrete checklist step extracted from plan.md, goal.md, or work-package.json.
type GoalTask struct {
	ID               string `json:"id,omitempty"`
	Index            int    `json:"index"`
	Text             string `json:"text"`
	Completed        bool   `json:"completed"`
	File             string `json:"file"`
	AcceptanceStatus string `json:"acceptance_status,omitempty"`
}

// GetGoalTasks returns all tasks for a goal package. When a work-package.json exists,
// it acts as the authoritative task definition. Otherwise, tasks are parsed from plan.md and goal.md.
func GetGoalTasks(rootDir, slug string) ([]GoalTask, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)

	// If structured work-package.json exists, it is the task authority
	if wpTasks, err := LoadWorkPackageTasksIfPresent(goalDir); err != nil {
		return nil, err
	} else if wpTasks != nil {
		return wpTasks, nil
	}

	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}

	var allTasks []GoalTask
	idx := 1

	// Check plan.md first
	planPath := filepath.Join(goalDir, "plan.md")
	if data, err := os.ReadFile(planPath); err == nil {
		tasks := parseTasksFromContent(string(data), "plan.md", idx)
		allTasks = append(allTasks, tasks...)
		idx += len(tasks)
	}

	// Also check goal.md if plan.md had no tasks
	if len(allTasks) == 0 {
		goalPath := filepath.Join(goalDir, "goal.md")
		if data, err := os.ReadFile(goalPath); err == nil {
			tasks := parseTasksFromContent(string(data), "goal.md", idx)
			allTasks = append(allTasks, tasks...)
		}
	}

	return allTasks, nil
}

func parseTasksFromContent(content, file string, startIdx int) []GoalTask {
	var tasks []GoalTask
	cur := startIdx
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		isDone := false
		var text string
		if strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [X]") ||
			strings.HasPrefix(trimmed, "* [x]") || strings.HasPrefix(trimmed, "* [X]") ||
			strings.HasPrefix(trimmed, "+ [x]") || strings.HasPrefix(trimmed, "+ [X]") {
			isDone = true
			if len(trimmed) > 5 {
				text = strings.TrimSpace(trimmed[5:])
			}
		} else if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "* [ ]") || strings.HasPrefix(trimmed, "+ [ ]") {
			isDone = false
			if len(trimmed) > 5 {
				text = strings.TrimSpace(trimmed[5:])
			}
		}
		if text != "" {
			tasks = append(tasks, GoalTask{
				Index:     cur,
				Text:      text,
				Completed: isDone,
				File:      file,
			})
			cur++
		}
	}
	if len(tasks) > 0 {
		return tasks
	}
	// Fallback: step headings win when present, otherwise numbered steps are
	// the tasks. This mirrors countCheckboxes and ToggleGoalTasks so listings,
	// counts, and toggle indices never diverge.
	steps := usesStepHeadings(content)
	for line := range strings.SplitSeq(content, "\n") {
		var text string
		completed := false
		if steps {
			var ok bool
			text, completed, ok = stepHeadingTask(strings.TrimSpace(line))
			if !ok {
				continue
			}
		} else {
			rest, ok := splitNumberedPrefix(strings.TrimSpace(line))
			if !ok {
				continue
			}
			text = rest
			if isCheckedMarker(rest) {
				completed = true
				text = strings.TrimSpace(rest[3:])
			}
			if text == "" {
				continue
			}
		}
		tasks = append(tasks, GoalTask{
			Index:     cur,
			Text:      text,
			Completed: completed,
			File:      file,
		})
		cur++
	}
	return tasks
}

// ParseTaskIndices parses a slice of task index strings supporting integers, comma lists ("1,2,3"), and ranges ("1-3").
func ParseTaskIndices(args []string) ([]int, error) {
	var indices []int
	seen := make(map[int]bool)
	for _, arg := range args {
		parts := strings.SplitSeq(arg, ",")
		for p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if strings.Contains(p, "-") {
				rangeParts := strings.SplitN(p, "-", 2)
				start, err1 := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
				end, err2 := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
				if err1 != nil || err2 != nil || start < 1 || end < start {
					return nil, fmt.Errorf("invalid task range %q", p)
				}
				for i := start; i <= end; i++ {
					if !seen[i] {
						seen[i] = true
						indices = append(indices, i)
					}
				}
			} else {
				idx, err := strconv.Atoi(p)
				if err != nil || idx < 1 {
					return nil, fmt.Errorf("invalid task index %q", p)
				}
				if !seen[idx] {
					seen[idx] = true
					indices = append(indices, idx)
				}
			}
		}
	}
	if len(indices) == 0 {
		return nil, fmt.Errorf("no task indices provided")
	}
	sort.Ints(indices)
	return indices, nil
}

// toggleNumberedLine rewrites a numbered step into a checked ([x]) or
// unchecked ([ ]) state, preserving indentation and the original step number.
// "2. Migrate schema" becomes "2. [x] Migrate schema"; an existing marker is
// replaced rather than stacked.
func toggleNumberedLine(line string, completed bool) string {
	trimmed := strings.TrimSpace(line)
	lead := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	rest := strings.TrimSpace(trimmed[i+1:])
	if len(rest) >= 3 && (strings.HasPrefix(rest, "[x]") || strings.HasPrefix(rest, "[X]") || strings.HasPrefix(rest, "[ ]")) {
		rest = strings.TrimSpace(rest[3:])
	}
	mark := " "
	if completed {
		mark = "x"
	}
	return lead + trimmed[:i] + ". [" + mark + "] " + rest
}

// ToggleGoalTasks updates the completion state for multiple 1-based task indices in a single pass.
func ToggleGoalTasks(rootDir, slug string, taskIndices []int, completed bool) ([]*GoalTask, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	if len(taskIndices) == 0 {
		return nil, fmt.Errorf("no task indices provided")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)

	// Structured tasks reject checkbox mutation until evidence acceptance is implemented
	if _, err := os.Stat(filepath.Join(goalDir, "work-package.json")); err == nil {
		return nil, fmt.Errorf("TASK_EVIDENCE_REQUIRED: structured goal tasks cannot be toggled via checkbox; verified evidence is required")
	}

	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}

	planPath := filepath.Join(goalDir, "plan.md")
	targetFile := planPath
	if !fileExists(planPath) {
		targetFile = filepath.Join(goalDir, "goal.md")
	}

	data, err := os.ReadFile(targetFile)
	if err != nil {
		return nil, err
	}

	indexMap := make(map[int]bool)
	for _, idx := range taskIndices {
		indexMap[idx] = true
	}

	lines := strings.Split(string(data), "\n")
	cur := 1
	var modifiedTasks []*GoalTask
	changed := false

	// Task identity matches GetGoalTasks: checkboxes win when present, then
	// step headings, then numbered steps. Mixed files stay checkbox-only so
	// toggle indices always agree with the task listing.
	numberedMode := true
	for _, line := range lines {
		if isCheckboxLine(strings.TrimSpace(line)) {
			numberedMode = false
			break
		}
	}
	stepMode := numberedMode && usesStepHeadings(string(data))

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isCheckbox := !numberedMode && isCheckboxLine(trimmed)
		isStep := false
		isNumbered := false
		if numberedMode {
			if stepMode {
				_, _, isStep = stepHeadingTask(trimmed)
			} else {
				_, isNumbered = splitNumberedPrefix(trimmed)
			}
		}

		if isCheckbox {
			if indexMap[cur] {
				prefixIdx := strings.Index(line, "[")
				closeIdx := strings.Index(line, "]")
				if prefixIdx >= 0 && closeIdx > prefixIdx {
					mark := " "
					if completed {
						mark = "x"
					}
					lines[i] = line[:prefixIdx+1] + mark + line[closeIdx:]
					changed = true
					text := ""
					if len(trimmed) > 5 {
						text = strings.TrimSpace(trimmed[5:])
					}
					modifiedTasks = append(modifiedTasks, &GoalTask{
						Index:     cur,
						Text:      text,
						Completed: completed,
						File:      filepath.Base(targetFile),
					})
				}
			}
			cur++
		} else if isStep {
			if indexMap[cur] {
				lines[i] = toggleStepHeadingLine(line, completed)
				changed = true
				text, _, _ := stepHeadingTask(strings.TrimSpace(lines[i]))
				modifiedTasks = append(modifiedTasks, &GoalTask{
					Index:     cur,
					Text:      text,
					Completed: completed,
					File:      filepath.Base(targetFile),
				})
			}
			cur++
		} else if isNumbered {
			if indexMap[cur] {
				lines[i] = toggleNumberedLine(line, completed)
				changed = true
				rest, _ := splitNumberedPrefix(strings.TrimSpace(lines[i]))
				text := rest
				if isCheckedMarker(rest) {
					text = strings.TrimSpace(rest[3:])
				}
				modifiedTasks = append(modifiedTasks, &GoalTask{
					Index:     cur,
					Text:      text,
					Completed: completed,
					File:      filepath.Base(targetFile),
				})
			}
			cur++
		}
	}

	if len(modifiedTasks) == 0 {
		return nil, fmt.Errorf("none of the requested task indices were found in %s", filepath.Base(targetFile))
	}

	if changed {
		if err := AtomicWriteFile(targetFile, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			return nil, err
		}
	}
	return modifiedTasks, nil
}

// ToggleGoalTask updates the completion state of the 1-based taskIndex for a goal package.
func ToggleGoalTask(rootDir, slug string, taskIndex int, completed bool) (*GoalTask, error) {
	tasks, err := ToggleGoalTasks(rootDir, slug, []int{taskIndex}, completed)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("task index %d not found", taskIndex)
	}
	return tasks[0], nil
}

// GlobalAuditResult aggregates goals roadmap metrics across all registered repositories.
type GlobalAuditResult struct {
	TotalProjects         int                     `json:"total_projects"`
	TotalGoals            int                     `json:"total_goals"`
	ImplementedCount      int                     `json:"implemented_count"`
	VerifiedCount         int                     `json:"verified_count"`
	UnverifiedCount       int                     `json:"unverified_count"`
	StaleCount            int                     `json:"stale_count,omitempty"`
	SupersededCount       int                     `json:"superseded_count,omitempty"`
	ClaimedCount          int                     `json:"claimed_count"`
	PendingCount          int                     `json:"pending_count"`
	ReadyCount            int                     `json:"ready_count"`
	ReadyIndependentCount int                     `json:"ready_independent_count,omitempty"`
	ReadyUnblockedCount   int                     `json:"ready_unblocked_count,omitempty"`
	BlockedCount          int                     `json:"blocked_count"`
	ContestedCount        int                     `json:"contested_count"`
	IndependentCount      int                     `json:"independent_count"`
	DraftCount            int                     `json:"draft_count"`
	ImmediateCount        int                     `json:"immediate_count,omitempty"`
	RoadmapCount          int                     `json:"roadmap_count,omitempty"`
	NiceToHaveCount       int                     `json:"nice_to_have_count,omitempty"`
	WontFixCount          int                     `json:"wont_fix_count,omitempty"`
	TotalTasks            int                     `json:"total_tasks,omitempty"`
	CompletedTasks        int                     `json:"completed_tasks,omitempty"`
	Projects              map[string]*AuditResult `json:"projects"`
	// Stats is the closing dashboard. It is additive: existing fields keep
	// their names and shapes.
	Stats *GlobalAuditStats `json:"stats,omitempty"`
}

// GlobalAuditStats is the closing dashboard of the multi-project roadmap.
// Every number is derived from the per-project audits, and every row in the
// rendered block names the command that acts on it.
type GlobalAuditStats struct {
	TopProjects             []TopProjectStat       `json:"top_projects"`
	StaleClaims             []StaleClaimStat       `json:"stale_claims"`
	ImplementedLockCount    int                    `json:"implemented_lock_count"`
	ImplementedLockProjects []string               `json:"implemented_lock_projects"`
	ImplementedLocks        []ImplementedLockStat  `json:"implemented_locks,omitempty"`
	BlockedTotal            int                    `json:"blocked_total"`
	BlockedByRoot           []BlockedRootStat      `json:"blocked_by_root"`
	BlockedProjects         []string               `json:"blocked_projects"`
	DuplicateRegistrations  []DuplicateProjectStat `json:"duplicate_registrations"`
	LookalikeRegistrations  []LookalikeProjectStat `json:"lookalike_registrations,omitempty"`
	Drafts                  []DraftProjectStat     `json:"drafts"`
}

// ImplementedLockStat is a claim record left on a goal that already shipped.
type ImplementedLockStat struct {
	Project string `json:"project"`
	Goal    string `json:"goal"`
}

// LookalikeProjectStat reports two different directories that share goal slugs.
// They are not duplicates and must not be merged.
type LookalikeProjectStat struct {
	Project     string   `json:"project"`
	Other       string   `json:"other"`
	SharedGoals []string `json:"shared_goals"`
}

// TopProjectStat is one row of the "where the work is" ranking.
type TopProjectStat struct {
	Project string `json:"project"`
	Pending int    `json:"pending"`
	Claimed int    `json:"claimed"`
	Total   int    `json:"total"`
}

// StaleClaimStat is a live claim older than staleClaimThreshold.
type StaleClaimStat struct {
	Goal    string `json:"goal"`
	Project string `json:"project"`
	Agent   string `json:"agent,omitempty"`
	AgeDays int    `json:"age_days"`
	Age     string `json:"age"` // compact, e.g. "5d"
	Reason  string `json:"reason,omitempty"`
}

// BlockedRootStat aggregates blocked goals by the root blocker that frees them.
type BlockedRootStat struct {
	Root     string   `json:"root"`
	Count    int      `json:"count"`
	Goals    []string `json:"goals,omitempty"`
	Projects []string `json:"projects,omitempty"`
}

// DuplicateProjectStat reports two project registrations that share goal slugs.
type DuplicateProjectStat struct {
	Project string `json:"project"`
	Other   string `json:"other"`
	Overlap int    `json:"overlap"`
}

// DraftProjectStat lists the goals still missing part of their package.
type DraftProjectStat struct {
	Project string              `json:"project"`
	Goals   []string            `json:"goals"`
	Missing map[string][]string `json:"missing,omitempty"`
}

// staleClaimThreshold is the age after which a live claim is surfaced as stale.
const staleClaimThreshold = 3 * 24 * time.Hour

// ScanAllProjectsGoals scans the goals/ directories across all registered projects in the knowledge store.
func ScanAllProjectsGoals(st *store.Store) (*GlobalAuditResult, error) {
	projs, err := st.Projects()
	if err != nil {
		return nil, err
	}

	res := &GlobalAuditResult{
		Projects: make(map[string]*AuditResult),
	}

	primaryByKey := make(map[string]string)
	var duplicatePairs []DuplicateProjectStat

	for _, p := range projs {
		if p.Path == "" {
			continue
		}
		goalsDir := filepath.Join(p.Path, "goals")
		if _, err := os.Stat(goalsDir); os.IsNotExist(err) {
			continue
		}

		audit, err := ScanGoals(p.Path)
		if err != nil || audit.Total == 0 {
			continue
		}

		// Alias registrations resolve to the same real directory: report the
		// pair, but never count its goals twice.
		if key := realDirKey(p.Path); key != "" {
			if primaryID, ok := primaryByKey[key]; ok {
				duplicatePairs = append(duplicatePairs, DuplicateProjectStat{
					Project: primaryID,
					Other:   p.ID,
					Overlap: sharedGoalCount(res.Projects[primaryID], audit),
				})
				continue
			}
			primaryByKey[key] = p.ID
		}

		res.TotalProjects++
		res.TotalGoals += audit.Total
		res.ImplementedCount += audit.ImplementedCount
		res.VerifiedCount += audit.VerifiedCount
		res.UnverifiedCount += audit.UnverifiedCount
		res.StaleCount += audit.StaleCount
		res.SupersededCount += audit.SupersededCount
		res.ClaimedCount += audit.ClaimedCount
		res.PendingCount += audit.PendingCount
		res.ReadyCount += audit.ReadyCount
		res.ReadyIndependentCount += audit.ReadyIndependentCount
		res.ReadyUnblockedCount += audit.ReadyUnblockedCount
		res.BlockedCount += audit.BlockedCount
		res.ContestedCount += audit.ContestedCount
		res.IndependentCount += audit.IndependentCount
		res.DraftCount += audit.DraftCount
		res.ImmediateCount += audit.ImmediateCount
		res.RoadmapCount += audit.RoadmapCount
		res.NiceToHaveCount += audit.NiceToHaveCount
		res.WontFixCount += audit.WontFixCount
		res.TotalTasks += audit.TotalTasks
		res.CompletedTasks += audit.CompletedTasks
		res.Projects[p.ID] = audit
	}

	res.Stats = computeGlobalStats(res.Projects, time.Now(), duplicatePairs...)
	return res, nil
}

// realDirKey resolves a path to its real directory for identity checks. It
// returns "" when the path cannot be resolved.
func realDirKey(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(resolved)
}

// sharedGoalCount counts goal slugs present in both audits.
func sharedGoalCount(a, b *AuditResult) int {
	if a == nil || b == nil {
		return 0
	}
	seen := make(map[string]bool, len(a.Goals))
	for _, g := range a.Goals {
		seen[g.Slug] = true
	}
	n := 0
	for _, g := range b.Goals {
		if seen[g.Slug] {
			n++
		}
	}
	return n
}

// computeGlobalStats derives the closing dashboard from the per-project
// audits. It is pure so tests can pin every statistic with a fixed clock.
func computeGlobalStats(projects map[string]*AuditResult, now time.Time, duplicatePairs ...DuplicateProjectStat) *GlobalAuditStats {
	// Slices start non-nil so the JSON shape is stable: an empty statistic is
	// an empty array, never a missing key.
	stats := &GlobalAuditStats{
		TopProjects:             []TopProjectStat{},
		StaleClaims:             []StaleClaimStat{},
		ImplementedLockProjects: []string{},
		BlockedByRoot:           []BlockedRootStat{},
		BlockedProjects:         []string{},
		DuplicateRegistrations:  append([]DuplicateProjectStat{}, duplicatePairs...),
		LookalikeRegistrations:  []LookalikeProjectStat{},
		Drafts:                  []DraftProjectStat{},
	}

	ids := make([]string, 0, len(projects))
	slugSets := make(map[string]map[string]bool, len(projects))
	for id := range projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	lockProjects := make(map[string]bool)
	blockedByProject := make(map[string]int)
	rootStats := make(map[string]*BlockedRootStat)
	var rootOrder []string
	draftByProject := make(map[string][]string)

	for _, id := range ids {
		audit := projects[id]
		for _, g := range audit.Goals {
			if slugSets[id] == nil {
				slugSets[id] = make(map[string]bool)
			}
			slugSets[id][g.Slug] = true

			switch g.Status {
			case "implemented":
				if g.Claim != nil {
					stats.ImplementedLockCount++
					lockProjects[id] = true
					stats.ImplementedLocks = append(stats.ImplementedLocks, ImplementedLockStat{Project: id, Goal: g.Slug})
				}
			case "claimed":
				if g.Claim != nil && !g.Claim.ClaimedAt.IsZero() {
					age := now.Sub(g.Claim.ClaimedAt)
					if age > staleClaimThreshold {
						agent := g.Claim.ClaimedBy
						if agent == "" {
							agent = g.Claim.Agent
						}
						stats.StaleClaims = append(stats.StaleClaims, StaleClaimStat{
							Goal:    g.Slug,
							Project: id,
							Agent:   agent,
							AgeDays: int(age.Hours() / 24),
							Age:     formatClaimAge(age),
							Reason:  g.Claim.StaleReason,
						})
					}
				}
			case "pending":
				if g.Blocked {
					blockedByProject[id]++
					roots := g.RootBlockers
					if len(roots) == 0 {
						roots = g.UnmetDeps
					}
					for _, root := range roots {
						rs, ok := rootStats[root]
						if !ok {
							rs = &BlockedRootStat{Root: root}
							rootStats[root] = rs
							rootOrder = append(rootOrder, root)
						}
						rs.Count++
						rs.Goals = appendUniqueSorted(rs.Goals, g.Slug)
						rs.Projects = appendUniqueSorted(rs.Projects, id)
					}
				}
			case "draft":
				draftByProject[id] = append(draftByProject[id], g.Slug)
			}
		}

		if audit.PendingCount > 0 {
			stats.TopProjects = append(stats.TopProjects, TopProjectStat{
				Project: id,
				Pending: audit.PendingCount,
				Claimed: audit.ClaimedCount,
				Total:   audit.Total,
			})
		}
	}

	sort.SliceStable(stats.TopProjects, func(i, j int) bool {
		a, b := stats.TopProjects[i], stats.TopProjects[j]
		if a.Pending != b.Pending {
			return a.Pending > b.Pending
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return a.Project < b.Project
	})
	if len(stats.TopProjects) > 5 {
		stats.TopProjects = stats.TopProjects[:5]
	}

	sort.SliceStable(stats.StaleClaims, func(i, j int) bool {
		if stats.StaleClaims[i].AgeDays != stats.StaleClaims[j].AgeDays {
			return stats.StaleClaims[i].AgeDays > stats.StaleClaims[j].AgeDays
		}
		return stats.StaleClaims[i].Goal < stats.StaleClaims[j].Goal
	})
	for id := range lockProjects {
		stats.ImplementedLockProjects = append(stats.ImplementedLockProjects, id)
	}
	sort.Strings(stats.ImplementedLockProjects)

	for _, root := range rootOrder {
		stats.BlockedByRoot = append(stats.BlockedByRoot, *rootStats[root])
	}
	sort.SliceStable(stats.BlockedByRoot, func(i, j int) bool {
		if stats.BlockedByRoot[i].Count != stats.BlockedByRoot[j].Count {
			return stats.BlockedByRoot[i].Count > stats.BlockedByRoot[j].Count
		}
		return stats.BlockedByRoot[i].Root < stats.BlockedByRoot[j].Root
	})

	blockedIDs := append([]string(nil), ids...)
	sort.SliceStable(blockedIDs, func(i, j int) bool {
		if blockedByProject[blockedIDs[i]] != blockedByProject[blockedIDs[j]] {
			return blockedByProject[blockedIDs[i]] > blockedByProject[blockedIDs[j]]
		}
		return blockedIDs[i] < blockedIDs[j]
	})
	for _, id := range blockedIDs {
		if blockedByProject[id] == 0 {
			continue
		}
		stats.BlockedTotal += blockedByProject[id]
		stats.BlockedProjects = append(stats.BlockedProjects, id)
	}

	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			var shared []string
			for slug := range slugSets[ids[i]] {
				if slugSets[ids[j]][slug] {
					shared = append(shared, slug)
				}
			}
			if len(shared) == 0 {
				continue
			}
			sort.Strings(shared)
			if sameRealDirectory(projects[ids[i]].ProjectDir, projects[ids[j]].ProjectDir) {
				stats.DuplicateRegistrations = append(stats.DuplicateRegistrations, DuplicateProjectStat{
					Project: ids[i],
					Other:   ids[j],
					Overlap: len(shared),
				})
			} else {
				stats.LookalikeRegistrations = append(stats.LookalikeRegistrations, LookalikeProjectStat{
					Project:     ids[i],
					Other:       ids[j],
					SharedGoals: shared,
				})
			}
		}
	}
	sort.SliceStable(stats.DuplicateRegistrations, func(i, j int) bool {
		a, b := stats.DuplicateRegistrations[i], stats.DuplicateRegistrations[j]
		if a.Overlap != b.Overlap {
			return a.Overlap > b.Overlap
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Other < b.Other
	})

	sort.SliceStable(stats.LookalikeRegistrations, func(i, j int) bool {
		a, b := stats.LookalikeRegistrations[i], stats.LookalikeRegistrations[j]
		if len(a.SharedGoals) != len(b.SharedGoals) {
			return len(a.SharedGoals) > len(b.SharedGoals)
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Other < b.Other
	})

	for _, id := range ids {
		if len(draftByProject[id]) == 0 {
			continue
		}
		slugs := draftByProject[id]
		sort.Strings(slugs)
		missing := map[string][]string{}
		for _, g := range projects[id].Goals {
			if g.Status == "draft" && len(g.MissingFiles) > 0 {
				missing[g.Slug] = g.MissingFiles
			}
		}
		stats.Drafts = append(stats.Drafts, DraftProjectStat{Project: id, Goals: slugs, Missing: missing})
	}

	return stats
}

// sameRealDirectory reports whether two project directories resolve to the
// same real path. Missing or unresolvable paths compare by cleaned path.
func sameRealDirectory(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return filepath.Clean(ra) == filepath.Clean(rb)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// formatClaimAge renders how long a claim has been held in one compact token.
func formatClaimAge(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return "<1m"
	}
}

// plural picks the singular or plural form for n in dashboard cells.
func plural(n int, singular string) string {
	if n == 1 {
		return singular
	}
	return singular + "s"
}

// firstFew joins at most n entries and summarizes the rest, keeping dashboard
// cells readable instead of turning them into a wall of names.
func firstFew(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(items[:n], ", "), len(items)-n)
}

// appendUniqueSorted adds v to list when absent, keeping it sorted. The lists
// are short, so the linear scan keeps the caller simple.
func appendUniqueSorted(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	list = append(list, v)
	sort.Strings(list)
	return list
}

// PrintGlobalAuditTable renders the multi-project roadmap matrix.
func PrintGlobalAuditTable(ui render.UI, res *GlobalAuditResult, filterMode string) {
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · goals"))+" — Global Multi-Project Roadmap Matrix")
	fmt.Fprintf(os.Stdout, "  Projects with Goals: %d\n", res.TotalProjects)
	fmt.Fprintf(os.Stdout, "  Total Goals:         %d\n", res.TotalGoals)
	contestedTag := ""
	if res.ContestedCount > 0 {
		contestedTag = fmt.Sprintf(", %s contested ⚠️", ui.Red(fmt.Sprint(res.ContestedCount)))
	}
	fmt.Fprintf(os.Stdout, "  🟡 Ready to Pick:    %s (%s independent ⚡, %s unblocked 🔗%s)\n",
		ui.Yellow(fmt.Sprint(res.ReadyCount)), ui.Cyan(fmt.Sprint(res.ReadyIndependentCount)), ui.Green(fmt.Sprint(res.ReadyUnblockedCount)), contestedTag)
	if res.ClaimedCount > 0 {
		fmt.Fprintf(os.Stdout, "  🔵 In Progress:      %s (🔒 claimed)\n", ui.Cyan(fmt.Sprint(res.ClaimedCount)))
	}
	if res.BlockedCount > 0 {
		fmt.Fprintf(os.Stdout, "  ⛔ Blocked on Deps:   %s\n", ui.Red(fmt.Sprint(res.BlockedCount)))
	}
	fmt.Fprintf(os.Stdout, "  🟢 Implemented:      %s\n", ui.Green(fmt.Sprint(res.ImplementedCount)))
	if res.SupersededCount > 0 {
		fmt.Fprintf(os.Stdout, "  🟣 Superseded:       %s\n", ui.Dim(fmt.Sprint(res.SupersededCount)))
	}
	if res.ContestedCount > 0 {
		fmt.Fprintf(os.Stdout, "  ⚠️  Contested Files:  %s\n", ui.Red(fmt.Sprint(res.ContestedCount)))
	}
	if res.DraftCount > 0 {
		fmt.Fprintf(os.Stdout, "  ⚪ Draft / Setup:    %s\n", ui.Dim(fmt.Sprint(res.DraftCount)))
	}
	if res.ImmediateCount > 0 || res.RoadmapCount > 0 || res.NiceToHaveCount > 0 || res.WontFixCount > 0 {
		fmt.Fprintf(os.Stdout, "  🎯 By Tier:           %s immediate, %s roadmap, %s nice-to-have, %s wont-fix\n",
			ui.Red(fmt.Sprint(res.ImmediateCount)),
			ui.Yellow(fmt.Sprint(res.RoadmapCount)),
			ui.Dim(fmt.Sprint(res.NiceToHaveCount)),
			ui.Dim(fmt.Sprint(res.WontFixCount)))
	}
	if res.TotalTasks > 0 {
		pct := (res.CompletedTasks * 100) / res.TotalTasks
		bar := renderProgressBar(ui, res.CompletedTasks, res.TotalTasks, GoalProgressBarWidth)
		fmt.Fprintf(os.Stdout, "  📋 Workspace Tasks:  %s %d/%d completed (%d%%)\n", bar, res.CompletedTasks, res.TotalTasks, pct)
	}
	fmt.Fprintln(os.Stdout, ui.Dim("==============================================================="))
	fmt.Fprintln(os.Stdout)

	now := time.Now()

	// Projects order by pending work so the first screen is where the work is.
	var projIDs []string
	for id := range res.Projects {
		projIDs = append(projIDs, id)
	}
	sort.SliceStable(projIDs, func(i, j int) bool {
		a, b := res.Projects[projIDs[i]], res.Projects[projIDs[j]]
		if a.PendingCount != b.PendingCount {
			return a.PendingCount > b.PendingCount
		}
		if a.ClaimedCount != b.ClaimedCount {
			return a.ClaimedCount > b.ClaimedCount
		}
		return projIDs[i] < projIDs[j]
	})

	if filterMode == "implemented" {
		for _, pid := range projIDs {
			audit := res.Projects[pid]
			fmt.Fprintf(os.Stdout, "  %s %s — %s/%d implemented\n",
				ui.Green("✔"), ui.Magenta(ui.Bold(pid)),
				ui.Green(fmt.Sprint(audit.ImplementedCount)), audit.Total)
		}
		fmt.Fprintln(os.Stdout)
		printGlobalStats(ui, res.Stats)
		return
	}

	var fullyImplemented []string
	actionable := false
	for _, pid := range projIDs {
		audit := res.Projects[pid]
		claimed, pending := globalProjectGoals(audit, filterMode)
		if len(claimed) == 0 && len(pending) == 0 {
			// Only the default view collapses finished repositories; under a
			// filter, a project with no match is simply skipped.
			if filterMode == "" && audit.PendingCount == 0 && audit.ClaimedCount == 0 && audit.DraftCount == 0 && audit.ImplementedCount > 0 {
				fullyImplemented = append(fullyImplemented, pid)
			}
			continue
		}
		actionable = true

		fmt.Fprintf(os.Stdout, "📁 %s %s\n", ui.Magenta(ui.Bold(pid)), ui.Dim(fmt.Sprintf(
			"— %s pending · %s claimed · %s implemented (%d total)",
			ui.Yellow(fmt.Sprint(audit.PendingCount)), ui.Cyan(fmt.Sprint(audit.ClaimedCount)),
			ui.Green(fmt.Sprint(audit.ImplementedCount)), audit.Total)))

		for _, g := range claimed {
			fmt.Fprintln(os.Stdout, globalClaimedGoalLine(ui, g, now))
		}
		for _, g := range pending {
			fmt.Fprintln(os.Stdout, globalPendingGoalLine(ui, g))
		}
		fmt.Fprintln(os.Stdout)
	}

	if len(fullyImplemented) > 0 {
		fmt.Fprintf(os.Stdout, "%s %s (%d): %s\n\n",
			ui.Green("✔"), ui.Dim("fully implemented"), len(fullyImplemented),
			ui.Dim(strings.Join(fullyImplemented, ", ")))
	}

	if actionable {
		fmt.Fprintf(os.Stdout, "  %s %s   %s %s\n\n",
			ui.Dim("Claim:"), ui.Cyan("sila goals claim <slug> --agent=<name>"),
			ui.Dim("Launch:"), ui.Cyan("/goal goals/<slug>/goal.md"))
	}

	printGlobalStats(ui, res.Stats)
}

// globalProjectGoals selects the goals a global project section shows under a
// filter, mirroring the per-project view's semantics.
func globalProjectGoals(audit *AuditResult, filterMode string) (claimed, pending []Goal) {
	for _, g := range audit.Goals {
		switch g.Status {
		case "claimed":
			if filterMode == "" || filterMode == "claimed" {
				claimed = append(claimed, g)
			}
		case "pending":
			switch filterMode {
			case "claimed":
				// claimed-only view
			case "ready":
				if !g.Blocked {
					pending = append(pending, g)
				}
			case "independent":
				if g.Independent {
					pending = append(pending, g)
				}
			case "blocked":
				if g.Blocked {
					pending = append(pending, g)
				}
			default:
				pending = append(pending, g)
			}
		}
	}
	return claimed, pending
}

// globalGoalTitle shortens a goal title for the one-line global listing.
func globalGoalTitle(g Goal) string {
	return store.TruncateExcerpt(g.Title, 60)
}

// globalPendingGoalLine renders one pending goal in the compact global view.
func globalPendingGoalLine(ui render.UI, g Goal) string {
	mode := ui.Cyan("⚡")
	if !g.Independent {
		if g.Blocked {
			mode = ui.Red("⛔")
		} else {
			mode = ui.Green("🔗")
		}
	}
	line := fmt.Sprintf("  %s %s — %s %s", ui.Yellow("▶"), ui.Bold(g.Slug), ui.Dim(globalGoalTitle(g)), mode)
	if g.Blocked {
		deps := g.UnmetDeps
		if len(deps) > 2 {
			deps = append(append([]string{}, deps[:2]...), "…")
		}
		line += " " + ui.Red("blocked by "+strings.Join(deps, ", "))
	}
	return line
}

// globalClaimedGoalLine renders one claimed goal in the compact global view,
// including the holder and how long the claim has been held.
func globalClaimedGoalLine(ui render.UI, g Goal, now time.Time) string {
	mode := ui.Cyan("⚡")
	if !g.Independent {
		mode = ui.Green("🔗")
	}
	holder := "agent"
	if g.Claim != nil {
		if g.Claim.ClaimedBy != "" {
			holder = g.Claim.ClaimedBy
		} else if g.Claim.Agent != "" {
			holder = g.Claim.Agent
		}
	}
	age := ""
	if g.Claim != nil && !g.Claim.ClaimedAt.IsZero() {
		age = " " + formatClaimAge(now.Sub(g.Claim.ClaimedAt))
	}
	return fmt.Sprintf("  %s %s — %s %s %s", ui.Cyan("🔒"), ui.Bold(g.Slug), ui.Dim(globalGoalTitle(g)), mode,
		ui.Cyan(fmt.Sprintf("@%s%s", holder, age)))
}

// printGlobalStats renders the closing dashboard: where the work is, and the
// hygiene findings whose every row names the command that acts on it.
func printGlobalStats(ui render.UI, stats *GlobalAuditStats) {
	if stats == nil {
		return
	}

	if len(stats.TopProjects) > 0 {
		rows := make([][]string, 0, len(stats.TopProjects))
		for _, p := range stats.TopProjects {
			rows = append(rows, []string{p.Project, strconv.Itoa(p.Pending), strconv.Itoa(p.Claimed), strconv.Itoa(p.Total)})
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("📊 Where the work is")))
		fmt.Fprintln(os.Stdout, ui.Table([]string{"project", "pending", "claimed", "total"}, rows, []string{"l", "r", "r", "r"}))
		fmt.Fprintln(os.Stdout)
	}

	// One section per finding: what was found, why it exists, and the exact
	// commands a human or an agent can run. Counts are already distinct because
	// duplicate registrations are merged before aggregation.
	section := func(title, count, why string, commands []string) {
		fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold(title), ui.Dim("("+count+")"))
		fmt.Fprintf(os.Stdout, "     %s\n", ui.Dim("why: "+why))
		for _, c := range commands {
			fmt.Fprintf(os.Stdout, "     %s %s\n", ui.Cyan("→"), c)
		}
	}
	tail := func(shown, total int, cmd string) {
		if total > shown {
			fmt.Fprintf(os.Stdout, "     %s\n", ui.Dim(fmt.Sprintf("… and %d more; %s", total-shown, cmd)))
		}
	}
	any := false

	if len(stats.StaleClaims) > 0 {
		any = true
		section("Stale live claims", strconv.Itoa(len(stats.StaleClaims)),
			"Claims older than 3d on goals whose session exited or went quiet. Releasing deletes only the claim file; the goal returns to pending.",
			[]string{
				"human: sila goals release --stale --global --dry-run   (then without --dry-run)",
				"single: sila goals release <slug>",
				"agent: sila goals claim <slug> -p <repo> --agent=<name>",
			})
		const limit = 5
		for i, c := range stats.StaleClaims {
			if i == limit {
				break
			}
			reason := c.Reason
			if reason == "" {
				reason = "claim older than 3d"
			}
			fmt.Fprintf(os.Stdout, "     • %s/%s  @%s  %s  %s\n", c.Project, ui.Bold(c.Goal), c.Agent, c.Age, ui.Dim(reason))
		}
		tail(limit, len(stats.StaleClaims), "sila goals release --stale --global --dry-run lists them all")
		fmt.Fprintln(os.Stdout)
	}

	if stats.ImplementedLockCount > 0 {
		any = true
		section("Locks on implemented goals", fmt.Sprintf("%d goals in %d repos", stats.ImplementedLockCount, len(stats.ImplementedLockProjects)),
			"claim.json survived on goals that already shipped; cleanup previously ran only during a human local audit.",
			[]string{
				"human: sila goals prune --claims --global --dry-run   (then without --dry-run)",
				"single: sila goals release <slug>",
			})
		locks := stats.ImplementedLocks
		if len(locks) == 0 {
			for _, p := range stats.ImplementedLockProjects {
				locks = append(locks, ImplementedLockStat{Project: p, Goal: "(unknown)"})
			}
		}
		const limit = 5
		for i, l := range locks {
			if i == limit {
				break
			}
			fmt.Fprintf(os.Stdout, "     • %s/%s\n", l.Project, ui.Bold(l.Goal))
		}
		tail(limit, len(locks), "sila goals prune --claims --global lists them all")
		fmt.Fprintln(os.Stdout)
	}

	if len(stats.BlockedByRoot) > 0 {
		any = true
		section("Blocked work", fmt.Sprintf("%d goals", stats.BlockedTotal),
			"Pending goals whose prerequisites are still open. Closing the root frees them.",
			[]string{
				"human: sila goals claim <root> -p <repo> --agent=<name>",
				"agent: /goal goals/<root>/goal.md",
				"or: sila goals supersede <slug> --by=<goal> --reason=\"...\"",
			})
		const limit = 3
		for i, r := range stats.BlockedByRoot {
			if i == limit {
				break
			}
			fmt.Fprintf(os.Stdout, "     • %s blocks %s (%s)\n", ui.Bold(r.Root), strings.Join(r.Goals, ", "), strings.Join(r.Projects, ", "))
		}
		tail(limit, len(stats.BlockedByRoot), "sila goals blocked lists every chain")
		fmt.Fprintln(os.Stdout)
	}

	if len(stats.DuplicateRegistrations) > 0 {
		any = true
		section("Duplicate registrations", fmt.Sprintf("%d pairs", len(stats.DuplicateRegistrations)),
			"Two registrations resolve to the same real directory, so every count is doubled.",
			[]string{
				"human: sila projects merge <keep> <drop> --dry-run   (then without --dry-run)",
				"or: sila doctor --repair merges all alias duplicates",
			})
		const limit = 5
		for i, d := range stats.DuplicateRegistrations {
			if i == limit {
				break
			}
			fmt.Fprintf(os.Stdout, "     • %s ↔ %s (%d shared goals) → sila projects merge %s %s\n", d.Project, d.Other, d.Overlap, d.Project, d.Other)
		}
		tail(limit, len(stats.DuplicateRegistrations), "sila goals --global --json lists every pair")
		fmt.Fprintln(os.Stdout)
	}

	if len(stats.LookalikeRegistrations) > 0 {
		any = true
		section("Shared goal slugs", fmt.Sprintf("%d pairs", len(stats.LookalikeRegistrations)),
			"Different directories that happen to share goal slugs. Not duplicates; do not merge.",
			[]string{"review: sila goals <project> --json"})
		for _, d := range stats.LookalikeRegistrations {
			fmt.Fprintf(os.Stdout, "     • %s ↔ %s (%s)\n", d.Project, d.Other, strings.Join(d.SharedGoals, ", "))
		}
		fmt.Fprintln(os.Stdout)
	}

	if len(stats.Drafts) > 0 {
		any = true
		total := 0
		for _, d := range stats.Drafts {
			total += len(d.Goals)
		}
		section("Draft goals", strconv.Itoa(total),
			"Goals missing part of their package. Finish the named files, or remove the directory if it is scaffolding.",
			[]string{"human: sila goals --json lists every missing file"})
		const limit = 6
		shown := 0
		for _, d := range stats.Drafts {
			for _, slug := range d.Goals {
				if shown == limit {
					break
				}
				missing := d.Missing[slug]
				missingText := "goal.md, plan.md, facts.md"
				if len(missing) > 0 {
					missingText = strings.Join(missing, ", ")
				}
				fmt.Fprintf(os.Stdout, "     • %s/%s  missing: %s\n", d.Project, ui.Bold(slug), missingText)
				shown++
			}
			if shown == limit {
				break
			}
		}
		tail(limit, total, "sila goals --json lists them all")
		fmt.Fprintln(os.Stdout)
	}

	if !any {
		return
	}
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("⚠️  Needs attention")))
	fmt.Fprintln(os.Stdout)
}

// GoalTelemetrySummary encapsulates compact metrics for live watch status banners and periodic heartbeats.
type GoalTelemetrySummary struct {
	HasGoals       bool     `json:"has_goals"`
	Project        string   `json:"project,omitempty"`
	TotalGoals     int      `json:"total_goals"`
	Implemented    int      `json:"implemented"`
	Claimed        int      `json:"claimed"`
	Pending        int      `json:"pending"`
	Ready          int      `json:"ready"`
	Blocked        int      `json:"blocked"`
	TotalTasks     int      `json:"total_tasks"`
	CompletedTasks int      `json:"completed_tasks"`
	TaskPercent    int      `json:"task_percent"`
	ActiveWorkers  []string `json:"active_workers,omitempty"`
	ProgressBar    string   `json:"progress_bar,omitempty"`
	RoadmapLine    string   `json:"roadmap_line,omitempty"`
	NextReadyGoal  string   `json:"next_ready_goal,omitempty"`
	NextReadyMode  string   `json:"next_ready_mode,omitempty"`
	NextReadyCmd   string   `json:"next_ready_cmd,omitempty"`
	DecisionHint   string   `json:"decision_hint,omitempty"`
	SummaryLine    string   `json:"summary_line,omitempty"`
}

// RoadmapPulse returns a concise task bar and counts suitable for heartbeat ticks.
func (s *GoalTelemetrySummary) RoadmapPulse() string {
	if !s.HasGoals {
		return ""
	}
	parts := []string{}
	if s.ProgressBar != "" {
		parts = append(parts, fmt.Sprintf("%s %d%%", s.ProgressBar, s.TaskPercent))
	}
	sub := []string{}
	if s.Ready > 0 {
		sub = append(sub, fmt.Sprintf("%d ready", s.Ready))
	}
	if s.Claimed > 0 {
		sub = append(sub, fmt.Sprintf("%d active", s.Claimed))
	}
	if s.Blocked > 0 {
		sub = append(sub, fmt.Sprintf("%d blocked", s.Blocked))
	}
	sub = append(sub, fmt.Sprintf("%d done", s.Implemented))
	parts = append(parts, strings.Join(sub, " · "))
	return strings.Join(parts, " — ")
}

// ActionPulse returns the second-tier decision guidance line for watch heartbeats.
func (s *GoalTelemetrySummary) ActionPulse(ui render.UI) string {
	if !s.HasGoals {
		return ""
	}
	if len(s.ActiveWorkers) > 0 {
		return fmt.Sprintf("           ↳ 👷 %s", strings.Join(s.ActiveWorkers, ", "))
	}
	if s.NextReadyGoal != "" {
		cmdStr := s.NextReadyCmd
		if cmdStr == "" {
			cmdStr = "sila goals claim " + s.NextReadyGoal
		}
		return fmt.Sprintf("           ↳ %s Next up: %s (%s)",
			ui.Yellow("🟡"), ui.Bold(s.NextReadyGoal), ui.Dim("run '"+cmdStr+"'"))
	}
	if s.TotalGoals > 0 && s.Implemented == s.TotalGoals {
		return fmt.Sprintf("           ↳ %s All %d roadmap goals completed!", ui.Green("🟢"), s.TotalGoals)
	}
	if s.Blocked > 0 {
		return fmt.Sprintf("           ↳ %s %d goal(s) blocked on prerequisites", ui.Red("⛔"), s.Blocked)
	}
	return ""
}

// FormatProjectGoalsTelemetry extracts live roadmap telemetry and worker claims for a project directory.
func FormatProjectGoalsTelemetry(ui render.UI, rootDir string) (*GoalTelemetrySummary, string) {
	if rootDir == "" {
		cwd, _ := os.Getwd()
		rootDir = cwd
	}
	goalsDir := filepath.Join(rootDir, "goals")
	if _, err := os.Stat(goalsDir); os.IsNotExist(err) {
		return &GoalTelemetrySummary{HasGoals: false}, ""
	}

	res, err := ScanGoals(rootDir)
	if err != nil || res.Total == 0 {
		return &GoalTelemetrySummary{HasGoals: false}, ""
	}

	sum := &GoalTelemetrySummary{
		HasGoals:       true,
		Project:        store.ProjectID(rootDir),
		TotalGoals:     res.Total,
		Implemented:    res.ImplementedCount,
		Claimed:        res.ClaimedCount,
		Pending:        res.PendingCount,
		Ready:          res.ReadyCount,
		Blocked:        res.BlockedCount,
		TotalTasks:     res.TotalTasks,
		CompletedTasks: res.CompletedTasks,
	}

	if res.TotalTasks > 0 {
		sum.TaskPercent = (res.CompletedTasks * 100) / res.TotalTasks
		sum.ProgressBar = renderProgressBar(ui, res.CompletedTasks, res.TotalTasks, GoalProgressBarWidth)
	}

	for _, g := range res.Goals {
		if g.Claim != nil && g.Status == "claimed" {
			agentName := strings.TrimPrefix(g.Claim.ClaimedBy, "@")
			if agentName == "" {
				agentName = strings.TrimPrefix(g.Claim.Agent, "@")
			}
			if agentName == "" {
				agentName = "agent"
			}
			workerStr := fmt.Sprintf("@%s on %s", agentName, g.Slug)

			// Extract current active sub-task from plan.md / goal.md
			var taskSnippet string
			if tasks, err := GetGoalTasks(rootDir, g.Slug); err == nil && len(tasks) > 0 {
				for _, t := range tasks {
					if !t.Completed {
						taskSnippet = cleanTaskSnippet(t.Text, t.Index)
						break
					}
				}
				if taskSnippet == "" {
					taskSnippet = "all tasks met (verifying)"
				}
			}

			statusPrefix := ""
			if g.Claim.Status != "" && g.Claim.Status != "in_progress" && g.Claim.Status != "in-progress" {
				statusPrefix = strings.ToUpper(g.Claim.Status) + ": "
			}
			if taskSnippet != "" && g.Claim.Note != "" {
				workerStr += fmt.Sprintf(" [%s | %s%s]", taskSnippet, statusPrefix, g.Claim.Note)
			} else if taskSnippet != "" {
				if statusPrefix != "" {
					workerStr += fmt.Sprintf(" [%s | %s]", taskSnippet, strings.TrimSuffix(statusPrefix, ": "))
				} else {
					workerStr += fmt.Sprintf(" [%s]", taskSnippet)
				}
			} else if g.Claim.Note != "" {
				workerStr += fmt.Sprintf(" (%s%q)", statusPrefix, g.Claim.Note)
			}
			sum.ActiveWorkers = append(sum.ActiveWorkers, workerStr)
		}
	}

	// Find top candidate ready goal for next-step decision making
	for _, g := range res.Goals {
		if g.Status == "pending" && !g.Blocked && len(g.FileConflicts) == 0 {
			sum.NextReadyGoal = g.Slug
			if g.Independent {
				sum.NextReadyMode = "⚡ indep"
			} else {
				sum.NextReadyMode = "🔗 unblocked"
			}
			sum.NextReadyCmd = fmt.Sprintf("sila goals claim %s", g.Slug)
			sum.DecisionHint = fmt.Sprintf("▶ %s (%s) — run '%s'", g.Slug, sum.NextReadyMode, sum.NextReadyCmd)
			break
		}
	}

	if sum.DecisionHint == "" {
		if sum.Claimed > 0 {
			sum.DecisionHint = fmt.Sprintf("%d goal(s) in progress", sum.Claimed)
		} else if sum.Blocked > 0 {
			sum.DecisionHint = fmt.Sprintf("All remaining goals (%d) blocked on prerequisites", sum.Blocked)
		} else if sum.TotalGoals > 0 && sum.Implemented == sum.TotalGoals {
			sum.DecisionHint = "All roadmap goals implemented ✔"
		}
	}

	// Format clean roadmap line (WITHOUT workers, avoiding banner duplication)
	var roadParts []string
	if sum.ProgressBar != "" {
		roadParts = append(roadParts, fmt.Sprintf("%s %d/%d tasks (%d%%)", sum.ProgressBar, sum.CompletedTasks, sum.TotalTasks, sum.TaskPercent))
	}
	statusParts := []string{fmt.Sprintf("%d ready", sum.Ready)}
	if sum.Claimed > 0 {
		statusParts = append(statusParts, fmt.Sprintf("%d active", sum.Claimed))
	}
	if sum.Blocked > 0 {
		statusParts = append(statusParts, fmt.Sprintf("%d blocked", sum.Blocked))
	}
	statusParts = append(statusParts, fmt.Sprintf("%d done", sum.Implemented))
	roadParts = append(roadParts, strings.Join(statusParts, " · "))
	sum.RoadmapLine = strings.Join(roadParts, " — ")

	// SummaryLine (backwards compatible combined summary)
	sum.SummaryLine = sum.RoadmapLine
	if len(sum.ActiveWorkers) > 0 {
		sum.SummaryLine += " | 👷 Active: " + strings.Join(sum.ActiveWorkers, ", ")
	} else if sum.NextReadyGoal != "" {
		sum.SummaryLine += " | Next: " + sum.NextReadyGoal
	}
	return sum, sum.SummaryLine
}

func cleanTaskSnippet(raw string, index int) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "**", "")
	raw = strings.ReplaceAll(raw, "`", "")
	if idx := strings.Index(raw, ":"); idx > 0 && idx < 50 {
		return fmt.Sprintf("Task #%d: %s", index, strings.TrimSpace(raw[:idx]))
	}
	if len(raw) > 45 {
		return fmt.Sprintf("Task #%d: %s...", index, strings.TrimSpace(raw[:42]))
	}
	return fmt.Sprintf("Task #%d: %s", index, raw)
}

// FormatGlobalGoalsTelemetry extracts aggregate workspace roadmap telemetry across all projects.
func FormatGlobalGoalsTelemetry(ui render.UI, st *store.Store) (*GoalTelemetrySummary, string) {
	if st == nil {
		return &GoalTelemetrySummary{HasGoals: false}, ""
	}
	res, err := ScanAllProjectsGoals(st)
	if err != nil || res == nil || res.TotalGoals == 0 {
		return &GoalTelemetrySummary{HasGoals: false}, ""
	}

	sum := &GoalTelemetrySummary{
		HasGoals:       true,
		TotalGoals:     res.TotalGoals,
		Implemented:    res.ImplementedCount,
		Claimed:        res.ClaimedCount,
		Pending:        res.PendingCount,
		Ready:          res.ReadyCount,
		Blocked:        res.BlockedCount,
		TotalTasks:     res.TotalTasks,
		CompletedTasks: res.CompletedTasks,
	}

	if res.TotalTasks > 0 {
		sum.TaskPercent = (res.CompletedTasks * 100) / res.TotalTasks
		sum.ProgressBar = renderProgressBar(ui, res.CompletedTasks, res.TotalTasks, GoalProgressBarWidth)
	}

	seenWorkers := make(map[string]bool)
	for projID, audit := range res.Projects {
		for _, g := range audit.Goals {
			if g.Claim != nil && g.Status == "claimed" {
				agentName := strings.TrimPrefix(g.Claim.ClaimedBy, "@")
				if agentName == "" {
					agentName = strings.TrimPrefix(g.Claim.Agent, "@")
				}
				if agentName == "" {
					agentName = "agent"
				}

				var taskSnippet string
				if tasks, err := GetGoalTasks(audit.ProjectDir, g.Slug); err == nil && len(tasks) > 0 {
					for _, t := range tasks {
						if !t.Completed {
							taskSnippet = cleanTaskSnippet(t.Text, t.Index)
							break
						}
					}
					if taskSnippet == "" {
						taskSnippet = "all tasks met (verifying)"
					}
				}

				statusPrefix := ""
				if g.Claim.Status != "" && g.Claim.Status != "in_progress" && g.Claim.Status != "in-progress" {
					statusPrefix = strings.ToUpper(g.Claim.Status) + ": "
				}
				detail := ""
				if taskSnippet != "" && g.Claim.Note != "" {
					detail = fmt.Sprintf(" [%s | %s%s]", taskSnippet, statusPrefix, g.Claim.Note)
				} else if taskSnippet != "" {
					if statusPrefix != "" {
						detail = fmt.Sprintf(" [%s | %s]", taskSnippet, strings.TrimSuffix(statusPrefix, ": "))
					} else {
						detail = fmt.Sprintf(" [%s]", taskSnippet)
					}
				} else if g.Claim.Note != "" {
					detail = fmt.Sprintf(" (%s%q)", statusPrefix, g.Claim.Note)
				}

				key := fmt.Sprintf("@%s on %s/%s%s", agentName, projID, g.Slug, detail)
				if !seenWorkers[key] {
					seenWorkers[key] = true
					sum.ActiveWorkers = append(sum.ActiveWorkers, key)
				}
			}
		}
	}

	// Find top candidate ready goal across all projects
	for projID, audit := range res.Projects {
		for _, g := range audit.Goals {
			if g.Status == "pending" && !g.Blocked && len(g.FileConflicts) == 0 {
				if sum.NextReadyGoal == "" {
					sum.NextReadyGoal = projID + "/" + g.Slug
					if g.Independent {
						sum.NextReadyMode = "⚡ indep"
					} else {
						sum.NextReadyMode = "🔗 unblocked"
					}
					sum.NextReadyCmd = fmt.Sprintf("sila goals %s claim %s", projID, g.Slug)
					sum.DecisionHint = fmt.Sprintf("▶ %s/%s (%s) — run '%s'", projID, g.Slug, sum.NextReadyMode, sum.NextReadyCmd)
					break
				}
			}
		}
		if sum.NextReadyGoal != "" {
			break
		}
	}

	if sum.DecisionHint == "" {
		if sum.Claimed > 0 {
			sum.DecisionHint = fmt.Sprintf("%d goal(s) in progress across %d project(s)", sum.Claimed, res.TotalProjects)
		} else if sum.Blocked > 0 {
			sum.DecisionHint = fmt.Sprintf("All remaining goals (%d) blocked on prerequisites", sum.Blocked)
		} else if sum.TotalGoals > 0 && sum.Implemented == sum.TotalGoals {
			sum.DecisionHint = "All roadmap goals implemented across all projects ✔"
		}
	}

	var roadParts []string
	if sum.ProgressBar != "" {
		roadParts = append(roadParts, fmt.Sprintf("%s %d/%d workspace tasks (%d%%)", sum.ProgressBar, sum.CompletedTasks, sum.TotalTasks, sum.TaskPercent))
	}
	var globalStatusParts []string
	globalStatusParts = append(globalStatusParts, fmt.Sprintf("%d ready", sum.Ready))
	if sum.Claimed > 0 {
		globalStatusParts = append(globalStatusParts, fmt.Sprintf("%d active", sum.Claimed))
	}
	if sum.Blocked > 0 {
		globalStatusParts = append(globalStatusParts, fmt.Sprintf("%d blocked", sum.Blocked))
	}
	globalStatusParts = append(globalStatusParts, fmt.Sprintf("%d done (%d projects)", sum.Implemented, res.TotalProjects))
	roadParts = append(roadParts, strings.Join(globalStatusParts, " · "))
	sum.RoadmapLine = strings.Join(roadParts, " — ")

	sum.SummaryLine = sum.RoadmapLine
	if len(sum.ActiveWorkers) > 0 {
		sum.SummaryLine += " | 👷 Active: " + strings.Join(sum.ActiveWorkers, ", ")
	} else if sum.NextReadyGoal != "" {
		sum.SummaryLine += " | Next: " + sum.NextReadyGoal
	}
	return sum, sum.SummaryLine
}

// ResolveGoalsRootDir resolves the project root containing "goals/".
// If rootDir already contains a "goals/" directory, it is returned directly.
// Otherwise, it attempts bidirectional discovery via FindGoalsRoot.
func ResolveGoalsRootDir(rootDir string) string {
	if rootDir == "" {
		rootDir = "."
	}
	clean := filepath.Clean(rootDir)
	if fi, err := os.Stat(filepath.Join(clean, "goals")); err == nil && fi.IsDir() {
		return clean
	}
	if found := FindGoalsRoot(clean); found != "" {
		return found
	}
	return clean
}

func resolveGoalsRootDir(rootDir string) string {
	return ResolveGoalsRootDir(rootDir)
}

// FindGoalsRoot discovers a project directory containing "goals/" when rootDir does not have one directly.
// It searches in two directions:
//  1. Upward traversal: climbs parent directories to find an enclosing project root with "goals/".
//     This allows commands executed in subdirectories (e.g. cmd/sila/ or internal/store/) to find the project root.
//  2. Downward workspace discovery: checks immediate subdirectories in multi-repo / monorepo workspaces.
//     If exactly one child directory contains "goals/" (e.g. cli/goals), it returns that child.
//     If multiple child directories contain "goals/", it prefers standard primary folders ("cli", "core", "engine", "main", "app").
func FindGoalsRoot(rootDir string) string {
	if rootDir == "" {
		rootDir = "."
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		abs = rootDir
	}

	// 0. Direct check: root itself has goals/
	if fi, err := os.Stat(filepath.Join(abs, "goals")); err == nil && fi.IsDir() {
		return abs
	}

	// 1. Downward search: check immediate subdirectories within the current workspace first
	entries, err := os.ReadDir(abs)
	if err == nil {
		var candidates []string
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "node_modules" && e.Name() != "dist" && e.Name() != "vendor" {
				subGoals := filepath.Join(abs, e.Name(), "goals")
				if fi, err := os.Stat(subGoals); err == nil && fi.IsDir() {
					candidates = append(candidates, filepath.Join(abs, e.Name()))
				}
			}
		}
		if len(candidates) == 1 {
			return candidates[0]
		}
		if len(candidates) > 1 {
			for _, preferred := range []string{"cli", "core", "engine", "main", "app"} {
				for _, c := range candidates {
					if filepath.Base(c) == preferred {
						return c
					}
				}
			}
			return candidates[0]
		}
	}

	// 2. Upward search stays within the current repository, including worktrees.
	if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
		return ""
	}
	prev := ""
	for dir := filepath.Dir(abs); dir != "" && dir != "." && dir != prev; dir = filepath.Dir(dir) {
		prev = dir
		if fi, err := os.Stat(filepath.Join(dir, "goals")); err == nil && fi.IsDir() {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
	}

	return ""
}

// ResolveProjectDir resolves a project name, ID, or path without fuzzy matching.
// When target is empty (or "."), it performs smart context discovery:
//  1. CWD exact check: Does cwd contain a "goals" directory?
//  2. Upward traversal: Traverses parent directories to find the enclosing project root containing "goals/".
//  3. Downward workspace discovery: Checks immediate subdirectories of cwd (e.g. cli/goals, worker/goals).
//     If exactly one child contains "goals/", it selects it. If multiple contain "goals/", it prefers
//     standard primary folders ("cli", "core", "engine", "main", "app") or returns the first.
//  4. Fallback: returns cwd.
func ResolveProjectDir(st *store.Store, target string) (string, error) {
	if target != "" && target != "." {
		// 1. Direct directory check
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			return filepath.Clean(target), nil
		}

		// 2. Query store for matching project ID or Path
		if st != nil {
			p, err := st.ResolveProject(target)
			if err != nil {
				if pPtr, fErr := st.ResolveProjectFuzzy(target); fErr == nil && pPtr != nil {
					p = *pPtr
					err = nil
				}
			}
			if err == nil {
				if info, err := os.Stat(p.Path); err == nil && info.IsDir() {
					return p.Path, nil
				}
			}
		}

		// 3. Downward check from cwd if target is a subfolder name (e.g. target="cli")
		cwd, err := os.Getwd()
		if err == nil {
			sub := filepath.Join(cwd, target)
			if info, err := os.Stat(sub); err == nil && info.IsDir() {
				return sub, nil
			}
		}

		return "", fmt.Errorf("project %q is not a directory or registered project", target)
	}

	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}
	cwd = filepath.Clean(cwd)

	// One shared resolver: a workspace root selects the same child repository
	// as prime, salam, MCP, and hooks. Ambiguity is an error, never a silent
	// first-candidate pick.
	identity, err := store.ResolveWorkspaceIdentity(cwd)
	if err != nil {
		return "", err
	}
	if identity.ActiveDir != "" {
		return identity.ActiveDir, nil
	}

	// Defensive fallback for a store-free caller; the resolver normally
	// returns an active directory for every path.
	if fi, err := os.Stat(filepath.Join(cwd, "goals")); err == nil && fi.IsDir() {
		return cwd, nil
	}
	if found := FindGoalsRoot(cwd); found != "" {
		return found, nil
	}

	return cwd, nil
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
