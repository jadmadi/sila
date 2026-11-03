package store

import (
	"path/filepath"
	"testing"
)

func TestPreferencesRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	st, err := Open(filepath.Join(tmp, "pref_test.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	pref := &Preference{
		ProjectID:    "test-proj",
		ContextQuery: "sqlite concurrency",
		CandidateA:   "WAL mode with modernc.org/sqlite",
		CandidateB:   "mattn/go-sqlite3 with CGO",
		Winner:       "A",
		Rationale:    "Pure-Go CGO-free portability invariant",
		Agent:        "antigravity",
	}

	if err := st.SavePreference(pref); err != nil {
		t.Fatalf("SavePreference failed: %v", err)
	}

	prefs, err := st.Preferences(PreferenceFilter{ProjectID: "test-proj"})
	if err != nil {
		t.Fatalf("Preferences query failed: %v", err)
	}

	if len(prefs) != 1 {
		t.Fatalf("expected 1 preference, got %d", len(prefs))
	}
	p := prefs[0]
	if p.Winner != "A" || p.CandidateA != pref.CandidateA || p.Rationale != pref.Rationale {
		t.Fatalf("unexpected preference content: %+v", p)
	}
}
