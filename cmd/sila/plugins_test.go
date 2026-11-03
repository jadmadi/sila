package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestPluginsCLIUsageAndList(t *testing.T) {
	ui := render.New(os.Stdout, true)

	// 1. Test help display
	outHelp := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"help"})
	})
	if !strings.Contains(outHelp, "sila plugins") {
		t.Errorf("expected usage text, got: %s", outHelp)
	}
	if !strings.Contains(outHelp, "list") || !strings.Contains(outHelp, "init") {
		t.Errorf("expected subcommands in help, got: %s", outHelp)
	}

	// 2. Test list in isolated temp environment
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "bin")
	srcDir := filepath.Join(tempDir, "src")
	_ = os.MkdirAll(binDir, 0755)
	_ = os.MkdirAll(srcDir, 0755)

	t.Setenv("SILA_PLUGINS_BIN_DIR", binDir)
	t.Setenv("SILA_PLUGINS_SRC_DIR", srcDir)
	t.Setenv("XDG_DATA_HOME", tempDir)

	outListEmpty := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"list"})
	})
	if !strings.Contains(outListEmpty, "No plugins found") {
		t.Errorf("expected empty plugins message, got: %s", outListEmpty)
	}

	// 3. Test list --json
	outListJSON := captureStdout(t, func() {
		runPlugins(ui, true, false, []string{"list"})
	})
	if !strings.Contains(outListJSON, "[]") {
		t.Errorf("expected empty JSON array, got: %s", outListJSON)
	}

	// 4. Test list --toon
	outListTOON := captureStdout(t, func() {
		runPlugins(ui, false, true, []string{"list"})
	})
	if !strings.Contains(outListTOON, "plugins[0]:") {
		t.Errorf("expected TOON plugins header, got: %s", outListTOON)
	}
}

func TestPluginsCLILifecycle(t *testing.T) {
	ui := render.New(os.Stdout, true)
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "bin")
	srcDir := filepath.Join(tempDir, "src")
	_ = os.MkdirAll(binDir, 0755)
	_ = os.MkdirAll(srcDir, 0755)

	t.Setenv("SILA_PLUGINS_BIN_DIR", binDir)
	t.Setenv("SILA_PLUGINS_SRC_DIR", srcDir)
	t.Setenv("XDG_DATA_HOME", tempDir)

	// Create dummy go.mod in srcDir
	_ = os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module github.com/jadmadi/sila-plugins\n\ngo 1.24\n"), 0644)

	// 1. Test init
	outInit := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"init", "my-test-tool"})
	})
	if !strings.Contains(outInit, "Plugin scaffolded successfully") {
		t.Errorf("expected successful scaffolding message, got: %s", outInit)
	}

	// 2. Test install
	outInstall := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"install", "my-test-tool"})
	})
	if !strings.Contains(outInstall, "Installed my-test-tool") {
		t.Errorf("expected installed message, got: %s", outInstall)
	}

	// 3. Test list showing installed status
	outList := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"list"})
	})
	if !strings.Contains(outList, "my-test-tool") || !strings.Contains(outList, "installed") {
		t.Errorf("expected list to show my-test-tool installed, got: %s", outList)
	}

	// 4. Test lint
	outLint := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"lint", "my-test-tool"})
	})
	if !strings.Contains(outLint, "conforms cleanly to Sila Plugin Standard") {
		t.Errorf("expected clean lint report, got: %s", outLint)
	}

	// 5. Test uninstall
	outUninstall := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"uninstall", "my-test-tool"})
	})
	if !strings.Contains(outUninstall, "Uninstalled my-test-tool") {
		t.Errorf("expected uninstalled message, got: %s", outUninstall)
	}

	// 6. Test link and unlink
	dummyExe := filepath.Join(tempDir, "custom-standalone")
	_ = os.WriteFile(dummyExe, []byte("#!/bin/sh\necho custom\n"), 0755)

	outLink := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"link", dummyExe})
	})
	if !strings.Contains(outLink, "Linked") {
		t.Errorf("expected linked message, got: %s", outLink)
	}

	outUnlink := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"unlink", "custom-standalone"})
	})
	if !strings.Contains(outUnlink, "Unlinked") {
		t.Errorf("expected unlinked message, got: %s", outUnlink)
	}
}

func TestPluginsCLIActivation(t *testing.T) {
	ui := render.New(os.Stdout, true)
	tempDir := t.TempDir()
	origWd, _ := os.Getwd()
	_ = os.Chdir(tempDir)
	defer func() { _ = os.Chdir(origWd) }()

	// 1. Activate plugin
	outAct := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"activate", "testplugin"})
	})
	if !strings.Contains(outAct, "activated for project") {
		t.Errorf("expected activate success, got: %s", outAct)
	}

	// 2. Activate with --json
	outActJSON := captureStdout(t, func() {
		runPlugins(ui, true, false, []string{"activate", "testplugin"})
	})
	if !strings.Contains(outActJSON, `"active": true`) {
		t.Errorf("expected JSON active: true, got: %s", outActJSON)
	}

	// 3. Deactivate plugin
	outDeact := captureStdout(t, func() {
		runPlugins(ui, false, false, []string{"deactivate", "testplugin"})
	})
	if !strings.Contains(outDeact, "deactivated for project") {
		t.Errorf("expected deactivate success, got: %s", outDeact)
	}

	// 4. Deactivate with --json
	outDeactJSON := captureStdout(t, func() {
		runPlugins(ui, true, false, []string{"deactivate", "testplugin"})
	})
	if !strings.Contains(outDeactJSON, `"active": false`) {
		t.Errorf("expected JSON active: false, got: %s", outDeactJSON)
	}
}
