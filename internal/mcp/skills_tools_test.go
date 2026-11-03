package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPSkillTools_SearchAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	globalDir := filepath.Join(tempDir, "global_skills")
	projectDir := filepath.Join(tempDir, "workspace")
	pSkillsDir := filepath.Join(projectDir, ".agents", "skills")

	_ = os.MkdirAll(globalDir, 0755)
	_ = os.MkdirAll(pSkillsDir, 0755)

	t.Setenv("SILA_GLOBAL_SKILLS_DIR", globalDir)

	// Create a skill with scripts and references
	skillDir := filepath.Join(pSkillsDir, "custom-tester")
	_ = os.MkdirAll(filepath.Join(skillDir, "scripts"), 0755)
	_ = os.MkdirAll(filepath.Join(skillDir, "references"), 0755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: custom-tester
description: Custom testing tool for E2E suites
tags:
  - testing
  - automation
---
# Custom Tester Instructions
Detailed instructions for custom testing.
`), 0644)
	_ = os.WriteFile(filepath.Join(skillDir, "scripts", "run.sh"), []byte("#!/bin/sh\necho running"), 0755)
	_ = os.WriteFile(filepath.Join(skillDir, "references", "guide.md"), []byte("# Guide\nReference guide"), 0644)

	// 1. Test skill_search
	reqSearch := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"skill_search","arguments":{"query":"testing","project":` + jsonQuote(projectDir) + `}}}`
	var outSearch bytes.Buffer
	srv := NewServer(nil, strings.NewReader(reqSearch+"\n"), &outSearch)
	if err := srv.Run(); err != nil {
		t.Fatalf("srv.Run failed: %v", err)
	}

	var respSearch JSONRPCResponse
	if err := json.Unmarshal(outSearch.Bytes(), &respSearch); err != nil {
		t.Fatalf("unmarshal search response: %v\nOutput: %s", err, outSearch.String())
	}
	if respSearch.Error != nil {
		t.Fatalf("search error: %v", respSearch.Error.Message)
	}

	sData, ok := respSearch.Result.(map[string]any)["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredData: %v", respSearch.Result)
	}
	skillsList, ok := sData["skills"].([]any)
	if !ok || len(skillsList) == 0 {
		t.Fatalf("expected at least 1 skill in search results: %v", sData)
	}

	// 2. Test alias search_skills
	reqAlias := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_skills","arguments":{"query":"custom-tester","project":` + jsonQuote(projectDir) + `}}}`
	var outAlias bytes.Buffer
	srvAlias := NewServer(nil, strings.NewReader(reqAlias+"\n"), &outAlias)
	if err := srvAlias.Run(); err != nil {
		t.Fatalf("srvAlias.Run failed: %v", err)
	}
	var respAlias JSONRPCResponse
	_ = json.Unmarshal(outAlias.Bytes(), &respAlias)
	if respAlias.Error != nil {
		t.Fatalf("alias search_skills failed: %v", respAlias.Error.Message)
	}

	// 3. Test skill_load without resources
	reqLoad := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"skill_load","arguments":{"slug":"custom-tester","project":` + jsonQuote(projectDir) + `}}}`
	var outLoad bytes.Buffer
	srvLoad := NewServer(nil, strings.NewReader(reqLoad+"\n"), &outLoad)
	if err := srvLoad.Run(); err != nil {
		t.Fatalf("srvLoad.Run failed: %v", err)
	}
	var respLoad JSONRPCResponse
	if err := json.Unmarshal(outLoad.Bytes(), &respLoad); err != nil {
		t.Fatalf("unmarshal load response: %v", err)
	}
	if respLoad.Error != nil {
		t.Fatalf("load error: %v", respLoad.Error.Message)
	}
	loadData, ok := respLoad.Result.(map[string]any)["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing load structuredData: %v", respLoad.Result)
	}
	if loadData["slug"] != "custom-tester" {
		t.Errorf("got slug %v, want 'custom-tester'", loadData["slug"])
	}
	if loadData["content_hash"] == "" {
		t.Error("expected non-empty content_hash")
	}

	// 4. Test skill_load with resources
	reqLoadRes := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"skill_load","arguments":{"slug":"custom-tester","include_resources":true,"project":` + jsonQuote(projectDir) + `}}}`
	var outLoadRes bytes.Buffer
	srvLoadRes := NewServer(nil, strings.NewReader(reqLoadRes+"\n"), &outLoadRes)
	if err := srvLoadRes.Run(); err != nil {
		t.Fatalf("srvLoadRes.Run failed: %v", err)
	}
	var respLoadRes JSONRPCResponse
	_ = json.Unmarshal(outLoadRes.Bytes(), &respLoadRes)
	if respLoadRes.Error != nil {
		t.Fatalf("load with resources error: %v", respLoadRes.Error.Message)
	}
	loadResData := respLoadRes.Result.(map[string]any)["structuredData"].(map[string]any)
	resources, ok := loadResData["resources"].([]any)
	if !ok || len(resources) < 2 {
		t.Fatalf("expected at least 2 resources loaded, got %v", loadResData["resources"])
	}

	// 5. Test path traversal rejection in skill_load
	reqTraversal := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"skill_load","arguments":{"slug":"../../etc/passwd","project":` + jsonQuote(projectDir) + `}}}`
	var outTraversal bytes.Buffer
	srvTraversal := NewServer(nil, strings.NewReader(reqTraversal+"\n"), &outTraversal)
	_ = srvTraversal.Run()
	var respTraversal JSONRPCResponse
	_ = json.Unmarshal(outTraversal.Bytes(), &respTraversal)
	if respTraversal.Error == nil {
		t.Fatal("expected error for path traversal slug, got nil")
	}
	if respTraversal.Error.Code != -32602 {
		t.Errorf("expected error code -32602, got %d", respTraversal.Error.Code)
	}

	// 6. Test list_skills tool (compact progressive disclosure)
	reqList := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list_skills","arguments":{"filter":"custom","project":` + jsonQuote(projectDir) + `}}}`
	var outList bytes.Buffer
	srvList := NewServer(nil, strings.NewReader(reqList+"\n"), &outList)
	if err := srvList.Run(); err != nil {
		t.Fatalf("srvList.Run failed: %v", err)
	}
	var respList JSONRPCResponse
	_ = json.Unmarshal(outList.Bytes(), &respList)
	if respList.Error != nil {
		t.Fatalf("list_skills error: %v", respList.Error.Message)
	}
	listData, ok := respList.Result.(map[string]any)["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing list structuredData: %v", respList.Result)
	}
	summaries, ok := listData["skills"].([]any)
	if !ok || len(summaries) == 0 {
		t.Fatalf("expected at least 1 skill in list_skills, got %v", listData)
	}
	firstSummary := summaries[0].(map[string]any)
	if firstSummary["slug"] != "custom-tester" {
		t.Errorf("got slug %v, want 'custom-tester'", firstSummary["slug"])
	}
	if firstSummary["trigger"] == "" {
		t.Error("expected non-empty trigger in skill summary")
	}

	// 7. Test read_skill alias with include_scripts
	reqRead := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read_skill","arguments":{"slug":"custom-tester","include_scripts":true,"project":` + jsonQuote(projectDir) + `}}}`
	var outRead bytes.Buffer
	srvRead := NewServer(nil, strings.NewReader(reqRead+"\n"), &outRead)
	if err := srvRead.Run(); err != nil {
		t.Fatalf("srvRead.Run failed: %v", err)
	}
	var respRead JSONRPCResponse
	_ = json.Unmarshal(outRead.Bytes(), &respRead)
	if respRead.Error != nil {
		t.Fatalf("read_skill error: %v", respRead.Error.Message)
	}
	readData, ok := respRead.Result.(map[string]any)["structuredData"].(map[string]any)
	if !ok || readData["slug"] != "custom-tester" {
		t.Fatalf("invalid read_skill structuredData: %v", respRead.Result)
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
