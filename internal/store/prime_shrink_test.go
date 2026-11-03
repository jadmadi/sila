package store

import (
	"context"
	"strings"
	"testing"
)

func sampleBriefingText() string {
	return `### 🧠 Project Memory (sila · project: test-proj)

**Working Contract (7-Step Loop)**:
1. **Heal & Prime**: ` + "`sila heal`" + ` → ` + "`sila prime`" + `
2. **Pick Work**: ` + "`sila goals ready`" + `
3. **When Stuck**: ` + "`sila recall`" + `
4. **When Deciding**: ` + "`sila memo`" + `
5. **When Done**: commit ` + "`feat: ... (goals/<slug>/goal.md)`" + `
6. **Report Back**: goal slug and recorded lessons
7. **Friction**: ` + "`sila feedback`" + `
_Note: If ` + "`sila`" + ` is not in PATH, use ` + "`~/.local/bin/sila`" + `._

**Active Session Handoff** (by agent, 2026-09-29 19:00 on ` + "`main`" + `):
- **Summary**: Implemented core lock mechanics and verified race-clean.
- **Tasks & Open Items**:
  - [ ] Implement context shrink engine
  - [ ] Wire CLI flags
- **Decisions**:
  • Retain Tier 1 invariants unconditionally

**Active Goal Specification & Acceptance Contract**:
• 🎯 **multi-pressure-context-shrink**: Multi-Pressure Context Shrink & Deterministic Degradation Engine
  - Assigned: ` + "`@antigravity`" + `
  - Verification Command: ` + "`go test -race ./internal/store/...`" + `

**Completed Roadmap Goals**:
• ✔ ` + "`goal-scoped-charters-and-locks`" + `: Goal Scoped Charters & Lock Sentry
• ✔ ` + "`immutable-goal-acceptance-contracts`" + `: Cryptographic Goal Acceptance Contracts

**Discovered Repo Runbook (Guidance)**:
• **build**: ` + "`./build.sh`" + `
• **test**: ` + "`go test -race ./...`" + `

**Key Architectural Facts & Decisions**:
• Pure Go SQLite engine without CGO.
  This is a long prose narrative explaining why CGO was omitted. It discusses modernc.org/sqlite and compatibility across Linux, macOS, and Windows.
• Memory isolation invariant between projects.
  Another lengthy explanatory paragraph about workspace isolation.

**File-Referenced Claims** (observed in sessions, not distilled — verify before trusting):
• Observed handler in store (` + "`internal/store/lock.go`" + `)
• Untracked old legacy claim from six months ago

**Solved Before (solution lessons)**:
• 💡 Symptom: subshell exit 127 in sandbox
  fix:
  ` + "```diff" + `
  - cmd := exec.Command("sila")
  + cmd := exec.Command("~/.local/bin/sila")
  ` + "```" + `
• 💡 Symptom: race condition in store flock
  · cause: missing mutex wrapper
  · fix: wrap with sync.Mutex

**Candidate Directives (observed, NOT approved — review before treating as rules)**:
• ◻ Consider migrating to BoltDB _[cli]_

**Strategic Guardrails (Explicit Non-Goals)**:
• ⛔ Do not introduce CGO dependencies

**Attached Reference Docs & Sources**:
• **Raven WindowPressure Reference** (https://docs.raven.internal)

**Disk Grounding**: 42/42 claims verified with existing files on disk.
**Context Surface**: ~4.5k tok [Light]`
}

func TestWindowPressureThresholds(t *testing.T) {
	if DeterminePressure(1000) != PressureNormal {
		t.Fatalf("expected PressureNormal, got %s", DeterminePressure(1000))
	}
	if DeterminePressure(14999) != PressureNormal {
		t.Fatalf("expected PressureNormal, got %s", DeterminePressure(14999))
	}
	if DeterminePressure(15000) != PressureWatch {
		t.Fatalf("expected PressureWatch, got %s", DeterminePressure(15000))
	}
	if DeterminePressure(25000) != PressureWatch {
		t.Fatalf("expected PressureWatch, got %s", DeterminePressure(25000))
	}
	if DeterminePressure(25001) != PressureHeavy {
		t.Fatalf("expected PressureHeavy, got %s", DeterminePressure(25001))
	}
	if DeterminePressure(40000) != PressureHeavy {
		t.Fatalf("expected PressureHeavy, got %s", DeterminePressure(40000))
	}
	if DeterminePressure(40001) != PressureCritical {
		t.Fatalf("expected PressureCritical, got %s", DeterminePressure(40001))
	}
}

func TestShrinkBriefingInviolableTier1UnderCritical(t *testing.T) {
	raw := sampleBriefingText()
	ctx := context.Background()

	res := ShrinkBriefing(ctx, 0, raw, PressureCritical)

	if res.Pressure != PressureCritical {
		t.Fatalf("expected PressureCritical, got %s", res.Pressure)
	}

	// Inviolable 1: Core rules / working contract
	if !strings.Contains(res.Payload, "**Working Contract (7-Step Loop)**:") {
		t.Errorf("CRITICAL pressure dropped core working contract")
	}

	// Inviolable 2: Active Goal Specification & Acceptance Contract
	if !strings.Contains(res.Payload, "**Active Goal Specification & Acceptance Contract**:") {
		t.Errorf("CRITICAL pressure dropped active goal contract")
	}
	if !strings.Contains(res.Payload, "multi-pressure-context-shrink") {
		t.Errorf("CRITICAL pressure dropped active goal slug")
	}

	// Inviolable 3: Session Handoff open items
	if !strings.Contains(res.Payload, "**Active Session Handoff**") {
		t.Errorf("CRITICAL pressure dropped active handoff")
	}
	if !strings.Contains(res.Payload, "- [ ] Implement context shrink engine") {
		t.Errorf("CRITICAL pressure dropped handoff open checklist items")
	}

	// Must NOT contain Tier 2/3/4 suppressed elements
	if strings.Contains(res.Payload, "**Completed Roadmap Goals**:") {
		t.Errorf("CRITICAL pressure retained completed roadmap goals")
	}
	if strings.Contains(res.Payload, "**Solved Before (solution lessons)**:") {
		t.Errorf("CRITICAL pressure retained past lessons")
	}
	if strings.Contains(res.Payload, "**Key Architectural Facts & Decisions**:") {
		t.Errorf("CRITICAL pressure retained declarative memos")
	}
	if strings.Contains(res.Payload, "**Candidate Directives") {
		t.Errorf("CRITICAL pressure retained candidate directives")
	}
	if strings.Contains(res.Payload, "**Attached Reference Docs") {
		t.Errorf("CRITICAL pressure retained attached docs")
	}
}

func TestShrinkBriefingWatchTier2(t *testing.T) {
	raw := sampleBriefingText()
	ctx := context.Background()

	res := ShrinkBriefing(ctx, 0, raw, PressureWatch)

	if res.Pressure != PressureWatch {
		t.Fatalf("expected PressureWatch, got %s", res.Pressure)
	}

	// Tier 2: Completed roadmap goals MUST be omitted
	if strings.Contains(res.Payload, "**Completed Roadmap Goals**:") {
		t.Errorf("WATCH pressure should omit completed roadmap goals")
	}
	// Active goal MUST be retained
	if !strings.Contains(res.Payload, "**Active Goal Specification & Acceptance Contract**:") {
		t.Errorf("WATCH pressure dropped active goal")
	}

	// Tier 2: Multi-line diff in lesson collapsed to 1-line
	if strings.Contains(res.Payload, "```diff") {
		t.Errorf("WATCH pressure should collapse multi-line diff blocks in lessons")
	}
	if !strings.Contains(res.Payload, "• 💡 Symptom: subshell exit 127 in sandbox") {
		t.Errorf("WATCH pressure dropped lesson symptom")
	}
}

func TestShrinkBriefingHeavyTier3(t *testing.T) {
	raw := sampleBriefingText()
	ctx := context.Background()

	res := ShrinkBriefing(ctx, 0, raw, PressureHeavy)

	if res.Pressure != PressureHeavy {
		t.Fatalf("expected PressureHeavy, got %s", res.Pressure)
	}

	// Tier 3: Narrative paragraphs in memos stripped, retaining bullet lines
	if strings.Contains(res.Payload, "This is a long prose narrative explaining why CGO was omitted.") {
		t.Errorf("HEAVY pressure should strip narrative prose in memos")
	}
	if !strings.Contains(res.Payload, "• Pure Go SQLite engine without CGO.") {
		t.Errorf("HEAVY pressure dropped bulleted invariant in memos")
	}

	// Historical facts outside file refs or active goal omitted
	if strings.Contains(res.Payload, "Untracked old legacy claim") {
		t.Errorf("HEAVY pressure should suppress unreferenced historical facts")
	}
}

func TestShrinkBriefingMonotonicTokenReduction(t *testing.T) {
	raw := sampleBriefingText()
	ctx := context.Background()

	resNormal := ShrinkBriefing(ctx, 0, raw, PressureNormal)
	resWatch := ShrinkBriefing(ctx, 0, raw, PressureWatch)
	resHeavy := ShrinkBriefing(ctx, 0, raw, PressureHeavy)
	resCritical := ShrinkBriefing(ctx, 0, raw, PressureCritical)

	if resWatch.FinalTokens >= resNormal.FinalTokens {
		t.Errorf("WATCH tokens (%d) not strictly less than NORMAL (%d)", resWatch.FinalTokens, resNormal.FinalTokens)
	}
	if resHeavy.FinalTokens >= resWatch.FinalTokens {
		t.Errorf("HEAVY tokens (%d) not strictly less than WATCH (%d)", resHeavy.FinalTokens, resWatch.FinalTokens)
	}
	if resCritical.FinalTokens >= resHeavy.FinalTokens {
		t.Errorf("CRITICAL tokens (%d) not strictly less than HEAVY (%d)", resCritical.FinalTokens, resHeavy.FinalTokens)
	}
}

func BenchmarkShrinkBriefingCritical(b *testing.B) {
	// Construct a large 100KB synthetic briefing
	base := sampleBriefingText()
	var sb strings.Builder
	sb.WriteString(base)
	for i := 0; i < 50; i++ {
		sb.WriteString("\n• Extra historical claim item observed in session (`internal/store/item.go`)\n")
		sb.WriteString("  Prose explanation of the item detailing why it was added and how it works.\n")
	}
	largeBriefing := sb.String()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := ShrinkBriefing(ctx, 0, largeBriefing, PressureCritical)
		if res.FinalTokens <= 0 {
			b.Fatalf("unexpected 0 tokens")
		}
	}
}
