package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// TestCreateGoalSpecReview verifies create_goal MCP tool handling of optional spec_review.
func TestCreateGoalSpecReview(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	callTool := func(toolName string, args map[string]any) (map[string]any, *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}) {
		reqArgs, _ := json.Marshal(args)
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n", toolName, reqArgs)
		in := strings.NewReader(req)
		var out bytes.Buffer
		srv := NewServer(st, in, &out)
		if err := srv.Run(); err != nil {
			t.Fatalf("server.Run() failed: %v", err)
		}
		var resp struct {
			Result map[string]any `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Data    any    `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v; raw: %s", err, out.String())
		}
		return resp.Result, resp.Error
	}

	slug := "mcp-review-goal"
	wp := makeTestWorkPackage(slug)
	wpDigest, _ := goals.WorkPackageDigest(wp)
	wpBytes, _ := json.Marshal(wp)
	var wpMap map[string]any
	_ = json.Unmarshal(wpBytes, &wpMap)

	validReview := goals.SpecReview{
		SchemaVersion: goals.SpecReviewSchemaVersion,
		SpecDigest:    wpDigest,
		Reviewer:      "strong-model-planner",
		ReviewedAt:    time.Now().UTC().Format(time.RFC3339),
		Verdict:       goals.VerdictReady,
		Shortcuts: []goals.ShortcutCounter{
			{IncorrectImplementation: "bypass lock", ExcludedByIDs: []string{"T-001"}, Resolution: "strict lock required"},
			{IncorrectImplementation: "ignore errors", ExcludedByIDs: []string{"T-001"}, Resolution: "propagate errors"},
			{IncorrectImplementation: "unbounded paths", ExcludedByIDs: []string{"AC-001"}, Resolution: "boundary check"},
		},
		UnresolvedBlockers: []string{},
	}
	reviewBytes, _ := json.Marshal(validReview)
	var validReviewMap map[string]any
	_ = json.Unmarshal(reviewBytes, &validReviewMap)

	t.Run("CreateGoalWithValidSpecReview", func(t *testing.T) {
		repoDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(repoDir, "goals"), 0755)
		_ = os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test Repo\n"), 0644)

		res, rpcErr := callTool("create_goal", map[string]any{
			"project":      repoDir,
			"slug":         slug,
			"work_package": wpMap,
			"spec_review":  validReviewMap,
		})
		if rpcErr != nil {
			t.Fatalf("expected success with valid spec_review, got error: %d %s", rpcErr.Code, rpcErr.Message)
		}
		if res == nil {
			t.Fatal("expected non-nil result from create_goal")
		}

		// Verify spec-review.json written to disk
		reviewPath := filepath.Join(repoDir, "goals", slug, "spec-review.json")
		if !goals.FileExists(reviewPath) {
			t.Errorf("expected spec-review.json on disk at %s", reviewPath)
		}

		// Loaded review matches input
		loaded, err := goals.LoadSpecReview(repoDir, slug)
		if err != nil {
			t.Fatalf("LoadSpecReview: %v", err)
		}
		if loaded.SpecDigest != wpDigest {
			t.Errorf("loaded digest mismatch: %q vs %q", loaded.SpecDigest, wpDigest)
		}
	})

	t.Run("CreateGoalWithMismatchedDigestFails", func(t *testing.T) {
		repoDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(repoDir, "goals"), 0755)

		staleReview := validReview
		staleReview.SpecDigest = "0000000000000000000000000000000000000000000000000000000000000000"
		staleBytes, _ := json.Marshal(staleReview)
		var staleReviewMap map[string]any
		_ = json.Unmarshal(staleBytes, &staleReviewMap)

		res, rpcErr := callTool("create_goal", map[string]any{
			"project":      repoDir,
			"slug":         slug,
			"work_package": wpMap,
			"spec_review":  staleReviewMap,
		})
		isErr := rpcErr != nil || (res != nil && res["isError"] == true)
		if !isErr {
			t.Fatalf("expected error for stale spec_review, got success: %+v", res)
		}
		errMsg := ""
		if rpcErr != nil {
			errMsg = fmt.Sprintf("%s %v", rpcErr.Message, rpcErr.Data)
		} else {
			raw, _ := json.Marshal(res)
			errMsg = string(raw)
		}
		if !strings.Contains(errMsg, goals.DiagSpecReviewStale) && !strings.Contains(errMsg, "SPEC_REVIEW_STALE") {
			t.Errorf("expected SPEC_REVIEW_STALE in error, got: %s", errMsg)
		}

		// AC-010: Ensure no partial goal directory remains
		goalDir := filepath.Join(repoDir, "goals", slug)
		if _, statErr := os.Stat(goalDir); !os.IsNotExist(statErr) {
			t.Errorf("partial goal directory %s was left on disk after validation failure", goalDir)
		}
	})

	t.Run("CreateGoalSpecReviewWithoutWorkPackageFails", func(t *testing.T) {
		repoDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(repoDir, "goals"), 0755)

		res, rpcErr := callTool("create_goal", map[string]any{
			"project":     repoDir,
			"slug":        "orphan-review-goal",
			"spec_review": validReviewMap,
		})
		isErr := rpcErr != nil || (res != nil && res["isError"] == true)
		if !isErr {
			t.Fatalf("expected error for spec_review without work_package, got nil")
		}
	})
}
