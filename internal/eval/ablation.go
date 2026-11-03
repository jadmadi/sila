package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// resolveRuleFile resolves a rule path relative to explicit source roots independent of process cwd.
// Missing, unreadable, escaping, directory, or 0-byte (absent) files fail with an error.
func resolveRuleFile(rulePath string, sourceRoots []string, isTarget bool) (*ResolvedRule, error) {
	rulePath = strings.TrimSpace(rulePath)
	if rulePath == "" {
		return nil, fmt.Errorf("rule path cannot be empty")
	}

	cleanPath := filepath.Clean(rulePath)
	// Check for directory traversal escaping root
	if cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("rule path %q escapes source workspace (directory traversal)", rulePath)
	}

	// If path is absolute, ensure it is contained within at least one source root
	if filepath.IsAbs(cleanPath) && len(sourceRoots) > 0 {
		inside := false
		for _, root := range sourceRoots {
			absRoot, err := filepath.Abs(root)
			if err == nil {
				rel, err := filepath.Rel(absRoot, cleanPath)
				if err == nil && !strings.HasPrefix(rel, "..") && rel != ".." {
					inside = true
					break
				}
			}
		}
		if !inside {
			return nil, fmt.Errorf("rule path %q escapes source workspace", rulePath)
		}
	}

	// Build search candidates
	var candidates []string
	if filepath.IsAbs(cleanPath) {
		candidates = []string{cleanPath}
	} else {
		for _, root := range sourceRoots {
			candidates = append(candidates,
				filepath.Join(root, cleanPath),
				filepath.Join(root, ".sila", "rules", cleanPath),
				filepath.Join(root, ".cursor", "rules", cleanPath),
				filepath.Join(root, "rules", cleanPath),
			)
			if !strings.HasSuffix(cleanPath, ".md") {
				candidates = append(candidates,
					filepath.Join(root, cleanPath+".md"),
					filepath.Join(root, ".sila", "rules", cleanPath+".md"),
					filepath.Join(root, ".cursor", "rules", cleanPath+".md"),
					filepath.Join(root, "rules", cleanPath+".md"),
				)
			}
		}
	}

	var foundPath string
	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil {
			if fi.IsDir() {
				return nil, fmt.Errorf("rule path %q resolved to directory %q (rules must be regular files)", rulePath, cand)
			}
			foundPath = cand
			break
		}
	}

	if foundPath == "" {
		return nil, fmt.Errorf("rule path %q not found or unreadable in source roots", rulePath)
	}

	fi, err := os.Stat(foundPath)
	if err != nil {
		return nil, fmt.Errorf("stat rule %q: %w", rulePath, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("rule %q is not a regular file (mode: %v)", rulePath, fi.Mode())
	}

	data, err := os.ReadFile(foundPath)
	if err != nil {
		return nil, fmt.Errorf("read rule %q: %w", rulePath, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("rule %q contains 0 bytes (absent rule evidence)", rulePath)
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	relPath := rulePath
	for _, root := range sourceRoots {
		absRoot, _ := filepath.Abs(root)
		if rel, err := filepath.Rel(absRoot, foundPath); err == nil && !strings.HasPrefix(rel, "..") && rel != ".." {
			relPath = rel
			break
		}
	}

	return &ResolvedRule{
		Path:     foundPath,
		RelPath:  relPath,
		Content:  string(data),
		Hash:     hash,
		Bytes:    len(data),
		IsTarget: isTarget,
	}, nil
}

// RunAblationCase executes a single case under both "with" and "without" arms to measure delta.
func RunAblationCase(ctx context.Context, runner HarnessRunner, c *Case, target string, workDir string) (*AblationResult, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("ablation target cannot be empty")
	}

	// 1. Build source roots from workDir and case directory
	var sourceRoots []string
	if workDir != "" {
		if abs, err := filepath.Abs(workDir); err == nil {
			sourceRoots = append(sourceRoots, abs)
		} else {
			sourceRoots = append(sourceRoots, workDir)
		}
	}
	if c.Path != "" {
		caseAbs, err := filepath.Abs(c.Path)
		if err == nil {
			sourceRoots = append(sourceRoots, caseAbs)
			parent := filepath.Dir(caseAbs)
			if filepath.Base(parent) == "evals" {
				sourceRoots = append(sourceRoots, filepath.Dir(parent))
			} else if parent != caseAbs && parent != "/" && parent != "." {
				sourceRoots = append(sourceRoots, parent)
			}
		} else {
			sourceRoots = append(sourceRoots, c.Path)
		}
	}

	// 2. Resolve and load target rule bytes; must fail before execution if missing, unreadable, escaping or empty
	targetRule, err := resolveRuleFile(target, sourceRoots, true)
	if err != nil {
		return nil, fmt.Errorf("ablation target %q: %w", target, err)
	}

	// 3. Resolve and load control rules; must fail before execution if any control is invalid
	var controlRules []ResolvedRule
	controlHashes := make(map[string]string)
	seenRulePaths := make(map[string]bool)

	var declaredRules []string
	declaredRules = append(declaredRules, c.Prompt.Rules...)
	declaredRules = append(declaredRules, c.CaseConfig.Rules...)

	for _, rPath := range declaredRules {
		rPath = strings.TrimSpace(rPath)
		if rPath == "" {
			continue
		}
		rule, err := resolveRuleFile(rPath, sourceRoots, false)
		if err != nil {
			return nil, fmt.Errorf("control rule %q: %w", rPath, err)
		}
		// If declared rule matches the target rule by path or hash, exclude from controls
		if filepath.Clean(rule.Path) == filepath.Clean(targetRule.Path) || rule.Hash == targetRule.Hash {
			continue
		}
		if seenRulePaths[rule.Path] {
			continue
		}
		seenRulePaths[rule.Path] = true
		controlRules = append(controlRules, *rule)
		controlHashes[rule.RelPath] = rule.Hash
	}

	// 4. Construct Arm A (With: Target + Controls) and Arm B (Without: Controls only)
	rulesWith := append([]ResolvedRule{*targetRule}, controlRules...)
	rulesWithout := append([]ResolvedRule{}, controlRules...)

	// Invariant verification: target is present ONLY in rulesWith, controls match byte-for-byte
	if len(rulesWith) != len(rulesWithout)+1 {
		return nil, fmt.Errorf("invariant violation: with-arm rules count (%d) != without-arm rules count (%d) + 1", len(rulesWith), len(rulesWithout))
	}

	// 5. Execute Arm A: With Target
	resWith, err := ExecuteCaseWithRules(ctx, runner, c, workDir, "with", rulesWith, target, false)
	if err != nil {
		return nil, fmt.Errorf("arm 'with' failed: %w", err)
	}

	// 6. Execute Arm B: Without Target
	resWithout, err := ExecuteCaseWithRules(ctx, runner, c, workDir, "without", rulesWithout, target, true)
	if err != nil {
		return nil, fmt.Errorf("arm 'without' failed: %w", err)
	}

	delta := resWith.PassRate - resWithout.PassRate
	tokensWith := 0
	tokensWithout := 0
	tokensMeasured := false
	if resWith.RunResult != nil {
		tokensWith = resWith.RunResult.Tokens.TotalTokens
		if resWith.RunResult.Tokens.Measured {
			tokensMeasured = true
		}
	}
	if resWithout.RunResult != nil {
		tokensWithout = resWithout.RunResult.Tokens.TotalTokens
		if resWithout.RunResult.Tokens.Measured {
			tokensMeasured = true
		}
	}

	tokenDelta := tokensWith - tokensWithout
	var effPer1k float64
	var recommendation string
	var reason string

	isSynthetic := resWith.Synthetic || resWithout.Synthetic
	hasExecutionError := resWith.Error != "" || resWithout.Error != "" ||
		(resWith.RunResult != nil && resWith.RunResult.ExitCode != 0) ||
		(resWithout.RunResult != nil && resWithout.RunResult.ExitCode != 0)

	if isSynthetic {
		recommendation = "insufficient_evidence"
		reason = "synthetic mock runs cannot justify production rule keep/prune decisions"
		effPer1k = 0.0
	} else if hasExecutionError {
		recommendation = "insufficient_evidence"
		errDetails := []string{}
		if resWith.Error != "" {
			errDetails = append(errDetails, "with: "+resWith.Error)
		} else if resWith.RunResult != nil && resWith.RunResult.ExitCode != 0 {
			errDetails = append(errDetails, fmt.Sprintf("with: harness exited with code %d", resWith.RunResult.ExitCode))
		}
		if resWithout.Error != "" {
			errDetails = append(errDetails, "without: "+resWithout.Error)
		} else if resWithout.RunResult != nil && resWithout.RunResult.ExitCode != 0 {
			errDetails = append(errDetails, fmt.Sprintf("without: harness exited with code %d", resWithout.RunResult.ExitCode))
		}
		reason = fmt.Sprintf("execution error in evaluation runs (%s)", strings.Join(errDetails, ", "))
		effPer1k = 0.0
	} else if resWith.TotalScore == 0 && resWithout.TotalScore == 0 && !resWith.Passed && !resWithout.Passed {
		recommendation = "insufficient_evidence"
		reason = "both arms failed all criteria; zero comparable performance variation"
		effPer1k = 0.0
	} else if delta > 0 {
		if tokenDelta > 0 {
			effPer1k = (delta / float64(tokenDelta)) * 1000.0
		} else {
			effPer1k = delta * 1000.0
		}

		if delta < 0.10 && tokenDelta > 1500 {
			recommendation = "demote"
			reason = fmt.Sprintf("marginal gain (+%.1f%%) outweighed by excessive token cost (+%d tokens); demote to recall", delta*100.0, tokenDelta)
		} else {
			recommendation = "keep"
			reason = fmt.Sprintf("target demonstrates positive accuracy gain (+%.1f%%)", delta*100.0)
		}
	} else if delta < 0 {
		recommendation = "prune"
		reason = fmt.Sprintf("target degrades agent accuracy by %.1f%%", delta*100.0)
		effPer1k = 0.0
	} else { // delta == 0
		if tokenDelta > 0 {
			recommendation = "prune"
			reason = fmt.Sprintf("target consumes %d tokens with 0%% accuracy gain", tokenDelta)
			effPer1k = 0.0
		} else {
			recommendation = "insufficient_evidence"
			reason = "zero score delta and zero token delta across arms; insufficient evidence to recommend action"
			effPer1k = 0.0
		}
	}

	return &AblationResult{
		CaseID:          c.ID,
		Target:          target,
		TargetHash:      targetRule.Hash,
		ControlHashes:   controlHashes,
		InjectedWith:    rulesWith,
		InjectedWithout: rulesWithout,
		ScoreWith:       resWith.TotalScore,
		ScoreWithout:    resWithout.TotalScore,
		Delta:           delta,
		TokensWith:      tokensWith,
		TokensWithout:   tokensWithout,
		TokenDelta:      tokenDelta,
		TokensMeasured:  tokensMeasured,
		EfficacyPer1k:   effPer1k,
		Recommendation:  recommendation,
		Reason:          reason,
		ResultWith:      resWith,
		ResultWithout:   resWithout,
	}, nil
}

// ExecuteAblationSuite runs an entire suite under ablation comparison against a target rule/skill.
func ExecuteAblationSuite(ctx context.Context, runner HarnessRunner, cases []*Case, target string, workDir string, filterCase, filterTag string) (*SuiteResult, error) {
	start := time.Now()
	var filtered []*Case

	for _, c := range cases {
		if filterCase != "" && !strings.EqualFold(c.ID, filterCase) && !strings.Contains(strings.ToLower(c.ID), strings.ToLower(filterCase)) {
			continue
		}
		if filterTag != "" {
			tagMatch := false
			for _, t := range c.Prompt.Tags {
				if strings.EqualFold(t, filterTag) {
					tagMatch = true
					break
				}
			}
			if !tagMatch {
				continue
			}
		}
		filtered = append(filtered, c)
	}

	if len(filtered) == 0 {
		return &SuiteResult{
			Timestamp:   start,
			Harness:     runner.Name(),
			Synthetic:   IsSyntheticRunner(runner),
			Passed:      false,
			TotalCases:  0,
			PassedCases: 0,
			TotalScore:  0,
			MaxScore:    0,
			Duration:    time.Since(start),
			Cases:       nil,
			Ablations:   nil,
			Error:       "zero evaluation cases selected",
		}, nil
	}

	var ablations []AblationResult
	var casesRes []CaseResult
	totalScore := 0.0
	maxScore := 0.0
	passedCount := 0

	for _, c := range filtered {
		ab, err := RunAblationCase(ctx, runner, c, target, workDir)
		if err != nil {
			return nil, err
		}
		ablations = append(ablations, *ab)
		if ab.ResultWith != nil {
			casesRes = append(casesRes, *ab.ResultWith)
			totalScore += ab.ResultWith.TotalScore
			maxScore += ab.ResultWith.MaxScore
			if ab.ResultWith.Passed {
				passedCount++
			}
		}
	}

	suitePassed := len(filtered) > 0 && passedCount == len(filtered)

	return &SuiteResult{
		Timestamp:   time.Now(),
		Harness:     runner.Name(),
		Synthetic:   IsSyntheticRunner(runner),
		Passed:      suitePassed,
		TotalCases:  len(filtered),
		PassedCases: passedCount,
		TotalScore:  totalScore,
		MaxScore:    maxScore,
		Duration:    time.Since(start),
		Cases:       casesRes,
		Ablations:   ablations,
	}, nil
}
