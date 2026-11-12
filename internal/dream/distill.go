package dream

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

// maxClaimLen is the noise gate: claims longer than this never reach `high`
// confidence (ascii-noise signal).
// maxClaimLen is the ascii-noise cap on confidence. It is virtually unreachable
// today (both Distill branches TrimClaim to ~240 first); kept as defense in
// depth against any future caller that passes untrimmed text to Confidence.
const maxClaimLen = 2000

// factClaimLen is the stored-claim length: distilled claims are trimmed to a
// crisp fact-like sentence around this bound.
const factClaimLen = 240

var pathTokenRE = regexp.MustCompile(`(?:[A-Za-z0-9._/\-]+/)+[A-Za-z0-9._\-]+`)

// PathRefs returns the file-like path references in text that resolve to a
// path under dir. A token is a candidate only if it (a) has ≥2 path segments
// AND (b) is a plausible file path (valid extension with letters, no domain/version noise)
// AND (c) either exists on disk or looks like a valid repo-relative path.
// Traversal outside dir is always rejected. Refs are returned relative to dir.
func PathRefs(text, dir string) []string {
	dir = filepath.Clean(dir)
	seen := map[string]bool{}
	var out []string
	tokens := pathTokenRE.FindAllString(text, -1)
	// fff tool invocations carry file hints that generic path scanning misses
	// when the argument has no directory component.
	tokens = append(tokens, loaders.ExtractFFFToolPaths(text)...)
	for _, tok := range tokens {
		tok = strings.TrimRight(tok, ".,;:!?…'\")]`")
		tok = strings.TrimLeft(tok, "'\"`([<")

		// P3b: Reject dynamic URL parameters and template routes (e.g. /menu/{categoryId}/{itemId})
		if strings.Contains(tok, "{") || strings.Contains(tok, "}") {
			continue
		}

		// P4: Reject tokens with display-truncated ellipsis (e.g. ".../docs/plan.md")
		if strings.Contains(tok, "...") || strings.Contains(tok, "…") {
			continue
		}

		// P4: Fix absolute path prefixes mangled with leading chars (e.g. n/workspace/... -> /workspace/...)
		if idx := strings.Index(tok, "/mnt/"); idx > 0 {
			tok = tok[idx:]
		} else if idx := strings.Index(tok, "/home/"); idx > 0 {
			tok = tok[idx:]
		} else if idx := strings.Index(tok, "/Users/"); idx > 0 {
			tok = tok[idx:]
		} else if dir != "" && strings.Contains(tok, dir) {
			if idx := strings.Index(tok, dir); idx >= 0 {
				tok = tok[idx:]
			}
		}

		// P3b: If token has no extension, check if appending .tsx, .ts, .jsx, or .js resolves to an existing file
		if !hasExtension(tok) && dir != "" {
			for _, ext := range []string{".tsx", ".ts", ".jsx", ".js", ".html", ".astro"} {
				if existsInDir(tok+ext, dir) {
					tok = tok + ext
					break
				}
			}
		}

		// If token is an absolute path
		if filepath.IsAbs(tok) {
			tok = filepath.Clean(tok)
			if dir != "" && (tok == dir || strings.HasPrefix(tok, dir+string(filepath.Separator))) {
				if rel, err := filepath.Rel(dir, tok); err == nil {
					tok = filepath.ToSlash(rel)
				} else {
					continue
				}
			} else {
				continue // outside dir
			}
		}

		if !isPlausibleFilePath(tok) {
			continue
		}

		// Try direct existence first
		if existsInDir(tok, dir) {
			resolved := inDir(tok, dir)
			if resolved != nil {
				if rel, err := filepath.Rel(dir, *resolved); err == nil {
					rel = filepath.ToSlash(rel)
					if !seen[rel] {
						seen[rel] = true
						out = append(out, rel)
						continue
					}
				}
			}
		}

		// P2: Context-aware subpath resolution (e.g. routes/_index.tsx disambiguated by claim text)
		if matched := resolveSubpath(tok, dir, text, out); matched != "" {
			matched = filepath.ToSlash(matched)
			if !seen[matched] {
				seen[matched] = true
				out = append(out, matched)
				continue
			}
		}

		// If it doesn't exist on disk, only keep it if it carries a real extension and is strictly under dir
		if hasExtension(tok) {
			resolved := inDir(tok, dir)
			if resolved != nil {
				if rel, err := filepath.Rel(dir, *resolved); err == nil {
					rel = filepath.ToSlash(rel)
					if !seen[rel] {
						seen[rel] = true
						out = append(out, rel)
					}
				}
			}
		}
	}
	return out
}

var commonDomainTLDs = map[string]bool{
	"app": true, "net": true, "com": true, "org": true, "io": true,
	"dev": true, "ai": true, "co": true, "gov": true, "edu": true,
	"xml": false, "ts": false, "go": false, "js": false, "py": false,
}

// isPlausibleFilePath checks if a candidate path is a real file path and not a version number,
// section reference, domain name, extension pattern, or joined filenames.
func isPlausibleFilePath(tok string) bool {
	segments := strings.Split(tok, "/")
	if len(segments) < 2 {
		return false
	}

	// Reject if starts with a leading dot extension like .test.ts/.tsx
	if strings.HasPrefix(segments[0], ".") && !strings.HasPrefix(segments[0], "./") && !isKnownDotDir(segments[0]) {
		return false
	}

	// Reject if any intermediate segment looks like a file name (e.g. AGENTS.md/workers.json) or domain (e.g. maktabeh.app)
	for i := 0; i < len(segments)-1; i++ {
		seg := segments[i]
		if isNumericOrVersion(seg) {
			return false // e.g. 4.107 in 4.107/4.121 or 5.1 in 5.1/5.2
		}
		if dotIdx := strings.LastIndex(seg, "."); dotIdx > 0 {
			ext := strings.ToLower(seg[dotIdx+1:])
			if commonDomainTLDs[ext] || isKnownFileExt(ext) {
				return false // domain or intermediate file like AGENTS.md/foo
			}
		}
	}

	last := strings.TrimRight(segments[len(segments)-1], ".,;:!?…")
	// Last segment must not be purely numeric (e.g. 4.121 in 4.107/4.121)
	if isNumericOrVersion(last) {
		return false
	}

	// If last segment starts with a dot extension (e.g. .tsx in .test.ts/.tsx)
	if strings.HasPrefix(last, ".") && len(last) <= 5 {
		return false
	}

	return true
}

func isKnownDotDir(seg string) bool {
	switch seg {
	case ".github", ".vscode", ".claude", ".cursor", ".git", ".private", ".ui-craft":
		return true
	default:
		return false
	}
}

func isKnownFileExt(ext string) bool {
	switch ext {
	case "md", "json", "jsonc", "ts", "tsx", "js", "jsx", "go", "rs", "py", "sql", "css", "html", "yaml", "yml", "toml", "sh":
		return true
	default:
		return false
	}
}

func isNumericOrVersion(s string) bool {
	if s == "" {
		return false
	}
	hasDigit := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			hasDigit = true
		} else if r != '.' && r != '-' && r != '_' && r != 'v' && r != 'V' {
			return false
		}
	}
	return hasDigit
}

var (
	projectFilesCache   = map[string][]string{}
	projectFilesCacheMu sync.RWMutex
)

// InvalidateProjectFileCache clears a project snapshot after ingest changes so
// grounding always observes the current filesystem rather than a process-old list.
func InvalidateProjectFileCache(dir string) {
	projectFilesCacheMu.Lock()
	defer projectFilesCacheMu.Unlock()
	if dir == "" {
		projectFilesCache = map[string][]string{}
		return
	}
	delete(projectFilesCache, dir)
}

func getProjectFileList(dir string) []string {
	projectFilesCacheMu.RLock()
	files, ok := projectFilesCache[dir]
	projectFilesCacheMu.RUnlock()
	if ok {
		return files
	}

	projectFilesCacheMu.Lock()
	defer projectFilesCacheMu.Unlock()
	if files, ok := projectFilesCache[dir]; ok {
		return files
	}

	var list []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				name := info.Name()
				if name == "node_modules" || name == ".git" || name == "dist" || name == ".turbo" || name == ".next" || name == "build" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if rel, err := filepath.Rel(dir, path); err == nil {
			list = append(list, filepath.ToSlash(rel))
		}
		return nil
	})

	projectFilesCache[dir] = list
	return list
}

// resolveSubpath searches for a unique or best context-matching file in dir ending with tok.
func resolveSubpath(tok, dir, contextText string, otherRefs []string) string {
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	cleanTok := filepath.ToSlash(filepath.Clean(tok))
	suffix := "/" + cleanTok
	segs := strings.Split(cleanTok, "/")
	lastSeg := segs[len(segs)-1]
	firstSeg := segs[0]

	var matches []string
	allFiles := getProjectFileList(dir)
	for _, rel := range allFiles {
		if rel == cleanTok || strings.HasSuffix("/"+rel, suffix) {
			matches = append(matches, rel)
		} else if len(segs) >= 2 && strings.HasSuffix("/"+rel, "/"+lastSeg) && strings.Contains(rel, "/"+firstSeg+"/") {
			// P3: Fragment matching e.g. delivery/_layout.drivers.tsx -> apps/delivery/app/routes/_layout.drivers.tsx
			matches = append(matches, rel)
		}
	}

	if len(matches) == 1 {
		return matches[0]
	}
	if len(matches) > 1 {
		// P2: Context-aware disambiguation
		contextLower := strings.ToLower(contextText)
		type scoredMatch struct {
			path  string
			score int
		}
		var scored []scoredMatch
		for _, m := range matches {
			s := 0
			parts := strings.Split(m, "/")
			if len(parts) >= 2 {
				appPrefix := parts[0] + "/" + parts[1] // e.g. apps/storefront or packages/shared
				if strings.Contains(contextLower, strings.ToLower(appPrefix)) {
					s += 10
				} else if strings.Contains(contextLower, strings.ToLower(parts[1])) {
					s += 5
				}
			}
			for _, other := range otherRefs {
				if len(parts) >= 2 && strings.HasPrefix(other, parts[0]+"/"+parts[1]) {
					s += 8
				}
			}
			scored = append(scored, scoredMatch{path: m, score: s})
		}
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].score > scored[j].score
		})
		if scored[0].score > 0 && (len(scored) == 1 || scored[0].score > scored[1].score) {
			return scored[0].path
		}
	}
	return ""
}

// hasExtension reports whether the final path segment carries a real file
// extension with at least one letter (rejecting pure numbers like .121 or .2).
func hasExtension(tok string) bool {
	segs := strings.Split(tok, "/")
	last := strings.TrimRight(segs[len(segs)-1], ".,;:!?…")
	i := strings.LastIndex(last, ".")
	if i < 0 || i+1 >= len(last) {
		return false
	}
	ext := last[i+1:]
	if len(ext) > 6 || len(ext) == 0 {
		return false
	}
	hasLetter := false
	for _, r := range ext {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
		} else if !(r >= '0' && r <= '9') {
			return false
		}
	}
	return hasLetter
}

func isAlphaNum(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return len(s) > 0
}

// isProcessMonologue reports whether a message is internal agent monologue, meta-commentary, or conversational chatter.
func isProcessMonologue(s string) bool {
	sLower := strings.ToLower(strings.TrimSpace(s))
	monologuePrefixes := []string{
		"find over the whole filesystem", "let me just check", "let me check",
		"let me acknowledge", "let me write the prompt", "let me spawn",
		"let me show you why", "i decide what to add", "i decide",
		"i'll use spawn", "i'll spawn", "i will spawn", "i'll implement",
		"now update §", "now i understand", "now i have the full",
		"i checked the logs", "spawning the worker",
		// P6: Third-person meta commentary and conversational reply openers
		"the main agent", "the agent is tasked", "the agent is currently",
		"the agent completed", "the agent performed", "the agent is",
		"was asked to review", "was asked to", "mimo-", "devin was asked",
		"honest answer:", "technically yes", "practically no",
		"following the section which requires", "the user said",
		"the user asked", "the user reported", "the user wants",
		"the user is continuing",
	}
	for _, p := range monologuePrefixes {
		if strings.HasPrefix(sLower, p) || strings.Contains(sLower, "\n"+p) {
			return true
		}
	}
	return false
}

// isMetaAuditNoise reports whether a message is self-referential audit chatter about sila itself.
func isMetaAuditNoise(s string) bool {
	sLower := strings.ToLower(s)
	metaKeywords := []string{
		"stale claims", "file_refs_exist", "sila dream", "sila sync", "sila prime",
		"sila v0.", "v0.4.", "maintainer claim", "the maintainer", "scorecard",
		"verification results", "cross-project contamination", "genuinely stale",
		"p5b", "p1-p6", "p7", "any-missing staleness", "majority grounding",
		"audit messages", "audit report", "549 stale claims", "the stale set",
	}
	hits := 0
	for _, kw := range metaKeywords {
		if strings.Contains(sLower, kw) {
			hits++
			if hits >= 2 {
				return true
			}
		}
	}
	return false
}

var (
	xmlTagsRE   = regexp.MustCompile(`(?i)</?(?:summary|details|overview)[^>]*>`)
	mdHeaderRE  = regexp.MustCompile(`(?i)(?:^|\s)#{1,6}\s+(?:overview|summary|details|key changes|notes)?\s*`)
	preamblesRE = regexp.MustCompile(`(?i)^(?:based on (?:my )?(?:thorough )?exploration of (?:the )?codebase[.:,]\s*|all done[.:]\s*here'?s the summary[.:]\s*|here'?s (?:a|the) summary(?: of changes)?[.:]\s*|summary of changes[.:]\s*)`)
)

// SanitizeClaim strips XML markup, markdown title headers, and agent conversational boilerplate.
func SanitizeClaim(s string) string {
	s = xmlTagsRE.ReplaceAllString(s, " ")
	s = mdHeaderRE.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	s = preamblesRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	return s
}

// TrimClaim cleans boilerplate, collapses whitespace, and cuts to a readable
// sentence ending around factClaimLen with an ellipsis.
func TrimClaim(s string) string {
	s = SanitizeClaim(s)
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= factClaimLen {
		return s
	}
	cut := string(runes[:factClaimLen])
	if i := strings.LastIndexAny(cut, ".!?;"); i >= factClaimLen/2 {
		cut = cut[:i+1]
	}
	return cut + "…"
}

// inDir resolves a possibly-relative path and returns its cleaned absolute
// path only if it is strictly inside dir. No existence check is performed.
func inDir(tok, dir string) *string {
	var abs string
	if filepath.IsAbs(tok) {
		abs = filepath.Clean(tok)
	} else {
		abs = filepath.Clean(filepath.Join(dir, tok))
	}
	if abs != dir && !strings.HasPrefix(abs, dir+string(filepath.Separator)) {
		return nil
	}
	return &abs
}

// existsInDir reports whether a relative ref is a real, non-directory file
// under dir.
func existsInDir(ref, dir string) bool {
	abs := inDir(ref, dir)
	if abs == nil {
		return false
	}
	fi, err := os.Stat(*abs)
	return err == nil && !fi.IsDir()
}

// VerifyRatio checks how many of refs physically exist on disk under dir.
func VerifyRatio(refs []string, dir string) (allExist bool, ratio float64, count int) {
	if len(refs) == 0 {
		return false, 0, 0
	}
	for _, r := range refs {
		if existsInDir(r, dir) {
			count++
		}
	}
	ratio = float64(count) / float64(len(refs))
	allExist = count == len(refs)
	return allExist, ratio, count
}

// Verify reports whether at least 50% of the referenced files exist under dir (P1 majority grounding).
func Verify(refs []string, dir string) bool {
	if len(refs) == 0 {
		return false
	}
	_, ratio, _ := VerifyRatio(refs, dir)
	return ratio >= 0.5
}

// Confidence derives the corroboration tier based on majority file grounding:
// - "ungrounded": no file references or <50% exist and only 1 tool.
// - "grounded": ≥50% file-grounded on disk, or supported across ≥2 tools.
// - "corroborated": all referenced files exist on disk AND (≥2 tools or ≥2 files).
func Confidence(refs []string, dir string, nTools int, claimLen int) string {
	if claimLen > maxClaimLen {
		return "grounded"
	}
	if len(refs) == 0 {
		if nTools >= 2 {
			return "grounded"
		}
		return "ungrounded"
	}
	allExist, ratio, _ := VerifyRatio(refs, dir)
	if ratio < 0.5 {
		if nTools >= 2 {
			return "grounded"
		}
		return "ungrounded"
	}
	if allExist && (nTools >= 2 || len(refs) >= 2) {
		return "corroborated"
	}
	return "grounded"
}

// isDirective extracts explicit architectural/decision directives from prose.
// Internal notes and fixes (e.g. "Note:", "Fix:", "Important:") are excluded to prevent chatter.
var directiveLabelRE = regexp.MustCompile(`^[-*#>\s]+`)

// directiveWords are the accepted final label token before ":" in a directive line.
var directiveWords = map[string]bool{
	"decision": true, "rule": true, "architecture": true,
	"arch": true, "policy": true, "guideline": true, "guidance": true,
	"constraint": true, "invariant": true, "tradeoff": true, "memo": true,
	"convention": true, "requirement": true, "preference": true, "directive": true,
	"instruction": true, "standard": true,
}

var userDirectivePrefixes = []string{
	"never ", "always ", "do not ", "don't ", "must not ", "must always ",
	"stop ", "avoid ", "do never ", "please do not ", "please don't ",
}

// isQuoted reports whether the text arrives as a quotation (block-comment
// style or blockquote marker) rather than a direct instruction. Quotation
// context is preserved on inferred directive candidates so reviewers can see
// the text was observed, not authorized.
func isQuoted(text string) bool {
	for l := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(strings.TrimLeft(l, "-*+ \t"))
		if strings.HasPrefix(t, ">") || strings.HasPrefix(t, `"`) || strings.HasPrefix(t, "“") {
			return true
		}
	}
	return false
}

func isDirective(text string) (string, bool) {
	for l := range strings.SplitSeq(text, "\n") {
		l = strings.TrimSpace(l)
		if len(l) < 20 {
			continue
		}
		l = directiveLabelRE.ReplaceAllString(l, "")
		l = strings.TrimSpace(l)
		for _, m := range []string{"**", "__", "`"} {
			l = strings.TrimPrefix(l, m)
			l = strings.TrimSuffix(l, m)
		}
		l = strings.TrimLeft(l, "-*+ \t")
		l = strings.TrimSpace(l)

		// 1. Colon-delimited labels: e.g. "Rule: Always local commit your work"
		idx := strings.IndexByte(l, ':')
		if idx >= 0 {
			label := strings.ToLower(strings.TrimSpace(l[:idx]))
			toks := strings.Fields(label)
			if len(toks) > 0 && directiveWords[toks[len(toks)-1]] {
				return l, true
			}
		}

		// 2. Strong imperative negative/positive directives: e.g. "Never write into another tool's database"
		low := strings.ToLower(l)
		for _, pfx := range userDirectivePrefixes {
			if strings.HasPrefix(low, pfx) {
				if len(l) >= 25 && len(l) <= 300 && !strings.HasPrefix(l, "//") && !strings.HasPrefix(l, "/*") {
					return l, true
				}
			}
		}
	}
	return "", false
}

// isAlienToProject checks if ungrounded file refs use extensions or root directories that do not exist anywhere in the project.
func isAlienToProject(refs []string, dir string) bool {
	if dir == "" || len(refs) == 0 {
		return false
	}
	allFiles := getProjectFileList(dir)
	if len(allFiles) == 0 {
		return false
	}

	ignorePatterns := getSilaIgnorePatterns(dir)

	// Build set of all extensions, directory paths, and packages present in the repo
	repoExts := map[string]bool{}
	repoDirs := map[string]bool{}
	for _, f := range allFiles {
		if ext := filepath.Ext(f); len(ext) > 1 {
			repoExts[strings.ToLower(ext[1:])] = true
		}
		parts := strings.Split(f, "/")
		for i := 1; i <= len(parts); i++ {
			repoDirs[strings.Join(parts[:i], "/")] = true
		}
	}

	for _, r := range refs {
		rSlash := filepath.ToSlash(r)
		for _, pat := range ignorePatterns {
			if matched, _ := filepath.Match(pat, rSlash); matched {
				return true
			}
			if strings.HasPrefix(rSlash, strings.TrimSuffix(pat, "*")) {
				return true
			}
		}

		if ext := filepath.Ext(rSlash); len(ext) > 1 {
			extClean := strings.ToLower(ext[1:])
			// If file extension (e.g. .astro) is completely absent from this repository
			if extClean == "astro" && !repoExts["astro"] {
				return true
			}
		}

		parts := strings.Split(rSlash, "/")
		if len(parts) >= 2 && (parts[0] == "apps" || parts[0] == "packages") {
			pkgPath := parts[0] + "/" + parts[1]
			if !repoDirs[pkgPath] {
				return true // e.g. apps/dashboard or apps/profiles in example-app
			}
		}

		// Deep subpath check: does the immediate parent directory exist?
		// e.g. packages/shared/src/widgets in example-app
		dirOfRef := filepath.ToSlash(filepath.Dir(rSlash))
		if dirOfRef != "." && !repoDirs[dirOfRef] {
			partsDir := strings.Split(dirOfRef, "/")
			if len(partsDir) >= 3 {
				p3 := strings.Join(partsDir[:3], "/") // e.g. packages/shared/src
				if repoDirs[p3] && !repoDirs[dirOfRef] {
					return true
				}
			}
		}
	}
	return false
}

func getSilaIgnorePatterns(dir string) []string {
	for _, name := range []string{".silaignore", ".sila-ignore"} {
		p := filepath.Join(dir, name)
		if data, err := os.ReadFile(p); err == nil {
			var lines []string
			for l := range strings.SplitSeq(string(data), "\n") {
				l = strings.TrimSpace(l)
				if l != "" && !strings.HasPrefix(l, "#") {
					lines = append(lines, l)
				}
			}
			return lines
		}
	}
	return nil
}

// hasAnyRootDirectory reports whether at least one path candidate matches an existing top-level folder in dir.
func hasAnyRootDirectory(refs []string, dir string) bool {
	if dir == "" {
		return true
	}
	for _, r := range refs {
		parts := strings.Split(filepath.ToSlash(r), "/")
		if len(parts) > 0 {
			topDir := filepath.Join(dir, parts[0])
			if fi, err := os.Stat(topDir); err == nil && fi.IsDir() {
				return true
			}
		}
	}
	return false
}

// Distill turns one message into a durable claim or architectural invariant.
// It matches file refs against the project dir, and also extracts explicit
// conceptual/architectural directives. Messages with neither are dropped as noise.
func Distill(projectID, dir string, claim, sessionRef string, tools []string, firstSeen string) *store.Fact {
	// P7: Reject self-referential meta-audit chatter
	if isMetaAuditNoise(claim) {
		return nil
	}

	refs := PathRefs(claim, dir)

	// P6: Filter internal monologue & meta-commentary unless backed by verified files or directives
	if isProcessMonologue(claim) {
		if _, ok := isDirective(claim); !ok && (len(refs) == 0 || !Verify(refs, dir)) {
			return nil
		}
	}

	if len(refs) > 0 {
		_, ratio, count := VerifyRatio(refs, dir)

		// P5b: Filter cross-project contamination (alien framework, alien directories, or ungrounded alien packages)
		if isAlienToProject(refs, dir) || (!hasAnyRootDirectory(refs, dir) && count == 0) {
			if _, ok := isDirective(claim); !ok {
				return nil // Drop mis-attributed cross-project claim
			}
		}

		claim = TrimClaim(claim)
		f := Candidate(projectID, claim, sessionRef, tools, firstSeen)
		f.RepoFiles = refs
		f.FileRefsExist = ratio >= 0.5
		f.Verified = f.FileRefsExist
		if _, ok := isDirective(claim); ok {
			// Authority boundary: a directive observed in a transcript is an
			// attributed candidate, never an authorized rule.
			f.Kind = "invariant"
			f.Corroboration = "candidate"
			f.Confidence = "candidate"
			f.ReviewStatus = store.ReviewCandidate
			f.Quoted = isQuoted(claim)
		} else {
			// Ordinary claims are not authority-gated; they qualify only via
			// corroboration + distillation + on-disk grounding.
			f.Kind = "code_claim"
			f.Corroboration = Confidence(refs, dir, len(tools), len(claim))
			f.Confidence = f.Corroboration
			f.ReviewStatus = store.ReviewApproved
		}
		f.RefsCheckedAt = firstSeen
		f.DistillStatus = "pending"
		return f
	}

	// Fallback: check for explicit architectural decision or rule directive.
	// The extracted text stays an attributed candidate: without an explicit
	// memo it is an observation, and quoting context is preserved for review.
	if dirLine, ok := isDirective(claim); ok {
		trimmed := TrimClaim(dirLine)
		f := Candidate(projectID, trimmed, sessionRef, tools, firstSeen)
		f.Kind = "invariant"
		f.RepoFiles = []string{}
		f.FileRefsExist = false
		f.Verified = false
		f.Corroboration = "candidate"
		f.Confidence = "candidate"
		f.RefsCheckedAt = firstSeen
		f.DistillStatus = "pending"
		f.ReviewStatus = store.ReviewCandidate
		f.Quoted = isQuoted(claim)
		return f
	}

	return nil
}

// ConsolidateFacts merges facts referencing shared files or shared directives
// across tools into single consolidated claims, promoting their corroboration and tracking all contributing tools.
func ConsolidateFacts(facts []store.Fact) []store.Fact {
	n := len(facts)
	if n <= 1 {
		return facts
	}

	// Precompute each fact's claim token map once, in parallel, so the all-pairs
	// similarity never re-splits a claim string.
	tokens := make([]map[string]int, n)
	var twg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := range facts {
		twg.Add(1)
		go func(i int) {
			defer twg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			tokens[i] = tokenize(facts[i].Claim)
		}(i)
	}
	twg.Wait()

	// A pair (i, j) can only corroborate when they share a repo file (which also
	// covers the equal-file set) or both have zero repo files. Bucket file-bearing
	// facts into connected components by shared file, so candidates are confined
	// to one component; zero-file facts form their own all-pairs group. Candidates
	// are emitted per fact in ascending j > i order, preserving the original
	// comparison order and therefore the exact same results.
	fileBuckets := map[string][]int{} // repo file -> fact indices containing it
	hasFiles := make([]bool, n)
	for i := range facts {
		if len(facts[i].RepoFiles) == 0 {
			continue
		}
		hasFiles[i] = true
		for _, f := range facts[i].RepoFiles {
			fileBuckets[f] = append(fileBuckets[f], i)
		}
	}
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for _, list := range fileBuckets {
		for k := 1; k < len(list); k++ {
			union(list[0], list[k])
		}
	}
	comps := map[int][]int{}
	var zeroIdx []int
	for i := range facts {
		if !hasFiles[i] {
			zeroIdx = append(zeroIdx, i)
			continue
		}
		r := find(i)
		comps[r] = append(comps[r], i)
	}
	candList := make([][]int, n)
	for _, comp := range comps {
		sort.Ints(comp)
		for idx, i := range comp {
			candList[i] = comp[idx+1:]
		}
	}
	for idx, i := range zeroIdx {
		candList[i] = zeroIdx[idx+1:]
	}

	var out []store.Fact
	merged := make([]bool, n)
	for i := range n {
		if merged[i] {
			continue
		}
		base := facts[i]
		baseID := base.ID
		// baseTokens tracks tokenize(base.Claim) as base.Claim is replaced below,
		// so the similarity stays in lockstep with the live base claim.
		baseTokens := tokens[i]
		for _, j := range candList[i] {
			if merged[j] {
				continue
			}
			cand := facts[j]
			sameFiles := sameRepoFiles(base.RepoFiles, cand.RepoFiles)
			fileOverlap := sharesRepoFiles(base.RepoFiles, cand.RepoFiles)
			sim := diceFromTokens(baseTokens, tokens[j])

			corroborates := false
			if sameFiles && (base.Claim == cand.Claim || sim >= 0.10) {
				corroborates = true
			} else if fileOverlap && (base.Claim == cand.Claim || sim >= 0.45) {
				corroborates = true
			} else if len(base.RepoFiles) == 0 && len(cand.RepoFiles) == 0 && (base.Claim == cand.Claim || sim >= 0.65) {
				corroborates = true
			}

			if corroborates {
				base.Tools = unionTools(base.Tools, cand.Tools)
				// Constrain repo_files to the kept claim to avoid file-set conflation
				if len(cand.Claim) > len(base.Claim) && len(cand.Claim) <= maxClaimLen {
					base.Claim = cand.Claim
					baseTokens = tokens[j]
					base.RepoFiles = cand.RepoFiles
					base.FileRefsExist = cand.FileRefsExist
					base.Verified = cand.Verified
					base.FirstSeen = cand.FirstSeen
					// The richer claim belongs to another fact: keep the old block
					// intact and record which row this consolidated fact replaces.
					if baseID != "" && baseID != FactID(base.ProjectID, cand.Claim) {
						base.SupersedesID = baseID
					}
				}
				if (base.FileRefsExist || base.Verified) && (len(base.Tools) >= 2 || len(base.RepoFiles) >= 2) {
					base.Confidence = "corroborated"
					base.Corroboration = "corroborated"
				} else if len(base.Tools) >= 2 && (base.Confidence == "ungrounded" || base.Confidence == "low") {
					base.Confidence = "grounded"
					base.Corroboration = "grounded"
				}
				merged[j] = true
			}
		}
		// Recompute the id from (project, claim): base.Claim may have been
		// overwritten with a richer candidate's text above, so the id must stay
		// a deterministic function of the final claim — otherwise pruning could
		// delete a row (and its completed distillation) when cluster order shifts.
		base.ID = FactID(base.ProjectID, base.Claim)
		out = append(out, base)
	}
	return out
}

// diceFromTokens is Similarity precomputed over already-tokenized maps; two text
// inputs that tokenize to ta/tb score identically to Similarity(a, b).
func diceFromTokens(ta, tb map[string]int) float64 {
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for w, n := range ta {
		if m, ok := tb[w]; ok {
			if n < m {
				inter += n
			} else {
				inter += m
			}
		}
	}
	return 2 * float64(inter) / float64(len(ta)+len(tb))
}

func sameRepoFiles(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, f := range a {
		set[f] = true
	}
	for _, f := range b {
		if !set[f] {
			return false
		}
	}
	return true
}

func sharesRepoFiles(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, f := range a {
		set[f] = true
	}
	for _, f := range b {
		if set[f] {
			return true
		}
	}
	return false
}

func unionRepoFiles(a, b []string) []string {
	seen := make(map[string]bool)
	var res []string
	for _, f := range a {
		if !seen[f] {
			seen[f] = true
			res = append(res, f)
		}
	}
	for _, f := range b {
		if !seen[f] {
			seen[f] = true
			res = append(res, f)
		}
	}
	return res
}

func unionTools(a, b []string) []string {
	seen := make(map[string]bool)
	var res []string
	for _, t := range a {
		if !seen[t] {
			seen[t] = true
			res = append(res, t)
		}
	}
	for _, t := range b {
		if !seen[t] {
			seen[t] = true
			res = append(res, t)
		}
	}
	return res
}

// FactID is deterministic over (project_id, claim).
func FactID(projectID, claim string) string {
	return store.FactID(projectID, claim)
}

// Candidate builds a fact for one message whose refs exist. No match (empty
// refs) yields nil — the message carries no grounded knowledge.
func Candidate(projectID, claim, sessionRef string, tools []string, firstSeen string) *store.Fact {
	if claim == "" {
		return nil
	}
	return &store.Fact{
		ID:            FactID(projectID, claim),
		ProjectID:     projectID,
		Claim:         claim,
		SessionRef:    sessionRef,
		Tools:         tools,
		FirstSeen:     firstSeen,
		PromotedAt:    firstSeen,
		RefsCheckedAt: firstSeen,
		DistillStatus: "pending",
	}
}

// DistillRunStats tracks the outcome of an LLM distillation pass.
type DistillRunStats struct {
	Total     int `json:"total"`
	Distilled int `json:"distilled"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
}

// RunLLMDistillation iterates over claims in the store and uses an LLM to distill atomic facts.
func RunLLMDistillation(st *store.Store, llm LLM, projectID string, retryFailed bool, batchSize int) (DistillRunStats, error) {
	var stats DistillRunStats
	var facts []store.Fact
	var err error
	if projectID != "" {
		facts, err = st.FactsByProject(projectID)
	} else {
		facts, err = st.FactsAll()
	}
	if err != nil {
		return stats, err
	}

	for _, f := range facts {
		if f.DistillStatus == "distilled" || f.DistillStatus == "skipped" {
			continue
		}
		if f.DistillStatus == "failed" && !retryFailed {
			continue
		}

		stats.Total++
		req := DistillRequest{
			ClaimID:    f.ID,
			ProjectID:  f.ProjectID,
			RawMessage: f.Claim,
			RepoFiles:  f.RepoFiles,
			Tools:      f.Tools,
			SessionRef: f.SessionRef,
		}

		res, err := llm.Distill(req)
		if err != nil {
			stats.Failed++
			f.DistillStatus = "failed"
			_ = st.UpsertFact(f)
			continue
		}

		if res.Status == "distilled" && res.Fact != "" {
			f.Distilled = res.Fact
			f.DistillStatus = "distilled"
			stats.Distilled++
		} else if res.Status == "skipped" || res.Fact == "" {
			f.Distilled = ""
			f.DistillStatus = "skipped"
			stats.Skipped++
		} else {
			f.DistillStatus = "failed"
			stats.Failed++
		}

		if err := st.UpsertFact(f); err != nil {
			return stats, err
		}
	}
	return stats, nil
}
