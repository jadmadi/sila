package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultContractTimeout is the default execution deadline for a verification contract.
const DefaultContractTimeout = 300 // seconds

// DefaultContractMaxRounds is the default circuit breaker iteration threshold.
const DefaultContractMaxRounds = 5

// VerificationContract defines an immutable, tamper-proof acceptance contract for a goal.
// It binds the verification command, expected exit code, execution timeout, and cryptographic
// SHA256 digest of both the command and referenced test files to prevent test weakening or
// false assertion triumphs.
type VerificationContract struct {
	Command          string    `json:"command"`              // e.g. "go test -race ./internal/store/..."
	TimeoutSeconds   int       `json:"timeout_seconds"`      // Execution deadline (default: 300)
	ExpectedExitCode int       `json:"expected_exit_code"`   // Typically 0
	ContractHash     string    `json:"contract_hash"`        // SHA256 of the verification command and test files
	MaxRounds        int       `json:"max_rounds"`           // Circuit breaker iteration limit (default: 5)
	TestPaths        []string  `json:"test_paths,omitempty"` // Explicit test files or directories bound into hash
	CreatedAt        time.Time `json:"created_at"`           // Registration timestamp
}

// ContractPath returns the canonical filesystem path for a goal's verification contract.
func ContractPath(rootDir, slug string) string {
	return filepath.Join(resolveGoalsRootDir(rootDir), "goals", slug, "contract.json")
}

// ComputeContractHash calculates a deterministic SHA-256 digest of the verification command
// and the contents of all bound test files. If no explicit testPaths are provided, it automatically
// scans for test files in the goal package or target directory.
func ComputeContractHash(rootDir, command string, testPaths []string) (string, error) {
	h := sha256.New()

	// 1. Hash normalized command
	cmdClean := strings.TrimSpace(command)
	if cmdClean == "" {
		return "", fmt.Errorf("verification command cannot be empty")
	}
	h.Write([]byte("CMD:" + cmdClean + "\n"))

	// 2. Resolve, sort, and hash test file paths and contents
	var resolvedFiles []string
	cleanRoot := resolveGoalsRootDir(rootDir)

	for _, tp := range testPaths {
		tpClean := strings.TrimSpace(tp)
		if tpClean == "" {
			continue
		}
		absPath := tpClean
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(cleanRoot, tpClean)
		}
		fi, err := os.Stat(absPath)
		if err != nil {
			// If explicitly specified path doesn't exist yet, hash path name as marker
			resolvedFiles = append(resolvedFiles, tpClean)
			continue
		}
		if fi.IsDir() {
			_ = filepath.Walk(absPath, func(path string, info os.FileInfo, walkErr error) error {
				if walkErr == nil && !info.IsDir() && strings.HasSuffix(info.Name(), "_test.go") {
					rel, rErr := filepath.Rel(cleanRoot, path)
					if rErr == nil {
						resolvedFiles = append(resolvedFiles, rel)
					} else {
						resolvedFiles = append(resolvedFiles, path)
					}
				}
				return nil
			})
		} else {
			rel, rErr := filepath.Rel(cleanRoot, absPath)
			if rErr == nil {
				resolvedFiles = append(resolvedFiles, rel)
			} else {
				resolvedFiles = append(resolvedFiles, absPath)
			}
		}
	}

	sort.Strings(resolvedFiles)

	for _, rf := range resolvedFiles {
		h.Write([]byte("FILE:" + filepath.ToSlash(rf) + "\n"))
		absPath := rf
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(cleanRoot, rf)
		}
		f, err := os.Open(absPath)
		if err == nil {
			_, _ = io.Copy(h, f)
			_ = f.Close()
		}
		h.Write([]byte("\n"))
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// CreateVerificationContract builds and hashes a new VerificationContract.
func CreateVerificationContract(rootDir, command string, timeoutSeconds, maxRounds, expectedExitCode int, testPaths []string) (*VerificationContract, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("verification command is required")
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = DefaultContractTimeout
	}
	if maxRounds <= 0 {
		maxRounds = DefaultContractMaxRounds
	}

	hash, err := ComputeContractHash(rootDir, command, testPaths)
	if err != nil {
		return nil, fmt.Errorf("compute contract hash: %w", err)
	}

	return &VerificationContract{
		Command:          command,
		TimeoutSeconds:   timeoutSeconds,
		ExpectedExitCode: expectedExitCode,
		ContractHash:     hash,
		MaxRounds:        maxRounds,
		TestPaths:        testPaths,
		CreatedAt:        time.Now().UTC(),
	}, nil
}

// VerifyContractIntegrity ensures that the contract has not been tampered with by recomputing
// its cryptographic hash against current disk state.
func VerifyContractIntegrity(rootDir string, c *VerificationContract) error {
	if c == nil {
		return fmt.Errorf("verification contract is nil")
	}
	if c.ContractHash == "" {
		return fmt.Errorf("verification contract has empty contract_hash")
	}

	computed, err := ComputeContractHash(rootDir, c.Command, c.TestPaths)
	if err != nil {
		return fmt.Errorf("failed to recompute contract hash: %w", err)
	}

	if computed != c.ContractHash {
		return fmt.Errorf("contract integrity violation: computed hash %s does not match registered contract hash %s (test assertion tampering or command modification detected)", computed, c.ContractHash)
	}
	return nil
}

// SaveContract writes the contract JSON to goals/<slug>/contract.json.
func SaveContract(rootDir, slug string, c *VerificationContract) error {
	if slug == "" {
		return fmt.Errorf("missing goal slug")
	}
	if c == nil {
		return fmt.Errorf("contract is nil")
	}
	path := ContractPath(rootDir, slug)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal contract: %w", err)
	}
	return AtomicWriteFile(path, data, 0644)
}

// LoadContract loads a verification contract for a goal, checking contract.json first,
// and falling back to embedded goal.md metadata if present.
func LoadContract(rootDir, slug string) (*VerificationContract, error) {
	if slug == "" {
		return nil, fmt.Errorf("missing goal slug")
	}
	path := ContractPath(rootDir, slug)
	if fileExists(path) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var c VerificationContract
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("parse contract.json: %w", err)
		}
		return &c, nil
	}

	// Fallback: parse from goal.md metadata if embedded
	goalMdPath := filepath.Join(resolveGoalsRootDir(rootDir), "goals", slug, "goal.md")
	if fileExists(goalMdPath) {
		data, err := os.ReadFile(goalMdPath)
		if err == nil {
			if c, parseErr := parseEmbeddedContract(string(data)); parseErr == nil && c != nil {
				return c, nil
			}
		}
	}

	return nil, fmt.Errorf("no verification contract found for goal %q (checked %s and goal.md)", slug, path)
}

// parseEmbeddedContract extracts a VerificationContract embedded in goal.md frontmatter or code blocks.
func parseEmbeddedContract(content string) (*VerificationContract, error) {
	// 1. Look for ```contract or ```json:contract block
	marker := "```json:contract"
	idx := strings.Index(content, marker)
	if idx == -1 {
		marker = "```contract"
		idx = strings.Index(content, marker)
	}
	if idx != -1 {
		rest := content[idx+len(marker):]
		endIdx := strings.Index(rest, "```")
		if endIdx != -1 {
			block := strings.TrimSpace(rest[:endIdx])
			var c VerificationContract
			if err := json.Unmarshal([]byte(block), &c); err == nil && c.Command != "" {
				return &c, nil
			}
		}
	}

	// 2. Look for YAML frontmatter contract fields
	if m := frontmatterRE.FindStringSubmatch(content); len(m) > 1 {
		fm := m[1]
		lines := strings.Split(fm, "\n")
		var cmd, hash string
		var timeout, maxRounds, exitCode int
		var inTestPaths bool
		var testPaths []string

		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if inTestPaths {
				if strings.HasPrefix(trimmed, "-") {
					testPaths = append(testPaths, strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
					continue
				} else if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
					inTestPaths = false
				}
			}

			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				k := strings.ToLower(strings.TrimSpace(parts[0]))
				v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
				switch k {
				case "contract_command", "verification_command":
					cmd = v
				case "contract_hash":
					hash = v
				case "contract_timeout", "timeout_seconds":
					fmt.Sscanf(v, "%d", &timeout)
				case "max_rounds":
					fmt.Sscanf(v, "%d", &maxRounds)
				case "expected_exit_code":
					fmt.Sscanf(v, "%d", &exitCode)
				case "contract_test_paths", "test_paths":
					inTestPaths = true
				}
			}
		}
		if cmd != "" {
			if timeout <= 0 {
				timeout = DefaultContractTimeout
			}
			if maxRounds <= 0 {
				maxRounds = DefaultContractMaxRounds
			}
			return &VerificationContract{
				Command:          cmd,
				TimeoutSeconds:   timeout,
				ExpectedExitCode: exitCode,
				ContractHash:     hash,
				MaxRounds:        maxRounds,
				TestPaths:        testPaths,
				CreatedAt:        time.Now().UTC(),
			}, nil
		}
	}

	return nil, fmt.Errorf("no embedded contract found")
}
