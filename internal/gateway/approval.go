package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// ApprovalRequest defines the parameters for requesting human intervention.
type ApprovalRequest struct {
	Project        string            `json:"project"`
	Agent          string            `json:"agent"`
	Prompt         string            `json:"prompt"`
	RiskLevel      string            `json:"risk_level"` // "low", "medium", "high", "critical"
	Metadata       map[string]string `json:"metadata,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

// ApprovalResponse represents the outcome of an approval request.
type ApprovalResponse struct {
	ID         string `json:"id"`
	Approved   bool   `json:"approved"`
	Status     string `json:"status"` // "approved", "rejected", "timed_out", "cancelled"
	DecidedBy  string `json:"decided_by,omitempty"`
	Reason     string `json:"reason,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// ApprovalCoordinator coordinates synchronous human-in-the-loop approvals across external gateways and local CLI.
type ApprovalCoordinator struct {
	mu       sync.RWMutex
	waiters  map[string]chan ApprovalResponse
	store    *store.Store
	registry *Registry
}

// Global approval coordinator instance for cross-package and CLI access.
var (
	defaultCoordinatorMu sync.RWMutex
	defaultCoordinator   *ApprovalCoordinator
)

// SetDefaultCoordinator registers the active global coordinator.
func SetDefaultCoordinator(c *ApprovalCoordinator) {
	defaultCoordinatorMu.Lock()
	defer defaultCoordinatorMu.Unlock()
	defaultCoordinator = c
}

// GetDefaultCoordinator returns the global coordinator, creating a fallback one if necessary.
func GetDefaultCoordinator(st *store.Store, reg *Registry) *ApprovalCoordinator {
	defaultCoordinatorMu.Lock()
	defer defaultCoordinatorMu.Unlock()
	if defaultCoordinator == nil {
		defaultCoordinator = NewApprovalCoordinator(st, reg)
	} else {
		if st != nil {
			defaultCoordinator.SetStore(st)
		}
		if reg != nil {
			defaultCoordinator.SetRegistry(reg)
		}
	}
	return defaultCoordinator
}

// NewApprovalCoordinator creates a new human approval coordinator.
func NewApprovalCoordinator(st *store.Store, reg *Registry) *ApprovalCoordinator {
	return &ApprovalCoordinator{
		waiters:  make(map[string]chan ApprovalResponse),
		store:    st,
		registry: reg,
	}
}

// Store safely returns the underlying store pointer under read-lock.
func (c *ApprovalCoordinator) Store() *store.Store {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.store
}

// SetStore safely updates the underlying store pointer under lock.
func (c *ApprovalCoordinator) SetStore(st *store.Store) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store = st
}

// Registry safely returns the underlying gateway registry pointer under read-lock.
func (c *ApprovalCoordinator) Registry() *Registry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry
}

// SetRegistry safely updates the underlying gateway registry pointer under lock.
func (c *ApprovalCoordinator) SetRegistry(reg *Registry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registry = reg
}

// GenerateApprovalID produces a cryptographically random, human-distinguishable approval identifier.
func GenerateApprovalID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("appr_%s", hex.EncodeToString(b))
}

// RequestApproval initiates an approval request, notifies all enabled surfaces, and blocks synchronously until decision or timeout.
func (c *ApprovalCoordinator) RequestApproval(ctx context.Context, req ApprovalRequest) (*ApprovalResponse, error) {
	start := time.Now()

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required for approval request")
	}

	timeoutSec := req.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 300 // 5 minute default
	} else if timeoutSec > 3600 {
		timeoutSec = 3600 // 1 hour cap
	}

	risk := strings.ToLower(strings.TrimSpace(req.RiskLevel))
	if risk == "" {
		risk = "medium"
	}

	// 1. Redact secrets from prompt and metadata before sending out
	cleanPrompt := Redact(prompt)
	cleanMeta := make(map[string]string, len(req.Metadata)+3)
	for k, v := range req.Metadata {
		cleanMeta[Redact(k)] = Redact(v)
	}

	id := GenerateApprovalID()
	cleanMeta["approval_id"] = id
	cleanMeta["risk_level"] = risk
	if req.Agent != "" {
		cleanMeta["agent"] = Redact(req.Agent)
	}

	// 2. Persist in sila.db if store is available
	st := c.Store()
	if st != nil {
		apprRecord := &store.Approval{
			ID:             id,
			Project:        req.Project,
			Agent:          req.Agent,
			Prompt:         cleanPrompt,
			RiskLevel:      risk,
			Metadata:       cleanMeta,
			TimeoutSeconds: timeoutSec,
			Status:         "pending",
			CreatedAt:      start,
		}
		if err := st.InsertApproval(apprRecord); err != nil {
			return nil, fmt.Errorf("failed to persist approval request: %w", err)
		}
	}

	// 3. Register waiter channel
	waitCh := make(chan ApprovalResponse, 1)
	c.mu.Lock()
	c.waiters[id] = waitCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
	}()

	// 4. Broadcast notification to external surfaces (Telegram, Discord, MCP)
	reg := c.Registry()
	if reg != nil {
		level := "warn"
		if risk == "high" || risk == "critical" {
			level = "error"
		}
		notif := Notification{
			Title:    fmt.Sprintf("Approval Required: %s", cleanPrompt),
			Body:     fmt.Sprintf("Agent %q is requesting approval (%s risk).\nTo decide via CLI run:\n  sila approve %s [--reject]", req.Agent, strings.ToUpper(risk), id),
			Level:    level,
			Project:  req.Project,
			Category: "approval",
			Metadata: cleanMeta,
		}
		// Non-blocking broadcast
		go func() {
			broadcastCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = reg.Broadcast(broadcastCtx, notif)
		}()
	}

	// 5. Synchronous wait with timeout, store polling ticker, and context cancellation
	timeoutDuration := time.Duration(timeoutSec) * time.Second
	timer := time.NewTimer(timeoutDuration)
	defer timer.Stop()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case res := <-waitCh:
			res.DurationMs = time.Since(start).Milliseconds()
			return &res, nil

		case <-ticker.C:
			if curSt := c.Store(); curSt != nil {
				appr, err := curSt.GetApproval(id)
				if err == nil && appr != nil && appr.Status != "pending" {
					return &ApprovalResponse{
						ID:         appr.ID,
						Approved:   appr.Status == "approved",
						Status:     appr.Status,
						DecidedBy:  appr.DecidedBy,
						Reason:     appr.DecisionReason,
						DurationMs: time.Since(start).Milliseconds(),
					}, nil
				}
			}

		case <-timer.C:
			if curSt := c.Store(); curSt != nil {
				_ = curSt.UpdateApprovalDecision(id, "timed_out", "system:timeout", "Approval request expired")
			}
			return &ApprovalResponse{
				ID:         id,
				Approved:   false,
				Status:     "timed_out",
				DecidedBy:  "system:timeout",
				Reason:     "Approval request expired",
				DurationMs: time.Since(start).Milliseconds(),
			}, nil

		case <-ctx.Done():
			if curSt := c.Store(); curSt != nil {
				_ = curSt.UpdateApprovalDecision(id, "cancelled", "system:cancelled", ctx.Err().Error())
			}
			return &ApprovalResponse{
				ID:         id,
				Approved:   false,
				Status:     "cancelled",
				DecidedBy:  "system:cancelled",
				Reason:     ctx.Err().Error(),
				DurationMs: time.Since(start).Milliseconds(),
			}, ctx.Err()
		}
	}
}

// Resolve processes a human or surface decision (approve or reject) for an open approval request.
func (c *ApprovalCoordinator) Resolve(id string, approved bool, decidedBy string, reason string) (*ApprovalResponse, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("approval id is required")
	}

	status := "approved"
	if !approved {
		status = "rejected"
	}
	if strings.TrimSpace(decidedBy) == "" {
		decidedBy = "human"
	}

	// 1. Update in store
	if st := c.Store(); st != nil {
		if err := st.UpdateApprovalDecision(id, status, decidedBy, reason); err != nil {
			return nil, err
		}
	}

	res := ApprovalResponse{
		ID:        id,
		Approved:  approved,
		Status:    status,
		DecidedBy: decidedBy,
		Reason:    reason,
	}

	// 2. Unblock waiting goroutine if active
	c.mu.Lock()
	if ch, ok := c.waiters[id]; ok {
		select {
		case ch <- res:
		default:
		}
		delete(c.waiters, id)
	}
	c.mu.Unlock()

	// 3. Broadcast decision to external surfaces
	if reg := c.Registry(); reg != nil {
		verb := "APPROVED"
		level := "success"
		if !approved {
			verb = "REJECTED"
			level = "warn"
		}
		decisionNotif := Notification{
			Title:    fmt.Sprintf("Approval %s [%s]", verb, id),
			Body:     fmt.Sprintf("Decision recorded by %s%s", decidedBy, formatReason(reason)),
			Level:    level,
			Category: "approval_decision",
			Metadata: map[string]string{
				"approval_id": id,
				"status":      status,
				"decided_by":  decidedBy,
			},
		}
		go func() {
			bCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = reg.Broadcast(bCtx, decisionNotif)
		}()
	}

	return &res, nil
}

func formatReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return fmt.Sprintf("\nReason: %s", reason)
}

// Get retrieves an approval by ID.
func (c *ApprovalCoordinator) Get(id string) (*store.Approval, error) {
	st := c.Store()
	if st == nil {
		return nil, fmt.Errorf("store is not initialized")
	}
	return st.GetApproval(id)
}

// ListPending returns all currently pending approvals.
func (c *ApprovalCoordinator) ListPending(project string) ([]store.Approval, error) {
	st := c.Store()
	if st == nil {
		return nil, fmt.Errorf("store is not initialized")
	}
	return st.ListPendingApprovals(project)
}

// List returns recent approvals up to limit.
func (c *ApprovalCoordinator) List(project string, limit int) ([]store.Approval, error) {
	st := c.Store()
	if st == nil {
		return nil, fmt.Errorf("store is not initialized")
	}
	return st.ListApprovals(project, limit)
}
