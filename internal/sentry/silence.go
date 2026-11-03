package sentry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	reHex           = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	rePos           = regexp.MustCompile(`:\d+:\d+`)
	reLineNum       = regexp.MustCompile(`:\d+`)
	reTime          = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T\s]\d{2}:\d{2}:\d{2}(\.\d+)?Z?`)
	reUUID          = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reTempPath      = regexp.MustCompile(`(/tmp|/var/tmp)/[^\s]+`)
	reCompilerError = regexp.MustCompile(`(?m)^([a-zA-Z0-9_\-\./]+\.(?:go|ts|js|py|rs|c|cpp)):(\d+)(?::\d+)?: (.*)`)
	rePanicError    = regexp.MustCompile(`(?m)^panic: (.*)`)
	reTestFailure   = regexp.MustCompile(`(?m)^--- FAIL: (\w+) \((.*)\)`)
)

// MaskVolatileTokens replaces volatile identifiers like pointers, timestamps, and line numbers
// with deterministic wildcard tokens to prevent cluster vocabulary explosion.
func MaskVolatileTokens(raw string) string {
	s := reHex.ReplaceAllString(raw, "<HEX>")
	s = reTime.ReplaceAllString(s, "<TIME>")
	s = reUUID.ReplaceAllString(s, "<UUID>")
	s = reTempPath.ReplaceAllString(s, "<TMP>")
	s = rePos.ReplaceAllString(s, ":<POS>")
	s = reLineNum.ReplaceAllString(s, ":<LINE>")
	return strings.TrimSpace(s)
}

// DiagnosticCluster represents a clustered recurring diagnostic signature mined from agent sessions.
type DiagnosticCluster struct {
	Signature       string         `json:"signature"`
	Category        string         `json:"category"`      // "compiler_error", "linter_warning", "runtime_panic", "test_failure"
	Template        string         `json:"template"`      // Drain template with <*> wildcards
	TargetFile      string         `json:"target_file"`   // repository-relative path
	TargetSymbol    string         `json:"target_symbol"` // enclosing function, type, or package
	OccurrenceCount int            `json:"occurrence_count"`
	FirstSeen       string         `json:"first_seen"`
	LastSeen        string         `json:"last_seen"`
	SessionHistory  map[string]int `json:"session_history,omitempty"` // session_id -> count
}

// ComputeClusterSignature calculates a deterministic SHA-256 fingerprint from cluster fields.
func ComputeClusterSignature(category, targetFile, targetSymbol, template string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s", category, targetFile, targetSymbol, template)
	return "sig_" + hex.EncodeToString(h.Sum(nil)[:8])
}

// DrainParser provides an online streaming log template miner based on Drain.
type DrainParser struct {
	mu         sync.RWMutex
	similarity float64 // threshold ratio, default 0.85
	clusters   []*DiagnosticCluster
}

// NewDrainParser initializes a DrainParser with similarity threshold.
func NewDrainParser(similarity float64) *DrainParser {
	if similarity <= 0 || similarity > 1.0 {
		similarity = 0.85
	}
	return &DrainParser{
		similarity: similarity,
		clusters:   make([]*DiagnosticCluster, 0),
	}
}

// Parse ingests a raw diagnostic message, anchors it to a file and symbol, and returns its cluster.
func (p *DrainParser) Parse(category, targetFile, targetSymbol, rawMessage, sessionID string) *DiagnosticCluster {
	p.mu.Lock()
	defer p.mu.Unlock()

	masked := MaskVolatileTokens(rawMessage)
	tokens := strings.Fields(masked)
	if len(tokens) == 0 {
		tokens = []string{"<EMPTY>"}
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Search for matching cluster by length and token overlap ratio
	var bestCluster *DiagnosticCluster
	bestScore := 0.0

	for _, c := range p.clusters {
		if c.Category != category || c.TargetFile != targetFile || c.TargetSymbol != targetSymbol {
			continue
		}
		cTokens := strings.Fields(c.Template)
		if len(cTokens) != len(tokens) {
			continue
		}

		matching := 0
		for i := range tokens {
			if tokens[i] == cTokens[i] || cTokens[i] == "<*>" {
				matching++
			}
		}
		score := float64(matching) / float64(len(tokens))
		if score >= p.similarity && score > bestScore {
			bestScore = score
			bestCluster = c
		}
	}

	if bestCluster != nil {
		// Update existing cluster template with wildcards
		cTokens := strings.Fields(bestCluster.Template)
		for i := range tokens {
			if cTokens[i] != tokens[i] && cTokens[i] != "<*>" {
				cTokens[i] = "<*>"
			}
		}
		bestCluster.Template = strings.Join(cTokens, " ")
		bestCluster.OccurrenceCount++
		bestCluster.LastSeen = now
		if bestCluster.SessionHistory == nil {
			bestCluster.SessionHistory = make(map[string]int)
		}
		if sessionID != "" {
			bestCluster.SessionHistory[sessionID]++
		}
		return bestCluster
	}

	// Create new cluster leaf
	tpl := strings.Join(tokens, " ")
	sig := ComputeClusterSignature(category, targetFile, targetSymbol, tpl)
	newCluster := &DiagnosticCluster{
		Signature:       sig,
		Category:        category,
		Template:        tpl,
		TargetFile:      targetFile,
		TargetSymbol:    targetSymbol,
		OccurrenceCount: 1,
		FirstSeen:       now,
		LastSeen:        now,
		SessionHistory:  make(map[string]int),
	}
	if sessionID != "" {
		newCluster.SessionHistory[sessionID] = 1
	}

	p.clusters = append(p.clusters, newCluster)
	return newCluster
}

// PageHinkleyTracker tracks sequential change points in diagnostic reporting frequency.
type PageHinkleyTracker struct {
	BurnInSessions int     // Minimum sessions before anomaly alerting (default: 3)
	Delta          float64 // Stochastic noise tolerance (default: 0.10)
	Lambda         float64 // Anomaly alert threshold (default: 25.0)
}

// DefaultPageHinkleyTracker returns standard calibrated parameters.
func DefaultPageHinkleyTracker() PageHinkleyTracker {
	return PageHinkleyTracker{
		BurnInSessions: 3,
		Delta:          0.10,
		Lambda:         25.0,
	}
}

// DetectDownwardDrop computes the downward Page-Hinkley statistic across session counts.
// Returns (statistic, alertTriggered).
func (t PageHinkleyTracker) DetectDownwardDrop(sessionFrequencies []float64) (float64, bool) {
	n := len(sessionFrequencies)
	if n < t.BurnInSessions {
		return 0.0, false
	}

	// Calculate cumulative mean
	var sum float64
	for _, x := range sessionFrequencies {
		sum += x
	}
	mean := sum / float64(n)

	// Cumulative downward deviation m_t = sum(mean - x_i - delta)
	var mt float64
	minMt := 0.0
	maxPH := 0.0

	for _, x := range sessionFrequencies {
		mt += (mean - x - t.Delta)
		if mt < minMt {
			minMt = mt
		}
		ph := mt - minMt
		if ph > maxPH {
			maxPH = ph
		}
	}

	return maxPH, maxPH >= t.Lambda
}

// SilenceAnomaly summarizes a detected flag extinction or negative-drift anomaly.
type SilenceAnomaly struct {
	Signature       string  `json:"signature"`
	Category        string  `json:"category"`
	TargetFile      string  `json:"target_file"`
	TargetSymbol    string  `json:"target_symbol"`
	Template        string  `json:"template"`
	PageHinkleyStat float64 `json:"page_hinkley_stat"`
	CouplingIndex   float64 `json:"coupling_index"`
	Status          string  `json:"status"` // "silence_anomaly", "verified_fix", "overridden"
	Explanation     string  `json:"explanation"`
}

// EvaluateSilence determines whether a frequency collapse represents a genuine fix,
// flag extinction anomaly, stable frequency, or uncalibrated burn-in.
func EvaluateSilence(c *DiagnosticCluster, sessionFrequencies []float64, tracker any, aci float64) *SilenceAnomaly {
	detector := NewSessionHistoryDetector(tracker)
	return detector.EvaluateCluster(c, sessionFrequencies, aci)
}

// ExtractedDiagnostic represents an individual error or warning extracted from conversation logs.
type ExtractedDiagnostic struct {
	Category     string
	TargetFile   string
	TargetSymbol string
	RawMessage   string
	SessionID    string
}

// ExtractDiagnosticsFromText extracts diagnostics from text logs or message content.
func ExtractDiagnosticsFromText(text, sessionID, repoDir string) []ExtractedDiagnostic {
	var diags []ExtractedDiagnostic
	if strings.TrimSpace(text) == "" {
		return diags
	}

	// 1. Compiler errors
	matches := reCompilerError.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) >= 4 {
			file := normalizeLogPath(m[1], repoDir)
			diags = append(diags, ExtractedDiagnostic{
				Category:     "compiler_error",
				TargetFile:   file,
				TargetSymbol: deriveSymbolHint(file, m[3]),
				RawMessage:   strings.TrimSpace(m[0]),
				SessionID:    sessionID,
			})
		}
	}

	// 2. Panics
	panicMatches := rePanicError.FindAllStringSubmatch(text, -1)
	for _, m := range panicMatches {
		if len(m) >= 2 {
			diags = append(diags, ExtractedDiagnostic{
				Category:     "runtime_panic",
				TargetFile:   "runtime",
				TargetSymbol: "panic",
				RawMessage:   strings.TrimSpace(m[0]),
				SessionID:    sessionID,
			})
		}
	}

	// 3. Test failures
	testMatches := reTestFailure.FindAllStringSubmatch(text, -1)
	for _, m := range testMatches {
		if len(m) >= 3 {
			diags = append(diags, ExtractedDiagnostic{
				Category:     "test_failure",
				TargetFile:   "test",
				TargetSymbol: m[1],
				RawMessage:   strings.TrimSpace(m[0]),
				SessionID:    sessionID,
			})
		}
	}

	return diags
}

func normalizeLogPath(p, repoDir string) string {
	clean := filepath.Clean(strings.TrimSpace(p))
	if repoDir != "" && filepath.IsAbs(clean) {
		if rel, err := filepath.Rel(repoDir, clean); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(clean)
}

func deriveSymbolHint(file, msg string) string {
	if idx := strings.Index(msg, "undefined: "); idx != -1 {
		sym := strings.TrimSpace(msg[idx+len("undefined: "):])
		if f := strings.Fields(sym); len(f) > 0 {
			return f[0]
		}
	}
	base := filepath.Base(file)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// Ingest parses an extracted diagnostic and updates the parser clusters.
func (p *DrainParser) Ingest(diag ExtractedDiagnostic) *DiagnosticCluster {
	return p.Parse(diag.Category, diag.TargetFile, diag.TargetSymbol, diag.RawMessage, diag.SessionID)
}

// Clusters returns a copy of all current diagnostic clusters.
func (p *DrainParser) Clusters() []*DiagnosticCluster {
	p.mu.RLock()
	defer p.mu.RUnlock()
	res := make([]*DiagnosticCluster, len(p.clusters))
	copy(res, p.clusters)
	return res
}
