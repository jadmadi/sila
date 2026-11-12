package skills

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultLockTimeout specifies the maximum duration to wait for the config lock.
const DefaultLockTimeout = 5 * time.Second

// ErrLockTimeout indicates that the lock could not be acquired within the timeout.
var ErrLockTimeout = errors.New("timeout acquiring skill configuration lock")

// ErrLockBusy indicates that another process currently holds the lock.
var ErrLockBusy = errors.New("skill configuration lock is held by another process")

// UnlockFunc releases the acquired lock.
type UnlockFunc func()

// FileLock represents an acquired advisory lock on a lockfile.
type FileLock struct {
	Path     string
	file     *os.File
	Token    string
	PID      int
	unlocked bool
}

// LockInfo stores diagnostic metadata inside the lock file.
type LockInfo struct {
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Token     string    `json:"token"`
}

// AdapterOptions configures preview, apply, and remove operations.
type AdapterOptions struct {
	Harness   string   // "antigravity" (default), "claude-code", "codex", "opencode", or "gemini"
	Force     bool     // override collision detection or unmanaged removal
	DryRun    bool     // simulate without writing
	Skills    []string // explicit skills override (if empty, derives from assessment/policy)
	Exclude   []string // explicit exclude override
	ProjectID string   // project identifier
	Toolchain string   // toolchain identifier, defaults to "antigravity" or "sila"
}

// ManagedEnvelope guarantees provenance, collision detection, and safe rollback.
type ManagedEnvelope struct {
	Managed         bool      `json:"managed"`
	Version         int       `json:"version"`
	ProjectID       string    `json:"project_id,omitempty"`
	ContentHash     string    `json:"content_hash"`
	UpdatedAt       time.Time `json:"updated_at"`
	Toolchain       string    `json:"toolchain,omitempty"`
	ManagedExcludes []string  `json:"managed_excludes,omitempty"`
	ManagedSkills   []string  `json:"managed_skills,omitempty"`
}

// ManagedDocument provides non-destructive round-trip serialization for skills.json.
type ManagedDocument struct {
	Envelope *ManagedEnvelope           `json:"-"`
	Entries  []PathEntry                `json:"entries,omitempty"`
	Exclude  []string                   `json:"exclude,omitempty"`
	Extra    map[string]json.RawMessage `json:"-"`
}

// PreviewResult encapsulates the read-only planned configuration diff.
type PreviewResult struct {
	TargetPath     string      `json:"target_path"`
	FileExists     bool        `json:"file_exists"`
	HasChanges     bool        `json:"has_changes"`
	CurrentContent string      `json:"current_content"`
	PlannedContent string      `json:"planned_content"`
	Diff           string      `json:"diff"`
	PlannedConfig  *JSONConfig `json:"planned_config"`
	SelectedSkills []string    `json:"selected_skills"`
	ExcludedSkills []string    `json:"excluded_skills"`
	ZeroWrite      bool        `json:"zero_write"`
}

// ApplyResult encapsulates the outcome of an atomic configuration apply.
type ApplyResult struct {
	TargetPath     string      `json:"target_path"`
	Status         string      `json:"status"` // "applied" or "no_op"
	Applied        bool        `json:"applied"`
	NoOp           bool        `json:"no_op"`
	ContentHash    string      `json:"content_hash"`
	BytesWritten   int         `json:"bytes_written"`
	SelectedSkills []string    `json:"selected_skills"`
	ExcludedSkills []string    `json:"excluded_skills"`
	Config         *JSONConfig `json:"config"`
	AppliedAt      time.Time   `json:"applied_at"`
}

// RemoveResult encapsulates the outcome of a configuration rollback.
type RemoveResult struct {
	TargetPath          string `json:"target_path"`
	Status              string `json:"status"` // "removed" or "not_found"
	FileDeleted         bool   `json:"file_deleted"`
	ParentDirRemoved    bool   `json:"parent_dir_removed"`
	UserEntriesRestored bool   `json:"user_entries_restored"`
	OrphanFiles         int    `json:"orphan_files"`
}

// CollisionError encapsulates details of an external modification conflict.
type CollisionError struct {
	TargetPath   string   `json:"target_path"`
	RecordedHash string   `json:"recorded_hash"`
	CurrentHash  string   `json:"current_hash"`
	DivergedKeys []string `json:"diverged_keys"`
	Diff         string   `json:"diff"`
}

func (e *CollisionError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("concurrent edit collision detected in %s: file was modified externally (recorded %s, current %s).", e.TargetPath, e.RecordedHash, e.CurrentHash))
	if len(e.DivergedKeys) > 0 {
		sb.WriteString(fmt.Sprintf("\n  Diverged Keys: %s", strings.Join(e.DivergedKeys, ", ")))
	}
	if e.Diff != "" {
		sb.WriteString("\n\nDifferences (recorded -> on disk):\n")
		sb.WriteString(e.Diff)
	}
	sb.WriteString("\nUse --force to overwrite")
	return sb.String()
}

// GenerateLockToken creates a unique identifier for lock ownership verification.
func GenerateLockToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewLockInfo constructs serialized lock metadata with host and PID diagnostics.
func NewLockInfo() ([]byte, string, error) {
	hostname, _ := os.Hostname()
	token := GenerateLockToken()
	info := LockInfo{
		PID:       os.Getpid(),
		Hostname:  hostname,
		CreatedAt: time.Now().UTC(),
		Token:     token,
	}
	bytes, err := json.Marshal(info)
	if err != nil {
		return nil, "", err
	}
	return bytes, token, nil
}

// CleanStaleTempFiles scans a directory for orphaned .tmp files older than maxAge.
func CleanStaleTempFiles(dir, prefix string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) == ".tmp" || strings.HasSuffix(name, ".tmp") {
			info, err := entry.Info()
			if err == nil && (maxAge <= 0 || now.Sub(info.ModTime()) > maxAge) {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
}

// CleanAllTempFiles unconditionally deletes any .tmp file in the directory.
func CleanAllTempFiles(dir string) {
	CleanStaleTempFiles(dir, "", 0)
}

// UnmarshalJSON deserializes skills.json while capturing _sila, entries, exclude,
// and preserving any unknown properties inside Extra.
func (d *ManagedDocument) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	d.Extra = make(map[string]json.RawMessage)
	for k, v := range raw {
		switch k {
		case "_sila":
			var env ManagedEnvelope
			if err := json.Unmarshal(v, &env); err == nil {
				d.Envelope = &env
			} else {
				// Relaxed unmarshal in case version is passed as string or interface{}
				var relaxed struct {
					Managed         bool        `json:"managed"`
					Version         interface{} `json:"version"`
					ProjectID       string      `json:"project_id,omitempty"`
					ContentHash     string      `json:"content_hash"`
					UpdatedAt       time.Time   `json:"updated_at"`
					Toolchain       string      `json:"toolchain,omitempty"`
					ManagedExcludes []string    `json:"managed_excludes,omitempty"`
					ManagedSkills   []string    `json:"managed_skills,omitempty"`
				}
				if rErr := json.Unmarshal(v, &relaxed); rErr == nil {
					vInt := 1
					if f, ok := relaxed.Version.(float64); ok {
						vInt = int(f)
					} else if s, ok := relaxed.Version.(string); ok {
						_, _ = fmt.Sscanf(s, "%d", &vInt)
					}
					d.Envelope = &ManagedEnvelope{
						Managed:         relaxed.Managed,
						Version:         vInt,
						ProjectID:       relaxed.ProjectID,
						ContentHash:     relaxed.ContentHash,
						UpdatedAt:       relaxed.UpdatedAt,
						Toolchain:       relaxed.Toolchain,
						ManagedExcludes: relaxed.ManagedExcludes,
						ManagedSkills:   relaxed.ManagedSkills,
					}
				}
			}
		case "entries":
			var entries []PathEntry
			if err := json.Unmarshal(v, &entries); err == nil {
				d.Entries = entries
			}
		case "exclude":
			var exc []string
			if err := json.Unmarshal(v, &exc); err == nil {
				d.Exclude = exc
			}
		default:
			d.Extra[k] = v
		}
	}
	return nil
}

// MarshalJSON serializes the managed document with clean formatting.
func (d *ManagedDocument) MarshalJSON() ([]byte, error) {
	out := make(map[string]interface{})

	// Add extra fields first to preserve custom properties
	for k, v := range d.Extra {
		out[k] = v
	}

	if d.Envelope != nil {
		out["_sila"] = d.Envelope
	}
	if len(d.Entries) > 0 {
		out["entries"] = d.Entries
	}
	if len(d.Exclude) > 0 {
		out["exclude"] = d.Exclude
	}

	return json.MarshalIndent(out, "", "  ")
}

// ToJSONConfig transforms the ManagedDocument into a skills.JSONConfig.
func (d *ManagedDocument) ToJSONConfig() *JSONConfig {
	cfg := &JSONConfig{
		Entries: d.Entries,
		Exclude: d.Exclude,
	}
	if d.Envelope != nil {
		cfg.SilaEnvelope = &SilaEnvelope{
			Managed:         d.Envelope.Managed,
			Version:         d.Envelope.Version,
			ProjectID:       d.Envelope.ProjectID,
			ContentHash:     d.Envelope.ContentHash,
			UpdatedAt:       d.Envelope.UpdatedAt,
			ManagedExcludes: d.Envelope.ManagedExcludes,
		}
	}
	return cfg
}

// ComputePayloadHash calculates the SHA-256 of the configuration payload with masked envelope.
// content_hash is set to "" and updated_at is set to zero to avoid circular hash dependency.
func ComputePayloadHash(doc *ManagedDocument) (string, error) {
	if doc == nil {
		return "", errors.New("cannot compute hash of nil document")
	}

	m := make(map[string]interface{})
	for k, v := range doc.Extra {
		m[k] = v
	}

	if doc.Envelope != nil {
		envCopy := *doc.Envelope
		envCopy.ContentHash = ""
		envCopy.UpdatedAt = time.Time{}
		m["_sila"] = envCopy
	}
	if len(doc.Entries) > 0 {
		m["entries"] = doc.Entries
	}
	if len(doc.Exclude) > 0 {
		m["exclude"] = doc.Exclude
	}

	// json.Marshal on map sorts keys lexicographically
	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("canonical marshal: %w", err)
	}

	h := sha256.New()
	h.Write(data)
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// ComputeCanonicalHash is an alias for ComputePayloadHash.
func ComputeCanonicalHash(doc *ManagedDocument) (string, error) {
	return ComputePayloadHash(doc)
}

// DetectCollision tests if the existing on-disk document has been modified externally.
func DetectCollision(existing *ManagedDocument) (bool, string, string, error) {
	if existing == nil || existing.Envelope == nil || !existing.Envelope.Managed {
		return false, "", "", nil
	}

	recorded := existing.Envelope.ContentHash
	current, err := ComputePayloadHash(existing)
	if err != nil {
		return false, "", "", err
	}

	if recorded != "" && recorded != current {
		return true, recorded, current, nil // Collision detected!
	}

	return false, recorded, current, nil
}

// CheckCollision is an alias for DetectCollision.
func CheckCollision(existing *ManagedDocument) (bool, string, string, error) {
	return DetectCollision(existing)
}

// MergeConfig non-destructively merges proposed Sila entries and exclusions into an existing document.
func MergeConfig(existing *ManagedDocument, projectPath string, proposedPins []string, globalExcludes []string) *ManagedDocument {
	merged := &ManagedDocument{
		Extra: make(map[string]json.RawMessage),
	}

	if existing != nil {
		for k, v := range existing.Extra {
			merged.Extra[k] = v
		}
	}

	cleanProjectPath := filepath.Clean(projectPath)

	// 1. Reconcile entries
	var finalEntries []PathEntry
	projectEntryFound := false

	if existing != nil {
		for _, e := range existing.Entries {
			cleanEntryPath := filepath.Clean(e.Path)
			if cleanEntryPath == cleanProjectPath ||
				(cleanEntryPath == ".agents/skills" && cleanProjectPath == ".agents/skills") ||
				(cleanEntryPath == ".gemini/skills" && cleanProjectPath == ".gemini/skills") {
				projectEntryFound = true
				incSet := make(map[string]bool)
				var combined []string
				for _, p := range e.IncludeOnly {
					if !incSet[p] {
						incSet[p] = true
						combined = append(combined, p)
					}
				}
				for _, p := range proposedPins {
					pattern := p
					if !strings.HasPrefix(pattern, "^") && !strings.HasSuffix(pattern, "$") {
						pattern = "^" + regexp.QuoteMeta(p) + "$"
					}
					if !incSet[pattern] {
						incSet[pattern] = true
						combined = append(combined, pattern)
					}
				}
				finalEntries = append(finalEntries, PathEntry{
					Path:        e.Path,
					IncludeOnly: combined,
				})
			} else {
				// Keep non-Sila user entry verbatim!
				finalEntries = append(finalEntries, e)
			}
		}
	}

	if !projectEntryFound && len(proposedPins) > 0 {
		var formattedPins []string
		for _, p := range proposedPins {
			pattern := p
			if !strings.HasPrefix(pattern, "^") && !strings.HasSuffix(pattern, "$") {
				pattern = "^" + regexp.QuoteMeta(p) + "$"
			}
			formattedPins = append(formattedPins, pattern)
		}
		finalEntries = append(finalEntries, PathEntry{
			Path:        projectPath,
			IncludeOnly: formattedPins,
		})
	}
	merged.Entries = finalEntries

	// 2. Reconcile exclude patterns
	excSet := make(map[string]bool)
	var finalExcludes []string
	if existing != nil {
		for _, ex := range existing.Exclude {
			if !excSet[ex] {
				excSet[ex] = true
				finalExcludes = append(finalExcludes, ex)
			}
		}
	}
	for _, ex := range globalExcludes {
		if !excSet[ex] {
			excSet[ex] = true
			finalExcludes = append(finalExcludes, ex)
		}
	}
	merged.Exclude = finalExcludes

	return merged
}

// MergeConfiguration is an alias for MergeConfig.
func MergeConfiguration(existing *ManagedDocument, projectPath string, proposedPins []string, globalExcludes []string) *ManagedDocument {
	return MergeConfig(existing, projectPath, proposedPins, globalExcludes)
}

// IsSemanticallyEquivalent checks if proposed configuration matches existing on-disk configuration,
// strictly ignoring UpdatedAt and ContentHash to eliminate timestamp-induced churn.
func IsSemanticallyEquivalent(existing, proposed *ManagedDocument) bool {
	if existing == nil || proposed == nil {
		return false
	}

	// Compare Exclude
	if len(existing.Exclude) != len(proposed.Exclude) {
		return false
	}
	for i := range existing.Exclude {
		if existing.Exclude[i] != proposed.Exclude[i] {
			return false
		}
	}

	// Compare Entries
	if len(existing.Entries) != len(proposed.Entries) {
		return false
	}
	for i := range existing.Entries {
		if filepath.Clean(existing.Entries[i].Path) != filepath.Clean(proposed.Entries[i].Path) {
			return false
		}
		if !reflect.DeepEqual(existing.Entries[i].IncludeOnly, proposed.Entries[i].IncludeOnly) {
			return false
		}
	}

	// Compare Extra fields
	if len(existing.Extra) != len(proposed.Extra) {
		return false
	}
	for k, v := range existing.Extra {
		pv, ok := proposed.Extra[k]
		if !ok || string(v) != string(pv) {
			return false
		}
	}

	// Compare Envelope (ignoring UpdatedAt and ContentHash)
	if (existing.Envelope == nil) != (proposed.Envelope == nil) {
		return false
	}
	if existing.Envelope != nil && proposed.Envelope != nil {
		if existing.Envelope.Managed != proposed.Envelope.Managed {
			return false
		}
		if existing.Envelope.Version != proposed.Envelope.Version {
			return false
		}
		if existing.Envelope.ProjectID != proposed.Envelope.ProjectID {
			return false
		}
		if len(existing.Envelope.ManagedExcludes) != len(proposed.Envelope.ManagedExcludes) {
			return false
		}
		for i := range existing.Envelope.ManagedExcludes {
			if existing.Envelope.ManagedExcludes[i] != proposed.Envelope.ManagedExcludes[i] {
				return false
			}
		}
		if len(existing.Envelope.ManagedSkills) != len(proposed.Envelope.ManagedSkills) {
			return false
		}
		for i := range existing.Envelope.ManagedSkills {
			if existing.Envelope.ManagedSkills[i] != proposed.Envelope.ManagedSkills[i] {
				return false
			}
		}
	}

	return true
}

// AtomicWriteFile writes data to targetPath safely via a temporary staging file
// in the same directory, fsync flush, and atomic os.Rename.
func AtomicWriteFile(targetPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	base := filepath.Base(targetPath)
	randBytes := make([]byte, 3)
	_, _ = rand.Read(randBytes)
	randSuffix := hex.EncodeToString(randBytes)

	// Pattern: .<filename>.<pid>.<unixnano>.<rand>.tmp
	tmpName := fmt.Sprintf(".%s.%d.%d.%s.tmp", base, os.Getpid(), time.Now().UnixNano(), randSuffix)
	tmpPath := filepath.Join(dir, tmpName)

	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("create staging file %s: %w", tmpPath, err)
	}

	var success bool
	defer func() {
		if !success {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	// 1. Write data
	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("write data to staging file: %w", err)
	}

	// 2. Physical flush to storage (fsync)
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync staging file: %w", err)
	}

	// 3. Close file before rename (mandatory on Windows)
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close staging file: %w", err)
	}

	// 4. Atomic directory cutover
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("atomic rename %s to %s: %w", tmpPath, targetPath, err)
	}

	// 5. Sync parent directory to persist directory inode entry on Unix
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}

	success = true
	return nil
}

// AtomicWriteJSON marshals v with indentation and writes it atomically with mode 0644.
func AtomicWriteJSON(targetPath string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	data = append(data, '\n')
	return AtomicWriteFile(targetPath, data, 0644)
}

// GenerateLCSUnifiedDiff computes a standard unified diff between oldContent and newContent
// with contextLines of surrounding context using a pure Go Longest Common Subsequence (LCS)
// dynamic programming algorithm. Standard library only; zero dependencies in go.mod.
func GenerateLCSUnifiedDiff(oldLabel, newLabel, oldContent, newContent string, contextLines int) string {
	if oldContent == newContent {
		return ""
	}
	if contextLines < 0 {
		contextLines = 3
	}

	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	if len(oldLines) == 0 && len(newLines) > 0 {
		var sb strings.Builder
		sb.WriteString("--- /dev/null\n")
		sb.WriteString(fmt.Sprintf("+++ %s\n", newLabel))
		sb.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(newLines)))
		for _, l := range newLines {
			sb.WriteString("+" + l + "\n")
		}
		return sb.String()
	}

	if len(oldLines) > 0 && len(newLines) == 0 {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("--- %s\n", oldLabel))
		sb.WriteString("+++ /dev/null\n")
		sb.WriteString(fmt.Sprintf("@@ -1,%d +0,0 @@\n", len(oldLines)))
		for _, l := range oldLines {
			sb.WriteString("-" + l + "\n")
		}
		return sb.String()
	}

	m := len(oldLines)
	n := len(newLines)
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}

	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	type diffOp struct {
		kind byte // ' ', '-', '+'
		text string
	}
	var ops []diffOp
	i, j := 0, 0
	for i < m && j < n {
		switch {
		case oldLines[i] == newLines[j]:
			ops = append(ops, diffOp{' ', oldLines[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', oldLines[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', newLines[j]})
			j++
		}
	}
	for ; i < m; i++ {
		ops = append(ops, diffOp{'-', oldLines[i]})
	}
	for ; j < n; j++ {
		ops = append(ops, diffOp{'+', newLines[j]})
	}

	numOps := len(ops)
	hasChange := false
	for _, op := range ops {
		if op.kind != ' ' {
			hasChange = true
			break
		}
	}
	if !hasChange {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", oldLabel, newLabel)

	k := 0
	for k < numOps {
		if ops[k].kind == ' ' {
			k++
			continue
		}

		start := k - contextLines
		if start < 0 {
			start = 0
		}

		end := k
		for end < numOps {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := 0
			for end+run < numOps && ops[end+run].kind == ' ' {
				run++
			}
			if end+run == numOps {
				if run > contextLines {
					end += contextLines
				} else {
					end += run
				}
				break
			}
			if run > contextLines*2 {
				end += contextLines
				break
			}
			end += run
		}
		if end > numOps {
			end = numOps
		}

		var oldBefore, newBefore int
		for x := 0; x < start; x++ {
			switch ops[x].kind {
			case ' ', '-':
				oldBefore++
			}
			switch ops[x].kind {
			case ' ', '+':
				newBefore++
			}
		}

		var oldLen, newLen int
		for x := start; x < end; x++ {
			switch ops[x].kind {
			case ' ', '-':
				oldLen++
			}
			switch ops[x].kind {
			case ' ', '+':
				newLen++
			}
		}

		oldLine := oldBefore + 1
		if oldLen == 0 {
			oldLine = oldBefore
		}
		newLine := newBefore + 1
		if newLen == 0 {
			newLine = newBefore
		}

		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", oldLine, oldLen, newLine, newLen)
		for x := start; x < end; x++ {
			sb.WriteByte(ops[x].kind)
			sb.WriteString(ops[x].text)
			sb.WriteByte('\n')
		}

		k = end
	}

	return sb.String()
}

// GenerateUnifiedDiff produces a clean unified diff between oldContent and newContent.
func GenerateUnifiedDiff(filename, oldContent, newContent string) string {
	return GenerateLCSUnifiedDiff(filename, filename, oldContent, newContent, 3)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// BuildCollisionDiff reconstructs the baseline configuration document recorded by Sila
// and generates a unified diff showing what changed on disk.
func BuildCollisionDiff(filename string, existingDoc *ManagedDocument, opts AdapterOptions, diskContent string) string {
	if existingDoc == nil || existingDoc.Envelope == nil {
		return ""
	}

	recordedSkills := opts.Skills
	if len(existingDoc.Envelope.ManagedSkills) > 0 {
		recordedSkills = existingDoc.Envelope.ManagedSkills
	}

	recordedExcludes := opts.Exclude
	if len(existingDoc.Envelope.ManagedExcludes) > 0 {
		recordedExcludes = existingDoc.Envelope.ManagedExcludes
	}

	dirName := HarnessConfigDir(opts.Harness)
	if strings.Contains(filename, ".gemini") {
		dirName = ".gemini"
	}
	projectSkillsPath := dirName + "/skills"
	if existingDoc != nil {
		for _, e := range existingDoc.Entries {
			cleanP := filepath.Clean(e.Path)
			if cleanP == ".agents/skills" || cleanP == ".gemini/skills" {
				projectSkillsPath = cleanP
				break
			}
		}
	}
	baselineDoc := MergeConfig(nil, projectSkillsPath, recordedSkills, recordedExcludes)

	projectID := opts.ProjectID
	if projectID == "" {
		projectID = existingDoc.Envelope.ProjectID
	}
	toolchain := opts.Toolchain
	if toolchain == "" {
		toolchain = existingDoc.Envelope.Toolchain
	}
	if toolchain == "" {
		toolchain = "antigravity"
	}

	version := existingDoc.Envelope.Version
	if version == 0 {
		version = 1
	}

	baselineDoc.Envelope = &ManagedEnvelope{
		Managed:         true,
		Version:         version,
		ProjectID:       projectID,
		Toolchain:       toolchain,
		ContentHash:     existingDoc.Envelope.ContentHash,
		UpdatedAt:       existingDoc.Envelope.UpdatedAt,
		ManagedExcludes: recordedExcludes,
		ManagedSkills:   recordedSkills,
	}

	baselineBytes, err := baselineDoc.MarshalJSON()
	if err != nil {
		return ""
	}
	baselineBytes = append(baselineBytes, '\n')

	oldLabel := fmt.Sprintf("%s (recorded)", filename)
	newLabel := fmt.Sprintf("%s (on disk)", filename)
	return GenerateLCSUnifiedDiff(oldLabel, newLabel, string(baselineBytes), diskContent, 3)
}

// ParseDivergedKeys inspects an existing on-disk document against expected/recorded values
// across 4 distinct zones and returns a deduplicated, deterministic list of diverged keys.
func ParseDivergedKeys(existingDoc *ManagedDocument, expectedSkills, expectedExcludes []string, opts ...AdapterOptions) []string {
	if existingDoc == nil {
		return []string{"document"}
	}

	var diverged []string
	seen := make(map[string]bool)
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			diverged = append(diverged, k)
		}
	}

	// -------------------------------------------------------------
	// Zone 1: Envelope Checks (_sila.*)
	// -------------------------------------------------------------
	if existingDoc.Envelope == nil {
		add("_sila.managed")
	} else {
		if !existingDoc.Envelope.Managed {
			add("_sila.managed")
		}
		if existingDoc.Envelope.Version != 1 {
			add("_sila.version")
		}
		if len(opts) > 0 {
			if opts[0].ProjectID != "" && existingDoc.Envelope.ProjectID != "" && existingDoc.Envelope.ProjectID != opts[0].ProjectID {
				add("_sila.project_id")
			}
			if opts[0].Toolchain != "" && existingDoc.Envelope.Toolchain != "" && existingDoc.Envelope.Toolchain != opts[0].Toolchain {
				add("_sila.toolchain")
			}
		}
	}

	// -------------------------------------------------------------
	// Zone 2: Entries / skills.include Checks
	// -------------------------------------------------------------
	recordedSkills := expectedSkills
	if existingDoc.Envelope != nil && len(existingDoc.Envelope.ManagedSkills) > 0 {
		recordedSkills = existingDoc.Envelope.ManagedSkills
	}

	var silaEntry *PathEntry
	hasUnmanagedEntries := false
	for i := range existingDoc.Entries {
		e := &existingDoc.Entries[i]
		cleanP := filepath.Clean(e.Path)
		if cleanP == ".agents/skills" || cleanP == ".gemini/skills" {
			silaEntry = e
		} else {
			hasUnmanagedEntries = true
		}
	}

	if silaEntry == nil && len(recordedSkills) > 0 {
		add("entries (missing Sila entry)")
	} else if silaEntry != nil {
		if !includeOnlyMatchesSkills(silaEntry.IncludeOnly, recordedSkills) {
			add("skills.include")
		}
	}

	if hasUnmanagedEntries {
		add("entries (unmanaged entries)")
	}

	// -------------------------------------------------------------
	// Zone 3: Exclude Checks (exclude)
	// -------------------------------------------------------------
	recordedExcludes := expectedExcludes
	if existingDoc.Envelope != nil && len(existingDoc.Envelope.ManagedExcludes) > 0 {
		recordedExcludes = existingDoc.Envelope.ManagedExcludes
	}
	if !slicesEqual(existingDoc.Exclude, recordedExcludes) {
		add("exclude")
	}

	// -------------------------------------------------------------
	// Zone 4: Extra / Unmanaged Top-Level Keys
	// -------------------------------------------------------------
	if len(existingDoc.Extra) > 0 {
		var extraKeys []string
		for k := range existingDoc.Extra {
			extraKeys = append(extraKeys, k)
		}
		sort.Strings(extraKeys) // Deterministic ordering
		for _, k := range extraKeys {
			add(fmt.Sprintf("%s (unmanaged key)", k))
		}
	}

	// -------------------------------------------------------------
	// Fallback: Internal formatting/whitespace/hash divergence
	// -------------------------------------------------------------
	if len(diverged) == 0 {
		add("content_hash (internal formatting/whitespace)")
	}

	return diverged
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func includeOnlyMatchesSkills(includeOnly, skillsList []string) bool {
	var expected []string
	for _, s := range skillsList {
		pattern := s
		if !strings.HasPrefix(pattern, "^") && !strings.HasSuffix(pattern, "$") {
			pattern = "^" + regexp.QuoteMeta(s) + "$"
		}
		expected = append(expected, pattern)
	}

	if len(includeOnly) != len(expected) {
		return false
	}
	for i := range includeOnly {
		if includeOnly[i] != expected[i] {
			return false
		}
	}
	return true
}

// PreviewHarnessConfig generates a planned configuration diff without writing anything to disk.
func PreviewHarnessConfig(projectDir string, opts AdapterOptions) (*PreviewResult, error) {
	dirName := HarnessConfigDir(opts.Harness)
	targetPath := filepath.Join(projectDir, dirName, "skills.json")

	var currentContent string
	var existingDoc *ManagedDocument
	fileExists := false

	if data, err := os.ReadFile(targetPath); err == nil {
		fileExists = true
		currentContent = string(data)
		var doc ManagedDocument
		if err := json.Unmarshal(data, &doc); err == nil {
			existingDoc = &doc
		}
	}

	var selectedSkills []string
	if len(opts.Skills) > 0 {
		selectedSkills = opts.Skills
	}

	var excludedSkills []string
	if len(opts.Exclude) > 0 {
		excludedSkills = opts.Exclude
	}

	projectSkillsPath := dirName + "/skills"
	plannedDoc := MergeConfig(existingDoc, projectSkillsPath, selectedSkills, excludedSkills)

	toolchain := opts.Toolchain
	if toolchain == "" {
		if opts.Harness != "" {
			toolchain = opts.Harness
		} else {
			toolchain = "antigravity"
		}
	}

	projectID := opts.ProjectID
	if projectID == "" {
		projectID = filepath.Base(projectDir)
	}

	plannedDoc.Envelope = &ManagedEnvelope{
		Managed:         true,
		Version:         1,
		ProjectID:       projectID,
		Toolchain:       toolchain,
		ManagedExcludes: excludedSkills,
		ManagedSkills:   selectedSkills,
	}

	hash, err := ComputePayloadHash(plannedDoc)
	if err != nil {
		return nil, fmt.Errorf("compute payload hash: %w", err)
	}
	plannedDoc.Envelope.ContentHash = hash
	plannedDoc.Envelope.UpdatedAt = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	plannedBytes, err := plannedDoc.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal planned config: %w", err)
	}
	plannedBytes = append(plannedBytes, '\n')
	plannedContent := string(plannedBytes)

	relPath := filepath.Join(dirName, "skills.json")
	diff := GenerateUnifiedDiff(relPath, currentContent, plannedContent)
	hasChanges := (diff != "")

	return &PreviewResult{
		TargetPath:     targetPath,
		FileExists:     fileExists,
		HasChanges:     hasChanges,
		CurrentContent: currentContent,
		PlannedContent: plannedContent,
		Diff:           diff,
		PlannedConfig:  plannedDoc.ToJSONConfig(),
		SelectedSkills: selectedSkills,
		ExcludedSkills: plannedDoc.Exclude,
		ZeroWrite:      true,
	}, nil
}

// ApplyHarnessConfig applies Sila-managed skill filtering atomically under an advisory lock.
func ApplyHarnessConfig(projectDir string, opts AdapterOptions) (*ApplyResult, error) {
	dirName := HarnessConfigDir(opts.Harness)
	dir := filepath.Join(projectDir, dirName)
	targetPath := filepath.Join(dir, "skills.json")
	lockPath := filepath.Join(dir, ".skills.json.lock")

	fl, err := AcquireLock(lockPath, DefaultLockTimeout)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = fl.Unlock()
	}()

	// Scavenge any abandoned temp files
	CleanAllTempFiles(dir)

	var existingDoc *ManagedDocument
	var hasCollision bool
	if data, err := os.ReadFile(targetPath); err == nil {
		var doc ManagedDocument
		if err := json.Unmarshal(data, &doc); err != nil {
			if !opts.Force {
				return nil, fmt.Errorf("malformed JSON in %s: %w. Use --force to overwrite", targetPath, err)
			}
		} else {
			existingDoc = &doc
			var recorded, current string
			hasCollision, recorded, current, err = DetectCollision(existingDoc)
			if err == nil && hasCollision && !opts.Force {
				relPath := filepath.Join(dirName, "skills.json")
				divergedKeys := ParseDivergedKeys(existingDoc, opts.Skills, opts.Exclude, opts)
				diff := BuildCollisionDiff(relPath, existingDoc, opts, string(data))
				return nil, &CollisionError{
					TargetPath:   relPath,
					RecordedHash: recorded,
					CurrentHash:  current,
					DivergedKeys: divergedKeys,
					Diff:         diff,
				}
			}
		}
	}

	var selectedSkills []string
	if len(opts.Skills) > 0 {
		selectedSkills = opts.Skills
	}

	var globalExcludes []string
	if len(opts.Exclude) > 0 {
		globalExcludes = opts.Exclude
	}

	projectSkillsPath := dirName + "/skills"
	proposedDoc := MergeConfig(existingDoc, projectSkillsPath, selectedSkills, globalExcludes)

	toolchain := opts.Toolchain
	if toolchain == "" {
		if opts.Harness != "" {
			toolchain = opts.Harness
		} else {
			toolchain = "antigravity"
		}
	}

	projectID := opts.ProjectID
	if projectID == "" {
		projectID = filepath.Base(projectDir)
	}

	proposedDoc.Envelope = &ManagedEnvelope{
		Managed:         true,
		Version:         1,
		ProjectID:       projectID,
		Toolchain:       toolchain,
		ManagedExcludes: globalExcludes,
		ManagedSkills:   selectedSkills,
	}

	// Idempotency check: if existing config is semantically equivalent AND no collision occurred, skip write
	if !hasCollision && existingDoc != nil && IsSemanticallyEquivalent(existingDoc, proposedDoc) {
		CleanAllTempFiles(dir)
		return &ApplyResult{
			TargetPath:     targetPath,
			Status:         "no_op",
			Applied:        false,
			NoOp:           true,
			ContentHash:    existingDoc.Envelope.ContentHash,
			BytesWritten:   0,
			SelectedSkills: selectedSkills,
			ExcludedSkills: existingDoc.Exclude,
			Config:         existingDoc.ToJSONConfig(),
			AppliedAt:      existingDoc.Envelope.UpdatedAt,
		}, nil
	}

	// Calculate new payload hash
	hash, err := ComputePayloadHash(proposedDoc)
	if err != nil {
		return nil, fmt.Errorf("compute payload hash: %w", err)
	}
	appliedAt := time.Now().UTC()
	proposedDoc.Envelope.ContentHash = hash
	proposedDoc.Envelope.UpdatedAt = appliedAt

	jsonData, err := proposedDoc.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal JSON: %w", err)
	}
	jsonData = append(jsonData, '\n')

	if err := AtomicWriteFile(targetPath, jsonData, 0644); err != nil {
		return nil, err
	}

	CleanAllTempFiles(dir)

	return &ApplyResult{
		TargetPath:     targetPath,
		Status:         "applied",
		Applied:        true,
		NoOp:           false,
		ContentHash:    hash,
		BytesWritten:   len(jsonData),
		SelectedSkills: selectedSkills,
		ExcludedSkills: proposedDoc.Exclude,
		Config:         proposedDoc.ToJSONConfig(),
		AppliedAt:      appliedAt,
	}, nil
}

// RemoveHarnessConfig rolls back Sila-managed skill filtering cleanly.
func RemoveHarnessConfig(projectDir string, opts AdapterOptions) (*RemoveResult, error) {
	dirName := HarnessConfigDir(opts.Harness)
	dir := filepath.Join(projectDir, dirName)
	targetPath := filepath.Join(dir, "skills.json")
	lockPath := filepath.Join(dir, ".skills.json.lock")

	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return &RemoveResult{
			TargetPath:          targetPath,
			Status:              "not_found",
			FileDeleted:         false,
			ParentDirRemoved:    false,
			UserEntriesRestored: false,
			OrphanFiles:         0,
		}, nil
	}

	fl, err := AcquireLock(lockPath, DefaultLockTimeout)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = fl.Unlock()
	}()

	CleanAllTempFiles(dir)

	data, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, err
	}

	var doc ManagedDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		if !opts.Force {
			return nil, fmt.Errorf("malformed JSON in %s: %w. Use --force to remove", targetPath, err)
		}
		_ = os.Remove(targetPath)
		_ = fl.Unlock()
		dirRemoved := pruneEmptyDir(dir)
		return &RemoveResult{
			TargetPath:          targetPath,
			Status:              "removed",
			FileDeleted:         true,
			ParentDirRemoved:    dirRemoved,
			UserEntriesRestored: false,
			OrphanFiles:         0,
		}, nil
	}

	relPath := filepath.Join(dirName, "skills.json")
	if doc.Envelope == nil || !doc.Envelope.Managed {
		if !opts.Force {
			return nil, fmt.Errorf("%s is not managed by Sila (missing _sila envelope). Use --force to remove", relPath)
		}
		_ = os.Remove(targetPath)
		_ = fl.Unlock()
		dirRemoved := pruneEmptyDir(dir)
		return &RemoveResult{
			TargetPath:          targetPath,
			Status:              "removed",
			FileDeleted:         true,
			ParentDirRemoved:    dirRemoved,
			UserEntriesRestored: false,
			OrphanFiles:         0,
		}, nil
	}

	// Check if file contains user entries that should be preserved
	managedExcludeSet := make(map[string]bool)
	if doc.Envelope != nil {
		for _, ex := range doc.Envelope.ManagedExcludes {
			managedExcludeSet[ex] = true
		}
	}
	for _, ex := range opts.Exclude {
		managedExcludeSet[ex] = true
	}

	hasUserEntries := false
	if len(doc.Extra) > 0 {
		hasUserEntries = true
	}
	for _, e := range doc.Entries {
		cleanP := filepath.Clean(e.Path)
		if cleanP != ".agents/skills" && cleanP != ".gemini/skills" {
			hasUserEntries = true
		}
	}
	for _, ex := range doc.Exclude {
		if !managedExcludeSet[ex] {
			hasUserEntries = true
		}
	}

	if hasUserEntries {
		// Contract B: Strip _sila and Sila entries; restore user entries
		doc.Envelope = nil

		var remainingEntries []PathEntry
		for _, e := range doc.Entries {
			cleanP := filepath.Clean(e.Path)
			if cleanP != ".agents/skills" && cleanP != ".gemini/skills" {
				remainingEntries = append(remainingEntries, e)
			}
		}
		doc.Entries = remainingEntries

		var remainingExcludes []string
		for _, ex := range doc.Exclude {
			if !managedExcludeSet[ex] {
				remainingExcludes = append(remainingExcludes, ex)
			}
		}
		doc.Exclude = remainingExcludes

		cleanData, err := doc.MarshalJSON()
		if err != nil {
			return nil, err
		}
		cleanData = append(cleanData, '\n')

		if err := AtomicWriteFile(targetPath, cleanData, 0644); err != nil {
			return nil, err
		}

		_ = fl.Unlock()
		return &RemoveResult{
			TargetPath:          targetPath,
			Status:              "removed",
			FileDeleted:         false,
			ParentDirRemoved:    false,
			UserEntriesRestored: true,
			OrphanFiles:         0,
		}, nil
	}

	// Contract A: 100% Sila file
	if err := os.Remove(targetPath); err != nil {
		return nil, err
	}

	_ = fl.Unlock()
	dirRemoved := pruneEmptyDir(dir)

	return &RemoveResult{
		TargetPath:          targetPath,
		Status:              "removed",
		FileDeleted:         true,
		ParentDirRemoved:    dirRemoved,
		UserEntriesRestored: false,
		OrphanFiles:         0,
	}, nil
}

func pruneEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	// If only stale temp or lock files remain, clean them
	clean := true
	for _, e := range entries {
		name := e.Name()
		if name == ".skills.json.lock" || strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(filepath.Join(dir, name))
		} else {
			clean = false
		}
	}

	if clean {
		remaining, err := os.ReadDir(dir)
		if err == nil && len(remaining) == 0 {
			_ = os.Remove(dir)
			return true
		}
	}
	return false
}

// HarnessConfigDir returns the directory name used for project-level harness configuration.
func HarnessConfigDir(harness string) string {
	switch strings.ToLower(strings.TrimSpace(harness)) {
	case "gemini", "gemini-cli":
		return ".gemini"
	case "claude", "claude-code":
		return ".claude"
	case "opencode":
		return ".opencode"
	case "codex", "antigravity", "sila":
		return ".agents"
	default:
		return ".agents"
	}
}

// HarnessSkillVault returns the canonical skill vault directory path for a given harness relative to home.
func HarnessSkillVault(home, harness string) string {
	switch strings.ToLower(strings.TrimSpace(harness)) {
	case "claude", "claude-code":
		return filepath.Join(home, ".claude", "skills")
	case "codex":
		return filepath.Join(home, ".agents", "skills")
	case "antigravity":
		return filepath.Join(home, ".gemini", "antigravity", "builtin", "skills")
	case "opencode":
		if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
			xdgVault := filepath.Join(xdg, "opencode", "skills")
			if _, err := os.Stat(xdgVault); err == nil {
				return xdgVault
			}
		}
		flatpakVault := filepath.Join(home, ".var", "app", "ai.opencode.opencode", "config", "opencode", "skills")
		if _, err := os.Stat(flatpakVault); err == nil {
			return flatpakVault
		}
		return filepath.Join(home, ".config", "opencode", "skills")
	default:
		return filepath.Join(home, ".agents", "skills")
	}
}

// SupportedHarnesses returns the list of all AI coding agent harnesses supported by Sila.
func SupportedHarnesses() []string {
	return []string{
		"antigravity",
		"claude-code",
		"codex",
		"opencode",
		"gemini",
	}
}

// VerifyHarnessCompatibility returns configuration directory and skill vault location
// for a supported harness, or false if the harness is unrecognized.
func VerifyHarnessCompatibility(home, harness string) (configDir string, skillVault string, supported bool) {
	norm := strings.ToLower(strings.TrimSpace(harness))
	for _, h := range SupportedHarnesses() {
		if norm == h || (norm == "claude" && h == "claude-code") || (norm == "gemini-cli" && h == "gemini") {
			return HarnessConfigDir(norm), HarnessSkillVault(home, norm), true
		}
	}
	return ".agents", filepath.Join(home, ".agents", "skills"), false
}
