package ccli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/cli"
	"github.com/jadmadi/sila/internal/config"
)

func TestSilaInventoryZeroCollisions(t *testing.T) {
	inv := NewSilaInventory()
	AssertNoCollisions(t, inv)
}

func TestPlanRowCoverageAndDomainCounts(t *testing.T) {
	_, cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(cat.PlanRows) != 76 {
		t.Fatalf("len(cat.PlanRows) = %d, want 76", len(cat.PlanRows))
	}

	seenRows := make(map[int]bool, 76)
	domainCounts := make(map[string]int)
	statusCounts := make(map[string]int)

	for _, pr := range cat.PlanRows {
		if pr.PlanRow < 1 || pr.PlanRow > 76 {
			t.Fatalf("plan_row out of range 1..76: %d", pr.PlanRow)
		}
		if seenRows[pr.PlanRow] {
			t.Fatalf("duplicate plan_row %d", pr.PlanRow)
		}
		seenRows[pr.PlanRow] = true
		domainCounts[pr.Domain]++
		statusCounts[pr.RowStatus]++

		switch pr.RowStatus {
		case "implemented", "canonical_only", "mixed", "forbidden", "blocked":
		default:
			t.Fatalf("plan_row %d has invalid row_status %q", pr.PlanRow, pr.RowStatus)
		}

		if (pr.PlanRow == 3 || pr.PlanRow == 46) && pr.RowStatus != "mixed" {
			t.Fatalf("plan_row %d (%s) must have row_status 'mixed', got %q", pr.PlanRow, pr.Command, pr.RowStatus)
		}
		if pr.PlanRow == 76 && pr.RowStatus != "forbidden" {
			t.Fatalf("plan_row 76 must have row_status 'forbidden', got %q", pr.RowStatus)
		}
	}

	wantDomains := map[string]int{
		"Memory & Epistemic Core":         11,
		"Goals & Coordination":            21,
		"Quality Gates & Health":          8,
		"Setup & Onboarding":              6,
		"Store & Ingestion":               17,
		"Surfaces, Cloud & Configuration": 13,
	}
	if !reflect.DeepEqual(domainCounts, wantDomains) {
		t.Fatalf("domain counts = %v, want %v", domainCounts, wantDomains)
	}
}

func TestAll76RowsExactCanonicalArgvMappings(t *testing.T) {
	inv := NewSilaInventory()

	cases := []struct {
		row      int
		phrase   []string
		wantArgv []string
		direct   bool // true if RewriteSilaArgs rewrites directly; false if phrase-only-via-do
	}{
		// Domain 1: Memory & Epistemic Core (01-11)
		{1, []string{"say", "salam"}, []string{"salam"}, true},
		{1, []string{"say", "hello"}, []string{"salam"}, true},
		{1, []string{"greetings"}, []string{"salam"}, true},
		{2, []string{"refresh", "context"}, []string{"prime"}, true},
		{2, []string{"load", "context"}, []string{"prime"}, true},
		{2, []string{"prime", "context"}, []string{"prime"}, false},
		{3, []string{"remember", "error", "sqlite", "busy"}, []string{"recall", "sqlite busy"}, true},
		{3, []string{"lookup", "fix", "for", "sqlite", "busy"}, []string{"recall", "sqlite busy"}, true},
		{4, []string{"find", "wal", "checkpoint"}, []string{"search", "wal checkpoint"}, true},
		{4, []string{"search", "for", "wal", "checkpoint"}, []string{"search", "wal checkpoint"}, false},
		{4, []string{"search", "transcripts", "for", "wal", "checkpoint"}, []string{"search", "--omni", "wal checkpoint"}, false},
		{5, []string{"save", "memo", "use", "pure", "go"}, []string{"memo", "use pure go"}, true},
		{5, []string{"record", "memo", "use", "pure", "go"}, []string{"memo", "use pure go"}, true},
		{5, []string{"remember", "use", "pure", "go"}, []string{"memo", "use pure go"}, true},
		{6, []string{"save", "lesson", "race", "in", "map", "--cause=unprotected", "--fix=mutex"}, []string{"lesson", "race in map", "--cause=unprotected", "--fix=mutex"}, true},
		{6, []string{"record", "lesson", "race", "in", "map", "--cause=unprotected", "--fix=mutex"}, []string{"lesson", "race in map", "--cause=unprotected", "--fix=mutex"}, true},
		{7, []string{"record", "case", "Hexagonal", "Seam", "--approach=ports", "--invariant=pure"}, []string{"case", "Hexagonal Seam", "--approach=ports", "--invariant=pure"}, true},
		{7, []string{"log", "case", "Hexagonal", "Seam", "--approach=ports", "--invariant=pure"}, []string{"case", "Hexagonal Seam", "--approach=ports", "--invariant=pure"}, true},
		{8, []string{"save", "handoff", "finished", "phase", "one"}, []string{"handoff", "finished phase one"}, true},
		{8, []string{"pass", "handoff", "finished", "phase", "one"}, []string{"handoff", "finished phase one"}, true},
		{9, []string{"index", "doc", "docs/reference.md"}, []string{"doc", "docs/reference.md"}, true},
		{9, []string{"ingest", "doc", "docs/reference.md"}, []string{"doc", "docs/reference.md"}, false},
		{10, []string{"check", "rules"}, []string{"agents", "check"}, true},
		{10, []string{"verify", "rules"}, []string{"agents", "check"}, false},
		{11, []string{"compile", "rules"}, []string{"agents", "sync"}, true},
		{11, []string{"build", "rules"}, []string{"agents", "sync"}, true},

		// Domain 2: Goals & Coordination (12-32)
		{12, []string{"list", "goals"}, []string{"goals"}, true},
		{12, []string{"show", "goals"}, []string{"goals"}, true},
		{12, []string{"get", "goals"}, []string{"goals"}, true},
		{12, []string{"show", "ready", "goals"}, []string{"goals", "--ready"}, true},
		{12, []string{"list", "ready", "goals"}, []string{"goals", "--ready"}, true},
		{12, []string{"ready", "goals"}, []string{"goals", "--ready"}, true},
		{12, []string{"show", "pending", "goals"}, []string{"goals", "--pending"}, true},
		{12, []string{"list", "pending", "goals"}, []string{"goals", "--pending"}, true},
		{12, []string{"pending", "goals"}, []string{"goals", "--pending"}, true},
		{12, []string{"show", "claimed", "goals"}, []string{"goals", "--claimed"}, true},
		{12, []string{"list", "claimed", "goals"}, []string{"goals", "--claimed"}, true},
		{12, []string{"claimed", "goals"}, []string{"goals", "--claimed"}, true},
		{12, []string{"show", "done", "goals"}, []string{"goals", "--done"}, true},
		{12, []string{"list", "done", "goals"}, []string{"goals", "--done"}, true},
		{12, []string{"show", "implemented", "goals"}, []string{"goals", "--done"}, true},
		{13, []string{"claim", "goal", "ccli-inventory-completion"}, []string{"goals", "claim", "ccli-inventory-completion"}, true},
		{13, []string{"lock", "goal", "ccli-inventory-completion"}, []string{"goals", "claim", "ccli-inventory-completion"}, true},
		{14, []string{"drop", "goal", "ccli-inventory-completion"}, []string{"goals", "release", "ccli-inventory-completion"}, true},
		{14, []string{"unlock", "goal", "ccli-inventory-completion"}, []string{"goals", "release", "ccli-inventory-completion"}, true},
		{14, []string{"release", "goal", "ccli-inventory-completion"}, []string{"goals", "release", "ccli-inventory-completion"}, false},
		{15, []string{"show", "goal", "ccli-inventory-completion"}, []string{"goals", "report", "ccli-inventory-completion"}, true},
		{15, []string{"report", "goal", "ccli-inventory-completion"}, []string{"goals", "report", "ccli-inventory-completion"}, false},
		{16, []string{"create", "goal", "new-goal"}, []string{"goals", "create", "new-goal"}, true},
		{16, []string{"add", "goal", "new-goal"}, []string{"goals", "create", "new-goal"}, true},
		{17, []string{"list", "decisions"}, []string{"decision", "list"}, true},
		{17, []string{"show", "decisions"}, []string{"decision", "list"}, true},
		{18, []string{"view", "decision", "D1"}, []string{"decision", "view", "D1"}, true},
		{18, []string{"inspect", "decision", "D1"}, []string{"decision", "view", "D1"}, true},
		{19, []string{"create", "decision", "DB", "Driver", "-o", "A: SQLite", "-o", "B: PG", "--recommend=A"}, []string{"decision", "create", "DB Driver", "-o", "A: SQLite", "-o", "B: PG", "--recommend=A"}, true},
		{19, []string{"log", "decision", "DB", "Driver", "-o", "A: SQLite", "-o", "B: PG", "--recommend=A"}, []string{"decision", "create", "DB Driver", "-o", "A: SQLite", "-o", "B: PG", "--recommend=A"}, true},
		{20, []string{"resolve", "decision", "D1", "--choice=A"}, []string{"decision", "resolve", "D1", "--choice=A"}, true},
		{20, []string{"close", "decision", "D1", "--choice=A"}, []string{"decision", "resolve", "D1", "--choice=A"}, true},
		{21, []string{"list", "opinions"}, []string{"opinions", "list"}, true},
		{21, []string{"show", "opinions"}, []string{"opinions", "list"}, true},
		{22, []string{"add", "opinion", "no", "global", "singletons"}, []string{"opinions", "add", "no global singletons"}, true},
		{22, []string{"record", "opinion", "no", "global", "singletons"}, []string{"opinions", "add", "no global singletons"}, true},
		{23, []string{"compile", "opinions"}, []string{"opinions", "compile"}, true},
		{23, []string{"build", "opinions"}, []string{"opinions", "compile"}, true},
		{24, []string{"submit", "feedback", "great", "cli", "ux"}, []string{"feedback", "great cli ux"}, true},
		{24, []string{"record", "feedback", "great", "cli", "ux"}, []string{"feedback", "great cli ux"}, true},
		{24, []string{"report", "issue", "great", "cli", "ux"}, []string{"feedback", "great cli ux"}, false},
		{25, []string{"list", "crew"}, []string{"crew", "list"}, true},
		{25, []string{"show", "crew"}, []string{"crew", "list"}, true},
		{25, []string{"show", "crew", "status"}, []string{"crew", "list"}, true},
		{26, []string{"spawn", "crewmate", "codex", "for", "ccli-inventory-completion"}, []string{"crew", "spawn", "codex", "ccli-inventory-completion"}, true},
		{26, []string{"start", "crewmate", "codex", "for", "ccli-inventory-completion"}, []string{"crew", "spawn", "codex", "ccli-inventory-completion"}, true},
		{27, []string{"kill", "crewmate", "crew-1"}, []string{"crew", "kill", "crew-1"}, true},
		{27, []string{"stop", "crewmate", "crew-1"}, []string{"crew", "kill", "crew-1"}, true},
		{28, []string{"list", "worktrees"}, []string{"worktree", "list"}, true},
		{28, []string{"show", "worktrees"}, []string{"worktree", "list"}, true},
		{29, []string{"create", "worktree", "feat/ccli"}, []string{"worktree", "create", "feat/ccli"}, true},
		{29, []string{"add", "worktree", "feat/ccli"}, []string{"worktree", "create", "feat/ccli"}, true},
		{30, []string{"remove", "worktree", "feat-ccli"}, []string{"worktree", "clean", "feat-ccli"}, true},
		{30, []string{"clean", "worktree", "feat-ccli"}, []string{"worktree", "clean", "feat-ccli"}, false},
		{30, []string{"prune", "all", "worktrees"}, []string{"worktree", "clean", "--all"}, true},
		{31, []string{"show", "digest"}, []string{"digest"}, true},
		{31, []string{"get", "activity", "digest", "--since=24h"}, []string{"digest", "--since=24h"}, true},
		{32, []string{"run", "benchmark"}, []string{"benchmark"}, true},
		{32, []string{"show", "scorecard"}, []string{"benchmark"}, true},

		// Domain 3: Quality Gates & Health (33-40)
		{33, []string{"run", "gate"}, []string{"gate"}, true},
		{33, []string{"check", "gate"}, []string{"gate"}, true},
		{33, []string{"verify", "gate"}, []string{"gate"}, false},
		{34, []string{"run", "doctor"}, []string{"doctor"}, true},
		{34, []string{"check", "system"}, []string{"doctor"}, true},
		{34, []string{"diagnose", "system"}, []string{"doctor"}, true},
		{35, []string{"repair", "system"}, []string{"doctor", "--repair"}, true},
		{35, []string{"repair", "database"}, []string{"doctor", "--repair"}, true},
		{36, []string{"check", "references"}, []string{"verify"}, true},
		{36, []string{"verify", "files"}, []string{"verify"}, false},
		{37, []string{"run", "reportcard"}, []string{"reportcard"}, true},
		{37, []string{"check", "code", "quality"}, []string{"reportcard"}, true},
		{38, []string{"check", "coverage"}, []string{"coverage"}, true},
		{38, []string{"test", "coverage"}, []string{"coverage"}, false},
		{39, []string{"run", "eval"}, []string{"eval", "run"}, true},
		{39, []string{"evaluate", "agents"}, []string{"eval", "run"}, true},
		{40, []string{"check", "release"}, []string{"release", "preflight"}, true},
		{40, []string{"verify", "release", "readiness"}, []string{"release", "preflight"}, false},

		// Domain 4: Setup & Onboarding (41-46)
		{41, []string{"setup", "repository"}, []string{"install"}, true},
		{41, []string{"install", "rules"}, []string{"install"}, false},
		{42, []string{"check", "agent", "rules"}, []string{"agents", "check"}, true},
		{42, []string{"audit", "agent", "rules"}, []string{"agents", "check"}, false},
		{43, []string{"deploy", "agent", "rules"}, []string{"agents", "sync"}, true},
		{43, []string{"sync", "agent", "rules"}, []string{"agents", "sync"}, false},
		{43, []string{"compile", "agent", "rules"}, []string{"agents", "sync", "--dry-run"}, true},
		{44, []string{"check", "onboarding"}, []string{"onboard", "check"}, true},
		{44, []string{"scan", "legacy", "tasks"}, []string{"onboard", "check"}, true},
		{45, []string{"fix", "rules"}, []string{"heal"}, true},
		{45, []string{"heal", "rules"}, []string{"heal"}, false},
		{45, []string{"heal", "project"}, []string{"heal"}, false},
		{46, []string{"dump", "agent", "rules"}, []string{"export", "rules"}, true},

		// Domain 5: Store & Ingestion (47-63)
		{47, []string{"refresh", "memory"}, []string{"sync"}, true},
		{47, []string{"sync", "rules"}, []string{"sync"}, false},
		{47, []string{"sync", "project"}, []string{"sync"}, false},
		{47, []string{"sync", "memory"}, []string{"sync"}, false},
		{48, []string{"start", "live", "cockpit"}, []string{"live"}, true},
		{48, []string{"watch", "live"}, []string{"live"}, false},
		{49, []string{"distill", "memory"}, []string{"dream"}, true},
		{49, []string{"dream", "claims"}, []string{"dream"}, false},
		{50, []string{"show", "status"}, []string{"status"}, true},
		{50, []string{"get", "status"}, []string{"status"}, true},
		{50, []string{"check", "status"}, []string{"status"}, true},
		{51, []string{"pull", "messages", "from", "codex"}, []string{"ingest", "codex"}, true},
		{51, []string{"ingest", "transcripts", "from", "codex"}, []string{"ingest", "codex"}, false},
		{52, []string{"preview", "ingest", "from", "codex"}, []string{"assess", "codex"}, true},
		{52, []string{"assess", "transcripts", "from", "codex"}, []string{"assess", "codex"}, false},
		{53, []string{"list", "projects"}, []string{"projects"}, true},
		{53, []string{"show", "projects"}, []string{"projects"}, true},
		{53, []string{"get", "projects"}, []string{"projects"}, true},
		{54, []string{"merge", "projects", "keep", "into", "drop"}, []string{"projects", "merge", "keep", "drop"}, true},
		{55, []string{"check", "overlaps"}, []string{"overlaps"}, true},
		{55, []string{"show", "overlaps"}, []string{"overlaps"}, true},
		{56, []string{"inspect", "store"}, []string{"store", "status"}, true},
		{56, []string{"show", "store", "status"}, []string{"store", "status"}, true},
		{57, []string{"check", "store", "invariants"}, []string{"store", "verify"}, true},
		{57, []string{"verify", "store"}, []string{"store", "verify"}, false},
		{58, []string{"show", "storage"}, []string{"storage"}, true},
		{58, []string{"check", "disk", "usage"}, []string{"storage"}, true},
		{59, []string{"prune", "disk", "caches"}, []string{"clean"}, true},
		{59, []string{"wipe", "cache", "files"}, []string{"clean"}, true},
		{59, []string{"clean", "caches"}, []string{"clean"}, false},
		{60, []string{"defragment", "store"}, []string{"vacuum"}, true},
		{60, []string{"vacuum", "database"}, []string{"vacuum"}, false},
		{61, []string{"force", "vacuum", "database"}, []string{"vacuum", "--force"}, true},
		{61, []string{"purge", "free", "store", "pages"}, []string{"vacuum", "--force"}, true},
		{62, []string{"snapshot", "store", "to", "/tmp/store.bak"}, []string{"backup", "--out=/tmp/store.bak"}, true},
		{62, []string{"backup", "database"}, []string{"backup"}, false},
		{63, []string{"check", "drift"}, []string{"reconcile"}, true},
		{63, []string{"reconcile", "drift"}, []string{"reconcile"}, false},

		// Domain 6: Surfaces, Cloud & Configuration (64-75)
		{64, []string{"start", "web", "ui"}, []string{"ui"}, true},
		{64, []string{"launch", "cockpit"}, []string{"ui"}, true},
		{65, []string{"launch", "demo"}, []string{"demo"}, true},
		{65, []string{"run", "demo", "playground"}, []string{"demo"}, true},
		{66, []string{"start", "mcp", "server"}, []string{"mcp"}, true},
		{66, []string{"serve", "mcp"}, []string{"mcp"}, true},
		{67, []string{"check", "cloud", "status"}, []string{"cloud", "status"}, true},
		{67, []string{"test", "cloud", "connection"}, []string{"cloud", "status"}, false},
		{68, []string{"drain", "cloud", "mailbox"}, []string{"cloud", "pull"}, true},
		{68, []string{"pull", "cloud", "records"}, []string{"cloud", "pull"}, true},
		{69, []string{"push", "cloud", "sync"}, []string{"cloud", "push"}, true},
		{69, []string{"sync", "cloud", "records"}, []string{"cloud", "push"}, false},
		{70, []string{"check", "gateway", "status"}, []string{"gateway", "status"}, true},
		{70, []string{"test", "relay"}, []string{"gateway", "status"}, false},
		{71, []string{"discover", "tools"}, []string{"sources"}, true},
		{71, []string{"list", "tool", "sources"}, []string{"sources"}, true},
		{72, []string{"show", "config", "search_provider"}, []string{"config", "get", "search_provider"}, true},
		{72, []string{"get", "config", "search_provider"}, []string{"config", "get", "search_provider"}, true},
		{73, []string{"show", "config", "path"}, []string{"config", "path"}, true},
		{73, []string{"get", "config", "path"}, []string{"config", "path"}, true},
		{74, []string{"explain", "config"}, []string{"config", "explain"}, true},
		{74, []string{"describe", "config", "search_provider"}, []string{"config", "explain", "search_provider"}, true},
		{75, []string{"set", "config", "search_provider", "to", "brave"}, []string{"config", "set", "search_provider", "brave"}, true},
	}

	coveredRows := make(map[int]bool, 76)
	for _, tc := range cases {
		coveredRows[tc.row] = true
		res := Parse(tc.phrase, inv)
		if res.ExitCode != ExitSuccess {
			t.Fatalf("row %d phrase %v failed Parse: exit=%d err=%q", tc.row, tc.phrase, res.ExitCode, res.ErrorMessage)
		}
		if !reflect.DeepEqual(res.CanonicalArgs, tc.wantArgv) {
			t.Fatalf("row %d phrase %v CanonicalArgs = %v, want %v", tc.row, tc.phrase, res.CanonicalArgs, tc.wantArgv)
		}

		gotRewrite := RewriteSilaArgs(tc.phrase)
		if tc.direct {
			if !reflect.DeepEqual(gotRewrite, tc.wantArgv) {
				t.Fatalf("row %d direct RewriteSilaArgs(%v) = %v, want %v", tc.row, tc.phrase, gotRewrite, tc.wantArgv)
			}
		} else {
			if !reflect.DeepEqual(gotRewrite, tc.phrase) {
				t.Fatalf("row %d phrase-only-via-do RewriteSilaArgs(%v) = %v, want unchanged %v", tc.row, tc.phrase, gotRewrite, tc.phrase)
			}
		}
	}

	// Row 76 is forbidden
	coveredRows[76] = true
	if len(coveredRows) != 76 {
		t.Fatalf("coveredRows = %d, want all 76 rows", len(coveredRows))
	}
}

func TestPlanCorrectionsAndNegativeCases(t *testing.T) {
	inv := NewSilaInventory()

	// R2.1: rules -> agents check / agents sync
	if got := RewriteSilaArgs([]string{"check", "rules"}); !reflect.DeepEqual(got, []string{"agents", "check"}) {
		t.Fatalf("check rules = %v, want [agents check]", got)
	}
	if got := RewriteSilaArgs([]string{"compile", "rules"}); !reflect.DeepEqual(got, []string{"agents", "sync"}) {
		t.Fatalf("compile rules = %v, want [agents sync]", got)
	}

	// R2.2: crew spawn takes two slots; one-slot 'spawn crewmate <role>' is rejected with exit 2
	resOneSlot := Parse([]string{"spawn", "crewmate", "codex"}, inv)
	if resOneSlot.ExitCode != ExitUsage {
		t.Fatalf("one-slot 'spawn crewmate codex' expected ExitUsage (2), got %d", resOneSlot.ExitCode)
	}

	// R2.3: worktree clean with no target exits 2 with a fix hint
	resNoTarget := Parse([]string{"clean", "worktrees"}, inv)
	if resNoTarget.ExitCode != ExitUsage || !strings.Contains(resNoTarget.ErrorMessage, "worktree") {
		t.Fatalf("bare 'clean worktrees' expected ExitUsage with fix hint, got code=%d msg=%q", resNoTarget.ExitCode, resNoTarget.ErrorMessage)
	}

	// R2.4: release goal direct is NOT rewritten; drop/unlock goal IS rewritten
	if got := RewriteSilaArgs([]string{"release", "goal", "foo"}); !reflect.DeepEqual(got, []string{"release", "goal", "foo"}) {
		t.Fatalf("direct 'release goal foo' should not rewrite, got %v", got)
	}
	if got := RewriteSilaArgs([]string{"drop", "goal", "foo"}); !reflect.DeepEqual(got, []string{"goals", "release", "foo"}) {
		t.Fatalf("direct 'drop goal foo' = %v, want [goals release foo]", got)
	}

	// R2.7: vacuum force -> vacuum --force; backup -> backup --out=<path>; projects merge -> two args
	if got := RewriteSilaArgs([]string{"force", "vacuum", "database"}); !reflect.DeepEqual(got, []string{"vacuum", "--force"}) {
		t.Fatalf("force vacuum database = %v, want [vacuum --force]", got)
	}
	if got := RewriteSilaArgs([]string{"snapshot", "store", "to", "/tmp/x.bak"}); !reflect.DeepEqual(got, []string{"backup", "--out=/tmp/x.bak"}) {
		t.Fatalf("snapshot store to /tmp/x.bak = %v, want [backup --out=/tmp/x.bak]", got)
	}
	if got := RewriteSilaArgs([]string{"merge", "projects", "a", "into", "b"}); !reflect.DeepEqual(got, []string{"projects", "merge", "a", "b"}) {
		t.Fatalf("merge projects a into b = %v, want [projects merge a b]", got)
	}

	// R2.8 & R4: config set secret exits 3 and does not echo secret value
	secretVal := "super-secret-value-999"
	for _, secretKey := range []string{"brave_api_key", "github_token", "cloud_token", "alphaxiv_api_token", "stitch_api_key", "gateway_telegram_token"} {
		if !config.IsSecret(secretKey) {
			t.Fatalf("config.IsSecret(%q) = false, want true", secretKey)
		}
		resSec := Parse([]string{"set", "config", secretKey, "to", secretVal}, inv)
		if resSec.ExitCode != ExitPolicy {
			t.Fatalf("set config %s expected ExitPolicy (3), got %d (%s)", secretKey, resSec.ExitCode, resSec.ErrorMessage)
		}
		if strings.Contains(resSec.ErrorMessage, secretVal) {
			t.Fatalf("ErrorMessage echoed secret value %q: %s", secretVal, resSec.ErrorMessage)
		}
	}
}

func TestPrecedenceTierAndForbiddenMetadataInvariants(t *testing.T) {
	_, cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	tier3Count := 0
	tier3Required := map[string]bool{
		"sila_goals_release":      false,
		"sila_crew_kill":          false,
		"sila_worktree_remove":    false,
		"sila_worktree_prune_all": false,
		"sila_projects_merge":     false,
		"sila_clean_caches":       false,
		"sila_vacuum_force":       false,
	}

	for _, p := range cat.Productions {
		if strings.TrimSpace(p.Description) == "" || strings.TrimSpace(p.Docs) == "" {
			t.Fatalf("production %q missing description or docs anchor", p.ID)
		}

		// Canonical signature test: every canonical_base resolves through internal/cli
		if !cli.CanonicalAccepts(p.CanonicalBase) {
			t.Fatalf("production %q canonical_base %v rejected by cli.CanonicalAccepts", p.ID, p.CanonicalBase)
		}

		// Precedence check: every canonical-first phrase's first token is not a canonical command/alias
		if p.Precedence == "canonical-first" {
			for _, ph := range p.Phrases {
				if len(ph) > 0 && (ph[0].Kind == "keyword" || ph[0].Kind == "glue") {
					if _, isCanon := cli.Lookup(ph[0].Value); isCanon {
						t.Fatalf("canonical-first production %q has phrase starting with canonical command/alias %q", p.ID, ph[0].Value)
					}
				}
			}
		}

		// Tier 2/3 metadata check
		if p.Tier == TierMutation || p.Tier == TierDestructive {
			if p.TargetPolicy == "" {
				t.Fatalf("production %q (%s) missing target_policy", p.ID, p.Tier)
			}
			if p.TargetPolicy == "implicit" && strings.TrimSpace(p.TargetScope) == "" {
				t.Fatalf("production %q has target_policy=implicit with empty target_scope", p.ID)
			}
			if p.DryRunPolicy != "preview" {
				t.Fatalf("production %q (%s) must have dry_run_policy='preview', got %q", p.ID, p.Tier, p.DryRunPolicy)
			}
		}

		if p.Tier == TierDestructive {
			tier3Count++
			if p.ConfirmationPolicy != "required-interactive" {
				t.Fatalf("Tier 3 production %q must have confirmation_policy='required-interactive', got %q", p.ID, p.ConfirmationPolicy)
			}
			if _, ok := tier3Required[p.ID]; ok {
				tier3Required[p.ID] = true
			}
		}
	}

	if tier3Count < 6 {
		t.Fatalf("tier3Count = %d, want at least 6", tier3Count)
	}
	for id, seen := range tier3Required {
		if !seen {
			t.Fatalf("missing required Tier 3 production %q", id)
		}
	}

	// Forbidden structural test
	if len(cat.ForbiddenPhrases) == 0 {
		t.Fatal("expected at least one forbidden_phrases entry")
	}
	for _, fp := range cat.ForbiddenPhrases {
		if fp.ExitCode != 3 || strings.TrimSpace(fp.Reason) == "" || strings.TrimSpace(fp.Remediation) == "" {
			t.Fatalf("invalid forbidden_phrase %+v", fp)
		}
		if len(fp.SensitiveKeys) == 0 {
			t.Fatalf("forbidden_phrase %q missing sensitive_keys", fp.ID)
		}
	}
}
