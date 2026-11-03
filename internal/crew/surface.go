package crew

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// WriteSurface defines the exclusive write boundary patterns for an agent or crewmate.
type WriteSurface struct {
	AgentID   string         `json:"agent_id"`
	GoalSlug  string         `json:"goal_slug,omitempty"`
	RoleClass AgentRoleClass `json:"role_class,omitempty"`
	Patterns  []string       `json:"patterns"` // Positive patterns (e.g. "internal/crew/**") and exclusions ("!*.md")
}

// SurfaceCollisionError reports when two agents claim overlapping write surfaces.
type SurfaceCollisionError struct {
	AgentA   string   `json:"agent_a"`
	AgentB   string   `json:"agent_b"`
	GoalA    string   `json:"goal_a,omitempty"`
	GoalB    string   `json:"goal_b,omitempty"`
	Overlaps []string `json:"overlaps"`
}

func (e *SurfaceCollisionError) Error() string {
	return fmt.Sprintf("write-surface collision between @%s and @%s on %v",
		e.AgentA, e.AgentB, e.Overlaps)
}

// NormalizeSurfacePath standardizes a path to forward slashes with trimmed boundaries.
func NormalizeSurfacePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	return strings.Trim(p, "/")
}

// IsSharedLedgerFile reports whether a path is a repository-wide ledger or metadata file
// that is shared across tasks and exempt from single-owner exclusivity.
func IsSharedLedgerFile(path string) bool {
	norm := NormalizeSurfacePath(path)
	normNoCli := strings.TrimPrefix(norm, "cli/")
	if normNoCli == "CHANGELOG.md" || normNoCli == "goals/README.md" {
		return true
	}
	if strings.HasPrefix(normNoCli, ".sila/") || strings.HasPrefix(normNoCli, ".git/") {
		return true
	}
	return false
}

// MatchGlob evaluates whether a path matches a glob pattern supporting:
// - "**" for recursive directory descent
// - "*" for single path component wildcard
// - "?" for single character wildcard
// - Exact paths and directory prefix matches
func MatchGlob(pattern, path string) bool {
	pattern = NormalizeSurfacePath(pattern)
	path = NormalizeSurfacePath(path)

	if pattern == "" || path == "" {
		return false
	}
	if pattern == "*" || pattern == "**" || pattern == "." {
		return true
	}
	if pattern == path {
		return true
	}

	// Exact directory prefix: e.g. pattern "internal/crew" matches "internal/crew/foo.go"
	if !strings.ContainsAny(pattern, "*?[") {
		if strings.HasPrefix(path, pattern+"/") {
			return true
		}
	}

	// Extension shorthand: e.g. "*.go" matching filename
	if strings.HasPrefix(pattern, "*.") && !strings.Contains(pattern, "/") {
		ext := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(strings.ToLower(path), strings.ToLower(ext))
	}

	re, err := globToRegex(pattern)
	if err != nil {
		// Fallback to stdlib filepath.Match on base and path
		if m, _ := filepath.Match(pattern, path); m {
			return true
		}
		if m, _ := filepath.Match(pattern, filepath.Base(path)); m {
			return true
		}
		return false
	}

	if re.MatchString(path) {
		return true
	}

	// If pattern has no slash, also test against basename
	if !strings.Contains(pattern, "/") {
		return re.MatchString(filepath.Base(path))
	}

	return false
}

// globToRegex compiles a glob pattern into an anchored regular expression.
func globToRegex(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")

	i := 0
	n := len(pattern)
	for i < n {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < n && pattern[i+1] == '*' {
				// Double star: "**"
				i += 2
				if i < n && pattern[i] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				// Single star: "*"
				i++
				b.WriteString("[^/]*")
			}
		case '?':
			i++
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '{', '}', '[', ']', '^', '$', '\\':
			i++
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			i++
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// CanWrite reports whether the given path may be modified under this WriteSurface.
// Reviewers are structurally read-only and always return false.
// Builders must match at least one positive pattern and zero negative patterns (!).
func (s *WriteSurface) CanWrite(path string) bool {
	if s == nil {
		return false
	}
	// Invariant: Reviewer agents are structurally read-only with no write surface.
	if s.RoleClass == RoleClassReviewer {
		return false
	}

	norm := NormalizeSurfacePath(path)
	if norm == "" {
		return false
	}

	// Shared ledger files are allowed unless explicitly excluded by !pattern
	isShared := IsSharedLedgerFile(norm)

	// Check explicit exclusions first (!pattern)
	for _, pat := range s.Patterns {
		pat = strings.TrimSpace(pat)
		if strings.HasPrefix(pat, "!") {
			neg := strings.TrimPrefix(pat, "!")
			if isShared && (strings.ContainsAny(neg, "*?[") || !strings.EqualFold(neg, norm)) {
				// Generic wildcard exclusions (e.g. !*.md) do not block shared ledger files
				continue
			}
			if MatchGlob(neg, norm) {
				return false
			}
		}
	}

	if isShared {
		return true
	}

	if len(s.Patterns) == 0 {
		return false
	}

	hasPositive := false
	positiveMatch := false
	for _, pat := range s.Patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" || strings.HasPrefix(pat, "!") {
			continue
		}
		hasPositive = true
		if MatchGlob(pat, norm) {
			positiveMatch = true
			break
		}
	}

	if !hasPositive {
		return true // only exclusions were specified and none matched
	}
	return positiveMatch
}

// DetectCollision checks whether two WriteSurfaces overlap on any patterns or sample paths.
// Reviewer agents never collide since they have zero write surface.
func DetectCollision(s1, s2 *WriteSurface, samplePaths ...string) []string {
	if s1 == nil || s2 == nil {
		return nil
	}
	// Reviewers are read-only; no write collisions
	if s1.RoleClass == RoleClassReviewer || s2.RoleClass == RoleClassReviewer {
		return nil
	}
	if s1.AgentID != "" && s1.AgentID == s2.AgentID {
		return nil // Same agent
	}

	var overlaps []string
	seen := make(map[string]bool)

	// 1. Check explicit pattern intersection
	for _, p1 := range s1.Patterns {
		p1 = strings.TrimSpace(p1)
		if p1 == "" || strings.HasPrefix(p1, "!") {
			continue
		}
		cleanP1 := NormalizeSurfacePath(p1)
		if IsSharedLedgerFile(cleanP1) {
			continue
		}

		for _, p2 := range s2.Patterns {
			p2 = strings.TrimSpace(p2)
			if p2 == "" || strings.HasPrefix(p2, "!") {
				continue
			}
			cleanP2 := NormalizeSurfacePath(p2)
			if IsSharedLedgerFile(cleanP2) {
				continue
			}

			if patternsOverlap(cleanP1, cleanP2) {
				// Verify neither surface explicitly excludes this overlap
				probe := cleanP1
				if strings.ContainsAny(probe, "*?[") {
					probe = strings.TrimSuffix(probe, "/**")
					probe = strings.TrimSuffix(probe, "/*")
					probe = strings.TrimSuffix(probe, "*")
				}
				if probe != "" && s1.CanWrite(probe) && s2.CanWrite(probe) {
					key := cleanP1 + " <=> " + cleanP2
					if !seen[key] {
						seen[key] = true
						overlaps = append(overlaps, cleanP1)
					}
				}
			}
		}
	}

	// 2. Check sample concrete paths if provided
	for _, sp := range samplePaths {
		sp = NormalizeSurfacePath(sp)
		if sp == "" || IsSharedLedgerFile(sp) {
			continue
		}
		if s1.CanWrite(sp) && s2.CanWrite(sp) {
			if !seen[sp] {
				seen[sp] = true
				overlaps = append(overlaps, sp)
			}
		}
	}

	return overlaps
}

func patternsOverlap(p1, p2 string) bool {
	if p1 == p2 {
		return true
	}
	if p1 == "*" || p1 == "**" || p2 == "*" || p2 == "**" {
		return true
	}
	// Direct subpath check
	if strings.HasPrefix(p1, p2+"/") || strings.HasPrefix(p2, p1+"/") {
		return true
	}
	if MatchGlob(p1, p2) || MatchGlob(p2, p1) {
		return true
	}
	return false
}

// ValidateStagedFilesAgainstSurface asserts that all staged files are within the authorized write surface.
func ValidateStagedFilesAgainstSurface(surface *WriteSurface, stagedFiles []string) error {
	if surface == nil {
		return nil
	}
	if surface.RoleClass == RoleClassReviewer {
		if len(stagedFiles) > 0 {
			return fmt.Errorf("reviewer agent @%s is structurally read-only and cannot mutate files (%d files staged)",
				surface.AgentID, len(stagedFiles))
		}
		return nil
	}

	for _, raw := range stagedFiles {
		clean := NormalizeSurfacePath(raw)
		if clean == "" {
			continue
		}
		if !surface.CanWrite(clean) {
			return fmt.Errorf("file %q violates write-surface boundary for agent @%s (outside patterns or matches exclusion)",
				raw, surface.AgentID)
		}
	}
	return nil
}

// SurfaceMap manages multiple exclusive WriteSurfaces and enforces single-owner partitioning.
type SurfaceMap struct {
	mu       sync.RWMutex
	surfaces map[string]*WriteSurface
}

// NewSurfaceMap initializes an empty SurfaceMap.
func NewSurfaceMap() *SurfaceMap {
	return &SurfaceMap{
		surfaces: make(map[string]*WriteSurface),
	}
}

// Register adds an agent's WriteSurface to the map, validating that it does not collide
// with any already registered builder surface.
func (sm *SurfaceMap) Register(surface *WriteSurface) error {
	if surface == nil {
		return fmt.Errorf("nil write surface")
	}
	if surface.AgentID == "" {
		return fmt.Errorf("agent_id is required")
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Reviewer agents have no write surface, so they never collide
	if surface.RoleClass == RoleClassReviewer {
		sm.surfaces[surface.AgentID] = surface
		return nil
	}

	// Check collision against all existing registered surfaces
	for _, existing := range sm.surfaces {
		if overlaps := DetectCollision(surface, existing); len(overlaps) > 0 {
			return &SurfaceCollisionError{
				AgentA:   surface.AgentID,
				AgentB:   existing.AgentID,
				GoalA:    surface.GoalSlug,
				GoalB:    existing.GoalSlug,
				Overlaps: overlaps,
			}
		}
	}

	sm.surfaces[surface.AgentID] = surface
	return nil
}

// Unregister removes an agent's WriteSurface.
func (sm *SurfaceMap) Unregister(agentID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.surfaces, agentID)
}

// Get returns the WriteSurface for an agent ID.
func (sm *SurfaceMap) Get(agentID string) (*WriteSurface, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.surfaces[agentID]
	return s, ok
}

// FindOwner reports the single builder agent that owns the given path.
// If multiple owners are found, it returns an error reporting the collision.
func (sm *SurfaceMap) FindOwner(path string) (string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	clean := NormalizeSurfacePath(path)
	if clean == "" {
		return "", nil
	}

	var owners []string
	for _, s := range sm.surfaces {
		if s.CanWrite(clean) {
			owners = append(owners, s.AgentID)
		}
	}

	switch len(owners) {
	case 0:
		return "", nil
	case 1:
		return owners[0], nil
	default:
		return "", fmt.Errorf("path %q has multiple conflicting owners: %s",
			path, strings.Join(owners, ", "))
	}
}

// List returns all registered surfaces.
func (sm *SurfaceMap) List() []*WriteSurface {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	list := make([]*WriteSurface, 0, len(sm.surfaces))
	for _, s := range sm.surfaces {
		list = append(list, s)
	}
	return list
}
