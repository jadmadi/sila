package goals

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ContractResult captures structured execution metrics and evidence from running a VerificationContract.
type ContractResult struct {
	GoalSlug      string `json:"goal_slug"`
	Passed        bool   `json:"passed"`
	ExitCode      int    `json:"exit_code"`
	DurationMs    int64  `json:"duration_ms"`
	OutputSnippet string `json:"output_snippet"`
	FailureReason string `json:"failure_reason,omitempty"`
	TestsPassed   int    `json:"tests_passed,omitempty"`
	TestsTotal    int    `json:"tests_total,omitempty"`
}

var goTestPassRE = regexp.MustCompile(`(?m)^--- PASS: (\S+)`)
var goTestFailRE = regexp.MustCompile(`(?m)^--- FAIL: (\S+)`)

// RunVerificationContract executes a goal's immutable verification contract in an isolated subprocess.
// It enforces cryptographic integrity before execution, enforces context timeout deadlines,
// and extracts structured execution metrics and tail output.
func RunVerificationContract(ctx context.Context, rootDir, slug string) (*ContractResult, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	cleanRoot := resolveGoalsRootDir(rootDir)

	// 1. Load contract
	contract, err := LoadContract(cleanRoot, slug)
	if err != nil {
		return nil, fmt.Errorf("load contract: %w", err)
	}

	// 2. Cryptographic Integrity Guard: Ensure test files or command have not been tampered with
	if err := VerifyContractIntegrity(cleanRoot, contract); err != nil {
		return &ContractResult{
			GoalSlug:      slug,
			Passed:        false,
			ExitCode:      -1,
			FailureReason: err.Error(),
			OutputSnippet: err.Error(),
		}, err
	}

	// 3. Execution deadline with context
	timeout := time.Duration(contract.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(DefaultContractTimeout) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 4. Construct isolated subprocess
	cmd := exec.CommandContext(runCtx, "bash", "-c", contract.Command)
	cmd.Dir = cleanRoot

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	output := buf.String()
	tail := tailLines(output, 100)

	// 5. Parse test metrics if output is from go test or similar
	passes := len(goTestPassRE.FindAllString(output, -1))
	fails := len(goTestFailRE.FindAllString(output, -1))
	total := passes + fails

	res := &ContractResult{
		GoalSlug:      slug,
		ExitCode:      exitCode,
		DurationMs:    dur.Milliseconds(),
		OutputSnippet: tail,
		TestsPassed:   passes,
		TestsTotal:    total,
	}

	if runCtx.Err() == context.DeadlineExceeded {
		res.Passed = false
		res.FailureReason = fmt.Sprintf("verification contract execution timed out after %s", timeout)
		return res, nil
	}

	if exitCode == contract.ExpectedExitCode {
		res.Passed = true
	} else {
		res.Passed = false
		res.FailureReason = fmt.Sprintf("contract command exited with code %d (expected %d)", exitCode, contract.ExpectedExitCode)
	}

	return res, nil
}

// tailLines returns the last n lines of a multi-line string.
func tailLines(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
