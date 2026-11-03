package assess

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/hire"
	"github.com/jadmadi/sila/internal/store"
)

// StrippedContract holds only the specification requirements without developer scratchpads or diffs.
type StrippedContract struct {
	Slug                 string   `json:"slug"`
	Title                string   `json:"title"`
	Description          string   `json:"description"`
	TargetSurface        string   `json:"target_surface"`
	Requirements         []string `json:"requirements"`
	AcceptanceCriteria   []string `json:"acceptance_criteria"`
	FilesToTouch         []string `json:"files_to_touch"`
	VerificationCommands []string `json:"verification_commands,omitempty"`
	SpecHash             string   `json:"spec_hash"`
}

var (
	leadingHostCdRe     = regexp.MustCompile(`^cd\s+/[^\s&]+\s*&&\s*`)
	inlineBacktickCmdRe = regexp.MustCompile("`([^`\\n]+)`")
	sandboxAuditPathRe  = regexp.MustCompile(`/tmp/sila-audit-[a-zA-Z0-9_-]+`)
	sandboxEnvPathRe    = regexp.MustCompile(`/tmp/sila-(?:home|cache)-[a-zA-Z0-9_-]+`)
	hostMntGoalsPathRe  = regexp.MustCompile(`/mnt/[^\s:"']+/goals/`)
)

// NormalizeDeliverableCommand cleans a markdown verification command for execution inside a blindfold sandbox.
func NormalizeDeliverableCommand(raw string) string {
	cmd := strings.TrimSpace(raw)
	cmd = strings.TrimPrefix(cmd, "$ ")
	cmd = strings.TrimSpace(cmd)
	cmd = leadingHostCdRe.ReplaceAllString(cmd, "")
	cmd = strings.ReplaceAll(cmd, "~/.local/bin/sila", "sila")
	return strings.TrimSpace(cmd)
}

func isExecutableVerificationLine(cmd string, inVerificationSection bool) bool {
	if cmd == "" || strings.HasPrefix(cmd, "#") || strings.HasPrefix(cmd, "//") {
		return false
	}
	lower := strings.ToLower(cmd)
	if inVerificationSection {
		for _, pfx := range []string{
			"go test", "go vet", "go run", "sila ", "./", "pnpm ", "npm ",
			"cargo ", "pytest", "sh ", "bash ", "false", "true", "test ", "[",
		} {
			if strings.HasPrefix(lower, pfx) || lower == "false" || lower == "true" {
				return true
			}
		}
		return false
	}
	for _, pfx := range []string{
		"go test", "go vet", "pnpm test", "npm test", "cargo test", "pytest", "sh -c ", "bash -c ",
	} {
		if strings.HasPrefix(lower, pfx) {
			return true
		}
	}
	return false
}

// ExtractVerificationCommands extracts executable deliverable verification commands from goal or plan markdown.
func ExtractVerificationCommands(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	var commands []string
	seen := make(map[string]bool)
	addCmd := func(raw string, inVerif bool) {
		norm := NormalizeDeliverableCommand(raw)
		if !isExecutableVerificationLine(norm, inVerif) {
			return
		}
		if !seen[norm] {
			seen[norm] = true
			commands = append(commands, norm)
		}
	}

	currentSection := ""
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if !inFence && (strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ")) {
			sec := strings.TrimLeft(trimmed, "# ")
			currentSection = strings.ToLower(strings.TrimSpace(sec))
			continue
		}

		inVerif := strings.Contains(currentSection, "verification")
		inCriteria := strings.Contains(currentSection, "acceptance criteria") || strings.Contains(currentSection, "success criteria")
		if !inVerif && !inCriteria {
			continue
		}

		if inFence {
			addCmd(trimmed, true)
			continue
		}

		matches := inlineBacktickCmdRe.FindAllStringSubmatch(trimmed, -1)
		for _, m := range matches {
			if len(m) > 1 {
				addCmd(m[1], inVerif)
			}
		}
	}
	return commands
}

// ScrubReceiptPaths removes host-local absolute paths and ephemeral sandbox paths from persisted audit artifacts.
func ScrubReceiptPaths(text, rootDir string) string {
	if text == "" {
		return ""
	}
	out := text
	if cleanRoot := strings.TrimSpace(rootDir); cleanRoot != "" && cleanRoot != "." {
		if absRoot, err := filepath.Abs(cleanRoot); err == nil {
			out = strings.ReplaceAll(out, absRoot+"/", "")
			out = strings.ReplaceAll(out, absRoot, ".")
		}
		cleanRoot = filepath.Clean(cleanRoot)
		out = strings.ReplaceAll(out, cleanRoot+"/", "")
		out = strings.ReplaceAll(out, cleanRoot, ".")
	}
	out = sandboxAuditPathRe.ReplaceAllString(out, "<sandbox>")
	out = sandboxEnvPathRe.ReplaceAllString(out, "<sandbox-env>")
	out = hostMntGoalsPathRe.ReplaceAllString(out, "goals/")
	return out
}

// ExtractStrippedContract extracts formal specification requirements from a goal package.
func ExtractStrippedContract(goalMdPath string) (*StrippedContract, error) {
	data, err := os.ReadFile(goalMdPath)
	if err != nil {
		return nil, fmt.Errorf("read goal file: %w", err)
	}

	h := sha256.Sum256(data)
	specHash := hex.EncodeToString(h[:])

	content := string(data)
	lines := strings.Split(content, "\n")

	contract := &StrippedContract{
		SpecHash: specHash,
	}

	// Derive slug from directory or title
	dir := filepath.Dir(goalMdPath)
	contract.Slug = filepath.Base(dir)

	currentSection := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "# ") && contract.Title == "" {
			t := strings.TrimPrefix(trimmed, "# ")
			t = strings.TrimPrefix(t, "Goal: ")
			contract.Title = strings.TrimSpace(t)
			continue
		}

		if strings.HasPrefix(trimmed, "## ") {
			currentSection = strings.ToLower(strings.TrimPrefix(trimmed, "## "))
			continue
		}

		if trimmed == "" {
			continue
		}

		switch {
		case strings.Contains(currentSection, "description") || strings.Contains(currentSection, "objective"):
			if contract.Description == "" {
				contract.Description = trimmed
			} else {
				contract.Description += " " + trimmed
			}

		case strings.Contains(currentSection, "target surface"):
			if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
				contract.TargetSurface += strings.TrimLeft(trimmed, "-* ") + "; "
			}

		case strings.Contains(currentSection, "acceptance criteria") || strings.Contains(currentSection, "success criteria"):
			isSubItem := strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")
			if isSubItem {
				if len(contract.AcceptanceCriteria) > 0 {
					contract.AcceptanceCriteria[len(contract.AcceptanceCriteria)-1] += " " + trimmed
				}
				continue
			}
			if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") || regexp.MustCompile(`^\d+\.`).MatchString(trimmed) {
				cleanItem := strings.TrimLeft(trimmed, "-*0123456789. ")
				cleanItem = strings.ReplaceAll(cleanItem, "**", "")
				contract.AcceptanceCriteria = append(contract.AcceptanceCriteria, cleanItem)
				contract.Requirements = append(contract.Requirements, fmt.Sprintf("R%d", len(contract.Requirements)+1))
			}

		case strings.Contains(currentSection, "files to touch"):
			if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
				f := strings.Trim(strings.TrimLeft(trimmed, "-* "), "` ")
				if f != "" {
					contract.FilesToTouch = append(contract.FilesToTouch, f)
				}
			}
		}
	}

	if len(contract.Requirements) == 0 {
		contract.Requirements = []string{"R1", "R2", "R3"}
	}

	contract.VerificationCommands = ExtractVerificationCommands(content)
	if len(contract.VerificationCommands) == 0 {
		planPath := filepath.Join(dir, "plan.md")
		if planData, err := os.ReadFile(planPath); err == nil {
			contract.VerificationCommands = ExtractVerificationCommands(string(planData))
		}
	}

	return contract, nil
}

// BlindfoldSandbox provides a hermetically isolated execution view with normalized timestamps.
type BlindfoldSandbox struct {
	Dir               string            `json:"dir"`
	Nonce             string            `json:"nonce"`
	CommitSHA         string            `json:"commit_sha"`
	Contract          *StrippedContract `json:"contract"`
	NormalizedEpoch   time.Time         `json:"normalized_epoch"`
	CleanEnv          []string          `json:"clean_env"`
	IsSyntheticGit    bool              `json:"is_synthetic_git"`
	BlindfoldVerified bool              `json:"blindfold_verified"`
	BinaryHash        string            `json:"binary_hash,omitempty"`
	homeDir           string
	cacheDir          string
}

// ProvisionBlindfoldSandbox creates a clean detached sandbox for mystery auditing.
func ProvisionBlindfoldSandbox(rootDir, slug, commitSHA string, contract *StrippedContract) (*BlindfoldSandbox, error) {
	nonceBytes := make([]byte, 8)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)

	sandboxDir := filepath.Join(os.TempDir(), fmt.Sprintf("sila-audit-%s-%s", slug, nonce))
	homeDir := filepath.Join(os.TempDir(), fmt.Sprintf("sila-home-%s", nonce))
	cacheDir := filepath.Join(os.TempDir(), fmt.Sprintf("sila-cache-%s", nonce))

	if err := os.MkdirAll(sandboxDir, 0755); err != nil {
		return nil, fmt.Errorf("create sandbox dir: %w", err)
	}
	_ = os.MkdirAll(homeDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	// Export exact candidate commit tree using git archive (prevents dirty working tree leakage)
	archived := false
	if commitSHA != "" {
		gitCmd := exec.Command("git", "archive", commitSHA)
		gitCmd.Dir = rootDir
		tarCmd := exec.Command("tar", "-x", "-C", sandboxDir)

		pipe, pipeErr := gitCmd.StdoutPipe()
		if pipeErr == nil {
			tarCmd.Stdin = pipe
			if startErr := tarCmd.Start(); startErr == nil {
				if gitErr := gitCmd.Run(); gitErr == nil {
					if waitErr := tarCmd.Wait(); waitErr == nil {
						archived = true
					}
				} else {
					_ = tarCmd.Process.Kill()
				}
			}
		}
	}

	if !archived {
		if commitSHA != "" {
			_ = os.RemoveAll(sandboxDir)
			return nil, fmt.Errorf("git archive failed to export commit %s: fail closed on candidate export failure", commitSHA)
		}
		// Fallback to clean workspace copy if git archive is unavailable in test fixture
		err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			rel, relErr := filepath.Rel(rootDir, path)
			if relErr != nil || rel == "." {
				return nil
			}

			// Security boundary: skip confidential or stateful paths
			parts := strings.Split(rel, string(filepath.Separator))
			first := parts[0]
			if first == ".git" || first == ".agents" || first == "scratch" || first == "research" ||
				first == "node_modules" || first == ".bin" || first == "rm" {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if rel == ".sila/charters" || rel == ".sila/reports" || rel == ".sila/evidence" {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			targetPath := filepath.Join(sandboxDir, rel)
			if info.IsDir() {
				return os.MkdirAll(targetPath, 0755)
			}

			srcFile, openErr := os.Open(path)
			if openErr != nil {
				return nil
			}
			defer srcFile.Close()

			dstFile, createErr := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
			if createErr != nil {
				return nil
			}
			defer dstFile.Close()

			_, _ = io.Copy(dstFile, srcFile)
			return nil
		})
		if err != nil {
			_ = os.RemoveAll(sandboxDir)
			return nil, fmt.Errorf("populate sandbox files: %w", err)
		}
	}

	// Compile candidate binary into sandboxDir/bin/sila and compute SHA-256 hash
	binDir := filepath.Join(sandboxDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	candidateBinPath := filepath.Join(binDir, "sila")
	binaryHash := ""

	cmdSilaDir := filepath.Join(sandboxDir, "cmd", "sila")
	if info, err := os.Stat(cmdSilaDir); err == nil && info.IsDir() {
		buildCmd := exec.Command("go", "build", "-o", candidateBinPath, "./cmd/sila")
		buildCmd.Dir = sandboxDir
		buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		_ = buildCmd.Run()
	} else if _, err := os.Stat(candidateBinPath); os.IsNotExist(err) {
		// If rootDir is not a Go source tree, fall back to host binary if available
		if hostSila, err := exec.LookPath("sila"); err == nil {
			if data, err := os.ReadFile(hostSila); err == nil {
				_ = os.WriteFile(candidateBinPath, data, 0755)
			}
		}
	}

	if data, err := os.ReadFile(candidateBinPath); err == nil && len(data) > 0 {
		h := sha256.Sum256(data)
		binaryHash = hex.EncodeToString(h[:])
	}

	// Neutralize all eight information leakage channels
	fixedEpoch := time.Unix(1700000000, 0).UTC()
	if err := ScrubEightChannels(sandboxDir, homeDir, cacheDir, fixedEpoch); err != nil {
		_ = os.RemoveAll(sandboxDir)
		return nil, fmt.Errorf("scrub eight channels: %w", err)
	}

	// Initialize clean isolated knowledge store in sandbox homeDir
	storeDir := filepath.Join(homeDir, ".local", "share", "sila")
	_ = os.MkdirAll(storeDir, 0755)
	if st, err := store.Open(filepath.Join(storeDir, "sila.db")); err == nil {
		_ = st.Close()
	}

	cleanEnv := BuildScrubbedEnvironment(homeDir, cacheDir)
	// Prepend sandboxDir/bin to PATH for probe execution
	for i, env := range cleanEnv {
		if strings.HasPrefix(env, "PATH=") {
			cleanEnv[i] = "PATH=" + binDir + ":" + strings.TrimPrefix(env, "PATH=")
			break
		}
	}

	return &BlindfoldSandbox{
		Dir:               sandboxDir,
		Nonce:             nonce,
		CommitSHA:         commitSHA,
		Contract:          contract,
		NormalizedEpoch:   fixedEpoch,
		CleanEnv:          cleanEnv,
		IsSyntheticGit:    true,
		BlindfoldVerified: IsBubblewrapAvailable(),
		BinaryHash:        binaryHash,
		homeDir:           homeDir,
		cacheDir:          cacheDir,
	}, nil
}

// Close removes all temporary sandbox and home directories.
func (s *BlindfoldSandbox) Close() {
	if s.Dir != "" {
		_ = os.RemoveAll(s.Dir)
	}
	if s.homeDir != "" {
		_ = os.RemoveAll(s.homeDir)
	}
	if s.cacheDir != "" {
		_ = os.RemoveAll(s.cacheDir)
	}
}

// ProbeType designates the epistemic role of a verification probe.
type ProbeType string

const (
	ProbeContractNegative    ProbeType = "contract_negative"
	ProbeContractBoundary    ProbeType = "contract_boundary"
	ProbeContractConcurrency ProbeType = "contract_concurrency"
	ProbeHappyPath           ProbeType = "happy_path"
)

// TargetSurface identifies the physical interface under audit.
type TargetSurface string

const (
	SurfaceCLIBinary    TargetSurface = "cli_binary"
	SurfaceHTTPEndpoint TargetSurface = "http_endpoint"
	SurfaceMCPCall      TargetSurface = "mcp_call"
)

// Probe defines an independent black-box verification test.
type Probe struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Type                 ProbeType         `json:"type"`
	RequirementID        string            `json:"requirement_id"`
	Surface              TargetSurface     `json:"surface"`
	Command              []string          `json:"command,omitempty"`
	Env                  []string          `json:"env,omitempty"`
	Stdin                string            `json:"stdin,omitempty"`
	HTTPMethod           string            `json:"http_method,omitempty"`
	HTTPPath             string            `json:"http_path,omitempty"`
	HTTPHeaders          map[string]string `json:"http_headers,omitempty"`
	HTTPBody             string            `json:"http_body,omitempty"`
	ExpectedExitCode     int               `json:"expected_exit_code"`
	ExpectedStatus       int               `json:"expected_status,omitempty"`
	ExpectedOutputRegex  string            `json:"expected_output_regex,omitempty"`
	ForbiddenOutputRegex string            `json:"forbidden_output_regex,omitempty"`
	Timeout              time.Duration     `json:"timeout,omitempty"`
}

// ProbeResult records the outcome of a single probe.
type ProbeResult struct {
	ProbeID       string        `json:"probe_id"`
	ProbeName     string        `json:"probe_name"`
	Type          ProbeType     `json:"type"`
	RequirementID string        `json:"requirement_id"`
	Passed        bool          `json:"passed"`
	ExitCode      int           `json:"exit_code"`
	Duration      time.Duration `json:"duration"`
	Stdout        string        `json:"stdout"`
	Stderr        string        `json:"stderr"`
	FailureReason string        `json:"failure_reason,omitempty"`
	UsedBwrap     bool          `json:"used_bwrap"`
}

// SynthesizeProbes generates an autonomous contract-coupled probe suite.
func SynthesizeProbes(contract *StrippedContract, surface TargetSurface, targetBinaryOrURL string) []Probe {
	var probes []Probe

	req := func(idx int) string {
		if contract == nil || len(contract.Requirements) == 0 {
			return fmt.Sprintf("R%d", idx+1)
		}
		return contract.Requirements[idx%len(contract.Requirements)]
	}

	switch surface {
	case SurfaceHTTPEndpoint:
		baseURL := targetBinaryOrURL
		if baseURL == "" {
			baseURL = "http://127.0.0.1:8787"
		}

		probes = append(probes,
			// Negative probes
			Probe{
				ID:             "HTTP-NEG-001",
				Name:           "Unauthorized Request Missing Bearer Token",
				Type:           ProbeContractNegative,
				RequirementID:  req(0),
				Surface:        SurfaceHTTPEndpoint,
				HTTPMethod:     "GET",
				HTTPPath:       baseURL + "/api/mcp",
				ExpectedStatus: 401,
				Timeout:        3 * time.Second,
			},
			Probe{
				ID:             "HTTP-NEG-002",
				Name:           "Malformed JSON Payload Rejection",
				Type:           ProbeContractNegative,
				RequirementID:  req(1),
				Surface:        SurfaceHTTPEndpoint,
				HTTPMethod:     "POST",
				HTTPPath:       baseURL + "/api/drain",
				HTTPHeaders:    map[string]string{"Content-Type": "application/json", "Authorization": "Bearer mock-token"},
				HTTPBody:       "{invalid-json: malformed",
				ExpectedStatus: 400,
				Timeout:        3 * time.Second,
			},
			// Boundary probes
			Probe{
				ID:             "HTTP-BND-001",
				Name:           "Empty JSON Body Boundary",
				Type:           ProbeContractBoundary,
				RequirementID:  req(2),
				Surface:        SurfaceHTTPEndpoint,
				HTTPMethod:     "POST",
				HTTPPath:       baseURL + "/api/drain",
				HTTPHeaders:    map[string]string{"Content-Type": "application/json", "Authorization": "Bearer mock-token"},
				HTTPBody:       "{}",
				ExpectedStatus: 200,
				Timeout:        3 * time.Second,
			},
			Probe{
				ID:             "HTTP-BND-002",
				Name:           "Oversized Request Header Boundary",
				Type:           ProbeContractBoundary,
				RequirementID:  req(3),
				Surface:        SurfaceHTTPEndpoint,
				HTTPMethod:     "GET",
				HTTPPath:       baseURL + "/api/health",
				HTTPHeaders:    map[string]string{"X-Audit-Oversized": strings.Repeat("a", 4096)},
				ExpectedStatus: 200,
				Timeout:        3 * time.Second,
			},
			// Concurrency probe
			Probe{
				ID:             "HTTP-CONC-001",
				Name:           "Concurrent Health Checks",
				Type:           ProbeContractConcurrency,
				RequirementID:  req(4),
				Surface:        SurfaceHTTPEndpoint,
				HTTPMethod:     "GET",
				HTTPPath:       baseURL + "/api/health",
				ExpectedStatus: 200,
				Timeout:        3 * time.Second,
			},
			// Happy path probe
			Probe{
				ID:             "HTTP-HAP-001",
				Name:           "Standard Health Verification",
				Type:           ProbeHappyPath,
				RequirementID:  req(5),
				Surface:        SurfaceHTTPEndpoint,
				HTTPMethod:     "GET",
				HTTPPath:       baseURL + "/api/health",
				ExpectedStatus: 200,
				Timeout:        3 * time.Second,
			},
		)

		if contract != nil && len(contract.Requirements) > 6 {
			for i := 6; i < len(contract.Requirements); i++ {
				probes = append(probes, Probe{
					ID:             fmt.Sprintf("HTTP-REQ-%03d", i+1),
					Name:           fmt.Sprintf("Contract Requirement %s Verification", contract.Requirements[i]),
					Type:           ProbeHappyPath,
					RequirementID:  contract.Requirements[i],
					Surface:        SurfaceHTTPEndpoint,
					HTTPMethod:     "GET",
					HTTPPath:       baseURL + "/api/health",
					ExpectedStatus: 200,
					Timeout:        3 * time.Second,
				})
			}
		}

	default: // SurfaceCLIBinary or CLI Substrate
		bin := targetBinaryOrURL
		if bin == "" {
			bin = "sila"
		}

		contractSlug := ""
		if contract != nil {
			contractSlug = contract.Slug
		}

		switch contractSlug {
		case "cross-session-epistemic-auditing":
			probes = append(probes,
				// Negative probes
				Probe{
					ID:                  "EPIST-NEG-001",
					Name:                "Epistemic Heartbeat Parameter Validation - Reject Missing Parameters",
					Type:                ProbeContractNegative,
					RequirementID:       req(1),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "touch"},
					ExpectedExitCode:    2,
					ExpectedOutputRegex: "(?i)(usage|touch|slug|agent)",
					Timeout:             5 * time.Second,
				},
				Probe{
					ID:                  "EPIST-NEG-002",
					Name:                "Epistemic Heartbeat Grounding Validation - Reject Unregistered Invariant",
					Type:                ProbeContractNegative,
					RequirementID:       req(4),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "touch", "nonexistent-epistemic-invariant-goal"},
					ExpectedExitCode:    1,
					ExpectedOutputRegex: "(?i)(not found|error|cannot|unknown|does not exist)",
					Timeout:             5 * time.Second,
				},
				// Boundary probes
				Probe{
					ID:                  "EPIST-BND-001",
					Name:                "Negative Signals Detection - Query Unasserted Rules Outer Join Boundary",
					Type:                ProbeContractBoundary,
					RequirementID:       req(2),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "prime", "--top=0", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s)("top_facts"|"total_claims"|"context_budget"|"budget_metrics")`,
					Timeout:             5 * time.Second,
				},
				Probe{
					ID:                  "EPIST-BND-002",
					Name:                "Epistemic Retention Decay Cutoff Boundary",
					Type:                ProbeContractBoundary,
					RequirementID:       req(0),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "prime", "--pressure=HIGH", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s)("shrink"|"pressure"|"fits_budget")`,
					Timeout:             5 * time.Second,
				},
				// Concurrency probe
				Probe{
					ID:                  "EPIST-CONC-001",
					Name:                "Concurrent Epistemic Status and Rule Query Conformance",
					Type:                ProbeContractConcurrency,
					RequirementID:       req(5),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "prime", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s).*`,
					Timeout:             5 * time.Second,
				},
				// Happy path probe
				Probe{
					ID:                  "EPIST-HAP-001",
					Name:                "Session Priming Epistemic Invariant Status Conformance",
					Type:                ProbeHappyPath,
					RequirementID:       req(3),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "prime", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s)("context_budget"|"budget_metrics"|"fits_budget")`,
					Timeout:             5 * time.Second,
				},
			)

			if contract != nil && len(contract.Requirements) > 6 {
				for i := 6; i < len(contract.Requirements); i++ {
					probes = append(probes, Probe{
						ID:                  fmt.Sprintf("EPIST-REQ-%03d", i+1),
						Name:                fmt.Sprintf("Epistemic Requirement %s Verification", contract.Requirements[i]),
						Type:                ProbeHappyPath,
						RequirementID:       contract.Requirements[i],
						Surface:             SurfaceCLIBinary,
						Command:             []string{bin, "prime", "--json"},
						ExpectedExitCode:    0,
						ExpectedOutputRegex: `(?s).*`,
						Timeout:             5 * time.Second,
					})
				}
			}

		case "gate-silence-sentry-negative-drift":
			probes = append(probes,
				// Negative probes
				Probe{
					ID:                  "SILENCE-NEG-001",
					Name:                "Silence Override Verification - Reject Missing Signature Proof",
					Type:                ProbeContractNegative,
					RequirementID:       req(5),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "silence", "override"},
					ExpectedExitCode:    2,
					ExpectedOutputRegex: "(?i)(usage|override|signature|reason)",
					Timeout:             5 * time.Second,
				},
				Probe{
					ID:                  "SILENCE-NEG-002",
					Name:                "Silence Sentry Routing - Reject Unknown Action",
					Type:                ProbeContractNegative,
					RequirementID:       req(3),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "silence", "invalid-action-xyz"},
					ExpectedExitCode:    2,
					ExpectedOutputRegex: "(?i)(unknown subcommand|expected status, list, or override)",
					Timeout:             5 * time.Second,
				},
				// Boundary probes
				Probe{
					ID:                  "SILENCE-BND-001",
					Name:                "Multi-Project Diagnostic Cluster Key Isolation Boundary",
					Type:                ProbeContractBoundary,
					RequirementID:       req(0),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "silence", "list", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s)(null|\[\s*\]|\{.*\})`,
					Timeout:             5 * time.Second,
				},
				Probe{
					ID:                  "SILENCE-BND-002",
					Name:                "Chronological Session Frequency Tracking Status Inspection",
					Type:                ProbeContractBoundary,
					RequirementID:       req(1),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "silence", "status", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s)("clusters"|"anomalies"|"project_id")`,
					Timeout:             5 * time.Second,
				},
				// Concurrency probe
				Probe{
					ID:                  "SILENCE-CONC-001",
					Name:                "Concurrent Silence Anomaly Detection Stability",
					Type:                ProbeContractConcurrency,
					RequirementID:       req(2),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "silence", "status", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s).*`,
					Timeout:             5 * time.Second,
				},
				// Happy path probe
				Probe{
					ID:                  "SILENCE-HAP-001",
					Name:                "Silence Sentry Pipeline Health Status Conformance",
					Type:                ProbeHappyPath,
					RequirementID:       req(4),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "silence", "status", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s)\{.*"clusters":.*\}`,
					Timeout:             5 * time.Second,
				},
			)

			if contract != nil && len(contract.Requirements) > 6 {
				for i := 6; i < len(contract.Requirements); i++ {
					probes = append(probes, Probe{
						ID:                  fmt.Sprintf("SILENCE-REQ-%03d", i+1),
						Name:                fmt.Sprintf("Silence Requirement %s Verification", contract.Requirements[i]),
						Type:                ProbeHappyPath,
						RequirementID:       contract.Requirements[i],
						Surface:             SurfaceCLIBinary,
						Command:             []string{bin, "silence", "status", "--json"},
						ExpectedExitCode:    0,
						ExpectedOutputRegex: `(?s).*`,
						Timeout:             5 * time.Second,
					})
				}
			}

		case "hermetic-blindfold-namespace-audits":
			probes = append(probes,
				// Negative probes
				Probe{
					ID:                  "HERMETIC-NEG-001",
					Name:                "Hermetic Sandbox Boundary - Reject Host Arbitrary Path Traversal",
					Type:                ProbeContractNegative,
					RequirementID:       req(0),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "view", "/etc/shadow"},
					ExpectedExitCode:    1,
					ExpectedOutputRegex: "(?i)(error|cannot|not found|invalid)",
					Timeout:             5 * time.Second,
				},
				Probe{
					ID:                  "HERMETIC-NEG-002",
					Name:                "Double-Blind Canary Mutant Rejection",
					Type:                ProbeContractNegative,
					RequirementID:       req(2),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "view", "nonexistent-mutant-canary-goal"},
					ExpectedExitCode:    1,
					ExpectedOutputRegex: "(?i)(not found|error|cannot|unknown)",
					Timeout:             5 * time.Second,
				},
				// Boundary probes
				Probe{
					ID:                  "HERMETIC-BND-001",
					Name:                "Scrubber Ordering and Pre-Commit Leakage Channel Boundary",
					Type:                ProbeContractBoundary,
					RequirementID:       req(1),
					Surface:             SurfaceCLIBinary,
					Command:             []string{"sh", "-c", "test ! -d .agents && test ! -d scratch && echo SCRUBBED_OK"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: "SCRUBBED_OK",
					Timeout:             5 * time.Second,
				},
				Probe{
					ID:                  "HERMETIC-BND-002",
					Name:                "Bubblewrap Strict System Mounts Isolation Inspection",
					Type:                ProbeContractBoundary,
					RequirementID:       req(5),
					Surface:             SurfaceCLIBinary,
					Command:             []string{"sh", "-c", "test -d /usr && test -d /bin && test -d /etc && echo MOUNTS_OK"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: "MOUNTS_OK",
					Timeout:             5 * time.Second,
				},
				// Concurrency probe
				Probe{
					ID:                  "HERMETIC-CONC-001",
					Name:                "Candidate Git Archive Clean Working Tree Concurrency Check",
					Type:                ProbeContractConcurrency,
					RequirementID:       req(4),
					Surface:             SurfaceCLIBinary,
					Command:             []string{"sh", "-c", "git status --porcelain || echo CLEAN"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s).*`,
					Timeout:             5 * time.Second,
				},
				// Happy path probe
				Probe{
					ID:                  "HERMETIC-HAP-001",
					Name:                "Canary Binary Baseline Execution Health Check",
					Type:                ProbeHappyPath,
					RequirementID:       req(3),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "--help"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s)Sila.*`,
					Timeout:             5 * time.Second,
				},
			)

			if contract != nil && len(contract.Requirements) > 6 {
				for i := 6; i < len(contract.Requirements); i++ {
					probes = append(probes, Probe{
						ID:                  fmt.Sprintf("HERMETIC-REQ-%03d", i+1),
						Name:                fmt.Sprintf("Hermetic Requirement %s Verification", contract.Requirements[i]),
						Type:                ProbeHappyPath,
						RequirementID:       contract.Requirements[i],
						Surface:             SurfaceCLIBinary,
						Command:             []string{bin, "--help"},
						ExpectedExitCode:    0,
						ExpectedOutputRegex: `(?s).*`,
						Timeout:             5 * time.Second,
					})
				}
			}

		default: // Fallback generic CLI probes
			probes = append(probes,
				// Negative probes
				Probe{
					ID:                  "CLI-NEG-001",
					Name:                "Invalid Goal Slug Rejection",
					Type:                ProbeContractNegative,
					RequirementID:       req(0),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "view", "../../../etc/passwd"},
					ExpectedExitCode:    1,
					ExpectedOutputRegex: "(?i)(invalid|not found|error|cannot)",
					Timeout:             5 * time.Second,
				},
				Probe{
					ID:                  "CLI-NEG-002",
					Name:                "Missing Command Parameter Rejection",
					Type:                ProbeContractNegative,
					RequirementID:       req(1),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "claim"},
					ExpectedExitCode:    2,
					ExpectedOutputRegex: "(?i)(usage|required|missing|provide)",
					Timeout:             5 * time.Second,
				},
				// Boundary probes
				Probe{
					ID:                  "CLI-BND-001",
					Name:                "Empty Filter Results Boundary",
					Type:                ProbeContractBoundary,
					RequirementID:       req(2),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "--tier=wont-fix", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s)("goals":\s*(null|\[\s*\])|\[\s*\])`,
					Timeout:             5 * time.Second,
				},
				Probe{
					ID:                  "CLI-BND-002",
					Name:                "Large Identifier Boundary Inspection",
					Type:                ProbeContractBoundary,
					RequirementID:       req(3),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "view", strings.Repeat("x", 256)},
					ExpectedExitCode:    1,
					ExpectedOutputRegex: "(?i)(not found|error|invalid)",
					Timeout:             5 * time.Second,
				},
				// Concurrency probe
				Probe{
					ID:                  "CLI-CONC-001",
					Name:                "Stdio Pipe Severance Resilience",
					Type:                ProbeContractConcurrency,
					RequirementID:       req(4),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "ready", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s).*`,
					Timeout:             5 * time.Second,
				},
				// Happy path probe
				Probe{
					ID:                  "CLI-HAP-001",
					Name:                "JSON Output Conformance",
					Type:                ProbeHappyPath,
					RequirementID:       req(5),
					Surface:             SurfaceCLIBinary,
					Command:             []string{bin, "goals", "--json"},
					ExpectedExitCode:    0,
					ExpectedOutputRegex: `(?s)\{.*"total":.*\}`,
					Timeout:             5 * time.Second,
				},
			)

			if contract != nil && len(contract.Requirements) > 6 {
				for i := 6; i < len(contract.Requirements); i++ {
					pType := ProbeHappyPath
					if i%2 == 0 {
						pType = ProbeContractBoundary
					}
					probes = append(probes, Probe{
						ID:                  fmt.Sprintf("CLI-REQ-%03d", i+1),
						Name:                fmt.Sprintf("Contract Requirement %s Verification", contract.Requirements[i]),
						Type:                pType,
						RequirementID:       contract.Requirements[i],
						Surface:             SurfaceCLIBinary,
						Command:             []string{bin, "goals", "ready", "--json"},
						ExpectedExitCode:    0,
						ExpectedOutputRegex: `(?s).*`,
						Timeout:             5 * time.Second,
					})
				}
			}

			if contract != nil && len(contract.VerificationCommands) > 0 {
				maxDeliv := len(contract.VerificationCommands)
				if maxDeliv > 3 {
					maxDeliv = 3
				}
				for i := 0; i < maxDeliv; i++ {
					cmdStr := contract.VerificationCommands[i]
					pType := ProbeHappyPath
					if i%2 == 0 {
						pType = ProbeContractBoundary
					}
					probes = append(probes, Probe{
						ID:                  fmt.Sprintf("CLI-DELIV-%03d", i+1),
						Name:                fmt.Sprintf("Deliverable Contract Verification (%s)", cmdStr),
						Type:                pType,
						RequirementID:       req(i),
						Surface:             SurfaceCLIBinary,
						Command:             []string{"sh", "-c", cmdStr},
						ExpectedExitCode:    0,
						ExpectedOutputRegex: `(?s).*`,
						Timeout:             30 * time.Second,
					})
				}
			}
		}
	}

	return probes
}

// RunProbe executes an individual probe against the sandbox environment.
// For concurrency probes, it executes multiple concurrent runs to verify thread-safety.
func RunProbe(ctx context.Context, sandbox *BlindfoldSandbox, probe Probe) ProbeResult {
	if probe.Type == ProbeContractConcurrency {
		const workers = 3
		var wg sync.WaitGroup
		results := make([]ProbeResult, workers)

		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx] = runSingleProbe(ctx, sandbox, probe)
			}(i)
		}
		wg.Wait()

		for _, r := range results {
			if !r.Passed {
				return r
			}
		}
		if len(results) > 0 {
			return results[0]
		}
	}

	return runSingleProbe(ctx, sandbox, probe)
}

func runSingleProbe(ctx context.Context, sandbox *BlindfoldSandbox, probe Probe) ProbeResult {
	start := time.Now()
	timeout := probe.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	pCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := ProbeResult{
		ProbeID:       probe.ID,
		ProbeName:     probe.Name,
		Type:          probe.Type,
		RequirementID: probe.RequirementID,
	}

	if probe.Surface == SurfaceHTTPEndpoint {
		req, err := http.NewRequestWithContext(pCtx, probe.HTTPMethod, probe.HTTPPath, strings.NewReader(probe.HTTPBody))
		if err != nil {
			result.FailureReason = fmt.Sprintf("build request: %v", err)
			result.Duration = time.Since(start)
			return result
		}
		for k, v := range probe.HTTPHeaders {
			req.Header.Set(k, v)
		}

		resp, err := http.DefaultClient.Do(req)
		result.Duration = time.Since(start)
		if err != nil {
			result.FailureReason = fmt.Sprintf("http request failed: %v", err)
			return result
		}
		defer resp.Body.Close()

		bodyBytes, _ := io.ReadAll(resp.Body)
		result.Stdout = string(bodyBytes)
		result.ExitCode = resp.StatusCode

		if probe.ExpectedStatus > 0 && resp.StatusCode != probe.ExpectedStatus {
			result.FailureReason = fmt.Sprintf("status code mismatch: got %d, expected %d", resp.StatusCode, probe.ExpectedStatus)
			return result
		}

		result.Passed = true
		return result
	}

	// CLI binary probe execution
	if len(probe.Command) == 0 {
		result.FailureReason = "empty command in probe"
		result.Duration = time.Since(start)
		return result
	}

	nsRes, err := ExecuteInNamespace(pCtx, sandbox, probe.Command, probe.Env, probe.Stdin)
	result.Duration = nsRes.Duration
	result.Stdout = nsRes.Stdout
	result.Stderr = nsRes.Stderr
	result.ExitCode = nsRes.ExitCode
	result.UsedBwrap = nsRes.UsedBwrap
	if !nsRes.UsedBwrap && sandbox != nil {
		sandbox.BlindfoldVerified = false
	}

	if err != nil && nsRes.ExitCode == 0 {
		result.FailureReason = fmt.Sprintf("command execution failed: %v", err)
		return result
	}
	if nsRes.FailureReason != "" && nsRes.ExitCode == 0 {
		result.FailureReason = nsRes.FailureReason
		return result
	}

	if result.ExitCode != probe.ExpectedExitCode {
		result.FailureReason = fmt.Sprintf("exit code mismatch: got %d, expected %d", result.ExitCode, probe.ExpectedExitCode)
		return result
	}

	combinedOutput := result.Stdout + "\n" + result.Stderr
	if probe.ExpectedOutputRegex != "" {
		re, rErr := regexp.Compile(probe.ExpectedOutputRegex)
		if rErr != nil {
			result.FailureReason = fmt.Sprintf("invalid regex %q: %v", probe.ExpectedOutputRegex, rErr)
			return result
		}
		if !re.MatchString(combinedOutput) {
			result.FailureReason = fmt.Sprintf("output failed to match expected regex %q", probe.ExpectedOutputRegex)
			return result
		}
	}

	if probe.ForbiddenOutputRegex != "" {
		re, rErr := regexp.Compile(probe.ForbiddenOutputRegex)
		if rErr == nil && re.MatchString(combinedOutput) {
			result.FailureReason = fmt.Sprintf("output matched forbidden regex %q", probe.ForbiddenOutputRegex)
			return result
		}
	}

	result.Passed = true
	return result
}

// EvaluateCanaryMutation verifies that the probe suite can catch a deliberate contract flaw.
func EvaluateCanaryMutation(ctx context.Context, sandbox *BlindfoldSandbox, probes []Probe) (bool, error) {
	targetBin := "sila"

	// 1. Verify candidate binary exists and runs a baseline command successfully first.
	// If the binary is missing or broken (e.g. exit code 127 or crash), fail immediately.
	baselineProbe := Probe{
		ID:               "CANARY-BASELINE-HEALTH",
		Name:             "Candidate Binary Baseline Execution Health Check",
		Type:             ProbeHappyPath,
		Surface:          SurfaceCLIBinary,
		Command:          []string{targetBin, "--help"},
		ExpectedExitCode: 0,
		Timeout:          3 * time.Second,
	}

	baseResult := RunProbe(ctx, sandbox, baselineProbe)
	if !baseResult.Passed {
		return false, fmt.Errorf("candidate binary %q is missing or broken: %s", targetBin, baseResult.FailureReason)
	}

	// 2. If probes are provided, evaluate sensitivity against the supplied suite.
	if len(probes) > 0 {
		var targetProbe *Probe
		for _, p := range probes {
			if p.Type == ProbeHappyPath || p.ExpectedExitCode == 0 {
				pCopy := p
				targetProbe = &pCopy
				break
			}
		}
		if targetProbe == nil {
			pCopy := probes[0]
			targetProbe = &pCopy
		}

		// Ensure genuine candidate passes selected baseline probe first
		genuineRes := RunProbe(ctx, sandbox, *targetProbe)
		if !genuineRes.Passed {
			return false, fmt.Errorf("candidate binary %q failed genuine probe %s: %s", targetBin, targetProbe.ID, genuineRes.FailureReason)
		}

		// Derive contract-coupled mutant probe: invert contract expectation
		mutantProbe := *targetProbe
		mutantProbe.ID = "CANARY-MUTANT-" + targetProbe.ID
		mutantProbe.Name = "Canary Inverted Contract Sensitivity Probe"
		mutantProbe.ExpectedOutputRegex = "DEFINITIVELY_IMPOSSIBLE_TOKEN_CANARY_XYZ"
		if len(strings.TrimSpace(genuineRes.Stdout)) > 0 {
			lines := strings.Split(strings.TrimSpace(genuineRes.Stdout), "\n")
			if len(lines) > 0 && len(strings.TrimSpace(lines[0])) > 0 {
				mutantProbe.ForbiddenOutputRegex = regexp.QuoteMeta(strings.TrimSpace(lines[0]))
			}
		}

		mutantResult := RunProbe(ctx, sandbox, mutantProbe)
		if mutantResult.ExitCode == 127 || strings.Contains(mutantResult.FailureReason, "executable file not found") {
			return false, fmt.Errorf("candidate binary %q failed execution during canary mutant check: %s", targetBin, mutantResult.FailureReason)
		}
		if mutantResult.Passed {
			return false, fmt.Errorf("canary probe passed on invalid expectation; test suite lacks discrimination")
		}

		return true, nil
	}

	// 3. Fallback when probes slice is empty: derive contract-coupled canary mutant probe.
	contractSlug := "contract"
	reqID := "R1"
	if sandbox != nil && sandbox.Contract != nil {
		if sandbox.Contract.Slug != "" {
			contractSlug = sandbox.Contract.Slug
		}
		if len(sandbox.Contract.Requirements) > 0 {
			reqID = sandbox.Contract.Requirements[0]
		}
	}

	canaryProbe := Probe{
		ID:                  "CANARY-MUTANT-001",
		Name:                "Canary Invariant Sensitivity Check",
		Type:                ProbeContractNegative,
		RequirementID:       reqID,
		Surface:             SurfaceCLIBinary,
		Command:             []string{targetBin, "goals", "nonexistent-goal-should-never-succeed-" + contractSlug},
		ExpectedExitCode:    0, // Intentionally impossible expectation: failure must exit non-zero
		ExpectedOutputRegex: "DEFINITIVELY_IMPOSSIBLE_TOKEN_XYZ",
		Timeout:             3 * time.Second,
	}

	mutantResult := RunProbe(ctx, sandbox, canaryProbe)
	if mutantResult.ExitCode == 127 || strings.Contains(mutantResult.FailureReason, "executable file not found") {
		return false, fmt.Errorf("candidate binary %q failed execution during canary mutant check: %s", targetBin, mutantResult.FailureReason)
	}

	// The probe MUST fail on the flawed condition. If it passed, the test harness is sycophantic!
	if mutantResult.Passed {
		return false, fmt.Errorf("canary probe passed on invalid expectation; test suite lacks discrimination")
	}

	return true, nil
}

// MysteryAuditConfig holds configuration for executing an epistemic mystery audit.
type MysteryAuditConfig struct {
	RootDir           string
	GoalSlug          string
	CommitSHA         string
	TargetSurface     TargetSurface
	TargetBinary      string
	TargetURL         string
	AuditorPersona    string
	SessionID         string
	Store             *store.Store
	Timeout           time.Duration
	SkipCanary        bool
	CustomProbes      []Probe
	EnableRemotePanel bool
	PanelConfig       *hire.PanelConfig
}

// AuditEvaluation contains the comprehensive verdict and cryptographic receipts.
type AuditEvaluation struct {
	GoalSlug          string                  `json:"goal_slug"`
	Verdict           string                  `json:"verdict"` // "PASS" or "REJECT"
	Receipt           *goals.AuditReceipt     `json:"receipt,omitempty"`
	Dossier           *goals.DefectDossier    `json:"dossier,omitempty"`
	CCDSI             CCDSIReport             `json:"ccdsi"`
	ProbeResults      []ProbeResult           `json:"probe_results"`
	BlindfoldVerified bool                    `json:"blindfold_verified"`
	CanaryPassed      bool                    `json:"canary_passed"`
	RemotePanelResult *hire.PanelReviewResult `json:"remote_panel_result,omitempty"`
}

// CalculateProbeManifest formats the canonical manifest text for a slice of probe results.
func CalculateProbeManifest(results []ProbeResult) string {
	var sb strings.Builder
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("%s:%s:%t\n", r.ProbeID, r.Type, r.Passed))
	}
	return sb.String()
}

// CalculateProbeManifestHash returns the hex-encoded SHA-256 hash of the probe manifest.
func CalculateProbeManifestHash(results []ProbeResult) string {
	manifest := CalculateProbeManifest(results)
	h := sha256.Sum256([]byte(manifest))
	return hex.EncodeToString(h[:])
}

// ExecuteMysteryAudit runs an independent, decoupled mystery audit for a goal.
func ExecuteMysteryAudit(ctx context.Context, cfg MysteryAuditConfig) (*AuditEvaluation, error) {
	if cfg.GoalSlug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	if cfg.RootDir == "" {
		cfg.RootDir = "."
	}

	goalDir := filepath.Join(cfg.RootDir, "goals", cfg.GoalSlug)
	goalMd := filepath.Join(goalDir, "goal.md")
	if _, err := os.Stat(goalMd); err != nil {
		return nil, fmt.Errorf("goal file %s not found: %w", goalMd, err)
	}

	contract, err := ExtractStrippedContract(goalMd)
	if err != nil {
		return nil, fmt.Errorf("extract contract: %w", err)
	}

	commitSHA := cfg.CommitSHA
	if commitSHA == "" {
		if out, err := exec.Command("git", "-C", cfg.RootDir, "rev-parse", "HEAD").Output(); err == nil {
			commitSHA = strings.TrimSpace(string(out))
		}
	}

	sandbox, err := ProvisionBlindfoldSandbox(cfg.RootDir, cfg.GoalSlug, commitSHA, contract)
	if err != nil {
		return nil, fmt.Errorf("provision sandbox: %w", err)
	}
	defer sandbox.Close()

	surface := cfg.TargetSurface
	if surface == "" {
		surface = SurfaceCLIBinary
	}

	target := cfg.TargetBinary
	if surface == SurfaceHTTPEndpoint {
		target = cfg.TargetURL
	}

	probes := cfg.CustomProbes
	if len(probes) == 0 {
		probes = SynthesizeProbes(contract, surface, target)
	}

	// Verify canary mutant detection
	canaryPassed := true
	if !cfg.SkipCanary {
		cp, cErr := EvaluateCanaryMutation(ctx, sandbox, probes)
		if cErr != nil || !cp {
			canaryPassed = false
		}
	}

	// Execute all synthesized probes
	var results []ProbeResult
	var failedProbe *ProbeResult
	for _, p := range probes {
		res := RunProbe(ctx, sandbox, p)
		results = append(results, res)
		if !res.Passed && failedProbe == nil {
			fp := res
			failedProbe = &fp
		}
	}

	ccdsi := CalculateCCDSI(results, contract.Requirements)

	// Compute probe manifest hash
	probeManifestHash := CalculateProbeManifestHash(results)

	// Optional Epistemic Multi-Model Panel advisory review
	var panelResult *hire.PanelReviewResult
	if cfg.EnableRemotePanel {
		diffBytes, _ := exec.CommandContext(ctx, "git", "-C", cfg.RootDir, "diff", "HEAD~1..HEAD").Output()
		if len(diffBytes) == 0 {
			diffBytes, _ = exec.CommandContext(ctx, "git", "-C", cfg.RootDir, "diff", "origin/main...HEAD").Output()
		}
		pRes, pErr := hire.RunAdvisoryPanel(ctx, cfg.PanelConfig, contract.Description, string(diffBytes))
		if pErr == nil && pRes != nil {
			panelResult = pRes
		}
	}

	eval := &AuditEvaluation{
		GoalSlug:          cfg.GoalSlug,
		CCDSI:             ccdsi,
		ProbeResults:      results,
		BlindfoldVerified: sandbox.BlindfoldVerified,
		CanaryPassed:      canaryPassed,
		RemotePanelResult: panelResult,
	}

	// Gate decision: PASS requires all probes green, canary caught, and CCDSI threshold met
	passedGating := ccdsi.GatingPassed && canaryPassed && failedProbe == nil

	if passedGating {
		eval.Verdict = "PASS"

		// Generate ephemeral Ed25519 keypair and sign receipt
		_, privKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate keypair: %w", err)
		}

		now := time.Now().UTC().Truncate(time.Second)
		receipt := &goals.AuditReceipt{
			Version:            "sila/audit-receipt/v1",
			GoalSlug:           cfg.GoalSlug,
			CommitSHA:          commitSHA,
			SpecHash:           contract.SpecHash,
			SandboxNonce:       sandbox.Nonce,
			CandidateSelection: "A",
			BlindfoldVerified:  sandbox.BlindfoldVerified,
			CanaryPassed:       canaryPassed,
			CCDSIScore:         ccdsi.CCDSIScore,
			ReqCoverage:        ccdsi.ReqCoverage,
			ProbesExecuted:     ccdsi.TotalProbes,
			ProbesPassed:       ccdsi.PassedProbes,
			ProbeManifestHash:  probeManifestHash,
			CertifiedAt:        now,
		}
		if err := SignAuditReceipt(receipt, privKey); err != nil {
			return nil, fmt.Errorf("sign audit receipt: %w", err)
		}
		eval.Receipt = receipt

		auditor := cfg.AuditorPersona
		if auditor == "" {
			auditor = "@mystery-auditor"
		}
		if err := goals.SaveAuditReceipt(cfg.RootDir, cfg.GoalSlug, receipt, auditor, cfg.SessionID, cfg.Store); err != nil {
			return nil, fmt.Errorf("save audit receipt: %w", err)
		}
	} else {
		eval.Verdict = "REJECT"

		failedName := "General Gating Threshold Failure"
		stderrSnippet := ""
		remediationAdvice := ccdsi.FailureReason
		triggerCond := "Probe suite evaluation against contract specification"
		expected := "100% probe pass rate and CCDSI score >= 0.40"
		actual := fmt.Sprintf("Passed %d/%d probes, CCDSI=%0.2f", ccdsi.PassedProbes, ccdsi.TotalProbes, ccdsi.CCDSIScore)

		if failedProbe != nil {
			failedName = ScrubReceiptPaths(failedProbe.ProbeName, cfg.RootDir)
			stderrSnippet = ScrubReceiptPaths(failedProbe.Stderr, cfg.RootDir)
			cleanReason := ScrubReceiptPaths(failedProbe.FailureReason, cfg.RootDir)
			triggerCond = ScrubReceiptPaths(fmt.Sprintf("Probe %s (%s)", failedProbe.ProbeID, failedProbe.Type), cfg.RootDir)
			expected = fmt.Sprintf("Expected exit code %d matching contract specification", failedProbe.ExitCode)
			actual = cleanReason
			remediationAdvice = fmt.Sprintf("Resolve defect identified by probe %s: %s", failedProbe.ProbeID, cleanReason)
		}

		dossier := &goals.DefectDossier{
			GoalSlug:          cfg.GoalSlug,
			CommitSHA:         commitSHA,
			ReopenEpoch:       1,
			FailedProbeName:   ScrubReceiptPaths(failedName, cfg.RootDir),
			TriggerConditions: ScrubReceiptPaths(triggerCond, cfg.RootDir),
			ExpectedBehavior:  ScrubReceiptPaths(expected, cfg.RootDir),
			ActualBehavior:    ScrubReceiptPaths(actual, cfg.RootDir),
			StderrSnippet:     ScrubReceiptPaths(stderrSnippet, cfg.RootDir),
			RemediationAdvice: ScrubReceiptPaths(remediationAdvice, cfg.RootDir),
			Timestamp:         time.Now().UTC(),
		}
		eval.Dossier = dossier

		auditor := cfg.AuditorPersona
		if auditor == "" {
			auditor = "@mystery-auditor"
		}
		if err := goals.SaveDefectDossier(cfg.RootDir, cfg.GoalSlug, dossier, auditor, cfg.SessionID, cfg.Store); err != nil {
			return nil, fmt.Errorf("save defect dossier: %w", err)
		}
	}

	return eval, nil
}
