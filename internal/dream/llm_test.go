package dream

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestStdioLLM(t *testing.T) {
	in := bytes.NewBufferString("{\"fact\":\"auth/tokens.ts uses 7d tokens.\",\"confidence\":\"high\"}\n")
	var out bytes.Buffer

	llm := NewStdioLLM(in, &out)
	req := DistillRequest{
		ClaimID:    "fact_1",
		ProjectID:  "proj",
		RawMessage: "Done.",
		RepoFiles:  []string{"auth/tokens.ts"},
	}

	res, err := llm.Distill(req)
	if err != nil {
		t.Fatalf("StdioLLM.Distill failed: %v", err)
	}
	if res.Status != "distilled" || res.Fact != "auth/tokens.ts uses 7d tokens." {
		t.Fatalf("unexpected res: %+v", res)
	}
	if !bytes.Contains(out.Bytes(), []byte("fact_1")) {
		t.Fatalf("request not written to output stream: %s", out.String())
	}
}

func TestRunLLMDistillation(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/proj"); err != nil {
		t.Fatal(err)
	}

	f1 := store.Fact{
		ID: "fact_1", ProjectID: "proj", Claim: "Fixed issue in auth.ts",
		Corroboration: "corroborated", Confidence: "corroborated",
		FileRefsExist: true, Verified: true, RepoFiles: []string{"auth.ts"},
		Tools: []string{"devin", "mimocode"}, FirstSeen: "2026-08-01T00:00:00Z",
		DistillStatus: "pending", ReviewStatus: store.ReviewApproved,
	}
	f2 := store.Fact{
		ID: "fact_2", ProjectID: "proj", Claim: "Casual chat without fact",
		Corroboration: "ungrounded", Confidence: "ungrounded",
		FileRefsExist: false, Verified: false, RepoFiles: []string{},
		Tools: []string{"devin"}, FirstSeen: "2026-08-01T00:00:00Z",
		DistillStatus: "pending",
	}

	st.UpsertFact(f1)
	st.UpsertFact(f2)

	mock := &MockLLM{
		Handlers: map[string]DistillResult{
			"fact_1": {Fact: "auth.ts verifies bearer tokens.", Status: "distilled"},
			"fact_2": {Fact: "", Status: "skipped"},
		},
	}

	stats, err := RunLLMDistillation(st, mock, "proj", false, 10)
	if err != nil {
		t.Fatalf("RunLLMDistillation failed: %v", err)
	}
	if stats.Distilled != 1 || stats.Skipped != 1 {
		t.Fatalf("expected 1 distilled, 1 skipped, got %+v", stats)
	}

	facts, _ := st.FactsByProject("proj")
	for _, f := range facts {
		if f.ID == "fact_1" {
			if f.Distilled != "auth.ts verifies bearer tokens." || f.DistillStatus != "distilled" {
				t.Fatalf("fact_1 not updated: %+v", f)
			}
			if !f.IsFact() {
				t.Fatalf("fact_1 should qualify as IsFact: %+v", f)
			}
		}
		if f.ID == "fact_2" {
			if f.DistillStatus != "skipped" {
				t.Fatalf("fact_2 not marked skipped: %+v", f)
			}
			if f.IsFact() {
				t.Fatalf("fact_2 must not be IsFact: %+v", f)
			}
		}
	}
}

func TestRunLLMDistillationRetryFailed(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/proj"); err != nil {
		t.Fatal(err)
	}

	f1 := store.Fact{
		ID: "f_failed", ProjectID: "proj", Claim: "Failed in auth.ts",
		DistillStatus: "failed",
	}
	f2 := store.Fact{
		ID: "f_distilled", ProjectID: "proj", Claim: "Already distilled",
		Distilled: "Existing fact.", DistillStatus: "distilled",
	}
	st.UpsertFact(f1)
	st.UpsertFact(f2)

	mock := &MockLLM{
		Handlers: map[string]DistillResult{
			"f_failed": {Fact: "auth.ts recovers token errors.", Status: "distilled"},
		},
	}

	// Without retryFailed=true, f_failed is skipped
	statsNoRetry, err := RunLLMDistillation(st, mock, "proj", false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if statsNoRetry.Total != 0 {
		t.Fatalf("expected 0 processed without retry, got %+v", statsNoRetry)
	}

	// With retryFailed=true, f_failed is retried while f_distilled remains untouched
	statsRetry, err := RunLLMDistillation(st, mock, "proj", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if statsRetry.Distilled != 1 {
		t.Fatalf("expected 1 retried and distilled, got %+v", statsRetry)
	}

	facts, _ := st.FactsByProject("proj")
	for _, f := range facts {
		if f.ID == "f_failed" {
			if f.DistillStatus != "distilled" || f.Distilled != "auth.ts recovers token errors." {
				t.Fatalf("f_failed not updated on retry: %+v", f)
			}
		}
	}
}

func TestParseDistillResponseMarkdownStrip(t *testing.T) {
	wrapped := "```json\n{\"fact\": \"db/schema.sql has WAL mode enabled.\", \"confidence\": \"high\"}\n```"
	res, err := ParseDistillResponse(wrapped)
	if err != nil {
		t.Fatalf("ParseDistillResponse failed on markdown json: %v", err)
	}
	if res.Fact != "db/schema.sql has WAL mode enabled." {
		t.Fatalf("unexpected fact: %q", res.Fact)
	}
	if res.Status != "distilled" {
		t.Fatalf("unexpected status: %q", res.Status)
	}
}
