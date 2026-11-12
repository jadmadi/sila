package store

import (
	"path/filepath"
	"testing"
)

func TestMessagesOutcomeSchema(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name = 'outcome'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("outcome column = %d err=%v", count, err)
	}
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_messages_outcome'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("outcome index = %d err=%v", count, err)
	}
	if _, err := st.DB.Exec(`INSERT INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen) VALUES ('p','h','2026-11-06T10:00:00Z','user','hello','[]','2026-11-06T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	messages, err := st.RecentMessages("p", 10)
	if err != nil || len(messages) != 1 || messages[0].Outcome != "neutral" {
		t.Fatalf("messages = %+v err=%v", messages, err)
	}
}
