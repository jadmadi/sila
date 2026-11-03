package assess

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// LicenseCategory classifies a software license.
type LicenseCategory string

// LicenseCategory constants defining licensing classifications.
const (
	LicensePermissive LicenseCategory = "permissive"
	LicenseCopyleft   LicenseCategory = "copyleft"
	LicenseUnknown    LicenseCategory = "unknown"
)

// ModuleLicenseInfo holds license evaluation details for a Go module dependency.
type ModuleLicenseInfo struct {
	Path     string          `json:"path"`
	Version  string          `json:"version"`
	License  string          `json:"license"`
	Category LicenseCategory `json:"category"`
	File     string          `json:"file,omitempty"`
}

// LicenseAuditReport represents the verdict of an open-source license audit.
type LicenseAuditReport struct {
	Clean        bool                `json:"clean"`
	TotalScanned int                 `json:"total_scanned"`
	Modules      []ModuleLicenseInfo `json:"modules"`
	Violations   []string            `json:"violations,omitempty"`
}

// knownPermissiveModules provides curated fallbacks for ubiquitous Go ecosystem dependencies.
var knownPermissiveModules = map[string]string{
	"github.com/mattn/go-isatty":       "MIT",
	"golang.org/x/sys":                 "BSD-3-Clause",
	"golang.org/x/term":                "BSD-3-Clause",
	"golang.org/x/mod":                 "BSD-3-Clause",
	"golang.org/x/sync":                "BSD-3-Clause",
	"golang.org/x/tools":               "BSD-3-Clause",
	"modernc.org/sqlite":               "BSD-3-Clause",
	"modernc.org/libc":                 "BSD-3-Clause",
	"modernc.org/mathutil":             "BSD-3-Clause",
	"modernc.org/memory":               "BSD-3-Clause",
	"modernc.org/cc/v4":                "BSD-3-Clause",
	"modernc.org/ccgo/v4":              "BSD-3-Clause",
	"modernc.org/fileutil":             "BSD-3-Clause",
	"modernc.org/gc/v2":                "BSD-3-Clause",
	"modernc.org/gc/v3":                "BSD-3-Clause",
	"modernc.org/goabi0":               "BSD-3-Clause",
	"modernc.org/opt":                  "BSD-3-Clause",
	"modernc.org/sortutil":             "BSD-3-Clause",
	"modernc.org/strutil":              "BSD-3-Clause",
	"modernc.org/token":                "BSD-3-Clause",
	"github.com/dustin/go-humanize":    "MIT",
	"github.com/google/uuid":           "BSD-3-Clause",
	"github.com/ncruces/go-strftime":   "MIT",
	"github.com/remyoudompheng/bigfft": "BSD-3-Clause",
}

// goModuleJSON represents the output of `go list -m -json`.
type goModuleJSON struct {
	Path     string `json:"Path"`
	Version  string `json:"Version"`
	Main     bool   `json:"Main"`
	Indirect bool   `json:"Indirect"`
	Dir      string `json:"Dir"`
}

// AuditLicenses scans direct and indirect dependencies for open-source license compliance.
// It verifies that all dependencies adhere to approved permissive licenses (MIT, Apache-2.0, BSD, ISC)
// and strictly flags copyleft licenses (GPL, AGPL, SSPL, LGPL, MPL) per AGENTS.md §0.
func AuditLicenses(dir string) (*LicenseAuditReport, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	report := &LicenseAuditReport{
		Clean:   true,
		Modules: make([]ModuleLicenseInfo, 0),
	}

	modules, err := listModules(dir)
	if err != nil {
		// Fallback to reading go.mod directly
		modules, err = parseGoMod(filepath.Join(dir, "go.mod"))
		if err != nil {
			return nil, fmt.Errorf("unable to resolve module dependencies: %w", err)
		}
	}

	for _, m := range modules {
		if m.Main {
			continue // Skip root application module
		}

		info := evaluateModuleLicense(m)
		report.TotalScanned++
		report.Modules = append(report.Modules, info)

		if info.Category == LicenseCopyleft {
			report.Clean = false
			report.Violations = append(report.Violations,
				fmt.Sprintf("unauthorized copyleft license %q in module %s (%s)", info.License, info.Path, info.Version))
		} else if info.Category == LicenseUnknown {
			report.Clean = false
			report.Violations = append(report.Violations,
				fmt.Sprintf("unknown or unapproved license in module %s (%s)", info.Path, info.Version))
		}
	}

	return report, nil
}

// listModules runs `go list -deps` in the given directory to inspect all packages built into the repository.
func listModules(dir string) ([]goModuleJSON, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-f", "{{if and (not .Standard) .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}", "./...")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var modules []goModuleJSON

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) >= 2 {
			path := parts[0]
			if seen[path] {
				continue
			}
			seen[path] = true
			version := parts[1]
			dirPath := ""
			if len(parts) >= 3 {
				dirPath = parts[2]
			}
			modules = append(modules, goModuleJSON{
				Path:    path,
				Version: version,
				Dir:     dirPath,
			})
		}
	}

	return modules, nil
}

// parseGoMod parses module dependencies from go.mod when go tool is not directly available.
func parseGoMod(goModPath string) ([]goModuleJSON, error) {
	f, err := os.Open(goModPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var modules []goModuleJSON
	scanner := bufio.NewScanner(f)
	inRequire := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if line == "require (" {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}

		if inRequire || strings.HasPrefix(line, "require ") {
			text := strings.TrimPrefix(line, "require ")
			parts := strings.Fields(text)
			if len(parts) >= 2 {
				path := parts[0]
				version := parts[1]
				indirect := len(parts) >= 3 && parts[2] == "//" && len(parts) >= 4 && parts[3] == "indirect"
				modules = append(modules, goModuleJSON{
					Path:     path,
					Version:  version,
					Indirect: indirect,
				})
			}
		}
	}

	return modules, scanner.Err()
}

// evaluateModuleLicense examines a module's cached directory or known database to determine its license.
func evaluateModuleLicense(m goModuleJSON) ModuleLicenseInfo {
	info := ModuleLicenseInfo{
		Path:    m.Path,
		Version: m.Version,
	}

	// 1. Check local directory if available
	if m.Dir != "" {
		licName, cat, file := scanLicenseFileInDir(m.Dir)
		if cat != LicenseUnknown {
			info.License = licName
			info.Category = cat
			info.File = file
			return info
		}
	}

	// 2. Check GOPATH mod cache if Dir was empty
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, _ := os.UserHomeDir()
		gopath = filepath.Join(home, "go")
	}
	modCacheDir := filepath.Join(gopath, "pkg", "mod", m.Path+"@"+m.Version)
	if fi, err := os.Stat(modCacheDir); err == nil && fi.IsDir() {
		licName, cat, file := scanLicenseFileInDir(modCacheDir)
		if cat != LicenseUnknown {
			info.License = licName
			info.Category = cat
			info.File = file
			return info
		}
	}

	// 3. Check curated fallback database
	if lic, ok := knownPermissiveModules[m.Path]; ok {
		info.License = lic
		info.Category = LicensePermissive
		return info
	}

	// Default unknown
	info.License = "Unknown"
	info.Category = LicenseUnknown
	return info
}

var licenseFileNames = []string{
	"LICENSE",
	"LICENSE.md",
	"LICENSE.txt",
	"LICENSE-MIT",
	"LICENSE-APACHE",
	"LICENCE",
	"LICENCE.md",
	"LICENCE.txt",
	"COPYING",
	"COPYING.txt",
	"COPYING.md",
}

// scanLicenseFileInDir looks for standard license files in dir and identifies the license.
func scanLicenseFileInDir(dir string) (string, LicenseCategory, string) {
	for _, name := range licenseFileNames {
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p)
		if err == nil {
			licName, cat := DetectLicense(string(data))
			if cat != LicenseUnknown {
				return licName, cat, p
			}
		}
	}
	return "Unknown", LicenseUnknown, ""
}

// Regex patterns for license detection
var (
	reAGPL = regexp.MustCompile(`(?i)(GNU\s+Affero\s+General\s+Public\s+License|AGPL(?:-[\d\.]+)?|Affero\s+GPL)`)
	reGPL  = regexp.MustCompile(`(?i)(GNU\s+General\s+Public\s+License|GPLv[123]|GPL-[\d\.]+)`)
	reLGPL = regexp.MustCompile(`(?i)(GNU\s+Lesser\s+General\s+Public\s+License|GNU\s+Library\s+General\s+Public\s+License|LGPL(?:-[\d\.]+)?)`)
	reSSPL = regexp.MustCompile(`(?i)(Server\s+Side\s+Public\s+License|SSPL)`)
	reMPL  = regexp.MustCompile(`(?i)(Mozilla\s+Public\s+License|MPL-[\d\.]+)`)

	reApache = regexp.MustCompile(`(?i)(Apache\s+License,\s+Version\s+2\.0|http://www\.apache\.org/licenses/LICENSE-2\.0|Apache-2\.0)`)
	reMIT    = regexp.MustCompile(`(?i)(MIT\s+License|Permission\s+is\s+hereby\s+granted,\s+free\s+of\s+charge,\s+to\s+any\s+person)`)
	reBSD3   = regexp.MustCompile(`(?i)(Redistribution\s+and\s+use\s+in\s+source\s+and\s+binary\s+forms.*?Neither\s+the\s+name\s+of|3-Clause|BSD-3)`)
	reBSD2   = regexp.MustCompile(`(?i)(Redistribution\s+and\s+use\s+in\s+source\s+and\s+binary\s+forms.*?2-Clause|BSD-2)`)
	reISC    = regexp.MustCompile(`(?i)(Permission\s+to\s+use,\s+copy,\s+modify,\s+and/or\s+distribute\s+this\s+software\s+for\s+any\s+purpose|ISC\s+License)`)
	reUnlic  = regexp.MustCompile(`(?i)(This\s+is\s+free\s+and\s+unencumbered\s+software\s+released\s+into\s+the\s+public\s+domain|The\s+Unlicense|CC0-1\.0)`)
)

// DetectLicense analyzes raw license text and classifies it as permissive, copyleft, or unknown.
func DetectLicense(content string) (string, LicenseCategory) {
	// First check strictly banned copyleft
	if reMPL.MatchString(content) {
		return "MPL", LicenseCopyleft
	}
	if reAGPL.MatchString(content) {
		return "AGPL", LicenseCopyleft
	}
	if reSSPL.MatchString(content) {
		return "SSPL", LicenseCopyleft
	}
	if reGPL.MatchString(content) {
		return "GPL", LicenseCopyleft
	}
	if reLGPL.MatchString(content) {
		return "LGPL", LicenseCopyleft
	}

	// Permissive licenses
	if reApache.MatchString(content) {
		return "Apache-2.0", LicensePermissive
	}
	if reMIT.MatchString(content) {
		return "MIT", LicensePermissive
	}
	if reBSD3.MatchString(content) {
		return "BSD-3-Clause", LicensePermissive
	}
	if reBSD2.MatchString(content) {
		return "BSD-2-Clause", LicensePermissive
	}
	if reISC.MatchString(content) {
		return "ISC", LicensePermissive
	}
	if reUnlic.MatchString(content) {
		return "Public Domain / Unlicense", LicensePermissive
	}

	return "Unknown", LicenseUnknown
}
