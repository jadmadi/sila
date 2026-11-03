package heal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

// TestBuildPlanRegistersRepository covers the registration half of repository
// resolution: a repository sila heals gets its own row, so an unregistered
// repository under a registered container (the live /workspace/sila/github case where
// the container was registered as example-client.com from a stale workspace file)
// has an identity of its own instead of being absorbed or left unnamed.
func TestBuildPlanRegistersRepository(t *testing.T) {
	umbrella := filepath.Join(t.TempDir(), "github")
	repo := filepath.Join(umbrella, "projects", "waqftech", "MutmainKalp.com")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := `{"folders":[{"path":"clients/example-client.com"}],"settings":{}}`
	if err := os.WriteFile(filepath.Join(umbrella, "example-client.com.code-workspace"), []byte(ws), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	umbrellaProj, err := st.EnsureProject(umbrella)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.ProjectForDir(repo); err != nil || ok {
		t.Fatalf("nested repository must start unregistered: ok=%v err=%v", ok, err)
	}

	plan, err := BuildPlan(st, repo)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project == umbrellaProj.ID {
		t.Fatalf("heal scoped the nested repository to the container project %q", plan.Project)
	}
	if plan.Project != "MutmainKalp.com" {
		t.Fatalf("plan project = %q, want MutmainKalp.com", plan.Project)
	}
	registered, ok, err := st.ProjectForDir(repo)
	if err != nil || !ok {
		t.Fatalf("heal did not register the repository: ok=%v err=%v", ok, err)
	}
	if registered.ID != "MutmainKalp.com" {
		t.Fatalf("registered id = %q, want MutmainKalp.com", registered.ID)
	}
}
