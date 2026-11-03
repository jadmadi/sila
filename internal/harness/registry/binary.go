package registry

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// BinaryResolution describes how an installed hook should invoke Sila.
type BinaryResolution struct {
	Path        string `json:"path,omitempty"`
	Source      string `json:"source,omitempty"`
	WrapperPath string `json:"wrapper_path,omitempty"`
	Resolved    bool   `json:"resolved"`
}

// BinaryResolver is injectable for tests and for callers that already know the
// binary path. Production uses ResolveHookBinary.
var BinaryResolver = ResolveHookBinary

// ResolveHookBinary resolves the current Sila binary without relying on the
// harness inheriting ~/.local/bin in PATH. It falls back to a portable wrapper.
func ResolveHookBinary() (BinaryResolution, error) {
	home, _ := os.UserHomeDir()
	self, _ := os.Executable()
	return resolveHookBinary(home, os.Getenv("SILA_BIN"), os.Args[0], os.Getenv("PATH"), self)
}

func resolveHookBinary(home, silaBin, argv0, pathEnv, self string) (BinaryResolution, error) {
	// Only a running Sila process may resolve a global install. Test binaries
	// and unrelated callers keep the legacy command so golden configs remain
	// deterministic; they can inject BinaryResolver or set SILA_BIN.
	if strings.TrimSpace(silaBin) == "" && !isSilaBinary(argv0) {
		return BinaryResolution{Path: "sila", Source: "legacy"}, nil
	}
	if strings.TrimSpace(silaBin) != "" {
		if path, ok := executablePath(silaBin, pathEnv); ok {
			return BinaryResolution{Path: path, Source: "explicit", Resolved: true}, nil
		}
	}
	if isSilaBinary(argv0) && isSilaBinary(self) && isExecutable(self) {
		return BinaryResolution{Path: self, Source: "self", Resolved: true}, nil
	}
	// Only trust PATH when the running process is itself Sila. A test binary or
	// an unrelated caller must not embed a global sila it happened to find.
	if isSilaBinary(argv0) {
		if path, ok := executablePath("sila", pathEnv); ok {
			return BinaryResolution{Path: path, Source: "path", Resolved: true}, nil
		}
	}
	if home != "" {
		homeBin := filepath.Join(home, ".local", "bin", "sila")
		if isExecutable(homeBin) {
			return BinaryResolution{Path: homeBin, Source: "home", Resolved: true}, nil
		}
	}
	for _, rel := range []string{filepath.Join(".bin", "sila"), "sila"} {
		if isExecutable(rel) {
			abs, err := filepath.Abs(rel)
			if err == nil {
				return BinaryResolution{Path: abs, Source: "workspace", Resolved: true}, nil
			}
		}
	}
	wrapper, err := EnsureHookWrapper(home)
	if err == nil {
		return BinaryResolution{Path: wrapper, Source: "wrapper", WrapperPath: wrapper, Resolved: true}, nil
	}
	return BinaryResolution{Path: "sila", Source: "legacy"}, err
}

func executablePath(candidate, pathEnv string) (string, bool) {
	if strings.ContainsRune(candidate, filepath.Separator) || strings.Contains(candidate, "/") {
		if isExecutable(candidate) {
			abs, err := filepath.Abs(candidate)
			if err == nil {
				return abs, true
			}
			return candidate, true
		}
		return "", false
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, candidate)
		if isExecutable(p) {
			return p, true
		}
	}
	return "", false
}

func isSilaBinary(path string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(path)))
	switch base {
	case "sila", "sila.exe":
		return true
	default:
		return false
	}
}

func isExecutable(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// EnsureHookWrapper writes a stable wrapper that tries SILA_BIN, PATH,
// ~/.local/bin/sila, workspace shims, and (for the Sila source repo) go run.
func EnsureHookWrapper(home string) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("hook wrapper: user home directory is required")
	}
	dir := filepath.Join(home, ".local", "share", "sila", "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	name := "sila-hook"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	path := filepath.Join(dir, name)
	var script string
	if runtime.GOOS == "windows" {
		script = "@echo off\r\n" +
			"if defined SILA_BIN if exist \"%SILA_BIN%\" \"%SILA_BIN%\" %*\r\n" +
			"where sila >nul 2>nul && sila %*\r\n" +
			"if exist \"%USERPROFILE%\\.local\\bin\\sila.exe\" \"%USERPROFILE%\\.local\\bin\\sila.exe\" %*\r\n" +
			"echo sila-hook: Sila binary not found; set SILA_BIN or install sila.exe 1>&2\r\n" +
			"exit /b 127\r\n"
	} else {
		script = "#!/bin/sh\n" +
			"if [ -n \"${SILA_BIN:-}\" ] && [ -x \"$SILA_BIN\" ]; then exec \"$SILA_BIN\" \"$@\"; fi\n" +
			"if command -v sila >/dev/null 2>&1; then exec sila \"$@\"; fi\n" +
			"if [ -x \"$HOME/.local/bin/sila\" ]; then exec \"$HOME/.local/bin/sila\" \"$@\"; fi\n" +
			"if [ -x ./.bin/sila ]; then exec ./.bin/sila \"$@\"; fi\n" +
			"if [ -x ./sila ]; then exec ./sila \"$@\"; fi\n" +
			"if [ -f go.mod ] && grep -q '^module github.com/jadmadi/sila' go.mod 2>/dev/null; then exec go run ./cmd/sila \"$@\"; fi\n" +
			"echo \"sila-hook: Sila binary not found; set SILA_BIN or install ~/.local/bin/sila\" >&2\n" +
			"exit 127\n"
	}
	tmp, err := os.CreateTemp(dir, ".sila-hook-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o700); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(script); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return path, nil
}

// commandForInstall rewrites a registry command's leading "sila" token to the
// resolved binary or wrapper. Unknown commands and unresolved installs keep the
// legacy command so existing tests and configs remain valid.
func commandForInstall(cmd string) string {
	if !strings.HasPrefix(cmd, "sila ") {
		return cmd
	}
	res, err := BinaryResolver()
	if err != nil || !res.Resolved || strings.TrimSpace(res.Path) == "" {
		return cmd
	}
	return quoteShellPath(res.Path) + strings.TrimPrefix(cmd, "sila")
}

func quoteShellPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	if runtime.GOOS == "windows" {
		if !strings.ContainsAny(path, " \t\"") {
			return path
		}
		return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"`
	}
	if !strings.ContainsAny(path, " \t\"'\\$`") {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func binaryResolution() BinaryResolution {
	res, err := BinaryResolver()
	if err != nil {
		return BinaryResolution{Path: "sila", Source: "legacy"}
	}
	return res
}
