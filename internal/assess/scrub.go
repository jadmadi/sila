package assess

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ScrubEightChannels neutralizes all eight information leakage channels in the sandbox.
func ScrubEightChannels(sandboxDir, homeDir, cacheDir string, epoch time.Time) error {
	// Channel 2: Scratchpads — remove scratch files and temporary workspaces
	scrubPaths := []string{
		filepath.Join(sandboxDir, "scratch"),
		filepath.Join(sandboxDir, ".tmp"),
		filepath.Join(sandboxDir, "tmp"),
	}
	for _, p := range scrubPaths {
		_ = os.RemoveAll(p)
	}

	// Channel 4: Test mocks — remove recorded mock files
	_ = filepath.Walk(sandboxDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		name := info.Name()
		if strings.HasSuffix(name, ".mock.go") || strings.HasSuffix(name, "_mock.go") || name == "test_mocks" {
			_ = os.RemoveAll(path)
		}
		return nil
	})

	// Channel 5: Shell history — remove history files
	historyFiles := []string{
		filepath.Join(homeDir, ".bash_history"),
		filepath.Join(homeDir, ".zsh_history"),
		filepath.Join(homeDir, ".history"),
		filepath.Join(sandboxDir, ".bash_history"),
	}
	for _, h := range historyFiles {
		_ = os.Remove(h)
	}

	// Channel 6: Chat logs & transcripts — remove agent and system transcripts
	transcriptDirs := []string{
		filepath.Join(sandboxDir, ".system_generated"),
		filepath.Join(sandboxDir, ".agents"),
	}
	for _, td := range transcriptDirs {
		_ = os.RemoveAll(td)
	}

	// Channel 7: Defect trails — remove crash reports and error dossiers
	defectFiles := []string{
		filepath.Join(sandboxDir, "defect-dossier.json"),
		filepath.Join(sandboxDir, "gate-test.log"),
		filepath.Join(sandboxDir, ".sila", "reports"),
	}
	for _, df := range defectFiles {
		_ = os.RemoveAll(df)
	}

	// Channel 8: Filesystem timestamps — normalize every file and directory to fixed epoch
	if epoch.IsZero() {
		epoch = time.Unix(1700000000, 0).UTC()
	}
	_ = filepath.Walk(sandboxDir, func(path string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chtimes(path, epoch, epoch)
		}
		return nil
	})

	// Channel 1: Git metadata — ensure single synthetic commit with no parent history or remotes
	// Must execute AFTER neutralizing channels 2, 4, 5, 6, 7 so synthetic tree contains zero scrubbed files
	if err := scrubGitChannel(sandboxDir); err != nil {
		return fmt.Errorf("scrub git metadata: %w", err)
	}

	return nil
}

func scrubGitChannel(sandboxDir string) error {
	gitDir := filepath.Join(sandboxDir, ".git")
	_ = os.RemoveAll(gitDir)

	initCmd := exec.Command("git", "init")
	initCmd.Dir = sandboxDir
	if err := initCmd.Run(); err != nil {
		return err
	}

	cfgName := exec.Command("git", "config", "user.name", "Sila Epistemic Auditor")
	cfgName.Dir = sandboxDir
	_ = cfgName.Run()

	cfgEmail := exec.Command("git", "config", "user.email", "auditor@sila.local")
	cfgEmail.Dir = sandboxDir
	_ = cfgEmail.Run()

	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = sandboxDir
	_ = addCmd.Run()

	commitCmd := exec.Command("git", "commit", "-m", "clean-room specification baseline")
	commitCmd.Dir = sandboxDir
	_ = commitCmd.Run()

	return nil
}

// BuildScrubbedEnvironment creates the whitelisted environment variables for the mystery auditor.
func BuildScrubbedEnvironment(homeDir, cacheDir string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + homeDir,
		"TMPDIR=" + os.TempDir(),
		"GOPATH=" + filepath.Join(homeDir, "go"),
		"GOCACHE=" + filepath.Join(cacheDir, "go-build"),
		"GOPROXY=off",
		"GONOSUMDB=*",
		"HISTFILE=/dev/null",
		"TERM=dumb",
		"LC_ALL=C",
		"SILA_INTEGRITY_MODE=benchmark",
		"SILA_BLINDFOLD_AUDIT=1",
		"GOFLAGS=-p=2",
		"SILA_NO_TMUX=1",
		"SILA_STORE_PATH=:memory:",
	}
	hostModCache := os.Getenv("GOMODCACHE")
	if hostModCache == "" {
		if hostHome := os.Getenv("HOME"); hostHome != "" {
			candidate := filepath.Join(hostHome, "go", "pkg", "mod")
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				hostModCache = candidate
			}
		}
	}
	if hostModCache != "" {
		env = append(env, "GOMODCACHE="+hostModCache)
	}
	hostHome := os.Getenv("HOME")
	androidSDK := os.Getenv("ANDROID_HOME")
	if androidSDK == "" {
		androidSDK = os.Getenv("ANDROID_SDK_ROOT")
	}
	if androidSDK == "" && hostHome != "" {
		candidate := filepath.Join(hostHome, "Android", "Sdk")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			androidSDK = candidate
		}
	}
	if androidSDK != "" {
		env = append(env, "ANDROID_HOME="+androidSDK, "ANDROID_SDK_ROOT="+androidSDK)
	}
	if javaHome := os.Getenv("JAVA_HOME"); javaHome != "" {
		if info, err := os.Stat(javaHome); err == nil && info.IsDir() {
			env = append(env, "JAVA_HOME="+javaHome)
		}
	}
	if hostHome != "" {
		env = append(env, "SILA_HOST_HOME="+hostHome)
	}
	return env
}
