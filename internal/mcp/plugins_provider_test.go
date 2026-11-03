package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/plugins"
	"github.com/jadmadi/sila/internal/store"
)

// buildMockPluginBinary compiles a tiny Go binary in tempDir that conforms to Sila Plugin Standard.
func buildMockPluginBinary(t *testing.T, tempDir, name string) string {
	t.Helper()
	srcDir := filepath.Join(tempDir, "src", name)
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	goMod := "module mockplugin\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	mainGo := `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "help" && args[1] == "--json" {
		schema := map[string]any{
			"schema_version": "1.0.0",
			"name": "` + name + `",
			"short": "Mock plugin for testing",
			"commands": []map[string]any{
				{
					"name": "check",
					"short": "Run hygiene check",
					"flags": []map[string]any{
						{"name": "fix", "type": "bool", "usage": "Auto-fix issues"},
						{"name": "format", "type": "string", "usage": "Output format"},
					},
				},
				{
					"name": "echo",
					"short": "Echo inputs",
					"flags": []map[string]any{
						{"name": "msg", "type": "string", "usage": "Message to echo"},
					},
				},
			},
		}
		data, _ := json.Marshal(schema)
		fmt.Println(string(data))
		return
	}

	if len(args) > 0 {
		switch args[0] {
		case "check":
			fix := false
			for _, a := range args[1:] {
				if a == "--fix" {
					fix = true
				}
			}
			res := map[string]any{
				"status": "ok",
				"fixed": fix,
			}
			b, _ := json.Marshal(res)
			fmt.Println(string(b))
			return
		case "echo":
			msg := ""
			for _, a := range args[1:] {
				if strings.HasPrefix(a, "--msg=") {
					msg = strings.TrimPrefix(a, "--msg=")
				}
			}
			fmt.Println("ECHO: " + msg)
			return
		case "fail":
			fmt.Fprintln(os.Stderr, "command failed intentional error")
			os.Exit(1)
		}
	}

	fmt.Println("Mock plugin root output")
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}

	binPath := filepath.Join(tempDir, "bin", name)
	if err := os.MkdirAll(filepath.Dir(binPath), 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binPath, ".")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock plugin failed: %v\noutput: %s", err, string(out))
	}

	return binPath
}

func TestPluginToolProviderSynthesisAndExecution(t *testing.T) {
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "bin")
	projectDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	pluginName := "testmock"
	binPath := buildMockPluginBinary(t, tempDir, pluginName)
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("built binary missing: %v", err)
	}

	// 1. Initially plugin is NOT active
	provider := NewPluginToolProvider(projectDir, binDir)
	if len(provider.Tools()) != 0 {
		t.Errorf("expected 0 tools before activation, got %d", len(provider.Tools()))
	}

	// 2. Activate plugin
	if err := plugins.ActivatePlugin(projectDir, pluginName); err != nil {
		t.Fatalf("activate failed: %v", err)
	}

	// 3. Refresh provider
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	tools := provider.Tools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 synthesized tools, got %d", len(tools))
	}

	toolNames := map[string]bool{}
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	if !toolNames["plugin_testmock_check"] {
		t.Errorf("missing tool plugin_testmock_check")
	}
	if !toolNames["plugin_testmock_echo"] {
		t.Errorf("missing tool plugin_testmock_echo")
	}

	// Verify schema properties
	for _, tool := range tools {
		if tool.Name == "plugin_testmock_check" {
			if _, ok := tool.InputSchema.Properties["fix"]; !ok {
				t.Errorf("expected 'fix' property in check tool schema")
			}
			if tool.InputSchema.Properties["fix"].Type != "boolean" {
				t.Errorf("expected 'fix' property to be boolean")
			}
		}
	}

	// 4. Execute tool call (check with --fix)
	ctx := context.Background()
	callArgs := json.RawMessage(`{"fix": true}`)
	res, err := provider.Call(ctx, "plugin_testmock_check", callArgs)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if res.IsError {
		t.Errorf("expected successful call, got isError=true")
	}
	if res.StructuredData == nil || res.StructuredData["fixed"] != true {
		t.Errorf("expected structuredData fixed=true, got %+v", res.StructuredData)
	}

	// 5. Execute tool call (echo with msg)
	echoArgs := json.RawMessage(`{"msg": "hello world"}`)
	echoRes, err := provider.Call(ctx, "plugin_testmock_echo", echoArgs)
	if err != nil {
		t.Fatalf("echo call failed: %v", err)
	}
	if !strings.Contains(echoRes.Text, "ECHO: hello world") {
		t.Errorf("expected 'ECHO: hello world' in text, got %q", echoRes.Text)
	}

	// 6. Unknown tool returns error
	_, err = provider.Call(ctx, "plugin_unknown_tool", nil)
	if err == nil {
		t.Errorf("expected error for unknown tool")
	}

	// 7. Deactivate plugin and refresh
	if err := plugins.DeactivatePlugin(projectDir, pluginName); err != nil {
		t.Fatalf("deactivate failed: %v", err)
	}
	if err := provider.Refresh(ctx); err != nil {
		t.Fatalf("refresh after deactivate failed: %v", err)
	}
	if len(provider.Tools()) != 0 {
		t.Errorf("expected 0 tools after deactivation, got %d", len(provider.Tools()))
	}
}

func TestMCPServerPluginToolsIntegration(t *testing.T) {
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "bin")
	projectDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	pluginName := "myhelper"
	buildMockPluginBinary(t, tempDir, pluginName)

	// Activate plugin
	if err := plugins.ActivatePlugin(projectDir, pluginName); err != nil {
		t.Fatal(err)
	}

	provider := NewPluginToolProvider(projectDir, binDir)

	dbPath := filepath.Join(tempDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Test tools/list over JSON-RPC stdio
	listReq := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	in := strings.NewReader(listReq)
	var out bytes.Buffer

	srv := NewServer(st, in, &out)
	srv.SetPluginToolProvider(provider)

	if err := srv.Run(); err != nil {
		t.Fatalf("srv.Run failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v\noutput: %s", err, out.String())
	}
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}

	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	toolsList, ok := resultMap["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", resultMap["tools"])
	}

	hasPluginTool := false
	for _, toolObj := range toolsList {
		tm, _ := toolObj.(map[string]any)
		if strings.HasPrefix(tm["name"].(string), "plugin_myhelper_") {
			hasPluginTool = true
			break
		}
	}
	if !hasPluginTool {
		t.Errorf("tools/list did not contain plugin_myhelper_* tools")
	}

	// 2. Test tools/call over JSON-RPC stdio
	callReq := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"plugin_myhelper_check","arguments":{"fix":true}}}` + "\n"
	in2 := strings.NewReader(callReq)
	var out2 bytes.Buffer

	srv2 := NewServer(st, in2, &out2)
	srv2.SetPluginToolProvider(provider)

	if err := srv2.Run(); err != nil {
		t.Fatalf("srv2.Run failed: %v", err)
	}

	var callResp JSONRPCResponse
	if err := json.Unmarshal(out2.Bytes(), &callResp); err != nil {
		t.Fatalf("unmarshal call response failed: %v\noutput: %s", err, out2.String())
	}
	if callResp.Error != nil {
		t.Fatalf("tools/call RPC error: %+v", callResp.Error)
	}

	callResult, _ := callResp.Result.(map[string]any)
	if callResult["isError"] == true {
		t.Errorf("expected isError false")
	}
	contentArr, _ := callResult["content"].([]any)
	if len(contentArr) == 0 {
		t.Fatalf("expected content array in tool call result")
	}
	c0, _ := contentArr[0].(map[string]any)
	textVal, _ := c0["text"].(string)
	if !strings.Contains(textVal, `"status":"ok"`) && !strings.Contains(textVal, `"fixed":true`) {
		t.Errorf("unexpected output text: %s", textVal)
	}
}
