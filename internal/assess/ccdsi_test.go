package assess

import (
	"fmt"
	"testing"
)

func TestCCDSI_UndeclaredRequirementIDs(t *testing.T) {
	TestCalculateCCDSI_UndeclaredRequirementIDs(t)
}

func TestCalculateCCDSI_UndeclaredRequirementIDs(t *testing.T) {
	var results []ProbeResult
	for i := 0; i < 6; i++ {
		typ := ProbeContractNegative
		if i >= 2 {
			typ = ProbeContractBoundary
		}
		results = append(results, ProbeResult{
			ProbeID:       fmt.Sprintf("P-%d", i),
			Passed:        true,
			Type:          typ,
			RequirementID: fmt.Sprintf("UNDECLARED-%d", i),
		})
	}

	report := CalculateCCDSI(results, []string{"REAL-1", "REAL-2"})
	if report.ReqCoverage != 0.0 {
		t.Fatalf("expected ReqCoverage 0.0 for completely undeclared IDs, got %f", report.ReqCoverage)
	}
	if report.GatingPassed {
		t.Fatalf("expected GatingPassed=false for undeclared requirement IDs, got true")
	}
}

func TestCalculateCCDSI_ValidDeclaredRequirements(t *testing.T) {
	results := []ProbeResult{
		{ProbeID: "P-1", Passed: true, Type: ProbeContractNegative, RequirementID: "REAL-1"},
		{ProbeID: "P-2", Passed: true, Type: ProbeContractNegative, RequirementID: "REAL-1"},
		{ProbeID: "P-3", Passed: true, Type: ProbeContractBoundary, RequirementID: "REAL-2"},
		{ProbeID: "P-4", Passed: true, Type: ProbeContractBoundary, RequirementID: "REAL-2"},
		{ProbeID: "P-5", Passed: true, Type: ProbeContractConcurrency, RequirementID: "REAL-1"},
		{ProbeID: "P-6", Passed: true, Type: ProbeHappyPath, RequirementID: "REAL-2"},
	}

	report := CalculateCCDSI(results, []string{"REAL-1", "REAL-2"})
	if report.ReqCoverage != 1.0 {
		t.Fatalf("expected ReqCoverage 1.0, got %f", report.ReqCoverage)
	}
	if !report.GatingPassed {
		t.Fatalf("expected GatingPassed=true, got false: %s", report.FailureReason)
	}
}
