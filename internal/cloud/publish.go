package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jadmadi/sila/internal/gateway"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// EdgeFact represents a fact payload sent to the Cloudflare edge read replica.
type EdgeFact struct {
	ID          string   `json:"id"`
	ProjectSlug string   `json:"project_slug"`
	Fact        string   `json:"fact"`
	Confidence  string   `json:"confidence"`
	FileRefs    []string `json:"file_refs,omitempty"`
	UpdatedAt   string   `json:"updated_at"`
	HLC         string   `json:"hlc,omitempty"`
}

// EdgeMemo represents an active directive or architectural memo sent to the edge replica.
type EdgeMemo struct {
	ID          string `json:"id"`
	ProjectSlug string `json:"project_slug"`
	Title       string `json:"title"`
	Directive   string `json:"directive"`
	UpdatedAt   string `json:"updated_at"`
	HLC         string `json:"hlc,omitempty"`
}

// EdgeGoal represents a roadmap goal package sent to the edge replica.
type EdgeGoal struct {
	ID          string `json:"id"`
	ProjectSlug string `json:"project_slug"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	GoalMd      string `json:"goal_md"`
	PlanMd      string `json:"plan_md,omitempty"`
	FactsMd     string `json:"facts_md,omitempty"`
	Status      string `json:"status"`
	UpdatedAt   string `json:"updated_at"`
	HLC         string `json:"hlc,omitempty"`
}

// PublishPayload is the request body for POST /api/sync/publish.
type PublishPayload struct {
	Facts []EdgeFact `json:"facts,omitempty"`
	Memos []EdgeMemo `json:"memos,omitempty"`
	Goals []EdgeGoal `json:"goals,omitempty"`
	HLC   string     `json:"hlc,omitempty"`
}

// PublishResponse is the response from POST /api/sync/publish.
type PublishResponse struct {
	Success   bool `json:"success"`
	Published struct {
		Facts int `json:"facts"`
		Memos int `json:"memos"`
		Goals int `json:"goals"`
	} `json:"published"`
}

// PublishKnowledge publishes redacted verified facts, memos, and goals to the Cloudflare D1 edge read-replica.
func (c *Client) PublishKnowledge(ctx context.Context, payload PublishPayload) (*PublishResponse, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling publish payload: %w", err)
	}

	reqURL := c.endpoint + "/api/sync/publish"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("publish request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("publish failed (status %d): %s", resp.StatusCode, string(body))
	}

	var pubResp PublishResponse
	if err := json.NewDecoder(resp.Body).Decode(&pubResp); err != nil {
		return nil, fmt.Errorf("decoding publish response: %w", err)
	}

	return &pubResp, nil
}

// BuildPublishPayload compiles verified facts, memos, and goals for a project,
// passing every string through secret redaction prior to egress.
func BuildPublishPayload(st *store.Store, repoRoot, project string) (PublishPayload, error) {
	var payload PublishPayload
	now := time.Now().UTC().Format(time.RFC3339)
	payload.HLC = store.GenerateHLC()

	// 1. Facts & Memos from store
	if st != nil && project != "" {
		facts, err := st.FactsByProject(project)
		if err == nil {
			for _, f := range facts {
				claim := gateway.Redact(f.Claim)
				if claim == "" {
					continue
				}

				factID := f.ID
				if factID == "" {
					factID = fmt.Sprintf("fact-%d", time.Now().UnixNano())
				}

				if f.IsManualMemo() || f.IsInvariant() || f.Kind == "memo" {
					title := "Architectural Directive"
					if len(claim) > 50 {
						title = claim[:47] + "..."
					} else if claim != "" {
						title = claim
					}
					payload.Memos = append(payload.Memos, EdgeMemo{
						ID:          factID,
						ProjectSlug: project,
						Title:       gateway.Redact(title),
						Directive:   claim,
						UpdatedAt:   now,
						HLC:         store.GenerateHLC(),
					})
				} else {
					conf := f.Confidence
					if conf == "" {
						conf = "high"
					}
					var refs []string
					for _, r := range f.RepoFiles {
						refs = append(refs, gateway.Redact(r))
					}
					payload.Facts = append(payload.Facts, EdgeFact{
						ID:          factID,
						ProjectSlug: project,
						Fact:        claim,
						Confidence:  conf,
						FileRefs:    refs,
						UpdatedAt:   now,
						HLC:         store.GenerateHLC(),
					})
				}
			}
		}
	}

	// 2. Goals from disk
	if repoRoot != "" {
		if audit, err := goals.ScanGoals(repoRoot); err == nil {
			for _, g := range audit.Goals {
				goalMd := readGoalFile(repoRoot, g.Slug, "goal.md")
				planMd := readGoalFile(repoRoot, g.Slug, "plan.md")
				factsMd := readGoalFile(repoRoot, g.Slug, "facts.md")

				if goalMd == "" {
					goalMd = fmt.Sprintf("# Goal: %s\n\n%s", g.Title, g.Description)
				}

				payload.Goals = append(payload.Goals, EdgeGoal{
					ID:          fmt.Sprintf("%s:%s", project, g.Slug),
					ProjectSlug: project,
					Slug:        g.Slug,
					Title:       gateway.Redact(g.Title),
					GoalMd:      gateway.Redact(goalMd),
					PlanMd:      gateway.Redact(planMd),
					FactsMd:     gateway.Redact(factsMd),
					Status:      g.Status,
					UpdatedAt:   now,
					HLC:         store.GenerateHLC(),
				})
			}
		}
	}

	return payload, nil
}

func readGoalFile(repoRoot, slug, fileName string) string {
	path := filepath.Join(repoRoot, "goals", slug, fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
