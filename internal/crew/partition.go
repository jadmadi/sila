package crew

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	silaast "github.com/jadmadi/sila/internal/ast"
	"github.com/jadmadi/sila/internal/goals"
)

// ContractTouch represents an interface dependency where an upstream lane lands before a downstream lane.
type ContractTouch struct {
	UpstreamLane   int    `json:"upstream_lane"`
	DownstreamLane int    `json:"downstream_lane"`
	SharedPackage  string `json:"shared_package"`
}

// SameFileRisk tracks files concurrently touched by multiple candidate tasks.
type SameFileRisk struct {
	File  string `json:"file"`
	Lanes []int  `json:"lanes"`
}

// LaneConflict describes a hard mutation collision between lanes.
type LaneConflict struct {
	LaneA   int    `json:"lane_a"`
	LaneB   int    `json:"lane_b"`
	Package string `json:"package"`
	Reason  string `json:"reason"`
}

// Lane defines an isolated execution partition for a subagent.
type Lane struct {
	Lane         int      `json:"lane"`
	Role         string   `json:"role"`
	Packages     []string `json:"packages"`
	AllowPaths   []string `json:"allow_paths"`
	DenyPaths    []string `json:"deny_paths,omitempty"`
	LandingOrder int      `json:"landing_order"`
	Tasks        []string `json:"tasks,omitempty"`
}

// LanePlanResult is the output structure for sila crew plan-lanes.
type LanePlanResult struct {
	Status          string          `json:"status"` // "planned", "conflict", "error"
	RequestedLanes  int             `json:"requested_lanes"`
	PlannedLanes    int             `json:"planned_lanes"`
	Conflicts       []LaneConflict  `json:"conflicts"`
	SameFileRisks   []SameFileRisk  `json:"same_file_risks"`
	ContractTouches []ContractTouch `json:"contract_touches"`
	Lanes           []Lane          `json:"lanes"`
	ExitCode        int             `json:"exit_code"`
}

// TaskSpec describes a candidate task to be partitioned into a lane.
type TaskSpec struct {
	ID       string
	Packages []string
	Files    []string
}

// PackageDAG captures module dependencies across packages.
type PackageDAG struct {
	ModulePath string
	Imports    map[string][]string // pkg -> internal pkgs it imports (upstream dependencies)
	Dependents map[string][]string // pkg -> pkgs that import it (downstream dependents)
	Files      map[string][]string // pkg -> relative file paths
}

// BuildPackageDAG inspects all Go packages in repoRoot.
func BuildPackageDAG(repoRoot string) (*PackageDAG, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		absRoot = repoRoot
	}

	modPath, modRoot := silaast.ResolveModulePath(absRoot)
	relMod := ""
	if modRoot != "" {
		if rel, err := filepath.Rel(absRoot, modRoot); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			relMod = filepath.ToSlash(rel)
		}
	}

	dag := &PackageDAG{
		ModulePath: modPath,
		Imports:    make(map[string][]string),
		Dependents: make(map[string][]string),
		Files:      make(map[string][]string),
	}

	fset := token.NewFileSet()

	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "rm" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		relPath, err := filepath.Rel(absRoot, path)
		if err != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)

		pkgDir := filepath.ToSlash(filepath.Dir(relPath))
		if pkgDir == "" {
			pkgDir = "."
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		fileNode, err := parser.ParseFile(fset, relPath, src, parser.ImportsOnly)
		if err != nil {
			return nil
		}

		dag.Files[pkgDir] = append(dag.Files[pkgDir], relPath)

		for _, imp := range fileNode.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if modPath != "" && strings.HasPrefix(impPath, modPath) {
				cleanImp := strings.TrimPrefix(impPath, modPath)
				cleanImp = strings.TrimPrefix(cleanImp, "/")
				if cleanImp == "" {
					cleanImp = "."
				}
				if relMod != "" {
					cleanImp = filepath.ToSlash(filepath.Join(relMod, cleanImp))
				}
				dag.Imports[pkgDir] = append(dag.Imports[pkgDir], cleanImp)
				dag.Dependents[cleanImp] = append(dag.Dependents[cleanImp], pkgDir)
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("build package dag in %s: %w", repoRoot, err)
	}

	for p := range dag.Imports {
		dag.Imports[p] = dedupStrings(dag.Imports[p])
		sort.Strings(dag.Imports[p])
	}
	for p := range dag.Dependents {
		dag.Dependents[p] = dedupStrings(dag.Dependents[p])
		sort.Strings(dag.Dependents[p])
	}

	return dag, nil
}

// PlanLanes partitions candidate tasks into conflict-free execution lanes.
func PlanLanes(repoRoot string, requestedLanes int, briefPath string, taskGoals []string) (*LanePlanResult, error) {
	if requestedLanes <= 0 {
		requestedLanes = 2
	}

	dag, err := BuildPackageDAG(repoRoot)
	if err != nil {
		return nil, err
	}

	// 1. Gather task specifications
	tasks, err := loadTaskSpecs(repoRoot, briefPath, taskGoals, dag)
	if err != nil {
		return nil, err
	}

	res := &LanePlanResult{
		RequestedLanes:  requestedLanes,
		Conflicts:       []LaneConflict{},
		SameFileRisks:   []SameFileRisk{},
		ContractTouches: []ContractTouch{},
		Lanes:           []Lane{},
	}

	if len(tasks) == 0 {
		res.Status = "planned"
		res.PlannedLanes = 0
		res.ExitCode = 0
		return res, nil
	}

	// 2. Map tasks to lanes
	// Check for file overlaps across tasks
	fileToTasks := make(map[string][]string)
	for _, t := range tasks {
		for _, f := range t.Files {
			fileToTasks[f] = append(fileToTasks[f], t.ID)
		}
	}

	// Determine effective number of lanes
	numLanes := requestedLanes
	if len(tasks) < numLanes {
		numLanes = len(tasks)
	}
	if numLanes <= 0 {
		numLanes = 1
	}

	laneBuckets := make([][]TaskSpec, numLanes)
	for i, t := range tasks {
		targetLaneIdx := i % numLanes
		laneBuckets[targetLaneIdx] = append(laneBuckets[targetLaneIdx], t)
	}

	// 3. Assemble lanes
	laneIndex := 1
	for _, bucket := range laneBuckets {
		if len(bucket) == 0 {
			continue
		}

		var pkgs []string
		var allowPaths []string
		var taskIDs []string
		roleName := "worker"

		for _, t := range bucket {
			taskIDs = append(taskIDs, t.ID)
			pkgs = append(pkgs, t.Packages...)
			for _, p := range t.Packages {
				allowPaths = append(allowPaths, p+"/**")
			}
			for _, f := range t.Files {
				allowPaths = append(allowPaths, f)
			}
		}

		pkgs = dedupStrings(pkgs)
		allowPaths = dedupStrings(allowPaths)
		sort.Strings(pkgs)
		sort.Strings(allowPaths)

		if len(pkgs) > 0 {
			cleanRole := filepath.Base(pkgs[0])
			cleanRole = strings.TrimPrefix(cleanRole, ".")
			if cleanRole != "" {
				roleName = "worker_" + cleanRole
			}
		}

		res.Lanes = append(res.Lanes, Lane{
			Lane:         laneIndex,
			Role:         roleName,
			Packages:     pkgs,
			AllowPaths:   allowPaths,
			LandingOrder: 1, // Default landing order; adjusted in topological pass
			Tasks:        taskIDs,
		})
		laneIndex++
	}

	res.PlannedLanes = len(res.Lanes)

	// Populate DenyPaths for each lane based on other lanes' packages
	for i := range res.Lanes {
		var denies []string
		for j := range res.Lanes {
			if i == j {
				continue
			}
			for _, p := range res.Lanes[j].Packages {
				denies = append(denies, p+"/**")
			}
		}
		res.Lanes[i].DenyPaths = dedupStrings(denies)
		sort.Strings(res.Lanes[i].DenyPaths)
	}

	// 4. Check contract touches and calculate topological landing order
	for i := 0; i < len(res.Lanes); i++ {
		for j := 0; j < len(res.Lanes); j++ {
			if i == j {
				continue
			}
			laneA := res.Lanes[i]
			laneB := res.Lanes[j]

			// If laneB imports a package from laneA, laneA is upstream of laneB
			for _, pkgB := range laneB.Packages {
				upstreamPkgs := dag.Imports[pkgB]
				for _, up := range upstreamPkgs {
					for _, pkgA := range laneA.Packages {
						if up == pkgA {
							res.ContractTouches = append(res.ContractTouches, ContractTouch{
								UpstreamLane:   laneA.Lane,
								DownstreamLane: laneB.Lane,
								SharedPackage:  pkgA,
							})
						}
					}
				}
			}
		}
	}

	// Dedup contract touches
	seenCT := make(map[string]bool)
	var dedupedCT []ContractTouch
	for _, ct := range res.ContractTouches {
		key := fmt.Sprintf("%d->%d:%s", ct.UpstreamLane, ct.DownstreamLane, ct.SharedPackage)
		if !seenCT[key] {
			seenCT[key] = true
			dedupedCT = append(dedupedCT, ct)
		}
	}
	res.ContractTouches = dedupedCT

	// Propagate topological landing order until stable
	for iter := 0; iter < len(res.Lanes); iter++ {
		changed := false
		for _, ct := range res.ContractTouches {
			upIdx := ct.UpstreamLane - 1
			downIdx := ct.DownstreamLane - 1
			if upIdx >= 0 && upIdx < len(res.Lanes) && downIdx >= 0 && downIdx < len(res.Lanes) {
				if res.Lanes[downIdx].LandingOrder <= res.Lanes[upIdx].LandingOrder {
					res.Lanes[downIdx].LandingOrder = res.Lanes[upIdx].LandingOrder + 1
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}

	// 5. Detect same-file risks
	for file, tIDs := range fileToTasks {
		if len(tIDs) > 1 {
			var touchingLanes []int
			for _, l := range res.Lanes {
				for _, tid := range tIDs {
					for _, ltid := range l.Tasks {
						if tid == ltid {
							touchingLanes = append(touchingLanes, l.Lane)
							break
						}
					}
				}
			}
			touchingLanes = dedupInts(touchingLanes)
			if len(touchingLanes) > 1 {
				res.SameFileRisks = append(res.SameFileRisks, SameFileRisk{
					File:  file,
					Lanes: touchingLanes,
				})
			}
		}
	}

	// 6. Detect hard conflicts (two parallel lanes modifying same package with same landing order)
	for i := 0; i < len(res.Lanes); i++ {
		for j := i + 1; j < len(res.Lanes); j++ {
			laneA := res.Lanes[i]
			laneB := res.Lanes[j]
			if laneA.LandingOrder == laneB.LandingOrder {
				for _, pa := range laneA.Packages {
					for _, pb := range laneB.Packages {
						if pa == pb {
							res.Conflicts = append(res.Conflicts, LaneConflict{
								LaneA:   laneA.Lane,
								LaneB:   laneB.Lane,
								Package: pa,
								Reason:  "simultaneous execution in same landing tier without dependency ordering",
							})
						}
					}
				}
			}
		}
	}

	if len(res.Conflicts) > 0 {
		res.Status = "conflict"
		res.ExitCode = 1
	} else {
		res.Status = "planned"
		res.ExitCode = 0
	}

	return res, nil
}

func loadTaskSpecs(repoRoot, briefPath string, taskGoals []string, dag *PackageDAG) ([]TaskSpec, error) {
	var specs []TaskSpec

	// Case 1: Brief file provided
	if briefPath != "" {
		data, err := os.ReadFile(briefPath)
		if err != nil {
			return nil, fmt.Errorf("read brief file %s: %w", briefPath, err)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		lineNum := 1
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				lineNum++
				continue
			}

			taskID := fmt.Sprintf("task_%d", lineNum)
			parts := strings.SplitN(line, ":", 2)
			pathListStr := line
			if len(parts) == 2 {
				taskID = strings.TrimSpace(parts[0])
				pathListStr = parts[1]
			}

			var pkgs []string
			var files []string
			rawPaths := strings.Split(pathListStr, ",")
			for _, rp := range rawPaths {
				p := filepath.ToSlash(strings.TrimSpace(rp))
				if strings.HasSuffix(p, ".go") {
					files = append(files, p)
					pkg := filepath.Dir(p)
					pkgs = append(pkgs, pkg)
				} else if p != "" {
					pkgs = append(pkgs, p)
				}
			}

			specs = append(specs, TaskSpec{
				ID:       taskID,
				Packages: dedupStrings(pkgs),
				Files:    dedupStrings(files),
			})
			lineNum++
		}
		return specs, nil
	}

	// Case 2: Specific roadmap goals requested (or "pending")
	if len(taskGoals) > 0 {
		auditRes, err := goals.ScanGoals(repoRoot)
		if err == nil && auditRes != nil {
			for _, g := range auditRes.Goals {
				match := false
				for _, tg := range taskGoals {
					if tg == "pending" && (g.Status == "pending" || g.Status == "claimed") {
						match = true
						break
					}
					if tg == g.Slug {
						match = true
						break
					}
				}
				if match {
					var pkgs []string
					for _, t := range g.FilesToTouch {
						p := filepath.ToSlash(t)
						if strings.HasSuffix(p, ".go") {
							pkgs = append(pkgs, filepath.Dir(p))
						} else {
							pkgs = append(pkgs, p)
						}
					}
					specs = append(specs, TaskSpec{
						ID:       g.Slug,
						Packages: dedupStrings(pkgs),
						Files:    g.FilesToTouch,
					})
				}
			}
		}
		if len(specs) > 0 {
			return specs, nil
		}
	}

	// Case 3: Automatic fallback: synthesize tasks from top-level repository packages
	var allPkgs []string
	for p := range dag.Files {
		allPkgs = append(allPkgs, p)
	}
	sort.Strings(allPkgs)

	for _, p := range allPkgs {
		specs = append(specs, TaskSpec{
			ID:       "pkg_" + filepath.Base(p),
			Packages: []string{p},
			Files:    dag.Files[p],
		})
	}

	return specs, nil
}

func dedupStrings(slice []string) []string {
	seen := make(map[string]bool, len(slice))
	var res []string
	for _, s := range slice {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			res = append(res, s)
		}
	}
	return res
}

func dedupInts(slice []int) []int {
	seen := make(map[int]bool, len(slice))
	var res []int
	for _, n := range slice {
		if !seen[n] {
			seen[n] = true
			res = append(res, n)
		}
	}
	sort.Ints(res)
	return res
}
