package feedback

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestHashProjectID(t *testing.T) {
	if HashProjectID("") != "" {
		t.Fatalf("expected empty string for empty project, got %q", HashProjectID(""))
	}

	h1 := HashProjectID("sila")
	h2 := HashProjectID("sila")
	if h1 != h2 {
		t.Fatalf("expected deterministic hash: %s != %s", h1, h2)
	}
	if len(h1) != 16 {
		t.Fatalf("expected 16-char hex prefix, got len %d (%s)", len(h1), h1)
	}
	if strings.Contains(h1, "sila") {
		t.Fatalf("hash should not leak project name: %s", h1)
	}
}

func TestResolveURLs(t *testing.T) {
	os.Unsetenv("SILA_FEEDBACK_URL")
	os.Unsetenv("SILA_EDGE_URL")

	if u := ResolveFeedbackURL(""); u != DefaultEdgeFeedbackURL {
		t.Fatalf("expected default feedback URL %s, got %s", DefaultEdgeFeedbackURL, u)
	}
	if u := ResolveDrainURL(""); u != DefaultEdgeDrainURL {
		t.Fatalf("expected default drain URL %s, got %s", DefaultEdgeDrainURL, u)
	}
	if u := ResolveAckURL(""); u != DefaultEdgeAckURL {
		t.Fatalf("expected default ack URL %s, got %s", DefaultEdgeAckURL, u)
	}

	custom := "https://custom.example.com/api/feedback"
	if u := ResolveFeedbackURL(custom); u != custom {
		t.Fatalf("expected custom URL %s, got %s", custom, u)
	}

	t.Setenv("SILA_EDGE_URL", "https://edge.example.com")
	if u := ResolveFeedbackURL(""); u != "https://edge.example.com/api/feedback" {
		t.Fatalf("expected edge url override, got %s", u)
	}
	if u := ResolveDrainURL(""); u != "https://edge.example.com/api/feedback/drain" {
		t.Fatalf("expected edge drain url override, got %s", u)
	}
	if u := ResolveAckURL(""); u != "https://edge.example.com/api/feedback/ack" {
		t.Fatalf("expected edge ack url override, got %s", u)
	}
}

func TestSubmitToEdge(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var payload EdgeSubmitPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if strings.Contains(payload.Content, "/home/user") || strings.Contains(payload.Content, "ghp_123456789012345678901234567890123456") {
			t.Errorf("received unredacted content: %s", payload.Content)
		}
		if payload.Category != "issue" {
			t.Errorf("expected normalized category issue, got %s", payload.Category)
		}
		if payload.ProjectHash == "" || strings.Contains(payload.ProjectHash, "secret-repo") {
			t.Errorf("expected hashed project, got %s", payload.ProjectHash)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(EdgeSubmitResult{
			ID:      "fb_12345_abcde",
			Status:  "pending",
			Message: "Feedback received",
		})
	}))
	defer ts.Close()

	fb := &store.Feedback{
		ID:        "local-fb-1",
		ProjectID: "secret-repo",
		Agent:     "claude-opus",
		Category:  "bug",
		Content:   "Crash at /home/user/project with token ghp_123456789012345678901234567890123456",
	}

	res, err := SubmitToEdge(context.Background(), ts.URL, fb)
	if err != nil {
		t.Fatalf("SubmitToEdge failed: %v", err)
	}
	if res.ID != "fb_12345_abcde" || res.Status != "pending" {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Empty content should fail
	_, err = SubmitToEdge(context.Background(), ts.URL, &store.Feedback{Content: "   "})
	if err == nil {
		t.Fatalf("expected error on empty content")
	}
}

func TestDrainAndAckEdgeFeedback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
			return
		}

		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/drain") {
			limit := r.URL.Query().Get("limit")
			if limit != "10" {
				t.Errorf("expected limit=10, got %s", limit)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(EdgeDrainResult{
				Items: []EdgeFeedbackItem{
					{
						ID:          "fb_101",
						Category:    "idea",
						Content:     "Support D1 feedback draining",
						Agent:       "antigravity",
						ProjectHash: "a1b2c3d4",
						SilaVersion: "0.10.1",
						OS:          "linux",
						Arch:        "amd64",
						Status:      "draining",
						CreatedAt:   "2026-10-13T10:00:00Z",
					},
				},
				Count:        1,
				SessionToken: "sess_xyz",
			})
			return
		}

		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/ack") {
			var body struct {
				IDs          []string `json:"ids"`
				SessionToken string   `json:"session_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.IDs) != 1 || body.IDs[0] != "fb_101" {
				t.Errorf("unexpected ids: %+v", body.IDs)
			}
			if body.SessionToken != "sess_xyz" {
				t.Errorf("unexpected session token: %s", body.SessionToken)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":      true,
				"acknowledged": 1,
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	ctx := context.Background()

	// 1. Drain without auth should fail
	_, err := DrainFromEdge(ctx, ts.URL+"/drain", "wrong-token", 10)
	if err == nil {
		t.Fatalf("expected error on invalid token")
	}

	// 2. Drain with auth should succeed
	drainRes, err := DrainFromEdge(ctx, ts.URL+"/drain", "secret-token", 10)
	if err != nil {
		t.Fatalf("DrainFromEdge failed: %v", err)
	}
	if drainRes.Count != 1 || len(drainRes.Items) != 1 {
		t.Fatalf("unexpected drain count: %+v", drainRes)
	}
	if drainRes.Items[0].ID != "fb_101" {
		t.Fatalf("unexpected item id: %s", drainRes.Items[0].ID)
	}

	// 3. Acknowledge should succeed
	acked, err := AcknowledgeEdgeFeedback(ctx, ts.URL+"/ack", "secret-token", []string{"fb_101"}, drainRes.SessionToken)
	if err != nil {
		t.Fatalf("AcknowledgeEdgeFeedback failed: %v", err)
	}
	if acked != 1 {
		t.Fatalf("expected 1 acked item, got %d", acked)
	}
}
