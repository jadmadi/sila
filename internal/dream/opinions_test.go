package dream

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestOpinionsCompilationAndManagement(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_opinions.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	projectID := "testproj"
	cwd := "/tmp/testproj"

	// 1. Add architectural opinion
	_, err = AddOpinion(st, projectID, "arch", "Always enforce pure-Go SQLite with CGO_ENABLED=0", "tester", cwd)
	if err != nil {
		t.Fatalf("failed to add arch opinion: %v", err)
	}

	// 2. Add agents opinion
	_, err = AddOpinion(st, projectID, "agents", "Multi-harness portability: store state outside vendor silos", "tester", cwd)
	if err != nil {
		t.Fatalf("failed to add agents opinion: %v", err)
	}

	// 3. Add verification opinion
	_, err = AddOpinion(st, projectID, "verification", "Disk grounding: code in repo is ground truth over model claims", "tester", cwd)
	if err != nil {
		t.Fatalf("failed to add verification opinion: %v", err)
	}

	// 4. Add taste opinion
	_, err = AddOpinion(st, projectID, "taste", "Token-efficient output via TOON format saves 40% tokens", "tester", cwd)
	if err != nil {
		t.Fatalf("failed to add taste opinion: %v", err)
	}

	// 5. List opinions
	ops, err := ListOpinions(st, projectID)
	if err != nil {
		t.Fatalf("failed to list opinions: %v", err)
	}
	if len(ops) < 4 {
		t.Fatalf("expected at least 4 opinions, got %d", len(ops))
	}

	// 6. Compile OPINIONS.md
	doc, err := CompileOpinions(st, projectID)
	if err != nil {
		t.Fatalf("failed to compile opinions: %v", err)
	}

	if !strings.Contains(doc, "# OPINIONS.md — testproj") {
		t.Errorf("missing header in compiled doc:\n%s", doc)
	}
	if !strings.Contains(doc, "Always enforce pure-Go SQLite") {
		t.Errorf("missing arch opinion in doc:\n%s", doc)
	}
	if !strings.Contains(doc, "Multi-harness portability") {
		t.Errorf("missing agents opinion in doc:\n%s", doc)
	}
	if !strings.Contains(doc, "Disk grounding") {
		t.Errorf("missing verification opinion in doc:\n%s", doc)
	}
	if !strings.Contains(doc, "Token-efficient output via TOON") {
		t.Errorf("missing taste opinion in doc:\n%s", doc)
	}
}
