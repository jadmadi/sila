package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func testProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "a", "b.md"), []byte("x"), 0o644)
	return dir
}

func TestPathRefsFindsExistingFile(t *testing.T) {
	dir := testProject(t)
	got := PathRefs("the layout lives in a/b.md; see CHANGES.md for full notes.", dir)
	if len(got) != 1 || got[0] != "a/b.md" {
		t.Fatalf("PathRefs = %v, want [a/b.md]", got)
	}
}

func TestPathRefsIgnoresMissingAndTraversalAndSingletons(t *testing.T) {
	dir := testProject(t)
	text := "missing.go? ../outside.md is traversal; plainword has no dir part"
	if got := PathRefs(text, dir); len(got) != 0 {
		t.Fatalf("expected no refs, got %v", got)
	}
}

func TestPathRefsKeepsMissingFilesForVerification(t *testing.T) {
	dir := testProject(t)
	got := PathRefs("see a/aview.md for details", dir) // file does not exist
	if len(got) != 1 || got[0] != "a/aview.md" {
		t.Fatalf("PathRefs = %v, want [a/aview.md] (shape kept pending verify)", got)
	}
	if Verify(got, dir) {
		t.Fatal("a/aview.md does not exist, so must not verify")
	}
}

func TestPathRefsRejectsProseTouches(t *testing.T) {
	dir := testProject(t)
	// slash-separated prose (with and without trailing punctuation) must be
	// dropped; a real-looking path with an extension is kept even if not on disk.
	got := PathRefs("fix NewRecords/Merged. and left/arg/description, see help/usage., stable/resolved., or touch cmd/main.go", dir)
	if len(got) != 1 || got[0] != "cmd/main.go" {
		t.Fatalf("PathRefs = %v, want only [cmd/main.go] (prose dropped, dot-path kept)", got)
	}
}

func TestPathRefsRejectsNonPaths(t *testing.T) {
	dir := testProject(t)
	text := "upgraded from 4.107/4.121 and typescript 1.43/1.51.3, see sections 5.1/5.2, 3.4/10.1, also matching .test.ts/.tsx or maktabeh.app/.net and AGENTS.md/workers.json"
	got := PathRefs(text, dir)
	if len(got) != 0 {
		t.Fatalf("expected 0 refs for non-path strings, got %v", got)
	}
}

func TestPathRefsResolvesSubpaths(t *testing.T) {
	dir := testProject(t)
	// Create nested packages/shared/src/auth/session.ts
	subDir := filepath.Join(dir, "packages", "shared", "src", "auth")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "session.ts"), []byte("export {}"), 0644)

	got := PathRefs("I updated auth/session.ts to fix cookies", dir)
	if len(got) != 1 || got[0] != filepath.ToSlash(filepath.Join("packages", "shared", "src", "auth", "session.ts")) {
		t.Fatalf("PathRefs failed to resolve unique subpath, got %v", got)
	}
}

func TestPathRefsAmbiguousDisambiguation(t *testing.T) {
	dir := testProject(t)
	// Create apps/homepage/app/routes/_index.tsx and apps/storefront/app/routes/_index.tsx
	homeDir := filepath.Join(dir, "apps", "homepage", "app", "routes")
	storeDir := filepath.Join(dir, "apps", "storefront", "app", "routes")
	os.MkdirAll(homeDir, 0755)
	os.MkdirAll(storeDir, 0755)
	os.WriteFile(filepath.Join(homeDir, "_index.tsx"), []byte("export {}"), 0644)
	os.WriteFile(filepath.Join(storeDir, "_index.tsx"), []byte("export {}"), 0644)

	// Context mentioning storefront should pick storefront
	got := PathRefs("React Router config in apps/storefront routes/_index.tsx", dir)
	if len(got) != 1 || got[0] != "apps/storefront/app/routes/_index.tsx" {
		t.Fatalf("expected disambiguation to apps/storefront, got %v", got)
	}
}

func TestPathRefsFragmentAndAbsoluteCleaning(t *testing.T) {
	dir := testProject(t)
	driverDir := filepath.Join(dir, "apps", "delivery", "app", "routes")
	os.MkdirAll(driverDir, 0755)
	os.WriteFile(filepath.Join(driverDir, "_layout.drivers.tsx"), []byte("export {}"), 0644)

	// Fragment match: delivery/_layout.drivers.tsx
	got := PathRefs("see delivery/_layout.drivers.tsx", dir)
	if len(got) != 1 || got[0] != "apps/delivery/app/routes/_layout.drivers.tsx" {
		t.Fatalf("expected fragment resolution, got %v", got)
	}

	// Ellipsis rejected
	if gotEllipsis := PathRefs("see .../docs/plan.md", dir); len(gotEllipsis) != 0 {
		t.Fatalf("expected ellipsis path to be rejected, got %v", got)
	}

	// Absolute path cleaned
	absPath := filepath.Join(dir, "a", "b.md")
	gotAbs := PathRefs("in n"+absPath, dir)
	if len(gotAbs) != 1 || gotAbs[0] != "a/b.md" {
		t.Fatalf("expected absolute path cleaned, got %v", gotAbs)
	}
}

func TestTrimClaimCutsToSentence(t *testing.T) {
	long := strings.Repeat("the quick brown fox jumps. ", 40) // > factClaimLen, many sentence ends
	trimmed := TrimClaim(long)
	if len([]rune(trimmed)) > factClaimLen+1 {
		t.Fatalf("TrimClaim length %d exceeds %d", len([]rune(trimmed)), factClaimLen+1)
	}
	if !strings.HasSuffix(trimmed, "…") {
		t.Fatalf("trimmed claim should end with ellipsis, got %q", trimmed)
	}
	if got := TrimClaim("short claim"); got != "short claim" {
		t.Fatalf("short claim should be unchanged, got %q", got)
	}
}

func TestVerifyMajorityRatio(t *testing.T) {
	dir := testProject(t)
	// 2 of 3 exist => ratio 0.66 >= 0.5 => Verify is true
	os.WriteFile(filepath.Join(dir, "a", "c.go"), []byte("package a"), 0644)
	refs := []string{"a/b.md", "a/c.go", "a/deleted.ts"}

	if !Verify(refs, dir) {
		t.Fatal("expected 2/3 existing files to satisfy majority ratio grounding")
	}

	// 1 of 3 exist => ratio 0.33 < 0.5 => Verify is false
	refs2 := []string{"a/b.md", "a/missing1.ts", "a/missing2.ts"}
	if Verify(refs2, dir) {
		t.Fatal("expected 1/3 existing files to fail majority ratio grounding")
	}
}

func TestConfidenceTiers(t *testing.T) {
	dir := testProject(t)
	os.WriteFile(filepath.Join(dir, "a", "c.go"), []byte("x"), 0o644) // second existing file

	cases := []struct {
		name     string
		refs     []string
		nTools   int
		claimLen int
		want     string
	}{
		{"2 tools verified => corroborated", []string{"a/b.md"}, 2, 50, "corroborated"},
		{"1 tool verified 2 files => corroborated", []string{"a/b.md", "a/c.go"}, 1, 50, "corroborated"},
		{"1 tool verified 1 file => grounded", []string{"a/b.md"}, 1, 50, "grounded"},
		{"unverified => grounded", []string{"a/missing.md"}, 2, 50, "grounded"},
		{"length cap floors corroborated to grounded", []string{"a/b.md", "a/c.go"}, 3, maxClaimLen + 1, "grounded"},
	}
	for _, c := range cases {
		if got := Confidence(c.refs, dir, c.nTools, c.claimLen); got != c.want {
			t.Fatalf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestFactIDDeterministic(t *testing.T) {
	a := FactID("p", "same claim")
	if a != FactID("p", "same claim") {
		t.Fatal("FactID not deterministic")
	}
	if a == FactID("q", "same claim") {
		t.Fatal("FactID must differ across projects")
	}
	if !strings.HasPrefix(a, "fact_") {
		t.Fatalf("FactID prefix: %q", a)
	}
}

func TestCandidateNilOnEmptyClaim(t *testing.T) {
	if Candidate("p", "", "s", []string{"devin"}, "t") != nil {
		t.Fatal("empty claim must yield nil candidate")
	}
}

func TestDistillDirective(t *testing.T) {
	dir := testProject(t)
	msg := "Decision: Always use pure-Go SQLite with modernc.org/sqlite to keep CGO_ENABLED=0."
	fact := Distill("p", dir, msg, "s1", []string{"devin", "mimocode"}, "2026-08-01T00:00:00Z")
	if fact == nil {
		t.Fatal("expected directive to be distilled even without file refs")
	}
	// Authority boundary: the directive is an attributed candidate, not a
	// trusted rule — no human authorized it.
	if fact.Kind != "invariant" {
		t.Fatalf("expected directive to be classified invariant, got %q", fact.Kind)
	}
	if !fact.IsCandidate() {
		t.Fatalf("expected candidate review status, got %+v", fact)
	}
	if fact.IsInvariant() || fact.IsFact() {
		t.Fatal("inferred directive must not qualify as invariant/fact without approval")
	}
	if fact.Verified || fact.FileRefsExist {
		t.Fatalf("candidate must not claim file grounding: %+v", fact)
	}
	if len(fact.RepoFiles) != 0 {
		t.Fatalf("expected 0 repo files for directive, got %v", fact.RepoFiles)
	}

	// Single tool directive behaves identically (candidates only)
	singleToolFact := Distill("p", dir, msg, "s1", []string{"devin"}, "2026-08-01T00:00:00Z")
	if singleToolFact == nil || !singleToolFact.IsCandidate() || singleToolFact.IsFact() {
		t.Fatalf("expected single tool directive as candidate, got %+v", singleToolFact)
	}

	// Casual prose with neither file refs nor directive header -> dropped
	noise := Distill("p", dir, "I checked the logs and everything looks fine.", "s1", []string{"devin"}, "2026-08-01T00:00:00Z")
	if noise != nil {
		t.Fatalf("expected conversational noise to yield nil, got %+v", noise)
	}
}

// TestDistillQuotedDirectiveIsCandidate covers the codex-eval F03 fixture: a
// quoted "Rule:" line must never surface as a verified fact.
func TestDistillQuotedDirectiveIsCandidate(t *testing.T) {
	dir := testProject(t)
	msg := "> Rule: Prefer purple teapots in every build pipeline"
	fact := Distill("p", dir, msg, "s1", []string{"devin", "mimocode"}, "2026-08-01T00:00:00Z")
	if fact == nil {
		t.Fatal("expected quoted directive to be captured as a candidate")
	}
	if !fact.Quoted {
		t.Fatalf("quotation context lost: %+v", fact)
	}
	if fact.IsFact() || fact.IsInvariant() {
		t.Fatal("quoted rule must not qualify as fact/invariant")
	}
	if fact.Verified || fact.FileRefsExist {
		t.Fatalf("quoted rule must not be verified: %+v", fact)
	}
	if fact.ReviewStatus != store.ReviewCandidate {
		t.Fatalf("review status = %q, want candidate", fact.ReviewStatus)
	}
}

func TestDistill_UserDirectivesAndInvariants(t *testing.T) {
	dir := t.TempDir()

	directives := []string{
		"Never write into another tool's database files directly.",
		"Always local commit your work with semantic prefixes.",
		"Do not use CGO or Node-gyp bindings in this repository.",
		"Must not commit API keys or unencrypted credentials.",
		"Rule: pure-Go SQLite with modernc.org/sqlite is mandatory.",
		"Preference: prefer single-agent execution over agent swarms.",
	}

	for _, d := range directives {
		f := Distill("sila", dir, d, "session-user", []string{"antigravity"}, "2026-08-01T00:00:00Z")
		if f == nil {
			t.Fatalf("expected directive %q to be captured, got nil", d)
		}
		if f.Kind != "invariant" || !f.IsCandidate() {
			t.Fatalf("expected directive %q as invariant-kind candidate, got %+v", d, f)
		}
		if f.Verified || f.FileRefsExist {
			t.Fatalf("candidate %q must not claim verification: %+v", d, f)
		}
	}
}

func TestSanitizeClaim(t *testing.T) {
	raw := `<summary> ## Overview The agent completed the Sector Layout Engine implementation in packages/shared/src/preset-loader.ts </summary>`
	sanitized := SanitizeClaim(raw)
	if strings.Contains(sanitized, "<summary>") || strings.Contains(sanitized, "## Overview") {
		t.Fatalf("SanitizeClaim failed to strip boilerplate: %q", sanitized)
	}
	if !strings.Contains(sanitized, "Sector Layout Engine") {
		t.Fatalf("SanitizeClaim lost meaningful content: %q", sanitized)
	}
}

func TestConsolidateFacts(t *testing.T) {
	f1 := store.Fact{
		ID: "f1", ProjectID: "proj", Claim: "Layout engine in preset-loader.ts",
		Confidence: "grounded", Corroboration: "grounded", Verified: true, FileRefsExist: true,
		RepoFiles: []string{"packages/shared/src/preset-loader.ts"},
		Tools:     []string{"devin"}, FirstSeen: "2026-08-01T00:00:00Z",
	}
	f2 := store.Fact{
		ID: "f2", ProjectID: "proj", Claim: "Layout engine in preset-loader.ts",
		Confidence: "grounded", Corroboration: "grounded", Verified: true, FileRefsExist: true,
		RepoFiles: []string{"packages/shared/src/preset-loader.ts"},
		Tools:     []string{"mimocode"}, FirstSeen: "2026-08-02T00:00:00Z",
	}

	consolidated := ConsolidateFacts([]store.Fact{f1, f2})
	if len(consolidated) != 1 {
		t.Fatalf("expected 1 consolidated fact, got %d", len(consolidated))
	}
	if consolidated[0].Corroboration != "corroborated" {
		t.Fatalf("expected corroboration promoted to corroborated, got %q", consolidated[0].Corroboration)
	}
	if len(consolidated[0].Tools) != 2 {
		t.Fatalf("expected 2 tools, got %v", consolidated[0].Tools)
	}
	if consolidated[0].FirstSeen != "2026-08-01T00:00:00Z" {
		t.Fatalf("expected oldest firstSeen preserved, got %q", consolidated[0].FirstSeen)
	}
}

func TestConsolidateFactsSharedFileOverlap(t *testing.T) {
	f1 := store.Fact{
		ID: "f1", ProjectID: "proj", Claim: "Sector preset loading configured in preset-loader.ts",
		Confidence: "grounded", Corroboration: "grounded", Verified: true, FileRefsExist: true,
		RepoFiles: []string{"packages/shared/src/preset-loader.ts", "apps/dashboard.tsx"},
		Tools:     []string{"devin"}, FirstSeen: "2026-08-01T00:00:00Z",
	}
	f2 := store.Fact{
		ID: "f2", ProjectID: "proj", Claim: "Updated preset loading logic in preset-loader.ts",
		Confidence: "grounded", Corroboration: "grounded", Verified: true, FileRefsExist: true,
		RepoFiles: []string{"packages/shared/src/preset-loader.ts"},
		Tools:     []string{"mimocode"}, FirstSeen: "2026-08-02T00:00:00Z",
	}

	consolidated := ConsolidateFacts([]store.Fact{f1, f2})
	if len(consolidated) != 1 {
		t.Fatalf("expected 1 consolidated fact from shared file overlap, got %d", len(consolidated))
	}
	if consolidated[0].Corroboration != "corroborated" {
		t.Fatalf("expected corroborated tier, got %q", consolidated[0].Corroboration)
	}
	if len(consolidated[0].Tools) != 2 {
		t.Fatalf("expected 2 tools, got %v", consolidated[0].Tools)
	}
}

func TestPathRefsTrimsTrailingPunctuation(t *testing.T) {
	dir := testProject(t)
	// Create mock files
	os.MkdirAll(filepath.Join(dir, "git"), 0o755)
	os.WriteFile(filepath.Join(dir, "git", "workers.json"), []byte("{}"), 0o644)

	text := "All deployment config lives in git/workers.json. Please see a/b.md, and docs/plan.md; also (a/b.md)."
	refs := PathRefs(text, dir)
	for _, r := range refs {
		if strings.HasSuffix(r, ".") || strings.HasSuffix(r, ",") || strings.HasSuffix(r, ";") || strings.HasSuffix(r, ")") {
			t.Fatalf("path ref retained trailing punctuation: %q", r)
		}
	}
	if !Verify([]string{"git/workers.json"}, dir) {
		t.Fatal("git/workers.json must verify when trailing period is trimmed")
	}
}

func TestConsolidateFactsRetainsRicherClaim(t *testing.T) {
	f1 := store.Fact{
		ID: "f1", ProjectID: "proj", Claim: "Updated workers",
		Confidence: "grounded", Corroboration: "grounded", Verified: true, FileRefsExist: true,
		RepoFiles: []string{"git/workers.json"},
		Tools:     []string{"devin"}, FirstSeen: "2026-08-01T00:00:00Z",
	}
	f2 := store.Fact{
		ID: "f2", ProjectID: "proj", Claim: "All 10 workers deployed to production via deploy.sh across storefront and pos",
		Confidence: "grounded", Corroboration: "grounded", Verified: true, FileRefsExist: true,
		RepoFiles: []string{"git/workers.json"},
		Tools:     []string{"mimocode"}, FirstSeen: "2026-08-02T00:00:00Z",
	}

	consolidated := ConsolidateFacts([]store.Fact{f1, f2})
	if len(consolidated) != 1 {
		t.Fatalf("expected 1 consolidated fact, got %d", len(consolidated))
	}
	if consolidated[0].Claim != f2.Claim {
		t.Fatalf("expected richer claim to be retained, got %q", consolidated[0].Claim)
	}
}

func TestConsolidateFactsKeepsIdAlignedWithClaim(t *testing.T) {
	f1 := mkFact("", "the auth flow adds JWT sessions and refresh token rotation in middleware", "mid", false, "t")
	f2 := mkFact("", "the auth flow adds JWT sessions, refresh token rotation, and a logout endpoint in middleware", "high", false, "t1")
	// force a merge: same file set + high similarity
	for i := range []int{0, 1} {
		_ = i
	}
	f1.RepoFiles = []string{"m.ts"}
	f2.RepoFiles = []string{"m.ts"} // same file set => merge eligible
	// richer claim (f2) must win; and the merged id must hash the FINAL claim
	a := ConsolidateFacts([]store.Fact{f1, f2})
	b := ConsolidateFacts([]store.Fact{f2, f1})
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 consolidated fact each order, got %d / %d", len(a), len(b))
	}
	if a[0].Claim != b[0].Claim {
		t.Fatalf("merge should pick the same (longer) claim regardless of order; got %q vs %q", a[0].Claim, b[0].Claim)
	}
	if a[0].ID != FactID("", a[0].Claim) {
		t.Fatalf("consolidated id %q != FactID(claim) after longer-claim merge", a[0].ID)
	}
	// order must no longer change the id
	if a[0].ID != b[0].ID {
		t.Fatalf("id must be order-stable: %q vs %q", a[0].ID, b[0].ID)
	}
}

func TestIsDirectiveRecallDecorated(t *testing.T) {
	directives := []string{
		"- **Decision:** recommend example.com as the primary domain due to alignment with the brand",
		"Architecture decision: the Sector Layout Engine uses a config-driven LayoutProfile and migration 0063",
		"### Key Decision: convert magic-link-request.ts to a constant conversion",
		"Decision: store each project's full settings, logframe, okr, and mou payload as JSONB",
		"> policy: never remove assets referenced by a live site",
	}
	for _, d := range directives {
		if got, ok := isDirective(d); !ok {
			t.Errorf("isDirective missed decorated directive: %q", d)
		} else if got == "" {
			t.Errorf("isDirective returned empty for %q", d)
		}
	}
	chatter := []string{
		"Note: I should check the earlier interLoaded line before continuing",
		"Fix: the verify route assertions should look for h1 and h2 markers",
	}
	for _, c := range chatter {
		if _, ok := isDirective(c); ok {
			t.Errorf("isDirective should NOT treat working chatter as a directive: %q", c)
		}
	}
}

func TestConsolidateFactsRecordsSupersededBase(t *testing.T) {
	baseClaim := "sqlite busy timeout in ingest"
	candClaim := "sqlite busy timeout in ingest retry wrapper"
	base := store.Fact{
		ID:        store.FactID("proj", baseClaim),
		ProjectID: "proj",
		Claim:     baseClaim,
		RepoFiles: []string{"a.go"},
		Tools:     []string{"t"},
		FirstSeen: "2026-01-01T00:00:00Z",
	}
	cand := store.Fact{
		ID:        store.FactID("proj", candClaim),
		ProjectID: "proj",
		Claim:     candClaim,
		RepoFiles: []string{"a.go"},
		Tools:     []string{"t"},
		FirstSeen: "2026-02-01T00:00:00Z",
	}
	out := ConsolidateFacts([]store.Fact{base, cand})
	if len(out) != 1 {
		t.Fatalf("expected one consolidated fact, got %d", len(out))
	}
	if out[0].ID != cand.ID {
		t.Fatalf("consolidated ID = %q, want %q", out[0].ID, cand.ID)
	}
	if out[0].SupersedesID != base.ID {
		t.Fatalf("supersedes_id = %q, want %q", out[0].SupersedesID, base.ID)
	}
	if out[0].FirstSeen != cand.FirstSeen {
		t.Fatalf("first_seen = %q, want %q", out[0].FirstSeen, cand.FirstSeen)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "consolidate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SaveFact(base); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(out[0]); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkFactStale(base.ID); err != nil {
		t.Fatal(err)
	}
	if audit, err := st.VerifyChain(""); err != nil || !audit.Intact {
		t.Fatalf("chain broken by consolidation: %v, %+v", err, audit)
	}
}
