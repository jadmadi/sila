package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func TestPublishKnowledgeHTTP(t *testing.T) {
	var receivedBody PublishPayload
	var receivedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sync/publish" {
			http.NotFound(w, r)
			return
		}
		receivedAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"published": map[string]int{
				"facts": len(receivedBody.Facts),
				"memos": len(receivedBody.Memos),
				"goals": len(receivedBody.Goals),
			},
		})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "test-token-xyz", nil)

	payload := PublishPayload{
		Facts: []EdgeFact{
			{
				ID:          "fact-1",
				ProjectSlug: "sila",
				Fact:        "Sila is written in pure Go without CGO.",
				Confidence:  "high",
				UpdatedAt:   "2026-09-23T02:00:00Z",
			},
		},
		Memos: []EdgeMemo{
			{
				ID:          "memo-1",
				ProjectSlug: "sila",
				Title:       "Zero Ambient Telemetry",
				Directive:   "Never track users.",
				UpdatedAt:   "2026-09-23T02:00:00Z",
			},
		},
		Goals: []EdgeGoal{
			{
				ID:          "sila:edge-grounding-mcp",
				ProjectSlug: "sila",
				Slug:        "edge-grounding-mcp",
				Title:       "Edge Grounding",
				GoalMd:      "# Spec",
				Status:      "pending",
				UpdatedAt:   "2026-09-23T02:00:00Z",
			},
		},
	}

	resp, err := client.PublishKnowledge(context.Background(), payload)
	if err != nil {
		t.Fatalf("PublishKnowledge failed: %v", err)
	}

	if !resp.Success {
		t.Error("expected resp.Success to be true")
	}
	if resp.Published.Facts != 1 || resp.Published.Memos != 1 || resp.Published.Goals != 1 {
		t.Errorf("unexpected published counts: %+v", resp.Published)
	}
	if receivedAuth != "Bearer test-token-xyz" {
		t.Errorf("unexpected Authorization header: %q", receivedAuth)
	}
	if len(receivedBody.Facts) != 1 || receivedBody.Facts[0].ID != "fact-1" {
		t.Errorf("unexpected received facts: %+v", receivedBody.Facts)
	}
}

func TestBuildPublishPayloadAndRedaction(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Insert fact with sensitive data (home dir, private key)
	sensitiveClaim := "-----" + "BEGIN RSA " + "PRIVATE KEY-----\nMIIEowIBAAKCAQEA0\n-----" + "END RSA " + "PRIVATE KEY-----\nFact in /home/user/secrets/test.go"
	err = st.UpsertFact(store.Fact{
		ID:         "fact-test-1",
		ProjectID:  p.ID,
		Claim:      sensitiveClaim,
		Confidence: "grounded",
		Kind:       "code_claim",
		RepoFiles:  []string{"secrets/test.go"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Insert memo with bearer token
	memoClaim := "All requests must use Bearer secret_bearer_token_123456789012345"
	_, err = st.InsertMemo(p.ID, memoClaim, "agent-test", tmp, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Create a goal in repo
	indep := true
	_, err = goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     tmp,
		Slug:        "auth-hardening",
		Title:       "Auth Hardening",
		Description: "Hardening auth in /Users/jad/repo",
		Independent: &indep,
	})
	if err != nil {
		t.Fatal(err)
	}

	payload, err := BuildPublishPayload(st, tmp, p.ID)
	if err != nil {
		t.Fatalf("BuildPublishPayload failed: %v", err)
	}

	if len(payload.Facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(payload.Facts))
	}
	if strings.Contains(payload.Facts[0].Fact, "BEGIN RSA "+"PRIVATE KEY") {
		t.Error("private key was not redacted from fact claim")
	}
	if !strings.Contains(payload.Facts[0].Fact, "[REDACTED_PRIVATE_KEY]") {
		t.Errorf("expected [REDACTED_PRIVATE_KEY] in fact claim, got: %q", payload.Facts[0].Fact)
	}

	if len(payload.Memos) != 1 {
		t.Fatalf("expected 1 memo, got %d", len(payload.Memos))
	}
	if strings.Contains(payload.Memos[0].Directive, "secret_bearer_token") {
		t.Error("bearer token was not redacted from memo directive")
	}
	if !strings.Contains(payload.Memos[0].Directive, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in memo directive, got: %q", payload.Memos[0].Directive)
	}

	if len(payload.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(payload.Goals))
	}
	if payload.Goals[0].Slug != "auth-hardening" {
		t.Errorf("expected slug auth-hardening, got %q", payload.Goals[0].Slug)
	}
}
