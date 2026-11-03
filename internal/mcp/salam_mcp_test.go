package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func TestMCPSalamTool(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmp, "mcp-salam-repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// Create a ready goal in the test repo
	indep := true
	if _, err := goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     repoDir,
		Slug:        "first-contact-goal",
		Title:       "First Contact Goal",
		Description: "A ready goal for salam",
		Independent: &indep,
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate agent working inside repoDir
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWd)

	callTool := func(toolName string, args map[string]any) (map[string]any, string, error) {
		reqArgs, _ := json.Marshal(args)
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n", toolName, reqArgs)
		in := strings.NewReader(req)
		var out bytes.Buffer
		srv := NewServer(st, in, &out)
		if err := srv.Run(); err != nil {
			return nil, "", err
		}
		var resp struct {
			Result struct {
				StructuredData map[string]any `json:"structuredData"`
				Content        []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Data    string `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			return nil, "", fmt.Errorf("decode error: %w; raw: %s", err, out.String())
		}
		if resp.Error != nil {
			return nil, "", fmt.Errorf("tool error: %+v", resp.Error)
		}
		text := ""
		if len(resp.Result.Content) > 0 {
			text = resp.Result.Content[0].Text
		}
		return resp.Result.StructuredData, text, nil
	}

	// 1. Call salam with NO arguments (zero-config cwd detection)
	salamData, salamText, err := callTool("salam", map[string]any{})
	if err != nil {
		t.Fatalf("salam tool call failed: %v", err)
	}

	if proj, _ := salamData["project"].(string); proj != p.ID {
		t.Fatalf("expected project %q, got %q", p.ID, proj)
	}
	loop, ok := salamData["loop"].([]any)
	if !ok || len(loop) != 7 {
		t.Fatalf("expected 7 loop steps, got %v", loop)
	}
	readyGoals, ok := salamData["ready_goals"].([]any)
	if !ok || len(readyGoals) != 1 {
		t.Fatalf("expected 1 ready goal, got %v", readyGoals)
	}
	g0 := readyGoals[0].(map[string]any)
	if slug, _ := g0["slug"].(string); slug != "first-contact-goal" {
		t.Fatalf("expected slug first-contact-goal, got %q", slug)
	}

	if !strings.Contains(salamText, "# sila · salam — "+p.ID) {
		t.Fatalf("salam markdown missing project title: %s", salamText)
	}
	if !strings.Contains(salamText, "first-contact-goal") {
		t.Fatalf("salam markdown missing ready goal: %s", salamText)
	}
	if !strings.Contains(salamText, "The 7-Step Working Contract") {
		t.Fatalf("salam markdown missing 7-step working contract: %s", salamText)
	}

	// 2. Call prime_context to verify enriched 7-step working contract
	_, primeText, err := callTool("prime_context", map[string]any{})
	if err != nil {
		t.Fatalf("prime_context tool call failed: %v", err)
	}
	if !strings.Contains(primeText, "Working Contract (9-Verb Memory Loop)") {
		t.Fatalf("prime_context missing 9-verb working contract: %s", primeText)
	}
	if !strings.Contains(primeText, "Orient & Prime") {
		t.Fatalf("prime_context missing Orient & Prime step: %s", primeText)
	}
	if !strings.Contains(primeText, "Capability Triggers") {
		t.Fatalf("prime_context missing capability triggers: %s", primeText)
	}
}
