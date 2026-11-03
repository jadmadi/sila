package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSortGoalsTopologically(t *testing.T) {
	// a-leaf depends on m-mid, which depends on z-root
	goals := []Goal{
		{Slug: "a-leaf", Title: "Leaf Goal", Status: "pending", DependsOn: []string{"m-mid"}, Blocked: true},
		{Slug: "m-mid", Title: "Mid Goal", Status: "pending", DependsOn: []string{"z-root"}, Blocked: true},
		{Slug: "z-root", Title: "Root Goal", Status: "pending", Independent: true, Blocked: false},
	}

	sorted := SortGoalsTopologically(goals)
	if len(sorted) != 3 {
		t.Fatalf("expected 3 goals, got %d", len(sorted))
	}

	// Topological order MUST place z-root before m-mid, and m-mid before a-leaf
	if sorted[0].Slug != "z-root" {
		t.Errorf("expected sorted[0] to be z-root, got %q", sorted[0].Slug)
	}
	if sorted[1].Slug != "m-mid" {
		t.Errorf("expected sorted[1] to be m-mid, got %q", sorted[1].Slug)
	}
	if sorted[2].Slug != "a-leaf" {
		t.Errorf("expected sorted[2] to be a-leaf, got %q", sorted[2].Slug)
	}
}

func TestSortGoalsTopologicallyWithContext(t *testing.T) {
	// z-root is already implemented (in context), a-leaf and b-leaf are pending
	// b-leaf depends on a-leaf
	contextGoals := []Goal{
		{Slug: "z-root", Title: "Root", Status: "implemented", Independent: true},
	}

	pendingGoals := []Goal{
		{Slug: "b-leaf", Title: "Downstream Leaf", Status: "pending", DependsOn: []string{"a-leaf"}},
		{Slug: "a-leaf", Title: "Upstream Leaf", Status: "pending", DependsOn: []string{"z-root"}},
	}

	sorted := SortGoalsTopologically(pendingGoals, contextGoals)
	if len(sorted) != 2 {
		t.Fatalf("expected 2 goals, got %d", len(sorted))
	}
	if sorted[0].Slug != "a-leaf" {
		t.Errorf("expected a-leaf first, got %q", sorted[0].Slug)
	}
	if sorted[1].Slug != "b-leaf" {
		t.Errorf("expected b-leaf second, got %q", sorted[1].Slug)
	}
}

func TestSyncReadmeTopologicalOrdering(t *testing.T) {
	tmp := t.TempDir()
	goalsDir := filepath.Join(tmp, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}

	res := &AuditResult{
		Project: "test-topo",
		Goals: []Goal{
			// Even though a-leaf comes first alphanumerically, it depends on z-root
			{
				Slug:        "a-leaf",
				Title:       "Leaf Goal",
				Description: "Leaf depends on z-root",
				Status:      "pending",
				DependsOn:   []string{"z-root"},
				Blocked:     false,
				LaunchCmd:   "/goal goals/a-leaf/goal.md",
			},
			{
				Slug:        "z-root",
				Title:       "Root Goal",
				Description: "Root has no dependencies",
				Status:      "pending",
				Independent: true,
				Blocked:     false,
				LaunchCmd:   "/goal goals/z-root/goal.md",
			},
			// Implemented section: child depends on parent
			{
				Slug:      "a-impl-child",
				Title:     "Impl Child",
				Status:    "implemented",
				DependsOn: []string{"z-impl-parent"},
				Commit:    "abc1234",
			},
			{
				Slug:        "z-impl-parent",
				Title:       "Impl Parent",
				Status:      "implemented",
				Independent: true,
				Commit:      "def5678",
			},
		},
		Total:            4,
		PendingCount:     2,
		ImplementedCount: 2,
	}

	if err := SyncReadmeWithOpts(tmp, res, SyncReadmeOptions{}); err != nil {
		t.Fatalf("SyncReadmeWithOpts failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(goalsDir, "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	readme := string(content)

	// In Pending section, z-root MUST appear before a-leaf
	zRootIdx := strings.Index(readme, "[`z-root`]")
	aLeafIdx := strings.Index(readme, "[`a-leaf`]")
	if zRootIdx == -1 || aLeafIdx == -1 {
		t.Fatalf("expected both z-root and a-leaf in README, got:\n%s", readme)
	}
	if zRootIdx >= aLeafIdx {
		t.Errorf("expected z-root (index %d) to precede a-leaf (index %d) topologically in README.md", zRootIdx, aLeafIdx)
	}

	// In Implemented section, z-impl-parent MUST appear before a-impl-child
	zParentIdx := strings.Index(readme, "[`z-impl-parent`]")
	aChildIdx := strings.Index(readme, "[`a-impl-child`]")
	if zParentIdx == -1 || aChildIdx == -1 {
		t.Fatalf("expected both z-impl-parent and a-impl-child in README, got:\n%s", readme)
	}
	if zParentIdx >= aChildIdx {
		t.Errorf("expected z-impl-parent (index %d) to precede a-impl-child (index %d) topologically in README.md", zParentIdx, aChildIdx)
	}
}
