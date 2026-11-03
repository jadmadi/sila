package audit

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/persona"
)

func TestRecommendSquad(t *testing.T) {
	all := persona.FoundationPersonas

	// Test 1: Arabic / Waqf LMS URL
	arabicSquad := RecommendSquad(all, "https://manara.app/ar/courses", "Arabic educational waqf platform")
	foundWaqf := false
	for _, p := range arabicSquad {
		if p.ID == "arabic-waqf-craftsperson" {
			foundWaqf = true
		}
	}
	if !foundWaqf {
		t.Errorf("expected arabic-waqf-craftsperson in squad for Arabic platform, got %v", arabicSquad)
	}

	// Test 2: Pricing / Checkout URL
	pricingSquad := RecommendSquad(all, "https://saas.io/pricing", "subscription checkout and billing plans")
	foundSales := false
	for _, p := range pricingSquad {
		if p.ID == "enterprise-sales-investor" || p.ID == "conversion-copywriter" {
			foundSales = true
		}
	}
	if !foundSales {
		t.Errorf("expected enterprise sales or copywriter for pricing URL, got %v", pricingSquad)
	}

	// Test 3: Admin Dashboard
	adminSquad := RecommendSquad(all, "https://app.io/admin/dashboard", "internal operator power console")
	foundKeyboard := false
	for _, p := range adminSquad {
		if p.ID == "keyboard-power-maximalist" {
			foundKeyboard = true
		}
	}
	if !foundKeyboard {
		t.Errorf("expected keyboard-power-maximalist for admin console, got %v", adminSquad)
	}

	// Test 4: AI Agent Readiness scanner (isitagentready.com style)
	agentSquad := RecommendSquad(all, "https://isitagentready.com", "Scan site for AI agent readiness, MCP server cards, and markdown negotiation")
	foundAgentArch := false
	foundTokenEcon := false
	for _, p := range agentSquad {
		if p.ID == "agent-readiness-architect" {
			foundAgentArch = true
		}
		if p.ID == "content-negotiation-token-economist" {
			foundTokenEcon = true
		}
	}
	if !foundAgentArch || !foundTokenEcon {
		t.Errorf("expected agent-readiness-architect and content-negotiation-token-economist for agent URL, got %v", agentSquad)
	}
}

func TestDetectGaps(t *testing.T) {
	all := persona.FoundationPersonas
	existing := all[:10]

	// Gap 1: Mobile App touch surfaces
	gaps := DetectGaps(existing, "New Flutter mobile app with touch gestures and bottom sheets")
	if len(gaps) == 0 {
		t.Fatalf("expected gap detection for mobile app, got 0")
	}
	if gaps[0].ID != "mobile-native-ergonomist" {
		t.Errorf("expected mobile-native-ergonomist, got %s", gaps[0].ID)
	}
	p := gaps[0].ToPersona()
	if p.ID != "mobile-native-ergonomist" || len(p.SkillsInvoked) == 0 {
		t.Errorf("expected valid Persona struct from proposal, got %+v", p)
	}

	// Gap 2: Healthcare HIPAA
	healthGaps := DetectGaps(existing, "Clinical EHR portal storing patient health records under HIPAA")
	foundHIPAA := false
	for _, g := range healthGaps {
		if g.ID == "hipaa-healthcare-compliance" {
			foundHIPAA = true
		}
	}
	if !foundHIPAA {
		t.Errorf("expected hipaa-healthcare-compliance proposal, got %v", healthGaps)
	}
}

func TestOrchestratorAndSynthesizer(t *testing.T) {
	target := AuditTarget{
		URL:          "https://example-saas.com",
		AppName:      "Example SaaS",
		ScopePages:   []string{"/pricing", "/dashboard"},
		ExtraContext: "B2B SaaS with high bounce rate on mobile",
	}

	squad := RecommendSquad(persona.FoundationPersonas, target.URL, target.ExtraContext)
	if len(squad) == 0 {
		t.Fatalf("expected non-empty squad")
	}

	// Verify individual prompt generation
	prompt := GeneratePersonaPrompt(target, squad[0])
	if !strings.Contains(prompt, squad[0].Name) || !strings.Contains(prompt, target.URL) {
		t.Errorf("persona prompt missing name or target URL: %s", prompt)
	}

	// Verify master orchestration prompt
	masterPrompt := AssembleMasterGroupthinkPrompt(target, squad, nil)
	if !strings.Contains(masterPrompt, "Autonomous Multi-Persona Groupthink Audit Protocol") {
		t.Errorf("master prompt missing title header: %s", masterPrompt)
	}

	// Execute mock audit session
	session, err := ExecuteMock(context.Background(), target, squad)
	if err != nil {
		t.Fatalf("ExecuteMock failed: %v", err)
	}
	if len(session.Cards) != len(squad) {
		t.Errorf("expected %d cards, got %d", len(squad), len(session.Cards))
	}

	// Synthesize consensus into Sila goals
	proposals := SynthesizeConsensus(session)
	if len(proposals) == 0 {
		t.Fatalf("expected at least one synthesized proposal, got 0")
	}

	top := proposals[0]
	if top.Slug == "" || top.Title == "" {
		t.Errorf("invalid proposal slug/title: %+v", top)
	}
	if !strings.Contains(top.GoalMD, "# Goal:") || !strings.Contains(top.GoalMD, "## Done Condition") {
		t.Errorf("invalid goal.md markdown structure:\n%s", top.GoalMD)
	}
	if !strings.Contains(top.FactsMD, "# Facts:") {
		t.Errorf("invalid facts.md markdown structure:\n%s", top.FactsMD)
	}
	if !strings.Contains(top.PlanMD, "# Plan:") || !strings.Contains(top.PlanMD, "## Execution Steps") {
		t.Errorf("invalid plan.md markdown structure:\n%s", top.PlanMD)
	}

	// Test lesson feedback generation
	symptom, cause, fix := FormatLessonFeedback(top, true, "")
	if !strings.Contains(symptom, "Audit proposal accepted") || fix == "" {
		t.Errorf("unexpected accepted feedback format: %s, %s, %s", symptom, cause, fix)
	}

	symptomRej, causeRej, fixRej := FormatLessonFeedback(top, false, "contrast was intended aesthetic")
	if !strings.Contains(symptomRej, "Audit proposal rejected") || !strings.Contains(causeRej, "contrast was intended") {
		t.Errorf("unexpected rejected feedback format: %s, %s, %s", symptomRej, causeRej, fixRej)
	}
}

func TestRefactoringUIPersonaAudit_HTMLScanning(t *testing.T) {
	target := AuditTarget{
		URL:     "https://example.com/checkout",
		AppName: "Test App",
	}

	htmlSnippet := `
<div class="card mt-[13px] p-[17px] border-black">
  <div style="color: #000000; background: #ffffff;">
    <h1 class="text-[15px]" style="box-shadow: 0 8px 16px rgba(0,0,0,0.5);">Checkout</h1>
    <label class="mb-4">Email</label>
    <input type="email" />
  </div>
  <button class="bg-blue-600 btn-primary">Pay Now</button>
  <button class="bg-blue-600 btn-primary">Instant Checkout</button>
  <div class="empty-state-raw">No items</div>
</div>
`

	// 1. UI Craft
	uiPersona := persona.Persona{ID: "ui-craftsperson", Name: "UI Craftsperson", Domain: "ui"}
	findingsUI, suggestionsUI := AuditHTMLContent(target, uiPersona, htmlSnippet)
	if len(findingsUI) == 0 {
		t.Fatalf("expected UI craft findings, got 0")
	}
	foundSpacing := false
	foundButton := false
	for _, f := range findingsUI {
		if f.Category == "Spatial Rhythm" {
			foundSpacing = true
		}
		if f.Category == "Action Affordances" {
			foundButton = true
		}
	}
	if !foundSpacing {
		t.Errorf("expected spacing scale finding for mt-[13px]")
	}
	if !foundButton {
		t.Errorf("expected button hierarchy finding for multiple primary buttons")
	}
	if len(suggestionsUI) == 0 {
		t.Errorf("expected actionable suggestions for UI craftsperson")
	}

	// 2. DOM Ergonomist
	domPersona := persona.Persona{ID: "ux-information-architect", Name: "DOM Ergonomist", Domain: "ux"}
	findingsDOM, _ := AuditHTMLContent(target, domPersona, htmlSnippet)
	foundClutter := false
	for _, f := range findingsDOM {
		if f.Category == "DOM Ergonomics" {
			foundClutter = true
		}
	}
	if !foundClutter {
		t.Errorf("expected clutter finding for border-black")
	}

	// 3. A11y Sentry
	a11yPersona := persona.Persona{ID: "accessibility-sentinel", Name: "A11y Sentinel", Domain: "access"}
	findingsA11y, _ := AuditHTMLContent(target, a11yPersona, htmlSnippet)
	foundContrast := false
	for _, f := range findingsA11y {
		if f.Category == "Accessibility" {
			foundContrast = true
		}
	}
	if !foundContrast {
		t.Errorf("expected contrast finding for pure black on white")
	}

	// 4. Onboard Guide
	onboardPersona := persona.Persona{ID: "first-time-user-advocate", Name: "Onboard Guide", Domain: "user"}
	findingsOnboard, _ := AuditHTMLContent(target, onboardPersona, htmlSnippet)
	foundEmpty := false
	for _, f := range findingsOnboard {
		if f.Category == "Onboarding" {
			foundEmpty = true
		}
	}
	if !foundEmpty {
		t.Errorf("expected empty state finding for unhandled empty view")
	}
}

func TestRefactoringUIPersonaAudit_AliasResolution(t *testing.T) {
	all := persona.FoundationPersonas
	squad, err := persona.ResolvePersonas(all, "ui-craft, dom-ergonomist, a11y-sentry, onboard-guide")
	if err != nil {
		t.Fatalf("ResolvePersonas with aliases failed: %v", err)
	}
	if len(squad) != 4 {
		t.Fatalf("expected 4 resolved personas, got %d", len(squad))
	}

	expected := map[string]string{
		"ui-craftsperson":          "ui-craft",
		"ux-information-architect": "dom-ergonomist",
		"accessibility-sentinel":   "a11y-sentry",
		"first-time-user-advocate": "onboard-guide",
	}

	for _, p := range squad {
		if _, ok := expected[p.ID]; !ok {
			t.Errorf("unexpected resolved persona ID %q", p.ID)
		}
	}
}

func TestRefactoringUIPersonaAudit_Phase4ReportsAndGoals(t *testing.T) {
	tempDir := t.TempDir()
	if out, err := exec.Command("git", "-C", tempDir, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("initialize isolated repository: %v: %s", err, out)
	}

	target := AuditTarget{
		URL:     "https://example.com/app",
		AppName: "SaaS App",
	}

	all := persona.FoundationPersonas
	squad, err := persona.ResolvePersonas(all, "ui-craft, dom-ergonomist, a11y-sentry, onboard-guide")
	if err != nil {
		t.Fatal(err)
	}

	session, err := ExecuteMock(context.Background(), target, squad)
	if err != nil {
		t.Fatal(err)
	}

	if len(session.Cards) != 4 {
		t.Fatalf("expected 4 cards, got %d", len(session.Cards))
	}

	// Verify all cards have Phase 4 Refactoring UI findings
	for _, card := range session.Cards {
		if len(card.Findings) == 0 {
			t.Errorf("card %s has 0 findings", card.PersonaID)
		}
		if len(card.Suggestions) == 0 {
			t.Errorf("card %s has 0 suggestions", card.PersonaID)
		}
	}

	// Persist reports
	now := time.Now().UTC()
	var savedReports []*Report
	for _, card := range session.Cards {
		rep := &Report{
			ID:             "rep_" + card.PersonaID + "_test",
			AssociatedGoal: "unassigned",
			CreatedAt:      now,
			UpdatedAt:      now,
			PersonaID:      card.PersonaID,
			PersonaName:    card.PersonaName,
			Domain:         card.Domain,
			TargetURL:      target.URL,
			Status:         card.Status,
			Phase:          4,
			Summary:        card.ExecutiveSummary,
			Findings:       card.Findings,
			Suggestions:    card.Suggestions,
			SkillsUsed:     card.SkillsUsed,
		}
		if err := SaveReport(tempDir, rep); err != nil {
			t.Fatalf("SaveReport failed: %v", err)
		}
		savedReports = append(savedReports, rep)
	}

	// Verify reports list
	reports, err := ListReports(tempDir)
	if err != nil {
		t.Fatalf("ListReports failed: %v", err)
	}
	if len(reports) != 4 {
		t.Fatalf("expected 4 persisted reports, got %d", len(reports))
	}

	// Synthesize proposals
	proposals := SynthesizeConsensus(session)
	if len(proposals) == 0 {
		t.Fatalf("expected at least one proposal, got 0")
	}

	top := proposals[0]
	if !strings.Contains(top.GoalMD, "Refactoring UI atomic design heuristics") {
		t.Errorf("expected goal.md to reference Refactoring UI heuristics:\n%s", top.GoalMD)
	}

	// Link report to proposal
	if err := LinkReportToGoal(tempDir, savedReports[0].ID, top.Slug); err != nil {
		t.Fatalf("LinkReportToGoal failed: %v", err)
	}

	updatedRep, err := GetReport(tempDir, savedReports[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedRep.AssociatedGoal != top.Slug {
		t.Errorf("got associated goal %q, want %q", updatedRep.AssociatedGoal, top.Slug)
	}
}
