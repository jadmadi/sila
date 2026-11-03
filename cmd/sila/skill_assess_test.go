package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestSkillSubcommandRouting_CollisionGuard(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	ui := render.New(nil, true)

	// Invoke `runSkills` with "assess" — should NOT call runSkillCreate
	runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"assess", "--json"}, "skill")

	// Ensure no skill named "assess" was scaffolded
	assessSkillDir := filepath.Join(tempDir, ".agents", "skills", "assess")
	if _, err := os.Stat(assessSkillDir); !os.IsNotExist(err) {
		t.Fatalf("VIOLATION: Subcommand collision guard failed! 'assess' directory was scaffolded at %s", assessSkillDir)
	}

	// Ensure no test database was created if it didn't exist
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("VIOLATION: Zero-write violation! SQLite database was created at %s during assess", dbPath)
	}
}

func TestSkillAssessCLI_JSONOutput(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	ui := render.New(nil, true)

	// Create sample go.mod
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module myapp\ngo 1.27\n"), 0644)

	// Run assessment with --json
	runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"assess", "--json"}, "skill")

	// Check that no directory was created
	skillsDir := filepath.Join(tempDir, ".agents", "skills")
	if _, err := os.Stat(skillsDir); !os.IsNotExist(err) {
		t.Errorf("expected no .agents/skills directory created, but found one")
	}
}
