package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/proclive"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func commitFixture(t *testing.T) (repoDir, dbPath string, chain []proclive.Ancestor) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repoDir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "-q", "-m", "fixture")
	chain, err := proclive.Ancestry(os.Getpid(), 0)
	if err != nil || len(chain) < 2 {
		t.Skipf("ancestry too short: %v %d", err, len(chain))
	}
	dbPath = filepath.Join(t.TempDir(), "s.db")
	return repoDir, dbPath, chain
}

func insertSession(t *testing.T, dbPath, projectID, sessionID string, ancestor proclive.Ancestor) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	if err := st.UpsertAgentSession(store.AgentSession{
		SessionID: sessionID, ProjectID: projectID, Harness: "test", Agent: "test",
		PID: ancestor.PID, PIDStartTicks: ancestor.StartTicks, Source: "test", StartedAt: now, LastSeenAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCommitReceiptNearestAncestor(t *testing.T) {
	repoDir, dbPath, chain := commitFixture(t)
	projectID := store.ProjectID(repoDir)
	insertSession(t, dbPath, projectID, "s-parent", chain[1])
	if len(chain) > 2 {
		insertSession(t, dbPath, projectID, "s-grand", chain[2])
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	receipt, found, err := resolveCommitReceipt(st, projectID, repoDir)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if receipt.SessionID != "s-parent" || receipt.Source != "ancestry" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if receipt.CommitSHA == "" || receipt.TreeHash == "" || receipt.Branch == "" {
		t.Fatalf("missing git fields: %+v", receipt)
	}
}

func TestResolveCommitReceiptMismatchAndEmpty(t *testing.T) {
	repoDir, dbPath, chain := commitFixture(t)
	projectID := store.ProjectID(repoDir)
	missing := chain[1]
	missing.StartTicks++
	insertSession(t, dbPath, projectID, "wrong", missing)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, found, err := resolveCommitReceipt(st, projectID, repoDir); err != nil || found {
		t.Fatalf("mismatch found=%v err=%v", found, err)
	}
	emptyRepo := t.TempDir()
	emptyDB := filepath.Join(t.TempDir(), "e.db")
	st2, err := store.Open(emptyDB)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, found, err := resolveCommitReceipt(st2, store.ProjectID(emptyRepo), emptyRepo); err == nil && found {
		t.Fatal("empty store resolved a session")
	}
}

func TestRunHookCommitWritesReceipt(t *testing.T) {
	repoDir, dbPath, chain := commitFixture(t)
	projectID := store.ProjectID(repoDir)
	insertSession(t, dbPath, projectID, "s-hook", chain[1])
	ui := render.New(os.Stdout, true)
	captureStdout(t, func() {
		runHookCommit(ui, dbPath, projectID, repoDir, false)
	})
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sha, _, _, err := gitCommitInfo(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok, err := st.CommitReceipt(projectID, sha)
	if err != nil || !ok {
		t.Fatalf("receipt missing ok=%v err=%v", ok, err)
	}
	if receipt.SessionID != "s-hook" {
		t.Fatalf("receipt = %+v", receipt)
	}
}
