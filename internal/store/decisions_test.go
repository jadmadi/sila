package store

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDecisions_AllocationAndValidation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "decisions_test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	projectID := "proj-decisions"

	// 1. Missing title rejected
	_, err = s.CreateDecision(&Decision{
		ProjectID: projectID,
		Title:     "",
		Options: []DecisionOption{
			{Letter: "A", Title: "Opt A"},
			{Letter: "B", Title: "Opt B"},
		},
		Recommendation: "A",
	})
	if err == nil || !strings.Contains(err.Error(), "title cannot be empty") {
		t.Fatalf("expected title validation error, got: %v", err)
	}

	// 2. Less than 2 options rejected
	_, err = s.CreateDecision(&Decision{
		ProjectID: projectID,
		Title:     "Single option decision",
		Options: []DecisionOption{
			{Letter: "A", Title: "Only one"},
		},
		Recommendation: "A",
	})
	if err == nil || !strings.Contains(err.Error(), "at least two lettered options") {
		t.Fatalf("expected >= 2 options error, got: %v", err)
	}

	// 3. Recommendation not matching options rejected
	_, err = s.CreateDecision(&Decision{
		ProjectID: projectID,
		Title:     "Mismatched recommendation",
		Options: []DecisionOption{
			{Letter: "A", Title: "Opt A"},
			{Letter: "B", Title: "Opt B"},
		},
		Recommendation: "C",
	})
	if err == nil || !strings.Contains(err.Error(), "must match one of the lettered options") {
		t.Fatalf("expected recommendation match error, got: %v", err)
	}

	// 4. Successful creation of D1
	d1, err := s.CreateDecision(&Decision{
		ProjectID: projectID,
		Title:     "SQLite Engine Selection",
		Context:   "Pure-Go vs CGO SQLite performance and distribution tradeoffs",
		Options: []DecisionOption{
			{Letter: "A", Title: "modernc.org/sqlite", TradeOff: "Zero CGO, easy cross-compilation"},
			{Letter: "B", Title: "mattn/go-sqlite3", TradeOff: "CGO required, standard performance"},
		},
		Recommendation: "A",
		Rationale:      "Zero CGO enables clean standalone static binary distribution",
		CreatedBy:      "@antigravity",
		GoalSlug:       "sqlite-architecture",
	})
	if err != nil {
		t.Fatalf("CreateDecision D1 failed: %v", err)
	}
	if d1.Address != "D1" {
		t.Fatalf("expected Address D1, got %q", d1.Address)
	}
	if d1.SeqNumber != 1 {
		t.Fatalf("expected SeqNumber 1, got %d", d1.SeqNumber)
	}
	if d1.Status != "open" {
		t.Fatalf("expected status open, got %q", d1.Status)
	}

	// 5. Successful creation of D2 (sequential allocation)
	d2, err := s.CreateDecision(&Decision{
		ProjectID: projectID,
		Title:     "FTS5 Index Repair Strategy",
		Options: []DecisionOption{
			{Letter: "A", Title: "In-place Rebuild", TradeOff: "Instant recovery without data copying"},
			{Letter: "B", Title: "Drop and Recreate", TradeOff: "Higher latency and risk"},
		},
		Recommendation: "A",
		CreatedBy:      "@antigravity",
	})
	if err != nil {
		t.Fatalf("CreateDecision D2 failed: %v", err)
	}
	if d2.Address != "D2" {
		t.Fatalf("expected Address D2, got %q", d2.Address)
	}
	if d2.SeqNumber != 2 {
		t.Fatalf("expected SeqNumber 2, got %d", d2.SeqNumber)
	}

	// 6. Retrieve by address and ID
	gotD1, err := s.GetDecision(projectID, "D1")
	if err != nil {
		t.Fatalf("GetDecision D1 failed: %v", err)
	}
	if gotD1.Title != d1.Title || len(gotD1.Options) != 2 {
		t.Fatalf("GetDecision D1 mismatch: %+v", gotD1)
	}

	gotByID, err := s.GetDecision(projectID, d1.ID)
	if err != nil {
		t.Fatalf("GetDecision by ID failed: %v", err)
	}
	if gotByID.Address != "D1" {
		t.Fatalf("expected Address D1, got %s", gotByID.Address)
	}

	// 7. Resolve D1
	resolvedD1, err := s.ResolveDecision(projectID, "D1", "A", "Adopted pure-Go SQLite for standalone binary", "@jadmadi")
	if err != nil {
		t.Fatalf("ResolveDecision failed: %v", err)
	}
	if resolvedD1.Status != "resolved" {
		t.Fatalf("expected status resolved, got %s", resolvedD1.Status)
	}
	if resolvedD1.Resolution != "A" {
		t.Fatalf("expected resolution A, got %s", resolvedD1.Resolution)
	}
	if resolvedD1.ResolvedBy != "@jadmadi" {
		t.Fatalf("expected resolved_by @jadmadi, got %s", resolvedD1.ResolvedBy)
	}

	// Resolving already resolved decision fails
	_, err = s.ResolveDecision(projectID, "D1", "B", "second try", "@hacker")
	if err == nil || !strings.Contains(err.Error(), "already resolved") {
		t.Fatalf("expected already resolved error, got: %v", err)
	}

	// 8. List decisions
	listAll, err := s.ListDecisions(DecisionFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListDecisions failed: %v", err)
	}
	if len(listAll) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(listAll))
	}

	listOpen, err := s.ListDecisions(DecisionFilter{ProjectID: projectID, Status: "open"})
	if err != nil {
		t.Fatalf("ListDecisions open failed: %v", err)
	}
	if len(listOpen) != 1 || listOpen[0].Address != "D2" {
		t.Fatalf("expected 1 open decision D2, got %+v", listOpen)
	}

	// 9. Collision checks
	anomalies, err := s.CheckDecisionCollisions(projectID)
	if err != nil {
		t.Fatalf("CheckDecisionCollisions failed: %v", err)
	}
	if len(anomalies) != 0 {
		t.Fatalf("expected zero anomalies, got: %v", anomalies)
	}
}

func TestDecisions_AddressCollisionPrevention(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "collision_test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	projectID := "proj-collision"

	// Create initial decision D1
	_, err = s.CreateDecision(&Decision{
		ProjectID: projectID,
		Address:   "D1",
		Title:     "Initial decision",
		Options: []DecisionOption{
			{Letter: "A", Title: "Opt 1"},
			{Letter: "B", Title: "Opt 2"},
		},
		Recommendation: "A",
	})
	if err != nil {
		t.Fatalf("CreateDecision failed: %v", err)
	}

	// Attempt to create another decision claiming D1 must fail with collision error
	_, err = s.CreateDecision(&Decision{
		ProjectID: projectID,
		Address:   "D1",
		Title:     "Colliding decision",
		Options: []DecisionOption{
			{Letter: "A", Title: "Opt 1"},
			{Letter: "B", Title: "Opt 2"},
		},
		Recommendation: "A",
	})
	if err == nil || !strings.Contains(err.Error(), "already allocated (collision detected)") {
		t.Fatalf("expected collision error, got: %v", err)
	}

	// Concurrent creation stress-test: 10 goroutines creating decisions simultaneously
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, cErr := s.CreateDecision(&Decision{
				ProjectID: projectID,
				Title:     "Concurrent decision",
				Options: []DecisionOption{
					{Letter: "A", Title: "Opt A"},
					{Letter: "B", Title: "Opt B"},
				},
				Recommendation: "A",
			})
			if cErr != nil {
				errs <- cErr
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for cErr := range errs {
		t.Fatalf("unexpected concurrent creation failure: %v", cErr)
	}

	// All 11 decisions must have unique sequential addresses D1..D11
	decisions, err := s.ListDecisions(DecisionFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListDecisions failed: %v", err)
	}
	if len(decisions) != 11 {
		t.Fatalf("expected 11 decisions, got %d", len(decisions))
	}

	anomalies, err := s.CheckDecisionCollisions(projectID)
	if err != nil {
		t.Fatalf("CheckDecisionCollisions failed: %v", err)
	}
	if len(anomalies) != 0 {
		t.Fatalf("anomalies detected: %v", anomalies)
	}
}

func TestDecisions_PreferencesLinkage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "pref_link_test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	projectID := "proj-pref-link"

	// Create decision D1
	d1, err := s.CreateDecision(&Decision{
		ProjectID: projectID,
		Title:     "Database Driver",
		Options: []DecisionOption{
			{Letter: "A", Title: "Driver A"},
			{Letter: "B", Title: "Driver B"},
		},
		Recommendation: "A",
	})
	if err != nil {
		t.Fatalf("CreateDecision failed: %v", err)
	}

	// Save preference linked to D1
	pref := &Preference{
		ProjectID:       projectID,
		ContextQuery:    "driver comparison",
		CandidateA:      "Driver A",
		CandidateB:      "Driver B",
		Winner:          "A",
		Rationale:       "Driver A is faster",
		Agent:           "@antigravity",
		DecisionAddress: d1.Address,
	}
	if err := s.SavePreference(pref); err != nil {
		t.Fatalf("SavePreference failed: %v", err)
	}

	prefs, err := s.Preferences(PreferenceFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("Preferences query failed: %v", err)
	}
	if len(prefs) != 1 {
		t.Fatalf("expected 1 preference, got %d", len(prefs))
	}
	if prefs[0].DecisionAddress != "D1" {
		t.Fatalf("expected DecisionAddress D1, got %q", prefs[0].DecisionAddress)
	}
}

func TestDecisionDeferAndResolveWithQuote(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "defer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	d, err := s.CreateDecision(&Decision{
		ProjectID: "p", Title: "Choose worker mode", CreatedBy: "agent",
		Options:        []DecisionOption{{Letter: "A", Title: "Enable"}, {Letter: "B", Title: "Disable"}},
		Recommendation: "A",
	})
	if err != nil {
		t.Fatal(err)
	}
	deferred, err := s.DeferDecision("p", d.Address, "waiting on user", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if deferred.Status != "deferred" || deferred.Resolution != "" {
		t.Fatalf("deferred = status %q resolution %q, want deferred and empty", deferred.Status, deferred.Resolution)
	}
	resolved, err := s.ResolveDecisionWithOptions("p", d.Address, "A", "done", "agent", "User said choose A", "route via worker", true)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "resolved" || resolved.UserQuote != "User said choose A" || resolved.RouteNote != "route via worker" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestDecisionRequireQuoteRejectsEmptyQuote(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "require-quote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	d, err := s.CreateDecision(&Decision{
		ProjectID: "p", Title: "Choose worker mode", CreatedBy: "agent",
		Options:        []DecisionOption{{Letter: "A", Title: "Enable"}, {Letter: "B", Title: "Disable"}},
		Recommendation: "A",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveDecisionWithOptions("p", d.Address, "A", "", "agent", "", "", true); err == nil {
		t.Fatal("expected empty user quote to be rejected")
	} else if !strings.Contains(err.Error(), "requires a verbatim user quote") {
		t.Fatalf("error = %q", err.Error())
	}
	current, err := s.GetDecision("p", d.Address)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "open" {
		t.Fatalf("decision mutated to %q after rejected resolve", current.Status)
	}
}

func TestDecisionDeferredRejectsNonEmptyResolutionAnomaly(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "deferred-anomaly.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.CreateDecision(&Decision{
		ProjectID: "p", Title: "Anomalous deferred decision", CreatedBy: "agent",
		Options:        []DecisionOption{{Letter: "A", Title: "Enable"}, {Letter: "B", Title: "Disable"}},
		Recommendation: "A", Status: "deferred", Resolution: "A",
	}); err != nil {
		t.Fatal(err)
	}
	anomalies, err := s.CheckDecisionCollisions("p")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, anomaly := range anomalies {
		if strings.Contains(anomaly, "deferred but has resolution") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("anomalies = %v, want deferred resolution anomaly", anomalies)
	}
}
