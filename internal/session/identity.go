// Package session resolves and persists local agent session identity.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Identity is the local, non-telemetry identity attached to writes and hooks.
type Identity struct {
	SessionID       string `json:"session_id"`
	ProjectID       string `json:"project_id"`
	Harness         string `json:"harness"`
	Agent           string `json:"agent"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	ActiveGoal      string `json:"active_goal,omitempty"`
	ClaimID         string `json:"claim_id,omitempty"`
	Source          string `json:"source"` // flag|env|current-file|global-current|none
	Cwd             string `json:"cwd"`
	PID             int    `json:"pid"`
}

const currentValidity = 24 * time.Hour

// GitContext is the branch/HEAD snapshot a session owns.
type GitContext struct {
	Branch   string `json:"branch,omitempty"`
	Head     string `json:"head,omitempty"`
	Detached bool   `json:"detached,omitempty"`
}

// SnapshotGit reads the current branch and commit. A missing repository or a
// detached HEAD yields an empty branch; detached HEAD keeps the commit.
func SnapshotGit(cwd string) GitContext {
	if strings.TrimSpace(cwd) == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if strings.TrimSpace(cwd) == "" {
		return GitContext{}
	}
	branch := gitValue(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	head := gitValue(cwd, "rev-parse", "HEAD")
	if branch == "HEAD" {
		return GitContext{Head: head, Detached: true}
	}
	return GitContext{Branch: branch, Head: head}
}

func gitValue(cwd string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// IsZero reports whether no snapshot could be resolved.
func (g GitContext) IsZero() bool {
	return g.Branch == "" && g.Head == ""
}

// Label renders a compact human form.
func (g GitContext) Label() string {
	branch := g.Branch
	if branch == "" {
		branch = "detached"
	}
	head := g.Head
	if len(head) > 7 {
		head = head[:7]
	}
	if head == "" {
		return branch
	}
	return branch + "@" + head
}

// DriftFrom describes how g differs from prev, or "" when unchanged.
func (g GitContext) DriftFrom(prev GitContext) string {
	if g.Branch == prev.Branch && g.Head == prev.Head && g.Detached == prev.Detached {
		return ""
	}
	return fmt.Sprintf("%s -> %s", prev.Label(), g.Label())
}

// ResumeContext keeps the session's own snapshot. The current cwd is used only
// when the session has no snapshot or its recorded commit no longer exists
// (legacy/stale); a merely different current branch is recorded as drift but
// never adopted, so a resumed session cannot inherit a sibling's branch.
func ResumeContext(prev GitContext, cwd string) (GitContext, string) {
	current := SnapshotGit(cwd)
	if prev.IsZero() {
		if current.IsZero() {
			return prev, ""
		}
		return current, "no previous snapshot; resolved " + current.Label()
	}
	if current.IsZero() {
		return prev, ""
	}
	if prev.Head == "" || !gitCommitExists(cwd, prev.Head) {
		return current, "snapshot " + prev.Label() + " is gone; resolved " + current.Label()
	}
	if drift := current.DriftFrom(prev); drift != "" {
		return prev, drift
	}
	return prev, ""
}

func gitCommitExists(cwd, head string) bool {
	if strings.TrimSpace(head) == "" {
		return false
	}
	cmd := exec.Command("git", "-C", cwd, "cat-file", "-e", head+"^{commit}")
	return cmd.Run() == nil
}

type currentFile struct {
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id"`
	Harness   string `json:"harness"`
	WrittenAt string `json:"written_at"`
}

// ResolveIdentity applies the deterministic precedence: flag, environment,
// project current pointer, global current pointer, none. It is read-only.
func ResolveIdentity(explicitSession, projectID, cwd string) (Identity, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Identity{}, err
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return Identity{}, err
	}
	id := Identity{
		ProjectID:  projectID,
		Harness:    os.Getenv("SILA_HARNESS"),
		Agent:      os.Getenv("SILA_AGENT"),
		ActiveGoal: os.Getenv("SILA_ACTIVE_GOAL"),
		ClaimID:    os.Getenv("SILA_CLAIM_ID"),
		Cwd:        abs,
		PID:        os.Getpid(),
		Source:     "none",
	}
	if explicitSession = strings.TrimSpace(explicitSession); explicitSession != "" {
		id.SessionID = explicitSession
		id.Source = "flag"
		return id, nil
	}
	if envSession := strings.TrimSpace(os.Getenv("SILA_SESSION_ID")); envSession != "" {
		id.SessionID = envSession
		id.Source = "env"
		return id, nil
	}
	if applyCurrent(&id, ProjectCurrentPath(abs), projectID, "current-file") {
		return id, nil
	}
	if applyCurrent(&id, GlobalCurrentPath(projectID), projectID, "global-current") {
		return id, nil
	}
	return id, nil
}

func applyCurrent(id *Identity, path, projectID, source string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cf currentFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return false
	}
	if strings.TrimSpace(cf.SessionID) == "" {
		return false
	}
	if projectID != "" && cf.ProjectID != "" && cf.ProjectID != projectID {
		return false
	}
	if projectID == "" && cf.ProjectID != "" {
		id.ProjectID = cf.ProjectID
	}
	written, err := time.Parse(time.RFC3339, cf.WrittenAt)
	if err != nil || time.Since(written) > currentValidity || time.Until(written) > currentValidity {
		return false
	}
	id.SessionID = cf.SessionID
	id.Source = source
	if cf.Harness != "" {
		id.Harness = cf.Harness
	}
	return true
}

// ProjectCurrentPath is the project-local current pointer.
func ProjectCurrentPath(cwd string) string {
	return filepath.Join(cwd, ".sila", "state", "sessions", "current")
}

// GlobalCurrentPath is the per-project state pointer outside the repository.
func GlobalCurrentPath(projectID string) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	name := strings.ReplaceAll(strings.TrimSpace(projectID), string(filepath.Separator), "_")
	if name == "" {
		name = "default"
	}
	return filepath.Join(base, "sila", "sessions", "current-"+name+".json")
}

// WriteCurrent atomically writes the project current pointer with 0600/0700
// permissions.
func WriteCurrent(cwd string, id Identity) error {
	path := ProjectCurrentPath(cwd)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	payload, err := json.Marshal(currentFile{
		SessionID: id.SessionID,
		ProjectID: id.ProjectID,
		Harness:   id.Harness,
		WrittenAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".current-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ClearCurrent removes the project pointer only when it belongs to sessionID.
func ClearCurrent(cwd, projectID, sessionID string) error {
	path := ProjectCurrentPath(cwd)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var cf currentFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil
	}
	if cf.SessionID != sessionID {
		return nil
	}
	if projectID != "" && cf.ProjectID != "" && cf.ProjectID != projectID {
		return nil
	}
	return os.Remove(path)
}

// GenerateSessionID returns a local random identifier (never derived from
// user content).
func GenerateSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("session-%d", time.Now().UnixNano())
	}
	return "sess-" + hex.EncodeToString(b[:])
}
