package assess

import (
	"fmt"
	"math"
	"strings"
)

// CCDSIReport records formal metrics for the Contract-Coupled Discriminative Sensitivity Index.
type CCDSIReport struct {
	TotalProbes       int     `json:"total_probes"`
	NegativeProbes    int     `json:"negative_probes"`
	BoundaryProbes    int     `json:"boundary_probes"`
	ConcurrencyProbes int     `json:"concurrency_probes"`
	PassedProbes      int     `json:"passed_probes"`
	CCDSIScore        float64 `json:"ccdsi_score"`
	ReqCoverage       float64 `json:"req_coverage"`
	GatingPassed      bool    `json:"gating_passed"`
	FailureReason     string  `json:"failure_reason,omitempty"`
}

// CalculateCCDSI computes discriminative sensitivity according to formal specification.
func CalculateCCDSI(results []ProbeResult, declaredReqs []string) CCDSIReport {
	report := CCDSIReport{
		TotalProbes: len(results),
	}

	declaredSet := make(map[string]bool, len(declaredReqs))
	for _, req := range declaredReqs {
		if tr := strings.TrimSpace(req); tr != "" {
			declaredSet[tr] = true
		}
	}

	coveredReqs := make(map[string]bool)
	allPassed := true
	var unknownReqs []string

	for _, res := range results {
		if res.Passed {
			report.PassedProbes++
		} else {
			allPassed = false
		}

		if res.RequirementID != "" {
			if declaredSet[res.RequirementID] {
				coveredReqs[res.RequirementID] = true
			} else {
				unknownReqs = append(unknownReqs, res.RequirementID)
			}
		}

		switch res.Type {
		case ProbeContractNegative:
			report.NegativeProbes++
		case ProbeContractBoundary:
			report.BoundaryProbes++
		case ProbeContractConcurrency:
			report.ConcurrencyProbes++
		}
	}

	if report.TotalProbes > 0 {
		report.CCDSIScore = float64(report.NegativeProbes+report.BoundaryProbes+report.ConcurrencyProbes) / float64(report.TotalProbes)
	}

	if len(declaredSet) == 0 {
		report.ReqCoverage = 0.0
	} else {
		coverage := float64(len(coveredReqs)) / float64(len(declaredSet))
		report.ReqCoverage = math.Min(1.0, math.Max(0.0, coverage))
	}

	// Evaluate strict verification invariants
	var failures []string
	if len(unknownReqs) > 0 {
		failures = append(failures, fmt.Sprintf("probes referenced undeclared requirement IDs: %s", strings.Join(unknownReqs, ", ")))
	}
	if report.TotalProbes < 6 {
		failures = append(failures, fmt.Sprintf("probe floor violated: %d executed (min 6)", report.TotalProbes))
	}
	if report.BoundaryProbes < 2 {
		failures = append(failures, fmt.Sprintf("boundary probes insufficient: %d (min 2)", report.BoundaryProbes))
	}
	if report.NegativeProbes < 2 {
		failures = append(failures, fmt.Sprintf("negative probes insufficient: %d (min 2)", report.NegativeProbes))
	}
	if report.CCDSIScore < 0.40 {
		failures = append(failures, fmt.Sprintf("CCDSI score %0.3f < 0.40", report.CCDSIScore))
	}
	if report.ReqCoverage < 0.50 {
		failures = append(failures, fmt.Sprintf("requirement coverage %0.3f < 0.50", report.ReqCoverage))
	}
	if !allPassed {
		failures = append(failures, fmt.Sprintf("not all probes passed: %d/%d passed", report.PassedProbes, report.TotalProbes))
	}

	if len(failures) == 0 {
		report.GatingPassed = true
	} else {
		report.GatingPassed = false
		report.FailureReason = strings.Join(failures, "; ")
	}

	return report
}
