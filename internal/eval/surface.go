package eval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ComponentID identifies one of the 8 harness surface components (c1..c8).
type ComponentID string

const (
	CompSystemPrompt ComponentID = "c1"
	CompGuardrails   ComponentID = "c2"
	CompMemory       ComponentID = "c3"
	CompToolGuidance ComponentID = "c4"
	CompSkills       ComponentID = "c5"
	CompSubagents    ComponentID = "c6"
	CompScripts      ComponentID = "c7"
	CompHooks        ComponentID = "c8"
)

// SurfaceSpec defines the filesystem layout and always-on character budget for an agent substrate.
type SurfaceSpec struct {
	Name          string   `json:"name"`
	AlwaysOn      []string `json:"always_on"`
	SkillsGlob    string   `json:"skills_glob"`
	SubagentsGlob string   `json:"subagents_glob"`
	ScriptsGlob   string   `json:"scripts_glob"`
	HooksGlob     string   `json:"hooks_glob"`
	AlwaysOnMax   int      `json:"always_on_max"`
}

// Preset surface specifications matching Google Research harness2 substrates.
var (
	OpenCodeSurface = SurfaceSpec{
		Name: "opencode",
		AlwaysOn: []string{
			"prompts/systemprompt.md",
			"prompts/guardrails.md",
			"prompts/LongTermMEMORY.md",
			"prompts/tool_guidance.md",
		},
		SkillsGlob:    "skills/*/SKILL.md",
		SubagentsGlob: "sub_agents/*/AGENT.md",
		ScriptsGlob:   "scripts/*",
		HooksGlob:     "plugin/*",
		AlwaysOnMax:   24000,
	}

	CodexSurface = SurfaceSpec{
		Name:          "codex",
		AlwaysOn:      []string{"AGENTS.md"},
		SkillsGlob:    "skills/*/SKILL.md",
		SubagentsGlob: "sub_agents/*/AGENT.md",
		ScriptsGlob:   "scripts/*",
		HooksGlob:     ".codex/hooks.json",
		AlwaysOnMax:   8000,
	}

	ClaudeSurface = SurfaceSpec{
		Name:          "claude",
		AlwaysOn:      []string{"CLAUDE.md", "AGENTS.md"},
		SkillsGlob:    "skills/*/SKILL.md",
		SubagentsGlob: "sub_agents/*/AGENT.md",
		ScriptsGlob:   "scripts/*",
		HooksGlob:     ".claude/settings.json",
		AlwaysOnMax:   12000,
	}
)

// ResolveSurfaceSpec returns a SurfaceSpec by name ("opencode", "codex", "claude").
func ResolveSurfaceSpec(name string) (SurfaceSpec, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "codex":
		return CodexSurface, nil
	case "opencode":
		return OpenCodeSurface, nil
	case "claude":
		return ClaudeSurface, nil
	default:
		return SurfaceSpec{}, fmt.Errorf("unknown harness surface %q (valid: opencode, codex, claude)", name)
	}
}

// ComponentTree maps slash-normalized relative harness paths to their UTF-8 content.
type ComponentTree map[string]string

// ComponentEdit records a single file addition, modification, or deletion between two trees.
type ComponentEdit struct {
	Component ComponentID `json:"component"`
	Path      string      `json:"path"`
	Action    string      `json:"action"` // "added" | "modified" | "deleted"
}

// CloneTree returns a deep copy of a ComponentTree.
func CloneTree(tree ComponentTree) ComponentTree {
	out := make(ComponentTree, len(tree))
	for k, v := range tree {
		out[k] = v
	}
	return out
}

// AlwaysOnChars returns the total character (byte) count across all spec.AlwaysOn files.
func AlwaysOnChars(tree ComponentTree, spec SurfaceSpec) int {
	total := 0
	for _, rel := range spec.AlwaysOn {
		total += len(tree[rel])
	}
	return total
}

// ClassifyComponent maps a slash-normalized relative file path to its ComponentID (c1..c8).
func ClassifyComponent(relPath string, spec SurfaceSpec) ComponentID {
	norm := filepath.ToSlash(strings.TrimPrefix(relPath, "./"))
	for i, ao := range spec.AlwaysOn {
		if norm == ao {
			switch i {
			case 0:
				return CompSystemPrompt
			case 1:
				return CompGuardrails
			case 2:
				return CompMemory
			default:
				return CompToolGuidance
			}
		}
	}
	if matchGlob(spec.SkillsGlob, norm) || strings.HasPrefix(norm, "skills/") {
		return CompSkills
	}
	if matchGlob(spec.SubagentsGlob, norm) || strings.HasPrefix(norm, "sub_agents/") {
		return CompSubagents
	}
	if matchGlob(spec.ScriptsGlob, norm) || strings.HasPrefix(norm, "scripts/") {
		return CompScripts
	}
	if (spec.HooksGlob != "" && matchGlob(spec.HooksGlob, norm)) ||
		strings.HasPrefix(norm, "plugin/") ||
		norm == ".codex/hooks.json" ||
		norm == ".claude/settings.json" {
		return CompHooks
	}
	return CompSystemPrompt
}

func matchGlob(pattern, relPath string) bool {
	if pattern == "" {
		return false
	}
	ok, err := path.Match(pattern, relPath)
	return err == nil && ok
}

func belongsToSurface(relPath string, spec SurfaceSpec) bool {
	for _, ao := range spec.AlwaysOn {
		if relPath == ao {
			return true
		}
	}
	for _, g := range []string{spec.SkillsGlob, spec.SubagentsGlob, spec.ScriptsGlob, spec.HooksGlob} {
		if matchGlob(g, relPath) {
			return true
		}
	}
	return false
}

// ReadTree reads all harness files matching spec from rootDir into a ComponentTree.
func ReadTree(rootDir string, spec SurfaceSpec) (ComponentTree, error) {
	tree := make(ComponentTree)
	info, err := os.Stat(rootDir)
	if err != nil {
		return nil, fmt.Errorf("stat harness root %s: %w", rootDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("harness root %s is not a directory", rootDir)
	}

	err = filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(rootDir, p)
		if err != nil {
			return err
		}
		norm := filepath.ToSlash(rel)
		if !belongsToSurface(norm, spec) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", norm, err)
		}
		tree[norm] = string(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tree, nil
}

// WriteTree writes all entries in tree into rootDir, making scripts/* executable (0755).
func WriteTree(rootDir string, tree ComponentTree) error {
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, rel := range keys {
		cleanRel := filepath.Clean(filepath.FromSlash(rel))
		if strings.HasPrefix(cleanRel, "..") || filepath.IsAbs(cleanRel) {
			return fmt.Errorf("unsafe relative path in harness tree: %q", rel)
		}
		dest := filepath.Join(rootDir, cleanRel)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", rel, err)
		}
		mode := os.FileMode(0644)
		if strings.HasPrefix(filepath.ToSlash(rel), "scripts/") {
			mode = 0755
		}
		if err := os.WriteFile(dest, []byte(tree[rel]), mode); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// DiffComponentTrees computes deterministic sorted component diffs between base and candidate trees.
func DiffComponentTrees(base, candidate ComponentTree, spec SurfaceSpec) []ComponentEdit {
	seen := make(map[string]bool, len(base)+len(candidate))
	var paths []string
	for k := range base {
		seen[k] = true
		paths = append(paths, k)
	}
	for k := range candidate {
		if !seen[k] {
			paths = append(paths, k)
		}
	}
	sort.Strings(paths)

	edits := make([]ComponentEdit, 0)
	for _, p := range paths {
		bVal, inBase := base[p]
		cVal, inCand := candidate[p]
		comp := ClassifyComponent(p, spec)
		switch {
		case !inBase && inCand:
			edits = append(edits, ComponentEdit{
				Component: comp,
				Path:      p,
				Action:    "added",
			})
		case inBase && !inCand:
			edits = append(edits, ComponentEdit{
				Component: comp,
				Path:      p,
				Action:    "deleted",
			})
		case bVal != cVal:
			edits = append(edits, ComponentEdit{
				Component: comp,
				Path:      p,
				Action:    "modified",
			})
		}
	}
	return edits
}

var (
	frontmatterRE = regexp.MustCompile(`(?s)\A---\s*\n(.*?)\n---\s*\n`)
	fmNameRE      = regexp.MustCompile(`(?m)^name:\s*(.+?)\s*$`)
	fmDescRE      = regexp.MustCompile(`(?m)^description:\s*(.+?)\s*$`)
	scriptRefRE   = regexp.MustCompile(`\b(scripts/[A-Za-z0-9_.\-/]+)`)
	graderLeakRE  = regexp.MustCompile(`(?i)(\bPASS:\s*grader|\bFAIL:\s*grader|__SILA_GRADER_VERDICT__|\bgrader_verdict\b)`)
)

// AutoTriggerSkills deterministically injects "- When <description>, load the <name> skill."
// into spec.AlwaysOn[0] for any skill whose name does not yet appear in always-on text,
// provided the addition stays within spec.AlwaysOnMax.
func AutoTriggerSkills(tree ComponentTree, spec SurfaceSpec) ComponentTree {
	cloned := CloneTree(tree)
	if len(spec.AlwaysOn) == 0 {
		return cloned
	}

	var aoBuf strings.Builder
	for _, rel := range spec.AlwaysOn {
		aoBuf.WriteString(cloned[rel])
		aoBuf.WriteByte('\n')
	}
	alwaysOnText := aoBuf.String()

	paths := make([]string, 0, len(cloned))
	for k := range cloned {
		paths = append(paths, k)
	}
	sort.Strings(paths)

	var lines []string
	for _, rel := range paths {
		if !matchGlob(spec.SkillsGlob, rel) {
			continue
		}
		parts := strings.Split(rel, "/")
		if len(parts) < 2 {
			continue
		}
		skillName := parts[1]
		if strings.Contains(alwaysOnText, skillName) {
			continue
		}
		fmMatch := frontmatterRE.FindStringSubmatch(cloned[rel])
		if len(fmMatch) < 2 {
			continue
		}
		descMatch := fmDescRE.FindStringSubmatch(fmMatch[1])
		if len(descMatch) < 2 {
			continue
		}
		desc := strings.Trim(strings.TrimSpace(descMatch[1]), `"'`)
		if desc == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- When %s, load the %s skill.", desc, skillName))
	}

	if len(lines) == 0 {
		return cloned
	}

	addition := "\n" + strings.Join(lines, "\n") + "\n"
	if AlwaysOnChars(cloned, spec)+len(addition) <= spec.AlwaysOnMax {
		primary := spec.AlwaysOn[0]
		cloned[primary] = strings.TrimRight(cloned[primary], "\n") + addition
	}
	return cloned
}

// ValidateTree checks a candidate ComponentTree before rollout.
func ValidateTree(tree ComponentTree, spec SurfaceSpec) error {
	if len(tree) == 0 {
		return fmt.Errorf("harness tree is empty")
	}

	var errs []string
	if len(spec.AlwaysOn) > 0 {
		primary := spec.AlwaysOn[0]
		if strings.TrimSpace(tree[primary]) == "" {
			errs = append(errs, fmt.Sprintf("%s is missing or empty", primary))
		}
	}

	used := AlwaysOnChars(tree, spec)
	if used > spec.AlwaysOnMax {
		var perFile []string
		for _, rel := range spec.AlwaysOn {
			if c, ok := tree[rel]; ok {
				perFile = append(perFile, fmt.Sprintf("%s=%d", rel, len(c)))
			}
		}
		errs = append(errs, fmt.Sprintf(
			"always-on surface is %d chars (%s), exceeds budget of %d chars by %d; trim prose or move detail into an on-demand skill or script",
			used, strings.Join(perFile, ", "), spec.AlwaysOnMax, used-spec.AlwaysOnMax,
		))
	}

	errs = append(errs, validateSkills(tree, spec)...)
	errs = append(errs, validateSubagents(tree, spec)...)
	errs = append(errs, validateScriptRefs(tree, spec)...)
	errs = append(errs, validateHooks(tree, spec)...)
	errs = append(errs, validateNoGraderLeaks(tree)...)

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

func validateSkills(tree ComponentTree, spec SurfaceSpec) []string {
	var aoBuf strings.Builder
	for _, rel := range spec.AlwaysOn {
		aoBuf.WriteString(tree[rel])
		aoBuf.WriteByte('\n')
	}
	alwaysOnText := aoBuf.String()

	paths := sortedKeys(tree)
	var errs []string
	for _, rel := range paths {
		if !matchGlob(spec.SkillsGlob, rel) {
			continue
		}
		content := tree[rel]
		if strings.TrimSpace(content) == "" {
			errs = append(errs, fmt.Sprintf("%s is empty", rel))
			continue
		}
		parts := strings.Split(rel, "/")
		skillDir := ""
		if len(parts) >= 2 {
			skillDir = parts[1]
		}
		fmMatch := frontmatterRE.FindStringSubmatch(content)
		if len(fmMatch) < 2 {
			errs = append(errs, fmt.Sprintf("%s: missing YAML frontmatter with `description:`", rel))
			continue
		}
		fm := fmMatch[1]
		descMatch := fmDescRE.FindStringSubmatch(fm)
		if len(descMatch) < 2 || strings.Trim(strings.TrimSpace(descMatch[1]), `"'`) == "" {
			errs = append(errs, fmt.Sprintf("%s: frontmatter `description:` is missing or empty", rel))
		}
		if nameMatch := fmNameRE.FindStringSubmatch(fm); len(nameMatch) >= 2 {
			fmName := strings.Trim(strings.TrimSpace(nameMatch[1]), `"'`)
			if skillDir != "" && fmName != skillDir {
				errs = append(errs, fmt.Sprintf("%s: frontmatter `name: %s` does not match directory `%s`", rel, fmName, skillDir))
			}
		}
		if skillDir != "" && !strings.Contains(alwaysOnText, skillDir) {
			primary := "AGENTS.md"
			if len(spec.AlwaysOn) > 0 {
				primary = spec.AlwaysOn[0]
			}
			errs = append(errs, fmt.Sprintf(
				"skill `%s` (%s) is never mentioned in always-on instructions; add a `When <trigger>, load the %s skill` cue to %s",
				skillDir, rel, skillDir, primary,
			))
		}
	}
	return errs
}

func validateSubagents(tree ComponentTree, spec SurfaceSpec) []string {
	if spec.SubagentsGlob == "" {
		return nil
	}
	paths := sortedKeys(tree)
	var errs []string
	for _, rel := range paths {
		if !matchGlob(spec.SubagentsGlob, rel) {
			continue
		}
		fmMatch := frontmatterRE.FindStringSubmatch(tree[rel])
		if len(fmMatch) < 2 {
			errs = append(errs, fmt.Sprintf("%s: missing YAML frontmatter with `description:`", rel))
			continue
		}
		descMatch := fmDescRE.FindStringSubmatch(fmMatch[1])
		if len(descMatch) < 2 || strings.Trim(strings.TrimSpace(descMatch[1]), `"'`) == "" {
			errs = append(errs, fmt.Sprintf("%s: frontmatter `description:` is missing or empty", rel))
		}
	}
	return errs
}

func validateScriptRefs(tree ComponentTree, spec SurfaceSpec) []string {
	alwaysOnSet := make(map[string]bool, len(spec.AlwaysOn))
	for _, rel := range spec.AlwaysOn {
		alwaysOnSet[rel] = true
	}

	paths := sortedKeys(tree)
	var errs []string
	for _, rel := range paths {
		if !alwaysOnSet[rel] && !matchGlob(spec.SkillsGlob, rel) {
			continue
		}
		matches := scriptRefRE.FindAllStringSubmatch(tree[rel], -1)
		seenRef := make(map[string]bool)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			ref := strings.TrimRight(m[1], ".,;:)`'\"")
			if seenRef[ref] {
				continue
			}
			seenRef[ref] = true
			if _, ok := tree[ref]; !ok {
				errs = append(errs, fmt.Sprintf("%s references %s which is not in the harness tree", rel, ref))
			}
		}
	}
	return errs
}

var validCodexHookEvents = map[string]bool{
	"PreToolUse":       true,
	"PostToolUse":      true,
	"SessionStart":     true,
	"UserPromptSubmit": true,
	"Stop":             true,
}

func validateHooks(tree ComponentTree, spec SurfaceSpec) []string {
	if spec.HooksGlob != ".codex/hooks.json" {
		return nil
	}
	raw, ok := tree[".codex/hooks.json"]
	if !ok {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return []string{fmt.Sprintf(".codex/hooks.json: invalid JSON: %v", err)}
	}
	hooksVal, ok := parsed["hooks"]
	if !ok {
		return []string{`.codex/hooks.json: top-level object must have a "hooks" key`}
	}
	hooksMap, ok := hooksVal.(map[string]any)
	if !ok {
		return []string{`.codex/hooks.json: "hooks" must be a JSON object`}
	}

	events := make([]string, 0, len(hooksMap))
	for ev := range hooksMap {
		events = append(events, ev)
	}
	sort.Strings(events)

	var errs []string
	for _, ev := range events {
		if !validCodexHookEvents[ev] {
			errs = append(errs, fmt.Sprintf(".codex/hooks.json: unknown event '%s'", ev))
			continue
		}
		groups, ok := hooksMap[ev].([]any)
		if !ok {
			errs = append(errs, fmt.Sprintf(".codex/hooks.json: hooks.%s must be a list of matcher groups", ev))
			continue
		}
		for i, g := range groups {
			gMap, ok := g.(map[string]any)
			if !ok {
				errs = append(errs, fmt.Sprintf(".codex/hooks.json: hooks.%s[%d] must be an object with 'hooks'", ev, i))
				continue
			}
			innerList, ok := gMap["hooks"].([]any)
			if !ok {
				errs = append(errs, fmt.Sprintf(".codex/hooks.json: hooks.%s[%d] must contain a 'hooks' array", ev, i))
				continue
			}
			for j, h := range innerList {
				hMap, ok := h.(map[string]any)
				if !ok {
					errs = append(errs, fmt.Sprintf(".codex/hooks.json: hooks.%s[%d].hooks[%d] must be an object", ev, i, j))
					continue
				}
				hType, _ := hMap["type"].(string)
				hCmd, _ := hMap["command"].(string)
				if hType != "command" || strings.TrimSpace(hCmd) == "" {
					errs = append(errs, fmt.Sprintf(".codex/hooks.json: hooks.%s[%d].hooks[%d] must have type='command' and non-empty 'command'", ev, i, j))
				}
			}
		}
	}
	return errs
}

func validateNoGraderLeaks(tree ComponentTree) []string {
	paths := sortedKeys(tree)
	var errs []string
	for _, rel := range paths {
		if graderLeakRE.MatchString(tree[rel]) {
			errs = append(errs, fmt.Sprintf("%s contains leaked grader verdict or rubric marker", rel))
		}
	}
	return errs
}

func sortedKeys(tree ComponentTree) []string {
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
