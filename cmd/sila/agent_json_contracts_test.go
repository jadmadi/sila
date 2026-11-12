package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// Every --json run must put exactly one JSON document on stdout, including the
// no-handoff case. Feedback fb_2e419520abc4f0d3: the empty case printed prose.
func TestHandoffJSONAlwaysEmitsJSON(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "handoff_json.db")
	ui := render.New(os.Stdout, true)

	t.Run("no handoff emits status none", func(t *testing.T) {
		out := captureStdout(t, func() {
			runHandoff(ui, dbPath, "no-such-project", "", "", "", "", "", true, false, nil)
		})

		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("expected valid JSON, got %q: %v", out, err)
		}
		if got["status"] != "none" {
			t.Errorf("status = %v, want none", got["status"])
		}
		if got["project_id"] != "no-such-project" {
			t.Errorf("project_id = %v, want no-such-project", got["project_id"])
		}
	})

	t.Run("recorded handoff emits the handoff object", func(t *testing.T) {
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		p, err := st.EnsureProject(tmp)
		if err != nil {
			st.Close()
			t.Fatal(err)
		}
		err = st.InsertHandoff(store.Handoff{
			ProjectID: p.ID,
			Agent:     "agent",
			Summary:   "json contract test",
			Status:    "open",
		})
		st.Close()
		if err != nil {
			t.Fatal(err)
		}

		out := captureStdout(t, func() {
			runHandoff(ui, dbPath, p.ID, "", "", "", "", "", true, false, nil)
		})

		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("expected valid JSON, got %q: %v", out, err)
		}
		if got["summary"] != "json contract test" {
			t.Errorf("summary = %v, want json contract test", got["summary"])
		}
		if got["status"] != "open" {
			t.Errorf("status = %v, want open", got["status"])
		}
	})
}

// sila goals active is the supported active-goal binding requested by feedback
// fb_2e419520abc4f0d3. It must resolve to the claimed-goals view in both the
// top-level and the project-scoped dispatch.
func TestGoalsActiveAliasResolvesToClaimed(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "goals_active.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(tmp); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "Active Alias Target", "Description", "", "internal/alias/active_alias.go", 1,
		true, false, false, false, false, false, false, false, false, false, false, false, true,
		"", "", "", false, []string{"create", "active-alias"})
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0,
		false, false, false, false, false, false, false, false, false, false, false, false, true,
		"", "", "", false, []string{"claim", "active-alias"})

	assertClaimedPayload := func(t *testing.T, out string) {
		t.Helper()
		var payload struct {
			Project string       `json:"project"`
			Filter  string       `json:"filter"`
			Count   int          `json:"count"`
			Goals   []goals.Goal `json:"goals"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("expected valid JSON, got %q: %v", out, err)
		}
		if payload.Filter != "claimed" {
			t.Errorf("filter = %q, want claimed", payload.Filter)
		}
		if payload.Count != 1 || len(payload.Goals) != 1 {
			t.Fatalf("count = %d, goals = %d, want 1", payload.Count, len(payload.Goals))
		}
		if payload.Goals[0].Slug != "active-alias" {
			t.Errorf("slug = %q, want active-alias", payload.Goals[0].Slug)
		}
		if payload.Goals[0].Claim == nil || payload.Goals[0].Claim.ClaimedBy != "agent" {
			t.Errorf("claim = %+v, want claimed_by agent", payload.Goals[0].Claim)
		}
	}

	t.Run("top-level subcommand", func(t *testing.T) {
		out := captureStdout(t, func() {
			runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0,
				false, false, false, false, false, false, false, false, true, false, false, false, true,
				"", "", "", false, []string{"active"})
		})
		assertClaimedPayload(t, out)
	})

	t.Run("project-scoped subcommand", func(t *testing.T) {
		out := captureStdout(t, func() {
			runGoals(ui, dbPath, "", "agent", "", "", "", false, false, "", "", "", "", "", 0,
				false, false, false, false, false, false, false, false, true, false, false, false, true,
				"", "", "", false, []string{tmp, "active"})
		})
		assertClaimedPayload(t, out)
	})
}
