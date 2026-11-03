package assess

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// NamespaceResult captures the outcome of a command executed in a hermetic namespace.
type NamespaceResult struct {
	ExitCode      int           `json:"exit_code"`
	Stdout        string        `json:"stdout"`
	Stderr        string        `json:"stderr"`
	Duration      time.Duration `json:"duration_ms"`
	UsedBwrap     bool          `json:"used_bwrap"`
	FailureReason string        `json:"failure_reason,omitempty"`
}

// IsBubblewrapAvailable reports whether the bwrap binary exists on PATH.
func IsBubblewrapAvailable() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

// BuildBwrapArgs constructs CLI arguments for Bubblewrap namespace isolation.
func BuildBwrapArgs(sandboxDir, homeDir, cacheDir string, cleanEnv []string, command []string) []string {
	args := []string{
		"--unshare-all",
		"--unshare-net",
		"--clearenv",
		"--proc", "/proc",
		"--dev", "/dev",
		"--dir", "/tmp",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind-try", "/bin", "/bin",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--ro-bind-try", "/etc", "/etc",
	}

	if sandboxDir != "" {
		args = append(args, "--bind", sandboxDir, sandboxDir)
		args = append(args, "--chdir", sandboxDir)
	}
	if homeDir != "" {
		args = append(args, "--bind", homeDir, homeDir)
	}
	if cacheDir != "" {
		args = append(args, "--bind", cacheDir, cacheDir)
	}

	for _, e := range cleanEnv {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			args = append(args, "--setenv", parts[0], parts[1])
		}
	}

	args = append(args, "--")
	args = append(args, command...)
	return args
}

// ExecuteInNamespace runs a probe command inside a Bubblewrap container or fallback subshell.
func ExecuteInNamespace(ctx context.Context, sandbox *BlindfoldSandbox, cmd []string, env []string, stdin string) (*NamespaceResult, error) {
	if len(cmd) == 0 {
		return &NamespaceResult{FailureReason: "empty command"}, fmt.Errorf("empty command")
	}

	start := time.Now()
	combinedEnv := append([]string{}, sandbox.CleanEnv...)
	combinedEnv = append(combinedEnv, env...)

	var execCmd *exec.Cmd
	usedBwrap := false

	if os.Getenv("SILA_DISABLE_BWRAP") != "1" && IsBubblewrapAvailable() {
		bwrapArgs := BuildBwrapArgs(sandbox.Dir, sandbox.homeDir, sandbox.cacheDir, combinedEnv, cmd)
		bwrapArgs = injectReadOnlyToolchainBinds(bwrapArgs, combinedEnv, sandbox.Dir)
		execCmd = exec.CommandContext(ctx, "bwrap", bwrapArgs...)
		usedBwrap = true
	} else {
		execCmd = exec.CommandContext(ctx, cmd[0], cmd[1:]...)
		execCmd.Dir = sandbox.Dir
		execCmd.Env = combinedEnv
	}

	if stdin != "" {
		execCmd.Stdin = strings.NewReader(stdin)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	execCmd.Stdout = &stdoutBuf
	execCmd.Stderr = &stderrBuf

	err := execCmd.Run()
	duration := time.Since(start)

	exitCode := 0
	failureReason := ""
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			failureReason = fmt.Sprintf("execution failed: %v", err)
			return &NamespaceResult{
				ExitCode:      1,
				Duration:      duration,
				UsedBwrap:     usedBwrap,
				FailureReason: failureReason,
			}, err
		}
	}

	return &NamespaceResult{
		ExitCode:      exitCode,
		Stdout:        stdoutBuf.String(),
		Stderr:        stderrBuf.String(),
		Duration:      duration,
		UsedBwrap:     usedBwrap,
		FailureReason: failureReason,
	}, nil
}

func injectReadOnlyToolchainBinds(bwrapArgs, combinedEnv []string, sandboxDir string) []string {
	var extraBinds []string
	seen := make(map[string]bool)
	addBind := func(dir string) {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "" || dir == "/" || dir == "." || !filepath.IsAbs(dir) {
			return
		}
		// bwrap refuses to mount over a symlink destination. Optional host
		// toolchain shims (for example mise's "latest" links) are skipped.
		if info, err := os.Lstat(dir); err != nil || info.Mode()&os.ModeSymlink != 0 {
			return
		}
		for _, std := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/tmp", "/proc", "/dev"} {
			if dir == std || strings.HasPrefix(dir, std+"/") {
				return
			}
		}
		if sandboxDir != "" && (dir == sandboxDir || strings.HasPrefix(dir, sandboxDir+"/")) {
			return
		}
		if !seen[dir] {
			seen[dir] = true
			extraBinds = append(extraBinds, "--ro-bind-try", dir, dir)
		}
	}

	for _, e := range combinedEnv {
		switch {
		case strings.HasPrefix(e, "GOMODCACHE="):
			addBind(strings.TrimPrefix(e, "GOMODCACHE="))
		case strings.HasPrefix(e, "GOROOT="):
			addBind(strings.TrimPrefix(e, "GOROOT="))
		case strings.HasPrefix(e, "ANDROID_HOME="):
			addBind(strings.TrimPrefix(e, "ANDROID_HOME="))
		case strings.HasPrefix(e, "ANDROID_SDK_ROOT="):
			addBind(strings.TrimPrefix(e, "ANDROID_SDK_ROOT="))
		case strings.HasPrefix(e, "JAVA_HOME="):
			addBind(strings.TrimPrefix(e, "JAVA_HOME="))
		case strings.HasPrefix(e, "SILA_HOST_HOME="):
			hostHome := filepath.Clean(strings.TrimPrefix(e, "SILA_HOST_HOME="))
			sandboxHome := ""
			if sandboxDir != "" {
				sandboxHome = filepath.Join(filepath.Clean(sandboxDir), ".sila-home")
			}
			for _, rel := range []string{
				filepath.Join("Android", "Sdk"),
				filepath.Join(".gradle", "jdks"),
				filepath.Join(".gradle", "wrapper"),
				filepath.Join(".gradle", "caches"),
				".m2",
				".cargo",
				".rustup",
			} {
				src := filepath.Join(hostHome, rel)
				addBind(src)
				if sandboxHome != "" && sandboxHome != hostHome && strings.HasPrefix(rel, ".gradle") {
					if info, err := os.Stat(src); err == nil && info.IsDir() {
						dst := filepath.Join(sandboxHome, rel)
						_ = os.MkdirAll(dst, 0755)
						if !seen[dst] {
							seen[dst] = true
							extraBinds = append(extraBinds, "--ro-bind-try", src, dst)
						}
					}
				}
			}
		case strings.HasPrefix(e, "PATH="):
			for _, p := range filepath.SplitList(strings.TrimPrefix(e, "PATH=")) {
				cleanP := filepath.Clean(p)
				if filepath.Base(cleanP) == "bin" {
					addBind(filepath.Dir(cleanP))
				} else {
					addBind(cleanP)
				}
			}
		}
	}
	if len(extraBinds) == 0 {
		return bwrapArgs
	}
	for i, a := range bwrapArgs {
		if a == "--" {
			out := make([]string, 0, len(bwrapArgs)+len(extraBinds))
			out = append(out, bwrapArgs[:i]...)
			out = append(out, extraBinds...)
			out = append(out, bwrapArgs[i:]...)
			return out
		}
	}
	return bwrapArgs
}
