package dream

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// ExtractedKeepTokens holds the structured high-entropy technical facts mined from text.
type ExtractedKeepTokens struct {
	FilePaths   []string `json:"file_paths"`
	Symbols     []string `json:"symbols"`     // functions, types, struct names, methods
	ErrorCodes  []string `json:"error_codes"` // e.g. SQLITE_BUSY, ENOSPC, HTTP 429, exit 127
	URLs        []string `json:"urls"`
	Commands    []string `json:"commands"`    // cli invocations (e.g. go test, sila prime)
	Identifiers []string `json:"identifiers"` // IDs, hashes, env vars (SILA_*, etc.)
}

var (
	// URLs: standard web addresses.
	reURL = regexp.MustCompile(`https?://[^\s)\]>"']+`)

	// Commands: CLI invocations either on their own line, after a prompt symbol ($ or >), or in quotes/backticks.
	reCommandAny = regexp.MustCompile(`(?i)(?:^|['"` + "`" + `\s$>]|\b)((?:go\s+(?:test|run|build|vet|fmt|mod|install)|sila\s+[a-z0-9_.-]+|git\s+[a-z0-9_.-]+|pnpm\s+[a-z0-9_.-]+|npm\s+[a-z0-9_.-]+|npx\s+[a-z0-9_.-]+|cargo\s+[a-z0-9_.-]+|wrangler\s+[a-z0-9_.-]+|docker\s+[a-z0-9_.-]+|curl\s+[^\s'"` + "`" + `]+)[^'"` + "`" + `\n]*)`)

	// Error codes: exit codes, HTTP statuses, POSIX errors, SQLITE errors, error constants, panics.
	reErrorCode = regexp.MustCompile(`(?i)\b(?:exit(?:[\s_-]code|[\s_-]status)?\s*(?:=|:|\b)?\s*\d+|HTTP\s+\d{3}|status\s+\d{3}|SQLITE_[A-Z0-9_]+|E(?:NOSPC|NOENT|ADDRINUSE|ACCES|CONNREFUSED|EXIST|PERM|TIMEDOUT|BUSY|PIPE|FAULT|INVAL|NOTDIR|ISDIR|AGAIN|WOULDBLOCK)|[A-Z0-9_]+_(?:ERROR|ERR|FAIL|TIMEOUT|OVERFLOW|EXCEPTION)|ERR_[A-Z0-9_]+)\b`)
	rePanic     = regexp.MustCompile(`(?i)\bpanic:\s*([^\n.;]+)`)

	// File paths: Unix and Windows paths with directories or standard extensions.
	reUnixPath    = regexp.MustCompile(`\b(?:/[a-zA-Z0-9_.-]+)+|[a-zA-Z0-9_.-]+(?:/[a-zA-Z0-9_.-]+)+`)
	reWindowsPath = regexp.MustCompile(`\b(?:[a-zA-Z]:\\[a-zA-Z0-9_.-]+(?:\\[a-zA-Z0-9_.-]+)*|[a-zA-Z0-9_.-]+(?:\\[a-zA-Z0-9_.-]+)+)`)
	reFilename    = regexp.MustCompile(`\b[a-zA-Z0-9_.-]+\.(?:go|ts|js|jsx|tsx|json|md|sql|sh|yaml|yml|toml|html|css|rs|py|c|h|cpp)\b`)

	// Identifiers: Env vars (SILA_*, CGO_ENABLED), UUIDs, Prefixed IDs, Commit SHAs.
	reUUID       = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	rePrefixedID = regexp.MustCompile(`\b(?:fact|memo|hoff|task|proj|goal|case|sess)_[0-9a-zA-Z_-]+\b`)
	reEnvVar     = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{2,}\b`)
	reCommitSHA  = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

	// Symbols: backticked spans, package.Symbol, CamelCase/PascalCase, and snake_case identifiers.
	reBacktick  = regexp.MustCompile("`([a-zA-Z0-9_.*#/-]+)`")
	rePkgSymbol = regexp.MustCompile(`\b[a-zA-Z_][a-zA-Z0-9_]*\.[A-Z][a-zA-Z0-9_]*\b`)
	reCamelCase = regexp.MustCompile(`\b[A-Z][a-z0-9]+(?:[A-Z][a-zA-Z0-9]*)+\b`)
	reSnakeCase = regexp.MustCompile(`\b[a-z0-9]+(?:_[a-z0-9]+)+\b`)
)

// ExtractKeepTokens extracts durable technical keep-tokens from text using deterministic pure-Go regex miners.
func ExtractKeepTokens(text string) ExtractedKeepTokens {
	tokens := ExtractedKeepTokens{
		FilePaths:   make([]string, 0),
		Symbols:     make([]string, 0),
		ErrorCodes:  make([]string, 0),
		URLs:        make([]string, 0),
		Commands:    make([]string, 0),
		Identifiers: make([]string, 0),
	}

	if strings.TrimSpace(text) == "" {
		return tokens
	}

	// 1. Extract URLs
	urlSet := make(map[string]bool)
	for _, m := range reURL.FindAllString(text, -1) {
		clean := strings.TrimRight(m, ".,;:)\"']>")
		if _, err := url.ParseRequestURI(clean); err == nil && strings.HasPrefix(clean, "http") {
			tokens.URLs = append(tokens.URLs, clean)
			urlSet[clean] = true
		}
	}

	// 2. Extract Commands
	cmdSet := make(map[string]bool)
	for _, match := range reCommandAny.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			cmd := strings.TrimSpace(match[1])
			cmd = strings.Trim(cmd, "'\"`")
			cmd = strings.TrimRight(cmd, ",;:\n")
			if strings.HasSuffix(cmd, ".") && !strings.HasSuffix(cmd, "...") {
				cmd = strings.TrimSuffix(cmd, ".")
			}
			if cmd != "" && !cmdSet[cmd] {
				tokens.Commands = append(tokens.Commands, cmd)
				cmdSet[cmd] = true
			}
		}
	}

	// 3. Extract Error Codes
	errSet := make(map[string]bool)
	for _, m := range reErrorCode.FindAllString(text, -1) {
		clean := strings.TrimSpace(m)
		if clean != "" && !errSet[clean] {
			tokens.ErrorCodes = append(tokens.ErrorCodes, clean)
			errSet[clean] = true
		}
	}
	for _, match := range rePanic.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			p := "panic: " + strings.TrimSpace(match[1])
			if !errSet[p] {
				tokens.ErrorCodes = append(tokens.ErrorCodes, p)
				errSet[p] = true
			}
		}
	}

	// Prepare text with URLs stripped so URL paths aren't treated as file paths
	textWithoutURLs := reURL.ReplaceAllString(text, " ")

	// 4. Extract File Paths
	pathSet := make(map[string]bool)
	extractPaths := func(matches []string) {
		for _, m := range matches {
			clean := strings.TrimSpace(m)
			clean = strings.Trim(clean, "`'\"(),;:<>[]{}\t\r\n")
			clean = strings.TrimRight(clean, ".")
			if clean == "" || urlSet[clean] || cmdSet[clean] {
				continue
			}
			// Skip markdown formatting artifacts or pure numeric ratios
			if strings.Count(clean, "/") == 1 && (strings.HasPrefix(clean, "http") || strings.HasPrefix(clean, "0/") || strings.HasPrefix(clean, "1/")) {
				continue
			}
			if isLikelyFilePath(clean) && !pathSet[clean] {
				tokens.FilePaths = append(tokens.FilePaths, clean)
				pathSet[clean] = true
			}
		}
	}
	extractPaths(reUnixPath.FindAllString(textWithoutURLs, -1))
	extractPaths(reWindowsPath.FindAllString(textWithoutURLs, -1))
	extractPaths(reFilename.FindAllString(textWithoutURLs, -1))

	// Prune shorter redundant subpaths when a longer qualified path exists
	tokens.FilePaths = pruneRedundantPaths(tokens.FilePaths)
	pathSet = make(map[string]bool, len(tokens.FilePaths))
	for _, p := range tokens.FilePaths {
		pathSet[p] = true
	}

	// 5. Extract Identifiers FIRST (so specific IDs aren't swallowed as generic symbols)
	idSet := make(map[string]bool)
	var extractedUUIDs []string
	for _, id := range reUUID.FindAllString(textWithoutURLs, -1) {
		tokens.Identifiers = append(tokens.Identifiers, id)
		idSet[id] = true
		extractedUUIDs = append(extractedUUIDs, id)
	}
	for _, id := range rePrefixedID.FindAllString(textWithoutURLs, -1) {
		if !idSet[id] {
			tokens.Identifiers = append(tokens.Identifiers, id)
			idSet[id] = true
		}
	}
	commonWords := map[string]bool{
		"RUN": true, "FAIL": true, "PASS": true, "HTTP": true, "INFO": true,
		"WARN": true, "TRUE": true, "FALSE": true, "NULL": true, "NONE": true,
		"CASE": true, "JSON": true, "HEAD": true, "TEST": true,
	}
	for _, id := range reEnvVar.FindAllString(textWithoutURLs, -1) {
		if commonWords[id] || idSet[id] {
			continue
		}
		// Valid env var either has underscore (e.g. SILA_*, CGO_ENABLED) or is PATH
		if strings.Contains(id, "_") || id == "PATH" {
			tokens.Identifiers = append(tokens.Identifiers, id)
			idSet[id] = true
		}
	}
	for _, sha := range reCommitSHA.FindAllString(textWithoutURLs, -1) {
		if idSet[sha] {
			continue
		}
		// Check if this sha is a substring of an extracted UUID
		isUUIDSub := false
		for _, u := range extractedUUIDs {
			if strings.Contains(u, sha) {
				isUUIDSub = true
				break
			}
		}
		if !isUUIDSub {
			tokens.Identifiers = append(tokens.Identifiers, sha)
			idSet[sha] = true
		}
	}

	// 6. Extract Symbols
	symSet := make(map[string]bool)
	extractSymbols := func(items []string) {
		for _, sym := range items {
			clean := strings.Trim(sym, "`'\"(),;:<>[]{}")
			if clean == "" || pathSet[clean] || urlSet[clean] || cmdSet[clean] || errSet[clean] || idSet[clean] {
				continue
			}
			// Discard purely numeric or single char tokens
			if len(clean) <= 1 || isAllDigits(clean) {
				continue
			}
			if !symSet[clean] {
				tokens.Symbols = append(tokens.Symbols, clean)
				symSet[clean] = true
			}
		}
	}
	for _, m := range reBacktick.FindAllStringSubmatch(textWithoutURLs, -1) {
		if len(m) > 1 {
			val := m[1]
			if isLikelyFilePath(val) {
				if !pathSet[val] {
					tokens.FilePaths = append(tokens.FilePaths, val)
					pathSet[val] = true
				}
			} else if !cmdSet[val] && !errSet[val] && !idSet[val] {
				extractSymbols([]string{val})
			}
		}
	}
	extractSymbols(rePkgSymbol.FindAllString(textWithoutURLs, -1))
	extractSymbols(reCamelCase.FindAllString(textWithoutURLs, -1))
	extractSymbols(reSnakeCase.FindAllString(textWithoutURLs, -1))

	// Deterministic sorting and pruning
	tokens.FilePaths = dedupAndSort(tokens.FilePaths)
	tokens.Symbols = dedupAndSort(tokens.Symbols)
	tokens.ErrorCodes = dedupAndSort(tokens.ErrorCodes)
	tokens.URLs = dedupAndSort(tokens.URLs)
	tokens.Commands = dedupAndSort(tokens.Commands)
	tokens.Identifiers = dedupAndSort(tokens.Identifiers)

	return tokens
}

func pruneRedundantPaths(paths []string) []string {
	var kept []string
	for i, p1 := range paths {
		redundant := false
		for j, p2 := range paths {
			if i != j && strings.HasSuffix(p2, "/"+p1) {
				redundant = true
				break
			}
		}
		if !redundant {
			kept = append(kept, p1)
		}
	}
	return kept
}

func isLikelyFilePath(s string) bool {
	if strings.Contains(s, "/") || strings.Contains(s, "\\") {
		// Avoid URL schemes and simple division
		if strings.HasPrefix(s, "http:") || strings.HasPrefix(s, "https:") {
			return false
		}
		return true
	}
	// Has recognized file extension
	exts := []string{".go", ".ts", ".js", ".jsx", ".tsx", ".json", ".md", ".sql", ".sh", ".yaml", ".yml", ".toml", ".html", ".css", ".rs", ".py", ".c", ".h", ".cpp"}
	for _, ext := range exts {
		if strings.HasSuffix(strings.ToLower(s), ext) && len(s) > len(ext) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func dedupAndSort(slice []string) []string {
	if len(slice) == 0 {
		return slice
	}
	seen := make(map[string]bool, len(slice))
	out := make([]string, 0, len(slice))
	for _, s := range slice {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" && !seen[trimmed] {
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}
	sort.Strings(out)
	return out
}
