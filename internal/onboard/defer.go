package onboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// Deferral records an explicit deferral of a legacy task surface.
type Deferral struct {
	Surface    string    `json:"surface"`
	Reason     string    `json:"reason"`
	DeferredAt time.Time `json:"deferred_at"`
	Agent      string    `json:"agent,omitempty"`
}

// DeferralsFile represents the persisted JSON structure in .sila/deferrals.json.
type DeferralsFile struct {
	Deferrals []Deferral `json:"deferrals"`
}

func deferralsFilePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".sila", "deferrals.json")
}

// LoadDeferrals reads all recorded deferrals from .sila/deferrals.json.
func LoadDeferrals(repoRoot string) ([]Deferral, error) {
	path := deferralsFilePath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file DeferralsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Deferrals, nil
}

// SaveDeferral persists a deferral record to .sila/deferrals.json.
func SaveDeferral(repoRoot, surface, reason, agent string) (*Deferral, error) {
	if strings.TrimSpace(surface) == "" {
		return nil, fmt.Errorf("surface must not be empty")
	}
	cleanSurface := filepath.Clean(strings.TrimSpace(surface))
	if strings.TrimSpace(reason) == "" {
		reason = "deferred legacy task surface"
	}
	existing, _ := LoadDeferrals(repoRoot)
	rec := Deferral{
		Surface:    cleanSurface,
		Reason:     strings.TrimSpace(reason),
		DeferredAt: time.Now().UTC(),
		Agent:      strings.TrimSpace(agent),
	}

	updated := false
	for i, d := range existing {
		if strings.EqualFold(d.Surface, cleanSurface) {
			existing[i] = rec
			updated = true
			break
		}
	}
	if !updated {
		existing = append(existing, rec)
	}

	dir := filepath.Join(repoRoot, ".sila")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create .sila directory: %w", err)
	}

	payload := DeferralsFile{Deferrals: existing}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal deferrals: %w", err)
	}
	path := deferralsFilePath(repoRoot)
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return &rec, nil
}

// IsSurfaceDeferred evaluates whether a given surface name matches any recorded deferrals.
func IsSurfaceDeferred(surface string, deferrals []Deferral) (bool, string) {
	clean := filepath.Clean(strings.TrimSpace(surface))
	base := filepath.Base(clean)
	for _, d := range deferrals {
		dClean := filepath.Clean(d.Surface)
		dBase := filepath.Base(dClean)
		if strings.EqualFold(dClean, clean) || strings.EqualFold(dBase, base) {
			return true, d.Reason
		}
		// Generic surface kinds: "tasks", "brain", "legacy", "all"
		targetLower := strings.ToLower(d.Surface)
		if targetLower == "all" || targetLower == "legacy" {
			return true, d.Reason
		}
		if targetLower == "tasks" && (strings.HasSuffix(clean, ".md") || strings.HasSuffix(clean, ".txt") || base == "TODO" || base == "TASKS") {
			return true, d.Reason
		}
		if targetLower == "brain" && (base == ".brain" || base == "brain.json") {
			return true, d.Reason
		}
	}
	return false, ""
}

// LoadStoreDeferrals extracts deferrals recorded as architectural memos in the knowledge store.
func LoadStoreDeferrals(st *store.Store, project string) []Deferral {
	if st == nil || project == "" {
		return nil
	}
	facts, err := st.FactsByProject(project)
	if err != nil {
		return nil
	}
	var out []Deferral
	for _, f := range facts {
		if f.Kind == "deferral" || strings.HasPrefix(strings.ToLower(f.Claim), "deferral:") {
			content := f.Claim
			lower := strings.ToLower(content)
			if idx := strings.Index(lower, "legacy task surface "); idx != -1 {
				rem := content[idx+len("legacy task surface "):]
				parts := strings.SplitN(rem, " deferred:", 2)
				surface := strings.TrimSpace(parts[0])
				reason := ""
				if len(parts) > 1 {
					reason = strings.TrimSpace(parts[1])
				}
				if surface != "" {
					t, _ := time.Parse(time.RFC3339, f.FirstSeen)
					out = append(out, Deferral{
						Surface:    surface,
						Reason:     reason,
						DeferredAt: t,
						Agent:      f.OriginRole,
					})
				}
			}
		}
	}
	return out
}

// DeferSurface records an explicit deferral both locally in .sila/deferrals.json
// and as an architectural deferral memo in the knowledge store.
func DeferSurface(st *store.Store, repoRoot, project, surface, reason, agent string) (*Deferral, error) {
	rec, err := SaveDeferral(repoRoot, surface, reason, agent)
	if err != nil {
		return nil, err
	}

	if st != nil && project != "" {
		content := fmt.Sprintf("deferral: legacy task surface %s deferred: %s", rec.Surface, rec.Reason)
		_, _ = st.InsertMemoWithKind(project, content, agent, repoRoot, []string{rec.Surface}, "deferral")
	}

	return rec, nil
}
