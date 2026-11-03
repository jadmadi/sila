package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/feedback"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestFeedbackEdgeShareAndDrain(t *testing.T) {
	drainedIDs := make(map[string]bool)

	// Mock Cloudflare Worker D1 endpoint
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Public submit endpoint
		if r.Method == http.MethodPost && r.URL.Path == "/api/feedback" {
			var body feedback.EdgeSubmitPayload
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(feedback.EdgeSubmitResult{
				ID:      "fb_test_remote_999",
				Status:  "pending",
				Message: "Received",
			})
			return
		}

		// Authenticated drain endpoint
		if r.Method == http.MethodGet && r.URL.Path == "/api/feedback/drain" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-secret-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
				return
			}
			_ = json.NewEncoder(w).Encode(feedback.EdgeDrainResult{
				Items: []feedback.EdgeFeedbackItem{
					{
						ID:          "fb_edge_inbox_1",
						Category:    "issue",
						Content:     "Subshell environment isolation issue",
						Agent:       "devin",
						ProjectHash: "abcdef1234567890",
						SilaVersion: "0.10.1",
						OS:          "linux",
						Arch:        "amd64",
						Status:      "draining",
						CreatedAt:   "2026-10-13T12:00:00Z",
					},
				},
				Count:        1,
				SessionToken: "session_tok_abc",
			})
			return
		}

		// Authenticated ack endpoint
		if r.Method == http.MethodPost && r.URL.Path == "/api/feedback/ack" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-secret-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var ackBody struct {
				IDs          []string `json:"ids"`
				SessionToken string   `json:"session_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&ackBody)
			for _, id := range ackBody.IDs {
				drainedIDs[id] = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":      true,
				"acknowledged": len(ackBody.IDs),
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sila.db")

	ui := render.New(nil, true)

	// 1. Record feedback with --share flag
	runFeedback(ui, dbPath, "test-proj", "antigravity", "idea", "", "", "", "", true, 10, false, false, false, true, true, true, []string{
		"--remote=" + ts.URL + "/api/feedback",
		"Enable D1 edge feedback sharing",
	})

	st, err := store.OpenRO(dbPath)
	if err != nil {
		t.Fatalf("OpenRO failed: %v", err)
	}
	defer st.Close()

	items, err := st.ListFeedback("test-proj", "", 10)
	if err != nil {
		t.Fatalf("ListFeedback failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 feedback item, got %d", len(items))
	}
	if !strings.Contains(items[0].ResolutionNote, "Shared to Sila edge: fb_test_remote_999") {
		t.Errorf("expected ResolutionNote with edge ID, got: %q", items[0].ResolutionNote)
	}

	// 2. Share existing feedback item using "share" subcommand
	runFeedback(ui, dbPath, "test-proj", "antigravity", "", "", "", "", "", true, 10, false, false, false, false, true, true, []string{
		"share",
		items[0].ID,
		"--remote=" + ts.URL + "/api/feedback",
	})

	// 3. Maintainer drains edge feedback into local store
	runFeedback(ui, dbPath, "test-proj", "antigravity", "", "", "", "", "", true, 10, false, false, false, false, true, true, []string{
		"drain",
		"--token=test-secret-token",
		"--remote=" + ts.URL,
	})

	drainedItem, err := st.GetFeedback("fb_edge-inbox-1")
	if err != nil {
		t.Fatalf("expected drained feedback item in local store: %v", err)
	}
	if drainedItem.Content != "Subshell environment isolation issue" {
		t.Errorf("unexpected content: %s", drainedItem.Content)
	}
	if !drainedIDs["fb_edge_inbox_1"] {
		t.Errorf("expected item to be acknowledged on edge")
	}
}
