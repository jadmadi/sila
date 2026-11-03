package gate

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/assess"
)

const (
	PlaceholderAccountID  = "<CLOUDFLARE_ACCOUNT_ID>"
	PlaceholderDatabaseID = "00000000-0000-0000-0000-000000000000"
)

var (
	jsonAccountRegex = regexp.MustCompile(`("account_id"\s*:\s*)"[0-9a-fA-F]{32}"`)
	tomlAccountRegex = regexp.MustCompile(`(account_id\s*=\s*)"[0-9a-fA-F]{32}"`)

	jsonDatabaseRegex = regexp.MustCompile(`("database_id"\s*:\s*)"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"`)
	tomlDatabaseRegex = regexp.MustCompile(`(database_id\s*=\s*)"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"`)
)

// CFCleanViolation represents an unmasked Cloudflare account or database identifier.
type CFCleanViolation struct {
	File       string `json:"file"`
	LineNumber int    `json:"line_number"`
	Kind       string `json:"kind"`
	Line       string `json:"line"`
}

// CFCleanResult captures the outcome of Cloudflare credential sanitization checks.
type CFCleanResult struct {
	Clean        bool               `json:"clean"`
	Skipped      bool               `json:"skipped"`
	SkipReason   string             `json:"skip_reason,omitempty"`
	Violations   []CFCleanViolation `json:"violations,omitempty"`
	FilesScanned int                `json:"files_scanned"`
}

// IsWranglerFile checks if the path points to a Cloudflare Worker wrangler configuration file.
func IsWranglerFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == "wrangler.json" || base == "wrangler.jsonc" || base == "wrangler.toml" {
		return true
	}
	if strings.Contains(base, "wrangler") && (strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".jsonc") || strings.HasSuffix(base, ".toml")) {
		return true
	}
	return false
}

// DiscoverCFCredentials looks for known credentials in .env.local and .env files.
func DiscoverCFCredentials(repoRoot string) (accountID, databaseID string) {
	for _, envName := range []string{".env.local", ".env"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, envName))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
			if accountID == "" && (key == "CLOUDFLARE_ACCOUNT_ID" || key == "CF_ACCOUNT_ID") {
				accountID = val
			}
			if databaseID == "" && (key == "CLOUDFLARE_DATABASE_ID" || key == "CF_DATABASE_ID") {
				databaseID = val
			}
		}
	}
	if accountID == "" {
		if v := os.Getenv("CLOUDFLARE_ACCOUNT_ID"); v != "" {
			accountID = v
		} else if v := os.Getenv("CF_ACCOUNT_ID"); v != "" {
			accountID = v
		}
	}
	if databaseID == "" {
		if v := os.Getenv("CLOUDFLARE_DATABASE_ID"); v != "" {
			databaseID = v
		} else if v := os.Getenv("CF_DATABASE_ID"); v != "" {
			databaseID = v
		}
	}
	return accountID, databaseID
}

// ScanFileForCFCredentials scans a file's content for unmasked Cloudflare credentials.
func ScanFileForCFCredentials(filePath string, content []byte, knownAccountID, knownDatabaseID string) []CFCleanViolation {
	var violations []CFCleanViolation
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Check account ID
		if knownAccountID != "" && strings.Contains(trimmed, knownAccountID) && knownAccountID != PlaceholderAccountID {
			violations = append(violations, CFCleanViolation{
				File:       filePath,
				LineNumber: lineNum,
				Line:       trimmed,
				Kind:       "Known Cloudflare Account ID",
			})
		} else if (jsonAccountRegex.MatchString(trimmed) || tomlAccountRegex.MatchString(trimmed)) && !strings.Contains(trimmed, PlaceholderAccountID) {
			violations = append(violations, CFCleanViolation{
				File:       filePath,
				LineNumber: lineNum,
				Line:       trimmed,
				Kind:       "Unmasked 32-hex Account ID",
			})
		}

		// Check database ID
		if knownDatabaseID != "" && strings.Contains(trimmed, knownDatabaseID) && knownDatabaseID != PlaceholderDatabaseID {
			violations = append(violations, CFCleanViolation{
				File:       filePath,
				LineNumber: lineNum,
				Line:       trimmed,
				Kind:       "Known Cloudflare Database ID",
			})
		} else if (jsonDatabaseRegex.MatchString(trimmed) || tomlDatabaseRegex.MatchString(trimmed)) && !strings.Contains(trimmed, PlaceholderDatabaseID) {
			violations = append(violations, CFCleanViolation{
				File:       filePath,
				LineNumber: lineNum,
				Line:       trimmed,
				Kind:       "Unmasked 36-char Database UUID",
			})
		}
	}

	return violations
}

// AuditCFClean audits staged files or project wrangler configs for unmasked Cloudflare credentials.
func AuditCFClean(repoRoot string, stagedFiles []StagedFile) CFCleanResult {
	res := CFCleanResult{
		Clean:      true,
		Violations: make([]CFCleanViolation, 0),
	}

	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}

	if !assess.IsCFWorkerProject(repoRoot) && !assess.IsCloudflareWorkerProject(repoRoot) {
		res.Skipped = true
		res.SkipReason = "no Cloudflare Worker configuration detected"
		return res
	}

	knownAccountID, knownDatabaseID := DiscoverCFCredentials(repoRoot)

	// Determine files to scan
	var filesToScan []string
	if len(stagedFiles) > 0 {
		for _, sf := range stagedFiles {
			if sf.Status == "D" {
				continue
			}
			if IsWranglerFile(sf.Path) {
				filesToScan = append(filesToScan, sf.Path)
			}
		}
	} else {
		// If no staged files provided, walk project for wrangler manifests
		_ = filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				name := info.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist" || name == "build" || name == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if IsWranglerFile(path) {
				rel, _ := filepath.Rel(repoRoot, path)
				if rel == "" {
					rel = path
				}
				filesToScan = append(filesToScan, rel)
			}
			return nil
		})
	}

	res.FilesScanned = len(filesToScan)

	// If cfclean binary is present on PATH, also run cfclean check
	if len(filesToScan) > 0 {
		if _, err := exec.LookPath("cfclean"); err == nil {
			args := append([]string{"check"}, filesToScan...)
			cmdCheck := exec.Command("cfclean", args...)
			cmdCheck.Dir = repoRoot
			out, err := cmdCheck.CombinedOutput()
			if err != nil && len(out) > 0 {
				// Parse any violations from cfclean check output if not already captured
				scanner := bufio.NewScanner(bytes.NewReader(out))
				for scanner.Scan() {
					l := strings.TrimSpace(scanner.Text())
					if strings.HasPrefix(l, "[") && strings.Contains(l, "]") {
						// format: [path:line] Kind: line
						parts := strings.SplitN(l, "] ", 2)
						if len(parts) == 2 {
							loc := strings.TrimPrefix(parts[0], "[")
							locParts := strings.Split(loc, ":")
							fName := locParts[0]
							lNum := 0
							if len(locParts) > 1 {
								_, _ = fmt.Sscanf(locParts[1], "%d", &lNum)
							}
							kindMsg := parts[1]
							kind := "Unmasked Cloudflare Credential"
							lineContent := kindMsg
							if kmParts := strings.SplitN(kindMsg, ": ", 2); len(kmParts) == 2 {
								kind = kmParts[0]
								lineContent = kmParts[1]
							}
							res.Violations = append(res.Violations, CFCleanViolation{
								File:       fName,
								LineNumber: lNum,
								Kind:       kind,
								Line:       lineContent,
							})
						}
					}
				}
			}
		}
	}

	// Internal pure-Go scanner to guarantee hermetic execution
	for _, relPath := range filesToScan {
		fullPath := filepath.Join(repoRoot, relPath)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		violations := ScanFileForCFCredentials(relPath, data, knownAccountID, knownDatabaseID)
		// Avoid duplicate violation reporting
		for _, v := range violations {
			exists := false
			for _, ev := range res.Violations {
				if ev.File == v.File && ev.LineNumber == v.LineNumber {
					exists = true
					break
				}
			}
			if !exists {
				res.Violations = append(res.Violations, v)
			}
		}
	}

	if len(res.Violations) > 0 {
		res.Clean = false
	}

	return res
}
