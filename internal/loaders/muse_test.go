package loaders

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// museFixture writes a session shaped like the real store: `sessions/YYYY/MM/DD
// /<sid>/session.jsonl`, mixing a transaction-frame-wrapped record with bare
// records, and including the traps verified against real data.
func museFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "sessions")
	sessDir := filepath.Join(root, "2026", "09", "15", "sid-1")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The workspace record and the user turn arrive as frame-wrapped records.
	frame := func(recordJSON string) string {
		escaped := strings.ReplaceAll(recordJSON, `"`, `\"`)
		escaped = strings.ReplaceAll(escaped, "\n", `\n`)
		return `{"retained_frame":"session_permission_transaction","frame_schema_version":1,` +
			`"outer_log_ordinal":1,"children":[{"child_index":0,"record_json":"` + escaped + `"}]}`
	}
	lines := []string{
		frame(`{"schema_version":1,"sequence":10,"recorded_at":1700000000000000,"record_type":"event",` +
			`"payload_type":"session.workspace_branch.observed","stream":{"kind":"session","id":"sid-1"},` +
			`"payload":{"record":{"workspace_root":"/work/atlas"}}}`),
		frame(`{"schema_version":1,"sequence":18,"recorded_at":1700000001000000,"record_type":"event",` +
			`"payload_type":"runtime.user_intent.accepted","stream":{"kind":"session","id":"sid-1"},` +
			`"payload":{"refill_blocks":[{"text":"please diagnose the payment worker"}]}}`),
		// session.started echoes the prompt: ingesting it would double-count.
		`{"schema_version":1,"sequence":19,"recorded_at":1700000001100000,"record_type":"event",` +
			`"payload_type":"runtime.session","stream":{"kind":"session","id":"sid-1"},` +
			`"payload":{"event":{"kind":"started","prompt":"please diagnose the payment worker"}}}`,
		// reasoning is model-internal and must not be ingested
		`{"schema_version":1,"sequence":21,"recorded_at":1700000001200000,"record_type":"event",` +
			`"payload_type":"runtime.session","stream":{"kind":"session","id":"sid-1"},` +
			`"payload":{"event":{"kind":"reasoning_committed","text":"the user seems worried about goroutines"}}}`,
		`{"schema_version":1,"sequence":22,"recorded_at":1700000001300000,"record_type":"event",` +
			`"payload_type":"runtime.session","stream":{"kind":"session","id":"sid-1"},` +
			`"payload":{"event":{"kind":"assistant_message_committed","text":"It leaks one goroutine per request."}}}`,
		// slash commands are not conversation
		`{"schema_version":1,"sequence":25,"recorded_at":1700000001400000,"record_type":"event",` +
			`"payload_type":"command.invoked","stream":{"kind":"session","id":"sid-1"},` +
			`"payload":{"record":{"command":"/model"}}}`,
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, "session.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// The materialized view holds binary framing and must be skipped entirely.
	viewDir := filepath.Join(root, ".msp-view-v1", "sid-1")
	if err := os.MkdirAll(viewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(viewDir, "journal-00000000.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadMuse(t *testing.T) {
	root := museFixture(t)

	msgs, wm, err := loadMuse(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 curated messages, got %d: %+v", len(msgs), msgs)
	}

	user, assistant := msgs[0], msgs[1]
	if user.Role != "user" || assistant.Role != "assistant" {
		t.Fatalf("roles wrong: %q / %q", user.Role, assistant.Role)
	}
	if user.Content != "please diagnose the payment worker" {
		t.Fatalf("user content = %q", user.Content)
	}
	if strings.Contains(assistant.Content, "worried") {
		t.Fatal("reasoning text was ingested")
	}
	if assistant.Content != "It leaks one goroutine per request." {
		t.Fatalf("assistant content = %q", assistant.Content)
	}

	// The prompt appears once: `session.started` echoed it and was skipped.
	if strings.Count(strings.Join([]string{user.Content, assistant.Content}, "|"), "payment worker") != 1 {
		t.Fatal("prompt double-counted via session.started")
	}

	// Provenance: session id from the record, workspace from a separate record,
	// order from `sequence`.
	for _, m := range msgs {
		if m.Tool != "muse" || m.SessionID != "sid-1" {
			t.Fatalf("provenance wrong: %+v", m)
		}
		if m.ProjectDir != "/work/atlas" {
			t.Fatalf("workspace not applied: %q", m.ProjectDir)
		}
		if m.SourceLocation == "" || m.SourceOrder == 0 {
			t.Fatalf("source position missing: %+v", m)
		}
	}
	if want := time.UnixMicro(1700000001300000).UTC(); !assistant.TS.Equal(want) {
		t.Fatalf("assistant ts = %v, want %v", assistant.TS, want)
	}

	if wm != "1700000001300000" {
		t.Fatalf("watermark = %q", wm)
	}
	n, err := countMuse(root, nil)
	if err != nil || n != 2 {
		t.Fatalf("countMuse = %d (err=%v), want 2", n, err)
	}
}

func TestLoadMuseIncremental(t *testing.T) {
	root := museFixture(t)
	_, wm, err := loadMuse(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	msgs, wm2, err := loadMuse(root, nil, wm)
	if err != nil {
		t.Fatal(err)
	}
	// Only the record on the boundary comes back, and the cursor holds.
	if len(msgs) != 1 || msgs[0].Role != "assistant" {
		t.Fatalf("incremental pass wrong: %+v", msgs)
	}
	if wm2 != wm {
		t.Fatalf("watermark moved on an unchanged pass: %q -> %q", wm, wm2)
	}
}

func TestLoadMuseMissingRoot(t *testing.T) {
	if _, _, err := loadMuse(filepath.Join(t.TempDir(), "absent"), nil, ""); err == nil {
		t.Fatal("expected an error for a missing Muse sessions directory")
	}
	if _, err := countMuse(filepath.Join(t.TempDir(), "absent"), nil); err == nil {
		t.Fatal("expected an error for a missing Muse sessions directory")
	}
}
