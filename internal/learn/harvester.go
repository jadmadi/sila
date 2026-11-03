package learn

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/gateway"
	"github.com/jadmadi/sila/internal/store"
)

// HarvesterOptions configures the cross-project learnings extraction engine.
type HarvesterOptions struct {
	ProjectID     string // Restrict to a single project (empty = all registered projects)
	Domain        string // Restrict to a specific domain (empty = all domains)
	SearchQuery   string // Restrict to learnings matching search query (empty = all)
	IncludeMemos  bool   // If true, also extract operational directives from memos/decisions
	RedactSecrets bool   // If true, scrub credentials and sensitive tokens (default: true)
	ScanAllStores bool   // If true, discover and harvest all project databases in ~/.local/share/sila (default: true)
}

// DefaultHarvesterOptions returns safe, production-grade defaults.
func DefaultHarvesterOptions() HarvesterOptions {
	return HarvesterOptions{
		IncludeMemos:  true,
		RedactSecrets: true,
		ScanAllStores: true,
	}
}

// Domain keyword patterns for classification.
var (
	reGo       = regexp.MustCompile(`(?i)\b(golang|go\s+1\.\d+|goroutine|gofmt|race\s+detector|cgo|cgo_enabled|build\.sh|go\.mod|modernc\.org/sqlite|nil\s+pointer|sync\.mutex|channel)\b`)
	reTS       = regexp.MustCompile(`(?i)\b(typescript|tsc|package\.json|pnpm|npm|vitest|eslint|tsconfig|esm|node_modules|vite|webpack|tailwind)\b`)
	reCF       = regexp.MustCompile(`(?i)\b(cloudflare|workers?|wrangler|miniflare|edge\s+mcp|isolated\s+storage|vitest-pool-workers|env\.db|env\.kv)\b`)
	reD1       = regexp.MustCompile(`(?i)\b(d1|sqlite|drizzle|sql|migration|table|fts5|foreign\s+key|database\s+page|wal\s+mode|truncate|unisolated\s+d1)\b`)
	reDevOps   = regexp.MustCompile(`(?i)\b(systemd|linux|subshell|subshell\s+mount|path\s+resilience|file\s+descriptor|exec\.command|daemon|pid|tmpfs|proc)\b`)
	reCICD     = regexp.MustCompile(`(?i)\b(ci/cd|github\s+actions?|pre-commit|pre-push|git\s+worktree|quality\s+gate|slsa|reproducible|atomic\s+commit|two-stage)\b`)
	reSecurity = regexp.MustCompile(`(?i)\b(security|token|secret|credential|redact|covenant|data\s+sovereignty|sanitiz|synthetic\s+secret|private\s+key|bearer|pat)\b`)
	reTesting  = regexp.MustCompile(`(?i)\b(testing|test\s+fixture|mock|assert|flakiness|flaky|beforeeach|aftereach|storage\s+sandbox|table-driven|go\s+test|testify)\b`)
	reA11y     = regexp.MustCompile(`(?i)\b(a11y|accessibility|wcag|aria|screen\s+reader|contrast|keyboard\s+trap|focus\s+state|truecolor)\b`)
)

// ClassifyDomain assigns an operational text string or tag slice to a canonical domain.
func ClassifyDomain(text string, tags []string) string {
	combined := text + " " + strings.Join(tags, " ")
	lower := strings.ToLower(combined)

	// Check domain matches with priority weighting
	switch {
	case reSecurity.MatchString(lower) && (strings.Contains(lower, "secret") || strings.Contains(lower, "redact") || strings.Contains(lower, "credential") || strings.Contains(lower, "token")):
		return DomainSecurity
	case reGo.MatchString(lower) && (strings.Contains(lower, "goroutine") || strings.Contains(lower, "gofmt") || strings.Contains(lower, "golang") || strings.Contains(lower, "cgo") || strings.Contains(lower, "go.mod") || strings.Contains(lower, "race")):
		return DomainGo
	case reCF.MatchString(lower) && (strings.Contains(lower, "worker") || strings.Contains(lower, "wrangler") || strings.Contains(lower, "miniflare")):
		return DomainCloudflare
	case reD1.MatchString(lower) && (strings.Contains(lower, "d1") || strings.Contains(lower, "sqlite") || strings.Contains(lower, "drizzle")):
		return DomainD1
	case reTesting.MatchString(lower) && (strings.Contains(lower, "flaky") || strings.Contains(lower, "fixture") || strings.Contains(lower, "mock") || strings.Contains(lower, "assert")):
		return DomainTesting
	case reGo.MatchString(lower):
		return DomainGo
	case reTS.MatchString(lower):
		return DomainTypeScript
	case reCICD.MatchString(lower):
		return DomainCICD
	case reDevOps.MatchString(lower):
		return DomainDevOps
	case reTesting.MatchString(lower):
		return DomainTesting
	case reA11y.MatchString(lower):
		return DomainA11y
	default:
		return DomainGeneral
	}
}

// BuiltinFoundationalLearnings supplies bedrock architectural invariants
// proven across the Sila multi-repo ecosystem.
func BuiltinFoundationalLearnings() []OperationalLearning {
	return []OperationalLearning{
		{
			ID:            "op-foundational-security-01",
			ProjectID:     "sila",
			Domain:        DomainSecurity,
			TechStack:     "git, pre-commit, regex-sentry",
			Title:         "Synthetic Secret Sanitization in Test Fixtures",
			Symptom:       "Local git commit blocked by pre-commit sentry scanning cached diffs for credentials.",
			RootCause:     "Test fixtures for secret redaction included contiguous mock private keys or tokens that triggered diff regex alarms.",
			PivotalFix:    "Fragment mock credentials into split string concatenations (e.g. `\"-----\" + \"BEGIN RSA \" + \"PRIVATE KEY-----\"`).",
			GuardrailRule: "Never author synthetic credentials as single contiguous strings in test fixtures; always split or concatenate.",
			Tags:          []string{"security", "testing", "pre-commit", "sentry", "credentials"},
			Confidence:    "verified",
			CreatedAt:     time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
		},
		{
			ID:            "op-foundational-testing-01",
			ProjectID:     "sila",
			Domain:        DomainTesting,
			TechStack:     "cloudflare-workers, vitest, d1",
			Title:         "Cloudflare Vitest Storage Sandbox Isolation",
			Symptom:       "Intermittent test failures and order-dependent state leakage in @cloudflare/vitest-pool-workers.",
			RootCause:     "Tests mutating D1 or KV storage without beforeEach hooks leaked dirty rows into subsequent test cases.",
			PivotalFix:    "Enforce per-test storage reset via beforeEach(async () => { await env.DB.prepare('DELETE FROM ...').run(); }) and schema initialization in beforeAll.",
			GuardrailRule: "All mutating edge tests must define a beforeEach cleanup hook or run in isolated storage sandboxes.",
			Tags:          []string{"cloudflare", "testing", "d1", "miniflare", "storage-sandbox"},
			Confidence:    "verified",
			CreatedAt:     time.Date(2026, 9, 23, 14, 0, 0, 0, time.UTC),
		},
		{
			ID:            "op-foundational-golang-01",
			ProjectID:     "sila",
			Domain:        DomainGo,
			TechStack:     "go1.27, gofmt, gate",
			Title:         "Pre-Test gofmt Formatting Discipline",
			Symptom:       "`go test ./...` fails with exit code 1 on TestGateFormatClean after editing files.",
			RootCause:     "Go tests enforcing repository format hygiene flag newly modified or generated files before formatting.",
			PivotalFix:    "Run `gofmt -w cmd/ internal/` immediately after creating or editing Go files before invoking test runners or gates.",
			GuardrailRule: "Always format Go source code with gofmt -w prior to executing unit tests or quality gates.",
			Tags:          []string{"golang", "gofmt", "quality-gate", "testing"},
			Confidence:    "verified",
			CreatedAt:     time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC),
		},
		{
			ID:            "op-foundational-cicd-01",
			ProjectID:     "sila",
			Domain:        DomainCICD,
			TechStack:     "git, roadmap, build.sh",
			Title:         "Two-Stage Atomic Commit Protocol for Goal Registries",
			Symptom:       "Goal registry `goals/README.md` committed with outdated commit SHA or unverified binaries.",
			RootCause:     "Staging roadmap registry documents simultaneously with code changes before the implementation commit SHA exists.",
			PivotalFix:    "Automate two-stage protocol: commit code first, rebuild binary, verify sha, then commit updated goals/README.md in a distinct documentation commit.",
			GuardrailRule: "Never combine roadmap registry status files into implementation commits; use `sila goals commit <slug>`.",
			Tags:          []string{"git", "roadmap", "atomic-commit", "ci/cd"},
			Confidence:    "verified",
			CreatedAt:     time.Date(2026, 9, 23, 16, 0, 0, 0, time.UTC),
		},
		{
			ID:            "op-foundational-d1-01",
			ProjectID:     "sila",
			Domain:        DomainD1,
			TechStack:     "sqlite, modernc, wal",
			Title:         "SQLite Hash Chain Mutex Serialization",
			Symptom:       "Split hash ledger branches and broken previous-hash pointers under concurrent fact appends.",
			RootCause:     "Concurrent goroutines querying the tip hash simultaneously before inserting generated colliding previous-hash links.",
			PivotalFix:    "Wrap ledger appends with an internal store mutex (factMu) ensuring strictly sequential hash chaining.",
			GuardrailRule: "Hash-chained append-only ledgers in SQLite must coordinate write tip acquisition under a synchronized lock.",
			Tags:          []string{"sqlite", "d1", "concurrency", "ledger", "hash-chain"},
			Confidence:    "verified",
			CreatedAt:     time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC),
		},
	}
}

// DiscoverProjectStores finds all SQLite databases in standard Sila store locations.
func DiscoverProjectStores(activeDBPath string) []string {
	var dbPaths []string
	seen := make(map[string]bool)

	addPath := func(p string) {
		clean := filepath.Clean(p)
		if !seen[clean] {
			if fi, err := os.Stat(clean); err == nil && !fi.IsDir() && fi.Size() > 0 {
				dbPaths = append(dbPaths, clean)
				seen[clean] = true
			}
		}
	}

	if activeDBPath != "" {
		addPath(activeDBPath)
	}

	// In isolated test environments with SILA_DB set, never scan host databases
	if custom := os.Getenv("SILA_DB"); custom != "" {
		addPath(custom)
		return dbPaths
	}
	home, err := os.UserHomeDir()
	if err == nil {
		shareDir := filepath.Join(home, ".local", "share", "sila")
		if entries, err := os.ReadDir(shareDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && (strings.HasSuffix(e.Name(), ".db") || strings.HasSuffix(e.Name(), ".sqlite")) {
					addPath(filepath.Join(shareDir, e.Name()))
				}
			}
		}
	}

	// 2. XDG_DATA_HOME/sila/
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		xdgDir := filepath.Join(xdg, "sila")
		if entries, err := os.ReadDir(xdgDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && (strings.HasSuffix(e.Name(), ".db") || strings.HasSuffix(e.Name(), ".sqlite")) {
					addPath(filepath.Join(xdgDir, e.Name()))
				}
			}
		}
	}

	return dbPaths
}

// Harvest extracts, normalizes, and categorizes operational learnings from SQLite stores.
func Harvest(st *store.Store, opts HarvesterOptions) ([]OperationalLearning, error) {
	var results []OperationalLearning
	seenIDs := make(map[string]bool)

	// Step 1: Seed with foundational learnings
	for _, bl := range BuiltinFoundationalLearnings() {
		if opts.ProjectID != "" && bl.ProjectID != opts.ProjectID && bl.ProjectID != "sila" && bl.ProjectID != "universal" {
			continue
		}
		if opts.Domain != "" && bl.Domain != opts.Domain {
			continue
		}
		bl.Kind = KindLesson
		results = append(results, bl)
		seenIDs[bl.ID] = true
	}

	// Step 2: Assemble stores to scan
	var storesToScan []*store.Store
	var openedStores []*store.Store

	if st != nil && st.DB != nil {
		storesToScan = append(storesToScan, st)
	}

	if opts.ScanAllStores {
		activePath := ""
		if st != nil {
			activePath = st.Path
		}
		allPaths := DiscoverProjectStores(activePath)
		for _, p := range allPaths {
			if st != nil && filepath.Clean(st.Path) == filepath.Clean(p) {
				continue
			}
			if s, err := store.OpenRO(p); err == nil {
				storesToScan = append(storesToScan, s)
				openedStores = append(openedStores, s)
			}
		}
	}
	defer func() {
		for _, s := range openedStores {
			s.Close()
		}
	}()

	// Step 3: Query stored facts from all discovered stores
	for _, s := range storesToScan {
		harvestFromStore(s, opts, seenIDs, &results)
	}

	// Step 3.5: Deduplicate by exact symptom and cause hash
	results = DeduplicateBySymptomCauseHash(results)

	// Step 4: Pure-Go Deduplication across all harvested repositories
	results = Deduplicate(results)

	// Step 5: Sort results: verified first, then newest first
	sort.Slice(results, func(i, j int) bool {
		if results[i].Confidence == "verified" && results[j].Confidence != "verified" {
			return true
		}
		if results[i].Confidence != "verified" && results[j].Confidence == "verified" {
			return false
		}
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	if opts.SearchQuery != "" {
		results = FilterLearnings(results, opts.SearchQuery)
	}

	return results, nil
}

// SymptomCauseHash computes a deterministic SHA-256 hash for exact symptom + cause deduplication.
func SymptomCauseHash(symptom, rootCause string) string {
	normSym := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(symptom))), " ")
	normCause := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(rootCause))), " ")
	h := sha256.Sum256([]byte(normSym + "::" + normCause))
	return hex.EncodeToString(h[:])
}

// learningDedupKey selects the stable identity fields for a record kind. Cases
// and directives carry no symptom/cause, so hashing those empty fields would
// merge every non-lesson record into one.
func learningDedupKey(l OperationalLearning) string {
	switch l.EffectiveKind() {
	case KindCase:
		return string(KindCase) + "::" + SymptomCauseHash(l.Intent, l.Approach+"::"+l.Invariant)
	case KindMemo, KindDecision:
		return string(l.EffectiveKind()) + "::" + SymptomCauseHash(l.DirectiveText(), "")
	default:
		return SymptomCauseHash(l.Symptom, l.RootCause)
	}
}

// DeduplicateBySymptomCauseHash deduplicates operational learnings by exact
// kind-aware identity: symptom+cause for lessons, intent+approach+invariant for
// cases, and the directive text for memos/decisions.
func DeduplicateBySymptomCauseHash(learnings []OperationalLearning) []OperationalLearning {
	if len(learnings) <= 1 {
		return learnings
	}

	var merged []OperationalLearning
	hashToIdx := make(map[string]int)

	for _, incoming := range learnings {
		if incoming.SourceOccurrences <= 0 {
			incoming.SourceOccurrences = 1
		}
		hash := learningDedupKey(incoming)
		if idx, exists := hashToIdx[hash]; exists {
			if merged[idx].SourceOccurrences <= 0 {
				merged[idx].SourceOccurrences = 1
			}
			mergeFindings(&merged[idx], incoming)
		} else {
			idx = len(merged)
			merged = append(merged, incoming)
			hashToIdx[hash] = idx
		}
	}
	return merged
}

func harvestFromStore(st *store.Store, opts HarvesterOptions, seenIDs map[string]bool, results *[]OperationalLearning) {
	if st == nil || st.DB == nil {
		return
	}

	query := `SELECT id, project_id, claim, kind, first_seen, confidence FROM facts WHERE (kind IN ('operational_learning', 'lesson', 'case', 'memo', 'decision') OR claim LIKE 'Lesson:%' OR claim LIKE 'OperationalLearning:%') AND is_stale = 0`
	var args []any
	if opts.ProjectID != "" {
		query += ` AND project_id = ?`
		args = append(args, opts.ProjectID)
	}
	query += ` ORDER BY rowid DESC`

	rows, err := st.DB.Query(query, args...)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, projectID, claim, kind, confidence string
			firstSeen                              sql.NullString
		)
		if err := rows.Scan(&id, &projectID, &claim, &kind, &firstSeen, &confidence); err != nil {
			continue
		}

		if seenIDs[id] {
			continue
		}

		created := time.Now()
		if firstSeen.Valid && firstSeen.String != "" {
			if t, err := time.Parse(time.RFC3339, firstSeen.String); err == nil {
				created = t
			}
		}

		// Apply secret redaction if configured
		if opts.RedactSecrets {
			claim = gateway.Redact(claim)
		}

		switch kind {
		case "operational_learning":
			if op, ok := ParseOperationalLearning(claim); ok {
				op.ID = id
				op.ProjectID = projectID
				if op.Confidence == "" {
					op.Confidence = confidence
				}
				op.CreatedAt = created
				if opts.Domain == "" || op.Domain == opts.Domain {
					*results = append(*results, *op)
					seenIDs[id] = true
				}
			} else {
				domain := ClassifyDomain(claim, nil)
				if opts.Domain == "" || domain == opts.Domain {
					rule := HarmonizeRule(claim)
					op := OperationalLearning{
						ID:                id,
						ProjectID:         projectID,
						Kind:              KindLesson,
						Domain:            domain,
						TechStack:         domain,
						Title:             claim,
						Symptom:           claim,
						RootCause:         "Operational learning recorded in session.",
						PivotalFix:        claim,
						GuardrailRule:     rule,
						Confidence:        confidence,
						SourceOccurrences: 1,
						CreatedAt:         created,
					}
					*results = append(*results, op)
					seenIDs[id] = true
				}
			}

		case "lesson":
			symptom, cause, fix, tagsStr := store.ParseLesson(claim)
			if symptom == "" {
				symptom = strings.TrimPrefix(strings.TrimSpace(claim), "Lesson: ")
			}
			var tags []string
			if tagsStr != "" {
				for _, t := range strings.Split(tagsStr, ",") {
					if ct := strings.TrimSpace(t); ct != "" {
						tags = append(tags, ct)
					}
				}
			}
			domain := ClassifyDomain(symptom+" "+cause+" "+fix, tags)
			if opts.Domain != "" && domain != opts.Domain {
				continue
			}

			rule := HarmonizeRule(fix)
			if rule == "" && cause != "" {
				rule = HarmonizeRule("Avoid " + cause)
			}
			if rule == "" {
				rule = HarmonizeRule(symptom)
			}

			title := symptom
			if len(title) > 90 {
				title = title[:87] + "..."
			}

			op := OperationalLearning{
				ID:                id,
				ProjectID:         projectID,
				Kind:              KindLesson,
				Domain:            domain,
				TechStack:         domain,
				Title:             title,
				Symptom:           symptom,
				RootCause:         cause,
				PivotalFix:        fix,
				GuardrailRule:     rule,
				Tags:              tags,
				Confidence:        confidence,
				SourceOccurrences: 1,
				CreatedAt:         created,
			}
			*results = append(*results, op)
			seenIDs[id] = true

		case "case":
			intent, approach, invariant, tagsStr := store.ParseCase(claim)
			var tags []string
			if tagsStr != "" {
				for _, t := range strings.Split(tagsStr, ",") {
					if ct := strings.TrimSpace(t); ct != "" {
						tags = append(tags, ct)
					}
				}
			}
			domain := ClassifyDomain(intent+" "+approach+" "+invariant, tags)
			if opts.Domain != "" && domain != opts.Domain {
				continue
			}

			rule := HarmonizeRule(invariant)
			if rule == "" {
				rule = HarmonizeRule(approach)
			}

			title := intent
			if title == "" {
				title = "Architectural Case Pattern"
			}

			// A case is not a lesson: preserve intent/approach/invariant as
			// first-class fields and never synthesize failure-mode text.
			op := OperationalLearning{
				ID:            id,
				ProjectID:     projectID,
				Kind:          KindCase,
				Domain:        domain,
				TechStack:     domain,
				Title:         title,
				Intent:        intent,
				Approach:      approach,
				Invariant:     invariant,
				GuardrailRule: rule,
				Tags:          tags,
				Confidence:    confidence,
				CreatedAt:     created,
			}
			*results = append(*results, op)
			seenIDs[id] = true

		case "memo", "decision":
			if !opts.IncludeMemos {
				continue
			}
			domain := ClassifyDomain(claim, nil)
			if opts.Domain != "" && domain != opts.Domain {
				continue
			}

			learningKind := KindMemo
			if kind == "decision" {
				learningKind = KindDecision
			}

			title := claim
			if len(title) > 80 {
				title = title[:77] + "..."
			}

			// Directives are not failures: keep the full text and render it as
			// Directive/Decision, never as symptom/cause/fix.
			op := OperationalLearning{
				ID:            id,
				ProjectID:     projectID,
				Kind:          learningKind,
				Domain:        domain,
				TechStack:     domain,
				Title:         title,
				Directive:     claim,
				GuardrailRule: HarmonizeRule(claim),
				Confidence:    confidence,
				CreatedAt:     created,
			}
			*results = append(*results, op)
		default:
			trimmedClaim := strings.TrimSpace(claim)
			if strings.HasPrefix(trimmedClaim, "Lesson:") {
				symptom, cause, fix, tagsStr := store.ParseLesson(trimmedClaim)
				if symptom == "" {
					symptom = strings.TrimPrefix(trimmedClaim, "Lesson: ")
				}
				var tags []string
				if tagsStr != "" {
					for _, t := range strings.Split(tagsStr, ",") {
						if ct := strings.TrimSpace(t); ct != "" {
							tags = append(tags, ct)
						}
					}
				}
				domain := ClassifyDomain(symptom+" "+cause+" "+fix, tags)
				if opts.Domain != "" && domain != opts.Domain {
					continue
				}

				rule := HarmonizeRule(fix)
				if rule == "" && cause != "" {
					rule = HarmonizeRule("Avoid " + cause)
				}
				if rule == "" {
					rule = HarmonizeRule(symptom)
				}

				title := symptom
				if len(title) > 90 {
					title = title[:87] + "..."
				}

				op := OperationalLearning{
					ID:            id,
					ProjectID:     projectID,
					Kind:          KindLesson,
					Domain:        domain,
					TechStack:     domain,
					Title:         title,
					Symptom:       symptom,
					RootCause:     cause,
					PivotalFix:    fix,
					GuardrailRule: rule,
					Tags:          tags,
					Confidence:    confidence,
					CreatedAt:     created,
				}
				*results = append(*results, op)
				seenIDs[id] = true
			} else if strings.HasPrefix(trimmedClaim, "OperationalLearning:") {
				if op, ok := ParseOperationalLearning(trimmedClaim); ok {
					op.ID = id
					op.ProjectID = projectID
					if op.Confidence == "" {
						op.Confidence = confidence
					}
					op.CreatedAt = created
					if opts.Domain == "" || op.Domain == opts.Domain {
						*results = append(*results, *op)
						seenIDs[id] = true
					}
				}
			}
		}
	}
}
