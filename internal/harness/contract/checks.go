package contract

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

var checkOrder = []string{"session", "binding", "claim", "tasks", "evidence", "facts", "handoff", "memory", "adoption"}

var checkReason = map[string]string{
	"binding":  ReasonGoalUnbound,
	"claim":    ReasonClaimMissing,
	"tasks":    ReasonTasksIncomplete,
	"evidence": ReasonEvidenceMissing,
	"facts":    ReasonFactsDirty,
	"handoff":  ReasonHandoffMissing,
	"memory":   ReasonMemoryMissing,
}

type factsEntry struct {
	at    time.Time
	err   error
	audit *store.ChainAuditResult
}

var factsCache = struct {
	sync.Mutex
	m map[string]factsEntry
}{m: map[string]factsEntry{}}

// Checker evaluates a signal against the local goal package and store.
type Checker struct {
	Store      *store.Store
	ProjectDir string
	Now        time.Time
	FactsTTL   time.Duration
}

// NewChecker creates a checker. A nil clock uses UTC now.
func NewChecker(st *store.Store, projectDir string) *Checker {
	return &Checker{Store: st, ProjectDir: projectDir, Now: time.Now().UTC(), FactsTTL: 30 * time.Second}
}

// Evaluate runs all checks. It never writes state.
func (c *Checker) Evaluate(sig Signal, policy EffectivePolicy) Decision {
	return c.evaluate(sig, policy, nil, nil)
}

// EvaluateWith allows tests and callers to inject a binding/waiver.
func (c *Checker) EvaluateWith(sig Signal, policy EffectivePolicy, binding *Binding, waiver *Waiver) Decision {
	return c.evaluate(sig, policy, binding, waiver)
}

func (c *Checker) evaluate(sig Signal, policy EffectivePolicy, binding *Binding, waiver *Waiver) Decision {
	start := time.Now()
	sig = NormalizeSignal(sig)
	checks := make(map[string]CheckResult, len(checkOrder))
	decision := Decision{Schema: DecisionSchema, Decision: DecisionAllow, Checks: checks, Bypassed: policy.Bypass}
	if policy.Policy == PolicyOff {
		checks["policy"] = CheckResult{Status: CheckSkip, Detail: "policy off"}
		decision.ReasonCode = ReasonPolicyOff
		return decision
	}
	if policy.Bypass {
		checks["policy"] = CheckResult{Status: CheckWarn, Detail: "one-session bypass"}
		decision.Bypassed = true
		decision.ReasonCode = ReasonBypassed
		return decision
	}
	if err := sig.Validate(); err != nil {
		checks["signal"] = CheckResult{Status: CheckFail, Detail: err.Error()}
		return aggregate(decision, policy, checks, ReasonSignalInvalid, err.Error(), "sila goals contract link --session=<id> --goal=<slug>")
	}

	projectDir := c.ProjectDir
	if projectDir == "" {
		projectDir = sig.Cwd
	}
	if projectDir == "" {
		checks["session"] = CheckResult{Status: CheckFail, Detail: "project directory is unknown"}
		return aggregate(decision, policy, checks, ReasonSignalInvalid, "project directory is unknown", "run from the project root")
	}
	if !filepath.IsAbs(projectDir) {
		if abs, err := filepath.Abs(projectDir); err == nil {
			projectDir = abs
		}
	}
	root := goals.ResolveGoalsRootDir(projectDir)
	if root == "" {
		root = projectDir
	}
	now := c.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// Binding may be explicit, supplied, or inferred from an active claim.
	if binding == nil {
		if b, err := LoadBinding(projectDir, sig.Harness, sig.SessionID); err == nil && b != nil {
			binding = b
		}
	}
	if binding == nil {
		if slug := c.claimGoalForSession(root, sig.SessionID, sig.ParentSessionID); slug != "" {
			binding = &Binding{
				Schema: BindingSchema, Harness: sig.Harness, SessionID: sig.SessionID,
				SilaGoalSlug: slug, Source: "claim", CreatedAt: now,
			}
		}
	}
	if binding == nil && sig.SilaGoalSlug != "" {
		binding = &Binding{
			Schema: BindingSchema, Harness: sig.Harness, SessionID: sig.SessionID,
			SilaGoalSlug: sig.SilaGoalSlug, Source: "explicit", CreatedAt: now,
		}
	}
	if waiver == nil {
		waiver, _ = LoadWaiver(projectDir, sig.Harness, sig.SessionID)
	}

	// Session check is deliberately non-fatal: a missing session row is normal
	// for a harness that does not persist sessions.
	checks["session"] = CheckResult{Status: CheckPass, Detail: "session " + shortID(sig.SessionID)}

	// Binding check.
	if binding == nil || strings.TrimSpace(binding.SilaGoalSlug) == "" {
		setCheck(checks, waiver, "binding", CheckFail, ReasonGoalUnbound, "no Sila goal binding or waiver", "sila goals contract link --session="+sig.SessionID+" --goal=<slug>")
	} else if !binding.ExpiresAt.IsZero() && now.After(binding.ExpiresAt) {
		setCheck(checks, waiver, "binding", CheckFail, ReasonBindingExpired, "binding expired at "+binding.ExpiresAt.UTC().Format(time.RFC3339), "sila goals contract link --session="+sig.SessionID+" --goal="+binding.SilaGoalSlug)
	} else if sig.SilaGoalSlug != "" && sig.SilaGoalSlug != binding.SilaGoalSlug {
		setCheck(checks, waiver, "binding", CheckFail, ReasonGoalMismatch, "signal goal "+sig.SilaGoalSlug+" != binding goal "+binding.SilaGoalSlug, "sila goals contract link --session="+sig.SessionID+" --goal="+sig.SilaGoalSlug)
	} else if binding.HarnessGoalID != "" && sig.HarnessGoalID != "" && binding.HarnessGoalID != sig.HarnessGoalID {
		setCheck(checks, waiver, "binding", CheckFail, ReasonGoalMismatch, "harness goal id does not match the binding", "re-link the harness goal")
	} else {
		checks["binding"] = CheckResult{Status: CheckPass, Detail: binding.SilaGoalSlug}
	}

	slug := ""
	if binding != nil {
		slug = binding.SilaGoalSlug
	}
	var goal *goals.Goal
	if slug != "" {
		if audit, err := goals.ScanGoals(root); err == nil {
			for i := range audit.Goals {
				if audit.Goals[i].Slug == slug {
					goal = &audit.Goals[i]
					break
				}
			}
		}
	}
	if slug != "" && goal == nil {
		setCheck(checks, waiver, "claim", CheckFail, ReasonClaimMissing, "goal package not found: "+slug, "sila goals view "+slug)
	} else if goal != nil {
		c.checkClaim(checks, waiver, sig, goal)
		c.checkTasks(checks, waiver, goal)
		c.checkEvidence(checks, waiver, sig, root, goal)
	}

	c.checkFacts(checks, waiver, sig, projectDir)
	c.checkHandoff(checks, waiver, sig, now, projectDir)
	c.checkMemory(checks, waiver, sig, projectDir)
	checks["adoption"] = CheckResult{Status: CheckSkip, Detail: "advisory only"}

	d := aggregate(decision, policy, checks, "", "", "")
	d.DurationMS = time.Since(start).Milliseconds()
	return d
}

func (c *Checker) claimGoalForSession(root, session, parent string) string {
	audit, err := goals.ScanGoals(root)
	if err != nil {
		return ""
	}
	for i := range audit.Goals {
		cl := audit.Goals[i].Claim
		if cl == nil {
			continue
		}
		if cl.SessionID != "" && (cl.SessionID == session || cl.SessionID == parent) {
			return audit.Goals[i].Slug
		}
	}
	return ""
}

func (c *Checker) checkClaim(checks map[string]CheckResult, w *Waiver, sig Signal, goal *goals.Goal) {
	cl := goal.Claim
	if cl == nil || cl.Status == "superseded" || cl.Stale {
		setCheck(checks, w, "claim", CheckFail, ReasonClaimMissing, "no active claim for "+goal.Slug, "sila goals claim "+goal.Slug)
		return
	}
	if cl.SessionID != "" && cl.SessionID != sig.SessionID && cl.SessionID != sig.ParentSessionID {
		setCheck(checks, w, "claim", CheckFail, ReasonClaimMismatch, "claim belongs to session "+shortID(cl.SessionID), "sila goals release "+goal.Slug+" --force")
		return
	}
	if sig.ParentSessionID != "" && cl.SessionID != "" && cl.SessionID != sig.ParentSessionID {
		setCheck(checks, w, "claim", CheckFail, ReasonClaimDuplicate, "child session cannot hold a second claim", "let the parent session own the claim")
		return
	}
	checks["claim"] = CheckResult{Status: CheckPass, Detail: "claimed by " + cl.ClaimedBy}
}

func (c *Checker) checkTasks(checks map[string]CheckResult, w *Waiver, goal *goals.Goal) {
	if goal.TasksTotal == 0 {
		setCheck(checks, w, "tasks", CheckFail, ReasonTasksIncomplete, "plan has no tasks", "add tasks to plan.md")
		return
	}
	if goal.TasksMet < goal.TasksTotal {
		setCheck(checks, w, "tasks", CheckFail, ReasonTasksIncomplete, fmt.Sprintf("%d/%d tasks checked", goal.TasksMet, goal.TasksTotal), "check every task in plan.md")
		return
	}
	checks["tasks"] = CheckResult{Status: CheckPass, Detail: fmt.Sprintf("%d/%d", goal.TasksMet, goal.TasksTotal)}
}

func (c *Checker) checkEvidence(checks map[string]CheckResult, w *Waiver, sig Signal, root string, goal *goals.Goal) {
	if goal.Commit != "" {
		checks["evidence"] = CheckResult{Status: CheckPass, Detail: "commit " + shortID(goal.Commit)}
		return
	}
	path := filepath.Join(root, "goals", goal.Slug, "evidence.md")
	data, err := os.ReadFile(path)
	if err != nil {
		setCheck(checks, w, "evidence", CheckFail, ReasonEvidenceMissing, "evidence.md not found", "write goals/"+goal.Slug+"/evidence.md with Verdict: PASS")
		return
	}
	if !hasPassVerdict(string(data)) {
		setCheck(checks, w, "evidence", CheckFail, ReasonEvidenceInvalid, "evidence.md has no standalone Verdict: PASS", "add a standalone 'Verdict: PASS' line")
		return
	}
	for _, ep := range sig.EvidencePaths {
		if ep == "" {
			continue
		}
		full := ep
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, ep)
		}
		if _, err := os.Stat(full); err != nil {
			setCheck(checks, w, "evidence", CheckFail, ReasonEvidenceMissing, "declared evidence path missing: "+ep, "fix or remove the evidence path")
			return
		}
	}
	checks["evidence"] = CheckResult{Status: CheckPass, Detail: "Verdict: PASS"}
}

func hasPassVerdict(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "Verdict: PASS") {
			return true
		}
	}
	return false
}

func (c *Checker) checkFacts(checks map[string]CheckResult, w *Waiver, sig Signal, projectDir string) {
	if c.Store == nil {
		checks["facts"] = CheckResult{Status: CheckSkip, Detail: "store unavailable"}
		return
	}
	audit, err := c.verifyFacts(projectDir)
	if err != nil {
		setCheck(checks, w, "facts", CheckFail, ReasonFactsDirty, err.Error(), "sila facts verify")
		return
	}
	if !audit.Intact || len(audit.Violations) > 0 {
		setCheck(checks, w, "facts", CheckFail, ReasonFactsDirty, fmt.Sprintf("%d violation(s)", len(audit.Violations)), "sila facts verify")
		return
	}
	checks["facts"] = CheckResult{Status: CheckPass, Detail: fmt.Sprintf("%d verified block(s)", audit.VerifiedBlocks)}
}

func (c *Checker) verifyFacts(projectID string) (*store.ChainAuditResult, error) {
	ttl := c.FactsTTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	now := time.Now().UTC()
	factsCache.Lock()
	if e, ok := factsCache.m[projectID]; ok && now.Sub(e.at) < ttl {
		factsCache.Unlock()
		return e.audit, e.err
	}
	factsCache.Unlock()
	audit, err := c.Store.VerifyChain(projectID)
	factsCache.Lock()
	factsCache.m[projectID] = factsEntry{at: now, err: err, audit: audit}
	factsCache.Unlock()
	return audit, err
}

func (c *Checker) checkHandoff(checks map[string]CheckResult, w *Waiver, sig Signal, now time.Time, projectDir string) {
	if c.Store == nil {
		checks["handoff"] = CheckResult{Status: CheckSkip, Detail: "store unavailable"}
		return
	}
	h, err := c.Store.LatestHandoff(sig.ProjectID)
	if err != nil || h == nil {
		warnOnly := sig.Phase == PhaseStop || sig.Phase == PhaseTurnEnd || sig.Phase == PhaseHandoff || sig.Phase == PhaseCertify
		if warnOnly {
			checks["handoff"] = CheckResult{Status: CheckWarn, Detail: "no handoff yet (will be required at session end)"}
		} else {
			setCheck(checks, w, "handoff", CheckFail, ReasonHandoffMissing, "no handoff recorded", "sila handoff \"<summary>\" --open=\"...\"")
		}
		return
	}
	if h.CreatedAt != "" {
		if ts, perr := time.Parse(time.RFC3339, h.CreatedAt); perr == nil && now.Sub(ts) > 7*24*time.Hour {
			setCheck(checks, w, "handoff", CheckFail, ReasonHandoffMissing, "latest handoff is older than 7 days", "sila handoff \"<summary>\"")
			return
		}
	}
	if h.SessionID != "" && h.SessionID != sig.SessionID && h.SessionID != sig.ParentSessionID {
		checks["handoff"] = CheckResult{Status: CheckWarn, Detail: "latest handoff belongs to another session"}
		return
	}
	checks["handoff"] = CheckResult{Status: CheckPass, Detail: shortID(h.ID)}
}

func (c *Checker) checkMemory(checks map[string]CheckResult, w *Waiver, sig Signal, projectDir string) {
	if c.Store == nil {
		checks["memory"] = CheckResult{Status: CheckSkip, Detail: "store unavailable"}
		return
	}
	n, err := c.Store.CountMemoryWrites(sig.ProjectID, sig.SessionID)
	if err == nil && n == 0 && sig.ParentSessionID != "" {
		n, err = c.Store.CountMemoryWrites(sig.ProjectID, sig.ParentSessionID)
	}
	if err != nil {
		checks["memory"] = CheckResult{Status: CheckWarn, Detail: "memory store unavailable"}
		return
	}
	if n > 0 {
		checks["memory"] = CheckResult{Status: CheckPass, Detail: fmt.Sprintf("%d memo/lesson/case write(s)", n)}
		return
	}
	// Harness and MCP session ids can differ. Accept a recent write in the same
	// project rather than forcing a false negative at session end.
	if c.hasRecentMemoryWrite(sig.ProjectID, 24*time.Hour) {
		checks["memory"] = CheckResult{Status: CheckPass, Detail: "recent project memory write"}
		return
	}
	warnOnly := sig.Phase == PhaseStop || sig.Phase == PhaseTurnEnd || sig.Phase == PhaseHandoff || sig.Phase == PhaseCertify
	if warnOnly {
		checks["memory"] = CheckResult{Status: CheckWarn, Detail: "no memo/lesson/case yet (required at session end)"}
		return
	}
	setCheck(checks, w, "memory", CheckFail, ReasonMemoryMissing, "no memo/lesson/case for this session", "sila memo \"...\" --files=...")
}

func setCheck(checks map[string]CheckResult, w *Waiver, name, status, code, detail, recovery string) {
	if w != nil && !w.Expired(time.Now().UTC()) && w.Covers(name) {
		checks[name] = CheckResult{Status: CheckPass, Detail: "waived: " + w.Reason}
		return
	}
	checks[name] = CheckResult{Status: status, Detail: detail, Code: code, Recovery: recovery}
}

func shortID(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "unknown"
	}
	return s
}

// RecordDecision stores a receipt and attaches its path to the decision.
func RecordDecision(projectDir string, sig Signal, policy EffectivePolicy, d Decision, duration time.Duration) Decision {
	r := Receipt{
		Schema: ReceiptSchema, Harness: sig.Harness, SessionID: sig.SessionID,
		ParentSessionID: sig.ParentSessionID, ProjectID: sig.ProjectID,
		SilaGoalSlug: sig.SilaGoalSlug, ClaimID: sig.ClaimID,
		Decision: d.Decision, ReasonCode: d.ReasonCode, Checks: d.Checks,
		Policy: policy.Policy, Bypassed: d.Bypassed,
		DurationMS: duration.Milliseconds(), Timestamp: time.Now().UTC(),
	}
	if path, err := SaveReceipt(projectDir, r); err == nil {
		d.ReceiptPath = path
	}
	return d
}

// ReasonForCheck returns the stable code for a check name.
func ReasonForCheck(name string) string { return checkReason[name] }

// ErrNoGoal is returned by helper resolvers when no goal is available.
var ErrNoGoal = errors.New("no goal")

func contractStoreBuildArgsShape(raw []byte) string { return store.BuildArgsShape(raw) }

func (c *Checker) hasRecentMemoryWrite(projectID string, window time.Duration) bool {
	facts, err := c.Store.FactsByProject(projectID)
	if err != nil {
		return false
	}
	cutoff := time.Now().UTC().Add(-window)
	for _, f := range facts {
		switch f.Kind {
		case "memo", "lesson", "case":
		default:
			continue
		}
		if ts, err := time.Parse(time.RFC3339, f.FirstSeen); err == nil && ts.After(cutoff) {
			return true
		}
	}
	return false
}
