package onboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func directiveItem(t *testing.T, rep *CheckReport) CheckItem {
	t.Helper()
	for _, item := range rep.Items {
		if item.Name == "Recorded directives" {
			return item
		}
	}
	t.Fatalf("Recorded directives item missing: %+v", rep.Items)
	return CheckItem{}
}

func newProbeStore(t *testing.T) (*store.Store, string, store.Project) {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		st.Close()
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repo)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	return st, repo, p
}

func TestOnboardDirectiveProbeCLIAndMCPMemos(t *testing.T) {
	t.Run("CLI memo", func(t *testing.T) {
		st, repo, p := newProbeStore(t)
		defer st.Close()
		if _, err := st.InsertMemo(p.ID, "cli directive", "user", "", nil); err != nil {
			t.Fatal(err)
		}
		rep := CheckWithOptions(st, repo, p.ID, false, true)
		if item := directiveItem(t, rep); item.Status != "pass" {
			t.Fatalf("CLI memo not counted: %+v", item)
		}
	})

	t.Run("MCP memo", func(t *testing.T) {
		st, repo, p := newProbeStore(t)
		defer st.Close()
		if _, err := st.InsertMemoWithSession(p.ID, "mcp directive", "agent", "", nil, "session-1"); err != nil {
			t.Fatal(err)
		}
		rep := CheckWithOptions(st, repo, p.ID, false, true)
		if item := directiveItem(t, rep); item.Status != "pass" {
			t.Fatalf("MCP memo not counted: %+v", item)
		}
	})

	t.Run("approved invariant", func(t *testing.T) {
		st, repo, p := newProbeStore(t)
		defer st.Close()
		if err := st.UpsertFact(store.Fact{
			ID: "inv-1", ProjectID: p.ID, Claim: "rule: keep authority",
			Kind: "invariant", Confidence: "invariant", ReviewStatus: store.ReviewApproved,
		}); err != nil {
			t.Fatal(err)
		}
		rep := CheckWithOptions(st, repo, p.ID, false, true)
		if item := directiveItem(t, rep); item.Status != "pass" {
			t.Fatalf("approved invariant not counted: %+v", item)
		}
	})

	t.Run("candidate and empty", func(t *testing.T) {
		st, repo, p := newProbeStore(t)
		defer st.Close()
		if err := st.UpsertFact(store.Fact{
			ID: "cand-1", ProjectID: p.ID, Claim: "rule: candidate only",
			Kind: "invariant", Confidence: "candidate", ReviewStatus: store.ReviewCandidate,
		}); err != nil {
			t.Fatal(err)
		}
		rep := CheckWithOptions(st, repo, p.ID, false, true)
		item := directiveItem(t, rep)
		if item.Status != "warn" || item.Fix == "" {
			t.Fatalf("candidate must warn with a fix: %+v", item)
		}
	})
}
