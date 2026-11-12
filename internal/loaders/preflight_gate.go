package loaders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jadmadi/sila/internal/config"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PreflightPolicy controls the Command Code preflight gate.
type PreflightPolicy string

const (
	PreflightOff       PreflightPolicy = "off"
	PreflightWarn      PreflightPolicy = "warn"
	PreflightEnforce   PreflightPolicy = "enforce"
	PreflightSchema                    = 2
	PreflightMarkerTTL                 = 30 * time.Minute
)

// PreflightMarker is the session-scoped proof that the Sila bridge ran.
type PreflightMarker struct {
	SchemaVersion     int             `json:"schema_version"`
	SessionID         string          `json:"session_id"`
	Project           string          `json:"project"`
	ProjectDir        string          `json:"project_dir"`
	SilaVersion       string          `json:"sila_version"`
	CreatedAt         string          `json:"created_at"`
	DurationMS        int64           `json:"duration_ms"`
	Policy            PreflightPolicy `json:"policy"`
	Status            string          `json:"status"` // ok | degraded | failed | bypassed
	Checks            map[string]bool `json:"checks"`
	Warnings          []string        `json:"warnings"`
	Bypassed          bool            `json:"bypassed"`
	ContextSHA256     string          `json:"context_sha256"`
	PrimeStatus       PrimeStatus     `json:"prime_status,omitempty"`
	PrimeErrorClass   string          `json:"prime_error_class,omitempty"`
	PrimeError        string          `json:"prime_error,omitempty"`
	PrimeAt           string          `json:"prime_at,omitempty"`
	PrimeDurationMS   int64           `json:"prime_duration_ms,omitempty"`
	PrimeSource       PrimeSource     `json:"prime_source,omitempty"`
	BriefBytes        int             `json:"brief_bytes,omitempty"`
	MemoryUnavailable bool            `json:"memory_unavailable,omitempty"`
}

// PreflightDecision is the allow/deny result consumed by Command Code.
type PreflightDecision struct {
	Decision        string `json:"decision"` // allow | deny | warn
	Reason          string `json:"reason,omitempty"`
	Warning         string `json:"warning,omitempty"`
	RecoveryCommand string `json:"recovery_command,omitempty"`
	MarkerPath      string `json:"marker_path,omitempty"`
	Bypassed        bool   `json:"bypassed,omitempty"`
}

// PreflightRequest is the minimal hook input needed by the gate.
type PreflightRequest struct {
	Policy         PreflightPolicy
	SessionID      string
	ProjectDir     string
	ToolName       string
	ToolInput      string
	PermissionMode string
	SessionStart   time.Time
	Now            time.Time
	Bypass         bool
	MarkerPath     string
}

// NormalizePreflightPolicy returns a valid policy or "" for an unknown value.
func NormalizePreflightPolicy(raw string) PreflightPolicy {
	switch PreflightPolicy(strings.ToLower(strings.TrimSpace(raw))) {
	case PreflightOff:
		return PreflightOff
	case PreflightWarn:
		return PreflightWarn
	case PreflightEnforce:
		return PreflightEnforce
	default:
		return ""
	}
}

// ResolvePreflightPolicy applies project > user > warn precedence.
func ResolvePreflightPolicy(projectDir string) PreflightPolicy {
	userDir, _ := os.UserConfigDir()
	return ResolvePreflightPolicyWithUser(projectDir, userDir)
}

// ResolvePreflightPolicyWithUser is the testable precedence resolver.
func ResolvePreflightPolicyWithUser(projectDir, userConfigDir string) PreflightPolicy {
	if projectDir != "" {
		for _, path := range []string{
			filepath.Join(projectDir, ".sila", "config"),
			filepath.Join(projectDir, ".commandcode", "settings.json"),
		} {
			if policy := policyFromFile(path); policy != "" {
				return policy
			}
		}
	}
	if userConfigDir != "" {
		for _, path := range []string{
			filepath.Join(userConfigDir, "sila", "config"),
			filepath.Join(userConfigDir, "sila", "config.json"),
		} {
			if policy := policyFromFile(path); policy != "" {
				return policy
			}
		}
	}
	return PreflightWarn
}

func policyFromFile(path string) PreflightPolicy {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		var root map[string]any
		if json.Unmarshal(data, &root) != nil {
			return ""
		}
		for _, section := range []string{"sila", "command_code", "commandCode"} {
			if m, ok := root[section].(map[string]any); ok {
				if raw, ok := m["preflight_policy"].(string); ok {
					return NormalizePreflightPolicy(raw)
				}
			}
		}
		if raw, ok := root["preflight_policy"].(string); ok {
			return NormalizePreflightPolicy(raw)
		}
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "preflight_policy") {
			continue
		}
		_, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		return NormalizePreflightPolicy(strings.Trim(strings.TrimSpace(value), `"'`))
	}
	return ""
}

// PreflightMarkerPath returns the project-local marker path, sanitizing the
// session id so it cannot traverse or escape the state directory.
func PreflightMarkerPath(projectDir, sessionID string) string {
	if projectDir == "" {
		return ""
	}
	name := sanitizePreflightSession(sessionID)
	return filepath.Join(projectDir, ".sila", "state", "preflight", name+".json")
}

func sanitizePreflightSession(sessionID string) string {
	name := strings.TrimSpace(sessionID)
	if name == "" || name == "." || name == ".." {
		return "unbound"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "." || out == ".." || out == "" {
		return "unbound"
	}
	return out
}

// PreflightContextHash is stable for the same project/session/policy/status.
func PreflightContextHash(project, sessionID string, policy PreflightPolicy, status string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + sessionID + "\x00" + string(policy) + "\x00" + status))
	return hex.EncodeToString(sum[:])
}

// WritePreflightMarker writes the marker atomically with mode 0600.
func WritePreflightMarker(projectDir string, marker PreflightMarker) (string, error) {
	if config.IsForbiddenLocalStateDir(projectDir) {
		return "", nil
	}
	path := PreflightMarkerPath(projectDir, marker.SessionID)
	if path == "" {
		return "", fmt.Errorf("preflight marker: project directory is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	if marker.SchemaVersion == 0 {
		marker.SchemaVersion = PreflightSchema
	}
	if marker.CreatedAt == "" {
		marker.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".preflight-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return path, nil
}

// ReadPreflightMarker reads and parses one marker.
func ReadPreflightMarker(path string) (*PreflightMarker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var marker PreflightMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, err
	}
	return &marker, nil
}

// ValidatePreflightMarker enforces schema, session, age, status, and mode.
func ValidatePreflightMarker(path string, marker *PreflightMarker, sessionID string, sessionStart, now time.Time) error {
	if marker == nil {
		return fmt.Errorf("marker is nil")
	}
	if marker.SchemaVersion != 1 && marker.SchemaVersion != PreflightSchema {
		return fmt.Errorf("unsupported marker schema %d", marker.SchemaVersion)
	}
	if marker.SessionID == "" || marker.SessionID != sessionID {
		return fmt.Errorf("marker belongs to another session")
	}
	switch marker.Status {
	case "ok", "degraded", "bypassed":
	default:
		return fmt.Errorf("marker status %q is not usable", marker.Status)
	}
	if marker.SchemaVersion < 2 || marker.PrimeStatus == "" {
		return fmt.Errorf("marker has no prime status (legacy or unknown)")
	}
	if marker.PrimeStatus != PrimeStatusOK {
		return fmt.Errorf("prime status is %s", marker.PrimeStatus)
	}
	if marker.Checks == nil || !marker.Checks["prime"] {
		return fmt.Errorf("prime check is false")
	}
	if marker.MemoryUnavailable {
		return fmt.Errorf("prime memory is unavailable")
	}
	created, err := time.Parse(time.RFC3339, marker.CreatedAt)
	if err != nil {
		return fmt.Errorf("marker created_at is invalid")
	}
	if !sessionStart.IsZero() && created.Before(sessionStart.Add(-2*time.Second)) {
		return fmt.Errorf("marker predates this session")
	}
	if !now.IsZero() && now.Sub(created) > PreflightMarkerTTL {
		return fmt.Errorf("marker is older than %s", PreflightMarkerTTL)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("marker path is a symlink")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("marker mode is %o, want 600", info.Mode().Perm())
	}
	if marker.Checks == nil {
		return fmt.Errorf("marker checks are missing")
	}
	return nil
}

// EvaluatePreflight decides whether a tool call may proceed. It never spawns a
// subprocess, reads tool input as data, or writes outside the marker/warn state.
func EvaluatePreflight(req PreflightRequest) PreflightDecision {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	policy := req.Policy
	if policy == "" {
		policy = PreflightWarn
	}
	if policy == PreflightOff {
		return PreflightDecision{Decision: "allow", Reason: "preflight policy is off"}
	}
	if strings.EqualFold(req.PermissionMode, "plan") {
		return PreflightDecision{Decision: "allow", Warning: "preflight gate skipped in plan mode"}
	}
	if preflightToolAllowed(req.ToolName, req.ToolInput) {
		return PreflightDecision{Decision: "allow", Reason: "preflight command or Sila MCP tool"}
	}
	markerPath := req.MarkerPath
	if markerPath == "" {
		markerPath = PreflightMarkerPath(req.ProjectDir, req.SessionID)
	}
	if policy == PreflightEnforce && req.Bypass {
		return PreflightDecision{
			Decision: "allow", Warning: "SILA_PREFLIGHT_BYPASS=1 bypassed the preflight gate",
			MarkerPath: markerPath, Bypassed: true,
		}
	}

	decision := PreflightDecision{MarkerPath: markerPath, RecoveryCommand: "sila hook session-start"}
	reason := ""
	if markerPath == "" {
		reason = "preflight marker path is unavailable"
	} else if marker, err := ReadPreflightMarker(markerPath); err != nil {
		reason = "preflight marker is missing or unreadable; run sila hook session-start"
	} else if err := ValidatePreflightMarker(markerPath, marker, req.SessionID, req.SessionStart, now); err != nil {
		reason = "preflight marker is invalid: " + err.Error()
	} else if !preflightProjectMatches(marker, req.ProjectDir) {
		reason = "preflight marker belongs to another project"
	}
	if reason == "" {
		return PreflightDecision{Decision: "allow", MarkerPath: markerPath}
	}
	decision.Reason = reason
	if policy == PreflightEnforce {
		decision.Decision = "deny"
		return decision
	}
	decision.Decision = "warn"
	if !warnOnce(req.ProjectDir, req.SessionID) {
		decision.Warning = ""
	} else {
		decision.Warning = reason
	}
	return decision
}

func preflightProjectMatches(marker *PreflightMarker, projectDir string) bool {
	if marker == nil || strings.TrimSpace(projectDir) == "" || strings.TrimSpace(marker.ProjectDir) == "" {
		return true
	}
	a, errA := os.Stat(marker.ProjectDir)
	b, errB := os.Stat(projectDir)
	if errA == nil && errB == nil {
		return os.SameFile(a, b)
	}
	absA, _ := filepath.Abs(marker.ProjectDir)
	absB, _ := filepath.Abs(projectDir)
	return strings.EqualFold(filepath.Clean(absA), filepath.Clean(absB))
}

func preflightToolAllowed(toolName, toolInput string) bool {
	if strings.HasPrefix(toolName, "mcp__sila__") {
		return true
	}
	command := preflightCommand(toolInput)
	if command == "" {
		return false
	}
	for _, allowed := range []string{
		"sila hook session-start", "sila prime", "sila salam",
		"sila heal --dry-run", "sila goals ready", "sila recall",
		"sila doc search", "preflight",
	} {
		if command == allowed || strings.HasPrefix(command, allowed+" ") {
			return true
		}
	}
	return false
}

func preflightCommand(toolInput string) string {
	toolInput = strings.TrimSpace(toolInput)
	if toolInput == "" {
		return ""
	}
	var value any
	if json.Unmarshal([]byte(toolInput), &value) == nil {
		if m, ok := value.(map[string]any); ok {
			for _, key := range []string{"command", "cmd", "script"} {
				if s, ok := m[key].(string); ok {
					return strings.TrimSpace(s)
				}
			}
			return ""
		}
		if s, ok := value.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	return toolInput
}

func warnOnce(projectDir, sessionID string) bool {
	if projectDir == "" || config.IsForbiddenLocalStateDir(projectDir) {
		return true
	}
	path := filepath.Join(projectDir, ".sila", "state", "preflight", sanitizePreflightSession(sessionID)+".warn")
	if _, err := os.Stat(path); err == nil {
		return false
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return true
	}
	_ = os.Chmod(dir, 0o700)
	if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return true
	}
	return true
}
