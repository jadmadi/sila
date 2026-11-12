package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/audit"
	"github.com/jadmadi/sila/internal/learn"
	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/persona"
	"github.com/jadmadi/sila/internal/store"
)

func TestSpecialistPersonaSelection(t *testing.T) {
	squad, err := persona.ResolvePersonas(persona.FoundationPersonas, "accessibility-sentinel")
	if err != nil {
		t.Fatal(err)
	}
	if len(squad) != 1 || squad[0].ID != "accessibility-sentinel" {
		t.Fatalf("resolved squad = %+v", squad)
	}
	if squad[0].EvaluationRubric == "" {
		t.Fatal("selected persona has no evaluation rubric")
	}
}

func TestSpecialistAuditReportPath(t *testing.T) {
	dir := t.TempDir()
	rep := &audit.Report{
		ID:          "rep_accessibility-sentinel_test",
		PersonaID:   "accessibility-sentinel",
		PersonaName: "Lead Accessibility & Inclusion Engineer",
		Domain:      "access",
		TargetURL:   "README.md",
		Status:      "warning",
		Summary:     "conformance report",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := audit.SaveReport(dir, rep); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(audit.ReportsDir(dir), rep.ID+".json")
	if !strings.HasSuffix(path, filepath.Join(".sila", "reports", rep.ID+".json")) {
		t.Fatalf("unexpected report path: %s", path)
	}
	list, err := audit.ListReports(dir)
	if err != nil || len(list) != 1 || list[0].ID != rep.ID {
		t.Fatalf("ListReports = %+v, %v", list, err)
	}
	if list[0].TargetURL != "README.md" || list[0].Status != "warning" {
		t.Fatalf("report lost fields: %+v", list[0])
	}
}

func TestSpecialistLearnArtifactRoundTrip(t *testing.T) {
	l := learn.OperationalLearning{
		Domain:        "testing",
		Title:         "Repin schema fixtures",
		Symptom:       "marker test fails",
		RootCause:     "digest drift",
		PivotalFix:    "regenerate contract.json",
		GuardrailRule: "repin in the same commit",
		Tags:          []string{"mcp", "fixtures"},
		Confidence:    "high",
	}
	formatted := learn.FormatOperationalLearning(l)
	if !strings.HasPrefix(formatted, "OperationalLearning: Repin schema fixtures") {
		t.Fatalf("formatted = %q", formatted)
	}
	parsed, ok := learn.ParseOperationalLearning(formatted)
	if !ok {
		t.Fatal("formatted learning did not parse")
	}
	if parsed.Domain != "testing" || parsed.GuardrailRule != "repin in the same commit" {
		t.Fatalf("round-trip lost fields: %+v", parsed)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "learn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proj, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fact, err := st.SaveOperationalLearning(proj.ID, formatted, "agent", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fact.ID == "" || fact.Kind != "operational_learning" {
		t.Fatalf("saved learning = %+v", fact)
	}
}

func TestSpecialistMCPDescriptionsAndMissingTool(t *testing.T) {
	want := map[string]string{
		"get_activity_digest":       "standup",
		"consult_personas":          "persona",
		"save_domain_learning":      "guidebook",
		"check_symbol_blast_radius": "edit-check",
	}
	defs := map[string]mcp.Tool{}
	for _, def := range mcp.ToolDefinitions() {
		defs[def.Name] = def
	}
	for name, trigger := range want {
		def, ok := defs[name]
		if !ok {
			t.Errorf("MCP tool %q is missing", name)
			continue
		}
		if !strings.Contains(strings.ToLower(def.Description), trigger) {
			t.Errorf("%s description %q does not mention %q", name, def.Description, trigger)
		}
		if mcp.ContinuityToolNames[name] {
			t.Errorf("%s should stay in the full profile only", name)
		}
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "mcp_missing.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req := `{"jsonrpc":"2.0","id":700,"method":"tools/call","params":{"name":"definitely_missing_tool","arguments":{}}}`
	var out bytes.Buffer
	srv := mcp.NewServer(st, strings.NewReader(req+"\n"), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	var resp mcp.JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("missing-tool error = %+v", resp.Error)
	}
}
