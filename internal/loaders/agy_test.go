package loaders

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgyLoaderWorkspaceAndRoles(t *testing.T) {
	tmp := t.TempDir()
	convID := "35e85294-a6bd-4d7c-b02e-3425fa9981e8"
	logDir := filepath.Join(tmp, "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	projDir := filepath.Join(tmp, "project-a")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	transcriptPath := filepath.Join(logDir, "transcript.jsonl")
	content := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-08-13T20:00:00Z","content":"<user_information>\n` + projDir + ` -> ` + projDir + `\n</user_information>\nhow do you evaluate this app?"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-08-13T20:01:00Z","content":"Evaluating sila requires checking internal/store and internal/dream."}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewAgyLoader(tmp)
	msgs, wm, err := loader.Load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	if msgs[0].SessionID != convID {
		t.Fatalf("expected session ID %q, got %q", convID, msgs[0].SessionID)
	}
	if msgs[0].ProjectDir != projDir {
		t.Fatalf("expected project dir %q, got %q", projDir, msgs[0].ProjectDir)
	}
	if msgs[0].Role != "user" {
		t.Fatalf("expected role user, got %q", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", msgs[1].Role)
	}
	if wm != "2026-08-13T20:01:00Z" {
		t.Fatalf("expected watermark 2026-08-13T20:01:00Z, got %q", wm)
	}

	// Test incremental load with watermark
	incMsgs, _, err := loader.Load(wm, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(incMsgs) != 0 {
		t.Fatalf("expected 0 new messages after watermark, got %d", len(incMsgs))
	}
}

func TestAgyLoaderConversationSummariesSQLite(t *testing.T) {
	tmp := t.TempDir()

	// 1. Create conversation_summaries.db
	dbPath := filepath.Join(tmp, "conversation_summaries.db")
	db, err := openWritableSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	schema := `
	CREATE TABLE conversation_summaries (
		conversation_id text PRIMARY KEY,
		title text NOT NULL DEFAULT "",
		preview text NOT NULL DEFAULT "",
		step_count integer NOT NULL DEFAULT 0,
		last_modified_time datetime NOT NULL,
		workspace_uris text NOT NULL,
		status text NOT NULL DEFAULT "",
		last_user_input_time datetime NOT NULL
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	cidArchived := "11111111-1111-1111-1111-111111111111"
	cidActive := "22222222-2222-2222-2222-222222222222"

	_, err = db.Exec(`
		INSERT INTO conversation_summaries 
		(conversation_id, title, preview, step_count, last_modified_time, workspace_uris, status, last_user_input_time)
		VALUES
		(?, 'Archived Task', 'Refactor SQLite queries', 15, '2026-08-10 12:00:00+00:00', '["file:///workspace/project-archived"]', 'completed', '2026-08-10 11:59:00+00:00'),
		(?, 'Active Task', 'Implement new feature', 40, '2026-08-12 14:00:00+00:00', '["file:///workspace/project-active"]', 'in_progress', '2026-08-12 13:59:00+00:00');
	`, cidArchived, cidActive)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Create transcript ONLY for cidActive (cidArchived is pruned from disk)
	logDir := filepath.Join(tmp, "brain", cidActive, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(logDir, "transcript.jsonl")
	// Notice transcript does NOT contain workspace header; workspace MUST come from conversation_summaries.db
	transcriptContent := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-08-12T13:59:00Z","content":"Please build the feature"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-08-12T14:00:00Z","content":"Feature implemented successfully"}
`
	if err := os.WriteFile(transcriptPath, []byte(transcriptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Load via NewAgyLoader
	loader := NewAgyLoader(tmp)
	msgs, wm, err := loader.Load("", nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should have 1 archived summary message + 2 transcript messages = 3 messages
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Check that msgs are sorted chronologically
	if msgs[0].SessionID != cidArchived {
		t.Errorf("expected first msg to be archived session %q, got %q", cidArchived, msgs[0].SessionID)
	}
	if msgs[0].ProjectDir != "/workspace/project-archived" {
		t.Errorf("expected archived project dir /workspace/project-archived, got %q", msgs[0].ProjectDir)
	}
	if msgs[0].Content != "Archived Task\n\nRefactor SQLite queries" {
		t.Errorf("unexpected archived content: %q", msgs[0].Content)
	}

	// Check that active transcript turns got their workspace populated from conversation_summaries.db
	if msgs[1].SessionID != cidActive || msgs[1].ProjectDir != "/workspace/project-active" {
		t.Errorf("expected transcript msg 1 to have project /workspace/project-active, got %q", msgs[1].ProjectDir)
	}
	if msgs[2].SessionID != cidActive || msgs[2].ProjectDir != "/workspace/project-active" {
		t.Errorf("expected transcript msg 2 to have project /workspace/project-active, got %q", msgs[2].ProjectDir)
	}

	if wm != "2026-08-12T14:00:00Z" {
		t.Errorf("expected watermark 2026-08-12T14:00:00Z, got %q", wm)
	}

	// 4. Test project scoping
	archivedSel := "project-archived"
	archivedMsgs, _, err := loader.Load("", &archivedSel)
	if err != nil {
		t.Fatal(err)
	}
	if len(archivedMsgs) != 1 || archivedMsgs[0].SessionID != cidArchived {
		t.Errorf("expected 1 scoped message for project-archived, got %d", len(archivedMsgs))
	}

	// 5. Test countAgy
	cnt, err := loader.Count(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 3 {
		t.Fatalf("expected count 3, got %d", cnt)
	}
}

func TestParseWorkspaceURIs(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{`["file:///workspace/sila"]`, "/workspace/sila"},
		{`["file:///home/user/my%20project/sub"]`, "/home/user/my project/sub"},
		{`["/direct/path"]`, "/direct/path"},
		{`file:///plain/path`, "/plain/path"},
		{`[]`, ""},
		{`""`, ""},
	}
	for _, c := range cases {
		got := parseWorkspaceURIs(c.input)
		if got != c.expected {
			t.Errorf("parseWorkspaceURIs(%q) = %q, expected %q", c.input, got, c.expected)
		}
	}
}

func openWritableSQLite(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}

// TestLoadAgyScanErrorIsReported pins the contract that a transcript which
// cannot be read in full is reported, not skipped while the cursor advances
// past the records it still holds. The scan error used to die in the caller's
// loop, so the tool reported success and moved its watermark.
func TestLoadAgyScanErrorIsReported(t *testing.T) {
	root := t.TempDir()
	// A healthy conversation and an unreadable one, in the same tree.
	good := filepath.Join(root, "brain", "conv-good", ".system_generated", "logs")
	bad := filepath.Join(root, "brain", "conv-bad", ".system_generated", "logs")
	for _, d := range []string{good, bad} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ok := `{"created_at":"2026-09-01T10:00:00Z","content":"healthy turn"}` + "\n"
	if err := os.WriteFile(filepath.Join(good, "transcript.jsonl"), []byte(ok), 0o644); err != nil {
		t.Fatal(err)
	}
	// A line beyond the scanner's 32 MiB cap aborts the scan mid-file.
	broken := `{"created_at":"2026-09-01T10:00:01Z","content":"before"}` + "\n" +
		`{"created_at":"2026-09-01T10:00:02Z","content":"` + strings.Repeat("z", 33*1024*1024) + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(bad, "transcript.jsonl"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	l := NewAgyLoader(root)
	_, wm, err := l.Load("", nil)
	if err == nil {
		t.Fatal("an unreadable transcript must surface as a scan error, not a silent skip")
	}
	if wm != "" {
		t.Fatalf("cursor advanced past an incomplete scan: %q", wm)
	}
	// The count path already propagated; keep it that way.
	if _, err := l.Count(nil); err == nil {
		t.Fatal("count must report the interrupted scan too")
	}
}

func TestStripAGYUserEnvelope(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "full envelope with metadata and settings change",
			in: "<USER_REQUEST>\nrename the payment worker\n</USER_REQUEST>\n" +
				"<ADDITIONAL_METADATA> The current local time is: 2026-09-16T18:25:13+03:00. </ADDITIONAL_METADATA>\n" +
				"<USER_SETTINGS_CHANGE>changed setting `Model Selection` from None to Flash. No need to comment.</USER_SETTINGS_CHANGE>",
			want: "rename the payment worker",
		},
		{
			name: "single line envelope",
			in:   "<USER_REQUEST> run 'sila goals' </USER_REQUEST> <ADDITIONAL_METADATA> time is 18:25 </ADDITIONAL_METADATA>",
			want: "run 'sila goals'",
		},
		{
			name: "prompt keeps its own formatting and inner angle brackets",
			in:   "<USER_REQUEST>\n## Notes\n\n- fix <Foo> generics\n</USER_REQUEST>\n<ADDITIONAL_METADATA>x</ADDITIONAL_METADATA>",
			want: "## Notes\n\n- fix <Foo> generics",
		},
		{
			name: "truncated record without a closing tag keeps the prompt",
			in:   "<USER_REQUEST>\n/plannotator-setup-goal ### Quality Audit\n\n   ID | Category\n",
			want: "/plannotator-setup-goal ### Quality Audit\n\n   ID | Category",
		},
		{
			name: "truncated record still drops a trailing harness block",
			in:   "<USER_REQUEST>\nthe prompt\n<ADDITIONAL_METADATA>noise</ADDITIONAL_METADATA>",
			want: "the prompt",
		},
		{
			name: "content with no envelope is untouched",
			in:   "just a question about sqlite",
			want: "just a question about sqlite",
		},
		{
			name: "text before an opening tag is preserved, never dropped",
			in:   "the record starts with <USER_REQUEST> when agy logs it",
			want: "the record starts with\nwhen agy logs it",
		},
		{
			name: "empty envelope is returned unchanged rather than emptied",
			in:   "<USER_REQUEST></USER_REQUEST>",
			want: "<USER_REQUEST></USER_REQUEST>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripAGYUserEnvelope(tc.in); got != tc.want {
				t.Errorf("StripAGYUserEnvelope() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStripAGYAssistantEnvelope covers the header shapes measured on the
// maintainer's source store: the paired Created/Completed form (25,445
// records), the lone Created form followed by real content (1,235), and the
// safety cases that must never lose text.
func TestStripAGYAssistantEnvelope(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "paired timestamps",
			in:   "Created At: 2026-08-31T04:22:04+03:00\nCompleted At: 2026-08-31T04:22:04+03:00\nCreated file file:///x/DISPATCH.md with requested content.",
			want: "Created file file:///x/DISPATCH.md with requested content.",
		},
		{
			name: "lone created followed by background-task status",
			in:   "Created At: 2026-09-01T10:00:00+03:00\nTool is running as a background task with output captured.",
			want: "Tool is running as a background task with output captured.",
		},
		{
			name: "multiline content after the pair",
			in:   "Created At: 2026-08-24T23:35:55+03:00\nCompleted At: 2026-08-24T23:35:55+03:00\nFound 6 results\napps/homepage/dist/chunk.mjs",
			want: "Found 6 results\napps/homepage/dist/chunk.mjs",
		},
		{
			// A pending claim is the message text with whitespace normalized,
			// so the envelope arrives space-separated; the strip must still
			// find it.
			name: "space-normalized envelope, as claims carry it",
			in:   "Created At: 2026-08-24T23:35:55+03:00 Completed At: 2026-08-24T23:35:55+03:00 Found 6 results apps/homepage/dist/chunk.mjs",
			want: "Found 6 results apps/homepage/dist/chunk.mjs",
		},
		{
			name: "lone created, space-normalized",
			in:   "Created At: 2026-09-01T10:00:00+03:00 Tool is running as a background task",
			want: "Tool is running as a background task",
		},
		{
			name: "content without the header is untouched",
			in:   "The payment worker retries three times before dead-lettering.",
			want: "The payment worker retries three times before dead-lettering.",
		},
		{
			name: "created without a parseable timestamp is untouched",
			in:   "Created At: yesterday, roughly\nthen the model went on",
			want: "Created At: yesterday, roughly\nthen the model went on",
		},
		{
			name: "a human turn starting with the words is kept",
			in:   "Created At: is the name of my repo, please clone it",
			want: "Created At: is the name of my repo, please clone it",
		},
		{
			name: "header alone is returned unchanged rather than emptied",
			in:   "Created At: 2026-08-31T04:22:04+03:00\nCompleted At: 2026-08-31T04:22:04+03:00",
			want: "Created At: 2026-08-31T04:22:04+03:00\nCompleted At: 2026-08-31T04:22:04+03:00",
		},
		{
			name: "lone created with nothing after is unchanged",
			in:   "Created At: 2026-09-01T10:00:00+03:00",
			want: "Created At: 2026-09-01T10:00:00+03:00",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripAGYAssistantEnvelope(tc.in); got != tc.want {
				t.Errorf("StripAGYAssistantEnvelope() = %q, want %q", got, tc.want)
			}
		})
	}
}
