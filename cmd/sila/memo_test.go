package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func seedMemoProject(t *testing.T) (string, store.Project) {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "memo.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()
	return dbPath, p
}

func memoCount(t *testing.T, dbPath, project string) []store.Fact {
	t.Helper()
	st, err := store.OpenRO(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	memos, err := st.ListFactsByKind("memo", project, time.Time{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	return memos
}

func TestMemoRoundTripWithoutExplicitProject(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	dbPath := filepath.Join(tmp, "memo.db")
	ui := render.New(os.Stdout, true)

	write := captureStdout(t, func() {
		runMemo(ui, dbPath, "", "tester", "", "", memoOptions{jsonOut: true}, []string{"alpha directive one"})
	})
	var written store.Fact
	if err := json.Unmarshal([]byte(write), &written); err != nil {
		t.Fatalf("write JSON: %v\n%s", err, write)
	}

	list := captureStdout(t, func() {
		runMemo(ui, dbPath, "", "tester", "", "", memoOptions{jsonOut: true}, []string{"list"})
	})
	var payload struct {
		Project        string       `json:"project"`
		ProjectDisplay string       `json:"project_display"`
		Count          int          `json:"count"`
		Memos          []store.Fact `json:"memos"`
	}
	if err := json.Unmarshal([]byte(list), &payload); err != nil {
		t.Fatalf("list JSON: %v\n%s", err, list)
	}
	if payload.Count != 1 {
		t.Fatalf("read-after-write count = %d, want 1 (project %q): %s", payload.Count, payload.Project, list)
	}
	if payload.Project != written.ProjectID {
		t.Fatalf("write project %q != list project %q", written.ProjectID, payload.Project)
	}
	if payload.ProjectDisplay == "" {
		t.Fatal("list JSON must expose project_display")
	}
}

func TestMemoEmptyStateTeachesRecordCommand(t *testing.T) {
	dbPath, p := seedMemoProject(t)
	ui := render.New(os.Stdout, false)
	out := captureStdout(t, func() {
		runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{}, []string{"list"})
	})
	if !strings.Contains(out, "No memos recorded") || !strings.Contains(out, "sila memo") {
		t.Fatalf("empty state must name the record command: %s", out)
	}
}

func TestMemoListHistoricalDerivedKeyFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	dbPath := filepath.Join(tmp, "memo.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	raw := store.ProjectID(store.CanonicalProjectPath(tmp))
	if _, err := st.InsertMemo(raw, "legacy directive", "tester", tmp, nil); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runMemo(ui, dbPath, "", "tester", "", "", memoOptions{jsonOut: true}, []string{"list"})
	})
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list JSON: %v\n%s", err, out)
	}
	if payload.Count != 1 {
		t.Fatalf("historical memo count = %d, want 1: %s", payload.Count, out)
	}
}

func TestMemoClaimFlagOwnershipAndFilter(t *testing.T) {
	if !subcommandOwnedFlags("memo")["claim"] {
		t.Fatal("memo --claim must be owned by the memo subcommand, not the global goals boolean")
	}
	if subcommandOwnedFlags("memo")["claim-text"] {
		t.Fatal("--claim-text must stay a root string flag")
	}

	dbPath, p := seedMemoProject(t)
	ui := render.New(os.Stdout, true)
	for _, text := range []string{"alpha directive", "beta directive"} {
		captureStdout(t, func() {
			runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{jsonOut: true}, []string{text})
		})
	}

	out := captureStdout(t, func() {
		runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{jsonOut: true}, []string{"list", "--claim=alpha directive"})
	})
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list JSON: %v\n%s", err, out)
	}
	if payload.Count != 1 {
		t.Fatalf("claim filter count = %d, want 1: %s", payload.Count, out)
	}
}

func TestMemoSubcommandsDoNotWriteByAccident(t *testing.T) {
	dbPath, p := seedMemoProject(t)
	ui := render.New(os.Stdout, true)

	captureStdout(t, func() {
		runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{jsonOut: true}, []string{"plain decision"})
	})
	if got := len(memoCount(t, dbPath, p.ID)); got != 1 {
		t.Fatalf("shorthand wrote %d memos", got)
	}

	captureStdout(t, func() {
		runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{jsonOut: true}, []string{"add", "list"})
	})
	memos := memoCount(t, dbPath, p.ID)
	if len(memos) != 2 || memos[0].Claim != "list" && memos[1].Claim != "list" {
		t.Fatalf("memo add list failed: %+v", memos)
	}

	captureStdout(t, func() {
		runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{jsonOut: true, text: "list"}, nil)
	})
	memos = memoCount(t, dbPath, p.ID)
	if len(memos) != 2 {
		t.Fatalf("memo --text=list wrote %d memos, want 2 after dedupe", len(memos))
	}
	if memos[0].Claim != "list" && memos[1].Claim != "list" {
		t.Fatalf("memo --text=list did not record the literal text: %+v", memos)
	}

	// memo list is read-only and repeatable.
	out := captureStdout(t, func() {
		runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{jsonOut: true}, []string{"list"})
	})
	var payload struct {
		Count int `json:"count"`
		Memos []struct {
			ID    string `json:"id"`
			Claim string `json:"claim"`
			Agent string `json:"agent"`
		} `json:"memos"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid list JSON: %v\n%s", err, out)
	}
	if payload.Count != 2 {
		t.Fatalf("expected 2 memos, got %+v", payload)
	}
	captureStdout(t, func() {
		runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{jsonOut: true}, []string{"list"})
	})
	if got := len(memoCount(t, dbPath, p.ID)); got != 2 {
		t.Fatalf("memo list changed the count: %d", got)
	}

	// Historical trap diagnostic.
	claimOut := captureStdout(t, func() {
		runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{jsonOut: true, claim: "list"}, []string{"list"})
	})
	if err := json.Unmarshal([]byte(claimOut), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 {
		t.Fatalf("memo list --claim=list = %d, want 1", payload.Count)
	}

	// memo show returns the fact unchanged.
	id := memoCount(t, dbPath, p.ID)[0].ID
	show := captureStdout(t, func() {
		runMemo(ui, dbPath, p.ID, "tester", "", "", memoOptions{jsonOut: true}, []string{"show", id})
	})
	var fact store.Fact
	if err := json.Unmarshal([]byte(show), &fact); err != nil || fact.ID != id {
		t.Fatalf("memo show failed: %v %+v", err, fact)
	}
}
