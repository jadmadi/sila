package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestFeedbackCLI_ExplicitIDAndEpistemicGrammar(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "feedback_id_test.db")

	ui := render.New(nil, true)

	// 1. Record feedback with explicit customID argument
	runFeedback(ui, dbPath, "sila", "antigravity", "idea", "", "", "", "dialectic-subshell-paths", true, 10, false, false, false, false, false, false, []string{"Consider documenting dialectic subshell paths"})

	st, err := store.OpenRO(dbPath)
	if err != nil {
		t.Fatalf("OpenRO: %v", err)
	}
	defer st.Close()

	fbCustom, err := st.GetFeedback("fb_dialectic-subshell-paths")
	if err != nil {
		t.Fatalf("expected fb_dialectic-subshell-paths to be found: %v", err)
	}
	if fbCustom.Content != "Consider documenting dialectic subshell paths" {
		t.Errorf("unexpected content: %s", fbCustom.Content)
	}

	// 2. Record feedback with inline --id= flag in args
	runFeedback(ui, dbPath, "sila", "antigravity", "tip", "", "", "", "", true, 10, false, false, false, false, false, false, []string{"--id=sentry-toolchain-integrity", "Always sentry toolchain integrity in pre-push"})

	fbFlag, err := st.GetFeedback("fb_sentry-toolchain-integrity")
	if err != nil {
		t.Fatalf("expected fb_sentry-toolchain-integrity to be found: %v", err)
	}
	if !strings.Contains(fbFlag.Content, "toolchain integrity") {
		t.Errorf("unexpected content: %s", fbFlag.Content)
	}

	// 3. Record feedback without ID; derives mnemonic ID automatically
	runFeedback(ui, dbPath, "sila", "antigravity", "issue", "", "", "", "", true, 10, false, false, false, false, false, false, []string{"Reconcile workspace sovereignty across subrepos"})

	items, err := st.ListFeedback("sila", "issue", 10)
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 issue item, got %d", len(items))
	}
	if !strings.HasPrefix(items[0].ID, "fb_reconcile-") {
		t.Errorf("expected derived ID to start with fb_reconcile-, got %s", items[0].ID)
	}
}
