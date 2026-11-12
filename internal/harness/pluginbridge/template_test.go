package pluginbridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderDeterministicAndValidJS(t *testing.T) {
	for name, raw := range map[string]string{
		"opencode": RawTemplate(),
		"mimocode": RawMiMoCodeTemplate(),
	} {
		if !strings.Contains(raw, "{{HARNESS}}") || !strings.Contains(raw, "{{VERSION}}") {
			t.Fatalf("%s template missing {{HARNESS}} or {{VERSION}} placeholders", name)
		}
	}

	oc1, err := Render("opencode", "v1.2.3")
	if err != nil {
		t.Fatalf("Render opencode failed: %v", err)
	}
	oc2, err := Render("open-code", "v1.2.3")
	if err != nil {
		t.Fatalf("Render open-code failed: %v", err)
	}
	if oc1 != oc2 {
		t.Fatalf("Render is not deterministic across alias open-code")
	}
	if strings.Contains(oc1, "{{HARNESS}}") || strings.Contains(oc1, "{{VERSION}}") {
		t.Fatalf("rendered opencode bridge still has unresolved placeholders")
	}
	if !strings.Contains(oc1, `const HARNESS = "opencode";`) {
		t.Fatalf("expected HARNESS = opencode in rendered output")
	}

	mc, err := Render("mimocode", "v1.2.3")
	if err != nil {
		t.Fatalf("Render mimocode failed: %v", err)
	}
	if !strings.Contains(mc, `const HARNESS = "mimocode";`) {
		t.Fatalf("expected HARNESS = mimocode in rendered output")
	}

	if _, err := Render("claude", "v1.2.3"); err == nil {
		t.Fatalf("expected error rendering plugin bridge for shell-hook harness claude")
	}

	// The OpenCode artifact must be V2 only: no MiMoCode or V1 code paths.
	if !strings.Contains(oc1, BridgeAPIMarkerV2) {
		t.Fatalf("rendered opencode bridge is missing the V2 API marker %q", BridgeAPIMarkerV2)
	}
	for _, forbidden := range []string{
		BridgeAPIMarkerV1,
		"SilaBridge = async",
		"loadedInstances",
		"tool.execute.before",
		"tool.execute.after",
		"experimental.session.compacting",
		`HARNESS === "opencode"`,
		"server: SilaBridge",
	} {
		if strings.Contains(oc1, forbidden) {
			t.Fatalf("rendered opencode bridge still contains V1/MiMoCode code %q", forbidden)
		}
	}
	if !strings.Contains(oc1, "id: PLUGIN_ID,") || !strings.Contains(oc1, "setup: setupV2,") {
		t.Fatalf("rendered opencode bridge must define the V2 id and setup")
	}

	// MiMoCode keeps the V1 function and must not carry the V2 definition.
	if !strings.Contains(mc, BridgeAPIMarkerV1) {
		t.Fatalf("rendered mimocode bridge is missing the V1 API marker %q", BridgeAPIMarkerV1)
	}
	if !strings.Contains(mc, "export const SilaBridge = async") {
		t.Fatalf("rendered mimocode bridge must export the V1 plugin function")
	}
	if strings.Contains(mc, "setupV2") {
		t.Fatalf("rendered mimocode bridge must not contain the OpenCode V2 setup")
	}

	// Verify no forbidden eval, network, or npm imports exist in either render.
	for name, rendered := range map[string]string{"opencode": oc1, "mimocode": mc} {
		for _, forbidden := range []string{"eval(", "new Function(", "fetch(", "http:", "https:"} {
			if strings.Contains(rendered, forbidden) {
				t.Fatalf("%s bridge contains forbidden token %q", name, forbidden)
			}
		}
	}

	// Run node --check if node is available
	if nodeBin, err := exec.LookPath("node"); err == nil {
		tmpDir := t.TempDir()
		files := map[string]string{
			"opencode-bridge.mjs": oc1,
			"mimocode-bridge.mjs": mc,
		}
		for name, content := range files {
			p := filepath.Join(tmpDir, name)
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
			cmd := exec.Command(nodeBin, "--check", p)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("node --check failed for %s: %v\n%s", name, err, string(out))
			}
		}

		// Prove the exported shape matches each harness loader.
		probe := `const m = await import(process.env.BRIDGE_UNDER_TEST);
const d = m.default;
if (process.env.BRIDGE_EXPECT === "v2") {
  if (!d || typeof d !== "object" || d.id !== "sila-bridge" || typeof d.setup !== "function") {
    console.error("opencode default export is not a V2 definition");
    process.exit(2);
  }
} else if (typeof d !== "function") {
  console.error("mimocode default export is not a V1 plugin function");
  process.exit(3);
}
`
		for name, expect := range map[string]string{
			"opencode-bridge.mjs": "v2",
			"mimocode-bridge.mjs": "v1",
		} {
			cmd := exec.Command(nodeBin, "--input-type=module", "-e", probe)
			cmd.Env = append(os.Environ(),
				"BRIDGE_UNDER_TEST="+filepath.Join(tmpDir, name),
				"BRIDGE_EXPECT="+expect,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("export shape probe failed for %s: %v\n%s", name, err, string(out))
			}
		}
	}
}
