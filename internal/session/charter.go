package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
)

// SubagentCharter is a local, signed capability grant for one child agent.
type SubagentCharter struct {
	Schema              string   `json:"schema"`
	CharterID           string   `json:"charter_id"`
	ParentSessionID     string   `json:"parent_session_id"`
	SubagentID          string   `json:"subagent_id"`
	Harness             string   `json:"harness,omitempty"`
	Agent               string   `json:"agent,omitempty"`
	ProjectID           string   `json:"project_id"`
	GoalSlug            string   `json:"goal_slug,omitempty"`
	ClaimID             string   `json:"claim_id,omitempty"`
	Lane                string   `json:"lane,omitempty"`
	AllowPaths          []string `json:"allow_paths"`
	DenyPaths           []string `json:"deny_paths"`
	MemoryVerbsRequired []string `json:"memory_verbs_required"`
	HandoffPolicy       string   `json:"handoff_policy"`
	CreatedAt           string   `json:"created_at"`
	ExpiresAt           string   `json:"expires_at"`
	Signature           string   `json:"signature,omitempty"`
}

// CharterError is a typed charter rejection.
type CharterError struct {
	Code    string
	Message string
}

func (e *CharterError) Error() string { return e.Code + ": " + e.Message }

func charterErr(code, message string) error { return &CharterError{Code: code, Message: message} }

const CharterSchema = "sila/subagent-charter/v1"

// CharterKeyPath returns the local signing key path for a project.
func CharterKeyPath(projectDir string) string {
	return filepath.Join(projectDir, ".sila", "state", "charter.key")
}

// LoadOrCreateCharterKey reads the local key, creating it with 0600 on first use.
func LoadOrCreateCharterKey(projectDir string) ([]byte, error) {
	path := CharterKeyPath(projectDir)
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return data, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func canonicalCharter(charter SubagentCharter) ([]byte, error) {
	charter.Signature = ""
	return json.Marshal(charter)
}

// SignCharter returns the charter with an hmac-sha256 signature.
func SignCharter(charter SubagentCharter, key []byte) (SubagentCharter, error) {
	if len(key) == 0 {
		return charter, charterErr("CHARTER_INVALID", "signing key is empty")
	}
	charter.Schema = CharterSchema
	payload, err := canonicalCharter(charter)
	if err != nil {
		return charter, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	charter.Signature = "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
	return charter, nil
}

// VerifyCharter checks schema, signature, identity, expiry, and path rules.
func VerifyCharter(charter SubagentCharter, key []byte) error {
	if charter.Schema != CharterSchema {
		return charterErr("CHARTER_INVALID", "unsupported schema")
	}
	if charter.ParentSessionID == "" || charter.SubagentID == "" || charter.ProjectID == "" {
		return charterErr("CHARTER_INVALID", "parent, subagent, and project are required")
	}
	if !strings.HasPrefix(charter.Signature, "hmac-sha256:") {
		return charterErr("CHARTER_SIGNATURE", "signature is missing")
	}
	expected := charter
	expected.Signature = ""
	payload, err := canonicalCharter(expected)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if !hmac.Equal([]byte(charter.Signature), []byte("hmac-sha256:"+hex.EncodeToString(mac.Sum(nil)))) {
		return charterErr("CHARTER_SIGNATURE", "signature mismatch")
	}
	created, err := time.Parse(time.RFC3339, charter.CreatedAt)
	if err != nil {
		return charterErr("CHARTER_INVALID", "created_at is invalid")
	}
	expires, err := time.Parse(time.RFC3339, charter.ExpiresAt)
	if err != nil {
		return charterErr("CHARTER_INVALID", "expires_at is invalid")
	}
	if expires.Sub(created) > 24*time.Hour {
		return charterErr("CHARTER_INVALID", "charter lifetime exceeds 24h")
	}
	if time.Now().UTC().After(expires) {
		return charterErr("CHARTER_EXPIRED", "charter expired")
	}
	for _, path := range append(append([]string{}, charter.AllowPaths...), charter.DenyPaths...) {
		if err := validateCharterPathShape(path); err != nil {
			return err
		}
	}
	return nil
}

func validateCharterPathShape(path string) error {
	if strings.TrimSpace(path) == "" {
		return charterErr("CHARTER_INVALID", "path is empty")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return charterErr("CHARTER_PATH_DENIED", "absolute paths are not allowed")
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return charterErr("CHARTER_PATH_DENIED", "path traversal is not allowed")
	}
	return nil
}

// ValidatePath applies allow/deny rules to one repo-relative path.
func (c SubagentCharter) ValidatePath(path string) error {
	if err := validateCharterPathShape(path); err != nil {
		return err
	}
	goalCharter := &goals.GoalCharter{
		AllowPaths: append([]string{}, c.AllowPaths...),
		DenyPaths:  append(append([]string{}, c.DenyPaths...), goals.DefaultInviolableDenyPaths...),
	}
	if err := goals.ValidateStagedFilesAgainstCharter(goalCharter, []string{path}); err != nil {
		return charterErr("CHARTER_PATH_DENIED", err.Error())
	}
	return nil
}

// LoadCharter reads one charter file.
func LoadCharter(path string) (SubagentCharter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SubagentCharter{}, charterErr("CHARTER_INVALID", err.Error())
	}
	var charter SubagentCharter
	if err := json.Unmarshal(data, &charter); err != nil {
		return SubagentCharter{}, charterErr("CHARTER_INVALID", "invalid JSON")
	}
	return charter, nil
}

// SaveCharter writes a charter atomically with mode 0600.
func SaveCharter(path string, charter SubagentCharter) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	data, err := json.MarshalIndent(charter, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".charter-*")
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

// NewSubagentCharter builds and signs a charter with a 24h expiry.
func NewSubagentCharter(projectDir, projectID, parentSession, subagentID, harness, agent, goalSlug, lane string, allowPaths []string, handoffPolicy string) (SubagentCharter, string, error) {
	if parentSession == "" || subagentID == "" || projectID == "" {
		return SubagentCharter{}, "", charterErr("CHARTER_INVALID", "parent session, subagent, and project are required")
	}
	if handoffPolicy == "" {
		handoffPolicy = "parent-only"
	}
	if handoffPolicy != "parent-only" && handoffPolicy != "subagent-allowed" {
		return SubagentCharter{}, "", charterErr("CHARTER_INVALID", "handoff policy must be parent-only or subagent-allowed")
	}
	now := time.Now().UTC()
	charter := SubagentCharter{
		Schema:              CharterSchema,
		CharterID:           "sub_" + subagentID,
		ParentSessionID:     parentSession,
		SubagentID:          subagentID,
		Harness:             harness,
		Agent:               agent,
		ProjectID:           projectID,
		GoalSlug:            goalSlug,
		Lane:                lane,
		AllowPaths:          allowPaths,
		DenyPaths:           append([]string{}, goals.DefaultInviolableDenyPaths...),
		MemoryVerbsRequired: []string{"prime", "recall", "memo", "lesson", "facts", "gate", "handoff"},
		HandoffPolicy:       handoffPolicy,
		CreatedAt:           now.Format(time.RFC3339),
		ExpiresAt:           now.Add(24 * time.Hour).Format(time.RFC3339),
	}
	key, err := LoadOrCreateCharterKey(projectDir)
	if err != nil {
		return SubagentCharter{}, "", err
	}
	charter, err = SignCharter(charter, key)
	if err != nil {
		return SubagentCharter{}, "", err
	}
	path := filepath.Join(projectDir, ".sila", "state", "charters", subagentID+".json")
	if err := SaveCharter(path, charter); err != nil {
		return SubagentCharter{}, "", err
	}
	return charter, path, nil
}

// CharterFromEnvOrArgs loads a charter from --charter=<file> or SILA_CHARTER.
func CharterFromEnvOrArgs(args []string) (SubagentCharter, string, error) {
	path := strings.TrimSpace(os.Getenv("SILA_CHARTER"))
	for _, arg := range args {
		if strings.HasPrefix(arg, "--charter=") {
			path = strings.TrimPrefix(arg, "--charter=")
		}
	}
	if path == "" {
		return SubagentCharter{}, "", nil
	}
	charter, err := LoadCharter(path)
	if err != nil {
		return SubagentCharter{}, path, err
	}
	key, err := LoadOrCreateCharterKey(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path)))))
	if err != nil {
		return SubagentCharter{}, path, err
	}
	if err := VerifyCharter(charter, key); err != nil {
		return SubagentCharter{}, path, err
	}
	return charter, path, nil
}

// OriginRole returns the fact origin role for a child write.
func (c SubagentCharter) OriginRole() string { return "subagent:" + c.SubagentID }

// Summary returns a short human line.
func (c SubagentCharter) Summary() string {
	return fmt.Sprintf("%s parent=%s goal=%s lane=%s expires=%s", c.SubagentID, c.ParentSessionID, c.GoalSlug, c.Lane, c.ExpiresAt)
}
