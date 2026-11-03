package version

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/render"
	"github.com/mattn/go-isatty"
)

const DefaultR2BaseURL = "https://sila.jadmadi.net/sila"

type ReleaseInfo struct {
	Version   string   `json:"version"`
	Commit    string   `json:"commit"`
	BuiltAt   string   `json:"built_at"`
	Platforms []string `json:"platforms"`
}

type UpdateOptions struct {
	Force     bool   `json:"force"`
	CheckOnly bool   `json:"check_only"`
	Source    bool   `json:"source"`
	JSON      bool   `json:"json"`
	NoColor   bool   `json:"no_color"`
	BaseURL   string `json:"base_url,omitempty"`
}

type UpdateCache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

func UpdateCachePath() string {
	return filepath.Join(config.CacheDir(), "update.json")
}

func LoadUpdateCache() (UpdateCache, error) {
	p := UpdateCachePath()
	b, err := os.ReadFile(p)
	if err != nil {
		return UpdateCache{}, err
	}
	var c UpdateCache
	if err := json.Unmarshal(b, &c); err != nil {
		return UpdateCache{}, err
	}
	return c, nil
}

func SaveUpdateCache(c UpdateCache) error {
	p := UpdateCachePath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0644)
}

func resolveBaseURL(override string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	if env := os.Getenv("SILA_URL_BASE"); env != "" {
		return strings.TrimRight(env, "/")
	}
	return DefaultR2BaseURL
}

func checksumForAsset(manifest, asset string) (string, error) {
	for line := range strings.SplitSeq(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			filename := strings.TrimPrefix(fields[len(fields)-1], "*")
			filename = filepath.Base(filename)
			if filename == asset {
				if len(fields[0]) != 64 {
					return "", fmt.Errorf("invalid SHA-256 checksum for %s", asset)
				}
				return fields[0], nil
			}
		}
	}
	return "", fmt.Errorf("checksum for %s is missing", asset)
}

func verifiedDownload(client *http.Client, binaryURL, checksumURL, asset string, dst io.Writer) error {
	resp, err := client.Get(checksumURL)
	if err != nil {
		return fmt.Errorf("download checksum manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checksum server returned status %d", resp.StatusCode)
	}
	manifest, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return fmt.Errorf("read checksum manifest: %w", err)
	}
	expected, err := checksumForAsset(string(manifest), asset)
	if err != nil {
		return err
	}
	resp, err = client.Get(binaryURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download server returned status %d", resp.StatusCode)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), resp.Body); err != nil {
		return fmt.Errorf("write binary: %w", err)
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)); !strings.EqualFold(got, expected) {
		return fmt.Errorf("download checksum mismatch for %s: expected %s, got %s", asset, expected, got)
	}
	return nil
}

type releaseMetadata struct {
	Version              string `json:"version"`
	MCPContractVersion   int    `json:"mcp_contract_version"`
	MCPContractSHA256    string `json:"mcp_contract_sha256"`
	SchemaVersion        int    `json:"schema_version"`
	MinimumSchemaVersion int    `json:"minimum_schema_version"`
}

func validateReleaseMetadata(metadata releaseMetadata, expectedVersion string) error {
	if !IsRelease(metadata.Version) || metadata.Version != expectedVersion {
		return fmt.Errorf("release metadata version %q does not match expected %q", metadata.Version, expectedVersion)
	}
	if metadata.MCPContractVersion < 1 || len(metadata.MCPContractSHA256) != 64 || strings.Trim(metadata.MCPContractSHA256, "0123456789abcdefABCDEF") != "" {
		return fmt.Errorf("release metadata has invalid MCP contract")
	}
	if metadata.SchemaVersion < 1 || metadata.MinimumSchemaVersion < 1 || metadata.MinimumSchemaVersion > metadata.SchemaVersion {
		return fmt.Errorf("release metadata has invalid schema version")
	}
	return nil
}

// FetchLatestRelease queries the Cloudflare R2 distribution bucket for release metadata.
func FetchLatestRelease(baseURL string) (ReleaseInfo, error) {
	baseURL = resolveBaseURL(baseURL)
	url := baseURL + "/latest/version.json"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ReleaseInfo{}, err
	}
	req.Header.Set("User-Agent", "sila-update/"+Version)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ReleaseInfo{}, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ReleaseInfo{}, fmt.Errorf("distribution server returned status %s", resp.Status)
	}

	var info ReleaseInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&info); err != nil {
		return ReleaseInfo{}, fmt.Errorf("decode release manifest: %w", err)
	}
	return info, nil
}

// RunBackgroundUpdateCheck queries the latest release in the background and memoizes the result.
func RunBackgroundUpdateCheck() {
	info, err := FetchLatestRelease("")
	if err != nil || info.Version == "" {
		return
	}
	_ = SaveUpdateCache(UpdateCache{
		CheckedAt:     time.Now().UTC(),
		LatestVersion: info.Version,
	})
}

// MaybeCheckForUpdate checks if an update is available and spawns a background update check if stale.
func MaybeCheckForUpdate(interactive bool, noColor bool, currentSub string) {
	if !interactive {
		return
	}
	if os.Getenv("SILA_NO_UPDATE_CHECK") != "" || os.Getenv("CI") != "" {
		return
	}
	if currentSub == "upgrade" || currentSub == "update" || currentSub == "--check-update-bg" || currentSub == "mcp" {
		return
	}
	if !isatty.IsTerminal(os.Stderr.Fd()) && !isatty.IsCygwinTerminal(os.Stderr.Fd()) {
		return
	}

	cache, err := LoadUpdateCache()
	now := time.Now().UTC()

	// If cache has a newer version than current, notify user on stderr
	current := Version
	if current != "" && cache.LatestVersion != "" {
		if current == "dev" || strings.HasPrefix(current, "dev+") {
			return
		}
		if SemverCompare(cache.LatestVersion, current) > 0 {
			curTag := current
			if !strings.HasPrefix(curTag, "v") && curTag != "dev" && !strings.HasPrefix(curTag, "dev+") {
				curTag = "v" + curTag
			}
			latTag := cache.LatestVersion
			if !strings.HasPrefix(latTag, "v") {
				latTag = "v" + latTag
			}
			msg := fmt.Sprintf("A new version of sila is available: %s → %s. Run 'sila upgrade' to update.", curTag, latTag)
			if !noColor {
				fmt.Fprintf(os.Stderr, "\n\033[2m%s\033[0m\n", msg)
			} else {
				fmt.Fprintf(os.Stderr, "\n%s\n", msg)
			}
		}
	}

	// If cache is missing or last check is older than 24 hours, spawn detached background check
	if err != nil || now.Sub(cache.CheckedAt) > 24*time.Hour {
		_ = SaveUpdateCache(UpdateCache{
			CheckedAt:     now,
			LatestVersion: cache.LatestVersion,
		})
		spawnBackgroundUpdateCheck()
	}
}

func spawnBackgroundUpdateCheck() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if strings.HasSuffix(exe, ".test") || strings.Contains(exe, "__debug_bin") {
		return
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		exe, _ = os.Executable()
	}

	cmd := exec.Command(exe, "--check-update-bg")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	setDetachedProcess(cmd)
	_ = cmd.Start()
}

// RunUpgrade performs a self-upgrade of the running sila binary.
func RunUpgrade(ui render.UI, opts UpdateOptions) error {
	baseURL := resolveBaseURL(opts.BaseURL)

	// Check if local source rebuild was requested
	if opts.Source {
		if _, err := os.Stat("build.sh"); err == nil {
			if _, err := os.Stat("cmd/sila/main.go"); err == nil {
				fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · upgrade"))+" — rebuilding from local source via ./build.sh...")
				cmd := exec.Command("./build.sh")
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					return fmt.Errorf("build.sh failed: %w", err)
				}
				fmt.Fprintln(os.Stdout, ui.Green("✔ Successfully rebuilt and installed sila."))
				return nil
			}
		}
		return fmt.Errorf("--source specified but build.sh or cmd/sila/main.go not found in current directory")
	}

	if !opts.JSON {
		fmt.Fprintf(os.Stdout, "  sila %s  checking for updates on %s...\n", String(), baseURL)
	}

	release, err := FetchLatestRelease(baseURL)
	if err != nil {
		// If running in local repository checkout, suggest building from source
		if _, statErr := os.Stat("build.sh"); statErr == nil {
			if _, statErr2 := os.Stat("cmd/sila/main.go"); statErr2 == nil {
				fmt.Fprintf(os.Stderr, "  warning: remote update check failed (%v), falling back to local source build...\n", err)
				cmd := exec.Command("./build.sh")
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				return cmd.Run()
			}
		}
		return fmt.Errorf("check latest release: %w", err)
	}

	current := Version
	latest := release.Version

	isUpToDate := false
	if current != "dev" && !strings.HasPrefix(current, "dev+") && current == latest {
		isUpToDate = true
	} else if current != "dev" && !strings.HasPrefix(current, "dev+") && SemverCompare(current, latest) >= 0 {
		isUpToDate = true
	}

	if opts.CheckOnly {
		if opts.JSON {
			emitJSON(map[string]any{
				"current_version":  current,
				"latest_version":   latest,
				"update_available": !isUpToDate,
				"commit":           release.Commit,
				"built_at":         release.BuiltAt,
			})
			return nil
		}
		if isUpToDate {
			fmt.Fprintf(os.Stdout, "  ✔ already up to date — %s\n", latest)
		} else {
			fmt.Fprintf(os.Stdout, "  update available: %s → %s. Run 'sila upgrade' to update.\n", current, latest)
		}
		return nil
	}

	if isUpToDate && !opts.Force {
		if opts.JSON {
			emitJSON(map[string]any{
				"current_version": current,
				"latest_version":  latest,
				"updated":         false,
				"message":         "already up to date",
			})
			return nil
		}
		fmt.Fprintf(os.Stdout, "  ✔ already up to date — %s\n", latest)
		_ = SaveUpdateCache(UpdateCache{CheckedAt: time.Now().UTC(), LatestVersion: latest})
		return nil
	}

	if current == latest && opts.Force {
		fmt.Fprintf(os.Stdout, "  forcing reinstallation of %s...\n", latest)
	} else if current == "dev" || strings.HasPrefix(current, "dev+") {
		if !opts.Force {
			return fmt.Errorf("current binary is a local development build (%s); running upgrade would overwrite it with published release %s (use --force to overwrite)", current, latest)
		}
		fmt.Fprintf(os.Stdout, "  current: %s\n", current)
		fmt.Fprintf(os.Stdout, "  latest:  %s\n", latest)
	} else {
		fmt.Fprintf(os.Stdout, "  update available: %s → %s\n", current, latest)
		if IsBreakingUpgrade(current, latest) {
			fmt.Fprintln(os.Stdout, ui.Yellow("  notice: crossing release boundary — see CHANGELOG.md for migration details."))
		}
	}

	// Locate target platform binary
	targetOS := runtime.GOOS
	targetArch := runtime.GOARCH
	ext := ""
	if targetOS == "windows" {
		ext = ".exe"
	}
	assetName := fmt.Sprintf("sila-%s-%s%s", targetOS, targetArch, ext)

	// Resolve executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		execPath, _ = os.Executable()
	}

	installDir := filepath.Dir(execPath)
	// Test writability of target directory
	testFile := filepath.Join(installDir, fmt.Sprintf(".sila-write-test-%d", os.Getpid()))
	if err := os.WriteFile(testFile, []byte(""), 0600); err != nil {
		return fmt.Errorf("cannot write to %s: %w\nPermission denied. If installed in a system directory, run: sudo sila upgrade", installDir, err)
	}
	_ = os.Remove(testFile)

	fmt.Fprintf(os.Stdout, "  downloading %s...\n", assetName)

	downloadURL := fmt.Sprintf("%s/latest/%s", baseURL, assetName)
	checksumURL := fmt.Sprintf("%s/latest/checksums.txt", baseURL)

	client := &http.Client{Timeout: 60 * time.Second}

	tmpFile := filepath.Join(installDir, fmt.Sprintf(".sila-new-%d%s", os.Getpid(), ext))
	out, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create temporary binary: %w", err)
	}

	if err := verifiedDownload(client, downloadURL, checksumURL, assetName, out); err != nil {
		out.Close()
		_ = os.Remove(tmpFile)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("close downloaded binary: %w", err)
	}

	if err := os.Chmod(tmpFile, 0755); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("chmod binary: %w", err)
	}

	// Atomic replacement: rename current -> old, tmp -> current, remove old
	oldPath := execPath + ".old"
	_ = os.Remove(oldPath)

	if err := os.Rename(execPath, oldPath); err != nil {
		// Fallback for environments where renaming running executable is restricted
		if copyErr := copyFile(tmpFile, execPath); copyErr != nil {
			_ = os.Remove(tmpFile)
			return fmt.Errorf("replace binary: %w (fallback: %v)", err, copyErr)
		}
		_ = os.Remove(tmpFile)
	} else {
		if err := os.Rename(tmpFile, execPath); err != nil {
			_ = os.Rename(oldPath, execPath)
			_ = os.Remove(tmpFile)
			return fmt.Errorf("swap binary: %w", err)
		}
		_ = os.Remove(oldPath)
	}

	_ = SaveUpdateCache(UpdateCache{CheckedAt: time.Now().UTC(), LatestVersion: latest})

	if opts.JSON {
		emitJSON(map[string]any{
			"current_version": current,
			"latest_version":  latest,
			"updated":         true,
			"binary_path":     execPath,
		})
		return nil
	}

	fmt.Fprintf(os.Stdout, "  %s upgraded to %s\n", ui.Green("✔"), latest)
	fmt.Fprintf(os.Stdout, "  restart sila to use the new version.\n")
	return nil
}

// RunUpdate performs self-update for backwards compatibility.
func RunUpdate(ui render.UI, jsonOut bool) error {
	return RunUpgrade(ui, UpdateOptions{JSON: jsonOut})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
