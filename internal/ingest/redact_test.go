package ingest

import (
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/importmem"
)

func TestIngestRedactsTranscript(t *testing.T) {
	content := "run with postgres://user:ingestpass@db.local/app and password=hunter2"
	redacted, err := importmem.Redact(content, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(redacted.Text, "ingestpass") || strings.Contains(redacted.Text, "hunter2") {
		t.Fatalf("transcript not redacted: %q", redacted.Text)
	}
	if redacted.Counts["secret"] == 0 {
		t.Fatalf("counts = %+v", redacted.Counts)
	}
}
