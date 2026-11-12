package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

func seedLifecycleMemory(t *testing.T, st *Store, project, title, summary string) *MemoryRecord {
	t.Helper()
	env := testMemoryEnvelope(project, title, summary)
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), project, env, Provenance{})
	if err != nil {
		t.Fatalf("save memory %q: %v", title, err)
	}
	return rec
}

func seedSupersedingMemory(t *testing.T, st *Store, project, title, summary, targetID string) *MemoryRecord {
	t.Helper()
	env := testMemoryEnvelope(project, title, summary)
	env.Relations = []MemoryRelation{{Type: MemoryRelationSupersedes, TargetMemoryID: targetID}}
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), project, env, Provenance{})
	if err != nil {
		t.Fatalf("save superseding memory %q: %v", title, err)
	}
	return rec
}

func TestMemoryLifecycleTypes(t *testing.T) {
	if MemoryStateCurrent != "current" || MemoryStateArchived != "archived" || MemoryStateSuperseded != "superseded" {
		t.Fatalf("state constants changed: %q %q %q", MemoryStateCurrent, MemoryStateArchived, MemoryStateSuperseded)
	}
	if len(ArchiveReasonValues()) != 7 || len(ArchiveAuthorityValues()) != 3 {
		t.Fatalf("reason/authority vocabularies changed")
	}
	cases := []struct {
		name      string
		authority ArchiveAuthority
		reason    ArchiveReason
		note      string
		duplicate string
		code      string
	}{
		{"valid user request", ArchiveAuthorityUserRequest, ArchiveReasonWrong, "", "", ""},
		{"valid agent outdated", ArchiveAuthorityAgentVerified, ArchiveReasonOutdated, "", "", ""},
		{"valid sensitive", ArchiveAuthoritySensitive, ArchiveReasonSensitive, "", "", ""},
		{"missing authority", "", ArchiveReasonWrong, "", "", MemoryCodeArchiveAuthority},
		{"unknown reason", ArchiveAuthorityUserRequest, "because", "", "", MemoryCodeArchiveAuthority},
		{"sensitive mismatch", ArchiveAuthorityUserRequest, ArchiveReasonSensitive, "", "", MemoryCodeArchiveAuthority},
		{"other without note", ArchiveAuthorityAgentVerified, ArchiveReasonOther, "  ", "", MemoryCodeArchiveNote},
		{"duplicate without target", ArchiveAuthorityAgentVerified, ArchiveReasonDuplicate, "", "", MemoryCodeDuplicateTarget},
		{"target without duplicate reason", ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "", "mem_other", MemoryCodeDuplicateTarget},
		{"note over bound", ArchiveAuthorityAgentVerified, ArchiveReasonWrong, string(make([]byte, MemoryArchiveMaxNote+1)), "", MemoryCodeArchiveNote},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArchiveInput(tc.authority, tc.reason, tc.note, tc.duplicate)
			if got := MemoryCode(err); got != tc.code {
				t.Fatalf("code = %q, want %q (err %v)", got, tc.code, err)
			}
		})
	}
}

func TestArchiveMemory(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Archive me", "Archive me summary")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	archived, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "wrong fact", "agent", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if archived.State != string(MemoryStateArchived) || archived.Authority != string(ArchiveAuthorityAgentVerified) ||
		archived.Reason != string(ArchiveReasonWrong) || archived.Actor != "agent" || archived.Note != "wrong fact" {
		t.Fatalf("archive record: %+v", archived)
	}
	if archived.ArchiveID == "" || !archived.AgentRestorable || archived.RestoreDeadline == "" {
		t.Fatalf("archive identity/deadline: %+v", archived)
	}
	deadline, err := time.Parse(time.RFC3339Nano, archived.RestoreDeadline)
	if err != nil || !deadline.Equal(now.Add(MemoryArchiveUndoWindow)) {
		t.Fatalf("deadline = %q err=%v", archived.RestoreDeadline, err)
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.State != string(MemoryStateArchived) {
		t.Fatalf("stored state = %+v err=%v", loaded, err)
	}
	if loaded.UpdatedAt == rec.UpdatedAt {
		t.Fatal("updated_at was not advanced")
	}
	events, err := st.lifecycleEvents(context.Background(), rec.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v err=%v", events, err)
	}
	if events[0].Action != MemoryActionArchive || events[0].FromState != "current" || events[0].ToState != "archived" {
		t.Fatalf("archive event: %+v", events[0])
	}
	if events[0].PrevHash != "" || events[0].EventHash == "" {
		t.Fatalf("first event chain fields: %+v", events[0])
	}
}

func TestArchiveDuplicateRequiresTarget(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Duplicate", "Duplicate summary")
	_, err := st.ArchiveMemory(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonDuplicate, "", "agent", "")
	if MemoryCode(err) != MemoryCodeDuplicateTarget {
		t.Fatalf("missing target code = %q", MemoryCode(err))
	}
	_, err = st.ArchiveMemory(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonDuplicate, "", "agent", rec.ID)
	if MemoryCode(err) != MemoryCodeDuplicateTarget {
		t.Fatalf("self target code = %q", MemoryCode(err))
	}
	_, err = st.ArchiveMemory(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonDuplicate, "", "agent", "mem_missing")
	if MemoryCode(err) != MemoryCodeDuplicateTarget {
		t.Fatalf("missing memory code = %q", MemoryCode(err))
	}
	other := seedLifecycleMemory(t, st, "other", "Other project", "Other project summary")
	_, err = st.ArchiveMemory(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonDuplicate, "", "agent", other.ID)
	if MemoryCode(err) != MemoryCodeDuplicateTarget {
		t.Fatalf("cross-project target code = %q", MemoryCode(err))
	}
	target := seedLifecycleMemory(t, st, "proj", "Target", "Target summary")
	archived, err := st.ArchiveMemory(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonDuplicate, "", "agent", target.ID)
	if err != nil || archived.Reason != string(ArchiveReasonDuplicate) {
		t.Fatalf("valid duplicate archive: %+v err=%v", archived, err)
	}
}

func TestArchiveInvalidAuthority(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Authority", "Authority summary")
	cases := []struct {
		authority ArchiveAuthority
		reason    ArchiveReason
		code      string
	}{
		{ArchiveAuthoritySensitive, ArchiveReasonWrong, MemoryCodeArchiveAuthority},
		{ArchiveAuthorityUserRequest, ArchiveReasonSensitive, MemoryCodeArchiveAuthority},
		{"root", ArchiveReasonWrong, MemoryCodeArchiveAuthority},
		{ArchiveAuthorityAgentVerified, "because", MemoryCodeArchiveAuthority},
		{ArchiveAuthorityAgentVerified, ArchiveReasonOther, MemoryCodeArchiveNote},
	}
	for _, tc := range cases {
		if _, err := st.ArchiveMemory(context.Background(), "proj", rec.ID, tc.authority, tc.reason, "", "agent", ""); MemoryCode(err) != tc.code {
			t.Fatalf("%s/%s code = %q, want %q", tc.authority, tc.reason, MemoryCode(err), tc.code)
		}
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.State != string(MemoryStateCurrent) {
		t.Fatalf("state changed after rejected archive: %+v err=%v", loaded, err)
	}
	events, _ := st.lifecycleEvents(context.Background(), rec.ID)
	if len(events) != 0 {
		t.Fatalf("rejected archive wrote events: %+v", events)
	}
}

func TestRestoreMemory(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Restore me", "Restore me summary")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	archived, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonOutdated, "old", "agent", "", now)
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "user", ArchiveAuthorityUserRequest, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Restored || result.State != string(MemoryStateCurrent) || result.MemoryID != rec.ID {
		t.Fatalf("restore result: %+v", result)
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.State != string(MemoryStateCurrent) {
		t.Fatalf("state after restore: %+v err=%v", loaded, err)
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "user", ArchiveAuthorityUserRequest, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("idempotent restore: %v", err)
	}
	events, _ := st.lifecycleEvents(context.Background(), rec.ID)
	if len(events) != 2 || events[1].Action != MemoryActionRestore || events[1].FromState != "archived" || events[1].ToState != "current" {
		t.Fatalf("restore events: %+v", events)
	}
	if events[1].PrevHash != events[0].EventHash {
		t.Fatalf("restore event is not chain-linked")
	}
	if archived.ArchiveID == "" {
		t.Fatal("archive id missing")
	}
}

func TestRestoreByArchiveID(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Archive id", "Archive id summary")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	archived, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityUserRequest, ArchiveReasonUserRequested, "", "user", "", now)
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.RestoreMemory(context.Background(), "proj", archived.ArchiveID, "user", ArchiveAuthorityUserRequest, now)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Restored || result.MemoryID != rec.ID {
		t.Fatalf("restore by archive id: %+v", result)
	}
}

func TestRestoreWindow(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Window", "Window summary")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	if _, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "", "agent", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "agent", ArchiveAuthorityAgentVerified, now.Add(23*time.Hour)); err != nil {
		t.Fatalf("agent restore inside window: %v", err)
	}
	if _, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "", "agent", "", now); err != nil {
		t.Fatal(err)
	}
	_, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "agent", ArchiveAuthorityAgentVerified, now.Add(25*time.Hour))
	if MemoryCode(err) != MemoryCodeRestoreWindow {
		t.Fatalf("expired agent restore code = %q", MemoryCode(err))
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "user", ArchiveAuthorityUserRequest, now.Add(25*time.Hour)); err != nil {
		t.Fatalf("user restore after window: %v", err)
	}
}

func TestRestoreSensitiveRefused(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Secret", "Secret summary")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	archived, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthoritySensitive, ArchiveReasonSensitive, "contains a secret", "agent", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Sensitive || archived.AgentRestorable {
		t.Fatalf("sensitive archive policy: %+v", archived)
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "agent", ArchiveAuthorityAgentVerified, now); MemoryCode(err) != MemoryCodeSensitiveRestore {
		t.Fatalf("agent sensitive restore code = %q", MemoryCode(err))
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "user", ArchiveAuthorityUserRequest, now); err != nil {
		t.Fatalf("user sensitive restore: %v", err)
	}
}

func TestRestoreIdempotent(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Idempotent", "Idempotent summary")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	result, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "user", ArchiveAuthorityUserRequest, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Restored || result.State != string(MemoryStateCurrent) {
		t.Fatalf("already-current restore: %+v", result)
	}
	if _, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "", "agent", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "user", ArchiveAuthorityUserRequest, now); err != nil {
		t.Fatal(err)
	}
	result, err = st.RestoreMemory(context.Background(), "proj", rec.ID, "user", ArchiveAuthorityUserRequest, now)
	if err != nil || result.Restored {
		t.Fatalf("second restore: %+v err=%v", result, err)
	}
}

func TestSupersedeMemory(t *testing.T) {
	st := openMemoryStore(t)
	oldRec := seedLifecycleMemory(t, st, "proj", "Old", "Old superseded summary")
	newRec := seedSupersedingMemory(t, st, "proj", "New", "New current summary", oldRec.ID)
	record, err := st.SupersedeMemory(context.Background(), "proj", oldRec.ID, newRec.ID, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != string(MemoryStateSuperseded) || record.Reason != string(ArchiveReasonSuperseded) {
		t.Fatalf("supersede record: %+v", record)
	}
	loaded, err := st.MemoryRecordByID(oldRec.ID)
	if err != nil || loaded.State != string(MemoryStateSuperseded) {
		t.Fatalf("old state: %+v err=%v", loaded, err)
	}
	newLoaded, err := st.MemoryRecordByID(newRec.ID)
	if err != nil || newLoaded.State != string(MemoryStateCurrent) {
		t.Fatalf("new state: %+v err=%v", newLoaded, err)
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", oldRec.ID, "user", ArchiveAuthorityUserRequest, time.Now().UTC()); MemoryCode(err) != MemoryCodeSupersededRestore {
		t.Fatalf("superseded restore code = %q", MemoryCode(err))
	}
}

func TestSupersedeRequiresRelation(t *testing.T) {
	st := openMemoryStore(t)
	oldRec := seedLifecycleMemory(t, st, "proj", "No relation old", "No relation old summary")
	newRec := seedLifecycleMemory(t, st, "proj", "No relation new", "No relation new summary")
	_, err := st.SupersedeMemory(context.Background(), "proj", oldRec.ID, newRec.ID, "agent")
	if MemoryCode(err) != MemoryCodeRelationTarget {
		t.Fatalf("missing relation code = %q", MemoryCode(err))
	}
}

func TestSupersedeCycle(t *testing.T) {
	st := openMemoryStore(t)
	oldRec := seedLifecycleMemory(t, st, "proj", "Cycle old", "Cycle old summary")
	newRec := seedSupersedingMemory(t, st, "proj", "Cycle new", "Cycle new summary", oldRec.ID)
	if _, err := st.DB.Exec(`INSERT INTO memory_relations(memory_id, relation_type, target_memory_id, evidence_json)
		VALUES(?, 'supersedes', ?, '[]')`, oldRec.ID, newRec.ID); err != nil {
		t.Fatal(err)
	}
	_, err := st.SupersedeMemory(context.Background(), "proj", oldRec.ID, newRec.ID, "agent")
	if MemoryCode(err) != MemoryCodeRelationCycle {
		t.Fatalf("cycle code = %q", MemoryCode(err))
	}
}

func TestSupersededRestoreRefused(t *testing.T) {
	st := openMemoryStore(t)
	oldRec := seedLifecycleMemory(t, st, "proj", "Terminal old", "Terminal old summary")
	newRec := seedSupersedingMemory(t, st, "proj", "Terminal new", "Terminal new summary", oldRec.ID)
	if _, err := st.SupersedeMemory(context.Background(), "proj", oldRec.ID, newRec.ID, "agent"); err != nil {
		t.Fatal(err)
	}
	_, err := st.RestoreMemory(context.Background(), "proj", oldRec.ID, "user", ArchiveAuthorityUserRequest, time.Now().UTC())
	if MemoryCode(err) != MemoryCodeSupersededRestore {
		t.Fatalf("superseded restore code = %q", MemoryCode(err))
	}
}

func TestLifecycleHashChain(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Chain", "Chain summary")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	if _, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "", "agent", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "user", ArchiveAuthorityUserRequest, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityUserRequest, ArchiveReasonWrong, "", "user", "", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	history, err := st.VerifyMemoryHistory(context.Background(), "proj", rec.ID)
	if err != nil || !history.Verified || len(history.Events) != 3 {
		t.Fatalf("history: %+v err=%v", history, err)
	}
	prev := ""
	for _, event := range history.Events {
		if event.PrevHash != prev {
			t.Fatalf("prev_hash %q != %q", event.PrevHash, prev)
		}
		prev = event.EventHash
	}
}

func TestLifecycleTamperDetection(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Tamper", "Tamper summary")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	archived, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "clean", "agent", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE memory_lifecycle SET note='tampered' WHERE event_id=?`, archived.ArchiveID); err != nil {
		t.Fatal(err)
	}
	history, err := st.VerifyMemoryHistory(context.Background(), "proj", rec.ID)
	if MemoryCode(err) != MemoryCodeChainBroken {
		t.Fatalf("tamper code = %q", MemoryCode(err))
	}
	if history.Verified || history.BrokenEventID != archived.ArchiveID {
		t.Fatalf("tamper history: %+v", history)
	}
	diags, err := st.MemoryDiagnostics()
	if err != nil || diags.BrokenLifecycleChains != 1 {
		t.Fatalf("diagnostics broken chains = %d err=%v", diags.BrokenLifecycleChains, err)
	}
}

func TestLifecycleConcurrency(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Concurrent", "Concurrent summary")
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = st.archiveMemoryCASAt(context.Background(), rec, MemoryStateCurrent, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "", "agent", "", now)
		}(i)
	}
	close(start)
	wg.Wait()
	winners, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			winners++
		case MemoryCode(err) == MemoryCodeStateConflict:
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d errs=%v", winners, conflicts, errs)
	}
	events, _ := st.lifecycleEvents(context.Background(), rec.ID)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestLifecycleRollback(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Rollback", "Rollback summary")
	if _, err := st.ArchiveMemory(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonDuplicate, "", "agent", "mem_missing"); MemoryCode(err) != MemoryCodeDuplicateTarget {
		t.Fatalf("duplicate target code = %q", MemoryCode(err))
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.State != string(MemoryStateCurrent) {
		t.Fatalf("state after failed archive: %+v err=%v", loaded, err)
	}
	events, _ := st.lifecycleEvents(context.Background(), rec.ID)
	if len(events) != 0 {
		t.Fatalf("failed archive left events: %+v", events)
	}
	if _, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "", "agent", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "agent", ArchiveAuthoritySensitive, time.Now().UTC()); MemoryCode(err) != MemoryCodeArchiveAuthority {
		t.Fatalf("bad restore authority code = %q", MemoryCode(err))
	}
	loaded, _ = st.MemoryRecordByID(rec.ID)
	if loaded.State != string(MemoryStateArchived) {
		t.Fatalf("failed restore changed state: %+v", loaded)
	}
}

func TestLifecycleProjectMismatch(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Project check", "Project check summary")
	if _, err := st.ArchiveMemory(context.Background(), "other", rec.ID, ArchiveAuthorityUserRequest, ArchiveReasonUserRequested, "", "user", ""); MemoryCode(err) != MemoryCodeArchiveProject {
		t.Fatalf("project mismatch code = %q", MemoryCode(err))
	}
}

func TestArchiveStoreListAndStats(t *testing.T) {
	st := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	first := seedLifecycleMemory(t, st, "proj", "First", "First archived summary")
	second := seedLifecycleMemory(t, st, "proj", "Second", "Second archived summary")
	old := seedLifecycleMemory(t, st, "proj", "Old", "Old superseded list summary")
	newRec := seedSupersedingMemory(t, st, "proj", "New", "New current list summary", old.ID)
	if _, err := st.archiveMemoryAt(context.Background(), "proj", first.ID, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "", "agent", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.archiveMemoryAt(context.Background(), "proj", second.ID, ArchiveAuthorityUserRequest, ArchiveReasonOutdated, "", "user", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupersedeMemory(context.Background(), "proj", old.ID, newRec.ID, "agent"); err != nil {
		t.Fatal(err)
	}
	records, err := st.ListArchivedMemoriesAt("proj", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3: %+v", len(records), records)
	}
	stats := st.ComputeArchiveStats(records, now)
	if stats.Archived != 2 || stats.Superseded != 1 || stats.Restorable != 2 || stats.AgentRestorable != 2 || !stats.ChainHealthy {
		t.Fatalf("stats: %+v", stats)
	}
	if stats.ByReason["wrong"] != 1 || stats.ByAuthority["user_request"] != 2 {
		t.Fatalf("grouped stats: %+v", stats)
	}
}

func TestArchivedMemoryExcludedFromListAndSearch(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Hidden needle", "Hidden needle summary")
	if _, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityUserRequest, ArchiveReasonOutdated, "", "user", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	records, err := st.ListMemoryRecords(MemoryListOptions{ProjectID: "proj", Limit: 10})
	if err != nil || len(records) != 0 {
		t.Fatalf("default list = %+v err=%v", records, err)
	}
	records, err = st.ListMemoryRecords(MemoryListOptions{ProjectID: "proj", Limit: 10, IncludeArchived: true})
	if err != nil || len(records) != 1 || records[0].State != string(MemoryStateArchived) {
		t.Fatalf("include-archived list = %+v err=%v", records, err)
	}
	hits, err := st.SearchMemoryRecords("needle", "proj", 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("default search = %+v err=%v", hits, err)
	}
	hits, err = st.SearchMemoryRecordsIncludingArchived("needle", "proj", 10, true)
	if err != nil || len(hits) != 1 {
		t.Fatalf("include-archived search = %+v err=%v", hits, err)
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "user", ArchiveAuthorityUserRequest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	hits, err = st.SearchMemoryRecords("needle", "proj", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("restored search = %+v err=%v", hits, err)
	}
}

func TestHardDeleteRefused(t *testing.T) {
	err := RefuseMemoryHardDelete()
	if MemoryCode(err) != MemoryCodeHardDeleteRefused {
		t.Fatalf("code = %q", MemoryCode(err))
	}
}

func TestLifecycleMissingMemory(t *testing.T) {
	st := openMemoryStore(t)
	if _, err := st.ArchiveMemory(context.Background(), "proj", "mem_missing", ArchiveAuthorityUserRequest, ArchiveReasonWrong, "", "user", ""); MemoryCode(err) != MemoryCodeBound {
		t.Fatalf("missing archive code = %q", MemoryCode(err))
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", "mem_missing", "user", ArchiveAuthorityUserRequest, time.Now().UTC()); MemoryCode(err) != MemoryCodeBound {
		t.Fatalf("missing restore code = %q", MemoryCode(err))
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", "mle_missing", "user", ArchiveAuthorityUserRequest, time.Now().UTC()); MemoryCode(err) != MemoryCodeBound {
		t.Fatalf("missing archive id code = %q", MemoryCode(err))
	}
}

func TestLifecycleSQLRollsBackOnEventFailure(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedLifecycleMemory(t, st, "proj", "Tx rollback", "Tx rollback summary")
	if _, err := st.DB.Exec(`CREATE TRIGGER fail_lifecycle BEFORE INSERT ON memory_lifecycle BEGIN SELECT RAISE(ABORT, 'boom'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.archiveMemoryAt(context.Background(), "proj", rec.ID, ArchiveAuthorityAgentVerified, ArchiveReasonWrong, "", "agent", "", time.Now().UTC()); err == nil {
		t.Fatal("expected event insert failure")
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.State != string(MemoryStateCurrent) {
		t.Fatalf("state after event failure: %+v err=%v", loaded, err)
	}
	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM memory_lifecycle WHERE memory_id=?`, rec.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("events after rollback = %d", count)
	}
}

func TestLifecycleForeignKeysNotRequired(t *testing.T) {
	st := openMemoryStore(t)
	var fk int
	if err := st.DB.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`SELECT COUNT(*) FROM memory_lifecycle`); err != nil {
		t.Fatalf("lifecycle table unreadable: %v", err)
	}
}
