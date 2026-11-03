package store

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// canonicalVerbs maps verb roots and common inflectional forms to their base action verb.
var canonicalVerbs = map[string]string{
	// Core verbs from specification
	"reconcile":   "reconcile",
	"reconciles":  "reconcile",
	"reconciled":  "reconcile",
	"reconciling": "reconcile",

	"unify":    "unify",
	"unifies":  "unify",
	"unified":  "unify",
	"unifying": "unify",

	"sentry":    "sentry",
	"sentries":  "sentry",
	"sentried":  "sentry",
	"sentrying": "sentry",

	"decouple":   "decouple",
	"decouples":  "decouple",
	"decoupled":  "decouple",
	"decoupling": "decouple",

	"harmonize":   "harmonize",
	"harmonizes":  "harmonize",
	"harmonized":  "harmonize",
	"harmonizing": "harmonize",

	"enforce":   "enforce",
	"enforces":  "enforce",
	"enforced":  "enforce",
	"enforcing": "enforce",

	"isolate":   "isolate",
	"isolates":  "isolate",
	"isolated":  "isolate",
	"isolating": "isolate",

	"audit":    "audit",
	"audits":   "audit",
	"audited":  "audit",
	"auditing": "audit",

	"streamline":   "streamline",
	"streamlines":  "streamline",
	"streamlined":  "streamline",
	"streamlining": "streamline",

	// Additional epistemic software engineering verbs
	"calibrate":   "calibrate",
	"calibrates":  "calibrate",
	"calibrated":  "calibrate",
	"calibrating": "calibrate",

	"distill":    "distill",
	"distills":   "distill",
	"distilled":  "distill",
	"distilling": "distill",

	"optimize":   "optimize",
	"optimizes":  "optimize",
	"optimized":  "optimize",
	"optimizing": "optimize",

	"fortify":    "fortify",
	"fortifies":  "fortify",
	"fortified":  "fortify",
	"fortifying": "fortify",

	"refactor":    "refactor",
	"refactors":   "refactor",
	"refactored":  "refactor",
	"refactoring": "refactor",

	"validate":   "validate",
	"validates":  "validate",
	"validated":  "validate",
	"validating": "validate",

	"clarify":    "clarify",
	"clarifies":  "clarify",
	"clarified":  "clarify",
	"clarifying": "clarify",

	"observe":   "observe",
	"observes":  "observe",
	"observed":  "observe",
	"observing": "observe",

	"prune":   "prune",
	"prunes":  "prune",
	"pruned":  "prune",
	"pruning": "prune",

	"bridge":   "bridge",
	"bridges":  "bridge",
	"bridged":  "bridge",
	"bridging": "bridge",

	"elevate":   "elevate",
	"elevates":  "elevate",
	"elevated":  "elevate",
	"elevating": "elevate",

	"guard":    "guard",
	"guards":   "guard",
	"guarded":  "guard",
	"guarding": "guard",

	"anchor":    "anchor",
	"anchors":   "anchor",
	"anchored":  "anchor",
	"anchoring": "anchor",

	"synthesize":   "synthesize",
	"synthesizes":  "synthesize",
	"synthesized":  "synthesize",
	"synthesizing": "synthesize",

	"diagnose":   "diagnose",
	"diagnoses":  "diagnose",
	"diagnosed":  "diagnose",
	"diagnosing": "diagnose",

	"heal":    "heal",
	"heals":   "heal",
	"healed":  "heal",
	"healing": "heal",

	"shield":    "shield",
	"shields":   "shield",
	"shielded":  "shield",
	"shielding": "shield",

	"inspect":    "inspect",
	"inspects":   "inspect",
	"inspected":  "inspect",
	"inspecting": "inspect",

	"crystallize":   "crystallize",
	"crystallizes":  "crystallize",
	"crystallized":  "crystallize",
	"crystallizing": "crystallize",

	"illuminate":   "illuminate",
	"illuminates":  "illuminate",
	"illuminated":  "illuminate",
	"illuminating": "illuminate",

	"stabilize":   "stabilize",
	"stabilizes":  "stabilize",
	"stabilized":  "stabilize",
	"stabilizing": "stabilize",

	"resolve":   "resolve",
	"resolves":  "resolve",
	"resolved":  "resolve",
	"resolving": "resolve",

	"automate":   "automate",
	"automates":  "automate",
	"automated":  "automate",
	"automating": "automate",

	"throttle":   "throttle",
	"throttles":  "throttle",
	"throttled":  "throttle",
	"throttling": "throttle",

	"restrict":    "restrict",
	"restricts":   "restrict",
	"restricted":  "restrict",
	"restricting": "restrict",

	"sandbox":    "sandbox",
	"sandboxes":  "sandbox",
	"sandboxed":  "sandbox",
	"sandboxing": "sandbox",

	"verify":    "verify",
	"verifies":  "verify",
	"verified":  "verify",
	"verifying": "verify",

	"persist":    "persist",
	"persists":   "persist",
	"persisted":  "persist",
	"persisting": "persist",

	"scaffold":    "scaffold",
	"scaffolds":   "scaffold",
	"scaffolded":  "scaffold",
	"scaffolding": "scaffold",

	"sync":          "synchronize",
	"syncs":         "synchronize",
	"synced":        "synchronize",
	"syncing":       "synchronize",
	"synchronize":   "synchronize",
	"synchronizes":  "synchronize",
	"synchronized":  "synchronize",
	"synchronizing": "synchronize",

	"compress":    "compress",
	"compresses":  "compress",
	"compressed":  "compress",
	"compressing": "compress",
}

// canonicalNouns maps domain nouns and plurals to their singular domain concept.
var canonicalNouns = map[string]string{
	// Core nouns from specification
	"sovereignty": "sovereignty",

	"toolchain":  "toolchain",
	"toolchains": "toolchain",

	"hygiene": "hygiene",

	"telemetry": "telemetry",

	"latency":   "latency",
	"latencies": "latency",

	"integrity": "integrity",

	// Additional epistemic domain nouns
	"context":  "context",
	"contexts": "context",

	"epistemic":  "epistemic",
	"epistemics": "epistemic",

	"grammar":  "grammar",
	"grammars": "grammar",

	"knowledge": "knowledge",

	"memory":   "memory",
	"memories": "memory",

	"governance": "governance",

	"drift":  "drift",
	"drifts": "drift",

	"friction":  "friction",
	"frictions": "friction",

	"contract":  "contract",
	"contracts": "contract",

	"consensus": "consensus",

	"pipeline":  "pipeline",
	"pipelines": "pipeline",

	"protocol":  "protocol",
	"protocols": "protocol",

	"session":  "session",
	"sessions": "session",

	"worktree":  "worktree",
	"worktrees": "worktree",

	"roadmap":  "roadmap",
	"roadmaps": "roadmap",

	"artifact":  "artifact",
	"artifacts": "artifact",

	"schema":  "schema",
	"schemas": "schema",

	"boundary":   "boundary",
	"boundaries": "boundary",

	"payload":  "payload",
	"payloads": "payload",

	"runtime":  "runtime",
	"runtimes": "runtime",

	"sandbox":   "sandbox",
	"sandboxes": "sandbox",

	"matrix":   "matrix",
	"matrices": "matrix",

	"benchmark":  "benchmark",
	"benchmarks": "benchmark",

	"topology":   "topology",
	"topologies": "topology",

	"workspace":  "workspace",
	"workspaces": "workspace",

	"ticket":  "ticket",
	"tickets": "ticket",

	"gate":  "gate",
	"gates": "gate",

	"store":  "store",
	"stores": "store",

	"database":  "database",
	"databases": "database",

	"daemon":  "daemon",
	"daemons": "daemon",

	"cache":  "cache",
	"caches": "cache",

	"query":   "query",
	"queries": "query",

	"subshell":  "subshell",
	"subshells": "subshell",

	"index":   "index",
	"indexes": "index",
	"indices": "index",

	"token":  "token",
	"tokens": "token",

	"hook":  "hook",
	"hooks": "hook",
}

// stopwords filtered when extracting salient words.
var stopwords = map[string]bool{
	"a": true, "about": true, "above": true, "after": true, "again": true, "against": true,
	"all": true, "am": true, "an": true, "and": true, "any": true, "are": true, "aren't": true,
	"as": true, "at": true, "be": true, "because": true, "been": true, "before": true, "being": true,
	"below": true, "between": true, "both": true, "but": true, "by": true, "can": true, "cannot": true,
	"could": true, "did": true, "do": true, "does": true, "doing": true, "down": true, "during": true,
	"each": true, "few": true, "for": true, "from": true, "further": true, "had": true, "has": true,
	"have": true, "having": true, "he": true, "her": true, "here": true, "hers": true, "herself": true,
	"him": true, "himself": true, "his": true, "how": true, "i": true, "if": true, "in": true,
	"into": true, "is": true, "it": true, "its": true, "itself": true, "me": true, "more": true,
	"most": true, "my": true, "myself": true, "no": true, "nor": true, "not": true, "of": true,
	"off": true, "on": true, "once": true, "only": true, "or": true, "other": true, "ought": true,
	"our": true, "ours": true, "ourselves": true, "out": true, "over": true, "own": true, "same": true,
	"she": true, "should": true, "so": true, "some": true, "such": true, "than": true, "that": true,
	"the": true, "their": true, "theirs": true, "them": true, "themselves": true, "then": true, "there": true,
	"these": true, "they": true, "this": true, "those": true, "through": true, "to": true, "too": true,
	"under": true, "until": true, "up": true, "very": true, "was": true, "we": true, "were": true,
	"what": true, "when": true, "where": true, "which": true, "while": true, "who": true, "whom": true,
	"why": true, "with": true, "would": true, "you": true, "your": true, "yours": true, "yourself": true,
}

var nonAlphanumericRegex = regexp.MustCompile(`[^a-z0-9]+`)

// tokenizeWords splits text into clean lowercase words.
func tokenizeWords(s string) []string {
	var words []string
	var cur strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// defaultVerbForCategory returns a sensible philosophical verb for a category.
func defaultVerbForCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "idea":
		return "crystallize"
	case "issue":
		return "diagnose"
	case "tip":
		return "illuminate"
	case "praise":
		return "elevate"
	case "challenge":
		return "surmount"
	default:
		return "observe"
	}
}

// DeriveFeedbackSlug derives a mnemonic epistemic slug from category and content.
// Format: <action-verb>-<concept-noun>[-<qualifier>]
func DeriveFeedbackSlug(category, content string) string {
	words := tokenizeWords(content)

	var matchedVerb string
	var matchedNouns []string
	var salientWords []string

	for _, w := range words {
		if v, ok := canonicalVerbs[w]; ok {
			if matchedVerb == "" {
				matchedVerb = v
			}
		}
		if n, ok := canonicalNouns[w]; ok {
			// Avoid duplicate nouns in slug
			alreadyPresent := slices.Contains(matchedNouns, n)
			if !alreadyPresent && len(matchedNouns) < 2 {
				matchedNouns = append(matchedNouns, n)
			}
		}
		if len(w) >= 4 && !stopwords[w] {
			salientWords = append(salientWords, w)
		}
	}

	if matchedVerb == "" {
		matchedVerb = defaultVerbForCategory(category)
	}

	var parts []string
	parts = append(parts, matchedVerb)

	if len(matchedNouns) > 0 {
		parts = append(parts, matchedNouns...)
	} else if len(salientWords) > 0 {
		count := 0
		for _, sw := range salientWords {
			if sw != matchedVerb {
				parts = append(parts, sw)
				count++
				if count >= 2 {
					break
				}
			}
		}
	}

	if len(parts) == 1 {
		// Fallback noun if no domain noun or salient word was found
		h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s", category, content)))
		fallbackNouns := []string{"verity", "paradigm", "axiom", "heuristic", "tenet", "praxis"}
		idx := int(h[0]) % len(fallbackNouns)
		parts = append(parts, fallbackNouns[idx])
	}

	return strings.Join(parts, "-")
}

// SanitizeFeedbackID formats and validates an explicit or derived ticket ID.
// If it lacks the "fb_" prefix, it adds it. It strips disallowed characters,
// converts to lowercase, collapses consecutive hyphens, and trims leading/trailing hyphens.
func SanitizeFeedbackID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "fb_observe-verity"
	}

	prefix := "fb_"
	core := raw
	if strings.HasPrefix(raw, "fb_") {
		core = raw[3:]
	} else if strings.HasPrefix(raw, "fb-") {
		prefix = "fb-"
		core = raw[3:]
	}

	core = strings.ToLower(core)
	// Replace non-alphanumeric characters with hyphen
	core = nonAlphanumericRegex.ReplaceAllString(core, "-")
	core = strings.Trim(core, "-")

	if core == "" {
		core = "observe-verity"
	}

	return prefix + core
}
