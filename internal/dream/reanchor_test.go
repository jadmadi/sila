package dream

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/store"
)

func TestReanchorBudgetAndContract(t *testing.T) {
	dir := t.TempDir()
	for _, budget := range []int{256, 512, 2048} {
		brief, text, err := BuildReanchorBrief(nil, "probe", dir, "s1", budget)
		if err != nil {
			t.Fatal(err)
		}
		if len(text) > budget {
			t.Errorf("budget %d: text is %d bytes", budget, len(text))
		}
		if !strings.HasSuffix(text, "\n") {
			t.Errorf("budget %d: text does not end with a newline", budget)
		}
		if strings.Count(text, "verbs: ") != 1 {
			t.Errorf("budget %d: verbs line missing or duplicated:\n%s", budget, text)
		}
		if !strings.Contains(text, brief.MemoryContractVersion) {
			t.Errorf("budget %d: memory contract version missing", budget)
		}
		if brief.Bytes != len(text) {
			t.Errorf("budget %d: bytes = %d, len(text) = %d", budget, brief.Bytes, len(text))
		}
		if !brief.MemoryUnavailable {
			t.Errorf("budget %d: nil store should report memory_unavailable", budget)
		}
	}
}

func TestReanchorDropOrder(t *testing.T) {
	brief := &ReanchorBrief{
		Project:               "proj",
		Branch:                strings.Repeat("branch-", 100),
		Head:                  strings.Repeat("a", 700),
		ActiveGoal:            "goal-x",
		SessionID:             "s1",
		HandoffID:             "hoff_123",
		NextCommand:           "sila goals ready",
		MemoryContractVersion: "sila/memory-contract/v1",
	}
	text := renderReanchor(brief, 512)
	if len(text) > 512 || !strings.HasSuffix(text, "\n") {
		t.Fatalf("drop order output is %d bytes or lacks newline:\n%s", len(text), text)
	}
	for _, dropped := range []string{"next:", "handoff:", "before grep/web", brief.Branch} {
		if strings.Contains(text, dropped) {
			t.Errorf("expected %q to be dropped before the 512-byte cap:\n%s", dropped, text)
		}
	}
	for _, kept := range []string{"proj", "verbs:", "contract:", brief.MemoryContractVersion} {
		if !strings.Contains(text, kept) {
			t.Errorf("expected %q to survive the drop order:\n%s", kept, text)
		}
	}
}

func TestReanchorUTF8Truncation(t *testing.T) {
	brief := &ReanchorBrief{
		Project:               strings.Repeat("مشروع", 80),
		Branch:                strings.Repeat("فرع", 120),
		Head:                  strings.Repeat("ع", 120),
		ActiveGoal:            "هدف",
		SessionID:             "جلسة",
		HandoffID:             "hoff_utf",
		NextCommand:           "sila goals ready",
		MemoryContractVersion: "sila/memory-contract/v1",
	}
	text := renderReanchor(brief, 256)
	if len(text) > 256 {
		t.Fatalf("UTF-8 output is %d bytes", len(text))
	}
	if !utf8.ValidString(text) {
		t.Fatal("UTF-8 truncation cut a rune")
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("UTF-8 output lacks a trailing newline")
	}
}

func TestBuildReanchorUsesStoreHandoffAndGoal(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "reanchor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.InsertHandoff(store.Handoff{
		ID: "h_new", ProjectID: proj.ID, Summary: "recent", Branch: "main",
		CreatedAt: now.Format(time.RFC3339), Status: string(store.HandoffPending),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgentSession(store.AgentSession{
		SessionID: "sess-bound", ProjectID: proj.ID, ActiveGoal: "goal-bound", Source: "test",
	}); err != nil {
		t.Fatal(err)
	}
	brief, text, err := BuildReanchorBrief(st, proj.ID, dir, "sess-bound", 2048)
	if err != nil {
		t.Fatal(err)
	}
	if brief.HandoffID != "h_new" {
		t.Errorf("handoff = %q, want h_new", brief.HandoffID)
	}
	if brief.ActiveGoal != "goal-bound" {
		t.Errorf("active goal = %q, want goal-bound", brief.ActiveGoal)
	}
	if !strings.Contains(text, "handoff: h_new") || !strings.Contains(text, "goal: goal-bound") {
		t.Errorf("anchor missing resolved context:\n%s", text)
	}

	t.Setenv("SILA_ACTIVE_GOAL", "goal-env")
	brief, _, err = BuildReanchorBrief(nil, proj.ID, dir, "sess-bound", 512)
	if err != nil {
		t.Fatal(err)
	}
	if brief.ActiveGoal != "goal-env" {
		t.Errorf("env goal = %q, want goal-env", brief.ActiveGoal)
	}
}

func TestBuildReanchorDropsOldHandoff(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	if err := st.InsertHandoff(store.Handoff{
		ID: "h_old", ProjectID: proj.ID, Summary: "old", Branch: "main",
		CreatedAt: old.Format(time.RFC3339), Status: string(store.HandoffPending),
	}); err != nil {
		t.Fatal(err)
	}
	brief, text, err := BuildReanchorBrief(st, proj.ID, dir, "", 512)
	if err != nil {
		t.Fatal(err)
	}
	if brief.HandoffID != "" {
		t.Errorf("old handoff = %q, want none", brief.HandoffID)
	}
	if strings.Contains(text, "handoff:") {
		t.Errorf("old handoff leaked into text:\n%s", text)
	}
}

func TestReanchorGitState(t *testing.T) {
	dir := t.TempDir()
	if branch, head := reanchorGitState(dir); branch != "none" || head != "none" {
		t.Fatalf("non-git state = %q/%q, want none/none", branch, head)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	branch, head := reanchorGitState(dir)
	if branch == "none" || len(head) != 7 {
		t.Fatalf("git state = %q/%q, want branch and 7-char head", branch, head)
	}
}

func TestReanchorKeyAndMarkerPath(t *testing.T) {
	a := ReanchorKey("s1", "abc1234", "post-compaction", "")
	if a != ReanchorKey("s1", "abc1234", "post-compaction", "") {
		t.Fatal("reanchor key is not deterministic")
	}
	if a == ReanchorKey("s1", "abc1234", "post-compaction", "2") {
		t.Fatal("sequence must change the dedupe key")
	}
	path := ReanchorMarkerPath("/tmp/proj", "a/b:c")
	if !strings.HasSuffix(path, "a_b_c.json") || !strings.Contains(path, ".sila/state/reanchor") {
		t.Fatalf("marker path = %q", path)
	}
	if !strings.HasSuffix(ReanchorMarkerPath("/tmp/proj", ""), "unbound.json") {
		t.Fatal("empty session must use the unbound marker")
	}
}

func TestReanchorDoesNotWriteStore(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "readonly.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := st.FactsByProject(proj.ID)
	if _, _, err := BuildReanchorBrief(st, proj.ID, dir, "s1", 512); err != nil {
		t.Fatal(err)
	}
	after, _ := st.FactsByProject(proj.ID)
	if len(after) != len(before) {
		t.Fatalf("re-anchor wrote facts: before %d, after %d", len(before), len(after))
	}
}
