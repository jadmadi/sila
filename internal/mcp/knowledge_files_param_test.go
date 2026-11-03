package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestMCPKnowledgeFilesParamUnification(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	projDir := filepath.Join(tmp, "test-proj")
	p, err := st.EnsureProject(projDir)
	if err != nil {
		t.Fatal(err)
	}

	callTool := func(toolName string, args map[string]any) map[string]any {
		reqArgs, _ := json.Marshal(args)
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n", toolName, reqArgs)
		in := strings.NewReader(req)
		var out bytes.Buffer
		srv := NewServer(st, in, &out)
		if err := srv.Run(); err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Result struct {
				StructuredData map[string]any `json:"structuredData"`
			} `json:"result"`
			Error any `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v; raw: %s", err, out.String())
		}
		if resp.Error != nil {
			t.Fatalf("tool call returned error: %+v", resp.Error)
		}
		return resp.Result.StructuredData
	}

	// 1. save_memo with repo_files only
	res1 := callTool("save_memo", map[string]any{
		"project":    p.ID,
		"content":    "Memo 1: repo_files only",
		"repo_files": []string{"fileA.go", "fileB.go"},
	})
	refFiles1, ok := res1["repo_files"].([]any)
	if !ok || len(refFiles1) != 2 {
		t.Fatalf("save_memo repo_files expected 2 files, got %+v", res1["repo_files"])
	}

	// 2. save_memo with files alias
	res2 := callTool("save_memo", map[string]any{
		"project": p.ID,
		"content": "Memo 2: files alias",
		"files":   []string{"fileC.go", "fileD.go"},
	})
	refFiles2, ok := res2["repo_files"].([]any)
	if !ok || len(refFiles2) != 2 {
		t.Fatalf("save_memo files alias expected 2 files, got %+v", res2["repo_files"])
	}

	// 3. save_memo with both repo_files and files (should merge and deduplicate)
	res3 := callTool("save_memo", map[string]any{
		"project":    p.ID,
		"content":    "Memo 3: both with duplicates",
		"repo_files": []string{"shared.go", "file1.go"},
		"files":      []string{"shared.go", "file2.go"},
	})
	refFiles3, ok := res3["repo_files"].([]any)
	if !ok || len(refFiles3) != 3 {
		t.Fatalf("save_memo merge expected 3 unique files, got %+v", res3["repo_files"])
	}

	// 4. save_lesson with files only
	res4 := callTool("save_lesson", map[string]any{
		"project": p.ID,
		"symptom": "crash on startup",
		"fix":     "init map properly",
		"files":   []string{"main.go"},
	})
	refFiles4, ok := res4["repo_files"].([]any)
	if !ok || len(refFiles4) != 1 {
		t.Fatalf("save_lesson files expected 1 file, got %+v", res4["repo_files"])
	}

	// 5. save_lesson with repo_files alias
	res5 := callTool("save_lesson", map[string]any{
		"project":    p.ID,
		"symptom":    "deadlock on mutex",
		"fix":        "use RWMutex",
		"repo_files": []string{"sync.go", "lock.go"},
	})
	refFiles5, ok := res5["repo_files"].([]any)
	if !ok || len(refFiles5) != 2 {
		t.Fatalf("save_lesson repo_files alias expected 2 files, got %+v", res5["repo_files"])
	}

	// 6. save_lesson with both files and repo_files (merged)
	res6 := callTool("save_lesson", map[string]any{
		"project":    p.ID,
		"symptom":    "leak in watcher",
		"fix":        "close channel on cancel",
		"files":      []string{"watch.go", "dup.go"},
		"repo_files": []string{"dup.go", "cleanup.go"},
	})
	refFiles6, ok := res6["repo_files"].([]any)
	if !ok || len(refFiles6) != 3 {
		t.Fatalf("save_lesson merge expected 3 unique files, got %+v", res6["repo_files"])
	}

	// 7. save_handoff with files alias
	res7 := callTool("save_handoff", map[string]any{
		"project": p.ID,
		"summary": "Completed feature phase 1",
		"files":   []string{"feature.go", "feature_test.go"},
	})
	refFiles7, ok := res7["repo_files"].([]any)
	if !ok || len(refFiles7) != 2 {
		t.Fatalf("save_handoff files alias expected 2 files, got %+v", res7["repo_files"])
	}
}

func TestMergeUniqueStrings(t *testing.T) {
	res := mergeUniqueStrings(
		[]string{"a", "b", "c"},
		[]string{"b", "c", "d", " "},
		[]string{"e", "a"},
	)
	expected := []string{"a", "b", "c", "d", "e"}
	if !slices.Equal(res, expected) {
		t.Fatalf("expected %v, got %v", expected, res)
	}
}
