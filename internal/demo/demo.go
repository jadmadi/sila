// Package demo builds an ephemeral, pre-populated sandbox knowledge store
// loaded with a realistic multi-agent team dataset so new users can explore
// the Web Cockpit and CLI search immediately. Everything lives under a
// temporary directory and is removed when the demo session ends.
package demo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/store"
)

// ProjectName is the fictional demo project the sandbox is seeded with.
const ProjectName = "atlas-web"

// Sandbox is one ephemeral demo workspace.
type Sandbox struct {
	Root   string // temporary root holding everything
	Dir    string // project dir (goals/, docs/adr/) scanned by the cockpit
	DBPath string // seeded knowledge store path
}

// Create provisions a fresh sandbox: a temp root, a seeded knowledge store,
// and a project tree with sample goals and ADRs. The caller owns st and must
// Close it before Cleanup.
func Create() (*Sandbox, *store.Store, error) {
	root, err := os.MkdirTemp("", "sila-demo-")
	if err != nil {
		return nil, nil, fmt.Errorf("create sandbox root: %w", err)
	}
	sb := &Sandbox{
		Root:   root,
		Dir:    filepath.Join(root, ProjectName),
		DBPath: filepath.Join(root, "sila.db"),
	}

	st, err := store.Open(sb.DBPath)
	if err != nil {
		sb.Cleanup()
		return nil, nil, fmt.Errorf("open demo store: %w", err)
	}
	// Files must exist before the store seeds grounded records: the write
	// helpers stat RepoFiles to set FileRefsExist, and a claim that points at
	// a missing file is a lie the demo would then have to reconcile.
	if err := seedProjectFiles(sb.Dir); err != nil {
		st.Close()
		sb.Cleanup()
		return nil, nil, fmt.Errorf("seed demo project files: %w", err)
	}
	if err := seedStore(st, sb.Dir); err != nil {
		st.Close()
		sb.Cleanup()
		return nil, nil, fmt.Errorf("seed demo store: %w", err)
	}
	return sb, st, nil
}

// Cleanup removes the sandbox root. Missing roots are ignored so double
// cleanup is safe.
func (sb *Sandbox) Cleanup() {
	if sb == nil || sb.Root == "" {
		return
	}
	_ = os.RemoveAll(sb.Root)
}

// hoursAgo renders an RFC3339 timestamp n hours in the past so the demo
// dataset always looks recent.
func hoursAgo(n int) string {
	return time.Now().UTC().Add(-time.Duration(n) * time.Hour).Format(time.RFC3339)
}

// seedStore populates the store with a realistic multi-agent team dataset:
// cross-agent transcripts, distilled claims, feedback tickets, and a handoff.
func seedStore(st *store.Store, projectDir string) error {
	proj, err := st.EnsureProject(projectDir)
	if err != nil {
		return err
	}
	pid := proj.ID

	type turn struct {
		tool, role, content string
		hours               int
	}
	turns := []turn{
		// Session 1 — payment incident triage, cross-agent.
		{"devin", "user", "Checkout fails intermittently in production: customers see a 500 after the 3DS redirect. Repro rate ~8%. Start by reading src/payments/checkout.go.", 30},
		{"devin", "assistant", "Root cause candidate: the 3DS return webhook races the polling loop in src/payments/checkout.go. When the webhook lands first, markPaid() runs twice and the second write trips the unique constraint, surfacing as a 500. Proposal: make markPaid idempotent and drive the UI off a single state machine.", 29},
		{"claude-code", "assistant", "Confirmed by reading src/payments/checkout.go: markPaid() has no idempotency guard, and the webhook handler calls it before the poller does. An idempotency key on (order_id, provider_event_id) fixes the double write. The polling loop can then be simplified or dropped.", 28},
		{"codex", "assistant", "The provider docs say webhook delivery is at-least-once, so dedupe on provider_event_id is mandatory. Also note the retry backlog: failed webhook deliveries replay up to 72h later, so the fix must tolerate late events, not just concurrent ones.", 27},
		{"opencode", "assistant", "I added a regression test: two concurrent markPaid() calls for the same order now commit exactly once. The 500 rate in staging dropped to zero after the idempotency patch.", 26},
		{"cursor", "assistant", "IDE inline check: the dedupe table already has a unique index on (order_id, provider_event_id), so the idempotency guard can rely on the constraint instead of a read-then-write.", 25},
		{"mimocode", "assistant", "Follow-up: the same at-least-once pattern applies to the refund webhook in src/payments/refunds.go. It currently trusts event ordering; worth an idempotency key before we ship the checkout fix.", 25},
		{"devin", "assistant", "Decision: ship the idempotency patch for checkout first, then apply the same key scheme to refunds. The poller stays as a fallback for missed webhooks but no longer writes state — it only reconciles.", 24},

		// Session 2 — search & roadmap planning.
		{"claude-code", "user", "Plan the search filters epic: users want to filter the dashboard by status, owner, and date range. Keep it client-side first.", 20},
		{"mimocode", "assistant", "Client-side filtering is fine up to ~10k rows. Beyond that we need server-side pagination; propose shipping filters client-side now and adding a /api/search cursor endpoint when the dataset grows.", 19},
		{"codex", "assistant", "Agreed on the two-phase plan. For the cursor endpoint, use opaque cursors (not offsets) so inserts between pages don't shuffle results. Store the sort key + tiebreaker in the cursor payload.", 18},
		{"devin", "assistant", "Roadmap note: search filters ships after the payment hardening lands. The demo goal board mirrors this ordering: payment-webhooks is in progress, search-filters is ready, billing-migrations is blocked on search-filters.", 17},
		{"claude-code", "assistant", "I sketched the filter state machine: idle → filtering → debounced-apply. Debounce at 150ms keeps the UI snappy without hammering re-renders. Empty states need copy for zero-result filters.", 16},

		// Session 3 — infra & ops chatter.
		{"opencode", "user", "The staging Postgres replica lagged 40s during the last deploy. Any ideas?", 12},
		{"devin", "assistant", "The deploy script runs a full VACUUM before the migration window; on staging that holds the write lock and starves the replication stream. Move VACUUM out of the deploy path into the nightly maintenance job.", 11},
		{"codex", "assistant", "Also add a lag guard: pause deploys when replication lag exceeds 10s. A pre-deploy check reading pg_stat_replication is enough; no need for a full monitoring stack yet.", 10},
		{"mimocode", "assistant", "Deploy runbook updated: VACUUM moved to nightly, lag guard added to the pre-deploy checklist. Staging deploys are clean on the last three runs.", 9},
	}

	var msgs []store.LoadMessage
	for _, t := range turns {
		msgs = append(msgs, store.LoadMessage{
			ProjectID:   pid,
			ContentHash: store.ContentHash(t.role, t.content),
			TS:          hoursAgo(t.hours),
			Role:        t.role,
			Content:     t.content,
			Tool:        t.tool,
		})
	}
	if _, _, err := st.WriteRun("", turns[0].tool, "", msgs); err != nil {
		return err
	}

	// Cross-agent corroboration: the same invariant recorded independently by
	// two tools merges into one message with both tools attributed.
	shared := "Invariant: every payment write path must be idempotent on (order_id, provider_event_id); webhook delivery is at-least-once."
	for _, tool := range []string{"devin", "claude-code"} {
		if _, _, err := st.WriteRun("", tool, "", []store.LoadMessage{{
			ProjectID:   pid,
			ContentHash: store.ContentHash("assistant", shared),
			TS:          hoursAgo(23),
			Role:        "assistant",
			Content:     shared,
			Tool:        tool,
		}}); err != nil {
			return err
		}
	}

	facts := []store.Fact{
		{
			ID: "fact_demo_inv_idem", ProjectID: pid,
			Claim: "Invariant: every payment write path must be idempotent on (order_id, provider_event_id); webhook delivery is at-least-once.",
			Kind:  "invariant", Confidence: "invariant", Corroboration: "invariant",
			FileRefsExist: false, Tools: []string{"devin", "claude-code"},
			SessionRef: "manual_memo", FirstSeen: hoursAgo(23), PromotedAt: hoursAgo(23),
			DistillStatus: "skipped", ReviewStatus: store.ReviewApproved, OriginRole: "user",
		},
		{
			ID: "fact_demo_inv_envelope", ProjectID: pid,
			Claim: "Decision: all API responses use the standard envelope {data, error}; endpoints never invent their own error shape.",
			Kind:  "invariant", Confidence: "invariant", Corroboration: "invariant",
			FileRefsExist: false, Tools: []string{"codex"},
			SessionRef: "manual_memo", FirstSeen: hoursAgo(26), PromotedAt: hoursAgo(26),
			DistillStatus: "skipped", ReviewStatus: store.ReviewApproved, OriginRole: "user",
		},
		{
			ID: "fact_demo_ng_framework", ProjectID: pid,
			Claim: "Non-goal: atlas-web never adds a second frontend framework; one SPA stack only.",
			Kind:  "nongoal", Confidence: "invariant", Corroboration: "invariant",
			FileRefsExist: false, Tools: []string{"claude-code"},
			SessionRef: "manual_memo", FirstSeen: hoursAgo(22), PromotedAt: hoursAgo(22),
			DistillStatus: "skipped", ReviewStatus: store.ReviewApproved, OriginRole: "user",
		},
		{
			ID: "fact_demo_ng_orm", ProjectID: pid,
			Claim: "Non-goal: no raw SQL strings in route handlers; data access goes through the repository layer.",
			Kind:  "nongoal", Confidence: "invariant", Corroboration: "invariant",
			FileRefsExist: false, Tools: []string{"devin"},
			SessionRef: "manual_memo", FirstSeen: hoursAgo(21), PromotedAt: hoursAgo(21),
			DistillStatus: "skipped",
		},
		{
			ID: "fact_demo_ms_beta", ProjectID: pid,
			Claim: "Milestone: public beta of atlas-web ships with payment hardening and search filters complete.",
			Kind:  "milestone", Confidence: "invariant", Corroboration: "invariant",
			FileRefsExist: false, Tools: []string{"devin", "mimocode"},
			SessionRef: "manual_memo", FirstSeen: hoursAgo(20), PromotedAt: hoursAgo(20),
			DistillStatus: "skipped",
		},
		{
			ID: "fact_demo_claim_checkout", ProjectID: pid,
			Claim: "markPaid() in src/payments/checkout.go is guarded by an idempotency key and safe under concurrent webhook delivery.",
			Kind:  "code_claim", Confidence: "corroborated", Corroboration: "corroborated",
			Verified: true, FileRefsExist: true, RepoFiles: []string{"src/payments/checkout.go"},
			Tools:      []string{"devin", "claude-code", "opencode"},
			SessionRef: "sess_payment_triage", FirstSeen: hoursAgo(28), PromotedAt: hoursAgo(27),
			Distilled:     "Idempotency key added in PR #482; regression test covers concurrent markPaid calls.",
			DistillStatus: "distilled", RefsCheckedAt: hoursAgo(8),
		},
		{
			ID: "fact_demo_claim_runbook", ProjectID: pid,
			Claim: "The deploy runbook at docs/deploy.md documents the replication lag guard and nightly VACUUM schedule.",
			Kind:  "code_claim", Confidence: "corroborated", Corroboration: "corroborated",
			Verified: true, FileRefsExist: true, RepoFiles: []string{"docs/deploy.md"},
			Tools:      []string{"mimocode", "opencode"},
			SessionRef: "sess_infra", FirstSeen: hoursAgo(9), PromotedAt: hoursAgo(8),
			Distilled:     "Runbook updated after the staging replica lag incident; three clean deploys since.",
			DistillStatus: "distilled", RefsCheckedAt: hoursAgo(7),
		},
		{
			ID: "fact_demo_stale_refund", ProjectID: pid,
			Claim: "Refund flows run through src/payments/refund_legacy.go, which is scheduled for deletion.",
			Kind:  "code_claim", Confidence: "ungrounded", Corroboration: "ungrounded",
			FileRefsExist: false, RepoFiles: []string{"src/payments/refund_legacy.go"},
			Tools:      []string{"mimocode"},
			SessionRef: "sess_payment_triage", FirstSeen: hoursAgo(25), PromotedAt: hoursAgo(25),
			DistillStatus: "pending",
		},
	}
	for i := range facts {
		if err := st.UpsertFact(facts[i]); err != nil {
			return err
		}
	}

	// Deliberate memory records. They must be written through the store write
	// helpers so they carry a hash-chain link and a typed envelope; a lesson
	// or case injected as a raw transcript would not rank at Tier 1000.
	lesson := store.FormatLesson(
		"Checkout returns HTTP 500 on ~8% of 3DS redirects",
		"3DS webhook races the checkout polling loop and calls markPaid() twice",
		"Key every payment write on (order_id, provider_event_id) and make the poller read-only",
		"payments,idempotency,webhooks",
	)
	if _, err := st.InsertLessonWithSession(pid, lesson, "opencode", projectDir, []string{"src/payments/checkout.go", "src/payments/refunds.go"}, "sess_payment_triage"); err != nil {
		return err
	}
	agentCase := store.FormatCase(
		"At-least-once payment and refund webhook processing",
		"Composite idempotency key (order_id, provider_event_id) enforced before state transition",
		"Webhook handlers are idempotent; pollers reconcile without writing state",
		"payments,idempotency",
	)
	if _, err := st.InsertCaseWithSession(pid, agentCase, "claude-code", projectDir, []string{"src/payments/checkout.go"}, "sess_payment_triage"); err != nil {
		return err
	}

	feedback := []store.Feedback{
		{ProjectID: pid, Agent: "devin", Category: "issue", Content: "sila gate times out on a cold build cache; consider warming the build before the test phase.", Status: "open", CreatedAt: hoursAgo(14)},
		{ProjectID: pid, Agent: "claude-code", Category: "idea", Content: "Add a --focus flag to sila digest that narrows the standup to one tool.", Status: "in_progress", CreatedAt: hoursAgo(15)},
		{ProjectID: pid, Agent: "codex", Category: "tip", Content: "sila search expand is great for postmortems; window=6 covers most incident threads.", Status: "closed", CreatedAt: hoursAgo(40), ResolvedAt: hoursAgo(20), ResolvedBy: "devin", ResolutionNote: "Documented in the onboarding guide."},
		{ProjectID: pid, Agent: "opencode", Category: "challenge", Content: "The goals claim lock should surface the holder's last progress note in the audit table.", Status: "open", CreatedAt: hoursAgo(13)},
		{ProjectID: pid, Agent: "mimocode", Category: "praise", Content: "The web cockpit knowledge-healing flow made claim triage a two-minute job.", Status: "closed", CreatedAt: hoursAgo(38), ResolvedAt: hoursAgo(30), ResolvedBy: "jad", ResolutionNote: "Shipped as part of the decision interface."},
	}
	for i := range feedback {
		if feedback[i].ID == "" {
			feedback[i].ID = store.FeedbackID(pid, feedback[i].Category, feedback[i].Content, time.Now().UTC())
		}
		if err := st.SaveFeedback(&feedback[i]); err != nil {
			return err
		}
	}

	handoff := store.Handoff{
		ProjectID: pid,
		Agent:     "devin",
		Branch:    "feat/payment-idempotency",
		Summary:   "Payment idempotency patch merged to staging; refunds key scheme designed but not implemented.",
		OpenItems: []string{"Apply (order_id, provider_event_id) key to src/payments/refunds.go", "Drop the polling writer once webhook coverage hits 100%", "Add replication lag guard to the production deploy checklist"},
		Decisions: []string{"Webhook is the source of truth; the poller only reconciles", "Opaque cursors for any new list endpoint"},
		RepoFiles: []string{"src/payments/checkout.go", "docs/deploy.md"},
		CreatedAt: hoursAgo(8),
		Status:    "open",
	}
	return st.InsertHandoff(handoff)
}

// seedProjectFiles writes the sample goals tree and ADRs the cockpit's
// roadmap and ADR views scan for.
func seedProjectFiles(dir string) error {
	goals := map[string]string{
		"payment-webhooks": `# Goal: Idempotent Payment Webhooks

## Goal Description
Make every payment write path idempotent on (order_id, provider_event_id) so
at-least-once webhook delivery can never double-write an order.

## Dependencies & Execution Order
- **Mode**: Independent (disjoint, parallelizable)
- **Depends On**: none
- **Sequence**: 1
- **Shape**: ship
- **Memory Protocol**: required

## Files to Touch
- src/payments/checkout.go
- src/payments/refunds.go

## References
- **Execution Plan**: [` + "`goals/payment-webhooks/plan.md`" + `](plan.md)

## Done Condition
1. Concurrent and late webhook deliveries commit exactly once.
2. All automated tests pass with -race.
`,
		"search-filters": `# Goal: Dashboard Search Filters

## Goal Description
Client-side filtering by status, owner, and date range, with a documented
upgrade path to opaque-cursor server pagination.

## Dependencies & Execution Order
- **Mode**: Independent (disjoint, parallelizable)
- **Depends On**: none
- **Sequence**: 2
- **Shape**: ship

## References
- **Execution Plan**: [` + "`goals/search-filters/plan.md`" + `](plan.md)

## Done Condition
1. Filters apply with a 150ms debounce and ship zero-result empty states.
2. All automated tests pass with -race.
`,
		"billing-migrations": `# Goal: Billing Schema Migrations

## Goal Description
Introduce versioned billing migrations that reuse the search pagination
cursor format for batched backfills.

## Dependencies & Execution Order
- **Mode**: Sequential
- **Depends On**: search-filters
- **Sequence**: 3
- **Shape**: ship

## References
- **Execution Plan**: [` + "`goals/billing-migrations/plan.md`" + `](plan.md)

## Done Condition
1. Backfills run in bounded batches using opaque cursors.
2. All automated tests pass with -race.
`,
		"dark-mode-toggle": `# Goal: Dark Mode Toggle

## Goal Description
Ship a persisted dark/light theme toggle across the dashboard.

## Dependencies & Execution Order
- **Mode**: Independent (disjoint, parallelizable)
- **Depends On**: none
- **Sequence**: 4
- **Shape**: ship

## References
- **Execution Plan**: [` + "`goals/dark-mode-toggle/plan.md`" + `](plan.md)

## Done Condition
1. Theme preference persists across sessions.
2. All automated tests pass with -race.
`,
	}
	plans := map[string]string{
		"payment-webhooks": `# Plan: Idempotent Payment Webhooks

## Execution Steps
- [x] **Phase 1 — Contracts & Schemas**: define the idempotency key and dedupe table.
- [x] **Phase 2 — Core Implementation**: guard markPaid() and the webhook handler.
- [x] **Phase 3 — Verification & Tests**: concurrency regression test.
- [x] **Phase 4 — Attribution Commit**: commit and audit the goal board.
`,
		"search-filters": `# Plan: Dashboard Search Filters

## Execution Steps
- [ ] **Phase 1 — Contracts & Schemas**: filter state machine and query shape.
- [ ] **Phase 2 — Core Implementation**: debounced client-side filtering.
- [ ] **Phase 3 — Verification & Tests**: zero-result and large-dataset cases.
- [ ] **Phase 4 — Attribution Commit**: commit and audit the goal board.
`,
		"billing-migrations": `# Plan: Billing Schema Migrations

## Execution Steps
- [ ] **Phase 1 — Contracts & Schemas**: versioned migration layout.
- [ ] **Phase 2 — Core Implementation**: cursor-batched backfill runner.
- [ ] **Phase 3 — Verification & Tests**: interrupt-and-resume batches.
- [ ] **Phase 4 — Attribution Commit**: commit and audit the goal board.
`,
		"dark-mode-toggle": `# Plan: Dark Mode Toggle

## Execution Steps
- [x] **Phase 1 — Contracts & Schemas**: theme token map.
- [x] **Phase 2 — Core Implementation**: toggle + persistence.
- [x] **Phase 3 — Verification & Tests**: preference round-trip.
- [x] **Phase 4 — Attribution Commit**: commit and audit the goal board.
`,
	}
	for slug, goal := range goals {
		gdir := filepath.Join(dir, "goals", slug)
		if err := os.MkdirAll(gdir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(gdir, "goal.md"), []byte(goal), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(gdir, "plan.md"), []byte(plans[slug]), 0o644); err != nil {
			return err
		}
		facts := "# Facts: " + slug + "\n\n## Architectural Invariants & Constraints\n- Demo dataset for the sila playground.\n"
		if err := os.WriteFile(filepath.Join(gdir, "facts.md"), []byte(facts), 0o644); err != nil {
			return err
		}
	}
	// Mark dark-mode-toggle claimed by a (stale-looking but active) agent.
	claim := `{
  "goal_slug": "payment-webhooks",
  "claimed_by": "devin",
  "agent": "devin",
  "claimed_at": "` + hoursAgo(6) + `",
  "updated_at": "` + hoursAgo(1) + `",
  "status": "in_progress",
  "note": "Refunds key scheme designed; implementation next."
}`
	if err := os.WriteFile(filepath.Join(dir, "goals", "payment-webhooks", "claim.json"), []byte(claim), 0o644); err != nil {
		return err
	}

	readme := `# Goals — atlas-web (demo)

| Goal | Status | Notes |
| :--- | :--- | :--- |
| payment-webhooks | 🟡 Claimed | @devin |
| search-filters | ⚪ Ready | |
| billing-migrations | ⛔ Blocked | depends on search-filters |
| dark-mode-toggle | 🟢 Implemented | commit 9a1b2c3 |
`
	if err := os.WriteFile(filepath.Join(dir, "goals", "README.md"), []byte(readme), 0o644); err != nil {
		return err
	}

	adrDir := filepath.Join(dir, "docs", "adr")
	if err := os.MkdirAll(adrDir, 0o755); err != nil {
		return err
	}
	adrs := map[string]string{
		"ADR-0001-idempotent-payment-writes.md": `# ADR-0001: Idempotent Payment Writes

* Status: accepted
* Deciders: devin, claude-code, jad
* Date: ` + hoursAgo(28) + `

## Context
Payment webhooks are delivered at-least-once by the provider. The original
checkout flow wrote state from both the webhook and a poller, so concurrent
or late events could double-write and surface 500s.

## Decision
Every payment write path is keyed on (order_id, provider_event_id) and is
idempotent. The webhook is the source of truth; the poller only reconciles
and never writes state.

## Consequences
* Duplicate deliveries are safe by construction.
* Late events (up to 72h replays) are tolerated.
* The poller simplifies to a read-only reconciler.

## References
* ` + "`src/payments/checkout.go`" + `
* ` + "`src/payments/refunds.go`" + `
`,
		"ADR-0002-standard-api-envelope.md": `# ADR-0002: Standard API Envelope

* Status: accepted
* Deciders: codex, jad
* Date: ` + hoursAgo(26) + `

## Context
Endpoints grew ad-hoc error shapes, forcing every client to special-case
responses per route.

## Decision
All API responses use one envelope: {data, error}. error is null on success;
data is null on failure. No endpoint invents its own shape.

## Consequences
* Clients parse one structure everywhere.
* New endpoints have no format decisions to make.
* Envelope changes are a public contract and need a changelog entry.

## References
* ` + "`docs/deploy.md`" + `
`,
	}
	for name, body := range adrs {
		if err := os.WriteFile(filepath.Join(adrDir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}

	// docs/deploy.md grounds the infra claim (file_refs_exist=1).
	deploy := `# Deploy Runbook — atlas-web

1. Pre-deploy: pause when Postgres replication lag exceeds 10s (read
   pg_stat_replication).
2. Migrations run inside the deploy window; VACUUM runs nightly, never in
   the deploy path.
3. Post-deploy: watch the 500 rate for 15 minutes; roll back on any spike.
`
	if err := os.WriteFile(filepath.Join(dir, "docs", "deploy.md"), []byte(deploy), 0o644); err != nil {
		return err
	}

	// Grounded source files. fact_demo_claim_checkout and the seeded lesson and
	// case point at these paths, so they must exist before the store is seeded.
	// src/payments/refund_legacy.go is deliberately absent: it is the one
	// deterministic stale claim the facts stage is meant to catch.
	checkout := `package payments

// markPaid commits a paid state transition exactly once per provider event.
// The unique index on (order_id, provider_event_id) makes concurrent webhook
// deliveries safe: the second writer loses the race instead of double-writing.
func markPaid(orderID, providerEventID string) error {
	return nil
}
`
	refunds := `package payments

// processRefund applies the same idempotency key to refunds. Refund webhooks
// are delivered at-least-once, so the write must tolerate late replays.
func processRefund(orderID, providerEventID string) error {
	return nil
}
`
	paymentsDir := filepath.Join(dir, "src", "payments")
	if err := os.MkdirAll(paymentsDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(paymentsDir, "checkout.go"), []byte(checkout), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(paymentsDir, "refunds.go"), []byte(refunds), 0o644); err != nil {
		return err
	}

	// Managed rules and store wiring. salam derives its wiring state from the
	// heal plan, so a bare marker-only AGENTS.md would report the sandbox as
	// unwired. Compose the same block `sila install` writes instead.
	compiler := agents.NewCompiler(dir)
	managed, err := compiler.CompileProjectManagedBlock(agents.CompileOptions{RepoRoot: dir, ProjectName: ProjectName})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agents.ComposeAgentsFile("", managed)), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, ".sila"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".sila/\ngoals/*/claim.json\n"), 0o644); err != nil {
		return err
	}

	return gitInitDemoRepo(dir)
}

// gitInitDemoRepo turns the project dir into a tiny git repo whose history
// marks dark-mode-toggle implemented (the scanner matches the goal path in
// the commit message).
func gitInitDemoRepo(dir string) error {
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=demo", "GIT_AUTHOR_EMAIL=demo@sila.local",
			"GIT_COMMITTER_NAME=demo", "GIT_COMMITTER_EMAIL=demo@sila.local",
			"GIT_CONFIG_NOSYSTEM=1",
		)
		return cmd.Run()
	}
	if err := run("init", "-q"); err != nil {
		return err
	}
	// Stage the whole tree so the sandbox looks like a real checked-out repo.
	// .gitignore keeps .sila/ and claim files out of history, exactly as a
	// user repository would.
	if err := run("add", "-A"); err != nil {
		return err
	}
	return run("commit", "-q", "--no-gpg-sign", "-m", "feat: implement dark mode toggle (goals/dark-mode-toggle/goal.md)")
}
