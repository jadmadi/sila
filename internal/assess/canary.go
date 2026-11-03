package assess

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CanaryMutant defines an intentional defect injected into a candidate build to test auditor sensitivity.
type CanaryMutant struct {
	ID                  string        `json:"id"`
	RequirementID       string        `json:"requirement_id"`
	Description         string        `json:"description"`
	Command             []string      `json:"command"`
	ExpectedExitCode    int           `json:"expected_exit_code"`
	ExpectedOutputRegex string        `json:"expected_output_regex"`
	Timeout             time.Duration `json:"timeout"`
}

// CanaryTrialResult records the outcome of a double-blind canary challenge.
type CanaryTrialResult struct {
	GenuineCandidatePassed bool    `json:"genuine_candidate_passed"`
	MutantCandidatePassed  bool    `json:"mutant_candidate_passed"`
	CanaryCaught           bool    `json:"canary_caught"`
	SycophancyDetected     bool    `json:"sycophancy_detected"`
	SensitivityScore       float64 `json:"sensitivity_score"`
	Details                string  `json:"details"`
}

// GenerateCanaryMutant creates an inverted contract probe targeting a declared requirement.
func GenerateCanaryMutant(contract *StrippedContract) *CanaryMutant {
	reqID := "R1"
	slug := "xyz"
	if contract != nil {
		if len(contract.Requirements) > 0 {
			reqID = contract.Requirements[0]
		}
		if contract.Slug != "" {
			slug = contract.Slug
		}
	}

	return &CanaryMutant{
		ID:                  "CANARY-MUTANT-001",
		RequirementID:       reqID,
		Description:         "Inverted invariant sensitivity check to expose sycophantic auditors",
		Command:             []string{"sila", "goals", "nonexistent-goal-should-never-succeed-" + slug},
		ExpectedExitCode:    0, // Intentionally impossible: failure exit must be non-zero
		ExpectedOutputRegex: "DEFINITIVELY_IMPOSSIBLE_TOKEN_CANARY_XYZ",
		Timeout:             3 * time.Second,
	}
}

// DispatchDoubleBlindTrial challenges the auditor with genuine and mutant candidates in unlinked trials.
func DispatchDoubleBlindTrial(ctx context.Context, sandbox *BlindfoldSandbox, contract *StrippedContract, genuineRunner func() (bool, error)) (*CanaryTrialResult, error) {
	// Trial A: evaluate genuine candidate
	genuinePassed, err := genuineRunner()
	if err != nil {
		return nil, fmt.Errorf("genuine candidate trial failed: %w", err)
	}

	// Verify candidate binary exists and runs baseline before testing mutant
	mutant := GenerateCanaryMutant(contract)
	targetBin := "sila"
	if len(mutant.Command) > 0 {
		targetBin = mutant.Command[0]
	}
	baselineProbe := Probe{
		ID:               "CANARY-TRIAL-BASELINE",
		Name:             "Candidate Binary Baseline Health",
		Type:             ProbeHappyPath,
		Surface:          SurfaceCLIBinary,
		Command:          []string{targetBin, "--help"},
		ExpectedExitCode: 0,
		Timeout:          3 * time.Second,
	}
	baseResult := RunProbe(ctx, sandbox, baselineProbe)
	if !baseResult.Passed {
		return nil, fmt.Errorf("candidate binary %q is missing or broken: %s", targetBin, baseResult.FailureReason)
	}

	// Trial B: evaluate canary mutant
	canaryProbe := Probe{
		ID:                  mutant.ID,
		Name:                mutant.Description,
		Type:                ProbeContractNegative,
		RequirementID:       mutant.RequirementID,
		Surface:             SurfaceCLIBinary,
		Command:             mutant.Command,
		ExpectedExitCode:    mutant.ExpectedExitCode,
		ExpectedOutputRegex: mutant.ExpectedOutputRegex,
		Timeout:             mutant.Timeout,
	}

	mutantResult := RunProbe(ctx, sandbox, canaryProbe)
	if mutantResult.ExitCode == 127 || strings.Contains(mutantResult.FailureReason, "executable file not found") {
		return nil, fmt.Errorf("candidate binary %q failed execution during canary mutant check: %s", targetBin, mutantResult.FailureReason)
	}
	mutantPassed := mutantResult.Passed

	// An epistemically sound auditor must FAIL on the mutant.
	canaryCaught := !mutantPassed
	sycophancy := mutantPassed

	sensitivityScore := 1.0
	details := "Canary mutant successfully discriminated; zero sycophancy detected."
	if sycophancy {
		sensitivityScore = 0.0
		details = "Sycophancy detected: auditor passed an impossible mutant canary."
	}

	return &CanaryTrialResult{
		GenuineCandidatePassed: genuinePassed,
		MutantCandidatePassed:  mutantPassed,
		CanaryCaught:           canaryCaught,
		SycophancyDetected:     sycophancy,
		SensitivityScore:       sensitivityScore,
		Details:                details,
	}, nil
}
