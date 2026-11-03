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

func TestMCPListProjectsQueryAndLimit(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Ensure 5 distinct projects
	for i := 1; i <= 5; i++ {
		pName := fmt.Sprintf("alpha-service-%d", i)
		if i > 3 {
			pName = fmt.Sprintf("beta-service-%d", i)
		}
		if _, err := st.EnsureProject(filepath.Join(tmp, pName)); err != nil {
			t.Fatal(err)
		}
	}

	call := func(args map[string]any) []store.ProjectStat {
		reqArgs, _ := json.Marshal(args)
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_projects","arguments":%s}}`+"\n", reqArgs)
		in := strings.NewReader(req)
		var out bytes.Buffer
		srv := NewServer(st, in, &out)
		if err := srv.Run(); err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Result struct {
				StructuredData []store.ProjectStat `json:"structuredData"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v; raw: %s", err, out.String())
		}
		return resp.Result.StructuredData
	}

	// 1. Limit test: limit=2 must return exactly 2 items
	resLimit := call(map[string]any{"limit": 2})
	if len(resLimit) != 2 {
		t.Fatalf("expected 2 projects with limit=2, got %d", len(resLimit))
	}

	// 2. Query test: query="beta" must return only beta projects
	resQuery := call(map[string]any{"query": "beta"})
	if len(resQuery) != 2 {
		t.Fatalf("expected 2 beta projects, got %d", len(resQuery))
	}
	for _, p := range resQuery {
		if p.ID != "beta-service-4" && p.ID != "beta-service-5" {
			t.Errorf("unexpected project in query result: %s", p.ID)
		}
	}

	// 3. Combined query and limit: query="alpha", limit=1
	resBoth := call(map[string]any{"query": "alpha", "limit": 1})
	if len(resBoth) != 1 {
		t.Fatalf("expected 1 project with query=alpha and limit=1, got %d", len(resBoth))
	}
}

func TestMCPListGoalsStatusAndLimit(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Scaffold 3 goals in tmp
	goalsDir := filepath.Join(tmp, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}

	p1 := goals.CreateGoalParams{RootDir: tmp, Slug: "goal-one", Title: "Goal One"}
	p2 := goals.CreateGoalParams{RootDir: tmp, Slug: "goal-two", Title: "Goal Two"}
	p3 := goals.CreateGoalParams{RootDir: tmp, Slug: "goal-three", Title: "Goal Three"}

	for _, p := range []goals.CreateGoalParams{p1, p2, p3} {
		if _, err := goals.CreateGoal(p); err != nil {
			t.Fatal(err)
		}
	}

	// Claim goal-two
	if _, err := goals.ClaimGoal(tmp, "goal-two", "agent-x", "testing claim", false); err != nil {
		t.Fatal(err)
	}

	// Supersede goal-three
	if _, err := goals.SupersedeGoal(tmp, "goal-three", "goal-one", "agent-x", "testing supersede"); err != nil {
		t.Fatal(err)
	}

	call := func(args map[string]any) []goals.Goal {
		args["project"] = tmp
		reqArgs, _ := json.Marshal(args)
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_goals","arguments":%s}}`+"\n", reqArgs)
		in := strings.NewReader(req)
		var out bytes.Buffer
		srv := NewServer(st, in, &out)
		if err := srv.Run(); err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Result struct {
				StructuredData []goals.Goal `json:"structuredData"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v; raw: %s", err, out.String())
		}
		return resp.Result.StructuredData
	}

	// 1. Status="pending" should return only goal-one
	pending := call(map[string]any{"status": "pending"})
	if len(pending) != 1 || pending[0].Slug != "goal-one" {
		t.Fatalf("expected only goal-one for status=pending, got %+v", pending)
	}

	// 2. Status="claimed" (or "active") should return only goal-two
	claimed := call(map[string]any{"status": "claimed"})
	if len(claimed) != 1 || claimed[0].Slug != "goal-two" {
		t.Fatalf("expected only goal-two for status=claimed, got %+v", claimed)
	}

	// 3. Status="superseded" should return only goal-three
	superseded := call(map[string]any{"status": "superseded"})
	if len(superseded) != 1 || superseded[0].Slug != "goal-three" {
		t.Fatalf("expected only goal-three for status=superseded, got %+v", superseded)
	}

	// 4. Status="all" with limit=2 should return exactly 2 goals
	allLimit := call(map[string]any{"status": "all", "limit": 2})
	if len(allLimit) != 2 {
		t.Fatalf("expected 2 goals with limit=2, got %d", len(allLimit))
	}
}
