package assess

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jadmadi/sila/internal/render"
)

// PackageCoverage stores statement coverage metrics for a single Go package.
type PackageCoverage struct {
	Package           string  `json:"package"`
	TotalStatements   int     `json:"total_statements"`
	CoveredStatements int     `json:"covered_statements"`
	CoveragePct       float64 `json:"coverage_pct"`
}

// CoverageProfile represents the aggregated coverage metrics parsed from a coverage profile.
type CoverageProfile struct {
	TotalStatements   int                         `json:"total_statements"`
	CoveredStatements int                         `json:"covered_statements"`
	OverallPct        float64                     `json:"overall_pct"`
	Packages          map[string]*PackageCoverage `json:"packages"`
}

// CoverageThresholds configures coverage gate assertions and minimum thresholds.
type CoverageThresholds struct {
	MinGlobalPct float64  // Minimum overall repository coverage percentage (e.g. 50.0 baseline, 80.0 strict)
	CoreMinPct   float64  // Minimum coverage percentage for core packages (e.g. 70.0 baseline, 90.0 strict)
	CorePackages []string // Package subpaths designated as core data engines
	Strict       bool     // If true, strictly enforces >= 80% global and >= 90% core thresholds
}

// DefaultCoverageThresholds returns recommended baseline thresholds for quality gates.
func DefaultCoverageThresholds() CoverageThresholds {
	return CoverageThresholds{
		MinGlobalPct: 50.0,
		CoreMinPct:   70.0,
		CorePackages: []string{"internal/store", "internal/loaders"},
		Strict:       false,
	}
}

// StrictCoverageThresholds returns aspirational quality standard thresholds (80% global, 90% core).
func StrictCoverageThresholds() CoverageThresholds {
	return CoverageThresholds{
		MinGlobalPct: 80.0,
		CoreMinPct:   90.0,
		CorePackages: []string{"internal/store", "internal/ingest", "internal/loaders"},
		Strict:       true,
	}
}

// CoverageReport contains the evaluation results of comparing a coverage profile against thresholds.
type CoverageReport struct {
	Clean          bool              `json:"clean"`
	OverallPct     float64           `json:"overall_pct"`
	MinGlobalPct   float64           `json:"min_global_pct"`
	GlobalPassed   bool              `json:"global_passed"`
	Violations     []string          `json:"violations"`
	CorePackages   []PackageCoverage `json:"core_packages"`
	PackageReports []PackageCoverage `json:"package_reports"`
}

// ParseCoverageProfile parses a standard Go coverage profile (e.g. coverage.out) from an io.Reader.
func ParseCoverageProfile(r io.Reader) (*CoverageProfile, error) {
	scanner := bufio.NewScanner(r)
	packages := make(map[string]*PackageCoverage)
	totalStmts := 0
	coveredStmts := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}

		// fields[0]: pkg/file.go:startLine.startCol,endLine.endCol
		// fields[1]: numStatements
		// fields[2]: count (times executed)
		colonIdx := strings.Index(fields[0], ":")
		if colonIdx < 0 {
			continue
		}
		filePath := fields[0][:colonIdx]
		pkgPath := filepath.Dir(filePath)

		numStmt, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}

		pkgCov, exists := packages[pkgPath]
		if !exists {
			pkgCov = &PackageCoverage{Package: pkgPath}
			packages[pkgPath] = pkgCov
		}

		pkgCov.TotalStatements += numStmt
		totalStmts += numStmt

		if count > 0 {
			pkgCov.CoveredStatements += numStmt
			coveredStmts += numStmt
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read coverage profile: %w", err)
	}

	overallPct := 0.0
	if totalStmts > 0 {
		overallPct = float64(coveredStmts) / float64(totalStmts) * 100.0
	}

	for _, p := range packages {
		if p.TotalStatements > 0 {
			p.CoveragePct = float64(p.CoveredStatements) / float64(p.TotalStatements) * 100.0
		}
	}

	return &CoverageProfile{
		TotalStatements:   totalStmts,
		CoveredStatements: coveredStmts,
		OverallPct:        overallPct,
		Packages:          packages,
	}, nil
}

// ParseCoverageFile reads and parses a coverage profile from the given filesystem path.
func ParseCoverageFile(path string) (*CoverageProfile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseCoverageProfile(f)
}

// AuditCoverage evaluates a CoverageProfile against specified CoverageThresholds.
func AuditCoverage(profile *CoverageProfile, thresholds CoverageThresholds) *CoverageReport {
	report := &CoverageReport{
		Clean:        true,
		OverallPct:   profile.OverallPct,
		MinGlobalPct: thresholds.MinGlobalPct,
		GlobalPassed: profile.OverallPct >= thresholds.MinGlobalPct,
		Violations:   []string{},
	}

	if !report.GlobalPassed {
		report.Clean = false
		report.Violations = append(report.Violations,
			fmt.Sprintf("overall coverage %.1f%% is below minimum required %.1f%%",
				profile.OverallPct, thresholds.MinGlobalPct))
	}

	// Check core package thresholds
	for _, corePkg := range thresholds.CorePackages {
		var matched *PackageCoverage
		for pkgName, pkgCov := range profile.Packages {
			if strings.HasSuffix(pkgName, corePkg) || strings.Contains(pkgName, corePkg) {
				matched = pkgCov
				break
			}
		}

		if matched != nil {
			report.CorePackages = append(report.CorePackages, *matched)
			if matched.CoveragePct < thresholds.CoreMinPct {
				report.Clean = false
				report.Violations = append(report.Violations,
					fmt.Sprintf("core package %s coverage %.1f%% is below required %.1f%%",
						corePkg, matched.CoveragePct, thresholds.CoreMinPct))
			}
		} else if thresholds.Strict {
			report.Clean = false
			report.Violations = append(report.Violations,
				fmt.Sprintf("core package %s not found in coverage profile", corePkg))
		}
	}

	// Collect and sort all package reports
	for _, pkgCov := range profile.Packages {
		report.PackageReports = append(report.PackageReports, *pkgCov)
	}
	sort.Slice(report.PackageReports, func(i, j int) bool {
		return report.PackageReports[i].Package < report.PackageReports[j].Package
	})

	return report
}

// RenderCoverageTOON renders a CoverageReport in compact Token-Oriented Object Notation.
func RenderCoverageTOON(report *CoverageReport) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"clean", fmt.Sprintf("%t", report.Clean)},
		{"overall_pct", fmt.Sprintf("%.1f%%", report.OverallPct)},
		{"min_global_pct", fmt.Sprintf("%.1f%%", report.MinGlobalPct)},
		{"global_passed", fmt.Sprintf("%t", report.GlobalPassed)},
		{"violations_count", fmt.Sprintf("%d", len(report.Violations))},
	}
	b.WriteString(render.FormatTOONObject("coverage_summary", summaryFields))

	if len(report.Violations) > 0 {
		var rows [][]string
		for _, v := range report.Violations {
			rows = append(rows, []string{v})
		}
		b.WriteString(render.FormatTOONTable("violations", []string{"violation"}, rows))
	}

	if len(report.PackageReports) > 0 {
		headers := []string{"package", "covered", "total", "pct"}
		var rows [][]string
		for _, p := range report.PackageReports {
			rows = append(rows, []string{
				p.Package,
				fmt.Sprintf("%d", p.CoveredStatements),
				fmt.Sprintf("%d", p.TotalStatements),
				fmt.Sprintf("%.1f%%", p.CoveragePct),
			})
		}
		b.WriteString(render.FormatTOONTable("packages", headers, rows))
	}

	return b.String()
}
