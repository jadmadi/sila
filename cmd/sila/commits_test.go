package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func commitsFixture(t *testing.T) (string, []store.CommitReceipt) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "c.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := time.Now().UTC().Add(-time.Hour)
	receipts := []store.CommitReceipt{
		{CommitSHA: "abcdef1234567890", ProjectID: "p", SessionID: "s1", Agent: "a", Source: "ancestry", CreatedAt: base},
		{CommitSHA: "abcdef9999999999", ProjectID: "p", SessionID: "s2", Agent: "b", Source: "ancestry", CreatedAt: base.Add(time.Minute)},
		{CommitSHA: "1234567890abcdef", ProjectID: "p", SessionID: "s3", Agent: "c", Source: "session-env", CreatedAt: base.Add(2 * time.Minute)},
	}
	for _, receipt := range receipts {
		if err := st.SaveCommitReceipt(receipt); err != nil {
			t.Fatal(err)
		}
	}
	return dbPath, receipts
}

func TestCommitsJSONListOrdered(t *testing.T) {
	dbPath, _ := commitsFixture(t)
	out := captureStdout(t, func() {
		runCommits(render.New(os.Stdout, true), dbPath, "p", "", true, nil)
	})
	var got []store.CommitReceipt
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(got) != 3 || got[0].SessionID != "s3" || got[1].SessionID != "s2" {
		t.Fatalf("order = %+v", got)
	}
}

func TestCommitsShortSHAAndAmbiguity(t *testing.T) {
	dbPath, _ := commitsFixture(t)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	receipt, notFound, candidates, err := findCommitReceipt(st, "p", "1234567")
	if err != nil || notFound || len(candidates) != 0 || receipt == nil || receipt.SessionID != "s3" {
		t.Fatalf("receipt=%+v notFound=%v candidates=%v err=%v", receipt, notFound, candidates, err)
	}
	receipt, notFound, candidates, err = findCommitReceipt(st, "p", "abcdef")
	if err != nil || notFound || len(candidates) != 2 || receipt != nil {
		t.Fatalf("ambiguous: %+v %v %v %v", receipt, notFound, candidates, err)
	}
	_, notFound, _, err = findCommitReceipt(st, "p", "ffff")
	if err != nil || !notFound {
		t.Fatalf("missing: %v %v", notFound, err)
	}
}

func TestCommitsHumanHasNoEscapes(t *testing.T) {
	dbPath, _ := commitsFixture(t)
	out := captureStdout(t, func() {
		runCommits(render.New(os.Stdout, true), dbPath, "p", "", false, nil)
	})
	if strings.Contains(out, "\x1b") {
		t.Fatal("human output contains escape bytes")
	}
	if !strings.Contains(out, "session") {
		t.Fatalf("output = %q", out)
	}
}

func TestCommitsShowJSON(t *testing.T) {
	dbPath, _ := commitsFixture(t)
	out := captureStdout(t, func() {
		runCommits(render.New(os.Stdout, true), dbPath, "p", "", true, []string{"show", "1234567"})
	})
	var receipt store.CommitReceipt
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if receipt.SessionID != "s3" {
		t.Fatalf("receipt = %+v", receipt)
	}
}
