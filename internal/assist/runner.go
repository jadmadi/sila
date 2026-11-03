package assist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	rxSecKey   = regexp.MustCompile(`(?i)(sk_live_[0-9a-zA-Z]{16,}|ghp_[0-9a-zA-Z]{20,}|gho_[0-9a-zA-Z]{20,}|AKIA[0-9A-Z]{16}|bearer\s+[a-zA-Z0-9_\-\.]{25,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|aws_secret_access_key\s*=\s*[a-zA-Z0-9/+=]{30,})`)
	rxPassword = regexp.MustCompile(`(?i)(?:password|secret|api_key)\s*[:=]\s*["'][^"'\s]{8,}["']`)
)

// tokenDiceSimilarity computes simple token overlap without importing dream.
func tokenDiceSimilarity(a, b string) float64 {
	ta := strings.Fields(strings.ToLower(a))
	tb := strings.Fields(strings.ToLower(b))
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	setA := map[string]int{}
	for _, w := range ta {
		setA[w]++
	}
	inter := 0
	for _, w := range tb {
		if setA[w] > 0 {
			inter++
			setA[w]--
		}
	}
	return 2 * float64(inter) / float64(len(ta)+len(tb))
}

// EvalFixture represents a single sanitized evaluation testcase.
type EvalFixture struct {
	ID             string         `json:"id"`
	Language       string         `json:"language"` // "en", "ar", "mixed"
	IsSynthetic    bool           `json:"is_synthetic"`
	Provenance     string         `json:"provenance,omitempty"`
	IsHeldOut      bool           `json:"is_held_out,omitempty"`
	Category       string         `json:"category"` // "activity", "drift", "rerank", "security"
	InputEvidence  map[string]any `json:"input_evidence"`
	ExpectedLabels map[string]any `json:"expected_labels"`
	Notes          string         `json:"notes,omitempty"`
}

// CaseResult captures the evaluation outcome for an individual fixture.
type CaseResult struct {
	ID             string             `json:"id"`
	Language       string             `json:"language"`
	Category       string             `json:"category,omitempty"`
	IsSynthetic    bool               `json:"is_synthetic,omitempty"`
	Provenance     string             `json:"provenance,omitempty"`
	IsHeldOut      bool               `json:"is_held_out,omitempty"`
	Expected       map[string]any     `json:"expected"`
	Scores         map[string]float64 `json:"scores"`
	Matched        bool               `json:"matched"`
	LocalJudgments map[string]bool    `json:"local_judgments"`
	LocalMatched   bool               `json:"local_matched"`
	Error          string             `json:"error,omitempty"`
	ResolvedModel  string             `json:"resolved_model,omitempty"`
	DurationMs     int64              `json:"duration_ms,omitempty"`
	Usage          TokenUsage         `json:"usage"`
	InvalidAnswers map[string]string  `json:"invalid_answers,omitempty"`
}

// TaskMetric records precision, recall, F1, and raw counts for a single label.
type TaskMetric struct {
	Total          int     `json:"total"`
	TruePositives  int     `json:"true_positives"`
	FalsePositives int     `json:"false_positives"`
	TrueNegatives  int     `json:"true_negatives"`
	FalseNegatives int     `json:"false_negatives"`
	Abstentions    int     `json:"abstentions"`
	Precision      float64 `json:"precision"`
	Recall         float64 `json:"recall"`
	F1             float64 `json:"f1"`
	Accuracy       float64 `json:"accuracy"`
}

// EvalMetric aggregates benchmark measurements.
type EvalMetric struct {
	TotalCases        int                   `json:"total_cases"`
	EvaluatedCases    int                   `json:"evaluated_cases"`
	CorrectJudgments  int                   `json:"correct_judgments"`
	Accuracy          float64               `json:"accuracy"`
	LocalAccuracy     float64               `json:"local_accuracy"`
	LocalDiceAccuracy float64               `json:"local_dice_accuracy"` // backward-compatible alias
	LocalP50LatencyMs int64                 `json:"local_p50_latency_ms"`
	LocalP95LatencyMs int64                 `json:"local_p95_latency_ms"`
	P50LatencyMs      int64                 `json:"p50_latency_ms"`
	P95LatencyMs      int64                 `json:"p95_latency_ms"`
	TotalTokens       int                   `json:"total_tokens"`
	EstimatedSpendUSD float64               `json:"estimated_spend_usd"`
	Failures          int                   `json:"failures"`
	Abstentions       int                   `json:"abstentions"`
	HeldOutCases      int                   `json:"held_out_cases"`
	HeldOutCorrect    int                   `json:"held_out_correct"`
	HeldOutAccuracy   float64               `json:"held_out_accuracy"`
	PerTaskMetrics    map[string]TaskMetric `json:"per_task_metrics,omitempty"`
	FixtureHash       string                `json:"fixture_hash"`
	QuestionsHash     string                `json:"questions_hash"`
	Model             string                `json:"model,omitempty"`
	HasErrors         bool                  `json:"has_errors"`
	Cases             []CaseResult          `json:"cases,omitempty"`
}

// BenchmarkRunner executes evaluations across a suite of fixtures.
type BenchmarkRunner struct {
	Provider Provider
	Fixtures []EvalFixture
	mu       sync.Mutex
}

// NewBenchmarkRunner creates an evaluation runner with a chosen provider.
func NewBenchmarkRunner(p Provider) *BenchmarkRunner {
	return &BenchmarkRunner{
		Provider: p,
	}
}

// LoadFixtures loads fixture JSON files from a directory.
func (r *BenchmarkRunner) LoadFixtures(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		var fixtures []EvalFixture
		if err := json.Unmarshal(data, &fixtures); err != nil {
			// Try single fixture
			var f EvalFixture
			if err2 := json.Unmarshal(data, &f); err2 == nil && f.ID != "" {
				r.Fixtures = append(r.Fixtures, f)
				continue
			}
			return fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}
		r.Fixtures = append(r.Fixtures, fixtures...)
	}
	return r.ValidateFixtures()
}

// ValidateFixtures checks for duplicate IDs, missing labels, and split leakage.
func (r *BenchmarkRunner) ValidateFixtures() error {
	seenIDs := make(map[string]bool)
	heldOutProv := make(map[string]string)
	trainProv := make(map[string]string)

	for _, f := range r.Fixtures {
		if strings.TrimSpace(f.ID) == "" {
			return fmt.Errorf("fixture has empty ID")
		}
		if seenIDs[f.ID] {
			return fmt.Errorf("duplicate fixture ID: %s", f.ID)
		}
		seenIDs[f.ID] = true

		if len(f.ExpectedLabels) == 0 {
			return fmt.Errorf("fixture %s has no expected labels", f.ID)
		}

		prov := strings.TrimSpace(f.Provenance)
		if prov != "" {
			if f.IsHeldOut {
				if trainID, exists := trainProv[prov]; exists {
					return fmt.Errorf("split leakage: provenance %q appears in training fixture %s and held-out fixture %s", prov, trainID, f.ID)
				}
				heldOutProv[prov] = f.ID
			} else {
				if heldID, exists := heldOutProv[prov]; exists {
					return fmt.Errorf("split leakage: provenance %q appears in held-out fixture %s and training fixture %s", prov, heldID, f.ID)
				}
				trainProv[prov] = f.ID
			}
		}
	}
	return nil
}

// RunBenchmark executes all loaded fixtures and compares against baseline.
func (r *BenchmarkRunner) RunBenchmark(ctx context.Context, model string) (*EvalMetric, error) {
	if len(r.Fixtures) == 0 {
		return nil, fmt.Errorf("no fixtures loaded")
	}

	metric := &EvalMetric{
		TotalCases:     len(r.Fixtures),
		EvaluatedCases: len(r.Fixtures),
		Model:          model,
		PerTaskMetrics: make(map[string]TaskMetric),
		FixtureHash:    computeFixtureHash(r.Fixtures),
		QuestionsHash:  computeQuestionsHash(r.Fixtures),
	}

	var latencies []int64
	var localLatencies []int64
	correct := 0
	localCorrect := 0

	taskCounts := make(map[string]*TaskMetric)

	for _, f := range r.Fixtures {
		// 1. Evaluate deterministic local baseline (never reads fixture ID)
		localJudgments, localElapsed := evaluateLocalBaseline(f)
		localLatencies = append(localLatencies, localElapsed)

		localMatched := true
		for k, expectedVal := range f.ExpectedLabels {
			expBool, _ := expectedVal.(bool)
			if localJudgments[k] != expBool {
				localMatched = false
			}
		}
		if localMatched {
			localCorrect++
		}

		// 2. Build questions matching expected labels for provider
		questions := make(map[string]Question)
		for k := range f.ExpectedLabels {
			questions[k] = Question{
				Type:         TypeNoul,
				Instructions: instructionForLabel(k),
			}
			if _, ok := taskCounts[k]; !ok {
				taskCounts[k] = &TaskMetric{}
			}
			taskCounts[k].Total++
		}

		start := time.Now()
		resp, err := r.Provider.Evaluate(ctx, model, f.InputEvidence, questions)
		elapsed := time.Since(start).Milliseconds()

		if err != nil {
			metric.Failures++
			metric.HasErrors = true
			if f.IsHeldOut {
				metric.HeldOutCases++
			}
			for k := range f.ExpectedLabels {
				taskCounts[k].Abstentions++
				metric.Abstentions++
			}
			metric.Cases = append(metric.Cases, CaseResult{
				ID:             f.ID,
				Language:       f.Language,
				Category:       f.Category,
				IsSynthetic:    f.IsSynthetic,
				Provenance:     f.Provenance,
				IsHeldOut:      f.IsHeldOut,
				Expected:       f.ExpectedLabels,
				LocalJudgments: localJudgments,
				LocalMatched:   localMatched,
				Matched:        false,
				Error:          err.Error(),
				DurationMs:     elapsed,
			})
			continue
		}

		latencies = append(latencies, elapsed)
		metric.TotalTokens += resp.Usage.InputTokens + resp.Usage.OutputTokens

		// Evaluate provider correctness and update task confusion matrix
		matched := true
		scores := make(map[string]float64)
		hasInvalid := false
		if len(resp.InvalidAnswers) > 0 {
			hasInvalid = true
		}
		for k, expectedVal := range f.ExpectedLabels {
			expBool, _ := expectedVal.(bool)
			tm := taskCounts[k]

			if _, isInv := resp.InvalidAnswers[k]; isInv {
				hasInvalid = true
			}

			if ans, ok := resp.Nouls[k]; ok {
				scores[k] = ans.Noul
				gotBool := ans.Noul >= 0.5
				if expBool != gotBool {
					matched = false
				}
				if expBool && gotBool {
					tm.TruePositives++
				} else if !expBool && gotBool {
					tm.FalsePositives++
				} else if !expBool && !gotBool {
					tm.TrueNegatives++
				} else if expBool && !gotBool {
					tm.FalseNegatives++
				}
			} else {
				matched = false
				hasInvalid = true
				tm.Abstentions++
				metric.Abstentions++
			}
		}

		if hasInvalid {
			metric.HasErrors = true
			metric.Failures++
		}

		if matched {
			correct++
		}

		if f.IsHeldOut {
			metric.HeldOutCases++
			if matched {
				metric.HeldOutCorrect++
			}
		}

		metric.Cases = append(metric.Cases, CaseResult{
			ID:             f.ID,
			Language:       f.Language,
			Category:       f.Category,
			IsSynthetic:    f.IsSynthetic,
			Provenance:     f.Provenance,
			IsHeldOut:      f.IsHeldOut,
			Expected:       f.ExpectedLabels,
			Scores:         scores,
			Matched:        matched,
			LocalJudgments: localJudgments,
			LocalMatched:   localMatched,
			ResolvedModel:  resp.Model,
			DurationMs:     elapsed,
			Usage:          resp.Usage,
			InvalidAnswers: resp.InvalidAnswers,
		})
	}

	metric.CorrectJudgments = correct
	if metric.TotalCases > 0 {
		metric.Accuracy = float64(correct) / float64(metric.TotalCases)
		metric.LocalAccuracy = float64(localCorrect) / float64(metric.TotalCases)
		metric.LocalDiceAccuracy = metric.LocalAccuracy
	}
	if metric.HeldOutCases > 0 {
		metric.HeldOutAccuracy = float64(metric.HeldOutCorrect) / float64(metric.HeldOutCases)
	}

	// Cost estimation: $0.042 / 1M input tokens
	metric.EstimatedSpendUSD = float64(metric.TotalTokens) / 1e6 * 0.042

	// Calculate sorted percentiles
	if len(latencies) > 0 {
		slices.Sort(latencies)
		metric.P50LatencyMs = percentile(latencies, 0.50)
		metric.P95LatencyMs = percentile(latencies, 0.95)
	}
	if len(localLatencies) > 0 {
		slices.Sort(localLatencies)
		metric.LocalP50LatencyMs = percentile(localLatencies, 0.50)
		metric.LocalP95LatencyMs = percentile(localLatencies, 0.95)
	}

	// Compute precision, recall, f1 per task
	for k, tm := range taskCounts {
		if tm.TruePositives+tm.FalsePositives > 0 {
			tm.Precision = float64(tm.TruePositives) / float64(tm.TruePositives+tm.FalsePositives)
		}
		if tm.TruePositives+tm.FalseNegatives > 0 {
			tm.Recall = float64(tm.TruePositives) / float64(tm.TruePositives+tm.FalseNegatives)
		}
		if tm.Precision+tm.Recall > 0 {
			tm.F1 = 2 * (tm.Precision * tm.Recall) / (tm.Precision + tm.Recall)
		}
		if tm.Total > 0 {
			tm.Accuracy = float64(tm.TruePositives+tm.TrueNegatives) / float64(tm.Total)
		}
		metric.PerTaskMetrics[k] = *tm
	}

	return metric, nil
}

func instructionForLabel(k string) string {
	switch k {
	case "durable_decision":
		return "Does this evidence describe a durable architectural decision, system configuration change, or design invariant?"
	case "security_violation":
		return "Does this evidence contain or describe a security violation, secret exposure, or credential leak?"
	case "blocker_issue":
		return "Does this evidence describe a blocking failure, outage, or critical bug?"
	case "drift_contradiction":
		return "Does this evidence show a code implementation contradicting an established architectural invariant?"
	case "semantic_relevance":
		return "Is this evidence semantically relevant to the target task or search objective?"
	default:
		return fmt.Sprintf("Judge condition %s for fixture evidence", k)
	}
}

func extractEvidenceText(evidence map[string]any) string {
	var sb strings.Builder
	for k, v := range evidence {
		if k == "session_id" || k == "tool" {
			continue
		}
		sb.WriteString(fmt.Sprintf("%v ", v))
	}
	return strings.TrimSpace(sb.String())
}

func evaluateLocalBaseline(f EvalFixture) (map[string]bool, int64) {
	start := time.Now()
	judgments := make(map[string]bool)
	evidenceText := extractEvidenceText(f.InputEvidence)
	lower := strings.ToLower(evidenceText)

	for label := range f.ExpectedLabels {
		switch label {
		case "durable_decision":
			judgments[label] = localJudgeDecision(lower)
		case "security_violation":
			judgments[label] = localJudgeSecurity(evidenceText, lower)
		case "blocker_issue":
			judgments[label] = localJudgeBlocker(lower)
		case "drift_contradiction":
			judgments[label] = localJudgeDrift(lower)
		case "semantic_relevance":
			judgments[label] = localJudgeRelevance(f.InputEvidence)
		default:
			judgments[label] = false
		}
	}
	return judgments, time.Since(start).Milliseconds()
}

func localJudgeDecision(lower string) bool {
	if strings.Contains(lower, "ignore previous instructions") {
		return false
	}
	enKeywords := []string{
		"decision", "invariant", "pragma busy_timeout", "wal mode", "refactor",
		"standardized", "architectural", "non-goal", "milestone", "settled",
		"policy", "architecture", "cgo_enabled=0", "idempotent", "schema migration",
		"single spa", "repository layer", "opaque cursor", "standard envelope",
		"pure-go",
	}
	for _, kw := range enKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	arKeywords := []string{
		"قرار", "ثابت", "تفعيل نمط", "قفل قاعدة البيانات", "هيكلة",
		"معيار", "قاعدة", "مهلة الانتظار", "ترحيل", "توحيد",
	}
	for _, kw := range arKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func localJudgeSecurity(raw, lower string) bool {
	if strings.Contains(lower, "example_token") || strings.Contains(lower, "placeholder") {
		return false
	}
	if rxSecKey.MatchString(raw) || rxPassword.MatchString(raw) {
		return true
	}
	if strings.Contains(lower, "hardcoded secret token") || strings.Contains(lower, "leaked credential") ||
		strings.Contains(lower, "تسريب مفتاح") || strings.Contains(lower, "رمز سري مسرب") {
		return true
	}
	return false
}

func localJudgeBlocker(lower string) bool {
	keywords := []string{"blocker", "deadlock", "database locked error", "outage", "production down", "fatal crash", "panic", "عطل", "انهيار", "توقف"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func localJudgeDrift(lower string) bool {
	keywords := []string{"violates", "violation", "cgo dependency", "mattn/go-sqlite3", "contradicts", "raw sql strings", "second frontend framework", "مخالفة", "تعارض"}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func localJudgeRelevance(evidence map[string]any) bool {
	query, _ := evidence["query"].(string)
	doc, _ := evidence["document"].(string)
	if query == "" || doc == "" {
		return false
	}
	return tokenDiceSimilarity(query, doc) >= 0.2
}

func percentile(sorted []int64, pct float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := max(int(math.Round(pct*float64(len(sorted)-1))), 0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func computeFixtureHash(fixtures []EvalFixture) string {
	h := sha256.New()
	for _, f := range fixtures {
		data, _ := json.Marshal(f)
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func computeQuestionsHash(fixtures []EvalFixture) string {
	h := sha256.New()
	qMap := make(map[string]string)
	for _, f := range fixtures {
		for k := range f.ExpectedLabels {
			qMap[k] = instructionForLabel(k)
		}
	}
	keys := make([]string, 0, len(qMap))
	for k := range qMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k + ":" + qMap[k] + ";"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
