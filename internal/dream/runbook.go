package dream

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// RunbookEntry represents a single verified runbook command.
type RunbookEntry struct {
	Phase   string `json:"phase"`            // e.g. "Test", "Build", "Gate"
	Command string `json:"command"`          // e.g. "go test -race ./...", "./build.sh"
	Source  string `json:"source,omitempty"` // e.g. "AGENTS.md", "manifest"
}

// RunbookSummary contains the verified runbook commands for a project.
type RunbookSummary struct {
	Entries []RunbookEntry `json:"entries,omitempty"`
}

var (
	testCmdRegex  = regexp.MustCompile(`(?i)(go test[^\n` + "`" + `]+|(aubr|pnpm) (run )?test[^\n` + "`" + `]*|cargo test[^\n` + "`" + `]*|pytest[^\n` + "`" + `]*|python3? (-m (pytest|unittest)[^\n` + "`" + `]*|[^\n` + "`" + `;&|]*\b(test|audit)[a-z0-9_.-]*\.py[^\n` + "`" + `;&|]*))`)
	buildCmdRegex = regexp.MustCompile(`(?i)(\./build\.sh|go build[^\n` + "`" + `]+|(aubr|pnpm) (run )?build[^\n` + "`" + `]*|cargo build[^\n` + "`" + `]*|python3? [^\n` + "`" + `;&|]+\.py[^\n` + "`" + `;&|]*)`)
	gateCmdRegex  = regexp.MustCompile(`(?i)(sila gate|golangci-lint[^\n` + "`" + `]*)`)
	cdPyRegex     = regexp.MustCompile(`^cd\s+([A-Za-z0-9._/-]+)\s*&&\s*(python3?)\s+([A-Za-z0-9._/-]+\.py)(.*)$`)
	negationRegex = regexp.MustCompile("(?i)(\\bnot\\b|\\bnever\\b|\\bavoid\\b|\\bcannot\\b|won't|doesn't|shouldn't|do not|don't|instead of|deprecated|superseded|replaced|no longer|\u274c|\U0001F6AB)")
)

// repoStack identifies the primary language stack of a directory.
type repoStack string

const (
	stackUnknown repoStack = "unknown"
	stackGo      repoStack = "go"
	stackNode    repoStack = "node"
	stackRust    repoStack = "rust"
	stackPython  repoStack = "python"
)

func detectStack(dir string) repoStack {
	if dir == "" {
		return stackUnknown
	}
	if fileExists(filepath.Join(dir, "go.mod")) || fileExists(filepath.Join(dir, "go.work")) || hasGoFiles(dir) {
		return stackGo
	}
	if fileExists(filepath.Join(dir, "package.json")) {
		return stackNode
	}
	if fileExists(filepath.Join(dir, "Cargo.toml")) {
		return stackRust
	}
	if fileExists(filepath.Join(dir, "pyproject.toml")) || fileExists(filepath.Join(dir, "setup.py")) || hasPyFiles(dir) {
		return stackPython
	}
	return stackUnknown
}

func commandStack(cmd string) repoStack {
	trimmed := strings.TrimSpace(cmd)
	switch {
	case strings.HasPrefix(trimmed, "go "):
		return stackGo
	case strings.HasPrefix(trimmed, "pnpm ") || strings.HasPrefix(trimmed, "aubr ") ||
		strings.HasPrefix(trimmed, "npm ") || strings.HasPrefix(trimmed, "yarn "):
		return stackNode
	case strings.HasPrefix(trimmed, "cargo "):
		return stackRust
	case strings.HasPrefix(trimmed, "pytest") || strings.HasPrefix(trimmed, "python ") || strings.HasPrefix(trimmed, "python3 "):
		return stackPython
	}
	return stackUnknown
}

func hasPyFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
			return true
		}
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			subEntries, err := os.ReadDir(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			for _, se := range subEntries {
				if !se.IsDir() && strings.HasSuffix(se.Name(), ".py") {
					return true
				}
			}
		}
	}
	return false
}

func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// isCommandApplicable checks whether a candidate runbook command is valid
// for the given project directory based on its files and manifests.
func isCommandApplicable(dir, cmd string) bool {
	if dir == "" || cmd == "" {
		return true // If no dir specified (pure markdown parse test), permit candidate
	}

	trimmed := strings.TrimSpace(cmd)

	// Shell scripts (e.g. ./build.sh)
	if strings.HasPrefix(trimmed, "./") {
		parts := strings.Fields(trimmed)
		script := parts[0]
		// If script exists in dir, definitely applicable
		if fileExists(filepath.Join(dir, script)) {
			return true
		}
		// If dir is a dummy dir with no manifests at all (e.g. test fixture), allow ./build.sh
		if detectStack(dir) == stackUnknown {
			return true
		}
		return false
	}

	cmdStk := commandStack(cmd)
	dirStk := detectStack(dir)

	// If directory stack is known and command stack is known, they must not conflict
	if dirStk != stackUnknown && cmdStk != stackUnknown && dirStk != cmdStk {
		return false
	}

	// For Node commands: must have package.json and valid script
	if cmdStk == stackNode {
		scripts := packageScripts(dir)
		if scripts == nil {
			// No package.json in dir: Node command is invalid!
			return false
		}
		parts := strings.Fields(trimmed)
		if len(parts) >= 2 {
			subCmd := parts[1]
			if subCmd == "run" && len(parts) >= 3 {
				subCmd = parts[2]
			}
			if _, ok := scripts[subCmd]; ok {
				return true
			}
			if subCmd == "test" || subCmd == "build" {
				if pickScript(scripts, subCmd, subCmd) != "" {
					return true
				}
			}
			return false
		}
	}

	return true
}

// DetectRunbook discovers the runbook for a project directory.
// It prioritizes explicit commands documented in AGENTS.md that are applicable to
// the selected repository stack, falling back to stack conventions.
func DetectRunbook(dir string) *RunbookSummary {
	if dir == "" {
		return nil
	}

	entries := make([]RunbookEntry, 0, 4)
	seenPhases := make(map[string]bool)

	// 1. Check for explicit commands in AGENTS.md (in dir, or in parent directory if dir is a sub-repo)
	for _, candidatePath := range []string{
		filepath.Join(dir, "AGENTS.md"),
		filepath.Join(filepath.Dir(dir), "AGENTS.md"),
	} {
		if content, err := os.ReadFile(candidatePath); err == nil {
			sourceName := filepath.Base(candidatePath)
			if candidatePath != filepath.Join(dir, "AGENTS.md") {
				sourceName = "../AGENTS.md"
			}
			extracted := parseRunbookFromMarkdown(string(content), dir)
			for _, e := range extracted {
				if !seenPhases[e.Phase] {
					e.Source = sourceName
					entries = append(entries, e)
					seenPhases[e.Phase] = true
				}
			}
			if len(entries) > 0 {
				break
			}
		}
	}

	// 2. Fallback / supplement with project manifest detection
	if !seenPhases["Test"] {
		if cmd := detectTestCommand(dir); cmd != "" {
			entries = append(entries, RunbookEntry{Phase: "Test", Command: cmd, Source: "manifest"})
			seenPhases["Test"] = true
		}
	}
	if !seenPhases["Build"] {
		if cmd := detectBuildCommand(dir); cmd != "" {
			entries = append(entries, RunbookEntry{Phase: "Build", Command: cmd, Source: "manifest"})
			seenPhases["Build"] = true
		}
	}
	if !seenPhases["Gate"] {
		if cmd := detectGateCommand(dir); cmd != "" {
			entries = append(entries, RunbookEntry{Phase: "Gate", Command: cmd, Source: "manifest"})
			seenPhases["Gate"] = true
		}
	}

	if len(entries) == 0 {
		return nil
	}
	return &RunbookSummary{Entries: entries}
}

// parseRunbookFromMarkdown extracts runbook commands from instruction files.
// Commands must be applicable to the selected repository directory and stack.
// Section headers targeting specific subdirectories are honored so that sibling
// repository instructions do not cross-pollute the selected repository.
func parseRunbookFromMarkdown(content string, targetDir ...string) []RunbookEntry {
	var dir string
	if len(targetDir) > 0 {
		dir = targetDir[0]
	}
	baseDir := ""
	if dir != "" {
		baseDir = strings.ToLower(filepath.Base(dir))
	}

	type entryWithScore struct {
		entry    RunbookEntry
		priority int // 2 = target section, 1 = general, 0 = sibling
	}

	entries := make(map[string]entryWithScore)

	currentPriority := 1 // default general section
	scanner := bufio.NewScanner(strings.NewReader(content))
	var inCodeBlock bool
	var blockLines []string

	processCommand := func(cmd string) {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" || strings.HasPrefix(cmd, "#") {
			return
		}
		if m := cdPyRegex.FindStringSubmatch(cmd); len(m) == 5 {
			joinedScript := filepath.ToSlash(filepath.Join(m[1], m[3]))
			if dir == "" || fileExists(filepath.Join(dir, joinedScript)) {
				cmd = m[2] + " " + joinedScript + m[4]
			}
		}
		phase, command := classifyCommand(cmd)
		if phase == "" {
			return
		}
		if dir != "" && !isCommandApplicable(dir, command) {
			return
		}

		existing, exists := entries[phase]
		if !exists || currentPriority >= existing.priority {
			entries[phase] = entryWithScore{
				entry:    RunbookEntry{Phase: phase, Command: command, Source: "AGENTS.md"},
				priority: currentPriority,
			}
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "#") && !inCodeBlock {
			// Section header
			headerText := strings.ToLower(strings.TrimLeft(trimmed, "#"))
			if baseDir != "" {
				if strings.Contains(headerText, baseDir+"/") || strings.Contains(headerText, "`"+baseDir+"`") || strings.Contains(headerText, baseDir+" ") {
					currentPriority = 2 // target directory section
				} else if strings.Contains(headerText, "/") {
					// Header mentions another path (e.g. worker/, research/)
					currentPriority = 0 // sibling directory section
				} else {
					currentPriority = 1 // general header
				}
			}
			continue
		}

		if strings.HasPrefix(trimmed, "```") {
			if inCodeBlock {
				inCodeBlock = false
				for _, bl := range blockLines {
					processCommand(bl)
				}
				blockLines = nil
			} else {
				inCodeBlock = true
			}
			continue
		}
		if inCodeBlock {
			blockLines = append(blockLines, trimmed)
			continue
		}
		for _, snippet := range inlineSnippets(line) {
			processCommand(snippet)
		}
	}

	// Collect result in deterministic phase order
	phaseOrder := []string{"Test", "Build", "Gate"}
	var result []RunbookEntry
	for _, p := range phaseOrder {
		if e, ok := entries[p]; ok {
			result = append(result, e.entry)
		}
	}
	return result
}

// inlineSnippets returns the backtick-quoted snippets of a line whose leading
// connector does not negate them. "Do not run `pnpm test`; use `aubr
// test:unit`." yields only the second snippet.
func inlineSnippets(line string) []string {
	var out []string
	pos := 0
	for {
		open := strings.Index(line[pos:], "`")
		if open < 0 {
			break
		}
		open += pos
		closing := strings.Index(line[open+1:], "`")
		if closing < 0 {
			break
		}
		closing += open + 1
		if !negatedConnector(line[pos:open]) {
			out = append(out, strings.TrimSpace(line[open+1:closing]))
		}
		pos = closing + 1
	}
	return out
}

// negatedConnector reports whether the clause directly before a snippet
// forbids it. Earlier clauses belong to previous snippets, not this one.
func negatedConnector(connector string) bool {
	if idx := strings.LastIndexAny(connector, ",.;:\n"); idx >= 0 {
		connector = connector[idx+1:]
	}
	return negationRegex.MatchString(connector)
}

func classifyCommand(cmd string) (string, string) {
	switch {
	case testCmdRegex.MatchString(cmd):
		return "Test", testCmdRegex.FindString(cmd)
	case buildCmdRegex.MatchString(cmd):
		return "Build", buildCmdRegex.FindString(cmd)
	case gateCmdRegex.MatchString(cmd):
		return "Gate", gateCmdRegex.FindString(cmd)
	}
	return "", ""
}

// packageScripts reads the scripts block from package.json, or nil when the
// file is missing or unreadable. A runbook command whose script does not exist
// must never reach an agent.
func packageScripts(dir string) map[string]string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || len(pkg.Scripts) == 0 {
		return nil
	}
	return pkg.Scripts
}

// jsRunner picks the package runner: aube when its lock exists or package.json
// is the only manifest (the project standard), pnpm when only its lock exists.
func jsRunner(dir string) string {
	if fileExists(filepath.Join(dir, "aube-lock.yaml")) {
		return "aubr"
	}
	if fileExists(filepath.Join(dir, "pnpm-lock.yaml")) {
		return "pnpm"
	}
	if fileExists(filepath.Join(dir, "package.json")) {
		return "aubr"
	}
	return ""
}

// pickScript returns the first existing candidate, then the first script whose
// name starts with prefix. Empty when nothing matches.
func pickScript(scripts map[string]string, prefix string, candidates ...string) string {
	for _, c := range candidates {
		if _, ok := scripts[c]; ok {
			return c
		}
	}
	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			return name
		}
	}
	return ""
}

func detectTestCommand(dir string) string {
	if fileExists(filepath.Join(dir, "go.mod")) {
		return "go test -race ./..."
	}
	if scripts := packageScripts(dir); scripts != nil {
		if runner := jsRunner(dir); runner != "" {
			if script := pickScript(scripts, "test", "test", "test:unit"); script != "" {
				if script == "test" {
					return runner + " test"
				}
				return runner + " run " + script
			}
		}
	}
	if fileExists(filepath.Join(dir, "Cargo.toml")) {
		return "cargo test"
	}
	return ""
}

func detectBuildCommand(dir string) string {
	if fileExists(filepath.Join(dir, "build.sh")) {
		return "./build.sh"
	}
	if fileExists(filepath.Join(dir, "go.mod")) {
		return "go build ./..."
	}
	if scripts := packageScripts(dir); scripts != nil {
		if runner := jsRunner(dir); runner != "" {
			if script := pickScript(scripts, "build", "build"); script != "" {
				return runner + " " + script
			}
		}
	}
	if fileExists(filepath.Join(dir, "Cargo.toml")) {
		return "cargo build --release"
	}
	return ""
}

func detectGateCommand(dir string) string {
	if fileExists(filepath.Join(dir, ".git")) {
		return "sila gate"
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
