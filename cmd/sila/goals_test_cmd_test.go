package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

func TestGoalsCLI_TestAndAutoCertify(t *testing.T) {
	tempDir := t.TempDir()
	slug := "autocert-cli-goal"
	goalDir := filepath.Join(tempDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalContent := "# Goal: autocert-cli-goal\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Create and save a passing verification contract
	contract, err := goals.CreateVerificationContract(tempDir, "echo 'cli test passed'", 30, 5, 0, nil)
	if err != nil {
		t.Fatalf("CreateVerificationContract failed: %v", err)
	}
	if err := goals.SaveContract(tempDir, slug, contract); err != nil {
		t.Fatalf("SaveContract failed: %v", err)
	}

	ui := render.New(os.Stdout, true)

	// 2. Claim goal
	claimArgs := []string{"claim", slug, "--agent=@antigravity"}
	runGoals(ui, "", tempDir, "antigravity", "starting", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, claimArgs)

	// 3. Test verification contract runner directly
	res, err := goals.RunVerificationContract(context.Background(), tempDir, slug)
	if err != nil {
		t.Fatalf("RunVerificationContract failed: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected contract to pass")
	}

	// 4. Auto-certify via goals certify --auto-run
	cert, err := goals.CertifyGoal(tempDir, slug, filepath.Join(goalDir, "goal.md"), "@independent-auditor", "manual cert verification")
	if err != nil {
		t.Fatalf("CertifyGoal failed: %v", err)
	}
	if !cert.Verified {
		t.Fatalf("expected cert.Verified to be true")
	}

	// 5. Test AutoCertifyGoal end-to-end
	slug2 := "autocert-full-goal"
	goalDir2 := filepath.Join(tempDir, "goals", slug2)
	_ = os.MkdirAll(goalDir2, 0755)
	_ = os.WriteFile(filepath.Join(goalDir2, "goal.md"), []byte("# Goal 2\n"), 0644)
	c2, _ := goals.CreateVerificationContract(tempDir, "true", 30, 5, 0, nil)
	_ = goals.SaveContract(tempDir, slug2, c2)

	cert2, res2, err := goals.AutoCertifyGoal(context.Background(), tempDir, slug2, "@antigravity", "auto cert")
	if err != nil {
		t.Fatalf("AutoCertifyGoal failed: %v", err)
	}
	if !res2.Passed || !cert2.Verified {
		t.Fatalf("expected res2.Passed and cert2.Verified")
	}
}
