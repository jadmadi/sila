package pluginbridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BridgeFilename is the canonical filename for the shared JS plugin bridge.
const BridgeFilename = "sila-bridge.js"

// InstallResult describes the outcome of a plugin bridge install or uninstall.
type InstallResult struct {
	Harness   string   `json:"harness"`
	Path      string   `json:"path,omitempty"`
	Installed bool     `json:"installed"`
	Skipped   bool     `json:"skipped"`
	Refused   bool     `json:"refused"`
	Files     []string `json:"files,omitempty"`
	Plan      []string `json:"plan,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

// Install writes the rendered shared JS bridge for OpenCode or MiMoCode.
//   - For OpenCode: writes `.opencode/plugins/sila-bridge.js` (mode 0644),
//     idempotently preserving any foreign files in `.opencode/plugins/`.
//   - For MiMoCode: because the official `/plugins` page returned 404, local path
//     semantics in `mimocode.json` are refused with `Refused: true` and the docs
//     URL unless `SILA_MIMOCODE_PLUGIN_VERIFIED=1` or an existing `mimocode.json`
//     with a `plugin` array is present in projectDir.
func Install(harness, projectDir, version string, dryRun bool) (InstallResult, error) {
	src, err := Render(harness, version)
	if err != nil {
		return InstallResult{Harness: harness, Refused: true, Detail: err.Error()}, nil
	}
	if normalizeHarness(harness) == "mimocode" {
		return installMiMoCode(projectDir, src, dryRun)
	}
	return installOpenCode(projectDir, src, dryRun)
}

// InstallUser writes the rendered shared JS bridge into the harness user config
// (`~/.config/opencode/plugins/sila-bridge.js` or `~/.mimocode/mimocode.json`).
func InstallUser(harness, homeDir, version string, dryRun bool) (InstallResult, error) {
	src, err := Render(harness, version)
	if err != nil {
		return InstallResult{Harness: harness, Refused: true, Detail: err.Error()}, nil
	}
	h := normalizeHarness(harness)
	if strings.TrimSpace(homeDir) == "" {
		return InstallResult{Harness: h, Refused: true, Detail: "user home directory is required for user-scope plugin install"}, nil
	}
	switch h {
	case "opencode":
		return installOpenCodeBridge(userOpenCodeBridgePath(homeDir), src, dryRun)
	case "mimocode":
		cfgPath, bridgePath := userMiMoCodePaths(homeDir)
		return installMiMoCodeBridge(cfgPath, bridgePath, userMiMoCodePluginEntry(), src, dryRun)
	default:
		return InstallResult{Harness: harness, Refused: true, Detail: fmt.Sprintf("unsupported plugin harness %q", harness)}, nil
	}
}

func normalizeHarness(harness string) string {
	h := strings.ToLower(strings.TrimSpace(harness))
	if h == "open-code" {
		return "opencode"
	}
	if h == "mimo-code" || h == "mimo" {
		return "mimocode"
	}
	return h
}

func userOpenCodeBridgePath(homeDir string) string {
	return filepath.Join(homeDir, ".config", "opencode", "plugins", BridgeFilename)
}

func userMiMoCodePaths(homeDir string) (cfgPath, bridgePath string) {
	return filepath.Join(homeDir, ".mimocode", "mimocode.json"),
		filepath.Join(homeDir, ".mimocode", "plugins", BridgeFilename)
}

func userMiMoCodePluginEntry() string {
	return "./plugins/" + BridgeFilename
}

func installOpenCode(projectDir, src string, dryRun bool) (InstallResult, error) {
	return installOpenCodeBridge(filepath.Join(projectDir, ".opencode", "plugins", BridgeFilename), src, dryRun)
}

func installOpenCodeBridge(path, src string, dryRun bool) (InstallResult, error) {
	res := InstallResult{Harness: "opencode", Path: path}

	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == src {
			res.Installed = true
			res.Skipped = true
			res.Detail = "opencode plugin bridge already current"
			return res, nil
		}
	} else if !os.IsNotExist(err) {
		return res, err
	}

	if dryRun {
		res.Installed = true
		res.Plan = append(res.Plan, "write "+path)
		res.Detail = "dry-run: no file written"
		return res, nil
	}

	if err := atomicWrite0644(path, []byte(src)); err != nil {
		return res, err
	}
	res.Installed = true
	res.Files = append(res.Files, path)
	res.Detail = "installed " + path
	return res, nil
}

func installMiMoCode(projectDir, src string, dryRun bool) (InstallResult, error) {
	return installMiMoCodeBridge(
		filepath.Join(projectDir, "mimocode.json"),
		filepath.Join(projectDir, ".mimocode", "plugins", BridgeFilename),
		"./.mimocode/plugins/"+BridgeFilename,
		src,
		dryRun,
	)
}

func installMiMoCodeBridge(cfgPath, bridgePath, pluginEntry, src string, dryRun bool) (InstallResult, error) {
	res := InstallResult{Harness: "mimocode", Path: bridgePath}

	verifiedEnv := strings.TrimSpace(os.Getenv("SILA_MIMOCODE_PLUGIN_VERIFIED")) == "1"
	cfgData, cfgErr := os.ReadFile(cfgPath)
	var cfgRoot map[string]any
	hasPluginArray := false
	if cfgErr == nil {
		if err := json.Unmarshal(cfgData, &cfgRoot); err != nil {
			return InstallResult{
				Harness: "mimocode",
				Path:    cfgPath,
				Refused: true,
				Detail:  "existing mimocode.json is not valid JSON; refusing to overwrite",
			}, nil
		}
		if _, ok := cfgRoot["plugin"].([]any); ok {
			hasPluginArray = true
		}
	}

	if !verifiedEnv && !hasPluginArray {
		return InstallResult{
			Harness: "mimocode",
			Path:    cfgPath,
			Refused: true,
			Detail:  "unsupported: MiMoCode plugin path semantics are not verified on this machine (https://mimo.xiaomi.com/mimocode/config-files); create mimocode.json with a \"plugin\": [] array or set SILA_MIMOCODE_PLUGIN_VERIFIED=1",
		}, nil
	}

	if cfgRoot == nil {
		cfgRoot = map[string]any{}
	}
	plugins, _ := cfgRoot["plugin"].([]any)
	hasBridgeEntry := false
	for _, item := range plugins {
		if s, ok := item.(string); ok && strings.Contains(s, BridgeFilename) {
			hasBridgeEntry = true
			break
		}
	}

	existingBridge, bridgeErr := os.ReadFile(bridgePath)
	bridgeCurrent := bridgeErr == nil && string(existingBridge) == src
	if bridgeCurrent && hasBridgeEntry {
		res.Installed = true
		res.Skipped = true
		res.Detail = "mimocode plugin bridge already current"
		return res, nil
	}

	if dryRun {
		res.Installed = true
		if !bridgeCurrent {
			res.Plan = append(res.Plan, "write "+bridgePath)
		}
		if !hasBridgeEntry {
			res.Plan = append(res.Plan, "write "+cfgPath)
		}
		res.Detail = "dry-run: no file written"
		return res, nil
	}

	if !bridgeCurrent {
		if err := atomicWrite0644(bridgePath, []byte(src)); err != nil {
			return res, err
		}
		res.Files = append(res.Files, bridgePath)
	}
	if !hasBridgeEntry {
		plugins = append(plugins, pluginEntry)
		cfgRoot["plugin"] = plugins
		outJSON, err := json.MarshalIndent(cfgRoot, "", "  ")
		if err != nil {
			return res, err
		}
		if err := atomicWrite0644(cfgPath, append(outJSON, '\n')); err != nil {
			return res, err
		}
		res.Files = append(res.Files, cfgPath)
	}
	res.Installed = true
	res.Detail = "installed " + bridgePath
	return res, nil
}

// Uninstall removes only the Sila bridge file and config entry, preserving all
// foreign plugins and user settings.
func Uninstall(harness, projectDir string, dryRun bool) (InstallResult, error) {
	switch normalizeHarness(harness) {
	case "opencode":
		return uninstallOpenCodeBridge(filepath.Join(projectDir, ".opencode", "plugins", BridgeFilename), dryRun)
	case "mimocode":
		return uninstallMiMoCodeBridge(
			filepath.Join(projectDir, "mimocode.json"),
			filepath.Join(projectDir, ".mimocode", "plugins", BridgeFilename),
			dryRun,
		)
	default:
		return InstallResult{Harness: harness, Refused: true, Detail: fmt.Sprintf("unsupported plugin harness %q", harness)}, nil
	}
}

// UninstallUser removes only the Sila user-scope bridge file and config entry,
// preserving all foreign plugins and user settings.
func UninstallUser(harness, homeDir string, dryRun bool) (InstallResult, error) {
	if strings.TrimSpace(homeDir) == "" {
		return InstallResult{Harness: normalizeHarness(harness), Refused: true, Detail: "user home directory is required for user-scope plugin uninstall"}, nil
	}
	switch normalizeHarness(harness) {
	case "opencode":
		return uninstallOpenCodeBridge(userOpenCodeBridgePath(homeDir), dryRun)
	case "mimocode":
		cfgPath, bridgePath := userMiMoCodePaths(homeDir)
		return uninstallMiMoCodeBridge(cfgPath, bridgePath, dryRun)
	default:
		return InstallResult{Harness: harness, Refused: true, Detail: fmt.Sprintf("unsupported plugin harness %q", harness)}, nil
	}
}

func uninstallOpenCodeBridge(path string, dryRun bool) (InstallResult, error) {
	res := InstallResult{Harness: "opencode", Path: path}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			res.Detail = "no plugin bridge file"
			return res, nil
		}
		return res, err
	}
	res.Installed = true
	if dryRun {
		res.Plan = append(res.Plan, "remove "+path)
		res.Detail = "dry-run: no file removed"
		return res, nil
	}
	if err := os.Remove(path); err != nil {
		return res, err
	}
	res.Files = append(res.Files, path)
	res.Detail = "removed " + path
	return res, nil
}

func uninstallMiMoCodeBridge(cfgPath, bridgePath string, dryRun bool) (InstallResult, error) {
	res := InstallResult{Harness: "mimocode", Path: bridgePath}
	removedAny := false

	if _, err := os.Stat(bridgePath); err == nil {
		removedAny = true
		if dryRun {
			res.Plan = append(res.Plan, "remove "+bridgePath)
		} else {
			if err := os.Remove(bridgePath); err != nil {
				return res, err
			}
			res.Files = append(res.Files, bridgePath)
		}
	}

	if cfgData, err := os.ReadFile(cfgPath); err == nil {
		var cfgRoot map[string]any
		if err := json.Unmarshal(cfgData, &cfgRoot); err != nil {
			return InstallResult{
				Harness: "mimocode",
				Path:    cfgPath,
				Refused: true,
				Detail:  "existing mimocode.json is not valid JSON; refusing to modify",
			}, nil
		}
		if plugins, ok := cfgRoot["plugin"].([]any); ok {
			filtered := make([]any, 0, len(plugins))
			changed := false
			for _, item := range plugins {
				if s, ok := item.(string); ok && strings.Contains(s, BridgeFilename) {
					changed = true
					removedAny = true
					continue
				}
				filtered = append(filtered, item)
			}
			if changed {
				cfgRoot["plugin"] = filtered
				if dryRun {
					res.Plan = append(res.Plan, "write "+cfgPath)
				} else {
					outJSON, err := json.MarshalIndent(cfgRoot, "", "  ")
					if err != nil {
						return res, err
					}
					if err := atomicWrite0644(cfgPath, append(outJSON, '\n')); err != nil {
						return res, err
					}
					res.Files = append(res.Files, cfgPath)
				}
			}
		}
	}

	if !removedAny {
		res.Detail = "no Sila plugin bridge found"
		return res, nil
	}
	res.Installed = true
	if dryRun {
		res.Detail = "dry-run: no file modified"
	} else {
		res.Detail = "removed mimocode plugin bridge"
	}
	return res, nil
}

// IsInstalled checks whether the plugin bridge is present for opencode or mimocode.
func IsInstalled(harness, projectDir string) (bool, string) {
	switch normalizeHarness(harness) {
	case "opencode":
		return isInstalledOpenCodeBridge(filepath.Join(projectDir, ".opencode", "plugins", BridgeFilename))
	case "mimocode":
		return isInstalledMiMoCodeBridge(
			filepath.Join(projectDir, "mimocode.json"),
			filepath.Join(projectDir, ".mimocode", "plugins", BridgeFilename),
		)
	default:
		return false, ""
	}
}

// IsInstalledUser checks whether the user-scope plugin bridge is present.
func IsInstalledUser(harness, homeDir string) (bool, string) {
	if strings.TrimSpace(homeDir) == "" {
		return false, ""
	}
	switch normalizeHarness(harness) {
	case "opencode":
		return isInstalledOpenCodeBridge(userOpenCodeBridgePath(homeDir))
	case "mimocode":
		cfgPath, bridgePath := userMiMoCodePaths(homeDir)
		return isInstalledMiMoCodeBridge(cfgPath, bridgePath)
	default:
		return false, ""
	}
}

func isInstalledOpenCodeBridge(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, path
	}
	return strings.Contains(string(data), "SilaBridge"), path
}

func isInstalledMiMoCodeBridge(cfgPath, bridgePath string) (bool, string) {
	data, err := os.ReadFile(bridgePath)
	if err != nil || !strings.Contains(string(data), "SilaBridge") {
		return false, bridgePath
	}
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(cfgData), BridgeFilename) {
		return false, bridgePath
	}
	return true, bridgePath
}

func atomicWrite0644(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sila-bridge-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
