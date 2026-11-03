package loaders

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadCodexProjectAttribution(t *testing.T) {
	dir := t.TempDir()
	// sibling sessions dir with a session_meta carrying the session's cwd
	sd := filepath.Join(dir, "sessions")
	os.MkdirAll(sd, 0o755)
	os.WriteFile(filepath.Join(sd, "roll.jsonl"),
		[]byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"s1\",\"cwd\":\"/workspace/dream\"}}\n"), 0o644)
	// history references that session
	hist := filepath.Join(dir, "history.jsonl")
	os.WriteFile(hist, []byte("{\"session_id\":\"s1\",\"ts\":170,\"text\":\"did work\"}\n"), 0o644)

	got := codexSessionCwd(sd)
	if got["s1"] != "/workspace/dream" {
		t.Fatalf("session map wrong: %#v", got)
	}

	msgs, _, err := loadCodex(hist, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].SessionID != "s1" || msgs[0].ProjectDir != "/workspace/dream" {
		t.Fatalf("attribution wrong: %+v", msgs)
	}
	if msgs[0].Role != "user" {
		t.Fatalf("history record role = %q, want user", msgs[0].Role)
	}
}

// codexSessionCwd remains available for direct use; alias the new combined
// scan through it so both entry points stay exercised.
func codexSessionCwd(sessionsRoot string) map[string]string {
	return scanCodexRollouts(sessionsRoot).cwdBySession
}

func TestLoadCodex(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "history.jsonl")
	os.WriteFile(f1, []byte(
		"{\"session_id\":\"a\",\"ts\":170,\"text\":\"first\"}\n"+
			"{\"session_id\":\"a\",\"ts\":175,\"text\":\"\"}\n"+
			"{\"session_id\":\"b\",\"ts\":180,\"text\":\"second\"}\n"), 0o644)

	msgs, wm, err := loadCodex(f1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if wm != "180" {
		t.Fatalf("watermark = %q, want 180", wm)
	}
	if msgs[0].Role != "user" || msgs[1].Role != "user" {
		t.Fatalf("history roles wrong: %+v", msgs)
	}

	// incremental: the equal-timestamp boundary is re-emitted (dedup downstream
	// absorbs it) and strictly older records are skipped
	again, _, _ := loadCodex(f1, "170")
	if len(again) != 2 {
		t.Fatalf("incremental scan wrong: %+v", again)
	}
	if again[0].Content != "first" || again[1].Content != "second" {
		t.Fatalf("incremental content wrong: %+v", again)
	}
}

func TestLoadCodexHistoryRoles(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "history.jsonl")
	os.WriteFile(f, []byte(
		"{\"session_id\":\"a\",\"ts\":170,\"text\":\"please diagnose the payment worker\"}\n"+
			"{\"session_id\":\"a\",\"ts\":171,\"role\":\"assistant\",\"text\":\"explicit role kept\"}\n"+
			"{\"session_id\":\"a\",\"ts\":172,\"role\":\"system\",\"text\":\"system note\"}\n"), 0o644)

	msgs, _, err := loadCodex(f, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" {
		t.Fatalf("record without role loaded as %q, want user", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" || msgs[2].Role != "system" {
		t.Fatalf("explicit roles not preserved: %+v", msgs)
	}
}

func TestLoadCodexRolloutAssistant(t *testing.T) {
	dir := t.TempDir()
	sd := filepath.Join(dir, "sessions")
	os.MkdirAll(sd, 0o755)
	rollout := "{\"type\":\"session_meta\",\"timestamp\":\"2026-09-15T10:00:00Z\",\"payload\":{\"id\":\"s9\",\"cwd\":\"/workspace/atlas\"}}\n" +
		"{\"timestamp\":\"2026-09-15T10:00:05Z\",\"ordinal\":1,\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"user question\"}]}}\n" +
		"{\"timestamp\":\"2026-09-15T10:00:09Z\",\"ordinal\":2,\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"the answer is 42\"}]}}\n" +
		"{\"timestamp\":\"2026-09-15T10:00:12Z\",\"ordinal\":3,\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"developer\",\"content\":[{\"type\":\"input_text\",\"text\":\"developer note\"}]}}\n"
	os.WriteFile(filepath.Join(sd, "rollout-2026-09-15.jsonl"), []byte(rollout), 0o644)
	// history.jsonl may not exist yet; rollouts must stand alone
	hist := filepath.Join(dir, "history.jsonl")
	os.WriteFile(hist, []byte(""), 0o644)

	scan := scanCodexRollouts(sd)
	if len(scan.messages) != 1 {
		t.Fatalf("expected 1 rollout assistant message, got %d: %+v", len(scan.messages), scan.messages)
	}
	m := scan.messages[0]
	if m.Role != "assistant" || m.SessionID != "s9" || m.ProjectDir != "/workspace/atlas" {
		t.Fatalf("rollout provenance wrong: %+v", m)
	}
	if m.Content != "the answer is 42" {
		t.Fatalf("rollout content wrong: %q", m.Content)
	}
	if want := time.Date(2026, 9, 15, 10, 0, 9, 0, time.UTC); !m.TS.Equal(want) {
		t.Fatalf("rollout ts = %v, want %v", m.TS, want)
	}

	msgs, wm, err := loadCodex(hist, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "the answer is 42" {
		t.Fatalf("loadCodex rollout import wrong: %+v", msgs)
	}
	if wm == "" {
		t.Fatal("expected non-empty watermark from rollout timestamps")
	}
}

func TestLoadCodexSameSecondAppend(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "history.jsonl")
	os.WriteFile(f, []byte(
		"{\"session_id\":\"a\",\"ts\":170,\"text\":\"first\"}\n"+
			"{\"session_id\":\"a\",\"ts\":170,\"text\":\"second\"}\n"), 0o644)

	msgs, wm, err := loadCodex(f, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || wm != "170" {
		t.Fatalf("initial load wrong: %d msgs, wm=%q", len(msgs), wm)
	}

	// a distinct record appended later with the same whole-second timestamp
	// as the saved cursor must not be silently dropped
	os.WriteFile(f, []byte(
		"{\"session_id\":\"a\",\"ts\":170,\"text\":\"first\"}\n"+
			"{\"session_id\":\"a\",\"ts\":170,\"text\":\"second\"}\n"+
			"{\"session_id\":\"a\",\"ts\":170,\"text\":\"appended at same second\"}\n"), 0o644)

	again, wm2, _ := loadCodex(f, wm)
	if len(again) != 3 {
		t.Fatalf("same-second append lost: got %d msgs: %+v", len(again), again)
	}
	found := false
	for _, m := range again {
		if strings.Contains(m.Content, "appended at same second") {
			found = true
		}
	}
	if !found {
		t.Fatalf("appended record missing: %+v", again)
	}
	if wm2 != "170" {
		t.Fatalf("watermark regressed/advanced wrongly: %q", wm2)
	}
}

func TestLoadCodexPartialAppendResumed(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "history.jsonl")
	os.WriteFile(f, []byte(
		"{\"session_id\":\"a\",\"ts\":100,\"text\":\"early\"}\n"+
			"{\"session_id\":\"a\",\"ts\":200,\"text\":\"latest\"}\n"), 0o644)
	_, wm, _ := loadCodex(f, "")

	// simulate an interrupted append: a trailing partial line plus a later
	// complete record; the partial line must not abort the scan
	os.WriteFile(f, []byte(
		"{\"session_id\":\"a\",\"ts\":100,\"text\":\"early\"}\n"+
			"{\"session_id\":\"a\",\"ts\":200,\"text\":\"latest\"}\n"+
			"{\"session_id\":\"a\",\"ts\":300,\"text\":\"resumed wor\n"+
			"{\"session_id\":\"a\",\"ts\":300,\"text\":\"resumed work after interruption\"}\n"), 0o644)

	msgs, _, err := loadCodex(f, wm)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range msgs {
		if m.Content == "resumed work after interruption" {
			found = true
		}
		if m.TS.Before(time.Unix(200, 0)) {
			t.Fatalf("stale record re-emitted past watermark: %+v", m)
		}
	}
	if !found {
		t.Fatalf("resumed record missing: %+v", msgs)
	}
}

// TestLoadCodexMixedResolutionCursor is the re-eval F01 reproduction: a
// sub-second rollout answer must never hide a whole-second history prompt that
// shares its second.
func TestLoadCodexMixedResolutionCursor(t *testing.T) {
	dir := t.TempDir()
	sd := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	// Rollout answer at T+0.5s.
	rollout := `{"type":"session_meta","timestamp":"2026-01-01T00:00:00Z","payload":{"id":"s1","cwd":"/work/atlas"}}` + "\n" +
		`{"timestamp":"2026-01-01T00:00:00.5Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the answer"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(sd, "roll.jsonl"), []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(dir, "history.jsonl")
	first := `{"session_id":"s1","ts":1767225600,"text":"first prompt"}` + "\n" // T = 2026-01-01T00:00:00Z
	if err := os.WriteFile(hist, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, wm, err := loadCodex(hist, "")
	if err != nil {
		t.Fatal(err)
	}
	foundAnswer, foundFirst := false, false
	for _, m := range msgs {
		if m.Content == "the answer" {
			foundAnswer = true
		}
		if m.Content == "first prompt" {
			foundFirst = true
		}
	}
	if !foundAnswer || !foundFirst {
		t.Fatalf("initial load wrong: answer=%v first=%v (%+v)", foundAnswer, foundFirst, msgs)
	}
	if wm != "1767225600.5" {
		t.Fatalf("watermark = %q, want 1767225600.5", wm)
	}

	// A distinct prompt appended later at the same whole second T as the
	// rollout's answer second must not be hidden behind the sub-second cursor.
	if err := os.WriteFile(hist, []byte(first+`{"session_id":"s1","ts":1767225600,"text":"second prompt same second"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, _, err := loadCodex(hist, wm)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range again {
		if m.Content == "second prompt same second" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mixed-resolution cursor dropped the history prompt: %+v", again)
	}
}

// TestLoadCodexScanErrorIsReported pins the scan-diagnostics contract: an
// interrupted file produces an error, not a silent partial result with an
// advanced watermark.
func TestLoadCodexScanErrorIsReported(t *testing.T) {
	dir := t.TempDir()
	hist := filepath.Join(dir, "history.jsonl")
	// A line beyond ScanJSONL's 32 MiB cap aborts the scan mid-file.
	if err := os.WriteFile(hist, []byte(
		`{"session_id":"a","ts":100,"text":"before"}`+"\n"+
			`{"session_id":"a","ts":200,"text":"`+strings.Repeat("x", 33*1024*1024)+`"}`+"\n"+
			`{"session_id":"a","ts":300,"text":"after"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, wm, err := loadCodex(hist, "50")
	if err == nil {
		t.Fatal("oversized line must surface as a scan error, not a silent partial load")
	}
	if wm != "50" {
		t.Fatalf("watermark advanced on a failed scan: %q", wm)
	}
}

// TestCountCodexReportsRolloutScanError pins the count path too: a rollout tree
// with an unreadable file must not produce a silent total, since assess
// publishes that number as "discoverable messages".
func TestCountCodexReportsRolloutScanError(t *testing.T) {
	dir := t.TempDir()
	hist := filepath.Join(dir, "history.jsonl")
	if err := os.WriteFile(hist, []byte(`{"session_id":"a","ts":100,"text":"one"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	broken := `{"type":"session_meta","timestamp":"2026-01-01T00:00:00Z","payload":{"id":"s1","cwd":"/w"}}` + "\n" +
		`{"timestamp":"2026-01-01T00:00:01Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + strings.Repeat("q", 33*1024*1024) + `"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(sd, "roll.jsonl"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := countCodex(hist, nil); err == nil {
		t.Fatal("countCodex must report an unreadable rollout, not return a partial total")
	}
	if _, _, err := loadCodex(hist, ""); err == nil {
		t.Fatal("loadCodex must report the same scan failure")
	}
}
