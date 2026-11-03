package persona_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/persona"
	"github.com/jadmadi/sila/internal/store"
)

func setupTestStore(t *testing.T) (*store.Store, *persona.Manager) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	mgr := persona.NewManager(st)
	return st, mgr
}

func TestEnsureSeeded(t *testing.T) {
	_, mgr := setupTestStore(t)

	err := mgr.EnsureSeeded()
	if err != nil {
		t.Fatalf("EnsureSeeded failed: %v", err)
	}

	all, err := mgr.List("", "")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(all) < 31 {
		t.Errorf("expected at least 31 foundation personas, got %d", len(all))
	}

	// Idempotency check: calling EnsureSeeded again shouldn't duplicate entries
	if err := mgr.EnsureSeeded(); err != nil {
		t.Fatalf("subsequent EnsureSeeded failed: %v", err)
	}
	allAgain, _ := mgr.List("", "")
	if len(allAgain) != len(all) {
		t.Errorf("expected %d personas after second seed, got %d", len(all), len(allAgain))
	}
}

func TestListFiltering(t *testing.T) {
	_, mgr := setupTestStore(t)

	// Filter by domain
	uiPersonas, err := mgr.List("ui", "")
	if err != nil {
		t.Fatalf("List domain=ui failed: %v", err)
	}
	if len(uiPersonas) == 0 {
		t.Errorf("expected at least 1 UI persona, got 0")
	}
	for _, p := range uiPersonas {
		if p.Domain != "ui" {
			t.Errorf("expected domain 'ui', got %q", p.Domain)
		}
	}

	// Filter by tag
	wcagPersonas, err := mgr.List("", "wcag")
	if err != nil {
		t.Fatalf("List tag=wcag failed: %v", err)
	}
	if len(wcagPersonas) == 0 {
		t.Errorf("expected at least 1 persona with tag 'wcag', got 0")
	}
}

func TestGet(t *testing.T) {
	_, mgr := setupTestStore(t)

	p, err := mgr.Get("accessibility-sentinel")
	if err != nil {
		t.Fatalf("Get accessibility-sentinel failed: %v", err)
	}

	if p.ID != "accessibility-sentinel" {
		t.Errorf("expected ID 'accessibility-sentinel', got %q", p.ID)
	}
	if len(p.SkillsInvoked) == 0 {
		t.Errorf("expected skillsInvoked to be populated, got empty slice")
	}

	// Nonexistent persona
	_, err = mgr.Get("nonexistent-persona")
	if err == nil {
		t.Errorf("expected error for nonexistent persona, got nil")
	}
}

func TestUpsertAndReputation(t *testing.T) {
	_, mgr := setupTestStore(t)

	custom := persona.Persona{
		ID:               "custom-graphql-auditor",
		Name:             "Senior GraphQL Schema Auditor",
		Domain:           "api",
		Tags:             []string{"graphql", "schema", "n+1"},
		SystemPrompt:     "Audits GraphQL query depth and N+1 resolver patterns.",
		SkillsInvoked:    []string{"api-and-interface-design"},
		EvaluationRubric: "- [ ] Inspect query complexity bounds\n- [ ] Check dataloader caching",
		NegativeLessons:  []string{},
		UsageCount:       0,
		ApprovalRate:     "100%",
	}

	if err := mgr.Upsert(custom); err != nil {
		t.Fatalf("Upsert custom persona failed: %v", err)
	}

	fetched, err := mgr.Get("custom-graphql-auditor")
	if err != nil {
		t.Fatalf("Get custom persona failed: %v", err)
	}
	if fetched.Name != custom.Name {
		t.Errorf("expected name %q, got %q", custom.Name, fetched.Name)
	}

	// Update reputation
	lesson := "Do not flag query depth on internal admin dashboards"
	if err := mgr.RecordReputation("custom-graphql-auditor", 2, lesson); err != nil {
		t.Fatalf("RecordReputation failed: %v", err)
	}

	updated, err := mgr.Get("custom-graphql-auditor")
	if err != nil {
		t.Fatalf("Get updated persona failed: %v", err)
	}
	if updated.UsageCount != 2 {
		t.Errorf("expected UsageCount 2, got %d", updated.UsageCount)
	}
	if len(updated.NegativeLessons) != 1 || updated.NegativeLessons[0] != lesson {
		t.Errorf("expected negative lesson %q, got %+v", lesson, updated.NegativeLessons)
	}

	// Test deduplication of negative lessons
	if err := mgr.RecordReputation("custom-graphql-auditor", 1, lesson); err != nil {
		t.Fatalf("second RecordReputation failed: %v", err)
	}
	updatedAgain, _ := mgr.Get("custom-graphql-auditor")
	if len(updatedAgain.NegativeLessons) != 1 {
		t.Errorf("expected 1 negative lesson after duplicate add, got %d", len(updatedAgain.NegativeLessons))
	}
}

func TestCanonicalGroupsAndOrder(t *testing.T) {
	if len(persona.CanonicalGroups) != 8 {
		t.Fatalf("expected 8 canonical groups, got %d", len(persona.CanonicalGroups))
	}

	totalInGroups := 0
	seen := make(map[string]bool)
	for _, g := range persona.CanonicalGroups {
		if g.ID < 1 || g.ID > 8 {
			t.Errorf("invalid group ID %d", g.ID)
		}
		if g.Slug == "" || g.Name == "" || g.Description == "" {
			t.Errorf("incomplete group metadata for group %d (%s)", g.ID, g.Slug)
		}
		for _, id := range g.PersonaIDs {
			if seen[id] {
				t.Errorf("duplicate persona ID %q in canonical groups", id)
			}
			seen[id] = true
			totalInGroups++
		}
	}

	if totalInGroups != 32 {
		t.Errorf("expected 32 total personas in canonical groups, got %d", totalInGroups)
	}

	if len(persona.CanonicalOrder) != 32 {
		t.Errorf("expected 32 items in CanonicalOrder, got %d", len(persona.CanonicalOrder))
	}
}

func TestEnrichAndGroupPersonas(t *testing.T) {
	_, mgr := setupTestStore(t)

	all, err := mgr.List("", "")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	// Verify all 32 personas have sequential NumID and valid GroupID
	for _, p := range all {
		if p.NumID < 1 || p.NumID > 32 {
			t.Errorf("persona %q has invalid NumID %d", p.ID, p.NumID)
		}
		if p.GroupID < 1 || p.GroupID > 8 {
			t.Errorf("persona %q has invalid GroupID %d", p.ID, p.GroupID)
		}
		if p.GroupSlug == "" || p.GroupName == "" {
			t.Errorf("persona %q missing GroupSlug or GroupName", p.ID)
		}
	}

	grouped := persona.GroupPersonas(all)
	if len(grouped) != 8 {
		t.Errorf("expected 8 grouped categories, got %d", len(grouped))
	}

	// Check group 1 has 4 personas
	if grouped[0].Group.ID != 1 || len(grouped[0].Personas) != 4 {
		t.Errorf("expected group 1 to have 4 personas, got %d", len(grouped[0].Personas))
	}
}

func TestFindGroup(t *testing.T) {
	g1, ok := persona.FindGroup("1")
	if !ok || g1.ID != 1 || g1.Slug != "agent-ready" {
		t.Errorf("FindGroup(1) failed: got %+v, %v", g1, ok)
	}

	gHash, ok := persona.FindGroup("#2")
	if !ok || gHash.ID != 2 || gHash.Slug != "engineering" {
		t.Errorf("FindGroup(#2) failed: got %+v, %v", gHash, ok)
	}

	gSlug, ok := persona.FindGroup("ui-ux")
	if !ok || gSlug.ID != 3 {
		t.Errorf("FindGroup(ui-ux) failed: got %+v, %v", gSlug, ok)
	}

	gWord, ok := persona.FindGroup("group 4")
	if !ok || gWord.ID != 4 {
		t.Errorf("FindGroup('group 4') failed: got %+v, %v", gWord, ok)
	}

	_, ok = persona.FindGroup("nonexistent-group-99")
	if ok {
		t.Errorf("expected FindGroup for nonexistent group to return false")
	}
}

func TestResolvePersonas(t *testing.T) {
	_, mgr := setupTestStore(t)
	all, _ := mgr.List("", "")

	// 1. Group hire: "group 1"
	squadG1, err := persona.ResolvePersonas(all, "group 1")
	if err != nil {
		t.Fatalf("ResolvePersonas('group 1') failed: %v", err)
	}
	if len(squadG1) != 4 {
		t.Errorf("expected 4 personas for group 1, got %d", len(squadG1))
	}

	// 2. Custom mesh by numeric IDs: "1,4,6,3,8"
	squadMesh, err := persona.ResolvePersonas(all, "1,4,6,3,8")
	if err != nil {
		t.Fatalf("ResolvePersonas('1,4,6,3,8') failed: %v", err)
	}
	if len(squadMesh) != 5 {
		t.Fatalf("expected 5 personas for mesh, got %d", len(squadMesh))
	}
	expectedIDs := []int{1, 4, 6, 3, 8}
	for i, expectedNum := range expectedIDs {
		if squadMesh[i].NumID != expectedNum {
			t.Errorf("mesh index %d: expected NumID %d, got %d (%s)", i, expectedNum, squadMesh[i].NumID, squadMesh[i].ID)
		}
	}

	// 3. Space-separated: "1 4 6"
	squadSpace, err := persona.ResolvePersonas(all, "1 4 6")
	if err != nil || len(squadSpace) != 3 {
		t.Errorf("ResolvePersonas('1 4 6') failed: got %d, err %v", len(squadSpace), err)
	}

	// 4. By slugs
	squadSlugs, err := persona.ResolvePersonas(all, "agent-readiness-architect, senior-fullstack-engineer")
	if err != nil || len(squadSlugs) != 2 {
		t.Errorf("ResolvePersonas by slug failed: got %d, err %v", len(squadSlugs), err)
	}

	// 5. Invalid numeric ID error
	_, err = persona.ResolvePersonas(all, "999")
	if err == nil {
		t.Errorf("expected error for numeric ID 999, got nil")
	}
}

func TestGetByNumericIDAndListGroup(t *testing.T) {
	_, mgr := setupTestStore(t)

	// Get by numeric "1"
	p1, err := mgr.Get("1")
	if err != nil {
		t.Fatalf("Get('1') failed: %v", err)
	}
	if p1.ID != "agent-readiness-architect" || p1.NumID != 1 || p1.GroupID != 1 {
		t.Errorf("Get('1') returned unexpected persona: %+v", p1)
	}

	// Get by "#4"
	p4, err := mgr.Get("#4")
	if err != nil {
		t.Fatalf("Get('#4') failed: %v", err)
	}
	if p4.ID != "machine-identity-auth-counsel" || p4.NumID != 4 {
		t.Errorf("Get('#4') returned unexpected persona: %+v", p4)
	}

	// ListGroup by "1"
	gPersonas, grp, err := mgr.ListGroup("1", "", "")
	if err != nil {
		t.Fatalf("ListGroup('1') failed: %v", err)
	}
	if grp.ID != 1 || len(gPersonas) != 4 {
		t.Errorf("ListGroup('1') unexpected result: grp=%+v, count=%d", grp, len(gPersonas))
	}
}

func TestComplianceAndHIPAAPersonaRubrics(t *testing.T) {
	_, mgr := setupTestStore(t)

	pLegal, err := mgr.Get("legal-compliance-counsel")
	if err != nil {
		t.Fatalf("Get('legal-compliance-counsel') failed: %v", err)
	}

	requiredLegalSubstrings := []string{
		"California Civil Code Section 1542",
		"WCAG 2.1 AA",
		"California CPRA",
		"binding arbitration",
		"Global DPA Standard Contractual Clauses",
	}
	for _, sub := range requiredLegalSubstrings {
		if !strings.Contains(pLegal.EvaluationRubric, sub) {
			t.Errorf("legal-compliance-counsel rubric missing expected clause: %q", sub)
		}
	}

	pHipaa, err := mgr.Get("hipaa-healthcare-compliance")
	if err != nil {
		t.Fatalf("Get('hipaa-healthcare-compliance') failed: %v", err)
	}

	requiredHipaaSubstrings := []string{
		"45 CFR § 164.504(e)",
		"breach notification",
		"subcontractor BAA flow-down",
	}
	for _, sub := range requiredHipaaSubstrings {
		if !strings.Contains(pHipaa.EvaluationRubric, sub) {
			t.Errorf("hipaa-healthcare-compliance rubric missing expected clause: %q", sub)
		}
	}
}
