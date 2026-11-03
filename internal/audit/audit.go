package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/hire"
	"github.com/jadmadi/sila/internal/store"
)

// GoalAuditOptions holds parameters for auditing a goal via persona or mystery red team.
type GoalAuditOptions struct {
	GoalSlug          string
	ProjectDir        string
	PersonaID         string
	SessionID         string
	CommitSHA         string
	TargetURL         string
	TargetBinary      string
	Timeout           time.Duration
	Blindfold         bool
	SkipCanary        bool
	CustomProbes      []assess.Probe
	EnableRemotePanel bool
	PanelConfig       *hire.PanelConfig
}

// AuditGoal executes an epistemic mystery audit against a specified goal package.
func AuditGoal(ctx context.Context, st *store.Store, opts GoalAuditOptions) (*assess.AuditEvaluation, error) {
	if opts.GoalSlug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	if opts.ProjectDir == "" {
		opts.ProjectDir = "."
	}
	if opts.PersonaID == "" {
		opts.PersonaID = "mystery-auditor"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}

	surface := assess.SurfaceCLIBinary
	if opts.TargetURL != "" {
		surface = assess.SurfaceHTTPEndpoint
	}

	cfg := assess.MysteryAuditConfig{
		RootDir:           opts.ProjectDir,
		GoalSlug:          opts.GoalSlug,
		CommitSHA:         opts.CommitSHA,
		TargetSurface:     surface,
		TargetBinary:      opts.TargetBinary,
		TargetURL:         opts.TargetURL,
		AuditorPersona:    opts.PersonaID,
		SessionID:         opts.SessionID,
		Store:             st,
		Timeout:           opts.Timeout,
		SkipCanary:        opts.SkipCanary,
		CustomProbes:      opts.CustomProbes,
		EnableRemotePanel: opts.EnableRemotePanel,
		PanelConfig:       opts.PanelConfig,
	}

	eval, err := assess.ExecuteMysteryAudit(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Persist an audit report in .sila/reports/
	reportID := fmt.Sprintf("rep_audit_%s_%d", opts.GoalSlug, time.Now().UTC().Unix())
	status := "success"
	summary := fmt.Sprintf("Epistemic mystery audit passed for goal %s (CCDSI=%0.2f)", opts.GoalSlug, eval.CCDSI.CCDSIScore)
	if eval.Verdict != "PASS" {
		status = "critical"
		summary = fmt.Sprintf("Epistemic mystery audit rejected goal %s: %s", opts.GoalSlug, eval.CCDSI.FailureReason)
	}

	rep := &Report{
		ID:             reportID,
		AssociatedGoal: opts.GoalSlug,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		PersonaID:      opts.PersonaID,
		PersonaName:    "Epistemic Mystery Auditor",
		Domain:         "epistemic-audit",
		TargetURL:      opts.TargetURL,
		Status:         status,
		Phase:          3,
		Summary:        summary,
		SkillsUsed:     []string{"doubt-driven-development", "api-and-interface-design", "debugging-and-error-recovery"},
	}
	_ = SaveReport(opts.ProjectDir, rep)

	return eval, nil
}
