package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Binding links a harness session to a Sila goal.
type Binding struct {
	Schema         string    `json:"schema"`
	Harness        HarnessID `json:"harness"`
	SessionID      string    `json:"session_id"`
	HarnessGoalID  string    `json:"harness_goal_id,omitempty"`
	SilaGoalSlug   string    `json:"sila_goal_slug"`
	ClaimID        string    `json:"claim_id,omitempty"`
	Source         string    `json:"source"` // explicit | harness-native | claim
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	BindingVersion string    `json:"version,omitempty"`
}

// Waiver permits named contract checks to be skipped for a bounded time.
type Waiver struct {
	Schema    string    `json:"schema"`
	Harness   HarnessID `json:"harness,omitempty"`
	SessionID string    `json:"session_id"`
	Checks    []string  `json:"checks,omitempty"` // empty means all checks
	Reason    string    `json:"reason"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Covers reports whether the waiver applies to a check name.
func (w Waiver) Covers(check string) bool {
	if len(w.Checks) == 0 {
		return true
	}
	for _, c := range w.Checks {
		if c == "*" || strings.EqualFold(strings.TrimSpace(c), check) {
			return true
		}
	}
	return false
}

// Expired reports whether the waiver is no longer valid.
func (w Waiver) Expired(now time.Time) bool {
	return !w.ExpiresAt.IsZero() && now.After(w.ExpiresAt)
}

// Receipt is the privacy-safe record of one contract decision.
type Receipt struct {
	Schema          string                 `json:"schema"`
	Harness         HarnessID              `json:"harness"`
	SessionID       string                 `json:"session_id"`
	ParentSessionID string                 `json:"parent_session_id,omitempty"`
	ProjectID       string                 `json:"project_id"`
	SilaGoalSlug    string                 `json:"sila_goal_slug,omitempty"`
	ClaimID         string                 `json:"claim_id,omitempty"`
	Decision        string                 `json:"decision"`
	ReasonCode      string                 `json:"reason_code,omitempty"`
	Checks          map[string]CheckResult `json:"checks"`
	Policy          Policy                 `json:"policy"`
	Bypassed        bool                   `json:"bypassed,omitempty"`
	Capabilities    map[string]bool        `json:"capabilities,omitempty"`
	DurationMS      int64                  `json:"duration_ms"`
	Timestamp       time.Time              `json:"timestamp"`
}

// BindingPath returns the binding path for a harness/session.
func BindingPath(projectDir string, harness HarnessID, sessionID string) string {
	if harness == "" {
		harness = "unknown"
	}
	return filepath.Join(projectDir, ".sila", "state", "harness-goals", sanitizeID(string(harness))+"-"+sanitizeID(sessionID)+".json")
}

// WaiverPath returns the waiver path for a harness/session.
func WaiverPath(projectDir string, harness HarnessID, sessionID string) string {
	if harness == "" {
		harness = "unknown"
	}
	return filepath.Join(projectDir, ".sila", "state", "goals", "waivers", sanitizeID(string(harness))+"-"+sanitizeID(sessionID)+".json")
}

// ReceiptPath returns the receipt path for a harness/session.
func ReceiptPath(projectDir string, harness HarnessID, sessionID string) string {
	if harness == "" {
		harness = "unknown"
	}
	return filepath.Join(projectDir, ".sila", "state", "goal-contract", sanitizeID(string(harness))+"-"+sanitizeID(sessionID)+".json")
}

// LoadBinding reads a binding. A missing file returns (nil, nil).
func LoadBinding(projectDir string, harness HarnessID, sessionID string) (*Binding, error) {
	var b Binding
	if err := readJSON(BindingPath(projectDir, harness, sessionID), &b); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// SaveBinding atomically writes a binding with 0600 permissions.
func SaveBinding(projectDir string, b Binding) error {
	if strings.TrimSpace(b.SilaGoalSlug) == "" {
		return errors.New("sila_goal_slug is required")
	}
	if b.Schema == "" {
		b.Schema = BindingSchema
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	if b.ExpiresAt.IsZero() {
		b.ExpiresAt = b.CreatedAt.Add(24 * time.Hour)
	}
	return writeJSON(BindingPath(projectDir, b.Harness, b.SessionID), b)
}

// RemoveBinding removes a binding if present.
func RemoveBinding(projectDir string, harness HarnessID, sessionID string) error {
	err := os.Remove(BindingPath(projectDir, harness, sessionID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// LoadWaiver reads a waiver. A missing file returns (nil, nil).
func LoadWaiver(projectDir string, harness HarnessID, sessionID string) (*Waiver, error) {
	var w Waiver
	if err := readJSON(WaiverPath(projectDir, harness, sessionID), &w); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

// SaveWaiver atomically writes a waiver with 0600 permissions.
func SaveWaiver(projectDir string, w Waiver) error {
	if strings.TrimSpace(w.Reason) == "" {
		return errors.New("waiver reason is required")
	}
	if w.Schema == "" {
		w.Schema = WaiverSchema
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	if w.ExpiresAt.IsZero() {
		w.ExpiresAt = w.CreatedAt.Add(7 * 24 * time.Hour)
	}
	return writeJSON(WaiverPath(projectDir, w.Harness, w.SessionID), w)
}

// SaveReceipt writes the current receipt and archives the prior one.
func SaveReceipt(projectDir string, r Receipt) (string, error) {
	if r.Schema == "" {
		r.Schema = ReceiptSchema
	}
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now().UTC()
	}
	path := ReceiptPath(projectDir, r.Harness, r.SessionID)
	if old, err := os.ReadFile(path); err == nil && len(old) > 0 {
		archive := filepath.Join(filepath.Dir(path), "receipts", sanitizeID(string(r.Harness))+"-"+sanitizeID(r.SessionID)+"-"+r.Timestamp.UTC().Format("20060102T150405.000000000Z")+".json")
		if err := writeRaw(archive, old); err != nil {
			return "", err
		}
	}
	if err := writeJSON(path, r); err != nil {
		return "", err
	}
	return path, nil
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeRaw(path, data)
}

func writeRaw(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func sanitizeID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unbound"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}
