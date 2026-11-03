package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// GlobalSkillsDir returns the path to the global skill vault (~/.agents/skills),
// with support for SILA_GLOBAL_SKILLS_DIR environment variable override in tests.
func GlobalSkillsDir() string {
	if env := os.Getenv("SILA_GLOBAL_SKILLS_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agents", "skills")
}

// IsDeniedByPolicy checks if a skill slug is excluded by project configuration.
func IsDeniedByPolicy(projectDir, slug string) bool {
	cleanSlug := strings.ToLower(strings.TrimSpace(slug))

	// 1. Check .agents/skills.json
	harnessPath := filepath.Join(projectDir, ".agents", "skills.json")
	if data, err := os.ReadFile(harnessPath); err == nil {
		var cfg JSONConfig
		if err := json.Unmarshal(data, &cfg); err == nil {
			for _, pat := range cfg.Exclude {
				if matchPattern(pat, cleanSlug) {
					return true
				}
			}
		}
	}

	// 2. Check .sila/config.json
	cfgPath := filepath.Join(projectDir, ".sila", "config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err == nil {
			if skillsMap, ok := raw["skills"].(map[string]interface{}); ok {
				if excList, ok := skillsMap["exclude"].([]interface{}); ok {
					for _, item := range excList {
						if pat, ok := item.(string); ok {
							if matchPattern(pat, cleanSlug) {
								return true
							}
						}
					}
				}
			}
			if excList, ok := raw["excluded_skills"].([]interface{}); ok {
				for _, item := range excList {
					if pat, ok := item.(string); ok {
						if matchPattern(pat, cleanSlug) {
							return true
						}
					}
				}
			}
		}
	}

	return false
}

func matchPattern(pattern, slug string) bool {
	pattern = strings.TrimSpace(strings.ToLower(pattern))
	if pattern == "" {
		return false
	}
	if pattern == slug {
		return true
	}
	// Try regex
	if re, err := regexp.Compile(pattern); err == nil {
		if re.MatchString(slug) {
			return true
		}
	}
	// Try filepath.Match
	if matched, err := filepath.Match(pattern, slug); err == nil && matched {
		return true
	}
	return false
}

// DiscoverDirSkills scans a directory for skills, following symlinks safely.
func DiscoverDirSkills(dir, source string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	realDir, _ := filepath.EvalSymlinks(dir)
	visitedDirs := make(map[string]bool)

	var skills []Skill
	for _, e := range entries {
		targetPath := filepath.Join(dir, e.Name())

		fi, err := os.Stat(targetPath)
		if err != nil || !fi.IsDir() {
			continue
		}

		realPath, err := filepath.EvalSymlinks(targetPath)
		if err != nil {
			continue
		}

		if realDir != "" && realPath == realDir {
			continue
		}

		if visitedDirs[realPath] {
			continue
		}
		visitedDirs[realPath] = true

		slug := e.Name()
		skillPath := filepath.Join(targetPath, "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			skillPath = filepath.Join(targetPath, "skill.md")
			data, err = os.ReadFile(skillPath)
			if err != nil {
				continue
			}
		}

		pf := ParseSkillContent(slug, string(data))
		h := sha256.New()
		h.Write(data)
		contentHash := hex.EncodeToString(h.Sum(nil))

		relPath := filepath.Join(".agents", "skills", slug, filepath.Base(skillPath))
		if source == "global" {
			relPath = skillPath
		}

		sk := Skill{
			Slug:          slug,
			Name:          pf.Name,
			Description:   pf.Description,
			Path:          relPath,
			BundleDir:     targetPath,
			Source:        source,
			ContentHash:   contentHash,
			Instructions:  pf.Body,
			Tags:          pf.Tags,
			Compatibility: pf.Compatibility,
			RawMarkdown:   string(data),
		}

		// Extract triggers from markdown body if any
		sk.Triggers = extractTriggers(pf.Body)

		// Scan resources
		ScanSkillResources(targetPath, &sk)

		skills = append(skills, sk)
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Slug < skills[j].Slug
	})

	return skills, nil
}

func extractTriggers(body string) []string {
	var triggers []string
	lines := strings.Split(body, "\n")
	inTriggers := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## Trigger Scenarios") || strings.HasPrefix(trimmed, "## Triggers") {
			inTriggers = true
			continue
		} else if strings.HasPrefix(trimmed, "## ") && inTriggers {
			inTriggers = false
		}

		if inTriggers {
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "• ") {
				tText := strings.TrimSpace(trimmed[2:])
				if tText != "" {
					triggers = append(triggers, tText)
				}
			}
		}
	}
	return triggers
}

// ListUnifiedSkills returns all skills available to a project (project skills overriding global).
func ListUnifiedSkills(projectDir string) ([]Skill, error) {
	bySlug := make(map[string]Skill)

	// 1. Scan global skills
	if gDir := GlobalSkillsDir(); gDir != "" {
		gSkills, err := DiscoverDirSkills(gDir, "global")
		if err == nil {
			for _, sk := range gSkills {
				if !IsDeniedByPolicy(projectDir, sk.Slug) {
					bySlug[sk.Slug] = sk
				}
			}
		}
	}

	// 2. Scan project skills (override global)
	pDir := filepath.Join(projectDir, ".agents", "skills")
	pSkills, err := DiscoverDirSkills(pDir, "project")
	if err == nil {
		for _, sk := range pSkills {
			if !IsDeniedByPolicy(projectDir, sk.Slug) {
				bySlug[sk.Slug] = sk
			}
		}
	}

	result := make([]Skill, 0, len(bySlug))
	for _, sk := range bySlug {
		result = append(result, sk)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Slug < result[j].Slug
	})

	return result, nil
}

// SearchCatalog searches skills matching query and tags up to limit.
func SearchCatalog(projectDir, query string, tags []string, limit int) ([]Skill, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	allSkills, err := ListUnifiedSkills(projectDir)
	if err != nil {
		return nil, err
	}

	qClean := strings.ToLower(strings.TrimSpace(query))
	qTokens := strings.Fields(qClean)

	type scoredSkill struct {
		skill Skill
		score int
	}

	var scored []scoredSkill

	tagFilters := make(map[string]bool)
	for _, t := range tags {
		tClean := strings.ToLower(strings.TrimSpace(t))
		if tClean != "" {
			tagFilters[tClean] = true
		}
	}

	for _, sk := range allSkills {
		score := 0
		sSlug := strings.ToLower(sk.Slug)
		sName := strings.ToLower(sk.Name)
		sDesc := strings.ToLower(sk.Description)

		// Check tag filters if specified
		if len(tagFilters) > 0 {
			hasMatchingTag := false
			for _, st := range sk.Tags {
				if tagFilters[strings.ToLower(st)] {
					hasMatchingTag = true
					score += 25
					break
				}
			}
			if !hasMatchingTag {
				// Also check if any trigger scenario matches tag
				for _, tr := range sk.Triggers {
					if tagFilters[strings.ToLower(tr)] {
						hasMatchingTag = true
						score += 25
						break
					}
				}
			}
			if !hasMatchingTag && qClean == "" {
				continue
			}
		}

		if qClean != "" {
			if sSlug == qClean {
				score += 100
			} else if strings.HasPrefix(sSlug, qClean) {
				score += 60
			} else if strings.Contains(sSlug, qClean) {
				score += 50
			}

			if sName == qClean {
				score += 80
			} else if strings.Contains(sName, qClean) {
				score += 40
			}

			for _, tok := range qTokens {
				if strings.Contains(sSlug, tok) {
					score += 20
				}
				if strings.Contains(sName, tok) {
					score += 15
				}
				if strings.Contains(sDesc, tok) {
					score += 10
				}
				for _, tr := range sk.Triggers {
					if strings.Contains(strings.ToLower(tr), tok) {
						score += 15
						break
					}
				}
			}
		} else if len(tagFilters) > 0 {
			// Query is empty but tags matched
			score += 10
		} else {
			// Both query and tags empty: match all with base score
			score = 1
		}

		if score > 0 {
			if sk.Source == "project" {
				score += 5 // Local precedence tie breaker
			}
			scored = append(scored, scoredSkill{skill: sk, score: score})
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].skill.Slug < scored[j].skill.Slug
	})

	if len(scored) > limit {
		scored = scored[:limit]
	}

	res := make([]Skill, len(scored))
	for i, s := range scored {
		res[i] = s.skill
	}

	return res, nil
}

// LoadSkill loads a specific skill by slug from project or global catalogs.
func LoadSkill(projectDir, slug string, includeResources bool) (*Skill, error) {
	cleanSlug := strings.TrimSpace(slug)
	cleanSlug = strings.TrimPrefix(cleanSlug, ".agents/skills/")
	cleanSlug = strings.TrimSuffix(cleanSlug, "/SKILL.md")
	cleanSlug = strings.TrimSuffix(cleanSlug, "/skill.md")
	cleanSlug = strings.Trim(cleanSlug, "/")

	if cleanSlug == "" || cleanSlug != filepath.Base(cleanSlug) ||
		strings.ContainsAny(cleanSlug, `/\\`) || strings.Contains(cleanSlug, "..") {
		return nil, fmt.Errorf("invalid skill slug %q: path traversal or separators prohibited", slug)
	}

	if IsDeniedByPolicy(projectDir, cleanSlug) {
		return nil, fmt.Errorf("skill %q is explicitly denied by project policy", cleanSlug)
	}

	// 1. Check project catalog
	pSkillDir := filepath.Join(projectDir, ".agents", "skills", cleanSlug)
	sk, err := loadFromDir(pSkillDir, cleanSlug, "project", includeResources)
	if err == nil {
		return sk, nil
	}

	// 2. Check global catalog
	if gDir := GlobalSkillsDir(); gDir != "" {
		gSkillDir := filepath.Join(gDir, cleanSlug)
		sk, err = loadFromDir(gSkillDir, cleanSlug, "global", includeResources)
		if err == nil {
			return sk, nil
		}
	}

	return nil, fmt.Errorf("skill %q not found in project or global catalogs", cleanSlug)
}

func loadFromDir(bundleDir, slug, source string, includeResources bool) (*Skill, error) {
	fi, err := os.Stat(bundleDir)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("bundle directory %s not found", bundleDir)
	}

	realBundle, err := filepath.EvalSymlinks(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("resolve bundle directory %s: %w", bundleDir, err)
	}

	skillPath := filepath.Join(realBundle, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		skillPath = filepath.Join(realBundle, "skill.md")
		data, err = os.ReadFile(skillPath)
		if err != nil {
			return nil, fmt.Errorf("missing SKILL.md in %s", bundleDir)
		}
	}

	pf := ParseSkillContent(slug, string(data))
	h := sha256.New()
	h.Write(data)
	contentHash := hex.EncodeToString(h.Sum(nil))

	relPath := filepath.Join(".agents", "skills", slug, filepath.Base(skillPath))
	if source == "global" {
		relPath = skillPath
	}

	sk := &Skill{
		Slug:          slug,
		Name:          pf.Name,
		Description:   pf.Description,
		Path:          relPath,
		BundleDir:     bundleDir,
		Source:        source,
		ContentHash:   contentHash,
		Instructions:  pf.Body,
		Tags:          pf.Tags,
		Compatibility: pf.Compatibility,
		RawMarkdown:   string(data),
	}

	sk.Triggers = extractTriggers(pf.Body)
	ScanSkillResources(realBundle, sk)

	if includeResources {
		var totalBytes int64
		const maxPerFile = 64 * 1024
		const maxTotal = 256 * 1024
		budgetExhausted := false

		addResources := func(paths []string, resType string) {
			for _, rel := range paths {
				if budgetExhausted || totalBytes >= maxTotal {
					break
				}
				resolved, err := ResolveResourcePath(realBundle, rel)
				if err != nil {
					continue
				}
				content, truncated, err := ReadResourceBounded(resolved, maxPerFile)
				if err != nil {
					continue
				}
				if truncated {
					content += "\n\n... [TRUNCATED: resource exceeds 64KB limit]"
				}
				if totalBytes+int64(len(content)) > maxTotal {
					budgetExhausted = true
					break
				}
				totalBytes += int64(len(content))
				sk.Resources = append(sk.Resources, SkillResource{
					Path:    rel,
					Type:    resType,
					Content: content,
				})
			}
		}

		addResources(sk.Scripts, "script")
		addResources(sk.References, "reference")
		addResources(sk.Assets, "asset")
	}

	return sk, nil
}

// ListSkillSummaries returns compact SkillSummary items for skills available to a project,
// optionally filtered by a substring query or prefix.
func ListSkillSummaries(projectDir string, filter string) ([]SkillSummary, error) {
	skillsList, err := ListUnifiedSkills(projectDir)
	if err != nil {
		return nil, err
	}

	filterClean := strings.ToLower(strings.TrimSpace(filter))
	var summaries []SkillSummary
	for _, s := range skillsList {
		if filterClean != "" {
			if !strings.Contains(strings.ToLower(s.Slug), filterClean) &&
				!strings.Contains(strings.ToLower(s.Name), filterClean) &&
				!strings.Contains(strings.ToLower(s.Description), filterClean) {
				continue
			}
		}
		summaries = append(summaries, s.ToSummary())
	}
	return summaries, nil
}

// SearchCatalogWithLessons fuses skills and grounded lessons using Reciprocal Rank Fusion (k=60).
// - Tier 1: Authoritative Skills (weight = 1.0)
// - Tier 2: Grounded Runbook Lessons with matching fix patterns (weight = 0.85)
func SearchCatalogWithLessons(projectDir string, query string, tags []string, limit int, st *store.Store) ([]FusedSkillResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	skillsList, err := SearchCatalog(projectDir, query, tags, limit)
	if err != nil {
		return nil, err
	}

	type scoreAcc struct {
		result FusedSkillResult
		score  float64
	}
	fusedMap := make(map[string]*scoreAcc)

	// 1. Tier 1: Authoritative Skills (weight = 1.0)
	for i, sk := range skillsList {
		rrf := 1.0 / (60.0 + float64(i+1))
		summary := sk.ToSummary()
		fusedMap[sk.Slug] = &scoreAcc{
			result: FusedSkillResult{
				SkillSummary: summary,
				RRFScore:     rrf,
				Tier:         1,
				Path:         sk.Path,
			},
			score: rrf,
		}
	}

	// 2. Tier 2: Grounded Runbook Lessons (weight = 0.85)
	if st != nil && strings.TrimSpace(query) != "" {
		qTokens := strings.Fields(strings.ToLower(query))
		lessons, err := st.LessonFacts("", 50)
		if err == nil {
			var matchingLessons []store.Fact
			for _, l := range lessons {
				symptom, cause, fix, lTags := store.ParseLesson(l.Claim)
				haystack := strings.ToLower(fmt.Sprintf("%s %s %s %s", symptom, cause, fix, lTags))
				matches := false
				for _, tok := range qTokens {
					if strings.Contains(haystack, tok) {
						matches = true
						break
					}
				}
				if matches {
					matchingLessons = append(matchingLessons, l)
				}
			}

			for j, l := range matchingLessons {
				if j >= limit {
					break
				}
				rrf := 0.85 / (60.0 + float64(j+1))
				symptom, cause, fix, _ := store.ParseLesson(l.Claim)
				slug := "lesson-" + l.ID
				trigger := fix
				if trigger == "" {
					trigger = cause
				}
				if trigger == "" {
					trigger = symptom
				}
				trigger = ExtractTrigger(trigger)

				if existing, exists := fusedMap[slug]; exists {
					existing.score += rrf
					existing.result.RRFScore = existing.score
				} else {
					fusedMap[slug] = &scoreAcc{
						result: FusedSkillResult{
							SkillSummary: SkillSummary{
								Slug:            slug,
								Name:            symptom,
								Trigger:         trigger,
								EstimatedTokens: store.EstimateTokens(len(l.Claim)),
								HasScripts:      false,
								Source:          "grounded_lesson",
							},
							RRFScore: rrf,
							Tier:     2,
							Path:     strings.Join(l.RepoFiles, ", "),
						},
						score: rrf,
					}
				}
			}
		}
	}

	var results []FusedSkillResult
	for _, acc := range fusedMap {
		acc.result.RRFScore = acc.score
		results = append(results, acc.result)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].RRFScore == results[j].RRFScore {
			return results[i].Tier < results[j].Tier
		}
		return results[i].RRFScore > results[j].RRFScore
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}
