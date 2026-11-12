package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func feedbackClock() time.Time {
	return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
}

func seedFeedbackMemory(t *testing.T, st *Store, project, title string) *MemoryRecord {
	t.Helper()
	env := testMemoryEnvelope(project, title, title+" summary")
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), project, env, Provenance{})
	if err != nil {
		t.Fatalf("save memory %q: %v", title, err)
	}
	return rec
}

func recordFeedbackRetrieval(t *testing.T, st *Store, project, query string, now time.Time, memories ...*MemoryRecord) string {
	t.Helper()
	items := make([]MemoryRetrievalItem, 0, len(memories))
	for i, rec := range memories {
		items = append(items, MemoryRetrievalItem{MemoryID: rec.ID, Rank: i + 1, Kind: rec.Kind, Score: 500})
	}
	id, err := st.RecordRetrievalAt(context.Background(), project, "agent", "", query, items, now)
	if err != nil {
		t.Fatalf("record retrieval: %v", err)
	}
	return id
}

func recordFeedback(t *testing.T, st *Store, in MemoryFeedbackInput, now time.Time) *MemoryFeedbackOutcome {
	t.Helper()
	out, err := st.RecordMemoryFeedbackAt(context.Background(), in, now)
	if err != nil {
		t.Fatalf("record feedback %+v: %v", in, err)
	}
	return out
}

func TestMemoryFeedbackTypes(t *testing.T) {
	want := []string{"helped", "verified", "less_important", "irrelevant", "wrong"}
	got := MemoryFeedbackSignalValues()
	if len(got) != len(want) {
		t.Fatalf("signals = %v, want %v", got, want)
	}
	for i, sig := range want {
		if got[i] != sig {
			t.Fatalf("signal %d = %q, want %q", i, got[i], sig)
		}
		parsed, ok := ParseMemoryFeedbackSignal(sig)
		if !ok || string(parsed) != sig {
			t.Fatalf("parse %q = %q,%v", sig, parsed, ok)
		}
	}
	for _, invalid := range []string{"", "helpful", "HELPED", "useful"} {
		if _, ok := ParseMemoryFeedbackSignal(invalid); ok {
			t.Fatalf("parse accepted invalid signal %q", invalid)
		}
	}
	weights := map[MemoryFeedbackSignal]float64{
		MemoryFeedbackHelped: 40, MemoryFeedbackVerified: 60,
		MemoryFeedbackLessImportant: -20, MemoryFeedbackIrrelevant: -40,
		MemoryFeedbackWrong: -80,
	}
	for sig, want := range weights {
		got, ok := MemoryFeedbackBaseWeight(sig)
		if !ok || got != want {
			t.Fatalf("weight %q = %v,%v want %v", sig, got, ok, want)
		}
	}
}

func TestRecordRetrieval(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Retrieval record needle")

	id := recordFeedbackRetrieval(t, st, "proj", "retrieval record needle", now, rec)
	if !strings.HasPrefix(id, "ret_") {
		t.Fatalf("retrieval id = %q", id)
	}
	row, err := st.MemoryRetrievalByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if row.ProjectID != "proj" || row.ItemCount != 1 || row.Agent != "agent" {
		t.Fatalf("retrieval row: %+v", row)
	}
	expires, err := time.Parse(time.RFC3339, row.ExpiresAt)
	if err != nil || !expires.After(now.Add(29*24*time.Hour)) || !expires.Before(now.Add(31*24*time.Hour)) {
		t.Fatalf("retrieval expiry = %s err=%v", row.ExpiresAt, err)
	}
	items, err := st.MemoryRetrievalItems(id)
	if err != nil || len(items) != 1 || items[0].MemoryID != rec.ID || items[0].Rank != 1 {
		t.Fatalf("retrieval items: %+v err=%v", items, err)
	}
}

func TestRecordRetrievalEmptyPack(t *testing.T) {
	st := openMemoryStore(t)
	id, err := st.RecordRetrievalAt(context.Background(), "proj", "agent", "session-1", "empty result query", nil, feedbackClock())
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.MemoryRetrievalByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if row.ItemCount != 0 {
		t.Fatalf("empty retrieval item_count = %d", row.ItemCount)
	}
	items, err := st.MemoryRetrievalItems(id)
	if err != nil || len(items) != 0 {
		t.Fatalf("empty retrieval items = %+v err=%v", items, err)
	}
}

func TestRecordRetrievalNoQueryText(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Privacy needle")
	const query = "SENSITIVE-QUERY-TEXT-DO-NOT-STORE"
	id := recordFeedbackRetrieval(t, st, "proj", query, now, rec)

	for _, table := range []string{"memory_retrievals", "memory_retrieval_items"} {
		columns := tableColumns(t, st, table)
		rows, err := st.DB.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			for _, value := range values {
				text := fmt.Sprint(value)
				if strings.Contains(text, "SENSITIVE-QUERY") || strings.Contains(text, query) {
					_ = rows.Close()
					t.Fatalf("%s stores query text in %q", table, text)
				}
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}

	wantColumns := map[string][]string{
		"memory_retrievals":      {"id", "project_id", "agent", "session_ref", "query_hash", "item_count", "created_at", "expires_at"},
		"memory_retrieval_items": {"retrieval_id", "memory_id", "rank", "kind", "score"},
	}
	for table, want := range wantColumns {
		got := tableColumns(t, st, table)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s columns = %v, want %v", table, got, want)
		}
	}
	row, err := st.MemoryRetrievalByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.QueryHash) != 64 || row.QueryHash == query {
		t.Fatalf("query hash = %q", row.QueryHash)
	}
}

func TestFeedbackBinding(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	returned := seedFeedbackMemory(t, st, "proj", "Returned memory")
	notReturned := seedFeedbackMemory(t, st, "proj", "Hidden memory")
	retrieval := recordFeedbackRetrieval(t, st, "proj", "binding query", now, returned)

	out := recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: returned.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackHelped, Agent: "agent-a",
	}, now)
	if !out.Created || out.Feedback.Weight != 40 {
		t.Fatalf("bound feedback outcome: %+v", out)
	}

	_, err := st.RecordMemoryFeedbackAt(context.Background(), MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: notReturned.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackHelped, Agent: "agent-a",
	}, now)
	if code := MemoryCode(err); code != MemoryCodeFeedbackNotReturned {
		t.Fatalf("not returned error = %v (%q)", err, code)
	}

	expired := recordFeedbackRetrieval(t, st, "proj", "expired query", now.Add(-31*24*time.Hour), returned)
	_, err = st.RecordMemoryFeedbackAt(context.Background(), MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: returned.ID, RetrievalID: expired,
		Signal: MemoryFeedbackHelped, Agent: "agent-a",
	}, now)
	if code := MemoryCode(err); code != MemoryCodeFeedbackExpired {
		t.Fatalf("expired error = %v (%q)", err, code)
	}

	_, err = st.RecordMemoryFeedbackAt(context.Background(), MemoryFeedbackInput{
		ProjectID: "other-proj", MemoryID: returned.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackHelped, Agent: "agent-a",
	}, now)
	if code := MemoryCode(err); code != MemoryCodeFeedbackProject {
		t.Fatalf("project mismatch error = %v (%q)", err, code)
	}
}

func TestFeedbackUnbound(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Unbound memory")

	out := recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, Signal: MemoryFeedbackHelped,
		Agent: "agent-a", Unbound: true,
	}, now)
	if !out.Feedback.Unbound || out.Feedback.RetrievalID != "" {
		t.Fatalf("unbound row: %+v", out.Feedback)
	}
	if out.Feedback.Weight != 20 {
		t.Fatalf("unbound helped weight = %v, want 20", out.Feedback.Weight)
	}
	if out.Adjustment.Adjustment != 20 {
		t.Fatalf("unbound adjustment = %v, want 20", out.Adjustment.Adjustment)
	}

	negative := recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, Signal: MemoryFeedbackLessImportant,
		Agent: "agent-b", Unbound: true,
	}, now)
	if negative.Feedback.Weight != -10 {
		t.Fatalf("unbound less_important weight = %v, want -10", negative.Feedback.Weight)
	}

	_, err := st.RecordMemoryFeedbackAt(context.Background(), MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, Signal: MemoryFeedbackHelped, Agent: "agent-c",
	}, now)
	if code := MemoryCode(err); code != MemoryCodeFeedbackNotReturned {
		t.Fatalf("missing retrieval error = %v (%q)", err, code)
	}

	_, err = st.RecordMemoryFeedbackAt(context.Background(), MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: "ret_x", Signal: MemoryFeedbackHelped,
		Agent: "agent-c", Unbound: true,
	}, now)
	if code := MemoryCode(err); code != MemoryCodeFeedbackBound {
		t.Fatalf("bound/unbound conflict error = %v (%q)", err, code)
	}
}

func TestFeedbackNotePrivacy(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Note memory")

	cases := []struct {
		name string
		note string
		code string
	}{
		{"too long", strings.Repeat("a", MemoryFeedbackMaxNote+1), MemoryCodeFeedbackNoteLimit},
		{"token", "token=abcdefghijklmnop", MemoryCodeFeedbackSecret},
		{"api key", "sk-abcdefghijklmnopqrstuvwxyz", MemoryCodeFeedbackSecret},
		{"private key", "-----BEGIN " + "RSA PRIVATE KEY-----", MemoryCodeFeedbackSecret},
		{"home path", "see ~/notes/secret.md", MemoryCodeFeedbackSecret},
		{"absolute home", "see /home/user/notes.md", MemoryCodeFeedbackSecret},
		{"absolute path", "see /etc/passwd", MemoryCodeFeedbackSecret},
		{"windows path", `see C:\Users\jad\notes.md`, MemoryCodeFeedbackSecret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.RecordMemoryFeedbackAt(context.Background(), MemoryFeedbackInput{
				ProjectID: "proj", MemoryID: rec.ID, Signal: MemoryFeedbackHelped,
				Agent: "agent-a", Unbound: true, Note: tc.note,
			}, now)
			if code := MemoryCode(err); code != tc.code {
				t.Fatalf("note %q error = %v (%q), want %q", tc.note, err, code, tc.code)
			}
		})
	}

	clean := recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, Signal: MemoryFeedbackHelped,
		Agent: "agent-a", Unbound: true, Note: strings.Repeat("a", MemoryFeedbackMaxNote),
	}, now)
	if clean.Feedback.Note == "" {
		t.Fatal("clean max-length note was dropped")
	}
}

func TestFeedbackIdempotency(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Idempotent memory")
	retrieval := recordFeedbackRetrieval(t, st, "proj", "idempotent query", now, rec)
	in := MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackHelped, Agent: "agent-a",
	}
	first := recordFeedback(t, st, in, now)
	if !first.Created {
		t.Fatal("first feedback was not created")
	}
	second := recordFeedback(t, st, in, now)
	if second.Created {
		t.Fatal("replay created a second row")
	}
	if second.Feedback.ID != first.Feedback.ID {
		t.Fatalf("replay id = %q, want %q", second.Feedback.ID, first.Feedback.ID)
	}
	var active int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM memory_feedback WHERE retrieval_id=? AND memory_id=? AND agent=? AND superseded_by IS NULL`,
		retrieval, rec.ID, "agent-a").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active rows = %d, want 1", active)
	}
}

func TestFeedbackSupersession(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Superseded memory")
	retrieval := recordFeedbackRetrieval(t, st, "proj", "supersede query", now, rec)

	first := recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackHelped, Agent: "agent-a",
	}, now)
	second := recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackWrong, Agent: "agent-a",
	}, now)
	if second.SupersededID != first.Feedback.ID {
		t.Fatalf("superseded id = %q, want %q", second.SupersededID, first.Feedback.ID)
	}
	old, err := st.MemoryFeedbackByID(first.Feedback.ID)
	if err != nil || old.SupersededBy != second.Feedback.ID {
		t.Fatalf("old row = %+v err=%v", old, err)
	}
	if second.Adjustment.Adjustment != -80 || second.RecommendedAction != "archive" {
		t.Fatalf("superseded adjustment = %+v", second.Adjustment)
	}
	if second.Adjustment.Superseded != 1 || second.Adjustment.Accepted != 1 {
		t.Fatalf("counts = %+v", second.Adjustment)
	}

	// A different agent aggregates instead of superseding.
	third := recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackVerified, Agent: "agent-b",
	}, now)
	if third.SupersededID != "" {
		t.Fatalf("cross-agent signal superseded a row: %+v", third)
	}
	if third.Adjustment.Adjustment != -20 {
		t.Fatalf("cross-agent adjustment = %v, want -20", third.Adjustment.Adjustment)
	}
}

func TestFeedbackWeights(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Weight memory")
	retrieval := recordFeedbackRetrieval(t, st, "proj", "weight query", now, rec)

	cases := []struct {
		signal       MemoryFeedbackSignal
		bound, loose float64
	}{
		{MemoryFeedbackHelped, 40, 20},
		{MemoryFeedbackVerified, 60, 30},
		{MemoryFeedbackLessImportant, -20, -10},
		{MemoryFeedbackIrrelevant, -40, -20},
		{MemoryFeedbackWrong, -80, -40},
	}
	for i, tc := range cases {
		bound := recordFeedback(t, st, MemoryFeedbackInput{
			ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
			Signal: tc.signal, Agent: fmt.Sprintf("bound-%d", i),
		}, now)
		if bound.Feedback.Weight != tc.bound {
			t.Fatalf("bound %s weight = %v, want %v", tc.signal, bound.Feedback.Weight, tc.bound)
		}
		loose := recordFeedback(t, st, MemoryFeedbackInput{
			ProjectID: "proj", MemoryID: rec.ID, Signal: tc.signal,
			Agent: fmt.Sprintf("loose-%d", i), Unbound: true,
		}, now)
		if loose.Feedback.Weight != tc.loose {
			t.Fatalf("unbound %s weight = %v, want %v", tc.signal, loose.Feedback.Weight, tc.loose)
		}
	}
}

func TestFeedbackCap(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	positive := seedFeedbackMemory(t, st, "proj", "Positive cap memory")
	negative := seedFeedbackMemory(t, st, "proj", "Negative cap memory")
	retrieval := recordFeedbackRetrieval(t, st, "proj", "cap query", now, positive, negative)

	for i := 0; i < 100; i++ {
		recordFeedback(t, st, MemoryFeedbackInput{
			ProjectID: "proj", MemoryID: positive.ID, RetrievalID: retrieval,
			Signal: MemoryFeedbackVerified, Agent: fmt.Sprintf("agent-%03d", i),
		}, now)
		recordFeedback(t, st, MemoryFeedbackInput{
			ProjectID: "proj", MemoryID: negative.ID, RetrievalID: retrieval,
			Signal: MemoryFeedbackWrong, Agent: fmt.Sprintf("agent-%03d", i),
		}, now)
	}
	adj, err := st.FeedbackAdjustmentAt(context.Background(), "proj", positive.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if adj.Adjustment != MemoryFeedbackWeightCap || adj.Accepted != 100 {
		t.Fatalf("positive adjustment = %+v", adj)
	}
	adj, err = st.FeedbackAdjustmentAt(context.Background(), "proj", negative.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if adj.Adjustment != -MemoryFeedbackWeightCap || adj.Wrong != 100 || adj.RecommendedAction != "archive" {
		t.Fatalf("negative adjustment = %+v", adj)
	}
}

func TestFeedbackDecay(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Decay memory")
	created := now.Add(-90 * 24 * time.Hour)
	retrieval := recordFeedbackRetrieval(t, st, "proj", "decay query", created, rec)
	recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackVerified, Agent: "agent-a",
	}, created)

	adj, err := st.FeedbackAdjustmentAt(context.Background(), "proj", rec.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if diff := adj.Adjustment - 30; diff > 0.01 || diff < -0.01 {
		t.Fatalf("90-day decay = %v, want 30", adj.Adjustment)
	}
}

func TestFeedbackExpiry(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Expired memory")
	recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, Signal: MemoryFeedbackVerified,
		Agent: "agent-a", Unbound: true,
	}, now.Add(-181*24*time.Hour))

	adj, err := st.FeedbackAdjustmentAt(context.Background(), "proj", rec.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if adj.Adjustment != 0 || adj.Accepted != 0 {
		t.Fatalf("expired feedback still counted: %+v", adj)
	}
}

func TestFeedbackNeverMutatesMemory(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Immutable memory")
	before, err := st.MemoryRecordByID(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	retrieval := recordFeedbackRetrieval(t, st, "proj", "immutable query", now, rec)
	out := recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackWrong, Agent: "agent-a",
	}, now)
	if out.RecommendedAction != "archive" {
		t.Fatalf("wrong must recommend archive: %+v", out)
	}
	after, err := st.MemoryRecordByID(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.EnvelopeJSON != before.EnvelopeJSON || after.Digest != before.Digest ||
		after.State != before.State || after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("feedback mutated memory:\nbefore=%+v\nafter=%+v", before, after)
	}
	var assertions int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM memory_assertions WHERE memory_id = ?`, rec.ID).Scan(&assertions); err != nil {
		t.Fatal(err)
	}
	if assertions != 1 {
		t.Fatalf("assertions = %d, want 1", assertions)
	}
	history, err := st.MemoryHistoryEvents(context.Background(), "proj", rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Events) != 0 {
		t.Fatalf("feedback wrote lifecycle events: %+v", history.Events)
	}
}

func TestFeedbackConcurrency(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Concurrent memory")
	retrieval := recordFeedbackRetrieval(t, st, "proj", "concurrent query", now, rec)

	const workers = 8
	var wg sync.WaitGroup
	created := make([]bool, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := st.RecordMemoryFeedbackAt(context.Background(), MemoryFeedbackInput{
				ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
				Signal: MemoryFeedbackHelped, Agent: "agent-a",
			}, now)
			if err != nil {
				errs[i] = err
				return
			}
			created[i] = out.Created
		}(i)
	}
	wg.Wait()
	createdCount := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
		if created[i] {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created rows = %d, want exactly 1", createdCount)
	}
	var rows int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM memory_feedback WHERE memory_id = ?`, rec.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("feedback rows = %d, want 1", rows)
	}
}

func TestFeedbackScaleCap(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Scale memory")
	stamp := now.Add(-24 * time.Hour).Format(time.RFC3339)
	expires := now.Add(24 * time.Hour).Format(time.RFC3339)
	for i := 0; i < 1000; i++ {
		agent := fmt.Sprintf("agent-%03d", i%100)
		retrieval := fmt.Sprintf("ret_scale_%03d", i/100)
		id := fmt.Sprintf("fb_scale_%04d", i)
		if _, err := st.DB.Exec(`INSERT INTO memory_feedback
			(id, project_id, memory_id, retrieval_id, signal, weight, unbound, note, agent,
			 session_ref, created_at, expires_at, superseded_by, metadata_json)
			VALUES(?,?,?,?,?,?,0,NULL,?,NULL,?,?,NULL,'{}')`,
			id, "proj", rec.ID, retrieval, string(MemoryFeedbackHelped), 40, agent, stamp, expires); err != nil {
			t.Fatal(err)
		}
	}
	adj, err := st.FeedbackAdjustmentAt(context.Background(), "proj", rec.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if adj.Adjustment != MemoryFeedbackWeightCap || adj.Accepted != 1000 {
		t.Fatalf("scale adjustment = %+v", adj)
	}
}

func TestFeedbackList(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "List memory")
	retrieval := recordFeedbackRetrieval(t, st, "proj", "list query", now, rec)
	recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackHelped, Agent: "agent-a",
	}, now)
	recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackWrong, Agent: "agent-a",
	}, now)
	recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, Signal: MemoryFeedbackVerified,
		Agent: "agent-b", Unbound: true,
	}, now)

	summary, err := st.ListMemoryFeedback(MemoryFeedbackListOptions{ProjectID: "proj", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 3 || summary.Accepted != 2 || summary.Superseded != 1 ||
		summary.Bound != 2 || summary.Unbound != 1 {
		t.Fatalf("summary counts = %+v", summary)
	}
	if summary.Counts[string(MemoryFeedbackHelped)] != 1 || summary.Counts[string(MemoryFeedbackWrong)] != 1 ||
		summary.Counts[string(MemoryFeedbackVerified)] != 1 {
		t.Fatalf("signal counts = %+v", summary.Counts)
	}
	if len(summary.TopAdjusted) != 1 || summary.TopAdjusted[0].MemoryID != rec.ID {
		t.Fatalf("top adjusted = %+v", summary.TopAdjusted)
	}

	filtered, err := st.ListMemoryFeedback(MemoryFeedbackListOptions{
		ProjectID: "proj", Signal: MemoryFeedbackVerified, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || string(filtered.Feedback[0].Signal) != "verified" {
		t.Fatalf("filtered list = %+v", filtered)
	}
}

func TestFeedbackPrivacySurface(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Surface memory")
	const query = "PRIVATE-QUERY-PROMPT"
	retrieval := recordFeedbackRetrieval(t, st, "proj", query, now, rec)
	recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackWrong, Agent: "agent-a", Note: "contradicted by the current test",
	}, now)

	summary, err := st.ListMemoryFeedback(MemoryFeedbackListOptions{ProjectID: "proj", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), query) || strings.Contains(string(encoded), "PRIVATE-QUERY") {
		t.Fatalf("feedback list leaks the query: %s", encoded)
	}
	diagnostics, err := st.MemoryFeedbackDiagnostics(now)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "contradicted") {
		t.Fatalf("diagnostics leak a note: %s", encoded)
	}
}

func TestFeedbackLedgerCleanupBound(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	stamp := now.Add(-31 * 24 * time.Hour).Format(time.RFC3339)
	for i := 0; i < 150; i++ {
		id := fmt.Sprintf("ret_expired_%03d", i)
		if _, err := st.DB.Exec(`INSERT INTO memory_retrievals
			(id, project_id, agent, session_ref, query_hash, item_count, created_at, expires_at)
			VALUES(?,?,?,NULL,?,?,?,?)`,
			id, "proj", "agent", strings.Repeat("a", 64), 1, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.Exec(`INSERT INTO memory_retrieval_items(retrieval_id, memory_id, rank, kind, score)
			VALUES(?,?,?,?,?)`, id, "mem_expired", 1, "fact", 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.RecordRetrievalAt(context.Background(), "proj", "agent", "", "cleanup", nil, now); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM memory_retrievals WHERE id LIKE 'ret_expired_%'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 50 {
		t.Fatalf("expired retrievals remaining = %d, want 50 after one bounded cleanup", remaining)
	}
	var orphaned int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM memory_retrieval_items i
		WHERE NOT EXISTS(SELECT 1 FROM memory_retrievals r WHERE r.id = i.retrieval_id)`).Scan(&orphaned); err != nil {
		t.Fatal(err)
	}
	if orphaned > 100 {
		t.Fatalf("orphaned items = %d, cleanup is not bounded", orphaned)
	}
}

func TestMemoryFeedbackDiagnostics(t *testing.T) {
	st := openMemoryStore(t)
	now := feedbackClock()
	rec := seedFeedbackMemory(t, st, "proj", "Diagnostics memory")
	retrieval := recordFeedbackRetrieval(t, st, "proj", "diagnostics query", now, rec)
	recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, RetrievalID: retrieval,
		Signal: MemoryFeedbackHelped, Agent: "agent-a",
	}, now)
	if _, err := st.DB.Exec(`INSERT INTO memory_feedback
		(id, project_id, memory_id, retrieval_id, signal, weight, unbound, note, agent,
		 session_ref, created_at, expires_at, superseded_by, metadata_json)
		VALUES('fb_orphan','proj','mem_missing',NULL,'helped',40,0,NULL,'agent',NULL,?,?,NULL,'{}')`,
		now.Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	d, err := st.MemoryFeedbackDiagnostics(now)
	if err != nil {
		t.Fatal(err)
	}
	if d.Retrievals != 1 || d.Items != 1 || d.Feedback != 2 || d.OrphanedFeedback != 1 {
		t.Fatalf("diagnostics = %+v", d)
	}
	report, err := st.Doctor(false)
	if err != nil {
		t.Fatal(err)
	}
	var status CheckStatus
	for _, check := range report.Checks {
		if check.Name == "Memory Relevance Feedback" {
			status = check.Status
		}
	}
	if status != StatusWarn {
		t.Fatalf("doctor feedback status = %q, want warn for orphaned feedback", status)
	}
}

func TestMemoryIDsForFactIDs(t *testing.T) {
	st := openMemoryStore(t)
	rec := seedFeedbackMemory(t, st, "proj", "Fact bridge memory")
	mapping, err := st.MemoryIDsForFactIDs([]string{rec.FactID, "missing-fact"})
	if err != nil {
		t.Fatal(err)
	}
	if mapping[rec.FactID] != rec.ID {
		t.Fatalf("fact mapping = %+v, want %s -> %s", mapping, rec.FactID, rec.ID)
	}
	if _, ok := mapping["missing-fact"]; ok {
		t.Fatalf("unexpected mapping for missing fact: %+v", mapping)
	}
}

func tableColumns(t *testing.T, st *Store, table string) []string {
	t.Helper()
	rows, err := st.DB.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	return columns
}
