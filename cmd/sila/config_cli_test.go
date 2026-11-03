package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestConfigPathCLI(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SILA_DB", "")
	customConfig := filepath.Join(tmp, "my_config")
	customData := filepath.Join(tmp, "my_data")
	t.Setenv("XDG_CONFIG_HOME", customConfig)
	t.Setenv("XDG_DATA_HOME", customData)

	ui := render.New(os.Stdout, true)

	// 1. Text output
	outText := captureStdout(t, func() {
		runConfig(ui, false, false, []string{"path"})
	})
	if !strings.Contains(outText, "sila · paths") {
		t.Errorf("expected header 'sila · paths', got:\n%s", outText)
	}
	if !strings.Contains(outText, filepath.Join(customConfig, "sila", "config.json")) {
		t.Errorf("expected config path in output, got:\n%s", outText)
	}
	if !strings.Contains(outText, filepath.Join(customData, "sila", "sila.db")) {
		t.Errorf("expected database path in output, got:\n%s", outText)
	}

	// 2. JSON output
	outJSON := captureStdout(t, func() {
		runConfig(ui, true, false, []string{"path"})
	})
	var pathsMap map[string]string
	if err := json.Unmarshal([]byte(outJSON), &pathsMap); err != nil {
		t.Fatalf("failed to parse JSON paths: %v\nJSON:\n%s", err, outJSON)
	}
	if pathsMap["config_file"] != filepath.Join(customConfig, "sila", "config.json") {
		t.Errorf("expected config_file %s, got %s", filepath.Join(customConfig, "sila", "config.json"), pathsMap["config_file"])
	}
	if pathsMap["sila_db"] != filepath.Join(customData, "sila", "sila.db") {
		t.Errorf("expected sila_db %s, got %s", filepath.Join(customData, "sila", "sila.db"), pathsMap["sila_db"])
	}
}

func TestConfigSchemaCLI(t *testing.T) {
	ui := render.New(os.Stdout, true)
	outJSON := captureStdout(t, func() {
		runConfig(ui, false, false, []string{"schema"})
	})

	var schema map[string]any
	if err := json.Unmarshal([]byte(outJSON), &schema); err != nil {
		t.Fatalf("failed to parse schema output as JSON: %v\nOutput:\n%s", err, outJSON)
	}
	if schema["$schema"] != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("expected Draft-07 schema URI, got: %v", schema["$schema"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		t.Fatalf("expected non-empty properties in schema JSON")
	}
}

func TestConfigExplainCLI(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	ui := render.New(os.Stdout, true)

	// 1. Text explain all
	outAll := captureStdout(t, func() {
		runConfig(ui, false, false, []string{"explain"})
	})
	if !strings.Contains(outAll, "sila · configuration keys reference") {
		t.Errorf("expected header in explain all output, got:\n%s", outAll)
	}
	if !strings.Contains(outAll, "search_provider") {
		t.Errorf("expected search_provider in explain all output")
	}

	// 2. Text explain single key
	outSingle := captureStdout(t, func() {
		runConfig(ui, false, false, []string{"explain", "assist_enabled"})
	})
	if !strings.Contains(outSingle, "sila · config · assist_enabled") {
		t.Errorf("expected header for assist_enabled, got:\n%s", outSingle)
	}
	if !strings.Contains(outSingle, "Type:         boolean") {
		t.Errorf("expected boolean type in single key output")
	}

	// 3. JSON explain single key
	outSingleJSON := captureStdout(t, func() {
		runConfig(ui, true, false, []string{"explain", "brave_api_key"})
	})
	var keyDoc map[string]any
	if err := json.Unmarshal([]byte(outSingleJSON), &keyDoc); err != nil {
		t.Fatalf("failed to parse explain single JSON: %v", err)
	}
	if keyDoc["key"] != "brave_api_key" {
		t.Errorf("expected key brave_api_key, got %v", keyDoc["key"])
	}
	if keyDoc["is_secret"] != true {
		t.Errorf("expected is_secret to be true for brave_api_key")
	}
}

func TestConfigInitCLI(t *testing.T) {
	tmp := t.TempDir()
	customConfig := filepath.Join(tmp, "custom_cfg")
	customData := filepath.Join(tmp, "custom_data")
	t.Setenv("XDG_CONFIG_HOME", customConfig)
	t.Setenv("XDG_DATA_HOME", customData)

	ui := render.New(os.Stdout, true)

	// 1. Initialize global config
	outInit := captureStdout(t, func() {
		runConfig(ui, false, false, []string{"init"})
	})
	if !strings.Contains(outInit, "Initialized global configuration") {
		t.Errorf("expected global init message, got:\n%s", outInit)
	}

	cfgFile := filepath.Join(customConfig, "sila", "config.json")
	if _, err := os.Stat(cfgFile); err != nil {
		t.Fatalf("expected config file created at %s", cfgFile)
	}

	// 2. Second init should report preserved
	outSecond := captureStdout(t, func() {
		runConfig(ui, false, false, []string{"init"})
	})
	if !strings.Contains(outSecond, "Configuration already exists") {
		t.Errorf("expected preserved message on second init, got:\n%s", outSecond)
	}

	// 3. JSON init
	outJSON := captureStdout(t, func() {
		runConfig(ui, true, false, []string{"init"})
	})
	var res map[string]any
	if err := json.Unmarshal([]byte(outJSON), &res); err != nil {
		t.Fatalf("failed to parse init JSON: %v", err)
	}
	if res["path"] != cfgFile {
		t.Errorf("expected path %s, got %v", cfgFile, res["path"])
	}
	if res["created"] != false {
		t.Errorf("expected created=false for existing file")
	}
}
