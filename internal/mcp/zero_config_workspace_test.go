package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestZeroConfigWorkspaceIdentity(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Create workspace directories: parent/cli, parent/worker, other/unrelated
	parentDir := filepath.Join(tmp, "workspace")
	cliDir := filepath.Join(parentDir, "cli")
	workerDir := filepath.Join(parentDir, "worker")
	otherDir := filepath.Join(tmp, "other-proj")

	for _, d := range []string{cliDir, workerDir, otherDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	pCli, err := st.EnsureProject(cliDir)
	if err != nil {
		t.Fatal(err)
	}
	pWorker, err := st.EnsureProject(workerDir)
	if err != nil {
		t.Fatal(err)
	}
	pOther, err := st.EnsureProject(otherDir)
	if err != nil {
		t.Fatal(err)
	}

	// Change working directory to cliDir to simulate agent working inside cli/
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cliDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWd)

	callTool := func(toolName string, args map[string]any) (any, string, error) {
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
				StructuredData any `json:"structuredData"`
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
			return nil, "", fmt.Errorf("decode response error: %w; raw: %s", err, out.String())
		}
		if resp.Error != nil {
			return nil, "", fmt.Errorf("tool error (%d): %s - %s", resp.Error.Code, resp.Error.Message, resp.Error.Data)
		}
		text := ""
		if len(resp.Result.Content) > 0 {
			text = resp.Result.Content[0].Text
		}
		return resp.Result.StructuredData, text, nil
	}

	// 1. save_memo WITHOUT project parameter (zero-config cwd detection)
	memoRes, _, err := callTool("save_memo", map[string]any{
		"content": "Architectural directive: pure Go, zero CGO",
	})
	if err != nil {
		t.Fatalf("save_memo without project failed: %v", err)
	}
	memoMap, ok := memoRes.(map[string]any)
	if !ok {
		t.Fatalf("expected map result for memo, got %T: %v", memoRes, memoRes)
	}
	if proj, _ := memoMap["project_id"].(string); proj != pCli.ID {
		t.Fatalf("expected memo project_id %q, got %q", pCli.ID, proj)
	}

	// 2. get_project_facts WITHOUT project parameter (should return the fact we just saved)
	factsRes, _, err := callTool("get_project_facts", map[string]any{})
	if err != nil {
		t.Fatalf("get_project_facts without project failed: %v", err)
	}
	factsList, ok := factsRes.([]any)
	if !ok || len(factsList) == 0 {
		t.Fatalf("expected non-empty facts list, got %v", factsRes)
	}
	f0 := factsList[0].(map[string]any)
	if proj, _ := f0["project_id"].(string); proj != pCli.ID {
		t.Fatalf("expected facts project_id %q, got %q", pCli.ID, proj)
	}

	// 3. save_handoff WITHOUT project parameter
	handoffRes, _, err := callTool("save_handoff", map[string]any{
		"summary": "Completed zero config implementation",
	})
	if err != nil {
		t.Fatalf("save_handoff without project failed: %v", err)
	}
	handoffMap, ok := handoffRes.(map[string]any)
	if !ok {
		t.Fatalf("expected map result for handoff, got %T: %v", handoffRes, handoffRes)
	}
	if proj, _ := handoffMap["project_id"].(string); proj != pCli.ID {
		t.Fatalf("expected handoff project_id %q, got %q", pCli.ID, proj)
	}

	// 4. get_latest_handoff WITHOUT project parameter
	latestRes, _, err := callTool("get_latest_handoff", map[string]any{})
	if err != nil {
		t.Fatalf("get_latest_handoff without project failed: %v", err)
	}
	latestMap, ok := latestRes.(map[string]any)
	if !ok {
		t.Fatalf("expected map result for latest handoff, got %T: %v", latestRes, latestRes)
	}
	if proj, _ := latestMap["project_id"].(string); proj != pCli.ID {
		t.Fatalf("expected get_latest_handoff project_id %q, got %q", pCli.ID, proj)
	}

	// 5. save_lesson WITHOUT project parameter
	lessonRes, _, err := callTool("save_lesson", map[string]any{
		"symptom": "Null project error in MCP",
		"cause":   "Missing auto-discovery fallback",
		"fix":     "Use resolveTargetProject helper",
	})
	if err != nil {
		t.Fatalf("save_lesson without project failed: %v", err)
	}
	lessonMap, ok := lessonRes.(map[string]any)
	if !ok {
		t.Fatalf("expected map result for lesson, got %T: %v", lessonRes, lessonRes)
	}
	if proj, _ := lessonMap["project_id"].(string); proj != pCli.ID {
		t.Fatalf("expected lesson project_id %q, got %q", pCli.ID, proj)
	}

	// 6. save_doc WITHOUT project parameter
	docRes, _, err := callTool("save_doc", map[string]any{
		"title":   "Zero Config Guide",
		"content": "All tools auto-detect project context from cwd.",
	})
	if err != nil {
		t.Fatalf("save_doc without project failed: %v", err)
	}
	docMap, ok := docRes.(map[string]any)
	if !ok {
		t.Fatalf("expected map result for doc, got %T: %v", docRes, docRes)
	}
	if proj, _ := docMap["project_id"].(string); proj != pCli.ID {
		t.Fatalf("expected doc project_id %q, got %q", pCli.ID, proj)
	}

	// 7. Fuzzy project resolution: pass alias "worker" to save_memo
	memoWorkerRes, _, err := callTool("save_memo", map[string]any{
		"project": "worker",
		"content": "Edge worker memo",
	})
	if err != nil {
		t.Fatalf("save_memo with fuzzy alias 'worker' failed: %v", err)
	}
	memoWorkerMap := memoWorkerRes.(map[string]any)
	if proj, _ := memoWorkerMap["project_id"].(string); proj != pWorker.ID {
		t.Fatalf("expected memo project_id %q, got %q", pWorker.ID, proj)
	}

	// 8. list_projects proximity sorting: pCli (active cwd) first, pWorker (sibling) second, pOther last
	listRes, _, err := callTool("list_projects", map[string]any{})
	if err != nil {
		t.Fatalf("list_projects failed: %v", err)
	}
	projects, ok := listRes.([]any)
	if !ok || len(projects) < 3 {
		t.Fatalf("expected at least 3 projects list, got %v", listRes)
	}
	p0 := projects[0].(map[string]any)["ID"].(string)
	p1 := projects[1].(map[string]any)["ID"].(string)
	p2 := projects[2].(map[string]any)["ID"].(string)

	if p0 != pCli.ID {
		t.Errorf("expected first project to be active cwd (%q), got %q", pCli.ID, p0)
	}
	if p1 != pWorker.ID {
		t.Errorf("expected second project to be sibling (%q), got %q", pWorker.ID, p1)
	}
	if p2 != pOther.ID {
		t.Errorf("expected third project to be other (%q), got %q", pOther.ID, p2)
	}
}
