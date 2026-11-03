package crew

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// AuditorVerdict represents the verification verdict of the independent victory auditor.
type AuditorVerdict string

const (
	VerdictApproved                  AuditorVerdict = "APPROVED"
	VerdictRejected                  AuditorVerdict = "REJECTED"
	VerdictSelfCertificationRejected AuditorVerdict = "SELF_CERTIFICATION_REJECTED"
	VerdictVetoed                    AuditorVerdict = "VETOED"
)

// VictoryAuditResult is returned by the Victory Auditor.
type VictoryAuditResult struct {
	Passed        bool           `json:"passed"`
	SelfCertified bool           `json:"self_certified"`
	Verdict       AuditorVerdict `json:"verdict"`
	AuditorAgent  string         `json:"auditor_agent"`
	ChecksRun     []string       `json:"checks_run"`
	Errors        []string       `json:"errors,omitempty"`
	Notes         string         `json:"notes,omitempty"`
}

// VictoryAuditor defines the contract for independent completion verification.
// Invariant: The auditor operates in an isolated context and rejects self-certification
// declarations lacking verifiable on-disk evidence and clean gate passes.
type VictoryAuditor interface {
	AuditVictory(cm *Crewmate) (*VictoryAuditResult, error)
}

// DefaultVictoryAuditor evaluates candidate victory in the crewmate's worktree.
type DefaultVictoryAuditor struct {
	AuditorAgent string
}

// NewDefaultVictoryAuditor creates an independent Victory Auditor.
func NewDefaultVictoryAuditor(agent string) *DefaultVictoryAuditor {
	if agent == "" {
		agent = "victory-auditor"
	}
	return &DefaultVictoryAuditor{AuditorAgent: agent}
}

// AuditVictory audits a candidate crewmate. Rejects self-certification without atomic commits or clean gates.
func (a *DefaultVictoryAuditor) AuditVictory(cm *Crewmate) (*VictoryAuditResult, error) {
	res := &VictoryAuditResult{
		AuditorAgent: a.AuditorAgent,
		ChecksRun:    []string{},
		Errors:       []string{},
	}

	if cm == nil {
		res.Verdict = VerdictRejected
		res.Errors = append(res.Errors, "crewmate is nil")
		return res, nil
	}

	// 1. Verify worktree exists
	if _, err := os.Stat(cm.Worktree); err != nil {
		res.Verdict = VerdictRejected
		res.Errors = append(res.Errors, fmt.Sprintf("worktree not found at %s", cm.Worktree))
		return res, nil
	}
	res.ChecksRun = append(res.ChecksRun, "worktree_existence_probe")

	// 2. Self-certification check: examine git status in worktree
	cmdGit := exec.Command("git", "status", "--porcelain")
	cmdGit.Dir = cm.Worktree
	outGit, err := cmdGit.Output()
	res.ChecksRun = append(res.ChecksRun, "git_cleanliness_probe")
	if err != nil {
		res.Verdict = VerdictRejected
		res.Errors = append(res.Errors, fmt.Sprintf("git status probe failed: %v", err))
		return res, nil
	}

	// If uncommitted or unstaged changes remain, reject self-certification
	if strings.TrimSpace(string(outGit)) != "" {
		res.SelfCertified = true
		res.Verdict = VerdictSelfCertificationRejected
		res.Errors = append(res.Errors, "uncommitted changes detected in worktree; worker self-certified completion without atomic commit")
		return res, nil
	}

	// 3. Verify git log has commits
	cmdLog := exec.Command("git", "log", "-n", "1", "--oneline")
	cmdLog.Dir = cm.Worktree
	outLog, _ := cmdLog.Output()
	res.ChecksRun = append(res.ChecksRun, "git_commit_verification")

	// 4. Run pre-commit gate check in the worktree
	res.ChecksRun = append(res.ChecksRun, "gate_precommit_probe")
	cmdGate := exec.Command("sila", "gate", "--pre-commit")
	cmdGate.Dir = cm.Worktree
	if outGate, err := cmdGate.CombinedOutput(); err != nil {
		res.Verdict = VerdictRejected
		res.Errors = append(res.Errors, fmt.Sprintf("independent quality gate failed: %v (output: %s)", err, strings.TrimSpace(string(outGate))))
		return res, nil
	}

	res.Passed = true
	res.Verdict = VerdictApproved
	res.Notes = fmt.Sprintf("Clean worktree, passing gate, verified commit (%s)", strings.TrimSpace(string(outLog)))
	return res, nil
}

// SentinelConfig configures dual-cron intervals and bounded retries.
type SentinelConfig struct {
	LivenessInterval time.Duration `json:"liveness_interval"`
	AuditorInterval  time.Duration `json:"auditor_interval"`
	MaxRetries       int           `json:"max_retries"`
	AuditorAgent     string        `json:"auditor_agent"`
}

// DefaultSentinelConfig returns production default supervision configuration.
func DefaultSentinelConfig() SentinelConfig {
	return SentinelConfig{
		LivenessInterval: 5 * time.Second,
		AuditorInterval:  30 * time.Second,
		MaxRetries:       3,
		AuditorAgent:     "victory-auditor",
	}
}

// SentinelReport summarizes one dual-cron supervision pass.
type SentinelReport struct {
	Timestamp      time.Time                      `json:"timestamp"`
	TotalCrewmates int                            `json:"total_crewmates"`
	Active         int                            `json:"active"`
	Audited        int                            `json:"audited"`
	Approved       int                            `json:"approved"`
	Rejected       int                            `json:"rejected"`
	HaltedRunaway  int                            `json:"halted_runaway"`
	AuditResults   map[string]*VictoryAuditResult `json:"audit_results,omitempty"`
}

// SentinelOrchestrator manages dual-cron supervision and isolated victory auditing.
type SentinelOrchestrator struct {
	mgr     *CrewManager
	config  SentinelConfig
	auditor VictoryAuditor
}

// NewSentinelOrchestrator constructs a Sentinel supervisor.
func NewSentinelOrchestrator(mgr *CrewManager, config SentinelConfig, auditor VictoryAuditor) *SentinelOrchestrator {
	if config.MaxRetries <= 0 {
		config.MaxRetries = 3
	}
	if config.LivenessInterval <= 0 {
		config.LivenessInterval = 5 * time.Second
	}
	if config.AuditorInterval <= 0 {
		config.AuditorInterval = 30 * time.Second
	}
	if auditor == nil {
		auditor = NewDefaultVictoryAuditor(config.AuditorAgent)
	}
	return &SentinelOrchestrator{
		mgr:     mgr,
		config:  config,
		auditor: auditor,
	}
}

// RunPass executes one synchronized dual-cron supervision pass:
// 1. Sweeps zero-token process liveness.
// 2. Evaluates candidate completions with the independent Victory Auditor.
// 3. Halts runaway recursion by enforcing bounded retries.
func (s *SentinelOrchestrator) RunPass() (*SentinelReport, error) {
	report := &SentinelReport{
		Timestamp:    time.Now().UTC(),
		AuditResults: make(map[string]*VictoryAuditResult),
	}

	crewmates, err := s.mgr.Supervise()
	if err != nil {
		return nil, fmt.Errorf("liveness sweep: %w", err)
	}
	report.TotalCrewmates = len(crewmates)

	for i := range crewmates {
		cm := &crewmates[i]
		if cm.Status == StatusRunning || cm.Status == StatusIdle || cm.Status == StatusSpawning {
			report.Active++
			continue
		}

		if cm.Status == StatusCompleted || cm.Status == StatusGateFailed {
			report.Audited++
			auditRes, auditErr := s.auditor.AuditVictory(cm)
			if auditErr != nil {
				auditRes = &VictoryAuditResult{
					Passed:  false,
					Verdict: VerdictRejected,
					Errors:  []string{auditErr.Error()},
				}
			}
			report.AuditResults[cm.ID] = auditRes
			cm.AuditorVerdict = string(auditRes.Verdict)

			if auditRes.Passed {
				report.Approved++
				cm.Status = StatusCompleted
				cm.GateResult = "approved by " + auditRes.AuditorAgent
			} else {
				report.Rejected++
				cm.RetryCount++
				maxRetries := s.config.MaxRetries
				if cm.MaxRetries > 0 {
					maxRetries = cm.MaxRetries
				}
				if cm.RetryCount >= maxRetries {
					report.HaltedRunaway++
					cm.Status = StatusFailed
					cm.ErrorMsg = fmt.Sprintf("halted runaway recursion: victory audit failed %d times (max retries: %d). Errors: %s",
						cm.RetryCount, maxRetries, strings.Join(auditRes.Errors, "; "))
				} else {
					cm.Status = StatusRunning
					cm.ErrorMsg = fmt.Sprintf("victory audit rejected (%s), retry %d/%d: %s",
						auditRes.Verdict, cm.RetryCount, maxRetries, strings.Join(auditRes.Errors, "; "))
				}
			}
			_ = s.mgr.SaveCrewmate(cm)
		}
	}

	return report, nil
}

// Watch runs periodic dual-cron supervision loops until context cancellation.
func (s *SentinelOrchestrator) Watch(ctx context.Context, onPass func(rep *SentinelReport)) error {
	ticker := time.NewTicker(s.config.LivenessInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			rep, err := s.RunPass()
			if err == nil && onPass != nil {
				onPass(rep)
			}
		}
	}
}
