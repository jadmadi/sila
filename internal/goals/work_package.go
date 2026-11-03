package goals

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// WorkPackageSchemaVersion defines the canonical v1 schema version string.
const WorkPackageSchemaVersion = "sila/work-package/v1"

// TaskResultReturnFormat defines the mandatory return format for task execution.
const TaskResultReturnFormat = "sila/task-result/v1"

// Scope defines the allowable execution verification scopes.
type Scope string

const (
	ScopeUnit        Scope = "unit"
	ScopeIntegration Scope = "integration"
	ScopeBrowser     Scope = "browser"
	ScopeProvider    Scope = "provider"
	ScopeManual      Scope = "manual"
)

// IsValid reports whether s is a recognized Scope enum value.
func (s Scope) IsValid() bool {
	switch s {
	case ScopeUnit, ScopeIntegration, ScopeBrowser, ScopeProvider, ScopeManual:
		return true
	default:
		return false
	}
}

// Capability defines the execution environment capability requirement.
type Capability string

const (
	CapabilityLocal    Capability = "local"
	CapabilityProvider Capability = "provider"
	CapabilityManual   Capability = "manual"
)

// IsValid reports whether c is a recognized Capability enum value.
func (c Capability) IsValid() bool {
	switch c {
	case CapabilityLocal, CapabilityProvider, CapabilityManual:
		return true
	default:
		return false
	}
}

// AssertionKind defines the verification assertion evaluator.
type AssertionKind string

const (
	AssertionGoTestPass        AssertionKind = "go_test_pass"
	AssertionStdoutExact       AssertionKind = "stdout_exact"
	AssertionJSONPointerEquals AssertionKind = "json_pointer_equals"
)

// IsValid reports whether k is a recognized AssertionKind enum value.
func (k AssertionKind) IsValid() bool {
	switch k {
	case AssertionGoTestPass, AssertionStdoutExact, AssertionJSONPointerEquals:
		return true
	default:
		return false
	}
}

// DependencyConditionKind defines the prerequisite condition type.
type DependencyConditionKind string

const (
	DepConditionAcceptedGoal   DependencyConditionKind = "accepted_goal"
	DepConditionAcceptedTask   DependencyConditionKind = "accepted_task"
	DepConditionArtifactDigest DependencyConditionKind = "artifact_digest"
	DepConditionDecisionEquals DependencyConditionKind = "decision_equals"
)

// IsValid reports whether k is a recognized DependencyConditionKind enum value.
func (k DependencyConditionKind) IsValid() bool {
	switch k {
	case DepConditionAcceptedGoal, DepConditionAcceptedTask, DepConditionArtifactDigest, DepConditionDecisionEquals:
		return true
	default:
		return false
	}
}

// TaskFiles defines the scoped filesystem permissions for a single task.
type TaskFiles struct {
	Read   []string `json:"read"`
	Edit   []string `json:"edit"`
	Create []string `json:"create"`
	Forbid []string `json:"forbid"`
}

// Reference points to an immutable code or documentation anchor.
type Reference struct {
	Path    string `json:"path"`
	Symbol  string `json:"symbol"`
	Excerpt string `json:"excerpt"`
	SHA256  string `json:"sha256"`
}

// Decision records an intentional design choice and rationale.
type Decision struct {
	ID        string   `json:"id"`
	Chosen    string   `json:"chosen"`
	Rationale string   `json:"rationale"`
	Rejected  []string `json:"rejected"`
}

// Step defines an individual ordered execution action within a task.
type Step struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Expected  string `json:"expected"`
	OnFailure string `json:"on_failure"`
}

// Example gives a literal input/output specification.
type Example struct {
	Input       string `json:"input"`
	Output      string `json:"output"`
	Explanation string `json:"explanation"`
}

// Assertion defines an evaluation assertion inside a check.
type Assertion struct {
	Kind     AssertionKind `json:"kind"`
	Target   string        `json:"target"`
	Expected any           `json:"expected"`
}

// Check defines an executable verification command and its assertions.
type Check struct {
	ID               string            `json:"id"`
	Executable       string            `json:"executable"`
	Args             []string          `json:"args"`
	Cwd              string            `json:"cwd"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
	ExpectedExitCode int               `json:"expected_exit_code"`
	Scope            Scope             `json:"scope"`
	Assertions       []Assertion       `json:"assertions"`
	Capability       Capability        `json:"capability"`
	Environment      map[string]string `json:"environment"`
}

// AcceptanceCase defines a testable acceptance scenario.
type AcceptanceCase struct {
	ID            string   `json:"id"`
	Requirement   string   `json:"requirement"`
	Given         string   `json:"given"`
	Action        string   `json:"action"`
	Expected      string   `json:"expected"`
	RequiredScope Scope    `json:"required_scope"`
	Mandatory     bool     `json:"mandatory"`
	CheckIDs      []string `json:"check_ids"`
}

// DependencyCondition specifies predecessor artifacts or decisions required for readiness.
type DependencyCondition struct {
	Kind     DependencyConditionKind `json:"kind"`
	GoalSlug string                  `json:"goal_slug"`
	TaskID   string                  `json:"task_id,omitempty"`
	Artifact string                  `json:"artifact,omitempty"`
	SHA256   string                  `json:"sha256,omitempty"`
	Pointer  string                  `json:"pointer,omitempty"`
	Equals   any                     `json:"equals,omitempty"`
}

// TaskContract defines a self-contained, typed task execution assignment.
type TaskContract struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Context        string      `json:"context"`
	Why            string      `json:"why"`
	DependsOn      []string    `json:"depends_on"`
	Files          TaskFiles   `json:"files"`
	References     []Reference `json:"references"`
	Decisions      []Decision  `json:"decisions"`
	Steps          []Step      `json:"steps"`
	Interfaces     []string    `json:"interfaces"`
	Constraints    []string    `json:"constraints"`
	Examples       []Example   `json:"examples"`
	AcceptanceIDs  []string    `json:"acceptance_ids"`
	Checks         []Check     `json:"checks"`
	StopConditions []string    `json:"stop_conditions"`
	ReturnFormat   string      `json:"return_format"`
}

// WorkPackage defines the complete v1 typed contract specification for a goal.
type WorkPackage struct {
	SchemaVersion        string                `json:"schema_version"`
	GoalSlug             string                `json:"goal_slug"`
	Revision             int                   `json:"revision"`
	Purpose              string                `json:"purpose"`
	Constraints          []string              `json:"constraints"`
	NonGoals             []string              `json:"non_goals"`
	Tasks                []TaskContract        `json:"tasks"`
	Acceptance           []AcceptanceCase      `json:"acceptance"`
	DependencyConditions []DependencyCondition `json:"dependency_conditions"`
	UnresolvedDecisions  []string              `json:"unresolved_decisions"`
	RetiredTaskIDs       []string              `json:"retired_task_ids"`
	RetiredAcceptanceIDs []string              `json:"retired_acceptance_ids"`
}

var (
	taskIDRegex       = regexp.MustCompile(`^T-[0-9]+$`)
	acceptanceIDRegex = regexp.MustCompile(`^AC-[0-9]+$`)
	decisionIDRegex   = regexp.MustCompile(`^D-[0-9]+$`)
	stepIDRegex       = regexp.MustCompile(`^S-[0-9]+$`)
	checkIDRegex      = regexp.MustCompile(`^C-[0-9]+$`)
)

// DecodeWorkPackage strictly decodes and validates a WorkPackage from JSON bytes.
// It rejects unknown fields, duplicate object keys, invalid enums, retired ID reuse,
// and trailing data after the JSON value.
func DecodeWorkPackage(data []byte) (*WorkPackage, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("empty work package data")
	}

	// 1. Enforce no duplicate object keys and no trailing bytes
	if err := checkDuplicateKeysAndEOF(data); err != nil {
		return nil, err
	}

	// 2. Strict decoding with unknown fields rejection
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var pkg WorkPackage
	if err := dec.Decode(&pkg); err != nil {
		return nil, fmt.Errorf("decode work package: %w", err)
	}

	// 3. Domain validation
	if err := ValidateWorkPackage(&pkg); err != nil {
		return nil, err
	}

	return &pkg, nil
}

// ValidateWorkPackage checks all structural and semantic invariants of a WorkPackage.
func ValidateWorkPackage(pkg *WorkPackage) error {
	if pkg.SchemaVersion != WorkPackageSchemaVersion {
		return fmt.Errorf("unsupported schema_version: %q (expected %q)", pkg.SchemaVersion, WorkPackageSchemaVersion)
	}
	if strings.TrimSpace(pkg.GoalSlug) == "" {
		return fmt.Errorf("missing or empty goal_slug")
	}
	if pkg.Revision < 1 {
		return fmt.Errorf("invalid revision %d: must be >= 1", pkg.Revision)
	}
	if strings.TrimSpace(pkg.Purpose) == "" {
		return fmt.Errorf("missing or empty purpose")
	}
	if len(pkg.Tasks) == 0 {
		return fmt.Errorf("missing tasks: work package must contain at least one task")
	}

	retiredTasks := make(map[string]bool)
	for _, id := range pkg.RetiredTaskIDs {
		if retiredTasks[id] {
			return fmt.Errorf("duplicate retired_task_id %q", id)
		}
		retiredTasks[id] = true
	}

	retiredAcceptance := make(map[string]bool)
	for _, id := range pkg.RetiredAcceptanceIDs {
		if retiredAcceptance[id] {
			return fmt.Errorf("duplicate retired_acceptance_id %q", id)
		}
		retiredAcceptance[id] = true
	}

	taskIDs := make(map[string]bool)
	for i, t := range pkg.Tasks {
		if !taskIDRegex.MatchString(t.ID) {
			return fmt.Errorf("task[%d] has invalid id %q: must match T-NNN", i, t.ID)
		}
		if taskIDs[t.ID] {
			return fmt.Errorf("duplicate task id %q", t.ID)
		}
		if retiredTasks[t.ID] {
			return fmt.Errorf("reused retired task id %q in task", t.ID)
		}
		taskIDs[t.ID] = true

		if strings.TrimSpace(t.Title) == "" {
			return fmt.Errorf("task %s: missing title", t.ID)
		}
		if strings.TrimSpace(t.Context) == "" {
			return fmt.Errorf("task %s: missing context", t.ID)
		}
		if strings.TrimSpace(t.Why) == "" {
			return fmt.Errorf("task %s: missing why", t.ID)
		}
		if t.ReturnFormat != TaskResultReturnFormat {
			return fmt.Errorf("task %s: invalid return_format %q (expected %q)", t.ID, t.ReturnFormat, TaskResultReturnFormat)
		}
		if len(t.Files.Edit)+len(t.Files.Create) == 0 {
			return fmt.Errorf("task %s: edit + create files must be non-empty", t.ID)
		}

		stepIDs := make(map[string]bool)
		for si, s := range t.Steps {
			if !stepIDRegex.MatchString(s.ID) {
				return fmt.Errorf("task %s: step[%d] invalid id %q: must match S-NNN", t.ID, si, s.ID)
			}
			if stepIDs[s.ID] {
				return fmt.Errorf("task %s: duplicate step id %q", t.ID, s.ID)
			}
			stepIDs[s.ID] = true
			if strings.TrimSpace(s.Action) == "" || strings.TrimSpace(s.Expected) == "" || strings.TrimSpace(s.OnFailure) == "" {
				return fmt.Errorf("task %s: step %s has empty required fields", t.ID, s.ID)
			}
		}

		decisionIDs := make(map[string]bool)
		for di, d := range t.Decisions {
			if !decisionIDRegex.MatchString(d.ID) {
				return fmt.Errorf("task %s: decision[%d] invalid id %q: must match D-NNN", t.ID, di, d.ID)
			}
			if decisionIDs[d.ID] {
				return fmt.Errorf("task %s: duplicate decision id %q", t.ID, d.ID)
			}
			decisionIDs[d.ID] = true
			if strings.TrimSpace(d.Chosen) == "" || strings.TrimSpace(d.Rationale) == "" {
				return fmt.Errorf("task %s: decision %s has empty chosen or rationale", t.ID, d.ID)
			}
		}

		checkIDs := make(map[string]bool)
		for ci, c := range t.Checks {
			if !checkIDRegex.MatchString(c.ID) {
				return fmt.Errorf("task %s: check[%d] invalid id %q: must match C-NNN", t.ID, ci, c.ID)
			}
			if checkIDs[c.ID] {
				return fmt.Errorf("task %s: duplicate check id %q", t.ID, c.ID)
			}
			checkIDs[c.ID] = true

			if c.TimeoutSeconds <= 0 {
				return fmt.Errorf("task %s: check %s timeout_seconds must be > 0", t.ID, c.ID)
			}
			if !c.Scope.IsValid() {
				return fmt.Errorf("task %s: check %s has invalid scope %q", t.ID, c.ID, c.Scope)
			}
			if !c.Capability.IsValid() {
				return fmt.Errorf("task %s: check %s has invalid capability %q", t.ID, c.ID, c.Capability)
			}
			if c.Capability == CapabilityManual {
				if c.Executable != "" || len(c.Args) > 0 || c.ExpectedExitCode != 0 {
					return fmt.Errorf("task %s: manual check %s must have executable=\"\", args=[], exit_code=0", t.ID, c.ID)
				}
			}
			for ai, a := range c.Assertions {
				if !a.Kind.IsValid() {
					return fmt.Errorf("task %s: check %s assertion[%d] has invalid kind %q", t.ID, c.ID, ai, a.Kind)
				}
				if strings.TrimSpace(a.Target) == "" {
					return fmt.Errorf("task %s: check %s assertion[%d] has empty target", t.ID, c.ID, ai)
				}
				if a.Expected == nil {
					return fmt.Errorf("task %s: check %s assertion[%d] has nil expected", t.ID, c.ID, ai)
				}
			}
		}
	}

	// Verify all task dependencies: must exist, not self, and acyclic
	for _, t := range pkg.Tasks {
		for _, dep := range t.DependsOn {
			if !taskIDs[dep] {
				return fmt.Errorf("task %s depends on unknown task %q", t.ID, dep)
			}
			if dep == t.ID {
				return fmt.Errorf("task %s cannot depend on itself", t.ID)
			}
		}
	}
	if err := checkTaskDependencyCycles(pkg.Tasks); err != nil {
		return err
	}

	// Enforce global check ID uniqueness across all tasks
	allCheckIDs := make(map[string]string)
	for _, t := range pkg.Tasks {
		for _, c := range t.Checks {
			if existingTask, ok := allCheckIDs[c.ID]; ok {
				return fmt.Errorf("duplicate check id %q (defined in task %s and task %s)", c.ID, existingTask, t.ID)
			}
			allCheckIDs[c.ID] = t.ID
		}
	}

	caseIDs := make(map[string]bool)
	for i, ac := range pkg.Acceptance {
		if !acceptanceIDRegex.MatchString(ac.ID) {
			return fmt.Errorf("acceptance[%d] has invalid id %q: must match AC-NNN", i, ac.ID)
		}
		if caseIDs[ac.ID] {
			return fmt.Errorf("duplicate acceptance id %q", ac.ID)
		}
		if retiredAcceptance[ac.ID] {
			return fmt.Errorf("reused retired acceptance id %q in acceptance case", ac.ID)
		}
		caseIDs[ac.ID] = true

		if strings.TrimSpace(ac.Requirement) == "" || strings.TrimSpace(ac.Given) == "" ||
			strings.TrimSpace(ac.Action) == "" || strings.TrimSpace(ac.Expected) == "" {
			return fmt.Errorf("acceptance %s has empty required fields", ac.ID)
		}
		if !ac.RequiredScope.IsValid() {
			return fmt.Errorf("acceptance %s has invalid required_scope %q", ac.ID, ac.RequiredScope)
		}
		if ac.Mandatory && len(ac.CheckIDs) == 0 {
			return fmt.Errorf("acceptance %s is mandatory but declares no check_ids", ac.ID)
		}
		for _, cid := range ac.CheckIDs {
			if _, ok := allCheckIDs[cid]; !ok {
				return fmt.Errorf("acceptance %s references unknown check_id %q", ac.ID, cid)
			}
		}
	}

	// Verify all task acceptance_ids exist in Acceptance
	for _, t := range pkg.Tasks {
		for _, aid := range t.AcceptanceIDs {
			if !caseIDs[aid] {
				return fmt.Errorf("task %s references unknown acceptance_id %q", t.ID, aid)
			}
		}
	}

	// Verify all dependency conditions
	for i, dc := range pkg.DependencyConditions {
		if !dc.Kind.IsValid() {
			return fmt.Errorf("dependency_condition[%d] has invalid kind %q", i, dc.Kind)
		}
		if strings.TrimSpace(dc.GoalSlug) == "" {
			return fmt.Errorf("dependency_condition[%d] missing goal_slug", i)
		}
		switch dc.Kind {
		case DepConditionAcceptedTask:
			if strings.TrimSpace(dc.TaskID) == "" {
				return fmt.Errorf("dependency_condition[%d] (accepted_task) missing task_id", i)
			}
		case DepConditionArtifactDigest:
			if strings.TrimSpace(dc.Artifact) == "" || strings.TrimSpace(dc.SHA256) == "" {
				return fmt.Errorf("dependency_condition[%d] (artifact_digest) missing artifact or sha256", i)
			}
		case DepConditionDecisionEquals:
			if strings.TrimSpace(dc.Artifact) == "" || strings.TrimSpace(dc.Pointer) == "" || dc.Equals == nil {
				return fmt.Errorf("dependency_condition[%d] (decision_equals) missing artifact, pointer, or equals", i)
			}
		}
	}

	return nil
}

// WorkPackageDigest calculates a deterministic SHA-256 digest of the normative content of a WorkPackage.
// Arrays of tasks, acceptance cases, and ID sets are sorted lexicographically so that array ordering
// does not alter the digest, while preserving the internal sequential step and example order.
func WorkPackageDigest(pkg *WorkPackage) (string, error) {
	if pkg == nil {
		return "", fmt.Errorf("nil work package")
	}

	canon := cloneWorkPackage(pkg)

	// Lexicographically sort top-level arrays that represent logical sets
	sort.Strings(canon.Constraints)
	sort.Strings(canon.NonGoals)
	sort.Strings(canon.UnresolvedDecisions)
	sort.Strings(canon.RetiredTaskIDs)
	sort.Strings(canon.RetiredAcceptanceIDs)

	// Sort tasks by ID
	sort.Slice(canon.Tasks, func(i, j int) bool {
		return canon.Tasks[i].ID < canon.Tasks[j].ID
	})

	for i := range canon.Tasks {
		t := &canon.Tasks[i]
		sort.Strings(t.DependsOn)
		sort.Strings(t.AcceptanceIDs)
		sort.Strings(t.Interfaces)
		sort.Strings(t.Constraints)
		sort.Strings(t.StopConditions)

		sort.Strings(t.Files.Read)
		sort.Strings(t.Files.Edit)
		sort.Strings(t.Files.Create)
		sort.Strings(t.Files.Forbid)

		sort.Slice(t.Decisions, func(a, b int) bool {
			return t.Decisions[a].ID < t.Decisions[b].ID
		})
		for di := range t.Decisions {
			sort.Strings(t.Decisions[di].Rejected)
		}

		sort.Slice(t.Checks, func(a, b int) bool {
			return t.Checks[a].ID < t.Checks[b].ID
		})
		// Notice: t.Steps, t.Examples, and t.Checks[ci].Args preserve their natural sequential/positional order.
	}

	// Sort acceptance cases by ID
	sort.Slice(canon.Acceptance, func(i, j int) bool {
		return canon.Acceptance[i].ID < canon.Acceptance[j].ID
	})
	for i := range canon.Acceptance {
		sort.Strings(canon.Acceptance[i].CheckIDs)
	}

	// Sort dependency conditions deterministically
	sort.Slice(canon.DependencyConditions, func(i, j int) bool {
		ki := fmt.Sprintf("%s:%s:%s:%s", canon.DependencyConditions[i].GoalSlug, canon.DependencyConditions[i].Kind, canon.DependencyConditions[i].TaskID, canon.DependencyConditions[i].Artifact)
		kj := fmt.Sprintf("%s:%s:%s:%s", canon.DependencyConditions[j].GoalSlug, canon.DependencyConditions[j].Kind, canon.DependencyConditions[j].TaskID, canon.DependencyConditions[j].Artifact)
		return ki < kj
	})

	canonBytes, err := json.Marshal(canon)
	if err != nil {
		return "", fmt.Errorf("marshal canonical work package: %w", err)
	}

	h := sha256.New()
	h.Write([]byte(canon.SchemaVersion + "\n"))
	h.Write(canonBytes)

	return hex.EncodeToString(h.Sum(nil)), nil
}

func cloneWorkPackage(p *WorkPackage) *WorkPackage {
	cp := *p

	cp.Constraints = append([]string(nil), p.Constraints...)
	cp.NonGoals = append([]string(nil), p.NonGoals...)
	cp.UnresolvedDecisions = append([]string(nil), p.UnresolvedDecisions...)
	cp.RetiredTaskIDs = append([]string(nil), p.RetiredTaskIDs...)
	cp.RetiredAcceptanceIDs = append([]string(nil), p.RetiredAcceptanceIDs...)

	cp.Tasks = make([]TaskContract, len(p.Tasks))
	for i, t := range p.Tasks {
		ct := t
		ct.DependsOn = append([]string(nil), t.DependsOn...)
		ct.Interfaces = append([]string(nil), t.Interfaces...)
		ct.Constraints = append([]string(nil), t.Constraints...)
		ct.AcceptanceIDs = append([]string(nil), t.AcceptanceIDs...)
		ct.StopConditions = append([]string(nil), t.StopConditions...)

		ct.Files.Read = append([]string(nil), t.Files.Read...)
		ct.Files.Edit = append([]string(nil), t.Files.Edit...)
		ct.Files.Create = append([]string(nil), t.Files.Create...)
		ct.Files.Forbid = append([]string(nil), t.Files.Forbid...)

		ct.References = append([]Reference(nil), t.References...)
		ct.Steps = append([]Step(nil), t.Steps...)
		ct.Examples = append([]Example(nil), t.Examples...)

		ct.Decisions = make([]Decision, len(t.Decisions))
		for di, d := range t.Decisions {
			cd := d
			cd.Rejected = append([]string(nil), d.Rejected...)
			ct.Decisions[di] = cd
		}

		ct.Checks = make([]Check, len(t.Checks))
		for ci, c := range t.Checks {
			cc := c
			cc.Args = append([]string(nil), c.Args...)
			cc.Assertions = append([]Assertion(nil), c.Assertions...)
			if c.Environment != nil {
				cc.Environment = make(map[string]string, len(c.Environment))
				for k, v := range c.Environment {
					cc.Environment[k] = v
				}
			}
			ct.Checks[ci] = cc
		}
		cp.Tasks[i] = ct
	}

	cp.Acceptance = make([]AcceptanceCase, len(p.Acceptance))
	for i, a := range p.Acceptance {
		ca := a
		ca.CheckIDs = append([]string(nil), a.CheckIDs...)
		cp.Acceptance[i] = ca
	}

	cp.DependencyConditions = append([]DependencyCondition(nil), p.DependencyConditions...)

	return &cp
}

const maxJSONNestingDepth = 32

func checkDuplicateKeysAndEOF(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := checkTokenDuplicateKeys(dec, 0); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing data after json value")
	}
	return nil
}

func checkTokenDuplicateKeys(dec *json.Decoder, depth int) error {
	if depth > maxJSONNestingDepth {
		return fmt.Errorf("exceeded maximum JSON nesting depth of %d", maxJSONNestingDepth)
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("expected string object key, got %v", keyTok)
			}
			if seen[key] {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = true
			if err := checkTokenDuplicateKeys(dec, depth+1); err != nil {
				return err
			}
		}
		// Consume closing '}'
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := checkTokenDuplicateKeys(dec, depth+1); err != nil {
				return err
			}
		}
		// Consume closing ']'
		_, err := dec.Token()
		return err
	}
	return nil
}

func checkTaskDependencyCycles(tasks []TaskContract) error {
	adj := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		adj[t.ID] = t.DependsOn
	}
	state := make(map[string]int, len(tasks)) // 0: unvisited, 1: visiting, 2: visited
	var visit func(id string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("task dependency cycle detected involving task %q", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, dep := range adj[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, t := range tasks {
		if state[t.ID] == 0 {
			if err := visit(t.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
