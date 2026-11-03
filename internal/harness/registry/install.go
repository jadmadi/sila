package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/harness/contract"
	"github.com/jadmadi/sila/internal/harness/pluginbridge"
)

// Status describes the installation and trust state of one harness.
type Status struct {
	Harness             string          `json:"harness"`
	DisplayName         string          `json:"display_name,omitempty"`
	Kind                Kind            `json:"kind,omitempty"`
	Path                string          `json:"path,omitempty"`
	Scope               string          `json:"scope,omitempty"`
	Supported           bool            `json:"supported"`
	Installed           bool            `json:"installed"`
	Configured          bool            `json:"configured,omitempty"`
	Trusted             bool            `json:"trusted"`
	TrustMode           string          `json:"trust_mode,omitempty"`
	ProjectHooksIgnored bool            `json:"project_hooks_ignored,omitempty"`
	Events              []string        `json:"events,omitempty"`
	Capabilities        map[string]bool `json:"capabilities,omitempty"`
	Limitations         []string        `json:"limitations,omitempty"`
	DocsURL             string          `json:"docs_url,omitempty"`
	Detail              string          `json:"detail,omitempty"`
	CompactionSupported bool            `json:"compaction_supported"`
	CompactionInstalled bool            `json:"compaction_installed"`
	CompactionDetail    string          `json:"compaction_detail,omitempty"`
	BinaryPath          string          `json:"binary_path,omitempty"`
	BinarySource        string          `json:"binary_source,omitempty"`
	WrapperPath         string          `json:"wrapper_path,omitempty"`
	BinaryResolved      bool            `json:"binary_resolved"`
	Surfaces            []SurfaceStatus `json:"surfaces,omitempty"`
}

// InstallResult is the outcome of a harness install or uninstall operation.
type InstallResult struct {
	Harness             string   `json:"harness"`
	Path                string   `json:"path,omitempty"`
	Installed           bool     `json:"installed"`
	Configured          bool     `json:"configured,omitempty"`
	Trusted             bool     `json:"trusted"`
	TrustRequired       bool     `json:"trust_required,omitempty"`
	ProjectHooksIgnored bool     `json:"project_hooks_ignored,omitempty"`
	Skipped             bool     `json:"skipped"`
	Refused             bool     `json:"refused"`
	Files               []string `json:"files,omitempty"`
	Plan                []string `json:"plan,omitempty"`
	Detail              string   `json:"detail,omitempty"`
}

// TrustReceipt records explicit workspace/project/digest approval for a harness.
type TrustReceipt struct {
	Harness    string `json:"harness"`
	Approved   bool   `json:"approved"`
	DigestSHA  string `json:"digest_sha256,omitempty"`
	ApprovedAt string `json:"approved_at,omitempty"`
}

// StatusAll returns the status for all registered harnesses in projectDir.
func StatusAll(projectDir string) map[string]Status {
	out := make(map[string]Status, len(allSpecs))
	for _, sp := range allSpecs {
		out[sp.ID] = StatusFor(sp.ID, projectDir)
	}
	return out
}

// StatusFor inspects the hook/plugin configuration and trust status for one
// harness. Project scope takes precedence; when the project is unconfigured the
// user-scope config under $HOME is inspected and reported with Scope "user".
func StatusFor(harness, projectDir string) Status {
	sp, ok := Lookup(harness)
	if !ok {
		h := NormalizeID(harness)
		return Status{
			Harness:   h,
			Supported: false,
			Detail:    manualWireMessage(h),
		}
	}

	if sp.Kind == KindCommitHook {
		st := baseHarnessStatus(sp)
		st.Path = resolveTargetConfigPath(sp, projectDir)
		return inspectCommitHookStatus(sp, st)
	}

	if sp.Kind == KindPluginHook {
		st := baseHarnessStatus(sp)
		okProject, projectBridge := pluginbridge.IsInstalled(sp.ID, projectDir)
		if okProject {
			return inspectPluginHookStatus(sp, st, projectBridge, "project")
		}
		if homeDir, err := userHomeDir(); err == nil {
			if okUser, userBridge := pluginbridge.IsInstalledUser(sp.ID, homeDir); okUser {
				return inspectPluginHookStatus(sp, st, userBridge, "user")
			}
		}
		st.Path = projectBridge
		st.Detail = "plugin bridge not installed"
		return st
	}

	if !sp.Config.ProjectHooksIgnored {
		projectSt := inspectShellHookStatus(sp, baseHarnessStatus(sp), resolveTargetConfigPath(sp, projectDir), projectDir)
		if projectSt.Configured || projectSt.Installed {
			projectSt.Scope = "project"
			return projectSt
		}
		if homeDir, err := userHomeDir(); err == nil {
			userSt := inspectShellHookStatus(sp, baseHarnessStatus(sp), resolveUserConfigPath(sp, homeDir), homeDir)
			if sp.ID == "antigravity" {
				userSt.Surfaces = antigravitySurfaceStatuses(sp, homeDir)
			}
			if userSt.Configured || userSt.Installed {
				userSt.Scope = "user"
				return userSt
			}
		}
		return projectSt
	}

	// Project hooks are ignored (ZCode): only user scope is meaningful.
	st := baseHarnessStatus(sp)
	st.ProjectHooksIgnored = true
	homeDir, err := userHomeDir()
	if err != nil {
		st.Detail = "project hooks ignored; user home directory is unavailable"
		return st
	}
	userSt := inspectShellHookStatus(sp, st, resolveUserConfigPath(sp, homeDir), homeDir)
	if userSt.Configured || userSt.Installed {
		userSt.Scope = "user"
		userSt.ProjectHooksIgnored = true
		return userSt
	}
	userSt.Scope = ""
	userSt.ProjectHooksIgnored = true
	userSt.Detail = "project hooks ignored by ZCode; run `sila hook install --harness=zcode --user` or configure user hooks"
	return userSt
}

// StatusForUser inspects only the harness user-scope configuration under homeDir.
func StatusForUser(harness, homeDir string) Status {
	sp, ok := Lookup(harness)
	if !ok {
		h := NormalizeID(harness)
		return Status{
			Harness:   h,
			Supported: false,
			Detail:    manualWireMessage(h),
		}
	}

	st := baseHarnessStatus(sp)
	st.Scope = "user"

	if sp.Kind == KindPluginHook {
		okBridge, bridgePath := pluginbridge.IsInstalledUser(sp.ID, homeDir)
		if bridgePath != "" {
			st.Path = bridgePath
		}
		if !okBridge {
			st.Detail = "plugin bridge not installed"
			return st
		}
		return inspectPluginHookStatus(sp, st, bridgePath, "user")
	}
	if sp.Kind == KindCommitHook || len(sp.Config.UserPaths) == 0 {
		st.Detail = fmt.Sprintf("harness %s does not declare a user-level hook config path", sp.ID)
		return st
	}
	if strings.TrimSpace(homeDir) == "" {
		st.Detail = "user home directory is required"
		return st
	}
	st = inspectShellHookStatus(sp, st, resolveUserConfigPath(sp, homeDir), homeDir)
	if sp.ID == "antigravity" {
		st.Surfaces = antigravitySurfaceStatuses(sp, homeDir)
	}
	return st
}

func baseHarnessStatus(sp HarnessSpec) Status {
	caps := make(map[string]bool, len(sp.Capabilities)+2)
	for k, v := range sp.Capabilities {
		caps[string(k)] = v
	}
	caps[string(CapPluginBridge)] = sp.Kind == KindPluginHook
	caps[string(CapShellHook)] = sp.Kind == KindShellHook

	st := Status{
		Harness:             sp.ID,
		DisplayName:         sp.DisplayName,
		Kind:                sp.Kind,
		Supported:           true,
		Trusted:             !sp.Config.TrustRequired,
		TrustMode:           sp.Trust,
		ProjectHooksIgnored: sp.Config.ProjectHooksIgnored,
		Capabilities:        caps,
		Limitations:         append([]string(nil), sp.Notes...),
		DocsURL:             sp.DocsURL,
		CompactionSupported: sp.Capabilities[contract.CapCompaction],
	}
	if st.CompactionSupported {
		switch sp.ID {
		case "claude":
			st.CompactionDetail = "PreCompact marker plus SessionStart source=compact fallback"
		case "cursor":
			st.CompactionDetail = "preCompact only"
		case "devin":
			st.CompactionDetail = "PostCompaction only"
		default:
			st.CompactionDetail = "documented compaction lifecycle events"
		}
	} else {
		st.CompactionDetail = "no documented compaction event; SessionStart fallback only"
	}
	bin := binaryResolution()
	st.BinaryPath = bin.Path
	st.BinarySource = bin.Source
	st.WrapperPath = bin.WrapperPath
	st.BinaryResolved = bin.Resolved
	return st
}

func inspectCommitHookStatus(sp HarnessSpec, st Status) Status {
	data, err := os.ReadFile(st.Path)
	if err != nil {
		st.Detail = "git pre-commit hook not installed"
		return st
	}
	if strings.Contains(string(data), "sila gate") || strings.Contains(string(data), "sila hook") {
		st.Configured = true
		st.Installed = true
		st.Events = []string{"pre-commit"}
		st.Detail = "sila pre-commit gate active"
	} else {
		st.Detail = "foreign git pre-commit hook present"
	}
	return st
}

func inspectPluginHookStatus(sp HarnessSpec, st Status, bridgePath, scope string) Status {
	st.Path = bridgePath
	st.Scope = scope
	st.Configured = true
	st.Installed = true
	for _, ev := range sp.Events {
		st.Events = append(st.Events, ev.NativeName)
	}
	sort.Strings(st.Events)
	st.CompactionInstalled = st.CompactionSupported
	st.Detail = "shared plugin bridge installed"
	return st
}

func inspectShellHookStatus(sp HarnessSpec, st Status, targetPath, baseDir string) Status {
	st.Path = targetPath
	if targetPath == "" {
		st.Detail = fmt.Sprintf("harness %s does not declare a hook config path", sp.ID)
		return st
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		st.Detail = "hook settings file does not exist"
		return st
	}

	if sp.Config.Format == "toml" {
		if strings.Contains(string(data), "sila hook ") {
			st.Configured = true
			for _, ev := range sp.Events {
				if strings.Contains(string(data), ev.NativeName) {
					st.Events = appendUnique(st.Events, ev.NativeName)
				}
			}
			sort.Strings(st.Events)
		} else {
			st.Detail = "no Sila-owned hooks found"
			return st
		}
	} else {
		var root map[string]any
		if err := json.Unmarshal(data, &root); err != nil {
			st.Detail = "hook settings file is not valid JSON"
			return st
		}
		switch sp.Config.Dialect {
		case DialectCursorFlat:
			statusCursorFlat(sp, root, &st)
		case DialectAntigravityNamed:
			if !statusAntigravityNamed(sp, root, &st) {
				return st
			}
		default:
			statusClaudeNested(sp, root, &st)
		}
	}

	if !st.Configured {
		st.Detail = "no Sila-owned hooks found"
		return st
	}

	if sp.Config.TrustRequired {
		st.Trusted = checkTrustApproved(baseDir, sp.ID, data)
		if !st.Trusted {
			st.Installed = false
			st.Detail = fmt.Sprintf("hooks written to %s, but %s trust approval is required before hooks are active", filepath.Base(targetPath), sp.Trust)
			return st
		}
	}

	st.Installed = true
	st.Detail = "installed and active"
	return st
}

func statusClaudeNested(sp HarnessSpec, root map[string]any, st *Status) {
	hooks, _ := root["hooks"].(map[string]any)
	for _, ev := range sp.Events {
		if eventHasSilaHook(hooks[ev.NativeName]) {
			st.Events = appendUnique(st.Events, ev.NativeName)
		}
	}
	sort.Strings(st.Events)
	st.Configured = len(st.Events) > 0
	st.CompactionInstalled = compactionInstalled(sp, func(native string) bool {
		return eventHasSilaHook(hooks[native])
	})
}

func statusCursorFlat(sp HarnessSpec, root map[string]any, st *Status) {
	hooks, _ := root["hooks"].(map[string]any)
	for _, ev := range sp.Events {
		if cursorEventHasSilaHook(hooks[ev.NativeName]) {
			st.Events = appendUnique(st.Events, ev.NativeName)
		}
	}
	sort.Strings(st.Events)
	st.Configured = len(st.Events) > 0
	st.CompactionInstalled = compactionInstalled(sp, func(native string) bool {
		return cursorEventHasSilaHook(hooks[native])
	})
}

func statusAntigravityNamed(sp HarnessSpec, root map[string]any, st *Status) bool {
	group, _ := root["sila"].(map[string]any)
	if enabled, ok := group["enabled"].(bool); ok && !enabled {
		st.Configured = false
		st.Installed = false
		st.Detail = "sila hook group disabled in settings"
		return false
	}
	for _, ev := range sp.Events {
		if antigravityEventHasSilaHook(ev.NativeName, group[ev.NativeName]) {
			st.Events = appendUnique(st.Events, ev.NativeName)
		}
	}
	sort.Strings(st.Events)
	st.Configured = len(st.Events) > 0
	st.CompactionInstalled = compactionInstalled(sp, func(native string) bool {
		return antigravityEventHasSilaHook(native, group[native])
	})
	return true
}

func compactionInstalled(sp HarnessSpec, has func(native string) bool) bool {
	if !sp.Capabilities[contract.CapCompaction] {
		return false
	}
	if pre, ok := sp.Events[contract.PhaseCompactPre]; ok && has(pre.NativeName) {
		return true
	}
	if post, ok := sp.Events[contract.PhaseCompactPost]; ok && has(post.NativeName) {
		return true
	}
	return false
}

// ApproveTrust records explicit trust approval (including SHA-256 digest for
// review-digest harnesses like Codewhale) under .sila/harness-trust/<harness>.json.
func ApproveTrust(harness, projectDir string) error {
	sp, ok := Lookup(harness)
	if !ok {
		return fmt.Errorf("unknown harness %q", harness)
	}
	targetPath := resolveTargetConfigPath(sp, projectDir)
	var digest string
	if data, err := os.ReadFile(targetPath); err == nil {
		sum := sha256.Sum256(data)
		digest = hex.EncodeToString(sum[:])
	}
	rec := TrustReceipt{
		Harness:   sp.ID,
		Approved:  true,
		DigestSHA: digest,
	}
	trustDir := filepath.Join(projectDir, ".sila", "harness-trust")
	if err := os.MkdirAll(trustDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(trustDir, sp.ID+".json"), append(b, '\n'), 0o600)
}

func checkTrustApproved(projectDir, harnessID string, configData []byte) bool {
	trustFile := filepath.Join(projectDir, ".sila", "harness-trust", harnessID+".json")
	raw, err := os.ReadFile(trustFile)
	if err != nil {
		return false
	}
	var rec TrustReceipt
	if err := json.Unmarshal(raw, &rec); err != nil || !rec.Approved {
		return false
	}
	if harnessID == "codewhale" && rec.DigestSHA != "" {
		sum := sha256.Sum256(configData)
		return rec.DigestSHA == hex.EncodeToString(sum[:])
	}
	return true
}

func resolveTargetConfigPath(sp HarnessSpec, projectDir string) string {
	if len(sp.Config.ProjectPaths) > 0 {
		return filepath.Join(projectDir, sp.Config.ProjectPaths[0])
	}
	return ""
}

func resolveUserConfigPath(sp HarnessSpec, homeDir string) string {
	if len(sp.Config.UserPaths) == 0 || strings.TrimSpace(homeDir) == "" {
		return ""
	}
	rel := strings.TrimPrefix(sp.Config.UserPaths[0], "~/")
	return filepath.Join(homeDir, filepath.FromSlash(rel))
}

func userHomeDir() (string, error) {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return home, nil
	}
	return os.UserHomeDir()
}

// Install merges Sila hooks into the harness configuration file in projectDir.
// For ZCode, project-level installation is refused because ZCode ignores project
// hooks; InstallUser must be used or the refusal is returned with project_hooks_ignored=true.
// For trust-required harnesses (Codex, Codewhale, Muse), files are written but
// Installed is false until trust is approved.
func Install(harness, projectDir string, dryRun, replace bool) (InstallResult, error) {
	sp, ok := Lookup(harness)
	if !ok {
		h := NormalizeID(harness)
		return InstallResult{Harness: h, Refused: true, Detail: manualWireMessage(h)}, nil
	}

	if sp.Config.ProjectHooksIgnored {
		return InstallResult{
			Harness:             sp.ID,
			Refused:             true,
			ProjectHooksIgnored: true,
			Detail:              "ZCode ignores project-level hooks; use user config (~/.zcode/cli/config.json) or distribute via a plugin",
		}, nil
	}

	if sp.Kind == KindCommitHook {
		return InstallResult{
			Harness: sp.ID,
			Refused: true,
			Detail:  "use `sila gate install --hook` to manage Git commit hooks",
		}, nil
	}

	if sp.Kind == KindPluginHook {
		pbRes, err := pluginbridge.Install(sp.ID, projectDir, pluginbridge.DefaultBridgeVersion, dryRun)
		return InstallResult{
			Harness:    sp.ID,
			Path:       pbRes.Path,
			Installed:  pbRes.Installed,
			Configured: pbRes.Installed,
			Trusted:    true,
			Skipped:    pbRes.Skipped,
			Refused:    pbRes.Refused,
			Files:      pbRes.Files,
			Plan:       pbRes.Plan,
			Detail:     pbRes.Detail,
		}, err
	}

	path := resolveTargetConfigPath(sp, projectDir)
	if sp.Config.Format == "toml" {
		return installTOML(sp, projectDir, path, dryRun)
	}
	return installJSON(sp, projectDir, path, dryRun, replace)
}

// InstallUser installs Sila hooks into the user-scoped config path (e.g. for ZCode).
func InstallUser(harness, homeDir string, dryRun, replace bool) (InstallResult, error) {
	sp, ok := Lookup(harness)
	if !ok {
		h := NormalizeID(harness)
		return InstallResult{Harness: h, Refused: true, Detail: manualWireMessage(h)}, nil
	}
	if strings.TrimSpace(homeDir) == "" {
		return InstallResult{Harness: sp.ID, Refused: true, Detail: "user home directory is required"}, nil
	}
	if sp.Kind == KindPluginHook {
		pbRes, err := pluginbridge.InstallUser(sp.ID, homeDir, "v1", dryRun)
		return InstallResult{
			Harness:    sp.ID,
			Path:       pbRes.Path,
			Installed:  pbRes.Installed,
			Configured: pbRes.Installed,
			Trusted:    true,
			Skipped:    pbRes.Skipped,
			Refused:    pbRes.Refused,
			Files:      pbRes.Files,
			Plan:       pbRes.Plan,
			Detail:     pbRes.Detail,
		}, err
	}
	if len(sp.Config.UserPaths) == 0 {
		return InstallResult{Harness: sp.ID, Refused: true, Detail: "no user config path declared"}, nil
	}
	path := resolveUserConfigPath(sp, homeDir)
	if sp.Config.Format == "toml" {
		return installTOML(sp, homeDir, path, dryRun)
	}
	return installJSON(sp, homeDir, path, dryRun, replace)
}

// UninstallUser removes only Sila-owned hooks from the harness user-scope
// configuration, preserving all foreign hooks and user settings.
func UninstallUser(harness, homeDir string, dryRun bool) (InstallResult, error) {
	sp, ok := Lookup(harness)
	if !ok {
		h := NormalizeID(harness)
		return InstallResult{Harness: h, Refused: true, Detail: manualWireMessage(h)}, nil
	}
	if strings.TrimSpace(homeDir) == "" {
		return InstallResult{Harness: sp.ID, Refused: true, Detail: "user home directory is required"}, nil
	}
	if sp.Kind == KindPluginHook {
		pbRes, err := pluginbridge.UninstallUser(sp.ID, homeDir, dryRun)
		return InstallResult{
			Harness:   sp.ID,
			Path:      pbRes.Path,
			Installed: pbRes.Installed,
			Refused:   pbRes.Refused,
			Files:     pbRes.Files,
			Plan:      pbRes.Plan,
			Detail:    pbRes.Detail,
		}, err
	}
	if len(sp.Config.UserPaths) == 0 {
		return InstallResult{Harness: sp.ID, Refused: true, Detail: "no user config path declared"}, nil
	}
	return uninstallAtPath(sp, resolveUserConfigPath(sp, homeDir), dryRun)
}

func installTOML(sp HarnessSpec, projectDir, path string, dryRun bool) (InstallResult, error) {
	res := InstallResult{
		Harness:       sp.ID,
		Path:          path,
		TrustRequired: sp.Config.TrustRequired,
	}
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
		if strings.Contains(existing, "sila hook ") {
			trusted := !sp.Config.TrustRequired || checkTrustApproved(projectDir, sp.ID, data)
			res.Skipped = true
			res.Configured = true
			res.Trusted = trusted
			res.Installed = trusted
			res.Detail = "Sila hooks already configured"
			return res, nil
		}
	} else if !os.IsNotExist(err) {
		return res, err
	}

	var b strings.Builder
	if strings.TrimSpace(existing) != "" {
		b.WriteString(strings.TrimRight(existing, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString("# sila:begin hooks\n")
	phases := sortedPhases(sp.Events)
	seenNative := map[string]bool{}
	for _, ph := range phases {
		ev := sp.Events[ph]
		if ev.Command == "" || seenNative[ev.NativeName] {
			continue
		}
		seenNative[ev.NativeName] = true
		b.WriteString("[[hooks]]\n")
		b.WriteString(fmt.Sprintf("event = %q\n", ev.NativeName))
		b.WriteString(fmt.Sprintf("command = %q\n", commandForInstall(ev.Command)))
		b.WriteString("\n")
	}
	b.WriteString("# sila:end hooks\n")
	payload := []byte(b.String())

	if dryRun {
		res.Configured = true
		res.Trusted = !sp.Config.TrustRequired
		res.Installed = !sp.Config.TrustRequired
		res.Plan = append(res.Plan, "write "+path)
		res.Detail = "dry-run: no file written"
		return res, nil
	}

	if err := atomicWriteFile(path, payload, 0o600); err != nil {
		return res, err
	}
	res.Files = append(res.Files, path)
	res.Configured = true
	res.Trusted = !sp.Config.TrustRequired || checkTrustApproved(projectDir, sp.ID, payload)
	res.Installed = res.Trusted
	if !res.Trusted {
		res.Detail = fmt.Sprintf("hooks written to %s; %s approval required before active", path, sp.Trust)
	} else {
		res.Detail = "installed"
	}
	return res, nil
}

func installJSON(sp HarnessSpec, projectDir, path string, dryRun, replace bool) (InstallResult, error) {
	res := InstallResult{
		Harness:       sp.ID,
		Path:          path,
		TrustRequired: sp.Config.TrustRequired,
	}
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return InstallResult{
				Harness: sp.ID,
				Path:    path,
				Refused: true,
				Detail:  "existing settings file is not valid JSON; refusing to overwrite",
			}, nil
		}
	} else if !os.IsNotExist(err) {
		return res, err
	}

	var changed bool
	var refusal *InstallResult
	switch sp.Config.Dialect {
	case DialectCursorFlat:
		changed, refusal = installCursorFlat(sp, root, replace)
	case DialectAntigravityNamed:
		changed, refusal = installAntigravityNamed(sp, root, replace)
	default:
		changed, refusal = installClaudeNested(sp, root, replace)
	}
	if refusal != nil {
		refusal.Path = path
		return *refusal, nil
	}

	if !changed {
		raw, _ := json.Marshal(root)
		trusted := !sp.Config.TrustRequired || checkTrustApproved(projectDir, sp.ID, raw)
		res.Configured = true
		res.Trusted = trusted
		res.Installed = trusted
		res.Skipped = true
		res.Detail = "Sila hooks already installed"
		return res, nil
	}

	if dryRun {
		res.Configured = true
		res.Trusted = !sp.Config.TrustRequired
		res.Installed = !sp.Config.TrustRequired
		res.Skipped = false
		res.Plan = append(res.Plan, "write "+path)
		res.Detail = "dry-run: no file written"
		return res, nil
	}

	data, err := marshalIndentedJSON(root)
	if err != nil {
		return res, err
	}
	if err := atomicWriteFile(path, data, 0o600); err != nil {
		return res, err
	}
	res.Files = append(res.Files, path)
	res.Configured = true
	res.Skipped = false
	res.Trusted = !sp.Config.TrustRequired || checkTrustApproved(projectDir, sp.ID, data)
	res.Installed = res.Trusted
	if !res.Trusted {
		res.Detail = fmt.Sprintf("hooks written to %s; %s approval required before active", path, sp.Trust)
	} else {
		res.Detail = "installed"
	}
	return res, nil
}

func installClaudeNested(sp HarnessSpec, root map[string]any, replace bool) (bool, *InstallResult) {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok && root["hooks"] != nil {
		if !replace {
			return false, &InstallResult{
				Harness: sp.ID,
				Refused: true,
				Detail:  "existing hooks key is not an object; use --replace to replace it",
			}
		}
		hooks = map[string]any{}
	}
	if hooks == nil {
		hooks = map[string]any{}
	}

	changed := false
	seenNative := map[string]bool{}
	for _, ph := range sortedPhases(sp.Events) {
		ev := sp.Events[ph]
		if ev.Command == "" || seenNative[ev.NativeName] {
			continue
		}
		seenNative[ev.NativeName] = true
		entries := toSlice(hooks[ev.NativeName])
		if replace {
			entries, _ = stripSilaEntries(entries)
		} else if containsCommand(entries, "sila hook ") {
			continue
		}
		entry := map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": commandForInstall(ev.Command),
				},
			},
		}
		if ev.Matcher != "" {
			entry["matcher"] = ev.Matcher
		}
		entries = append(entries, entry)
		hooks[ev.NativeName] = entries
		changed = true
	}
	root["hooks"] = hooks
	return changed, nil
}

func installCursorFlat(sp HarnessSpec, root map[string]any, replace bool) (bool, *InstallResult) {
	changed := false
	if root["version"] == nil {
		root["version"] = 1
		changed = true
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok && root["hooks"] != nil {
		if !replace {
			return false, &InstallResult{
				Harness: sp.ID,
				Refused: true,
				Detail:  "existing hooks key is not an object; use --replace to replace it",
			}
		}
		hooks = map[string]any{}
	}
	if hooks == nil {
		hooks = map[string]any{}
	}

	seenNative := map[string]bool{}
	for _, ph := range sortedPhases(sp.Events) {
		ev := sp.Events[ph]
		if ev.Command == "" || seenNative[ev.NativeName] {
			continue
		}
		seenNative[ev.NativeName] = true
		entries := toSlice(hooks[ev.NativeName])
		if !replace && cursorEventHasCommand(entries, ev.Command) {
			continue
		}
		kept, removed := stripSilaEntries(entries)
		if removed {
			changed = true
		}
		handler := map[string]any{
			"command": commandForInstall(ev.Command),
			"timeout": 10,
		}
		if ev.Matcher != "" {
			handler["matcher"] = ev.Matcher
		}
		hooks[ev.NativeName] = append(kept, handler)
		changed = true
	}
	root["hooks"] = hooks
	return changed, nil
}

func installAntigravityNamed(sp HarnessSpec, root map[string]any, replace bool) (bool, *InstallResult) {
	group, ok := root["sila"].(map[string]any)
	if !ok && root["sila"] != nil {
		if !replace {
			return false, &InstallResult{
				Harness: sp.ID,
				Refused: true,
				Detail:  "existing sila hook group is not an object; use --replace to replace it",
			}
		}
		group = nil
	}
	if group == nil {
		group = map[string]any{}
	}
	if _, hasEnabled := group["enabled"]; !hasEnabled {
		group["enabled"] = true
	}

	changed := false
	if stripLegacySilaHooks(root) {
		changed = true
	}

	seenNative := map[string]bool{}
	for _, ph := range sortedPhases(sp.Events) {
		ev := sp.Events[ph]
		if ev.Command == "" || seenNative[ev.NativeName] {
			continue
		}
		seenNative[ev.NativeName] = true
		entries := toSlice(group[ev.NativeName])
		if !replace && antigravityEventHasCommand(ev.NativeName, entries, ev.Command) {
			continue
		}
		kept, removed := stripSilaEntries(entries)
		if removed {
			changed = true
		}
		handler := map[string]any{
			"type":    "command",
			"command": commandForInstall(ev.Command),
			"timeout": 10,
		}
		if antigravityEventUsesMatcherGroup(ev.NativeName) {
			matcher := ev.Matcher
			if matcher == "" {
				matcher = "*"
			}
			kept = append(kept, map[string]any{
				"matcher": matcher,
				"hooks":   []any{handler},
			})
		} else {
			kept = append(kept, handler)
		}
		group[ev.NativeName] = kept
		changed = true
	}
	root["sila"] = group
	return changed, nil
}

// stripLegacySilaHooks removes Sila-owned entries that older installers wrote to
// Antigravity's top-level "hooks" map (the Claude-nested dialect), deleting the
// map when nothing else remains. It reports whether anything was removed.
func stripLegacySilaHooks(root map[string]any) bool {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return false
	}
	removed := false
	for event, raw := range hooks {
		entries, didRemove := stripSilaEntries(toSlice(raw))
		if !didRemove {
			continue
		}
		removed = true
		if len(entries) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = entries
		}
	}
	if !removed {
		return false
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	return true
}

// Uninstall removes only Sila-owned hooks from the harness configuration file,
// preserving all foreign hooks and user settings.
func Uninstall(harness, projectDir string, dryRun bool) (InstallResult, error) {
	sp, ok := Lookup(harness)
	if !ok {
		h := NormalizeID(harness)
		return InstallResult{Harness: h, Refused: true, Detail: manualWireMessage(h)}, nil
	}
	if sp.Config.ProjectHooksIgnored {
		return InstallResult{
			Harness:             sp.ID,
			Refused:             true,
			ProjectHooksIgnored: true,
			Detail:              "ZCode ignores project-level hooks; nothing to uninstall in project",
		}, nil
	}
	if sp.Kind == KindPluginHook {
		pbRes, err := pluginbridge.Uninstall(sp.ID, projectDir, dryRun)
		return InstallResult{
			Harness:   sp.ID,
			Path:      pbRes.Path,
			Installed: pbRes.Installed,
			Refused:   pbRes.Refused,
			Files:     pbRes.Files,
			Plan:      pbRes.Plan,
			Detail:    pbRes.Detail,
		}, err
	}
	return uninstallAtPath(sp, resolveTargetConfigPath(sp, projectDir), dryRun)
}

// uninstallAtPath removes Sila-owned hooks from one resolved config file.
// Project Uninstall and user-scope UninstallUser share this dialect-aware path.
func uninstallAtPath(sp HarnessSpec, path string, dryRun bool) (InstallResult, error) {
	res := InstallResult{Harness: sp.ID, Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			res.Detail = "no settings file"
			return res, nil
		}
		return res, err
	}

	if sp.Config.Format == "toml" {
		content := string(data)
		startIdx := strings.Index(content, "# sila:begin hooks\n")
		endIdx := strings.Index(content, "# sila:end hooks\n")
		if startIdx == -1 || endIdx == -1 || endIdx < startIdx {
			res.Detail = "no Sila-owned hooks found"
			return res, nil
		}
		updated := strings.TrimSpace(content[:startIdx] + content[endIdx+len("# sila:end hooks\n"):])
		if updated != "" {
			updated += "\n"
		}
		res.Installed = true
		if dryRun {
			res.Plan = append(res.Plan, "write "+path)
			res.Detail = "dry-run: no file written"
			return res, nil
		}
		if err := atomicWriteFile(path, []byte(updated), 0o600); err != nil {
			return res, err
		}
		res.Files = append(res.Files, path)
		return res, nil
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return InstallResult{
			Harness: sp.ID,
			Path:    path,
			Refused: true,
			Detail:  "existing settings file is not valid JSON; refusing to modify",
		}, nil
	}
	if _, ok := root["hooks"].(map[string]any); !ok && sp.Config.Dialect != DialectAntigravityNamed {
		res.Detail = "no hooks to remove"
		return res, nil
	}
	var removed bool
	switch sp.Config.Dialect {
	case DialectAntigravityNamed:
		removed = uninstallAntigravityNamed(root)
	default:
		removed = uninstallJSONHooks(root)
	}
	if !removed {
		res.Detail = "no Sila-owned hooks found"
		return res, nil
	}
	res.Installed = true
	if dryRun {
		res.Plan = append(res.Plan, "write "+path)
		res.Detail = "dry-run: no file written"
		return res, nil
	}
	outBytes, err := marshalIndentedJSON(root)
	if err != nil {
		return res, err
	}
	if err := atomicWriteFile(path, outBytes, 0o600); err != nil {
		return res, err
	}
	res.Files = append(res.Files, path)
	return res, nil
}

func sortedPhases(events map[contract.Phase]EventSpec) []contract.Phase {
	phases := make([]contract.Phase, 0, len(events))
	for p := range events {
		phases = append(phases, p)
	}
	sort.Slice(phases, func(i, j int) bool { return phases[i] < phases[j] })
	return phases
}

func marshalIndentedJSON(root map[string]any) ([]byte, error) {
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func atomicWriteFile(path string, data []byte, defaultMode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := defaultMode
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".sila-hook-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
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

func manualWireMessage(harness string) string {
	return fmt.Sprintf("%s hooks are not documented in this build; run `sila hook session-start --json` and `sila hook stop --json` from the harness's own hook configuration", harness)
}

func toSlice(v any) []any {
	if v == nil {
		return nil
	}
	if arr, ok := v.([]any); ok {
		return append([]any(nil), arr...)
	}
	return []any{v}
}

func containsCommand(entries []any, prefix string) bool {
	for _, entry := range entries {
		if commandContains(entry, prefix) {
			return true
		}
	}
	return false
}

func eventHasSilaHook(raw any) bool {
	return containsCommand(toSlice(raw), "sila hook ")
}

func cursorEventHasSilaHook(raw any) bool {
	return cursorEventHasCommand(toSlice(raw), "sila hook ")
}

// cursorEventHasCommand matches only flat handlers with a top-level command
// field, so a legacy Claude-nested wrapper is never reported as installed.
func cursorEventHasCommand(entries []any, cmd string) bool {
	for _, entry := range entries {
		if handlerHasCommand(entry, cmd) {
			return true
		}
	}
	return false
}

func antigravityEventUsesMatcherGroup(nativeName string) bool {
	switch nativeName {
	case "PreToolUse", "PostToolUse":
		return true
	}
	return false
}

func antigravityEventHasCommand(native string, entries []any, cmd string) bool {
	if antigravityEventUsesMatcherGroup(native) {
		for _, rawGroup := range entries {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				continue
			}
			for _, handler := range toSlice(group["hooks"]) {
				if handlerHasCommand(handler, cmd) {
					return true
				}
			}
		}
		return false
	}
	return cursorEventHasCommand(entries, cmd)
}

func antigravityEventHasSilaHook(native string, raw any) bool {
	return antigravityEventHasCommand(native, toSlice(raw), "sila hook ")
}

func handlerHasCommand(raw any, cmd string) bool {
	entry, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	command, _ := entry["command"].(string)
	if command == "" {
		return false
	}
	if cmd == "sila hook " {
		return isSilaHookCommand(command)
	}
	return sameHookCommand(command, cmd)
}

// commandBinary returns the first token, respecting single/double quotes.
func commandBinary(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if cmd[0] == '"' || cmd[0] == '\'' {
		quote := cmd[0]
		for i := 1; i < len(cmd); i++ {
			if cmd[i] == quote {
				return cmd[1:i]
			}
		}
		return cmd
	}
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		return cmd[:i]
	}
	return cmd
}

// isSilaHookCommand recognizes legacy (sila, sila-hook) and resolved
// (absolute/quoted binary) hook commands while rejecting foreign hooks.
func isSilaHookCommand(cmd string) bool {
	base := strings.ToLower(filepath.Base(commandBinary(cmd)))
	switch base {
	case "sila", "sila.exe", "sila-hook", "sila-hook.cmd":
	default:
		return false
	}
	return strings.Contains(cmd, " hook ") || strings.HasPrefix(strings.TrimSpace(cmd), "sila hook ")
}

func hookCommandSuffix(cmd string) string {
	if i := strings.Index(cmd, " hook "); i >= 0 {
		return cmd[i+1:]
	}
	return ""
}

func sameHookCommand(a, b string) bool {
	if strings.TrimSpace(a) == strings.TrimSpace(b) {
		return true
	}
	if !isSilaHookCommand(a) || !isSilaHookCommand(b) {
		return false
	}
	return hookCommandSuffix(a) == hookCommandSuffix(b)
}

func stripSilaEntries(entries []any) ([]any, bool) {
	kept := make([]any, 0, len(entries))
	removed := false
	for _, entry := range entries {
		if commandContains(entry, "sila hook ") {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	return kept, removed
}

func uninstallJSONHooks(root map[string]any) bool {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		return false
	}
	removed := false
	for event, raw := range hooks {
		entries, didRemove := stripSilaEntries(toSlice(raw))
		if !didRemove {
			continue
		}
		removed = true
		if len(entries) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = entries
		}
	}
	root["hooks"] = hooks
	return removed
}

func uninstallAntigravityNamed(root map[string]any) bool {
	removed := false
	if group, ok := root["sila"].(map[string]any); ok {
		for key, raw := range group {
			if key == "enabled" {
				continue
			}
			entries, didRemove := stripSilaEntries(toSlice(raw))
			if !didRemove {
				continue
			}
			removed = true
			if len(entries) == 0 {
				delete(group, key)
			} else {
				group[key] = entries
			}
		}
		if len(group) == 1 {
			if _, hasEnabled := group["enabled"]; hasEnabled {
				delete(root, "sila")
			}
		}
	}
	if stripLegacySilaHooks(root) {
		removed = true
	}
	return removed
}

func commandContains(v any, prefix string) bool {
	switch value := v.(type) {
	case map[string]any:
		if cmd, ok := value["command"].(string); ok {
			if prefix == "sila hook " {
				if isSilaHookCommand(cmd) {
					return true
				}
			} else if strings.Contains(cmd, prefix) {
				return true
			}
		}
		for _, child := range value {
			if commandContains(child, prefix) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if commandContains(child, prefix) {
				return true
			}
		}
	}
	return false
}

func appendUnique(list []string, item string) []string {
	for _, existing := range list {
		if existing == item {
			return list
		}
	}
	return append(list, item)
}
