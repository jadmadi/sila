package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestIsReadOnlyCLIInvocation(t *testing.T) {
	cases := []struct {
		name string
		sub  string
		args []string
		want bool
	}{
		{name: "empty subcommand", sub: "", args: nil, want: true},
		{name: "salam default", sub: "salam", args: nil, want: true},
		{name: "status default", sub: "status", args: []string{"--json"}, want: true},
		{name: "doctor default", sub: "doctor", args: nil, want: true},
		{name: "skill assess", sub: "skill", args: []string{"assess", "--json", "--out=/tmp/x"}, want: true},
		{name: "skill list", sub: "skill", args: []string{"list"}, want: true},
		{name: "skill preview", sub: "skill", args: []string{"preview", "cf-worker"}, want: true},
		{name: "skill search", sub: "skills", args: []string{"search", "cloudflare"}, want: true},
		{name: "skill create mutating", sub: "skill", args: []string{"create", "my-skill"}, want: false},
		{name: "skill install mutating", sub: "skill", args: []string{"install", "my-skill"}, want: false},
		{name: "skill install dry-run", sub: "skill", args: []string{"install", "my-skill", "--dry-run"}, want: true},
		{name: "facts verify read-only", sub: "facts", args: []string{"verify", "--json"}, want: true},
		{name: "facts rebaseline mutating", sub: "facts", args: []string{"rebaseline"}, want: false},
		{name: "projects list read-only", sub: "projects", args: nil, want: true},
		{name: "projects prune dry-run", sub: "projects", args: []string{"prune", "--dry-run"}, want: true},
		{name: "projects prune mutating", sub: "projects", args: []string{"prune", "--force"}, want: false},
		{name: "goals ready read-only", sub: "goals", args: []string{"ready"}, want: true},
		{name: "goals claim mutating", sub: "goals", args: []string{"claim", "my-goal"}, want: false},
		{name: "onboard check read-only", sub: "onboard", args: []string{"check", "--json"}, want: true},
		{name: "onboard mutating", sub: "onboard", args: nil, want: false},
		{name: "memo mutating", sub: "memo", args: []string{"use pure functions"}, want: false},
		{name: "install dry-run", sub: "install", args: []string{"--dry-run"}, want: true},
		{name: "install mutating", sub: "install", args: nil, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isReadOnlyCLIInvocation(tc.sub, tc.args); got != tc.want {
				t.Fatalf("isReadOnlyCLIInvocation(%q, %v) = %v, want %v", tc.sub, tc.args, got, tc.want)
			}
		})
	}
}

func TestFirstNonFlagArg(t *testing.T) {
	if got := firstNonFlagArg([]string{"--db", "/tmp/x.db", "--json", "Assess"}); got != "assess" {
		t.Fatalf("firstNonFlagArg = %q, want %q", got, "assess")
	}
	if got := firstNonFlagArg([]string{"--dry-run", "--"}); got != "" {
		t.Fatalf("firstNonFlagArg = %q, want empty", got)
	}
}

func TestRecordCLIAdoption_SkipsReadOnlyAndMissingDB(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Negative case: missing DB file must NOT be created even for a mutating command.
	missingDB := filepath.Join(tempDir, "missing", "sila.db")
	recordCLIAdoption(missingDB, "memo", []string{"some directive"}, time.Now().Add(-10*time.Millisecond))
	if _, err := os.Stat(missingDB); !os.IsNotExist(err) {
		t.Fatalf("expected missing DB %s to remain uncreated, got err=%v", missingDB, err)
	}

	// 2. Negative case: existing DB file must NOT be mutated on a read-only command (e.g. skill assess).
	existingDB := filepath.Join(tempDir, "existing.db")
	st, err := store.Open(existingDB)
	if err != nil {
		t.Fatalf("open existing db: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close existing db: %v", err)
	}
	past := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(existingDB, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	beforeInfo, err := os.Stat(existingDB)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	recordCLIAdoption(existingDB, "skill", []string{"assess", "--json"}, time.Now().Add(-5*time.Millisecond))

	afterInfo, err := os.Stat(existingDB)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("expected read-only skill assess to leave DB mtime unchanged (before=%v, after=%v)", beforeInfo.ModTime(), afterInfo.ModTime())
	}

	// 3. Positive case: existing DB file records CLI adoption event for mutating command (memo).
	recordCLIAdoption(existingDB, "memo", []string{"directive", "--files=cmd/sila/main.go"}, time.Now().Add(-15*time.Millisecond))

	stVerify, err := store.OpenRO(existingDB)
	if err != nil {
		t.Fatalf("openro existing db: %v", err)
	}
	defer stVerify.Close()
	events, err := stVerify.ListToolEvents(store.ToolEventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolEvents: %v", err)
	}
	if len(events) != 1 || events[0].ToolName != "memo" || events[0].Source != "cli" {
		t.Fatalf("expected 1 cli:memo tool event, got %+v", events)
	}
}
